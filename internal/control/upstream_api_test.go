package control

import (
	"bytes"
	"encoding/json"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/upstream"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetStatusOmitsLegacyCoordinationFields(t *testing.T) {
	server := newTestControlServer(t)
	server.fullCfg.Coordination = config.CoordinationConfig{
		Endpoint: "https://fbcoord.example",
		Token:    "node-token",
		Pool:     "legacy-pool",
		NodeID:   "legacy-node",
	}
	server.manager = fakeManager{
		coordState: upstream.CoordinationState{
			Connected:     true,
			Authoritative: false,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetStatus", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result payload: %#v", resp.Result)
	}
	coordinationState, ok := result["coordination"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected coordination payload: %#v", result["coordination"])
	}
	if _, exists := coordinationState["pool"]; exists {
		t.Fatalf("unexpected legacy pool in status response: %#v", coordinationState)
	}
	if _, exists := coordinationState["node_id"]; exists {
		t.Fatalf("unexpected legacy node_id in status response: %#v", coordinationState)
	}
	if coordinationState["authoritative"] != false {
		t.Fatalf("unexpected authoritative flag in status response: %#v", coordinationState)
	}
}
