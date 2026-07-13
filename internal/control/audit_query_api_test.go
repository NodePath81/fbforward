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
