//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestStartupServesIdentityAndEmbeddedAssets(t *testing.T) {
	controlPort := freeTCPPort(t)
	forwardPort := freeTCPPort(t)
	config := fmt.Sprintf(`hostname: e2e

listeners:
  - name: e2e
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
      host: 127.0.0.1

forwarding:
  limits:
    max_tcp_connections: 10
    max_udp_mappings: 10
  idle_timeout:
    tcp: 5s
    udp: 5s

control:
  bind_addr: 127.0.0.1
  bind_port: %d
  auth_token: e2e-control-token
  metrics:
    enabled: true

firewall:
  enabled: false
`, forwardPort, controlPort)
	forwarder := startForwarder(t, config, controlPort)

	client := &http.Client{Timeout: 500 * time.Millisecond}
	baseURL := forwarder.baseURL
	waitFor(t, 5*time.Second, func() bool {
		request, err := http.NewRequest(http.MethodGet, baseURL+"/identity", nil)
		if err != nil {
			return false
		}
		request.Header.Set("Authorization", "Bearer e2e-control-token")
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK
	})

	var status struct {
		Routes []struct {
			Name string `json:"route"`
		} `json:"routes"`
	}
	if raw := forwarder.rpc(t, "e2e-control-token", "GetStatus", nil); json.Unmarshal(raw, &status) != nil || len(status.Routes) != 1 || status.Routes[0].Name != "local" {
		t.Fatalf("unexpected GetStatus route response: %s", raw)
	}
	metricsRequest, err := http.NewRequest(http.MethodGet, baseURL+"/metrics", nil)
	if err != nil {
		t.Fatalf("create metrics request: %v", err)
	}
	metricsRequest.Header.Set("Authorization", "Bearer e2e-control-token")
	metricsResponse, err := client.Do(metricsRequest)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	metricsBody, readErr := io.ReadAll(metricsResponse.Body)
	_ = metricsResponse.Body.Close()
	if readErr != nil || metricsResponse.StatusCode != http.StatusOK || len(metricsBody) == 0 {
		t.Fatalf("GET /metrics: status=%d body=%d err=%v", metricsResponse.StatusCode, len(metricsBody), readErr)
	}

	for _, path := range []string{"/", "/app.js"} {
		response, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		if response.StatusCode != http.StatusOK || len(body) == 0 {
			t.Fatalf("GET %s: status=%d body=%d", path, response.StatusCode, len(body))
		}
	}
}
