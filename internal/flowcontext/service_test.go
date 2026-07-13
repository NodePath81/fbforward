package flowcontext

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

func resolveBody() string {
	return `{"protocol":"tcp","backend_key":"primary@192.0.2.10:443","local_addr":"10.0.0.1:43122","remote_addr":"192.0.2.10:443"}`
}

func serviceWithFlow(t *testing.T, options Options) (*Service, *Registry, flow.BackendTuple) {
	t.Helper()
	registry := NewRegistry(options)
	meta := testMeta("f1", flow.ProtocolTCP)
	tuple := testTuple(flow.ProtocolTCP)
	registry.Open(meta)
	if err := registry.Bind(meta.ID, tuple); err != nil {
		t.Fatal(err)
	}
	return NewService(registry, nil, HTTPOptions{Identities: []Identity{{ID: "caddy", Token: "secret", Routes: []string{""}, Upstreams: []string{"primary"}, Namespaces: []string{"app"}}}}, nil), registry, tuple
}

func doResolve(service *Service, method, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/flow-context/resolve", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	service.HandleResolve(recorder, req)
	return recorder
}

func TestServiceBodyLimitAndNotFound(t *testing.T) {
	service, registry, _ := serviceWithFlow(t, Options{CleanupInterval: time.Millisecond})
	defer registry.Shutdown()
	response := doResolve(service, http.MethodPost, "secret", strings.Repeat("x", int(maxFlowContextBodyBytes)+1))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit status=%d", response.Code)
	}
	missing := `{"protocol":"tcp","backend_key":"primary@192.0.2.10:443","local_addr":"10.0.0.1:9999","remote_addr":"192.0.2.10:443"}`
	response = doResolve(service, http.MethodPost, "secret", missing)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing tuple status=%d", response.Code)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"ok":false`)) {
		t.Fatalf("expected JSON error: %s", response.Body.String())
	}
}
