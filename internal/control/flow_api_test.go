package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

func TestGetActiveFlowsRPCReturnsSnapshot(t *testing.T) {
	server := newTestControlServer(t)
	id, err := flow.NewID()
	if err != nil {
		t.Fatalf("NewID error: %v", err)
	}
	meta := flow.Meta{
		ID:         id,
		Protocol:   flow.ProtocolTCP,
		ClientAddr: netip.MustParseAddrPort("192.0.2.10:4321"),
		Listener:   "127.0.0.1:8443",
		Route:      "web",
		Upstream:   "primary",
		StartedAt:  time.Now().UTC(),
	}
	server.status.Open(meta)

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetActiveFlows", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Ok     bool `json:"ok"`
		Result struct {
			CapturedAt int64         `json:"captured_at"`
			TCP        []StatusEntry `json:"tcp"`
			UDP        []StatusEntry `json:"udp"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Ok || response.Result.CapturedAt == 0 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if len(response.Result.TCP) != 1 || response.Result.TCP[0].ID != id.String() {
		t.Fatalf("unexpected flow snapshot: %+v", response.Result.TCP)
	}
	if response.Result.TCP[0].Route != "web" || response.Result.TCP[0].Upstream != "primary" {
		t.Fatalf("missing flow metadata: %+v", response.Result.TCP[0])
	}
}

func TestGetActiveFlowsRPCRequiresAuth(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetActiveFlows", nil)))
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Ok || response.Error != "unauthorized" {
		t.Fatalf("unexpected response: %+v", response)
	}
}
