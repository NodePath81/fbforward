//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/pkg/flowcontextclient"
)

func TestFlowContextResolvesAndTagsTCPFlow(t *testing.T) {
	echo := startTCPEcho(t)
	controlPort := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	config := staticConfig(staticConfigOptions{
		hostname:     "e2e-context",
		protocol:     "tcp",
		listenerName: "tcp",
		listenerPort: echo.port,
		controlPort:  controlPort,
		upstreamHost: "127.0.0.2",
		auditPath:    dbPath,
		flowContext:  true,
	})
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
	backendKey := "local@" + backendConnection.LocalAddr().String()
	backendSource, err := netip.ParseAddrPort(backendConnection.RemoteAddr().String())
	if err != nil {
		t.Fatalf("parse backend source address: %v", err)
	}
	clients, err := flowcontextclient.NewClientSet([]flowcontextclient.InstanceOptions{{
		Name:       "edge-e2e",
		SourceAddr: backendSource.Addr(),
		Client: flowcontextclient.Options{
			Endpoint:   forwarder.baseURL,
			Token:      "e2e-backend-token",
			BackendKey: backendKey,
		},
	}})
	if err != nil {
		t.Fatalf("create flow context client set: %v", err)
	}
	resolved, err := clients.ResolveConn(context.Background(), backendConnection)
	if err != nil {
		t.Fatalf("resolve flow context: %v", err)
	}
	if resolved.Instance != "edge-e2e" || resolved.ID == "" || resolved.State != "active" {
		t.Fatalf("unexpected flow context: %+v", resolved)
	}
	if err := resolved.SetFlowTag(context.Background(), flowcontextclient.Tag{
		Namespace: "app", Key: "case", Value: "e2e",
	}); err != nil {
		t.Fatalf("set flow tag: %v", err)
	}
	_ = connection.Close()
	_ = backendConnection.Close()
	closed, err := clients.ResolveConn(context.Background(), backendConnection)
	if err != nil {
		t.Fatalf("resolve closed flow context: %v", err)
	}
	if closed.ID != resolved.ID || closed.State != "closed" {
		t.Fatalf("unexpected closed grace context: %+v", closed)
	}

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
