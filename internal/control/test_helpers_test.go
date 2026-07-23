package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/notify"
	"github.com/NodePath81/fbforward/internal/policy"
	"github.com/NodePath81/fbforward/internal/upstream"
)

type fakeManager struct {
}

type fakeGeoIPManager struct {
	status    geoip.Status
	reloadErr error
}

type fakeNotifier struct {
	eventName  string
	severity   notify.Severity
	attributes map[string]any
	accepted   bool
}

func (fakeManager) SetAuto()                              {}
func (fakeManager) SetManual(string) error                { return nil }
func (fakeManager) Snapshot() []upstream.UpstreamSnapshot { return nil }
func (fakeManager) Mode() upstream.Mode                   { return upstream.ModeAuto }
func (fakeManager) ActiveTag() string                     { return "" }
func (fakeManager) Get(string) *upstream.Upstream         { return nil }
func (f fakeGeoIPManager) Status() geoip.Status {
	return f.status
}

func (f fakeGeoIPManager) Reload(context.Context) error {
	return f.reloadErr
}

func (f *fakeNotifier) Emit(eventName string, severity notify.Severity, attributes map[string]any) bool {
	f.eventName = eventName
	f.severity = severity
	f.attributes = attributes
	return f.accepted
}

func newTestControlServer(t *testing.T) *ControlServer {
	t.Helper()
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
		NewStatusStore(),
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

func callTestRPC(t *testing.T, server *ControlServer, token, method string, params any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, method, params)))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	return rec
}

func newTestOnlineProvider(t *testing.T) (*policy.OnlineProvider, *audit.Store) {
	t.Helper()
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider, err := policy.NewOnlineProvider(store)
	if err != nil {
		t.Fatal(err)
	}
	return provider, store
}

func newTestAuditStore(t *testing.T, server *ControlServer) *audit.Store {
	t.Helper()
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "iplog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server.SetAuditStore(store)
	return store
}
