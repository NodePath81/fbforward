//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
)

func TestOnlineDenyExpiresAndRestoresForwarding(t *testing.T) {
	echo := startTCPEcho(t)
	controlPort := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	config := staticConfig(staticConfigOptions{
		hostname:     "e2e-online",
		protocol:     "tcp",
		listenerName: "tcp",
		listenerPort: echo.port,
		controlPort:  controlPort,
		upstreamHost: "127.0.0.2",
		auditPath:    dbPath,
	})
	forwarder := startForwarder(t, config, controlPort)
	waitForIdentity(t, forwarder)

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
	_ = connection.Close()
	forwarder.rpc(t, "e2e-control-token", "ExpireOnlineRule", map[string]any{"rule_id": "e2e-expiring-deny"})
	store, err := audit.NewStore(dbPath)
	if err != nil {
		t.Fatalf("open audit store for rule events: %v", err)
	}
	defer store.Close()
	events, err := store.QueryOnlineRuleEvents("e2e-expiring-deny")
	if err != nil {
		t.Fatalf("query online rule events: %v", err)
	}
	if len(events) != 2 || events[0].Operation != "create" || events[1].Operation != "expire" {
		t.Fatalf("unexpected online rule events: %+v", events)
	}
}
