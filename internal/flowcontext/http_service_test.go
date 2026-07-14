package flowcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/util"
)

func newHTTPTagServiceTest(t *testing.T) (*Service, *Registry, *audit.Store, flow.BackendTuple) {
	t.Helper()
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(Options{CleanupInterval: time.Millisecond, GracePeriod: time.Second})
	meta := testMeta("f1", flow.ProtocolTCP)
	meta.Route = "web"
	tuple := testTuple(flow.ProtocolTCP)
	registry.Open(meta)
	if err := registry.Bind(meta.ID, tuple); err != nil {
		t.Fatal(err)
	}
	service := NewService(registry, store, HTTPOptions{
		Identities: []Identity{
			{ID: "caddy", Token: "backend-secret", Routes: []string{"web"}, Upstreams: []string{"primary"}, Namespaces: []string{"app"}},
			{ID: "other", Token: "other-secret", Routes: []string{"other"}, Upstreams: []string{"primary"}, Namespaces: []string{"app"}},
			{ID: "backup", Token: "backup-secret", Routes: []string{"web"}, Upstreams: []string{"backup"}, Namespaces: []string{"app"}},
		},
		MaxTTL:         2 * time.Second,
		RateLimitBurst: 100,
	}, nil)
	t.Cleanup(func() {
		_ = registry.Shutdown()
		_ = store.Close()
	})
	return service, registry, store, tuple
}

func callHTTPRPC(t *testing.T, client *http.Client, endpoint, token, method string, params any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Request-ID", "test-request")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, decoded
}

func TestHTTPServiceTagLifecycleUsesFlowEntities(t *testing.T) {
	service, registry, store, tuple := newHTTPTagServiceTest(t)
	server := httptest.NewServer(http.HandlerFunc(service.HandleRPC))
	defer server.Close()
	_ = tuple

	params := map[string]any{"flow_id": "f1", "namespace": "app", "key": "owner", "value": "alice"}
	if status, response := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "SetFlowTag", params); status != http.StatusOK || response["ok"] != true {
		t.Fatalf("set flow tag status=%d response=%v", status, response)
	}
	if result, err := store.Query(audit.QueryParams{Limit: 10}); err != nil {
		t.Fatal(err)
	} else if result.Total != 0 {
		t.Fatalf("active tagging inserted flow summary: %d", result.Total)
	}
	tags, err := store.QueryFlowTags("f1")
	if err != nil || len(tags) != 1 || tags[0].Tag != "app:owner=alice" {
		t.Fatalf("flow tags=%+v err=%v", tags, err)
	}

	params["value"] = "bob"
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "SetFlowTag", params); status != http.StatusOK {
		t.Fatalf("replace status=%d", status)
	}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "UnsetFlowTag", params); status != http.StatusOK {
		t.Fatalf("unset status=%d", status)
	}
	clientParams := map[string]any{"flow_id": "f1", "namespace": "app", "key": "class", "value": "customer", "ttl_seconds": 1}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "SetClientTag", clientParams); status != http.StatusOK {
		t.Fatalf("set client tag status=%d", status)
	}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "UnsetClientTag", clientParams); status != http.StatusOK {
		t.Fatalf("unset client tag status=%d", status)
	}
	events, err := store.QueryFlowTagEvents("f1")
	if err != nil || len(events) != 5 {
		t.Fatalf("tag events=%+v err=%v", events, err)
	}

	registry.Close(flow.Summary{Meta: testMeta("f1", flow.ProtocolTCP), EndedAt: time.Now().UTC()})
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "SetFlowTag", params); status != http.StatusOK {
		t.Fatalf("closed grace tag status=%d", status)
	}
}

func TestHTTPServiceIdentityAndTagValidation(t *testing.T) {
	service, _, _, _ := newHTTPTagServiceTest(t)
	server := httptest.NewServer(http.HandlerFunc(service.HandleRPC))
	defer server.Close()
	params := map[string]any{"flow_id": "f1", "namespace": "app", "key": "owner", "value": "x"}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "wrong", "SetFlowTag", params); status != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d", status)
	}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "other-secret", "SetFlowTag", params); status != http.StatusForbidden {
		t.Fatalf("cross route status=%d", status)
	}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backup-secret", "SetFlowTag", params); status != http.StatusForbidden {
		t.Fatalf("cross upstream status=%d", status)
	}
	params["namespace"] = "tenant"
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "SetFlowTag", params); status != http.StatusBadRequest {
		t.Fatalf("namespace status=%d", status)
	}
	params["namespace"] = "app"
	params["ttl_seconds"] = 3
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "SetFlowTag", params); status != http.StatusBadRequest {
		t.Fatalf("ttl status=%d", status)
	}
}

type recordingFlowController struct {
	blocked []flow.ID
	limited map[flow.ID]uint64
	cleared []flow.ID
	fail    bool
}

func (c *recordingFlowController) Block(id flow.ID) bool {
	c.blocked = append(c.blocked, id)
	return !c.fail
}

func (c *recordingFlowController) SetLimit(id flow.ID, rate uint64) bool {
	if c.limited == nil {
		c.limited = make(map[flow.ID]uint64)
	}
	c.limited[id] = rate
	return !c.fail
}

func (c *recordingFlowController) ClearLimit(id flow.ID) bool {
	c.cleared = append(c.cleared, id)
	return !c.fail
}

func TestHTTPServiceFlowControlsAndAudit(t *testing.T) {
	service, _, _, _ := newHTTPTagServiceTest(t)
	controller := &recordingFlowController{}
	service.SetFlowController(controller)
	server := httptest.NewServer(http.HandlerFunc(service.HandleRPC))
	defer server.Close()

	base := map[string]any{"flow_id": "f1"}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "SetFlowLimit", map[string]any{"flow_id": "f1", "rate_bps": 1000000}); status != http.StatusOK {
		t.Fatalf("set limit status=%d", status)
	}
	if controller.limited["f1"] != 1000000 {
		t.Fatalf("unexpected limit calls: %+v", controller.limited)
	}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "ClearFlowLimit", base); status != http.StatusOK {
		t.Fatalf("clear limit status=%d", status)
	}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "BlockFlow", map[string]any{"flow_id": "f1", "reason": "abuse"}); status != http.StatusOK {
		t.Fatalf("block status=%d", status)
	}
	if len(controller.blocked) != 1 || len(controller.cleared) != 1 {
		t.Fatalf("unexpected controller calls: %+v", controller)
	}
}

type recordingSlogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingSlogHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, record.Clone())
	h.mu.Unlock()
	return nil
}

func (h *recordingSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordingSlogHandler) WithGroup(string) slog.Handler { return h }

func (h *recordingSlogHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

func TestHTTPServiceFlowControlAuditFields(t *testing.T) {
	service, _, _, _ := newHTTPTagServiceTest(t)
	handler := &recordingSlogHandler{}
	service.logger = util.ComponentLogger(slog.New(handler), util.CompControl)
	service.SetFlowController(&recordingFlowController{})
	server := httptest.NewServer(http.HandlerFunc(service.HandleRPC))
	defer server.Close()

	for _, request := range []struct {
		method string
		params map[string]any
	}{
		{"SetFlowLimit", map[string]any{"flow_id": "f1", "rate_bps": 1000}},
		{"ClearFlowLimit", map[string]any{"flow_id": "f1"}},
		{"BlockFlow", map[string]any{"flow_id": "f1", "reason": "abuse"}},
	} {
		if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", request.method, request.params); status != http.StatusOK {
			t.Fatalf("%s status=%d", request.method, status)
		}
	}
	records := handler.snapshot()
	if len(records) < 3 {
		t.Fatalf("audit records=%d, want at least 3", len(records))
	}
	want := map[string]map[string]any{
		"set_flow_limit":   {"flow.id": flow.ID("f1"), "flow.route": "web", "flow.upstream": "primary", "backend.identity": "caddy", "rate_bps": uint64(1000), "result": "applied", "request.id": "test-request", "rpc.method": "SetFlowLimit"},
		"clear_flow_limit": {"flow.id": flow.ID("f1"), "flow.route": "web", "flow.upstream": "primary", "backend.identity": "caddy", "result": "applied", "request.id": "test-request", "rpc.method": "ClearFlowLimit"},
		"block_flow":       {"flow.id": flow.ID("f1"), "flow.route": "web", "flow.upstream": "primary", "backend.identity": "caddy", "reason": "abuse", "result": "applied", "request.id": "test-request", "rpc.method": "BlockFlow"},
	}
	found := make(map[string]bool)
	for _, record := range records {
		attrs := make(map[string]any)
		record.Attrs(func(attr slog.Attr) bool {
			attrs[attr.Key] = attr.Value.Resolve().Any()
			return true
		})
		fields, ok := want[record.Message]
		if !ok {
			continue
		}
		for key, value := range fields {
			if attrs[key] != value {
				t.Fatalf("%s audit %s=%v, want %v (all=%v)", record.Message, key, attrs[key], value, attrs)
			}
		}
		found[record.Message] = true
	}
	for event := range want {
		if !found[event] {
			t.Fatalf("missing %s audit event", event)
		}
	}
}

func TestHTTPServiceFlowControlsValidateStateAndAuthorization(t *testing.T) {
	service, registry, _, _ := newHTTPTagServiceTest(t)
	service.SetFlowController(&recordingFlowController{})
	server := httptest.NewServer(http.HandlerFunc(service.HandleRPC))
	defer server.Close()

	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "SetFlowLimit", map[string]any{"flow_id": "f1", "rate_bps": 0}); status != http.StatusBadRequest {
		t.Fatalf("zero rate status=%d", status)
	}
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "other-secret", "BlockFlow", map[string]any{"flow_id": "f1"}); status != http.StatusForbidden {
		t.Fatalf("cross route block status=%d", status)
	}
	service.SetFlowController(nil)
	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "BlockFlow", map[string]any{"flow_id": "f1"}); status != http.StatusServiceUnavailable {
		t.Fatalf("missing controller status=%d", status)
	}
	service.SetFlowController(&recordingFlowController{})
	for _, method := range []string{"SetFlowLimit", "ClearFlowLimit", "BlockFlow"} {
		params := map[string]any{"flow_id": "missing"}
		if method == "SetFlowLimit" {
			params["rate_bps"] = 1000
		}
		if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", method, params); status != http.StatusNotFound {
			t.Fatalf("%s missing flow status=%d", method, status)
		}
	}
	registry.Close(flow.Summary{Meta: testMeta("f1", flow.ProtocolTCP), EndedAt: time.Now().UTC()})
	for _, method := range []string{"SetFlowLimit", "ClearFlowLimit", "BlockFlow"} {
		params := map[string]any{"flow_id": "f1"}
		if method == "SetFlowLimit" {
			params["rate_bps"] = 1000
		}
		if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", method, params); status != http.StatusConflict {
			t.Fatalf("%s closed flow status=%d", method, status)
		}
	}

	storeService, _, _, _ := newHTTPTagServiceTest(t)
	storeService.SetFlowController(&recordingFlowController{})
	storeService.store = nil
	storeServer := httptest.NewServer(http.HandlerFunc(storeService.HandleRPC))
	defer storeServer.Close()
	if status, _ := callHTTPRPC(t, storeServer.Client(), storeServer.URL, "backend-secret", "SetFlowLimit", map[string]any{"flow_id": "f1", "rate_bps": 1000}); status != http.StatusServiceUnavailable {
		t.Fatalf("missing store status=%d", status)
	}
}

func TestHTTPServiceControlFailureAuditCorrelation(t *testing.T) {
	service, _, _, _ := newHTTPTagServiceTest(t)
	handler := &recordingSlogHandler{}
	service.logger = util.ComponentLogger(slog.New(handler), util.CompControl)
	service.SetFlowController(&recordingFlowController{fail: true})
	server := httptest.NewServer(http.HandlerFunc(service.HandleRPC))
	defer server.Close()

	if status, _ := callHTTPRPC(t, server.Client(), server.URL, "backend-secret", "BlockFlow", map[string]any{"flow_id": "f1"}); status != http.StatusConflict {
		t.Fatalf("failed block status=%d", status)
	}
	for _, record := range handler.snapshot() {
		if record.Message != "flow_context.request_completed" {
			continue
		}
		attrs := make(map[string]any)
		record.Attrs(func(attr slog.Attr) bool {
			attrs[attr.Key] = attr.Value.Resolve().Any()
			return true
		})
		if attrs["request.id"] != "test-request" || attrs["rpc.method"] != "BlockFlow" || fmt.Sprint(attrs["http.status_code"]) != "409" {
			t.Fatalf("incomplete failure audit: %v", attrs)
		}
		return
	}
	t.Fatal("missing request completion audit")
}

func TestHTTPServiceResolveRouteAndRPCMethod(t *testing.T) {
	service, _, _, tuple := newHTTPTagServiceTest(t)
	resolve := httptest.NewServer(http.HandlerFunc(service.HandleResolve))
	defer resolve.Close()
	body, _ := json.Marshal(ResolveFlowRequest{Protocol: tuple.Protocol, BackendKey: tuple.BackendKey, LocalAddr: tuple.LocalAddr.String(), RemoteAddr: tuple.RemoteAddr.String()})
	request, _ := http.NewRequest(http.MethodPost, resolve.URL, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer backend-secret")
	response, err := resolve.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("resolve status=%d", response.StatusCode)
	}
	tooLarge := httptest.NewRequest(http.MethodPost, "/flow-context/resolve", strings.NewReader(strings.Repeat("x", int(maxFlowContextBodyBytes)+1)))
	tooLarge.Header.Set("Authorization", "Bearer backend-secret")
	tooLargeRecorder := httptest.NewRecorder()
	service.HandleResolve(tooLargeRecorder, tooLarge)
	if tooLargeRecorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit status=%d", tooLargeRecorder.Code)
	}
	missing := httptest.NewRequest(http.MethodPost, "/flow-context/resolve", strings.NewReader(`{"protocol":"tcp","backend_key":"primary@192.0.2.10:443","local_addr":"10.0.0.1:9999","remote_addr":"192.0.2.10:443"}`))
	missing.Header.Set("Authorization", "Bearer backend-secret")
	missingRecorder := httptest.NewRecorder()
	service.HandleResolve(missingRecorder, missing)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing tuple status=%d", missingRecorder.Code)
	}
}
