package control

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
)

func TestControlServerStartFailsWhenPortIsInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve control port: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	server := NewControlServer(config.Config{Control: config.ControlConfig{
		BindAddr: "127.0.0.1", BindPort: port, AuthToken: "0123456789abcdef",
	}}, fakeManager{}, metrics.NewMetrics(nil), NewStatusStore(), nil, nil)
	if err := server.Start(t.Context()); err == nil {
		t.Fatal("Start succeeded while control port was already in use")
	}
}

func TestControlServerHealthzReadiness(t *testing.T) {
	server := NewControlServer(config.Config{}, fakeManager{}, metrics.NewMetrics(nil), NewStatusStore(), nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	server.handleHealthz(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready health status = %d, want 503", recorder.Code)
	}

	server.SetReady(true)
	recorder = httptest.NewRecorder()
	server.handleHealthz(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("ready health status = %d, want 204", recorder.Code)
	}

	server.SetReady(false)
	recorder = httptest.NewRecorder()
	server.handleHealthz(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped health status = %d, want 503", recorder.Code)
	}
}
