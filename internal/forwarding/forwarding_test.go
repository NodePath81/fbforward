package forwarding

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/control"
	"github.com/NodePath81/fbforward/internal/firewall"
	"github.com/NodePath81/fbforward/internal/iplog"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
)

type testSelector struct {
	mu          sync.Mutex
	selectCalls int
}

func (s *testSelector) SelectUpstream() (*upstream.Upstream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectCalls++
	return nil, nil
}

func (s *testSelector) MarkDialFailure(string, time.Duration) {}
func (s *testSelector) ClearDialFailure(string)               {}

func (s *testSelector) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectCalls
}

type stubAddr string

func (a stubAddr) Network() string { return "tcp" }
func (a stubAddr) String() string  { return string(a) }

type stubConn struct {
	local  net.Addr
	remote net.Addr
	closed bool
}

func (c *stubConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *stubConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *stubConn) Close() error                     { c.closed = true; return nil }
func (c *stubConn) LocalAddr() net.Addr              { return c.local }
func (c *stubConn) RemoteAddr() net.Addr             { return c.remote }
func (c *stubConn) SetDeadline(time.Time) error      { return nil }
func (c *stubConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubConn) SetWriteDeadline(time.Time) error { return nil }

type recordingWriter struct {
	mu      sync.Mutex
	records []iplog.EnrichedRecord
}

func (w *recordingWriter) InsertBatch(records []iplog.EnrichedRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.records = append(w.records, records...)
	return nil
}

func (w *recordingWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.records)
}

func newStatusStore(t *testing.T) (*control.StatusStore, chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	store := control.NewStatusStore(control.NewStatusHub(done, nil), metrics.NewMetrics(nil))
	return store, done
}

func newPipeline(t *testing.T, writer *recordingWriter) *iplog.Pipeline {
	t.Helper()
	p := iplog.NewPipeline(config.IPLogConfig{
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      1,
		FlushInterval:  config.Duration(time.Hour),
	}, nil, writer, metrics.NewMetrics(nil), nil)
	p.Start()
	return p
}

func TestTCPFirewallDenySkipsUpstreamSelection(t *testing.T) {
	selector := &testSelector{}
	engine, err := firewall.NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules: []config.FirewallRule{{
			Action: "deny",
			CIDR:   "10.0.0.0/8",
		}},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}

	conn := &stubConn{
		local:  stubAddr("127.0.0.1:9000"),
		remote: stubAddr("10.1.2.3:12345"),
	}
	listener := &TCPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		manager:  selector,
		firewall: engine,
		sem:      make(chan struct{}, 1),
	}
	listener.sem <- struct{}{}

	listener.handleConn(context.Background(), conn)

	if !conn.closed {
		t.Fatalf("expected denied TCP connection to close")
	}
	if selector.calls() != 0 {
		t.Fatalf("expected firewall deny before upstream selection")
	}
}

func TestUDPFirewallDenySkipsMappingCreation(t *testing.T) {
	selector := &testSelector{}
	engine, err := firewall.NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules: []config.FirewallRule{{
			Action: "deny",
			CIDR:   "10.0.0.0/8",
		}},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}

	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		manager:  selector,
		firewall: engine,
		sem:      make(chan struct{}, 1),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: udpMaxMappingsPerIP,
	}

	listener.handlePacket(context.Background(), &net.UDPAddr{IP: net.ParseIP("10.1.2.3"), Port: 12345}, []byte("payload"))

	if selector.calls() != 0 {
		t.Fatalf("expected firewall deny before upstream selection")
	}
	if len(listener.mappings) != 0 || len(listener.pending) != 0 {
		t.Fatalf("expected denied UDP packet to avoid mapping creation")
	}
}

func TestTCPCloseWithReasonEmitsOneIPLogRecord(t *testing.T) {
	status, done := newStatusStore(t)
	defer close(done)
	writer := &recordingWriter{}
	pipeline := newPipeline(t, writer)

	client, peer1 := net.Pipe()
	upstreamConn, peer2 := net.Pipe()
	defer peer1.Close()
	defer peer2.Close()

	conn := &tcpConn{
		client:      client,
		upstream:    upstreamConn,
		upstreamTag: "primary",
		listenPort:  9000,
		status:      status,
		pipeline:    pipeline,
		done:        make(chan struct{}),
		created:     time.Now().Add(-time.Second),
		clientAddr:  "1.1.1.1:12345",
		clientIP:    "1.1.1.1",
	}
	conn.id = status.AddTCP(conn.clientAddr, conn.upstreamTag, conn.listenPort, nil)

	conn.closeWithReason("test")
	conn.closeWithReason("test")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if writer.count() != 1 {
		t.Fatalf("expected one TCP iplog record, got %d", writer.count())
	}
}

func TestUDPCloseWithReasonEmitsOneIPLogRecord(t *testing.T) {
	status, done := newStatusStore(t)
	defer close(done)
	writer := &recordingWriter{}
	pipeline := newPipeline(t, writer)

	upstreamConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP error: %v", err)
	}

	parent := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		status:   status,
		sem:      make(chan struct{}, 1),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: map[string]int{"1.1.1.1": 1},
	}
	parent.sem <- struct{}{}

	clientAddr := &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 12345}
	mapping := &udpMapping{
		parent:        parent,
		clientAddr:    clientAddr,
		clientAddrStr: clientAddr.String(),
		clientIP:      "1.1.1.1",
		upstreamTag:   "primary",
		upstreamConn:  upstreamConn,
		status:        status,
		pipeline:      pipeline,
		done:          make(chan struct{}),
		created:       time.Now().Add(-time.Second),
	}
	mapping.id = status.AddUDP(mapping.clientAddrStr, mapping.upstreamTag, parent.cfg.BindPort, nil)
	parent.mappings[mapping.clientAddrStr] = mapping

	mapping.closeWithReason("test")
	mapping.closeWithReason("test")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if writer.count() != 1 {
		t.Fatalf("expected one UDP iplog record, got %d", writer.count())
	}
	if len(parent.mappings) != 0 {
		t.Fatalf("expected mapping removal on close")
	}
}
