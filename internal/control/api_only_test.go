package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIOnlyRoot(t *testing.T) {
	server := newTestControlServer(t)
	rec := httptest.NewRecorder()
	server.handleAPIOnlyRoot(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected API-only root to return 404, got %d", rec.Code)
	}
	if rec.Body.String() != "fbforward control API\n" {
		t.Fatalf("unexpected API-only root response: %q", rec.Body.String())
	}
}

func TestRemovedDistributedModeIsRejected(t *testing.T) {
	server := newTestControlServer(t)
	removedMode := "coord" + "ination"
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "SetUpstream", map[string]any{"mode": removedMode})))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected removed distributed mode to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
}
