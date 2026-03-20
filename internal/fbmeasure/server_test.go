package fbmeasure

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestServerClientRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := NewServer(Config{
		BindAddr: "127.0.0.1",
		Port:     0,
	}, nil)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		cancel()
		_ = srv.Close()
		srv.Wait()
	}()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(srv.Port()))
	opCtx, opCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer opCancel()

	client, err := Dial(opCtx, addr, ClientSecurityConfig{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	tcpRTT, err := client.PingTCP(opCtx, 3)
	if err != nil {
		t.Fatalf("PingTCP: %v", err)
	}
	if tcpRTT.Samples != 3 {
		t.Fatalf("PingTCP samples=%d", tcpRTT.Samples)
	}

	udpRTT, err := client.PingUDP(opCtx, 3)
	if err != nil {
		t.Fatalf("PingUDP: %v", err)
	}
	if udpRTT.Samples != 3 {
		t.Fatalf("PingUDP samples=%d", udpRTT.Samples)
	}

	retrans, err := client.TCPRetrans(opCtx, 64*1024)
	if err != nil {
		t.Fatalf("TCPRetrans: %v", err)
	}
	if retrans.BytesSent != 64*1024 {
		t.Fatalf("TCPRetrans bytes=%d", retrans.BytesSent)
	}
	if retrans.SegmentsSent == 0 {
		t.Fatalf("TCPRetrans segments=%d", retrans.SegmentsSent)
	}

	loss, err := client.UDPLoss(opCtx, 8, 256)
	if err != nil {
		t.Fatalf("UDPLoss: %v", err)
	}
	if loss.PacketsSent != 8 {
		t.Fatalf("UDPLoss packets_sent=%d", loss.PacketsSent)
	}
	if loss.PacketsRecv == 0 {
		t.Fatalf("UDPLoss packets_recv=%d", loss.PacketsRecv)
	}
}
