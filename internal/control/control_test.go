package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/iplog"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
)

type fakeManager struct{}

type fakeGeoIPManager struct {
	status        geoip.Status
	refreshResult geoip.RefreshResult
	refreshErr    error
}

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

func (f fakeGeoIPManager) Status() geoip.Status {
	return f.status
}

func (f fakeGeoIPManager) RefreshNow(context.Context) (geoip.RefreshResult, error) {
	return f.refreshResult, f.refreshErr
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

func TestGetGeoIPStatusUnavailableWithoutManager(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetGeoIPStatusReturnsResult(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{
		status: geoip.Status{
			ASNDB: geoip.DBStatus{
				Configured:  true,
				Available:   true,
				Path:        "/tmp/asn.mmdb",
				FileModTime: 1712505600,
				FileSize:    1234,
			},
			CountryDB:       geoip.DBStatus{},
			RefreshInterval: "24h0m0s",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
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
	result := resp.Result.(map[string]any)
	asn := result["asn_db"].(map[string]any)
	if !asn["configured"].(bool) || !asn["available"].(bool) {
		t.Fatalf("unexpected status payload: %+v", result)
	}
}

func TestGetGeoIPStatusAcceptsOmittedAndNullParams(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{status: geoip.Status{RefreshInterval: "24h0m0s"}})

	reqOmitted := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
	reqOmitted.Header.Set("Authorization", "Bearer 0123456789abcdef")
	recOmitted := httptest.NewRecorder()
	server.handleRPC(recOmitted, reqOmitted)
	if recOmitted.Code != http.StatusOK {
		t.Fatalf("expected omitted params to succeed, got %d body=%s", recOmitted.Code, recOmitted.Body.String())
	}

	reqNull := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(`{"method":"GetGeoIPStatus","params":null}`))
	reqNull.Header.Set("Authorization", "Bearer 0123456789abcdef")
	recNull := httptest.NewRecorder()
	server.handleRPC(recNull, reqNull)
	if recNull.Code != http.StatusOK {
		t.Fatalf("expected null params to succeed, got %d body=%s", recNull.Code, recNull.Body.String())
	}
}

func TestGetGeoIPStatusSupportsUnconfiguredAndSingleDBPayloads(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{
		status: geoip.Status{
			ASNDB:     geoip.DBStatus{Configured: true, Path: "/tmp/asn.mmdb"},
			CountryDB: geoip.DBStatus{},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
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
	result := resp.Result.(map[string]any)
	asn := result["asn_db"].(map[string]any)
	country := result["country_db"].(map[string]any)
	if !asn["configured"].(bool) || country["configured"].(bool) {
		t.Fatalf("unexpected single-db payload: %+v", result)
	}

	server.SetGeoIPManager(fakeGeoIPManager{status: geoip.Status{}})
	req = httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetGeoIPStatus", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec = httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for unconfigured manager, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRefreshGeoIPUnavailableWithoutManager(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "RefreshGeoIP", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestRefreshGeoIPNoConfiguredDatabases(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{refreshErr: geoip.ErrNoConfiguredDatabases})

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "RefreshGeoIP", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRefreshGeoIPReturnsResult(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{
		refreshResult: geoip.RefreshResult{
			ASNDB: geoip.RefreshDBResult{
				Configured:      true,
				Attempted:       true,
				Refreshed:       true,
				PreviousModTime: 10,
				CurrentModTime:  20,
			},
			CountryDB: geoip.RefreshDBResult{
				Configured:      true,
				Attempted:       true,
				Refreshed:       false,
				PreviousModTime: 10,
				CurrentModTime:  10,
				Error:           "download failed",
			},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "RefreshGeoIP", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRefreshGeoIPAcceptsOmittedAndNullParams(t *testing.T) {
	server := newTestControlServer(t)
	server.SetGeoIPManager(fakeGeoIPManager{refreshResult: geoip.RefreshResult{}})

	reqOmitted := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "RefreshGeoIP", nil)))
	reqOmitted.Header.Set("Authorization", "Bearer 0123456789abcdef")
	recOmitted := httptest.NewRecorder()
	server.handleRPC(recOmitted, reqOmitted)
	if recOmitted.Code != http.StatusOK {
		t.Fatalf("expected omitted params to succeed, got %d body=%s", recOmitted.Code, recOmitted.Body.String())
	}

	reqNull := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(`{"method":"RefreshGeoIP","params":null}`))
	reqNull.Header.Set("Authorization", "Bearer 0123456789abcdef")
	recNull := httptest.NewRecorder()
	server.handleRPC(recNull, reqNull)
	if recNull.Code != http.StatusOK {
		t.Fatalf("expected null params to succeed, got %d body=%s", recNull.Code, recNull.Body.String())
	}
}

func TestGetIPLogStatusUnavailableWithoutStore(t *testing.T) {
	server := newTestControlServer(t)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetIPLogStatus", nil)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestGetIPLogStatusAcceptsOmittedAndNullParams(t *testing.T) {
	server := newTestControlServer(t)
	dbPath := filepath.Join(t.TempDir(), "iplog.sqlite")
	store, err := iplog.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)
	server.fullCfg.IPLog.DBPath = dbPath

	reqOmitted := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetIPLogStatus", nil)))
	reqOmitted.Header.Set("Authorization", "Bearer 0123456789abcdef")
	recOmitted := httptest.NewRecorder()
	server.handleRPC(recOmitted, reqOmitted)
	if recOmitted.Code != http.StatusOK {
		t.Fatalf("expected omitted params to succeed, got %d body=%s", recOmitted.Code, recOmitted.Body.String())
	}

	reqNull := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(`{"method":"GetIPLogStatus","params":null}`))
	reqNull.Header.Set("Authorization", "Bearer 0123456789abcdef")
	recNull := httptest.NewRecorder()
	server.handleRPC(recNull, reqNull)
	if recNull.Code != http.StatusOK {
		t.Fatalf("expected null params to succeed, got %d body=%s", recNull.Code, recNull.Body.String())
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

func TestQueryIPLogRejectsMalformedAndInvalidPaging(t *testing.T) {
	server := newTestControlServer(t)
	store, err := iplog.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)

	malformed := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(`{"method":"QueryIPLog","params":{"limit":"bad"}}`))
	malformed.Header.Set("Authorization", "Bearer 0123456789abcdef")
	malformedRec := httptest.NewRecorder()
	server.handleRPC(malformedRec, malformed)
	if malformedRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed params, got %d", malformedRec.Code)
	}

	invalidPaging := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", map[string]any{
		"limit":  1001,
		"offset": -1,
	})))
	invalidPaging.Header.Set("Authorization", "Bearer 0123456789abcdef")
	invalidRec := httptest.NewRecorder()
	server.handleRPC(invalidRec, invalidPaging)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid paging, got %d", invalidRec.Code)
	}
}

func TestGetIPLogStatusReturnsStats(t *testing.T) {
	server := newTestControlServer(t)
	dbPath := filepath.Join(t.TempDir(), "iplog.sqlite")
	store, err := iplog.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)
	server.fullCfg.IPLog = config.IPLogConfig{
		Enabled:       true,
		DBPath:        dbPath,
		Retention:     config.Duration(24 * time.Hour),
		PruneInterval: config.Duration(2 * time.Hour),
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]iplog.EnrichedRecord{
		{CloseEvent: iplog.CloseEvent{IP: "192.168.1.10", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: now.Add(-time.Minute)}},
		{CloseEvent: iplog.CloseEvent{IP: "192.168.1.11", Protocol: "udp", Upstream: "b", Port: 2, RecordedAt: now}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetIPLogStatus", nil)))
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
	result := resp.Result.(map[string]any)
	if got := int(result["record_count"].(float64)); got != 2 {
		t.Fatalf("expected record_count=2, got %#v", result)
	}
	if got := result["retention"].(string); got != "24h0m0s" {
		t.Fatalf("unexpected retention: %#v", result)
	}
	if got := result["prune_interval"].(string); got != "2h0m0s" {
		t.Fatalf("unexpected prune interval: %#v", result)
	}
	if got := int64(result["file_size"].(float64)); got <= 0 {
		t.Fatalf("expected positive file_size, got %#v", result)
	}
}

func TestGetIPLogStatusEmptyAndStatFailure(t *testing.T) {
	server := newTestControlServer(t)
	storePath := filepath.Join(t.TempDir(), "iplog.sqlite")
	store, err := iplog.NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)
	server.fullCfg.IPLog = config.IPLogConfig{
		Enabled:       true,
		DBPath:        filepath.Join(t.TempDir(), "missing.sqlite"),
		Retention:     0,
		PruneInterval: config.Duration(time.Hour),
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "GetIPLogStatus", nil)))
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
	result := resp.Result.(map[string]any)
	if got := int(result["record_count"].(float64)); got != 0 {
		t.Fatalf("expected empty db count, got %#v", result)
	}
	if got := result["retention"].(string); got != "0s" {
		t.Fatalf("expected disabled retention, got %#v", result)
	}
	if got := int64(result["file_size"].(float64)); got != 0 {
		t.Fatalf("expected stat failure fallback to zero, got %#v", result)
	}
}

func TestQueryIPLogRejectsInvalidSortParams(t *testing.T) {
	server := newTestControlServer(t)
	store, err := iplog.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", map[string]any{
		"sort_by": "invalid",
	})))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()

	server.handleRPC(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", map[string]any{
		"sort_order": "sideways",
	})))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec = httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid sort_order to return 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestQueryIPLogSortsByBytesTotal(t *testing.T) {
	server := newTestControlServer(t)
	store, err := iplog.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]iplog.EnrichedRecord{
		{CloseEvent: iplog.CloseEvent{IP: "192.168.1.10", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 10, BytesDown: 1, RecordedAt: now.Add(-2 * time.Minute)}},
		{CloseEvent: iplog.CloseEvent{IP: "192.168.1.11", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 2, BytesDown: 50, RecordedAt: now.Add(-time.Minute)}},
		{CloseEvent: iplog.CloseEvent{IP: "192.168.1.12", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 3, BytesDown: 3, RecordedAt: now}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", map[string]any{
		"sort_by":    "bytes_total",
		"sort_order": "desc",
		"limit":      10,
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
	result := resp.Result.(map[string]any)
	records := result["records"].([]any)
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %#v", result)
	}
	first := records[0].(map[string]any)
	if got := first["ip"].(string); got != "192.168.1.11" {
		t.Fatalf("expected highest bytes_total first, got %#v", result)
	}
}

func TestQueryIPLogDefaultsAndCombinedFilters(t *testing.T) {
	server := newTestControlServer(t)
	store, err := iplog.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)

	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour).Unix()
	end := now.Add(time.Hour).Unix()
	asn := 13335
	if err := store.InsertBatch([]iplog.EnrichedRecord{
		{CloseEvent: iplog.CloseEvent{IP: "192.168.1.10", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 10, BytesDown: 1, DurationMs: 30, RecordedAt: now.Add(-2 * time.Minute)}, ASN: 13335, Country: "US"},
		{CloseEvent: iplog.CloseEvent{IP: "192.168.1.11", Protocol: "tcp", Upstream: "a", Port: 1, BytesUp: 2, BytesDown: 50, DurationMs: 20, RecordedAt: now.Add(-time.Minute)}, ASN: 13335, Country: "US"},
		{CloseEvent: iplog.CloseEvent{IP: "198.51.100.10", Protocol: "tcp", Upstream: "b", Port: 1, BytesUp: 99, BytesDown: 0, DurationMs: 99, RecordedAt: now}, ASN: 64500, Country: "CA"},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}

	defaultReq := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", map[string]any{
		"limit": 10,
	})))
	defaultReq.Header.Set("Authorization", "Bearer 0123456789abcdef")
	defaultRec := httptest.NewRecorder()
	server.handleRPC(defaultRec, defaultReq)
	if defaultRec.Code != http.StatusOK {
		t.Fatalf("expected default query to succeed, got %d body=%s", defaultRec.Code, defaultRec.Body.String())
	}
	var resp rpcResponse
	if err := json.Unmarshal(defaultRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	defaultRecords := resp.Result.(map[string]any)["records"].([]any)
	if got := defaultRecords[0].(map[string]any)["ip"].(string); got != "198.51.100.10" {
		t.Fatalf("expected default sort to remain recorded_at desc, got %#v", defaultRecords)
	}

	filteredReq := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryIPLog", map[string]any{
		"start_time": start,
		"end_time":   end,
		"asn":        asn,
		"country":    "us",
		"sort_by":    "bytes_total",
		"sort_order": "desc",
		"limit":      10,
	})))
	filteredReq.Header.Set("Authorization", "Bearer 0123456789abcdef")
	filteredRec := httptest.NewRecorder()
	server.handleRPC(filteredRec, filteredReq)
	if filteredRec.Code != http.StatusOK {
		t.Fatalf("expected filtered sort query to succeed, got %d body=%s", filteredRec.Code, filteredRec.Body.String())
	}
	if err := json.Unmarshal(filteredRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal filtered response: %v", err)
	}
	filtered := resp.Result.(map[string]any)
	if got := int(filtered["total"].(float64)); got != 2 {
		t.Fatalf("expected filtered total 2, got %#v", filtered)
	}
	first := filtered["records"].([]any)[0].(map[string]any)
	if got := first["ip"].(string); got != "192.168.1.11" {
		t.Fatalf("expected bytes_total sort after filters, got %#v", filtered)
	}
}

func TestRuntimeConfigIncludesIPLogTuning(t *testing.T) {
	server := newTestControlServer(t)
	server.fullCfg.IPLog = config.IPLogConfig{
		Enabled:        true,
		DBPath:         "/tmp/iplog.sqlite",
		Retention:      config.Duration(24 * time.Hour),
		GeoQueueSize:   64,
		WriteQueueSize: 32,
		BatchSize:      50,
		FlushInterval:  config.Duration(7 * time.Second),
		PruneInterval:  config.Duration(2 * time.Hour),
	}

	cfg := server.getRuntimeConfig()
	iplogCfg := cfg["ip_log"].(map[string]interface{})
	if got := iplogCfg["batch_size"]; got != 50 {
		t.Fatalf("expected batch_size in runtime config, got %#v", got)
	}
	if got := iplogCfg["flush_interval"]; got != "7s" {
		t.Fatalf("expected flush_interval in runtime config, got %#v", got)
	}
	if got := iplogCfg["prune_interval"]; got != "2h0m0s" {
		t.Fatalf("expected prune_interval in runtime config, got %#v", got)
	}
}
