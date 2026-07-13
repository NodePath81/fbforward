//go:build e2e

package e2e

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestAdaptiveRouteFallsBackAfterDialFailure(t *testing.T) {
	primary := startTCPEchoOn(t, "127.0.0.2")
	backup := startTCPEchoAt(t, "127.0.0.3", primary.port)
	controlPort := freeTCPPort(t)
	config := fmt.Sprintf(`hostname: e2e-adaptive

listeners:
  - name: adaptive
    bind: 127.0.0.1:%d
    protocol: tcp
    route: proxy

routes:
  - name: proxy
    strategy: adaptive
    upstreams: [primary, backup]

upstreams:
  - tag: primary
    priority: 100
    destination:
      host: 127.0.0.2
  - tag: backup
    priority: 10
    destination:
      host: 127.0.0.3

control:
  bind_addr: 127.0.0.1
  bind_port: %d
  auth_token: e2e-control-token

firewall:
  enabled: false
`, primary.port, controlPort)
	forwarder := startForwarder(t, config, controlPort)
	waitForIdentity(t, forwarder)

	first, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(primary.port)), time.Second)
	if err != nil {
		t.Fatalf("dial primary flow: %v", err)
	}
	defer first.Close()
	if got := echoPayload(t, first, "stage-14-primary"); got != "stage-14-primary" {
		t.Fatalf("initial route selected %q, want primary", got)
	}
	_ = primary.listener.Close()
	if got := echoPayload(t, first, "stage-14-pinned"); got != "stage-14-pinned" {
		t.Fatalf("existing flow moved or closed: %q", got)
	}

	// The first new Flow observes the failed primary dial and marks its
	// short cooldown; the following Flow is the one expected to fallback.
	second, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(primary.port)), time.Second)
	if err != nil {
		t.Fatalf("dial failed-primary flow: %v", err)
	}
	_ = second.Close()
	time.Sleep(200 * time.Millisecond)
	third, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(primary.port)), time.Second)
	if err != nil {
		t.Fatalf("dial fallback flow: %v", err)
	}
	defer third.Close()
	if got := echoPayload(t, third, "stage-14-backup"); got != "stage-14-backup" {
		t.Fatalf("fallback route response %q, want backup echo", got)
	}
	_ = backup
}

func echoPayload(t *testing.T, connection net.Conn, payload string) string {
	t.Helper()
	if _, err := io.WriteString(connection, payload); err != nil {
		t.Fatalf("write echo payload: %v", err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatalf("read echo payload: %v", err)
	}
	return string(response)
}
