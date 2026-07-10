package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRPCRegistryRejectsDuplicateAndEmptyNames(t *testing.T) {
	r := newRPCRegistry()
	h := func(*rpcContext, json.RawMessage) (any, *rpcFault) { return nil, nil }
	if err := r.Register("Ping", h); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register("Ping", h); err == nil {
		t.Fatal("expected duplicate registration error")
	}
	if err := r.Register("", h); err == nil {
		t.Fatal("expected empty name error")
	}
	if got, ok := r.Lookup("Ping"); !ok || got == nil {
		t.Fatal("registered handler not found")
	}
}

func TestRPCUnknownMethodReturnsJSONError(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "Unknown", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if response.Ok || response.Error != "unknown method" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestRPCBodyLimitReturnsJSON413(t *testing.T) {
	server := newTestControlServer(t)
	body := []byte(`{"method":"GetStatus","params":{"padding":"`)
	body = append(body, bytes.Repeat([]byte("x"), maxRPCBodyBytes)...)
	body = append(body, []byte(`"}}`)...)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Ok || response.Error != "request body too large" {
		t.Fatalf("unexpected response: %+v", response)
	}
}
