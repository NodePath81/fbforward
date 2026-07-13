package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnlineRuleRPCLifecycle(t *testing.T) {
	provider, store := newTestOnlineProvider(t)
	server := newTestControlServer(t)
	server.SetOnlinePolicyProvider(provider)
	request := func(method string, params any) *httptest.ResponseRecorder {
		return callTestRPC(t, server, "0123456789abcdef", method, params)
	}

	create := request("CreateOnlineRule", map[string]any{
		"rule_id":     "block-office",
		"action":      "deny",
		"matcher":     map[string]any{"source_cidr": "198.51.100.0/24", "protocol": "tcp", "port": 443},
		"priority":    100,
		"ttl_seconds": 3600,
		"reason":      "incident",
		"ticket_ref":  "INC-1",
	})
	if create.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	if duplicate := request("CreateOnlineRule", map[string]any{"rule_id": "block-office", "action": "deny", "matcher": map[string]any{"source_ip": "192.0.2.1"}, "ttl_seconds": 60}); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	list := request("ListOnlineRules", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var listResponse rpcResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listResponse); err != nil {
		t.Fatal(err)
	}
	if rules, ok := listResponse.Result.([]any); !ok || len(rules) != 1 {
		t.Fatalf("unexpected list result: %#v", listResponse.Result)
	}
	if expired := request("ExpireOnlineRule", map[string]any{"rule_id": "block-office"}); expired.Code != http.StatusOK {
		t.Fatalf("expire status=%d body=%s", expired.Code, expired.Body.String())
	}
	if active := request("ListOnlineRules", nil); active.Code != http.StatusOK || active.Body.String() == "" {
		t.Fatalf("active list failed: status=%d body=%s", active.Code, active.Body.String())
	}
	all := request("ListOnlineRules", map[string]any{"include_expired": true})
	if all.Code != http.StatusOK || len(all.Body.Bytes()) == 0 {
		t.Fatalf("all list failed: status=%d body=%s", all.Code, all.Body.String())
	}
	if deleted := request("DeleteOnlineRule", map[string]any{"rule_id": "block-office"}); deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if missing := request("DeleteOnlineRule", map[string]any{"rule_id": "block-office"}); missing.Code != http.StatusNotFound {
		t.Fatalf("missing delete status=%d body=%s", missing.Code, missing.Body.String())
	}
	events, err := store.QueryOnlineRuleEvents("block-office")
	if err != nil || len(events) != 3 {
		t.Fatalf("expected create/expire/delete audit events, got=%+v err=%v", events, err)
	}
	for _, event := range events {
		if event.PayloadJSON == "" || !strings.Contains(event.PayloadJSON, "block-office") {
			t.Fatalf("event missing complete rule snapshot: %+v", event)
		}
	}
}

func TestOnlineRuleRPCRejectsInvalidTTL(t *testing.T) {
	provider, _ := newTestOnlineProvider(t)
	server := newTestControlServer(t)
	server.SetOnlinePolicyProvider(provider)
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "CreateOnlineRule", map[string]any{
		"action": "deny", "matcher": map[string]any{"source_ip": "192.0.2.1"}, "ttl_seconds": 86401,
	})))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid TTL 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOnlineRuleRPCUnavailableWithoutSQLite(t *testing.T) {
	server := newTestControlServer(t)
	rec := callTestRPC(t, server, "0123456789abcdef", "ListOnlineRules", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected unavailable status, got %d body=%s", rec.Code, rec.Body.String())
	}
}
