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
