package forwarding

import (
	"context"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/util"
)

func TestTCPHalfClosePreservesReverseResponse(t *testing.T) {
	clientConn, clientPeer := tcpConnPair(t)
	upstreamConn, upstreamPeer := tcpConnPair(t)
	observer := &recordingObserver{}
	conn, done := startTCPTransport(t, context.Background(), clientConn, upstreamConn, observer, nil, time.Second)

	request := []byte("request before client half-close")
	response := []byte("response after upstream sees eof")
	setTCPTestDeadline(t, clientPeer)
	setTCPTestDeadline(t, upstreamPeer)
	if _, err := clientPeer.Write(request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := clientPeer.CloseWrite(); err != nil {
		t.Fatalf("half-close client upload: %v", err)
	}
	gotRequest, err := io.ReadAll(upstreamPeer)
	if err != nil {
		t.Fatalf("read request through propagated half-close: %v", err)
	}
	if string(gotRequest) != string(request) {
		t.Fatalf("request = %q, want %q", gotRequest, request)
	}

	if _, err := upstreamPeer.Write(response); err != nil {
		t.Fatalf("write response after upload eof: %v", err)
	}
	if err := upstreamPeer.CloseWrite(); err != nil {
		t.Fatalf("half-close upstream response: %v", err)
	}
	gotResponse, err := io.ReadAll(clientPeer)
	if err != nil {
		t.Fatalf("read response through propagated half-close: %v", err)
	}
	if string(gotResponse) != string(response) {
		t.Fatalf("response = %q, want %q", gotResponse, response)
	}
	waitTCPTransport(t, done)

	summary := onlyTCPSummary(t, observer)
	if summary.CloseReason != "eof" {
		t.Fatalf("close reason = %q, want eof", summary.CloseReason)
	}
	if summary.BytesUp != uint64(len(request)) || summary.BytesDown != uint64(len(response)) {
		t.Fatalf("flow bytes = (%d, %d), want (%d, %d)", summary.BytesUp, summary.BytesDown, len(request), len(response))
	}
	if conn.isClosed() == false {
		t.Fatal("transport did not enter closed state")
	}
}

func TestTCPTransportIdleAndContextCloseReasons(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		clientConn, clientPeer := tcpConnPair(t)
		upstreamConn, upstreamPeer := tcpConnPair(t)
		defer clientPeer.Close()
		defer upstreamPeer.Close()
		observer := &recordingObserver{}
		_, done := startTCPTransport(t, context.Background(), clientConn, upstreamConn, observer, nil, 40*time.Millisecond)
		waitTCPTransport(t, done)
		if got := onlyTCPSummary(t, observer).CloseReason; got != "idle_timeout" {
			t.Fatalf("close reason = %q, want idle_timeout", got)
		}
	})

	t.Run("context", func(t *testing.T) {
		clientConn, clientPeer := tcpConnPair(t)
		upstreamConn, upstreamPeer := tcpConnPair(t)
		defer clientPeer.Close()
		defer upstreamPeer.Close()
		observer := &recordingObserver{}
		ctx, cancel := context.WithCancel(context.Background())
		_, done := startTCPTransport(t, ctx, clientConn, upstreamConn, observer, nil, time.Second)
		cancel()
		waitTCPTransport(t, done)
		if got := onlyTCPSummary(t, observer).CloseReason; got != "context_done" {
			t.Fatalf("close reason = %q, want context_done", got)
		}
	})
}

func TestTCPBlockCancelsLimitedTransport(t *testing.T) {
	clientConn, clientPeer := tcpConnPair(t)
	upstreamConn, upstreamPeer := tcpConnPair(t)
	defer clientPeer.Close()
	defer upstreamPeer.Close()
	observer := &recordingObserver{}
	registry := flow.NewRegistry()
	conn, done := startTCPTransport(t, context.Background(), clientConn, upstreamConn, observer, registry, time.Second)
	conn.rateLimiter.SetOverride(1)
	if !conn.rateLimiter.Try(minRateLimitBurst) {
		t.Fatal("consume initial limiter burst")
	}
	if _, err := clientPeer.Write([]byte("limited")); err != nil {
		t.Fatalf("write limited request: %v", err)
	}
	if !registry.Block(conn.id) {
		t.Fatal("block active TCP flow")
	}
	waitTCPTransport(t, done)
	if got := onlyTCPSummary(t, observer).CloseReason; got != "backend_blocked" {
		t.Fatalf("close reason = %q, want backend_blocked", got)
	}
}

func TestTCPCopyCloseReason(t *testing.T) {
	if got := tcpCopyCloseReason(tcpCopyResult{end: tcpCopyReadError}); got != "read_error" {
		t.Fatalf("read close reason = %q", got)
	}
	if got := tcpCopyCloseReason(tcpCopyResult{end: tcpCopyWriteError}); got != "write_error" {
		t.Fatalf("write close reason = %q", got)
	}
}

func TestTCPConnectionLimitTracksActiveFlow(t *testing.T) {
	fixture := startTCPListenerFixture(t, 1)
	firstClient, firstBackend := fixture.connect(t)

	secondClient, err := net.Dial("tcp", fixture.proxyAddr)
	if err != nil {
		t.Fatalf("dial second client: %v", err)
	}
	_ = secondClient.Close()
	waitTCPRejections(t, fixture.observer, 1)
	select {
	case backend := <-fixture.backends:
		backend.Close()
		t.Fatal("connection limit admitted a second active flow")
	case <-time.After(50 * time.Millisecond):
	}

	_ = firstClient.Close()
	_ = firstBackend.Close()
	waitTCPFlowCapacity(t, fixture.listener.sem, 0)

	thirdClient, thirdBackend := fixture.connect(t)
	_ = thirdClient.Close()
	_ = thirdBackend.Close()
}

func TestTCPListenerShutdownWaitsForActiveFlow(t *testing.T) {
	fixture := startTCPListenerFixture(t, 1)
	client, backend := fixture.connect(t)
	defer client.Close()
	defer backend.Close()

	fixture.cancel()
	_ = fixture.listener.Close()
	done := make(chan struct{})
	go func() {
		fixture.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("listener shutdown did not wait for active TCP handler")
	}
	waitTCPFlowCapacity(t, fixture.listener.sem, 0)
}

func startTCPTransport(t *testing.T, ctx context.Context, client, upstream *net.TCPConn, observer *recordingObserver, registry *flow.Registry, timeout time.Duration) (*tcpConn, <-chan struct{}) {
	t.Helper()
	conn := &tcpConn{
		client:       client,
		upstream:     upstream,
		upstreamTag:  "primary",
		listenPort:   9000,
		timeout:      timeout,
		observer:     observer,
		registry:     registry,
		rateLimiter:  newByteRateLimiter(0),
		upstreamIP:   "127.0.0.1",
		upstreamAddr: upstream.RemoteAddr().String(),
		listenAddr:   "127.0.0.1:9000",
		route:        "test",
		created:      time.Now().UTC(),
	}
	done := make(chan struct{})
	go func() {
		conn.start(ctx)
		close(done)
	}()
	waitTCPOpen(t, observer)
	return conn, done
}

func tcpConnPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen TCP pair: %v", err)
	}
	peer, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		listener.Close()
		t.Fatalf("dial TCP pair: %v", err)
	}
	proxy, err := listener.AcceptTCP()
	listener.Close()
	if err != nil {
		peer.Close()
		t.Fatalf("accept TCP pair: %v", err)
	}
	t.Cleanup(func() {
		_ = proxy.Close()
		_ = peer.Close()
	})
	return proxy, peer
}

func setTCPTestDeadline(t *testing.T, conn *net.TCPConn) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set TCP test deadline: %v", err)
	}
}

func waitTCPOpen(t *testing.T, observer *recordingObserver) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		observer.mu.Lock()
		opened := len(observer.opens) == 1
		observer.mu.Unlock()
		if opened {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("TCP flow did not open")
}

func waitTCPTransport(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("TCP transport did not stop")
	}
}

func onlyTCPSummary(t *testing.T, observer *recordingObserver) flow.Summary {
	t.Helper()
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.closes) != 1 {
		t.Fatalf("TCP close summaries = %d, want 1", len(observer.closes))
	}
	return observer.closes[0]
}

type tcpListenerFixture struct {
	listener   *TCPListener
	backend    *net.TCPListener
	backends   chan *net.TCPConn
	proxyAddr  string
	observer   *recordingObserver
	cancel     context.CancelFunc
	wg         *sync.WaitGroup
	acceptDone chan struct{}
	cleanupOne sync.Once
}

func startTCPListenerFixture(t *testing.T, maxConnections int) *tcpListenerFixture {
	t.Helper()
	backend, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2)})
	if err != nil {
		t.Fatalf("listen upstream fixture: %v", err)
	}
	port := backend.Addr().(*net.TCPAddr).Port
	observer := &recordingObserver{}
	listener := NewTCPListener(
		config.ListenerConfig{BindAddr: "127.0.0.1", BindPort: port, Route: "test"},
		config.ForwardingLimitsConfig{MaxTCPConnections: maxConnections},
		time.Second,
		&fakePicker{selected: Upstream{Tag: "primary", Addr: netip.MustParseAddr("127.0.0.2")}},
		allowedPolicy(), observer, nil, nil, nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	if err := listener.Start(ctx, wg); err != nil {
		backend.Close()
		cancel()
		t.Fatalf("start TCP listener fixture: %v", err)
	}
	fixture := &tcpListenerFixture{
		listener:   listener,
		backend:    backend,
		backends:   make(chan *net.TCPConn, 4),
		proxyAddr:  net.JoinHostPort("127.0.0.1", util.FormatPort(port)),
		observer:   observer,
		cancel:     cancel,
		wg:         wg,
		acceptDone: make(chan struct{}),
	}
	go func() {
		defer close(fixture.acceptDone)
		for {
			conn, acceptErr := backend.AcceptTCP()
			if acceptErr != nil {
				return
			}
			select {
			case fixture.backends <- conn:
			case <-ctx.Done():
				_ = conn.Close()
				return
			}
		}
	}()
	t.Cleanup(func() { fixture.cleanup() })
	return fixture
}

func (f *tcpListenerFixture) connect(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	addr, err := net.ResolveTCPAddr("tcp", f.proxyAddr)
	if err != nil {
		t.Fatalf("resolve TCP listener fixture: %v", err)
	}
	client, err := net.DialTCP("tcp", nil, addr)
	if err != nil {
		t.Fatalf("dial TCP listener fixture: %v", err)
	}
	select {
	case backend := <-f.backends:
		return client, backend
	case <-time.After(2 * time.Second):
		client.Close()
		t.Fatal("TCP listener did not connect to backend")
		return nil, nil
	}
}

func (f *tcpListenerFixture) cleanup() {
	f.cleanupOne.Do(func() {
		f.cancel()
		_ = f.listener.Close()
		_ = f.backend.Close()
		f.wg.Wait()
		<-f.acceptDone
		close(f.backends)
		for conn := range f.backends {
			_ = conn.Close()
		}
	})
}

func waitTCPRejections(t *testing.T, observer *recordingObserver, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if observer.rejectionCount() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("TCP rejections = %d, want at least %d", observer.rejectionCount(), want)
}

func waitTCPFlowCapacity(t *testing.T, semaphore chan struct{}, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(semaphore) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("active TCP capacity = %d, want %d", len(semaphore), want)
}
