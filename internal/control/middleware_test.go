package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRPCRejectsMissingBearerToken(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRPCRejectsWrongHTTPMethod(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodGet, "/rpc", nil)
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d body=%s", rec.Code, rec.Body.String())
	}
}
