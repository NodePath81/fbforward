package control

import (
	"encoding/json"
	"net/http"
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
	rec := callTestRPC(t, server, "0123456789abcdef", "SetUpstream", params)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	return response
}

func TestSetUpstreamModesRPC(t *testing.T) {
	tests := []struct {
		name       string
		initial    recordingManager
		params     map[string]any
		wantMode   upstream.Mode
		wantTag    string
		wantAuto   int
		wantManual int
	}{
		{name: "auto", initial: recordingManager{mode: upstream.ModeManual, activeTag: "primary"}, params: map[string]any{"mode": "auto"}, wantMode: upstream.ModeAuto, wantAuto: 1},
		{name: "manual", initial: recordingManager{mode: upstream.ModeAuto}, params: map[string]any{"mode": "manual", "tag": "backup"}, wantMode: upstream.ModeManual, wantTag: "backup", wantManual: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			manager := testCase.initial
			response := callSetUpstreamRPC(t, newRecordingControlServer(t, &manager), testCase.params)
			if !response.Ok || response.Error != "" {
				t.Fatalf("unexpected response: %+v", response)
			}
			if manager.mode != testCase.wantMode || manager.activeTag != testCase.wantTag {
				t.Fatalf("unexpected selection: mode=%s active=%q", manager.mode, manager.activeTag)
			}
			if manager.autoCalls != testCase.wantAuto || manager.manualCalls != testCase.wantManual {
				t.Fatalf("unexpected manager calls: auto=%d manual=%d", manager.autoCalls, manager.manualCalls)
			}
		})
	}
}
