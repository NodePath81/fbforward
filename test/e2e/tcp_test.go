//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestStaticTCPForwardsToLoopbackUpstream(t *testing.T) {
	echo := startTCPEcho(t)
	controlPort := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	config := staticConfig(staticConfigOptions{
		hostname:     "e2e-tcp",
		protocol:     "tcp",
		listenerName: "tcp",
		listenerPort: echo.port,
		controlPort:  controlPort,
		upstreamHost: "127.0.0.2",
		auditPath:    dbPath,
	})
	forwarder := startForwarder(t, config, controlPort)
	waitForIdentity(t, forwarder)

	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(echo.port)), time.Second)
	if err != nil {
		t.Fatalf("dial forwarded listener: %v", err)
	}
	payload := []byte("stage-14-static-tcp")
	if _, err := connection.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatalf("read echoed payload: %v", err)
	}
	if string(response) != string(payload) {
		t.Fatalf("unexpected response %q", response)
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

type tcpEcho struct {
	listener net.Listener
	port     int
	accepted chan net.Conn
}

func startTCPEcho(t *testing.T) tcpEcho {
	return startTCPEchoOn(t, "127.0.0.2")
}

func startTCPEchoOn(t *testing.T, host string) tcpEcho {
	return startTCPEchoAt(t, host, 0)
}

func startTCPEchoAt(t *testing.T, host string, port int) tcpEcho {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		t.Fatalf("listen echo upstream: %v", err)
	}
	echo := tcpEcho{listener: listener, port: listener.Addr().(*net.TCPAddr).Port, accepted: make(chan net.Conn, 1)}
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			select {
			case echo.accepted <- connection:
			default:
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return echo
}
