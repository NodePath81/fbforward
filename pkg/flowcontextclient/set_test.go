package flowcontextclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func flowEnvelope(id string) string {
	return `{"ok":true,"flow":{"flow_id":"` + id + `","protocol":"tcp","client_addr":"203.0.113.40:51234","listener":"0.0.0.0:443","route":"web","upstream":"primary","state":"active"}}`
}

func instanceOptions(name string, source netip.Addr, endpoint string) InstanceOptions {
	return InstanceOptions{
		Name:       name,
		SourceAddr: source,
		Client:     Options{Endpoint: endpoint, Token: name + "-token", BackendKey: "primary@192.0.2.10:9000"},
	}
}

func testConn(source, target string) net.Conn {
	return &stubConn{
		local:  mustTCPAddr(target),
		remote: mustTCPAddr(source),
	}
}

type stubConn struct {
	local  net.Addr
	remote net.Addr
}

func (c *stubConn) Read([]byte) (int, error)           { return 0, net.ErrClosed }
func (c *stubConn) Write(value []byte) (int, error)    { return len(value), nil }
func (c *stubConn) Close() error                       { return nil }
func (c *stubConn) LocalAddr() net.Addr                { return c.local }
func (c *stubConn) RemoteAddr() net.Addr               { return c.remote }
func (c *stubConn) SetDeadline(_ time.Time) error      { return nil }
func (c *stubConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *stubConn) SetWriteDeadline(_ time.Time) error { return nil }

func mustTCPAddr(value string) *net.TCPAddr {
	parsed := netip.MustParseAddrPort(value)
	return &net.TCPAddr{IP: parsed.Addr().AsSlice(), Port: int(parsed.Port())}
}

func TestClientSetSelectsInstanceBySourceAddress(t *testing.T) {
	var aCalls, bCalls atomic.Int32
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aCalls.Add(1)
		_, _ = w.Write([]byte(flowEnvelope("flow-a")))
	}))
	t.Cleanup(serverA.Close)
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bCalls.Add(1)
		_, _ = w.Write([]byte(flowEnvelope("flow-b")))
	}))
	t.Cleanup(serverB.Close)
	set, err := NewClientSet([]InstanceOptions{
		instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), serverA.URL),
		instanceOptions("edge-b", netip.MustParseAddr("127.0.0.3"), serverB.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	flowA, err := set.ResolveConn(context.Background(), testConn("127.0.0.2:52000", "192.0.2.10:9000"))
	if err != nil {
		t.Fatal(err)
	}
	flowB, err := set.ResolveConn(context.Background(), testConn("127.0.0.3:52001", "192.0.2.10:9000"))
	if err != nil {
		t.Fatal(err)
	}
	if flowA.Instance != "edge-a" || flowA.ID != "flow-a" || flowB.Instance != "edge-b" || flowB.ID != "flow-b" {
		t.Fatalf("unexpected flows: A=%+v B=%+v", flowA, flowB)
	}
	if aCalls.Load() != 1 || bCalls.Load() != 1 {
		t.Fatalf("calls A=%d B=%d, want one each", aCalls.Load(), bCalls.Load())
	}
}

func TestClientSetHasSourceBoundaries(t *testing.T) {
	set, err := NewClientSet([]InstanceOptions{instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), "http://127.0.0.1:1")})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		set  *ClientSet
		addr netip.Addr
		want bool
	}{
		{name: "known", set: set, addr: netip.MustParseAddr("127.0.0.2"), want: true},
		{name: "unknown", set: set, addr: netip.MustParseAddr("127.0.0.9")},
		{name: "invalid", set: set},
		{name: "nil set", addr: netip.MustParseAddr("127.0.0.2")},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.set.HasSource(testCase.addr); got != testCase.want {
				t.Fatalf("HasSource(%v) = %v, want %v", testCase.addr, got, testCase.want)
			}
		})
	}
}

func TestClientSetResolveConnUsesBackendTuplePerspective(t *testing.T) {
	var request resolveRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(flowEnvelope("set-tcp-flow")))
	}))
	t.Cleanup(server.Close)
	set, err := NewClientSet([]InstanceOptions{instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.ResolveConn(context.Background(), testConn("127.0.0.2:52000", "192.0.2.10:9000")); err != nil {
		t.Fatal(err)
	}
	want := resolveRequest{
		Protocol:   "tcp",
		BackendKey: "primary@192.0.2.10:9000",
		LocalAddr:  "127.0.0.2:52000",
		RemoteAddr: "192.0.2.10:9000",
		WaitMS:     100,
	}
	if request != want {
		t.Fatalf("request=%+v, want %+v", request, want)
	}
}

func TestClientSetUDPTagStaysOnResolvedInstance(t *testing.T) {
	var aTags, bTags atomic.Int32
	newServer := func(counter *atomic.Int32, id string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == rpcPath {
				counter.Add(1)
				var request rpcRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if request.Method != "SetFlowTag" {
					t.Fatalf("method=%q, want SetFlowTag", request.Method)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"flow":{"flow_id":"` + id + `","protocol":"udp","client_addr":"203.0.113.40:51234","listener":"0.0.0.0:443","route":"udp","upstream":"primary","state":"active"}}`))
		}))
	}
	serverA := newServer(&aTags, "udp-a")
	t.Cleanup(serverA.Close)
	serverB := newServer(&bTags, "udp-b")
	t.Cleanup(serverB.Close)
	set, err := NewClientSet([]InstanceOptions{
		instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), serverA.URL),
		instanceOptions("edge-b", netip.MustParseAddr("127.0.0.3"), serverB.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := set.ResolveBackendTuple(context.Background(), "udp", netip.MustParseAddrPort("127.0.0.3:53000"), netip.MustParseAddrPort("192.0.2.20:443"))
	if err != nil {
		t.Fatal(err)
	}
	if flow.ID != "udp-b" || flow.Instance != "edge-b" {
		t.Fatalf("unexpected flow: %+v", flow)
	}
	if err := flow.SetFlowTag(context.Background(), Tag{Namespace: "app", Key: "user", Value: "udp-user"}); err != nil {
		t.Fatal(err)
	}
	if aTags.Load() != 0 || bTags.Load() != 1 {
		t.Fatalf("tag calls A=%d B=%d, want 0 and 1", aTags.Load(), bTags.Load())
	}
}

func TestClientSetNilInputsReturnStableErrors(t *testing.T) {
	var nilSet *ClientSet
	if _, err := nilSet.ResolveBackendTuple(context.Background(), "udp", netip.MustParseAddrPort("127.0.0.2:53000"), netip.MustParseAddrPort("192.0.2.20:443")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil set tuple error=%v, want ErrInvalidRequest", err)
	}
	if _, err := nilSet.ResolveConn(context.Background(), testConn("127.0.0.2:52000", "192.0.2.10:9000")); !errors.Is(err, ErrUnknownInstance) {
		t.Fatalf("nil set conn error=%v, want ErrUnknownInstance", err)
	}
	set := &ClientSet{}
	if _, err := set.ResolveConn(context.Background(), nil); !errors.Is(err, ErrUnknownInstance) {
		t.Fatalf("nil conn error=%v, want ErrUnknownInstance", err)
	}
}

func TestClientSetResolveBackendTupleUsesUDPAndSourceSelection(t *testing.T) {
	var resolveCalls, tagCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == rpcPath {
			tagCalls.Add(1)
			var request rpcRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode tag request: %v", err)
			}
			if request.Method != "SetFlowTag" {
				t.Fatalf("tag method=%q, want SetFlowTag", request.Method)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		resolveCalls.Add(1)
		var request resolveRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		want := resolveRequest{
			Protocol:   "udp",
			BackendKey: "primary@192.0.2.10:9000",
			LocalAddr:  "127.0.0.2:53000",
			RemoteAddr: "192.0.2.20:443",
			WaitMS:     100,
		}
		if request != want {
			t.Fatalf("request=%+v, want %+v", request, want)
		}
		_, _ = w.Write([]byte(`{"ok":true,"flow":{"flow_id":"udp-flow","protocol":"udp","client_addr":"203.0.113.40:51234","listener":"0.0.0.0:443","route":"udp","upstream":"primary","state":"active"}}`))
	}))
	t.Cleanup(server.Close)
	set, err := NewClientSet([]InstanceOptions{instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	if !set.HasSource(netip.MustParseAddr("127.0.0.2")) || set.HasSource(netip.MustParseAddr("127.0.0.9")) {
		t.Fatal("HasSource returned unexpected result")
	}
	flow, err := set.ResolveBackendTuple(context.Background(), "udp", netip.MustParseAddrPort("127.0.0.2:53000"), netip.MustParseAddrPort("192.0.2.20:443"))
	if err != nil {
		t.Fatal(err)
	}
	if flow.ID != "udp-flow" || flow.Protocol != "udp" || flow.Instance != "edge-a" {
		t.Fatalf("unexpected flow: %+v", flow)
	}
	if err := flow.SetFlowTag(context.Background(), Tag{Namespace: "app", Key: "user", Value: "udp-user"}); err != nil {
		t.Fatalf("set UDP flow tag: %v", err)
	}
	if resolveCalls.Load() != 1 || tagCalls.Load() != 1 {
		t.Fatalf("resolve calls=%d tag calls=%d, want one each", resolveCalls.Load(), tagCalls.Load())
	}
}

func TestClientSetResolveBackendTupleRejectsInvalidInput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(server.Close)
	set, err := NewClientSet([]InstanceOptions{instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	validSource := netip.MustParseAddrPort("127.0.0.2:53000")
	validDestination := netip.MustParseAddrPort("192.0.2.20:443")
	tests := []struct {
		name     string
		protocol string
		source   netip.AddrPort
		dest     netip.AddrPort
	}{
		{name: "protocol", protocol: "icmp", source: validSource, dest: validDestination},
		{name: "source", protocol: "udp", source: netip.AddrPort{}, dest: validDestination},
		{name: "destination", protocol: "udp", source: validSource, dest: netip.AddrPort{}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := set.ResolveBackendTuple(context.Background(), testCase.protocol, testCase.source, testCase.dest)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error=%v, want ErrInvalidRequest", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests contacted endpoint %d times", calls.Load())
	}
}

func TestClientSetResolveBackendTupleUnknownSourceDoesNotCallEndpoint(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(server.Close)
	set, err := NewClientSet([]InstanceOptions{instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = set.ResolveBackendTuple(context.Background(), "udp", netip.MustParseAddrPort("127.0.0.9:53000"), netip.MustParseAddrPort("192.0.2.20:443"))
	if !errors.Is(err, ErrUnknownInstance) || calls.Load() != 0 {
		t.Fatalf("error=%v calls=%d, want unknown instance and no call", err, calls.Load())
	}
}

func TestClientSetRejectsAmbiguousConfiguration(t *testing.T) {
	validSourceA := netip.MustParseAddr("127.0.0.2")
	validSourceB := netip.MustParseAddr("127.0.0.3")
	valid := func(name string, source netip.Addr) InstanceOptions {
		return instanceOptions(name, source, "http://127.0.0.1:1")
	}
	tests := []struct {
		name      string
		instances []InstanceOptions
	}{
		{"empty name", []InstanceOptions{valid("", validSourceA)}},
		{"duplicate name", []InstanceOptions{valid("edge", validSourceA), valid("edge", validSourceB)}},
		{"duplicate source", []InstanceOptions{valid("edge-a", validSourceA), valid("edge-b", validSourceA)}},
		{"invalid source", []InstanceOptions{valid("edge", netip.Addr{})}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewClientSet(testCase.instances); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error=%v, want invalid request", err)
			}
		})
	}

	var calls atomic.Int32
	set, err := NewClientSet([]InstanceOptions{{
		Name:       "edge",
		SourceAddr: validSourceA,
		Client: Options{Endpoint: "http://127.0.0.1:1", Token: "token", BackendKey: "backend", HTTPClient: httpDoerFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("must not be called")
		})},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = set.ResolveConn(context.Background(), testConn("127.0.0.9:52000", "192.0.2.10:9000"))
	if !errors.Is(err, ErrUnknownInstance) || calls.Load() != 0 {
		t.Fatalf("unknown source error=%v calls=%d", err, calls.Load())
	}
}

func TestResolvedFlowTagsOriginalInstance(t *testing.T) {
	var aTags, bTags atomic.Int32
	newServer := func(counter *atomic.Int32, id string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == rpcPath {
				counter.Add(1)
				var request rpcRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if request.Method != "SetFlowTag" {
					t.Fatalf("method=%q", request.Method)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			_, _ = w.Write([]byte(flowEnvelope(id)))
		}))
	}
	serverA := newServer(&aTags, "flow-a")
	t.Cleanup(serverA.Close)
	serverB := newServer(&bTags, "flow-b")
	t.Cleanup(serverB.Close)
	set, err := NewClientSet([]InstanceOptions{
		instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), serverA.URL),
		instanceOptions("edge-b", netip.MustParseAddr("127.0.0.3"), serverB.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := set.ResolveConn(context.Background(), testConn("127.0.0.2:52000", "192.0.2.10:9000"))
	if err != nil {
		t.Fatal(err)
	}
	if err := flow.SetFlowTag(context.Background(), Tag{Namespace: "app", Key: "user", Value: "user-1"}); err != nil {
		t.Fatal(err)
	}
	if aTags.Load() != 1 || bTags.Load() != 0 {
		t.Fatalf("tag calls A=%d B=%d", aTags.Load(), bTags.Load())
	}
}

func TestResolvedFlowControlZeroValueRejects(t *testing.T) {
	var resolved ResolvedFlow
	if err := resolved.SetLimit(context.Background(), 1000); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SetLimit error=%v", err)
	}
	if err := resolved.ClearLimit(context.Background()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ClearLimit error=%v", err)
	}
	if err := resolved.Block(context.Background(), "test"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Block error=%v", err)
	}
}
