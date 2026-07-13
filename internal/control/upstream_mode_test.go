package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
)

type recordingManager struct {
	mode        upstream.Mode
	activeTag   string
	manualTag   string
	autoCalls   int
	manualCalls int
}

func (m *recordingManager) SetAuto() {
	m.mode = upstream.ModeAuto
	m.activeTag = ""
	m.autoCalls++
}

func (m *recordingManager) SetManual(tag string) error {
	m.mode = upstream.ModeManual
	m.activeTag = tag
	m.manualTag = tag
	m.manualCalls++
	return nil
}

func (m *recordingManager) Snapshot() []upstream.UpstreamSnapshot { return nil }

func (m *recordingManager) Mode() upstream.Mode { return m.mode }

func (m *recordingManager) ActiveTag() string { return m.activeTag }

func (m *recordingManager) Get(string) *upstream.Upstream { return nil }

func newRecordingControlServer(t *testing.T, manager *recordingManager) *ControlServer {
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
		manager,
		metrics.NewMetrics(nil),
		NewStatusStore(),
		func() error { return nil },
		nil,
	)
}

func callSetUpstreamRPC(t *testing.T, server *ControlServer, params map[string]any) rpcResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(rpcRequestBody(t, "SetUpstream", params)))
	req.Header.Set("Authorization", "Bearer 0123456789abcdef")
	rec := httptest.NewRecorder()
	server.handleRPC(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	return response
}

func TestSetUpstreamAutoRPC(t *testing.T) {
	manager := &recordingManager{mode: upstream.ModeManual, activeTag: "primary"}
	server := newRecordingControlServer(t, manager)
	response := callSetUpstreamRPC(t, server, map[string]any{"mode": "auto"})

	if !response.Ok || response.Error != "" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if manager.mode != upstream.ModeAuto || manager.activeTag != "" {
		t.Fatalf("expected auto mode, got mode=%s active=%q", manager.mode, manager.activeTag)
	}
	if manager.autoCalls != 1 || manager.manualCalls != 0 {
		t.Fatalf("unexpected manager calls: auto=%d manual=%d", manager.autoCalls, manager.manualCalls)
	}
}

func TestSetUpstreamManualRPC(t *testing.T) {
	manager := &recordingManager{mode: upstream.ModeAuto}
	server := newRecordingControlServer(t, manager)
	response := callSetUpstreamRPC(t, server, map[string]any{"mode": "manual", "tag": "backup"})

	if !response.Ok || response.Error != "" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if manager.mode != upstream.ModeManual || manager.activeTag != "backup" || manager.manualTag != "backup" {
		t.Fatalf("expected manual backup selection, got mode=%s active=%q manual=%q", manager.mode, manager.activeTag, manager.manualTag)
	}
	if manager.autoCalls != 0 || manager.manualCalls != 1 {
		t.Fatalf("unexpected manager calls: auto=%d manual=%d", manager.autoCalls, manager.manualCalls)
	}
}
