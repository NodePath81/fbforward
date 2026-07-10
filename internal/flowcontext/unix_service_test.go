package flowcontext

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/flow"
)

func newUnixServiceTest(t *testing.T) (*UnixService, *Registry, *audit.Store, flow.BackendTuple, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := audit.NewStore(filepath.Join(dir, "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(Options{CleanupInterval: time.Millisecond, GracePeriod: time.Second})
	meta := testMeta("f1", flow.ProtocolTCP)
	tuple := testTuple(flow.ProtocolTCP)
	registry.Open(meta)
	if err := registry.Bind(meta.ID, tuple); err != nil {
		t.Fatal(err)
	}
	service := NewUnixService(registry, store, UnixOptions{
		SocketPath:        filepath.Join(dir, "flow-context.sock"),
		AuthToken:         "secret",
		AllowedNamespaces: []string{"app"},
		RateLimitBurst:    100,
	}, nil)
	return service, registry, store, tuple, dir
}

func unixRPCClient(t *testing.T, service *UnixService) (*http.Client, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("unix", service.options.SocketPath)
	}}
	return &http.Client{Transport: transport}, cancel
}

func callUnixRPC(t *testing.T, client *http.Client, token, method string, params any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://unix/v1/rpc", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
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

func identityFor(tuple flow.BackendTuple) BackendIdentity {
	return BackendIdentity{BackendKey: tuple.BackendKey, Upstream: "primary", Route: ""}
}

func tagParams(tuple flow.BackendTuple, value string) map[string]any {
	return map[string]any{
		"flow_id":   "f1",
		"identity":  identityFor(tuple),
		"namespace": "app",
		"key":       "owner",
		"value":     value,
	}
}

func TestUnixServiceTagLifecycleAndAuditPersistence(t *testing.T) {
	service, registry, store, tuple, _ := newUnixServiceTest(t)
	defer registry.Shutdown()
	defer store.Close()
	client, cancel := unixRPCClient(t, service)
	defer cancel()
	defer service.Shutdown(context.Background())

	status, response := callUnixRPC(t, client, "secret", "ResolveFlow", map[string]any{
		"protocol": tuple.Protocol, "backend_key": tuple.BackendKey,
		"local_addr": tuple.LocalAddr.String(), "remote_addr": tuple.RemoteAddr.String(),
		"identity": identityFor(tuple),
	})
	if status != http.StatusOK || response["ok"] != true {
		t.Fatalf("resolve status=%d response=%v", status, response)
	}
	if status, response = callUnixRPC(t, client, "secret", "SetFlowTag", tagParams(tuple, "alice")); status != http.StatusOK || response["ok"] != true {
		t.Fatalf("set status=%d response=%v", status, response)
	}
	if status, response = callUnixRPC(t, client, "secret", "SetFlowTag", tagParams(tuple, "bob")); status != http.StatusOK || response["ok"] != true {
		t.Fatalf("replace status=%d response=%v", status, response)
	}
	tags, err := store.QueryFlowTags("f1")
	if err != nil || len(tags) != 1 || tags[0].Tag != "app:owner=bob" {
		t.Fatalf("stored tags=%+v err=%v", tags, err)
	}
	if err := store.InsertFlows([]audit.FlowRecord{{FlowID: "f1", Protocol: "tcp", ClientIP: "203.0.113.10", ClientPort: 45678, Listener: "0.0.0.0:443", Upstream: "primary", StartedAt: time.Now().Add(-time.Second), EndedAt: time.Now(), LastActivity: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if tags, err = store.QueryFlowTags("f1"); err != nil || len(tags) != 1 || tags[0].Tag != "app:owner=bob" {
		t.Fatalf("tags lost after flow upsert=%+v err=%v", tags, err)
	}
	if status, response = callUnixRPC(t, client, "secret", "ListFlowTags", map[string]any{"flow_id": "f1", "identity": identityFor(tuple)}); status != http.StatusOK || response["ok"] != true {
		t.Fatalf("list status=%d response=%v", status, response)
	}
	if status, response = callUnixRPC(t, client, "secret", "UnsetFlowTag", tagParams(tuple, "")); status != http.StatusOK || response["ok"] != true {
		t.Fatalf("unset status=%d response=%v", status, response)
	}
	if tags, err = store.QueryFlowTags("f1"); err != nil || len(tags) != 0 {
		t.Fatalf("tags after unset=%+v err=%v", tags, err)
	}
	if events, err := store.QueryFlowTagEvents("f1"); err != nil || len(events) < 3 {
		t.Fatalf("tag audit events=%+v err=%v", events, err)
	}
	registry.Close(flow.Summary{Meta: testMeta("f1", flow.ProtocolTCP), EndedAt: time.Now().UTC()})
	if status, response = callUnixRPC(t, client, "secret", "SetFlowTag", tagParams(tuple, "after-close")); status != http.StatusOK || response["ok"] != true {
		t.Fatalf("closed flow tag status=%d response=%v", status, response)
	}

	clientTag := tagParams(tuple, "customer")
	clientTag["key"] = "class"
	clientTag["ttl_seconds"] = 1
	if status, response = callUnixRPC(t, client, "secret", "SetClientTag", clientTag); status != http.StatusOK || response["ok"] != true {
		t.Fatalf("client tag status=%d response=%v", status, response)
	}
	clientTags, err := store.QueryClientTags("203.0.113.10")
	if err != nil || len(clientTags) != 1 {
		t.Fatalf("client tags=%+v err=%v", clientTags, err)
	}
	time.Sleep(1100 * time.Millisecond)
	if clientTags, err = store.QueryClientTags("203.0.113.10"); err != nil || len(clientTags) != 0 {
		t.Fatalf("expired client tags=%+v err=%v", clientTags, err)
	}
}

func TestUnixServiceAuthorizationAndValidation(t *testing.T) {
	service, registry, store, tuple, dir := newUnixServiceTest(t)
	defer registry.Shutdown()
	defer store.Close()
	client, cancel := unixRPCClient(t, service)
	defer cancel()
	defer service.Shutdown(context.Background())

	wrong := tagParams(tuple, "x")
	wid := wrong["identity"].(BackendIdentity)
	wid.Upstream = "backup"
	wrong["identity"] = wid
	status, response := callUnixRPC(t, client, "secret", "SetFlowTag", wrong)
	if status != http.StatusForbidden || response["ok"] != false {
		t.Fatalf("wrong backend status=%d response=%v", status, response)
	}
	invalid := tagParams(tuple, "x")
	invalid["namespace"] = "not-allowed"
	status, response = callUnixRPC(t, client, "secret", "SetFlowTag", invalid)
	if status != http.StatusBadRequest || response["ok"] != false {
		t.Fatalf("invalid namespace status=%d response=%v", status, response)
	}
	status, response = callUnixRPC(t, client, "secret", "SetFlowTag", tagParams(tuple, "x"))
	if status != http.StatusOK || response["ok"] != true {
		t.Fatalf("valid tag status=%d response=%v", status, response)
	}
	if info, err := os.Stat(filepath.Join(dir, "flow-context.sock")); err != nil || info.Mode().Perm() != 0660 {
		t.Fatalf("socket mode=%v err=%v", info, err)
	}
	status, _ = callUnixRPC(t, client, "wrong", "ListFlowTags", map[string]any{"flow_id": "f1", "identity": identityFor(tuple)})
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d", status)
	}
}

func TestUnixServiceRequiresParamsAndBodyLimit(t *testing.T) {
	service, registry, store, _, _ := newUnixServiceTest(t)
	defer registry.Shutdown()
	defer store.Close()
	client, cancel := unixRPCClient(t, service)
	defer cancel()
	defer service.Shutdown(context.Background())
	status, _ := callUnixRPC(t, client, "secret", "SetFlowTag", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("missing params status=%d", status)
	}

	body := strings.Repeat("x", int(maxUnixRequestBytes)+1)
	request, err := http.NewRequest(http.MethodPost, "http://unix/v1/rpc", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("body status=%d body=%s", response.StatusCode, data)
	}
}

func TestUnixServiceRateLimit(t *testing.T) {
	service, registry, store, tuple, _ := newUnixServiceTest(t)
	service.options.RateLimitBurst = 1
	service.limiter = newTagRateLimiter(1, time.Minute)
	defer registry.Shutdown()
	defer store.Close()
	client, cancel := unixRPCClient(t, service)
	defer cancel()
	defer service.Shutdown(context.Background())
	params := map[string]any{"flow_id": "f1", "identity": identityFor(tuple)}
	if status, _ := callUnixRPC(t, client, "secret", "ListFlowTags", params); status != http.StatusOK {
		t.Fatalf("first request status=%d", status)
	}
	if status, _ := callUnixRPC(t, client, "secret", "ListFlowTags", params); status != http.StatusTooManyRequests {
		t.Fatalf("second request status=%d", status)
	}
}
