//go:build e2e

package e2e

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestStaticUDPForwardsToLoopbackUpstream(t *testing.T) {
	echo := startUDPEcho(t)
	controlPort := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	config := staticConfig(staticConfigOptions{
		hostname:     "e2e-udp",
		protocol:     "udp",
		listenerName: "udp",
		listenerPort: echo.port,
		controlPort:  controlPort,
		upstreamHost: "127.0.0.2",
		auditPath:    dbPath,
		udpIdle:      "100ms",
	})
	forwarder := startForwarder(t, config, controlPort)
	waitForIdentity(t, forwarder)

	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: echo.port})
	if err != nil {
		t.Fatalf("dial forwarded listener: %v", err)
	}
	defer connection.Close()
	payload := []byte("stage-14-static-udp")
	if _, err := connection.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	response := make([]byte, 1024)
	n, _, err := connection.ReadFromUDP(response)
	if err != nil {
		t.Fatalf("read echoed payload: %v", err)
	}
	if string(response[:n]) != string(payload) {
		t.Fatalf("unexpected response %q", response[:n])
	}
	_ = connection.Close()
	var audit struct {
		Total int `json:"total"`
	}
	waitForInterval(t, 3*time.Second, 300*time.Millisecond, func() bool {
		raw := forwarder.rpc(t, "e2e-control-token", "QueryIPLog", map[string]any{"limit": 10})
		return json.Unmarshal(raw, &audit) == nil && audit.Total == 1
	})
}

type udpEcho struct {
	connection *net.UDPConn
	port       int
}

func startUDPEcho(t *testing.T) udpEcho {
	t.Helper()
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 0})
	if err != nil {
		t.Fatalf("listen echo upstream: %v", err)
	}
	echo := udpEcho{connection: connection, port: connection.LocalAddr().(*net.UDPAddr).Port}
	go func() {
		buffer := make([]byte, 64*1024)
		for {
			n, remote, readErr := connection.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			_, _ = connection.WriteToUDP(buffer[:n], remote)
		}
	}()
	t.Cleanup(func() { _ = connection.Close() })
	return echo
}
