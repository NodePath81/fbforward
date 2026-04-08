package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/iplog"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
)

type fakeManager struct{}

func (fakeManager) SetAuto()                              {}
func (fakeManager) SetManual(string) error                { return nil }
func (fakeManager) SetCoordination()                      {}
func (fakeManager) Snapshot() []upstream.UpstreamSnapshot { return nil }
func (fakeManager) Mode() upstream.Mode                   { return upstream.ModeAuto }
func (fakeManager) ActiveTag() string                     { return "" }
func (fakeManager) Get(string) *upstream.Upstream         { return nil }
func (fakeManager) CoordinationState() upstream.CoordinationState {
	return upstream.CoordinationState{}
}

func newTestControlServer(t *testing.T) *ControlServer {
	t.Helper()
	ctxDone := make(chan struct{})
	t.Cleanup(func() { close(ctxDone) })
	return NewControlServer(
		config.Config{
			Hostname: "test",
			Control: config.ControlConfig{
				BindAddr:  "127.0.0.1",
				BindPort:  8080,
				AuthToken: "0123456789abcdef",
			},
		},
		fakeManager{},
		metrics.NewMetrics(nil),
		NewStatusStore(NewStatusHub(ctxDone, nil), nil),
		nil,
		func() error { return nil },
		nil,
	)
}

func rpcRequestBody(t *testing.T, method string, params any) []byte {
	t.Helper()
	payload := map[string]any{"method": method}
	if params != nil {
		payload["params"] = params
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return data
}

func TestQueryIPLogUnavailableWithoutStore(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestQueryIPLogRejectsCIDRWithoutTimeBound(t *testing.T) {
	server := newTestControlServer(t)
	store, err := iplog.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", map[string]any{
		"cidr": "192.168.0.0/16",
	})))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestQueryIPLogReturnsResult(t *testing.T) {
	server := newTestControlServer(t)
	store, err := iplog.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)
	if err := store.InsertBatch([]iplog.EnrichedRecord{{
		CloseEvent: iplog.CloseEvent{
			IP:       "192.168.1.5",
			Protocol: "tcp",
			Upstream: "primary",
			Port:     9000,
		},
		Country: "US",
	}}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", map[string]any{
		"limit": 10,
	})))
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
	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp.Result)
	}
	if got := int(resultMap["total"].(float64)); got != 1 {
		t.Fatalf("expected total=1, got %d", got)
	}
}
