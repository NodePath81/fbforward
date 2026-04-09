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
	"github.com/NodePath81/fbforward/internal/util"
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
	mu               sync.Mutex
	records          []iplog.EnrichedRecord
	rejectionRecords []iplog.EnrichedRejectionRecord
}

func (w *recordingWriter) InsertBatch(records []iplog.EnrichedRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.records = append(w.records, records...)
	return nil
}

func (w *recordingWriter) InsertRejectionBatch(records []iplog.EnrichedRejectionRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rejectionRecords = append(w.rejectionRecords, records...)
	return nil
}

func (w *recordingWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.records)
}

func (w *recordingWriter) rejectionCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.rejectionRecords)
}

func (w *recordingWriter) firstRejection() iplog.EnrichedRejectionRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.rejectionRecords) == 0 {
		return iplog.EnrichedRejectionRecord{}
	}
	return w.rejectionRecords[0]
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
		Enabled:        true,
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      1,
		FlushInterval:  config.Duration(time.Hour),
	}, nil, writer, metrics.NewMetrics(nil), nil)
	p.Start()
	return p
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
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

func TestTCPFirewallDenyEmitsRejectionRecord(t *testing.T) {
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

	writer := &recordingWriter{}
	pipeline := newPipeline(t, writer)

	conn := &stubConn{
		local:  stubAddr("127.0.0.1:9000"),
		remote: stubAddr("10.1.2.3:12345"),
	}
	listener := &TCPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		manager:  selector,
		firewall: engine,
		pipeline: pipeline,
		sem:      make(chan struct{}, 1),
	}
	listener.sem <- struct{}{}

	listener.handleConn(context.Background(), conn)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if writer.rejectionCount() != 1 {
		t.Fatalf("expected one rejection record, got %d", writer.rejectionCount())
	}
	record := writer.firstRejection()
	if record.Reason != "firewall_deny" || record.MatchedRuleType != "cidr" || record.MatchedRuleValue != "10.0.0.0/8" {
		t.Fatalf("unexpected rejection record: %+v", record)
	}
}

func TestTCPConnectionLimitEmitsRejectionRecord(t *testing.T) {
	writer := &recordingWriter{}
	pipeline := newPipeline(t, writer)

	port := freeTCPPort(t)
	listener := NewTCPListener(
		config.ListenerConfig{BindAddr: "127.0.0.1", BindPort: port},
		config.ForwardingLimitsConfig{MaxTCPConnections: 0},
		time.Second,
		&testSelector{},
		nil,
		nil,
		nil,
		pipeline,
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	if err := listener.Start(ctx, &wg); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", util.FormatPort(port)))
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	_ = conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if writer.rejectionCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = listener.Close()
	cancel()
	wg.Wait()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := pipeline.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}

	if writer.rejectionCount() != 1 {
		t.Fatalf("expected one rejection record, got %d", writer.rejectionCount())
	}
	if got := writer.firstRejection().Reason; got != "tcp_connection_limit" {
		t.Fatalf("expected tcp_connection_limit rejection, got %+v", writer.firstRejection())
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

func TestUDPFirewallDenyEmitsRejectionRecord(t *testing.T) {
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

	writer := &recordingWriter{}
	pipeline := newPipeline(t, writer)
	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		firewall: engine,
		pipeline: pipeline,
		sem:      make(chan struct{}, 1),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: udpMaxMappingsPerIP,
	}

	listener.handlePacket(context.Background(), &net.UDPAddr{IP: net.ParseIP("10.1.2.3"), Port: 12345}, []byte("payload"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if writer.rejectionCount() != 1 {
		t.Fatalf("expected one rejection record, got %d", writer.rejectionCount())
	}
	if got := writer.firstRejection().Reason; got != "firewall_deny" {
		t.Fatalf("unexpected rejection record: %+v", writer.firstRejection())
	}
}

func TestUDPPerIPLimitEmitsRejectionRecord(t *testing.T) {
	writer := &recordingWriter{}
	pipeline := newPipeline(t, writer)

	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		pipeline: pipeline,
		sem:      make(chan struct{}, 1),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: 0,
	}

	listener.handlePacket(context.Background(), &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 12345}, []byte("payload"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if writer.rejectionCount() != 1 || writer.firstRejection().Reason != "udp_per_ip_mapping_limit" {
		t.Fatalf("unexpected rejection records: %+v", writer.rejectionRecords)
	}
}

func TestUDPMappingLimitEmitsRejectionRecord(t *testing.T) {
	writer := &recordingWriter{}
	pipeline := newPipeline(t, writer)

	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		pipeline: pipeline,
		sem:      make(chan struct{}),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: udpMaxMappingsPerIP,
	}

	listener.handlePacket(context.Background(), &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 12345}, []byte("payload"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if writer.rejectionCount() != 1 || writer.firstRejection().Reason != "udp_mapping_limit" {
		t.Fatalf("unexpected rejection records: %+v", writer.rejectionRecords)
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
