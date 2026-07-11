package flowcontext

import (
	"bytes"
	"encoding/json"
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

func TestServiceResolveActiveFlow(t *testing.T) {
	service, registry, _ := serviceWithFlow(t, Options{CleanupInterval: time.Millisecond})
	defer registry.Shutdown()
	response := doResolve(service, http.MethodPost, "secret", resolveBody())
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		Ok   bool          `json:"ok"`
		Flow *flowResponse `json:"flow"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Ok || decoded.Flow == nil || decoded.Flow.FlowID != "f1" || decoded.Flow.State != StateActive {
		t.Fatalf("unexpected response: %+v", decoded)
	}
}

func TestServiceAuthMethodAndTupleErrors(t *testing.T) {
	service, registry, _ := serviceWithFlow(t, Options{CleanupInterval: time.Millisecond})
	defer registry.Shutdown()
	if response := doResolve(service, http.MethodPost, "", resolveBody()); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", response.Code)
	}
	if response := doResolve(service, http.MethodGet, "secret", "{}"); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status=%d", response.Code)
	}
	if response := doResolve(service, http.MethodPost, "secret", "not-json"); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d", response.Code)
	}
	if response := doResolve(service, http.MethodPost, "secret", `{"protocol":"tcp","backend_key":"x","local_addr":"bad","remote_addr":"bad"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("tuple status=%d", response.Code)
	}
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

func TestServiceClosedGraceAndExpiry(t *testing.T) {
	service, registry, tuple := serviceWithFlow(t, Options{CleanupInterval: time.Millisecond, GracePeriod: 8 * time.Millisecond})
	defer registry.Shutdown()
	meta := testMeta("f1", flow.ProtocolTCP)
	registry.Close(flow.Summary{Meta: meta, EndedAt: time.Now().UTC()})
	response := doResolve(service, http.MethodPost, "secret", resolveBody())
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"closed"`) {
		t.Fatalf("closed response status=%d body=%s", response.Code, response.Body.String())
	}
	_ = tuple
	time.Sleep(30 * time.Millisecond)
	response = doResolve(service, http.MethodPost, "secret", resolveBody())
	if response.Code != http.StatusNotFound {
		t.Fatalf("expired response status=%d", response.Code)
	}
}
