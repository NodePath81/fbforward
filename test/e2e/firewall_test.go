//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFirewallRejectsAndAuditsTCPConnection(t *testing.T) {
	echo := startTCPEcho(t)
	controlPort := freeTCPPort(t)
	forwardPort := echo.port

	policyPath := filepath.Join(t.TempDir(), "firewall.yaml")
	policy := []byte(`version: 1
default: allow
rules: []
`)
	if err := os.WriteFile(policyPath, policy, 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "audit.db")
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
      host: 127.0.0.2

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

ip_log:
  enabled: true
  db_path: %s
  batch_size: 1
  flush_interval: 10ms

firewall:
  enabled: true
  policy_file: %s
  fail_on_initial_load: true
`, forwardPort, controlPort, dbPath, policyPath)
	forwarder := startForwarder(t, config, controlPort)
	waitForIdentity(t, forwarder)

	flowA, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", forwardPort), time.Second)
	if err != nil {
		t.Fatalf("dial Flow A: %v", err)
	}
	if got := echoPayload(t, flowA, "stage-14-before-reload"); got != "stage-14-before-reload" {
		t.Fatalf("Flow A before reload: %q", got)
	}

	denyPolicy := []byte(`version: 1
default: allow
rules:
  - id: reject-e2e
    action: deny
    match:
      source_cidr: 127.0.0.0/8
`)
	temporaryPolicy := policyPath + ".new"
	if err := os.WriteFile(temporaryPolicy, denyPolicy, 0o600); err != nil {
		t.Fatalf("write replacement policy: %v", err)
	}
	if err := os.Rename(temporaryPolicy, policyPath); err != nil {
		t.Fatalf("replace policy: %v", err)
	}
	forwarder.rpc(t, "e2e-control-token", "ReloadFirewallPolicy", nil)
	if got := echoPayload(t, flowA, "stage-14-after-reload"); got != "stage-14-after-reload" {
		t.Fatalf("Flow A after reload: %q", got)
	}
	_ = flowA.Close()

	// The firewall rejects Flow B before the upstream is selected or dialed.
	flowB, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", forwardPort), time.Second)
	if err == nil {
		_ = flowB.Close()
	}

	var result struct {
		Total   int `json:"total"`
		Records []struct {
			Reason           string `json:"reason"`
			MatchedRuleValue string `json:"matched_rule_value"`
		} `json:"records"`
	}
	waitForInterval(t, 5*time.Second, 300*time.Millisecond, func() bool {
		raw := forwarder.rpc(t, "e2e-control-token", "QueryRejectionLog", map[string]any{"limit": 10})
		result = struct {
			Total   int `json:"total"`
			Records []struct {
				Reason           string `json:"reason"`
				MatchedRuleValue string `json:"matched_rule_value"`
			} `json:"records"`
		}{}
		return json.Unmarshal(raw, &result) == nil && result.Total > 0
	})
	if result.Records[0].Reason != "firewall_deny" {
		t.Fatalf("rejection reason = %q, want firewall_deny", result.Records[0].Reason)
	}
	if result.Records[0].MatchedRuleValue != "127.0.0.0/8" {
		t.Fatalf("matched rule = %q, want policy CIDR", result.Records[0].MatchedRuleValue)
	}
}
