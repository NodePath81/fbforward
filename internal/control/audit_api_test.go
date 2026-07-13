package control

import (
	"bytes"
	"encoding/json"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/iplog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

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
	if err := store.InsertRejectionBatch([]iplog.EnrichedRejectionRecord{
		{RejectionEvent: iplog.RejectionEvent{IP: "10.0.0.1", Protocol: "tcp", Port: 1, Reason: "firewall_deny", RecordedAt: now.Add(-30 * time.Second)}},
	}); err != nil {
		t.Fatalf("InsertRejectionBatch error: %v", err)
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
	if got := int(result["record_count"].(float64)); got != 3 {
		t.Fatalf("expected record_count=3, got %#v", result)
	}
	if got := int(result["flow_record_count"].(float64)); got != 2 {
		t.Fatalf("expected flow_record_count=2, got %#v", result)
	}
	if got := int(result["rejection_record_count"].(float64)); got != 1 {
		t.Fatalf("expected rejection_record_count=1, got %#v", result)
	}
	if got := int(result["total_record_count"].(float64)); got != 3 {
		t.Fatalf("expected total_record_count=3, got %#v", result)
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
	if got := int(result["flow_record_count"].(float64)); got != 0 {
		t.Fatalf("expected empty flow count, got %#v", result)
	}
	if got := int(result["rejection_record_count"].(float64)); got != 0 {
		t.Fatalf("expected empty rejection count, got %#v", result)
	}
	if got := result["retention"].(string); got != "0s" {
		t.Fatalf("expected disabled retention, got %#v", result)
	}
	if got := int64(result["file_size"].(float64)); got != 0 {
		t.Fatalf("expected stat failure fallback to zero, got %#v", result)
	}
}

func TestQueryRejectionLogReturnsResult(t *testing.T) {
	server := newTestControlServer(t)
	store, err := iplog.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertRejectionBatch([]iplog.EnrichedRejectionRecord{
		{RejectionEvent: iplog.RejectionEvent{
			IP:               "10.0.0.1",
			Protocol:         "udp",
			Port:             9000,
			Reason:           "udp_mapping_limit",
			MatchedRuleType:  "",
			MatchedRuleValue: "",
			RecordedAt:       now,
		}},
	}); err != nil {
		t.Fatalf("InsertRejectionBatch error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryRejectionLog", map[string]any{
		"reason": "udp_mapping_limit",
		"limit":  10,
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
	if got := int(result["total"].(float64)); got != 1 {
		t.Fatalf("expected total=1, got %#v", result)
	}
	first := result["records"].([]any)[0].(map[string]any)
	if got := first["reason"].(string); got != "udp_mapping_limit" {
		t.Fatalf("unexpected rejection query result: %#v", first)
	}
}

func TestQueryLogEventsReturnsMergedResult(t *testing.T) {
	server := newTestControlServer(t)
	store, err := iplog.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetIPLogStore(store)

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]iplog.EnrichedRecord{
		{CloseEvent: iplog.CloseEvent{
			IP:         "192.168.1.10",
			Protocol:   "tcp",
			Upstream:   "primary",
			Port:       9000,
			BytesUp:    10,
			BytesDown:  20,
			DurationMs: 30,
			RecordedAt: now.Add(-time.Minute),
		}},
	}); err != nil {
		t.Fatalf("InsertBatch error: %v", err)
	}
	if err := store.InsertRejectionBatch([]iplog.EnrichedRejectionRecord{
		{RejectionEvent: iplog.RejectionEvent{
			IP:               "10.0.0.1",
			Protocol:         "tcp",
			Port:             9000,
			Reason:           "firewall_deny",
			MatchedRuleType:  "cidr",
			MatchedRuleValue: "10.0.0.0/8",
			RecordedAt:       now,
		}},
	}); err != nil {
		t.Fatalf("InsertRejectionBatch error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "QueryLogEvents", map[string]any{
		"entry_type": "all",
		"sort_by":    "recorded_at",
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
	if got := int(result["total"].(float64)); got != 2 {
		t.Fatalf("expected total=2, got %#v", result)
	}
	records := result["records"].([]any)
	first := records[0].(map[string]any)
	second := records[1].(map[string]any)
	if first["entry_type"].(string) != "rejection" || second["entry_type"].(string) != "flow" {
		t.Fatalf("unexpected merged results: %#v", result)
	}
}

func TestRuntimeConfigIncludesIPLogTuning(t *testing.T) {
	server := newTestControlServer(t)
	logRejections := true
	server.fullCfg.IPLog = config.IPLogConfig{
		Enabled:        true,
		LogRejections:  &logRejections,
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
	if got := iplogCfg["log_rejections"]; got != true {
		t.Fatalf("expected log_rejections in runtime config, got %#v", got)
	}
}
