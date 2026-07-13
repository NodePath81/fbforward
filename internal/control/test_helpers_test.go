package control

import (
	"context"
	"encoding/json"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/notify"
	"github.com/NodePath81/fbforward/internal/upstream"
	"testing"
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
