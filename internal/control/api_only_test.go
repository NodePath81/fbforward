package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
