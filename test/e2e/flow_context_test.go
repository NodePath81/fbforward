//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestFlowContextResolvesAndTagsTCPFlow(t *testing.T) {
	echo := startTCPEcho(t)
	controlPort := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	config := fmt.Sprintf(`hostname: e2e-context

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

flow_context:
  enabled: true
  max_ttl: 24h
  identities:
    - id: caddy
      token: e2e-backend-token
      routes: [local]
      upstreams: [local]
      namespaces: [app]

firewall:
  enabled: false
`, echo.port, controlPort, dbPath)
	forwarder := startForwarder(t, config, controlPort)
	waitForIdentity(t, forwarder)

	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(echo.port)), time.Second)
	if err != nil {
		t.Fatalf("dial forwarded listener: %v", err)
	}
	payload := []byte("stage-14-flow-context")
	if _, err := connection.Write(payload); err != nil {
		connection.Close()
		t.Fatalf("write flow payload: %v", err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, response); err != nil {
		connection.Close()
		t.Fatalf("read flow payload: %v", err)
	}
	backendConnection := <-echo.accepted
	localAddr := backendConnection.RemoteAddr().String()
	remoteAddr := backendConnection.LocalAddr().String()

	var resolved struct {
		OK   bool `json:"ok"`
		Flow struct {
			FlowID string `json:"flow_id"`
			State  string `json:"state"`
		} `json:"flow"`
	}
	resolve := map[string]any{
		"protocol": "tcp", "backend_key": "local@" + remoteAddr,
		"local_addr": localAddr, "remote_addr": remoteAddr,
	}
	postFlowContext(t, forwarder.baseURL+"/flow-context/resolve", "e2e-backend-token", resolve, &resolved)
	if !resolved.OK || resolved.Flow.FlowID == "" || resolved.Flow.State != "active" {
		t.Fatalf("unexpected flow context: %+v", resolved)
	}

	var tagged struct {
		OK bool `json:"ok"`
	}
	postFlowContext(t, forwarder.baseURL+"/flow-context/rpc", "e2e-backend-token", map[string]any{
		"method": "SetFlowTag",
		"params": map[string]any{"flow_id": resolved.Flow.FlowID, "namespace": "app", "key": "case", "value": "e2e"},
	}, &tagged)
	if !tagged.OK {
		t.Fatal("SetFlowTag was not accepted")
	}
	_ = connection.Close()
	_ = backendConnection.Close()

	var audit struct {
		Total int `json:"total"`
	}
	waitForInterval(t, 5*time.Second, 300*time.Millisecond, func() bool {
		raw := forwarder.rpc(t, "e2e-control-token", "QueryIPLog", map[string]any{"tag": "app:case=e2e", "limit": 10})
		return json.Unmarshal(raw, &audit) == nil && audit.Total == 1
	})
}

func waitForIdentity(t *testing.T, forwarder *forwarderProcess) {
	t.Helper()
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
}

func postFlowContext(t *testing.T, endpoint, token string, payload any, result any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal flow context request: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create flow context request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatalf("flow context request: %v", err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		t.Fatalf("decode flow context response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("flow context status: %d", response.StatusCode)
	}
}
