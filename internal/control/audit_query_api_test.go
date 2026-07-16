package control

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/iplog"
)

func TestQueryAuditDSLAndTopASNs(t *testing.T) {
	server := newTestControlServer(t)
	store := newTestIPLogStore(t, server)
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]iplog.EnrichedRecord{
		{CloseEvent: iplog.CloseEvent{IP: "192.0.2.10", Protocol: "tcp", Upstream: "primary", BytesUp: 20, BytesDown: 30, RecordedAt: now}, ASN: 64500, ASOrg: "Example", Country: "US"},
		{CloseEvent: iplog.CloseEvent{IP: "192.0.2.11", Protocol: "tcp", Upstream: "primary", BytesUp: 1, BytesDown: 2, RecordedAt: now}, ASN: 64501, ASOrg: "Other", Country: "GB"},
	}); err != nil {
		t.Fatal(err)
	}
	rec := callTestRPC(t, server, "0123456789abcdef", "QueryAudit", map[string]any{
		"query": "flows protocol=tcp upstream=primary | sort bytes_total desc | limit 1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("QueryAudit status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result := response.Result.(map[string]any)["result"].(map[string]any)
	if int(result["total"].(float64)) != 2 || len(result["records"].([]any)) != 1 {
		t.Fatalf("unexpected flow query result: %#v", result)
	}

	rec = callTestRPC(t, server, "0123456789abcdef", "QueryAudit", map[string]any{
		"query": "top asns protocol=tcp | limit 10",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("top asns status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	items := response.Result.(map[string]any)["result"].([]any)
	if len(items) != 2 || int(items[0].(map[string]any)["asn"].(float64)) != 64500 {
		t.Fatalf("unexpected top ASN result: %#v", items)
	}
}

func TestQueryAuditRejectsUnknownFieldsAndSQL(t *testing.T) {
	server := newTestControlServer(t)
	_ = newTestIPLogStore(t, server)
	for _, params := range []map[string]any{
		{"query": "flows protocol=tcp; DROP TABLE flows"},
		{"query": "flows tag=x", "extra": true},
		{"query": "flows | limit 1001"},
	} {
		rec := callTestRPC(t, server, "0123456789abcdef", "QueryAudit", params)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("params=%v status=%d body=%s", params, rec.Code, rec.Body.String())
		}
	}
}

func TestQueryAuditSourcesAndFilters(t *testing.T) {
	server := newTestControlServer(t)
	store := newTestIPLogStore(t, server)
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.InsertBatch([]iplog.EnrichedRecord{{CloseEvent: iplog.CloseEvent{IP: "192.0.2.20", Protocol: "udp", Upstream: "backup", BytesUp: 3, BytesDown: 7, RecordedAt: now}, ASN: 64520, Country: "US"}}); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		"flows ip=192.0.2.20",
		"events country=us",
		"top clients protocol=UDP",
		"top asns protocol=UDP",
		"top tags protocol=UDP",
	}
	for _, query := range queries {
		rec := callTestRPC(t, server, "0123456789abcdef", "QueryAudit", map[string]any{"query": query})
		if rec.Code != http.StatusOK {
			t.Errorf("query %q status=%d body=%s", query, rec.Code, rec.Body.String())
		}
	}
	for _, query := range []string{
		"flows ip=192.0.2.999",
		"flows since=2026-01-02T00:00:00Z until=2026-01-01T00:00:00Z",
		"top asns protocol=icmp",
	} {
		rec := callTestRPC(t, server, "0123456789abcdef", "QueryAudit", map[string]any{"query": query})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("invalid query %q status=%d body=%s", query, rec.Code, rec.Body.String())
		}
	}
}

func TestFlowContextTagAndActionRPCs(t *testing.T) {
	server := newTestControlServer(t)
	store := newTestIPLogStore(t, server)
	now := time.Now().UTC()
	if err := store.UpsertFlowEntity(iplog.FlowEntity{FlowID: "rpc-context-flow", Protocol: "tcp", ClientIP: "192.0.2.40", CreatedAt: now, LastActivity: now, State: "closed"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(iplog.FlowTag{FlowID: "rpc-context-flow", Tag: "app:user=alice", Source: "flow-context", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFlowTagEvent(iplog.FlowTagEvent{EventID: "rpc-context-event", FlowID: "rpc-context-flow", Tag: "app:user=alice", Operation: "set", Actor: "micproxy", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for method := range map[string]bool{"ListFlowContextTags": true, "ListFlowContextActions": true} {
		rec := callTestRPC(t, server, "0123456789abcdef", method, map[string]any{"limit": 10})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", method, rec.Code, rec.Body.String())
		}
	}
	if rec := callTestRPC(t, server, "0123456789abcdef", "ListFlowContextTags", map[string]any{"scope": "invalid"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid context scope status=%d", rec.Code)
	}
	if rec := callTestRPC(t, server, "0123456789abcdef", "ListFlowContextActions", map[string]any{"offset": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid context offset status=%d", rec.Code)
	}
}

func TestQueryAuditFlowRecordsIncludeCurrentTags(t *testing.T) {
	server := newTestControlServer(t)
	store := newTestIPLogStore(t, server)
	now := time.Now().UTC()
	if err := store.InsertBatch([]iplog.EnrichedRecord{{CloseEvent: iplog.CloseEvent{FlowID: "audit-tag-flow", IP: "192.0.2.41", Protocol: "tcp", BytesUp: 4, BytesDown: 6, RecordedAt: now}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowEntity(iplog.FlowEntity{FlowID: "audit-tag-flow", Protocol: "tcp", ClientIP: "192.0.2.41", CreatedAt: now, LastActivity: now, State: "closed"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(iplog.FlowTag{FlowID: "audit-tag-flow", Tag: "app:user=alice", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	rec := callTestRPC(t, server, "0123456789abcdef", "QueryAudit", map[string]any{"query": "flows"})
	if rec.Code != http.StatusOK {
		t.Fatalf("QueryAudit status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	result := response.Result.(map[string]any)["result"].(map[string]any)
	records := result["records"].([]any)
	if len(records) != 1 || len(records[0].(map[string]any)["tags"].([]any)) != 1 {
		t.Fatalf("audit tags = %#v", records)
	}
}
