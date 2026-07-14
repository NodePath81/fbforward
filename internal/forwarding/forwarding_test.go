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

type fakePolicy struct {
	mu       sync.Mutex
	decision Decision
	metas    []flow.Meta
}

func (p *fakePolicy) Decide(meta flow.Meta) Decision {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.metas = append(p.metas, meta)
	return p.decision
}

func (p *fakePolicy) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.metas)
}

type fakePicker struct {
	mu               sync.Mutex
	selected         Upstream
	err              error
	metas            []flow.Meta
	dialFailures     []Upstream
	dialSuccesses    []Upstream
	feedbackCooldown []time.Duration
}

func (p *fakePicker) Pick(meta flow.Meta) (Upstream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.metas = append(p.metas, meta)
	return p.selected, p.err
}

func (p *fakePicker) MarkDialFailure(selected Upstream, cooldown time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dialFailures = append(p.dialFailures, selected)
	p.feedbackCooldown = append(p.feedbackCooldown, cooldown)
}

func (p *fakePicker) ClearDialFailure(selected Upstream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dialSuccesses = append(p.dialSuccesses, selected)
}

func (p *fakePicker) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.metas)
}

type recordingObserver struct {
	mu         sync.Mutex
	opens      []flow.Meta
	updates    []flow.Counters
	closes     []flow.Summary
	rejections []flow.Rejection
}

type recordingBinder struct {
	mu     sync.Mutex
	ids    []flow.ID
	tuples []flow.BackendTuple
	err    error
}

func (b *recordingBinder) Bind(id flow.ID, tuple flow.BackendTuple) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ids = append(b.ids, id)
	b.tuples = append(b.tuples, tuple)
	return b.err
}

func (o *recordingObserver) Open(meta flow.Meta) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opens = append(o.opens, meta)
}

func (o *recordingObserver) Update(_ flow.ID, counters flow.Counters) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.updates = append(o.updates, counters)
}

func (o *recordingObserver) Close(summary flow.Summary) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closes = append(o.closes, summary)
}

func (o *recordingObserver) Reject(rejection flow.Rejection) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rejections = append(o.rejections, rejection)
}

func (o *recordingObserver) rejectionCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.rejections)
}

func (o *recordingObserver) firstRejection() flow.Rejection {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.rejections) == 0 {
		return flow.Rejection{}
	}
	return o.rejections[0]
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

func allowedPolicy() *fakePolicy {
	return &fakePolicy{decision: Decision{Allowed: true}}
}

func selectedUpstream() Upstream {
	return Upstream{Tag: "primary", Addr: netip.MustParseAddr("127.0.0.1")}
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

func TestTCPPolicyDenySkipsUpstreamSelection(t *testing.T) {
	policy := &fakePolicy{decision: Decision{
		Allowed:   false,
		RuleType:  "cidr",
		RuleValue: "10.0.0.0/8",
	}}
	picker := &fakePicker{selected: selectedUpstream()}
	observer := &recordingObserver{}
	conn := &stubConn{
		local:  stubAddr("127.0.0.1:9000"),
		remote: stubAddr("10.1.2.3:12345"),
	}
	listener := &TCPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		picker:   picker,
		policy:   policy,
		observer: observer,
		sem:      make(chan struct{}, 1),
	}
	listener.sem <- struct{}{}

	listener.handleConn(context.Background(), conn)

	if !conn.closed {
		t.Fatalf("expected denied TCP connection to close")
	}
	if picker.calls() != 0 {
		t.Fatalf("expected policy deny before upstream selection")
	}
	policy.mu.Lock()
	candidate := policy.metas[0]
	policy.mu.Unlock()
	if candidate.ID != "" || candidate.Upstream != "" || candidate.Protocol != flow.ProtocolTCP || candidate.StartedAt.IsZero() {
		t.Fatalf("unexpected candidate metadata: %+v", candidate)
	}
	if observer.rejectionCount() != 1 {
		t.Fatalf("expected one policy rejection, got %d", observer.rejectionCount())
	}
	if got := observer.firstRejection().Reason; got != "firewall_deny" {
		t.Fatalf("unexpected rejection reason %q", got)
	}
}

func TestTCPPickErrorClosesConnectionWithoutCreatingFlow(t *testing.T) {
	picker := &fakePicker{err: context.Canceled}
	observer := &recordingObserver{}
	conn := &stubConn{
		local:  stubAddr("127.0.0.1:9000"),
		remote: stubAddr("192.0.2.1:12345"),
	}
	listener := &TCPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		picker:   picker,
		policy:   allowedPolicy(),
		observer: observer,
		sem:      make(chan struct{}, 1),
	}
	listener.sem <- struct{}{}

	listener.handleConn(context.Background(), conn)

	if !conn.closed {
		t.Fatalf("expected picker error to close TCP connection")
	}
	if len(observer.opens) != 0 || len(observer.closes) != 0 {
		t.Fatalf("picker error must not create a Flow: opens=%d closes=%d", len(observer.opens), len(observer.closes))
	}
}

func TestTCPFlowBindsUpstreamSocketTuple(t *testing.T) {
	observer := &recordingObserver{}
	binder := &recordingBinder{}
	client := &stubConn{
		local:  stubAddr("127.0.0.1:9000"),
		remote: stubAddr("192.0.2.1:12345"),
	}
	upstream := &stubConn{
		local:  stubAddr("10.0.0.1:43122"),
		remote: stubAddr("192.0.2.10:443"),
	}
	conn := &tcpConn{
		client:       client,
		upstream:     upstream,
		upstreamTag:  "primary",
		listenPort:   9000,
		timeout:      time.Second,
		observer:     observer,
		binder:       binder,
		listenAddr:   "127.0.0.1:9000",
		route:        "web",
		upstreamAddr: "192.0.2.10:443",
		created:      time.Now().UTC(),
	}
	conn.start(context.Background())
	defer conn.closeWithReason("test")
	binder.mu.Lock()
	defer binder.mu.Unlock()
	if len(binder.ids) != 1 || len(binder.tuples) != 1 {
		t.Fatalf("expected one backend binding, ids=%d tuples=%d", len(binder.ids), len(binder.tuples))
	}
	if got := binder.tuples[0].BackendKey; got != "primary@192.0.2.10:443" {
		t.Fatalf("unexpected backend key %q", got)
	}
	observer.mu.Lock()
	if len(observer.opens) != 1 || observer.opens[0].Route != "web" {
		t.Fatalf("unexpected TCP route metadata: %+v", observer.opens)
	}
	observer.mu.Unlock()
}

func TestUDPMappingBindsUpstreamSocketTuple(t *testing.T) {
	observer := &recordingObserver{}
	binder := &recordingBinder{}
	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindAddr: "127.0.0.1", BindPort: freeTCPPort(t), Route: "web"},
		picker:   &fakePicker{selected: selectedUpstream()},
		observer: observer,
		binder:   binder,
		sem:      make(chan struct{}, 1),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
	}
	listener.sem <- struct{}{}
	clientAddr := &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 12345}
	candidate, err := newCandidateMeta(flow.ProtocolUDP, clientAddr.String(), listener.listenAddr(), listener.cfg.Route)
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := listener.buildMapping(clientAddr, candidate)
	if err != nil {
		t.Fatal(err)
	}
	defer mapping.closeWithReason("test")
	binder.mu.Lock()
	defer binder.mu.Unlock()
	if len(binder.tuples) != 1 || binder.tuples[0].Protocol != flow.ProtocolUDP {
		t.Fatalf("expected one UDP binding, got %+v", binder.tuples)
	}
	if binder.tuples[0].LocalAddr.Port() == 0 || binder.tuples[0].RemoteAddr.Port() == 0 {
		t.Fatalf("expected concrete UDP socket tuple: %+v", binder.tuples[0])
	}
	observer.mu.Lock()
	if len(observer.opens) != 1 || observer.opens[0].Route != "web" {
		t.Fatalf("unexpected UDP route metadata: %+v", observer.opens)
	}
	observer.mu.Unlock()
}

func TestCandidateMetaCarriesRoute(t *testing.T) {
	candidate, err := newCandidateMeta(flow.ProtocolTCP, "127.0.0.1:40000", "127.0.0.1:5201", "iperf3")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Route != "iperf3" {
		t.Fatalf("expected candidate route iperf3, got %q", candidate.Route)
	}
}

func TestTCPConnectionLimitEmitsRejection(t *testing.T) {
	observer := &recordingObserver{}
	port := freeTCPPort(t)
	listener := NewTCPListener(
		config.ListenerConfig{BindAddr: "127.0.0.1", BindPort: port},
		config.ForwardingLimitsConfig{MaxTCPConnections: 0},
		time.Second,
		&fakePicker{selected: selectedUpstream()},
		allowedPolicy(),
		observer,
		nil,
		nil,
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
	for time.Now().Before(deadline) && observer.rejectionCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	_ = listener.Close()
	cancel()
	wg.Wait()

	if observer.rejectionCount() != 1 {
		t.Fatalf("expected one rejection, got %d", observer.rejectionCount())
	}
	if got := observer.firstRejection().Reason; got != "tcp_connection_limit" {
		t.Fatalf("expected tcp_connection_limit, got %q", got)
	}
}

func TestUDPPolicyDenySkipsMappingCreation(t *testing.T) {
	policy := &fakePolicy{decision: Decision{Allowed: false, RuleType: "cidr", RuleValue: "10.0.0.0/8"}}
	picker := &fakePicker{selected: selectedUpstream()}
	observer := &recordingObserver{}
	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		picker:   picker,
		policy:   policy,
		observer: observer,
		sem:      make(chan struct{}, 1),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: udpMaxMappingsPerIP,
	}

	listener.handlePacket(context.Background(), &net.UDPAddr{IP: net.ParseIP("10.1.2.3"), Port: 12345}, []byte("payload"))

	if picker.calls() != 0 {
		t.Fatalf("expected policy deny before upstream selection")
	}
	if len(listener.mappings) != 0 || len(listener.pending) != 0 {
		t.Fatalf("expected denied UDP packet to avoid mapping creation")
	}
	if observer.rejectionCount() != 1 {
		t.Fatalf("expected one policy rejection, got %d", observer.rejectionCount())
	}
}

func TestUDPPerIPLimitEmitsRejection(t *testing.T) {
	observer := &recordingObserver{}
	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		picker:   &fakePicker{selected: selectedUpstream()},
		policy:   allowedPolicy(),
		observer: observer,
		sem:      make(chan struct{}, 1),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: 0,
	}

	listener.handlePacket(context.Background(), &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 12345}, []byte("payload"))

	if observer.rejectionCount() != 1 || observer.firstRejection().Reason != "udp_per_ip_mapping_limit" {
		t.Fatalf("unexpected rejection records: %+v", observer.rejections)
	}
}

func TestUDPMappingLimitEmitsRejection(t *testing.T) {
	observer := &recordingObserver{}
	listener := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
		picker:   &fakePicker{selected: selectedUpstream()},
		policy:   allowedPolicy(),
		observer: observer,
		sem:      make(chan struct{}),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: udpMaxMappingsPerIP,
	}

	listener.handlePacket(context.Background(), &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 12345}, []byte("payload"))

	if observer.rejectionCount() != 1 || observer.firstRejection().Reason != "udp_mapping_limit" {
		t.Fatalf("unexpected rejection records: %+v", observer.rejections)
	}
}

func TestTCPCloseWithReasonEmitsOneSummary(t *testing.T) {
	observer := &recordingObserver{}
	client, peer1 := net.Pipe()
	upstreamConn, peer2 := net.Pipe()
	defer peer1.Close()
	defer peer2.Close()

	conn := &tcpConn{
		client:      client,
		upstream:    upstreamConn,
		upstreamTag: "primary",
		listenPort:  9000,
		observer:    observer,
		done:        make(chan struct{}),
		created:     time.Now().UTC().Add(-time.Second),
		clientAddr:  "1.1.1.1:12345",
		clientIP:    "1.1.1.1",
		listenAddr:  ":9000",
	}
	conn.id, _ = flow.NewID()
	conn.lifecycle = flow.NewLifecycle(flow.Meta{
		ID:         conn.id,
		Protocol:   flow.ProtocolTCP,
		ClientAddr: netip.MustParseAddrPort(conn.clientAddr),
		Listener:   conn.listenAddr,
		Upstream:   conn.upstreamTag,
		StartedAt:  conn.created,
	}, conn.observer, nil, conn.close)
	conn.lifecycle.Open()

	conn.closeWithReason("test")
	conn.closeWithReason("duplicate")

	if len(observer.closes) != 1 || observer.closes[0].CloseReason != "test" {
		t.Fatalf("unexpected TCP summaries: %+v", observer.closes)
	}
}

func TestTCPRegistryBlockClosesWithBackendReason(t *testing.T) {
	observer := &recordingObserver{}
	client, peer1 := net.Pipe()
	upstreamConn, peer2 := net.Pipe()
	defer peer1.Close()
	defer peer2.Close()

	registry := flow.NewRegistry()
	conn := &tcpConn{
		client: client, upstream: upstreamConn, upstreamTag: "primary", observer: observer,
		registry: registry, done: make(chan struct{}), created: time.Now().UTC(),
		clientAddr: "1.1.1.1:12345", clientIP: "1.1.1.1", listenAddr: ":9000",
	}
	conn.id, _ = flow.NewID()
	conn.lifecycle = flow.NewLifecycle(flow.Meta{
		ID: conn.id, Protocol: flow.ProtocolTCP, ClientAddr: netip.MustParseAddrPort(conn.clientAddr),
		Listener: conn.listenAddr, Upstream: conn.upstreamTag, StartedAt: conn.created,
	}, conn.observer, registry, conn.close)
	conn.lifecycle.Open()
	registry.SetControls(conn.id, flow.Controls{Block: func() { conn.closeWithReason("backend_blocked") }})

	if !registry.Block(conn.id) {
		t.Fatal("expected registry block to dispatch")
	}
	if registry.Block(conn.id) {
		t.Fatal("expected a closed flow to reject a second block")
	}
	if len(observer.closes) != 1 || observer.closes[0].CloseReason != "backend_blocked" {
		t.Fatalf("unexpected TCP block summary: %+v", observer.closes)
	}
}

func TestUDPCloseWithReasonEmitsOneSummary(t *testing.T) {
	observer := &recordingObserver{}
	upstreamConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP error: %v", err)
	}

	parent := &UDPListener{
		cfg:      config.ListenerConfig{BindPort: 9000},
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
		observer:      observer,
		done:          make(chan struct{}),
		created:       time.Now().UTC().Add(-time.Second),
		listenAddr:    ":9000",
	}
	mapping.id, _ = flow.NewID()
	mapping.lifecycle = flow.NewLifecycle(flow.Meta{
		ID:         mapping.id,
		Protocol:   flow.ProtocolUDP,
		ClientAddr: netip.MustParseAddrPort(mapping.clientAddrStr),
		Listener:   mapping.listenAddr,
		Upstream:   mapping.upstreamTag,
		StartedAt:  mapping.created,
	}, mapping.observer, nil, mapping.close)
	mapping.lifecycle.Open()
	parent.mappings[mapping.clientAddrStr] = mapping

	mapping.closeWithReason("test")
	mapping.closeWithReason("duplicate")

	if len(observer.closes) != 1 || observer.closes[0].CloseReason != "test" {
		t.Fatalf("unexpected UDP summaries: %+v", observer.closes)
	}
	if len(parent.mappings) != 0 {
		t.Fatalf("expected mapping removal on close")
	}
}

func TestUDPRegistryBlockClosesWithBackendReason(t *testing.T) {
	observer := &recordingObserver{}
	upstreamConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP error: %v", err)
	}
	defer upstreamConn.Close()

	parent := &UDPListener{
		sem: make(chan struct{}, 1), mappings: make(map[string]*udpMapping),
		ipCounts: map[string]int{"1.1.1.1": 1},
	}
	parent.sem <- struct{}{}
	registry := flow.NewRegistry()
	clientAddr := &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 12345}
	mapping := &udpMapping{
		parent: parent, clientAddr: clientAddr, clientAddrStr: clientAddr.String(), clientIP: "1.1.1.1",
		upstreamTag: "primary", upstreamConn: upstreamConn, observer: observer, registry: registry,
		done: make(chan struct{}), created: time.Now().UTC(), listenAddr: ":9000",
	}
	mapping.id, _ = flow.NewID()
	mapping.lifecycle = flow.NewLifecycle(flow.Meta{
		ID: mapping.id, Protocol: flow.ProtocolUDP, ClientAddr: netip.MustParseAddrPort(mapping.clientAddrStr),
		Listener: mapping.listenAddr, Upstream: mapping.upstreamTag, StartedAt: mapping.created,
	}, mapping.observer, registry, mapping.close)
	mapping.lifecycle.Open()
	parent.mappings[mapping.clientAddrStr] = mapping
	registry.SetControls(mapping.id, flow.Controls{Block: func() { mapping.closeWithReason("backend_blocked") }})

	if !registry.Block(mapping.id) {
		t.Fatal("expected registry block to dispatch")
	}
	if registry.Block(mapping.id) {
		t.Fatal("expected a closed mapping to reject a second block")
	}
	if len(observer.closes) != 1 || observer.closes[0].CloseReason != "backend_blocked" {
		t.Fatalf("unexpected UDP block summary: %+v", observer.closes)
	}
}
