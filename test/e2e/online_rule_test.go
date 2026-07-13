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

func TestOnlineDenyExpiresAndRestoresForwarding(t *testing.T) {
	echo := startTCPEcho(t)
	controlPort := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	config := fmt.Sprintf(`hostname: e2e-online

listeners:
  - name: tcp
    bind: 127.0.0.1:%d
    protocol: tcp
    route: local

routes:
  - name: local
    strategy: static
    upstreams: [local]

upstreams:
  - tag: local
    destination:
      host: 127.0.0.2

control:
  bind_addr: 127.0.0.1
  bind_port: %d
  auth_token: e2e-control-token

ip_log:
  enabled: true
  db_path: %s
  batch_size: 1
  flush_interval: 10ms

firewall:
  enabled: false
`, echo.port, controlPort, dbPath)
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

	forwarder.rpc(t, "e2e-control-token", "CreateOnlineRule", map[string]any{
		"rule_id":     "e2e-expiring-deny",
		"action":      "deny",
		"matcher":     map[string]any{"source_ip": "127.0.0.1", "protocol": "tcp"},
		"ttl_seconds": 1,
		"reason":      "stage-14-e2e",
	})
	if connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(echo.port)), time.Second); err == nil {
		_ = connection.Close()
	}

	var listed []struct {
		RuleID string `json:"rule_id"`
		State  string `json:"state"`
	}
	waitForInterval(t, 3*time.Second, 300*time.Millisecond, func() bool {
		raw := forwarder.rpc(t, "e2e-control-token", "ListOnlineRules", map[string]any{"include_expired": true})
		listed = nil
		if err := json.Unmarshal(raw, &listed); err != nil {
			return false
		}
		return len(listed) == 1 && listed[0].State == "expired"
	})
	if len(listed) != 1 || listed[0].RuleID != "e2e-expiring-deny" || listed[0].State != "expired" {
		t.Fatalf("unexpected expired rule state: %#v", listed)
	}

	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(echo.port)), time.Second)
	if err != nil {
		t.Fatalf("dial after online rule expiry: %v", err)
	}
	defer connection.Close()
	payload := []byte("stage-14-online-expired")
	if _, err := connection.Write(payload); err != nil {
		t.Fatalf("write after online rule expiry: %v", err)
	}
	response := make([]byte, len(payload))
	if _, err := connection.Read(response); err != nil {
		t.Fatalf("read after online rule expiry: %v", err)
	}
	if string(response) != string(payload) {
		t.Fatalf("unexpected response after expiry: %q", response)
	}
}
