package control

import (
	"net"
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
