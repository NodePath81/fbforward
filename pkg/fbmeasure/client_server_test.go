package fbmeasure

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
)

func startTestServer(t *testing.T) (*Server, context.CancelFunc, <-chan error) {
	t.Helper()
	server, err := NewServer(ServerConfig{ListenAddress: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	return server, cancel, done
}

func TestClientServerTCPAndUDPRoundTrip(t *testing.T) {
	server, _, _ := startTestServer(t)
	client, err := NewClient(ClientConfig{Address: server.Addr().String()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	tcpResult, err := client.ProbeTCP(context.Background())
	if err != nil {
		t.Fatalf("ProbeTCP: %v", err)
	}
	if !tcpResult.Reachable || tcpResult.RTT <= 0 || tcpResult.Protocol != ProtocolTCP {
		t.Fatalf("unexpected TCP result: %+v", tcpResult)
	}
	udpResult, err := client.ProbeUDP(context.Background())
	if err != nil {
		t.Fatalf("ProbeUDP: %v", err)
	}
	if !udpResult.Reachable || udpResult.RTT <= 0 || udpResult.Protocol != ProtocolUDP {
		t.Fatalf("unexpected UDP result: %+v", udpResult)
	}
}

func TestClientRejectsInvalidTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{time.Nanosecond, 11 * time.Second} {
		if _, err := NewClient(ClientConfig{Address: "127.0.0.1:9876", Timeout: timeout}); err == nil {
			t.Fatalf("timeout %s was accepted", timeout)
		}
	}
}

func TestServerUsesOneNumericPort(t *testing.T) {
	server, _, _ := startTestServer(t)
	addr := server.Addr()
	tcpConn, err := net.DialTimeout("tcp", addr.String(), time.Second)
	if err != nil {
		t.Fatalf("TCP dial: %v", err)
	}
	_ = tcpConn.Close()
	udpConn, err := net.DialTimeout("udp", addr.String(), time.Second)
	if err != nil {
		t.Fatalf("UDP dial: %v", err)
	}
	_ = udpConn.Close()
}

func TestClientRejectsOversizedUDPEcho(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		var buf [64 * 1024]byte
		for {
			n, addr, readErr := listener.ReadFromUDP(buf[:])
			if readErr != nil {
				return
			}
			response := append(append([]byte(nil), buf[:n]...), 0)
			_, _ = listener.WriteToUDP(response, addr)
		}
	}()

	client, err := NewClient(ClientConfig{
		Address: net.JoinHostPort("127.0.0.1", fmt.Sprint(listener.LocalAddr().(*net.UDPAddr).Port)),
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	result, err := client.ProbeUDP(context.Background())
	_ = client.Close()
	_ = listener.Close()
	<-serverDone
	if err == nil || result.Reachable {
		t.Fatalf("oversized UDP echo was accepted: result=%+v err=%v", result, err)
	}
}
