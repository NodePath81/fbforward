package fbmeasure

import (
	"bytes"
	"testing"
	"time"
)

func TestControlMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	msg := controlRequest{
		ID:      7,
		Op:      opPingTCP,
		Payload: []byte(`{"sequence":1}`),
	}
	if err := writeControlMessage(&buf, msg); err != nil {
		t.Fatalf("writeControlMessage: %v", err)
	}

	var decoded controlRequest
	if err := readControlMessage(&buf, &decoded); err != nil {
		t.Fatalf("readControlMessage: %v", err)
	}
	if decoded.ID != msg.ID || decoded.Op != msg.Op || string(decoded.Payload) != string(msg.Payload) {
		t.Fatalf("unexpected decoded message: %+v", decoded)
	}
}

func TestRTTAccumulator(t *testing.T) {
	var acc rttAccumulator
	acc.Add(10 * time.Millisecond)
	acc.Add(20 * time.Millisecond)
	acc.Add(30 * time.Millisecond)

	stats := acc.Stats()
	if stats.Samples != 3 {
		t.Fatalf("samples=%d, want 3", stats.Samples)
	}
	if stats.Min != 10*time.Millisecond {
		t.Fatalf("min=%s", stats.Min)
	}
	if stats.Max != 30*time.Millisecond {
		t.Fatalf("max=%s", stats.Max)
	}
	if stats.Mean < 19*time.Millisecond || stats.Mean > 21*time.Millisecond {
		t.Fatalf("mean=%s", stats.Mean)
	}
	if stats.Jitter <= 0 {
		t.Fatalf("jitter=%s, want > 0", stats.Jitter)
	}
}
