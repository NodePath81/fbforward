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

func TestQueryIPLogRejectsCIDRWithoutTimeBound(t *testing.T) {
	server := newTestControlServer(t)
	_ = newTestIPLogStore(t, server)

	rec := callTestRPC(t, server, "0123456789abcdef", "QueryIPLog", map[string]any{
		"cidr": "192.168.0.0/16",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestQueryIPLogRejectsMalformedAndInvalidPaging(t *testing.T) {
	server := newTestControlServer(t)
	_ = newTestIPLogStore(t, server)

	malformed := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(`{"method":"QueryIPLog","params":{"limit":"bad"}}`))
	malformed.Header.Set("Authorization", "Bearer 0123456789abcdef")
	malformedRec := httptest.NewRecorder()
	server.handleRPC(malformedRec, malformed)
	if malformedRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed params, got %d", malformedRec.Code)
	}

	invalidRec := callTestRPC(t, server, "0123456789abcdef", "QueryIPLog", map[string]any{
		"limit":  1001,
		"offset": -1,
	})
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

	rec := callTestRPC(t, server, "0123456789abcdef", "GetIPLogStatus", nil)
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

func TestQueryLogEventsReturnsMergedResult(t *testing.T) {
	server := newTestControlServer(t)
	store := newTestIPLogStore(t, server)

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

	rec := callTestRPC(t, server, "0123456789abcdef", "QueryLogEvents", map[string]any{
		"entry_type": "all",
		"sort_by":    "recorded_at",
		"sort_order": "desc",
		"limit":      10,
	})
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
