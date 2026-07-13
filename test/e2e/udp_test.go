//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestStaticUDPForwardsToLoopbackUpstream(t *testing.T) {
	echo := startUDPEcho(t)
	controlPort := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	config := fmt.Sprintf(`hostname: e2e-udp

listeners:
  - name: udp
    bind: 127.0.0.1:%d
    protocol: udp
    route: local

routes:
  - name: local
    strategy: static
    upstreams: [local]

upstreams:
  - tag: local
    destination:
      host: 127.0.0.2

forwarding:
  idle_timeout:
    tcp: 5s
    udp: 100ms

ip_log:
  enabled: true
  db_path: %s
  batch_size: 1
  flush_interval: 10ms

control:
  bind_addr: 127.0.0.1
  bind_port: %d
  auth_token: e2e-control-token

firewall:
  enabled: false
`, echo.port, dbPath, controlPort)
	forwarder := startForwarder(t, config, controlPort)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	waitFor(t, 5*time.Second, func() bool {
		request, err := http.NewRequest(http.MethodGet, forwarder.baseURL+"/identity", nil)
		if err != nil {
			return false
		}
		request.Header.Set("Authorization", "Bearer e2e-control-token")
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	})

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
