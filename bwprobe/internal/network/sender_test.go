package network

import (
	"testing"

	"github.com/NodePath81/fbforward/bwprobe/internal/protocol"
)

func TestTCPPayloadSize(t *testing.T) {
	payload, err := TCPPayloadSize(int64(protocol.TCPFrameHeaderSize + 100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload != 100 {
		t.Fatalf("expected payload 100, got %d", payload)
	}

	_, err = TCPPayloadSize(int64(protocol.TCPFrameHeaderSize))
	if err == nil {
		t.Fatalf("expected error for too-small chunk size")
	}
}

func TestUDPPayloadSizeClamp(t *testing.T) {
	payload, total, err := UDPPayloadSize(128 * 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != protocol.UDPMaxChunkSize {
		t.Fatalf("expected total %d, got %d", protocol.UDPMaxChunkSize, total)
	}
	if payload != total-protocol.UDPSeqHeaderSize {
		t.Fatalf("expected payload %d, got %d", total-protocol.UDPSeqHeaderSize, payload)
	}
}
