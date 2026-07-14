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
	if err := resolved.SetLimit(context.Background(), 1_000_000); err != nil {
		t.Fatalf("set flow limit: %v", err)
	}
	if err := resolved.ClearLimit(context.Background()); err != nil {
		t.Fatalf("clear flow limit: %v", err)
	}
	if err := resolved.Block(context.Background(), "e2e block"); err != nil {
		t.Fatalf("block flow: %v", err)
	}
	closed, err := clients.ResolveConn(context.Background(), backendConnection)
	if err != nil {
		t.Fatalf("resolve closed flow context: %v", err)
	}
	if closed.ID != resolved.ID || closed.State != "closed" {
		t.Fatalf("unexpected closed grace context: %+v", closed)
	}
	_ = connection.Close()
	_ = backendConnection.Close()

	var audit struct {
		Total   int `json:"total"`
		Records []struct {
			CloseReason string `json:"close_reason"`
		} `json:"records"`
	}
	waitForInterval(t, 5*time.Second, 300*time.Millisecond, func() bool {
		raw := forwarder.rpc(t, "e2e-control-token", "QueryIPLog", map[string]any{"tag": "app:case=e2e", "limit": 10})
		if json.Unmarshal(raw, &audit) != nil || audit.Total != 1 {
			return false
		}
		for _, record := range audit.Records {
			if record.CloseReason == "backend_blocked" {
				return true
			}
		}
		return false
	})
}

func TestFlowContextResolvesAndTagsUDPFlow(t *testing.T) {
	echo := startUDPEcho(t)
	controlPort := freeTCPPort(t)
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	config := staticConfig(staticConfigOptions{
		hostname:     "e2e-context-udp",
		protocol:     "udp",
		listenerName: "udp",
		listenerPort: echo.port,
		controlPort:  controlPort,
		upstreamHost: "127.0.0.2",
		auditPath:    dbPath,
		udpIdle:      "100ms",
		flowContext:  true,
	})
	forwarder := startForwarder(t, config, controlPort)
	waitForIdentity(t, forwarder)

	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: echo.port})
	if err != nil {
		t.Fatalf("dial forwarded UDP listener: %v", err)
	}
	payload := []byte("stage-14-flow-context-udp")
	if _, err := connection.Write(payload); err != nil {
		connection.Close()
		t.Fatalf("write UDP flow payload: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		connection.Close()
		t.Fatalf("set UDP read deadline: %v", err)
	}
	response := make([]byte, len(payload))
	if _, _, err := connection.ReadFromUDP(response); err != nil {
		connection.Close()
		t.Fatalf("read UDP flow payload: %v", err)
	}
	if string(response) != string(payload) {
		connection.Close()
		t.Fatalf("unexpected UDP flow response %q", response)
	}

	var backendSource *net.UDPAddr
	select {
	case backendSource = <-echo.received:
	case <-time.After(2 * time.Second):
		connection.Close()
		t.Fatal("did not observe UDP source address at backend")
	}
	source, err := netip.ParseAddrPort(backendSource.String())
	if err != nil {
		connection.Close()
		t.Fatalf("parse UDP backend source address: %v", err)
	}
	destination, err := netip.ParseAddrPort(echo.connection.LocalAddr().String())
	if err != nil {
		connection.Close()
		t.Fatalf("parse UDP backend destination address: %v", err)
	}
	backendKey := "local@" + destination.String()
	clients, err := flowcontextclient.NewClientSet([]flowcontextclient.InstanceOptions{{
		Name:       "edge-e2e-udp",
		SourceAddr: source.Addr(),
		Client: flowcontextclient.Options{
			Endpoint:   forwarder.baseURL,
			Token:      "e2e-backend-token",
			BackendKey: backendKey,
		},
	}})
	if err != nil {
		connection.Close()
		t.Fatalf("create UDP flow context client set: %v", err)
	}
	resolved, err := clients.ResolveBackendTuple(context.Background(), "udp", source, destination)
	if err != nil {
		connection.Close()
		t.Fatalf("resolve UDP flow context: %v", err)
	}
	if resolved.Instance != "edge-e2e-udp" || resolved.ID == "" || resolved.Protocol != "udp" || resolved.State != "active" {
		connection.Close()
		t.Fatalf("unexpected UDP flow context: %+v", resolved)
	}
	if err := resolved.SetFlowTag(context.Background(), flowcontextclient.Tag{
		Namespace: "app", Key: "user", Value: "test",
	}); err != nil {
		connection.Close()
		t.Fatalf("set UDP flow tag: %v", err)
	}
	_ = connection.Close()

	var audit struct {
		Total   int `json:"total"`
		Records []struct {
			Protocol  string `json:"protocol"`
			Route     string `json:"route"`
			Upstream  string `json:"upstream"`
			BytesUp   uint64 `json:"bytes_up"`
			BytesDown uint64 `json:"bytes_down"`
		} `json:"records"`
	}
	waitForInterval(t, 5*time.Second, 300*time.Millisecond, func() bool {
		raw := forwarder.rpc(t, "e2e-control-token", "QueryIPLog", map[string]any{"tag": "app:user=test", "limit": 10})
		if json.Unmarshal(raw, &audit) != nil || audit.Total != 1 || len(audit.Records) != 1 {
			return false
		}
		record := audit.Records[0]
		return record.Protocol == "udp" && record.Route == "local" && record.Upstream == "local" && record.BytesUp >= uint64(len(payload)) && record.BytesDown >= uint64(len(payload))
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
