package flowcontextclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestConnContextStoresResolvedFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(flowEnvelope("flow-context")))
	}))
	t.Cleanup(server.Close)
	set, err := NewClientSet([]InstanceOptions{instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := set.ConnContext(context.Background(), testConn("127.0.0.2:52000", "192.0.2.10:9000"))
	flow, ok := FromContext(ctx)
	if !ok || flow.ID != "flow-context" {
		t.Fatalf("flow=%+v ok=%v", flow, ok)
	}
	resolved, ok := ResolvedFromContext(ctx)
	if !ok || resolved.Instance != "edge-a" || resolved.ID != flow.ID {
		t.Fatalf("resolved=%+v ok=%v", resolved, ok)
	}
}

func TestConnContextDoesNotStoreFailedResolution(t *testing.T) {
	tests := []struct {
		name   string
		handle http.HandlerFunc
	}{
		{"flow not found", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error":"missing"}`))
		}},
		{"unavailable", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"ok":false,"error":"down"}`))
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(testCase.handle)
			t.Cleanup(server.Close)
			set, err := NewClientSet([]InstanceOptions{instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), server.URL)})
			if err != nil {
				t.Fatal(err)
			}
			ctx := set.ConnContext(context.Background(), testConn("127.0.0.2:52000", "192.0.2.10:9000"))
			if _, ok := FromContext(ctx); ok {
				t.Fatal("failed resolution stored a flow")
			}
		})
	}

	set, err := NewClientSet([]InstanceOptions{instanceOptions("edge-a", netip.MustParseAddr("127.0.0.2"), "http://127.0.0.1:1")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := set.ConnContext(context.Background(), testConn("127.0.0.9:52000", "192.0.2.10:9000"))
	if _, ok := FromContext(ctx); ok {
		t.Fatal("unknown instance stored a flow")
	}
	if _, err := set.ResolveConn(context.Background(), testConn("127.0.0.9:52000", "192.0.2.10:9000")); !errors.Is(err, ErrUnknownInstance) {
		t.Fatalf("unknown instance error=%v", err)
	}
}
