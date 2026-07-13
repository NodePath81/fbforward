package flowcontextclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testFlowEnvelope() string {
	return `{"ok":true,"flow":{"flow_id":"flow-1","protocol":"tcp","client_addr":"203.0.113.40:51234","listener":"0.0.0.0:443","route":"web","upstream":"primary","state":"active","new_field":"ignored"}}`
}

func newTestClient(t *testing.T, handler http.HandlerFunc, options ...func(*Options)) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	config := Options{Endpoint: server.URL, Token: "backend-token", BackendKey: "primary@192.0.2.10:9000"}
	for _, option := range options {
		option(&config)
	}
	client, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testTuple() Tuple {
	return Tuple{
		Protocol:   "tcp",
		BackendKey: "primary@192.0.2.10:9000",
		LocalAddr:  mustAddrPort("10.0.0.1:43122"),
		RemoteAddr: mustAddrPort("192.0.2.10:9000"),
	}
}

func mustAddrPort(value string) netip.AddrPort {
	return netip.MustParseAddrPort(value)
}

func TestResolveTuple(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != resolvePath || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer backend-token" {
			t.Fatalf("authorization=%q", got)
		}
		var request resolveRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request != (resolveRequest{Protocol: "tcp", BackendKey: "primary@192.0.2.10:9000", LocalAddr: "10.0.0.1:43122", RemoteAddr: "192.0.2.10:9000", WaitMS: 100}) {
			t.Fatalf("unexpected resolve request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(testFlowEnvelope()))
	}, func(options *Options) { options.ResolveWait = 100 * time.Millisecond })
	flow, err := client.ResolveTuple(context.Background(), testTuple())
	if err != nil {
		t.Fatal(err)
	}
	if flow.ID != "flow-1" || flow.ClientAddr.String() != "203.0.113.40:51234" || flow.Route != "web" {
		t.Fatalf("unexpected flow: %+v", flow)
	}
}

func TestResolveConnUsesBackendSocketPerspective(t *testing.T) {
	var request resolveRequest
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(testFlowEnvelope()))
	}, func(options *Options) { options.ResolveWait = 100 * time.Millisecond })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	dialed, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer dialed.Close()
	backendConn := <-accepted
	defer backendConn.Close()
	if _, err := client.ResolveConn(context.Background(), backendConn); err != nil {
		t.Fatal(err)
	}
	if request.LocalAddr != backendConn.RemoteAddr().String() || request.RemoteAddr != backendConn.LocalAddr().String() {
		t.Fatalf("tuple direction incorrect: request=%+v local=%s remote=%s", request, backendConn.RemoteAddr(), backendConn.LocalAddr())
	}
}

func TestResolveErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"not found", http.StatusNotFound, ErrFlowNotFound},
		{"unauthorized", http.StatusUnauthorized, ErrUnauthorized},
		{"forbidden", http.StatusForbidden, ErrForbidden},
		{"expired", http.StatusGone, ErrExpired},
		{"rate limited", http.StatusTooManyRequests, ErrRateLimited},
		{"server error", http.StatusServiceUnavailable, ErrUnavailable},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(`{"ok":false,"error":"test"}`))
			})
			_, err := client.ResolveTuple(context.Background(), testTuple())
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error=%v, want %v", err, testCase.want)
			}
		})
	}

	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not-json")) })
	if _, err := client.ResolveTuple(context.Background(), testTuple()); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("malformed response error=%v", err)
	}
}

func TestClientTimeoutDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	client, err := New(Options{
		Endpoint:   "http://127.0.0.1:1",
		Token:      "backend-token",
		BackendKey: "primary@192.0.2.10:9000",
		Timeout:    20 * time.Millisecond,
		HTTPClient: httpDoerFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ResolveTuple(context.Background(), testTuple())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error=%v, want unavailable", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("request count=%d, want 1", got)
	}
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSetTags(t *testing.T) {
	var requests []rpcRequest
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	if err := client.SetFlowTag(context.Background(), "flow-1", Tag{Namespace: "app", Key: "user", Value: "user-1", TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if err := client.SetClientTag(context.Background(), "flow-1", Tag{Namespace: "app", Key: "risk", Value: "abuse"}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d, want 2", len(requests))
	}
	if requests[0].Method != "SetFlowTag" || requests[1].Method != "SetClientTag" {
		t.Fatalf("methods=%q,%q", requests[0].Method, requests[1].Method)
	}
	first := requests[0].Params.(map[string]any)
	if first["flow_id"] != "flow-1" || first["ttl_seconds"] != float64(3600) {
		t.Fatalf("unexpected tag params: %+v", first)
	}
}

func TestClientCanBeShared(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(testFlowEnvelope()))
	})
	const count = 20
	errorsCh := make(chan error, count)
	var waitGroup sync.WaitGroup
	waitGroup.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer waitGroup.Done()
			flow, err := client.ResolveTuple(context.Background(), testTuple())
			if err == nil && flow.ID != "flow-1" {
				err = fmt.Errorf("flow id=%q", flow.ID)
			}
			errorsCh <- err
		}()
	}
	waitGroup.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != count {
		t.Fatalf("request count=%d, want %d", got, count)
	}
}
