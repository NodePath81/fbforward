package fbmeasure

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	want, err := newProbeFrame(0x0102030405060708)
	if err != nil {
		t.Fatalf("newProbeFrame: %v", err)
	}
	got, err := parseFrame(want[:])
	if err != nil {
		t.Fatalf("parseFrame: %v", err)
	}
	if got != want {
		t.Fatalf("frame changed during round trip")
	}
	if got.sequence() != 0x0102030405060708 {
		t.Fatalf("sequence=%x", got.sequence())
	}
}

func TestParseFrameRejectsMalformedInput(t *testing.T) {
	valid, err := newProbeFrame(1)
	if err != nil {
		t.Fatalf("newProbeFrame: %v", err)
	}
	copyFrame := func() []byte {
		data := make([]byte, frameSize)
		copy(data, valid[:])
		return data
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "short", data: copyFrame()[:frameSize-1]},
		{name: "long", data: append(copyFrame(), 0)},
		{name: "magic", data: func() []byte { data := copyFrame(); copy(data[0:4], []byte("BAD!")); return data }()},
		{name: "version", data: func() []byte { data := copyFrame(); data[4] = 2; return data }()},
		{name: "kind", data: func() []byte { data := copyFrame(); data[5] = 2; return data }()},
		{name: "reserved", data: func() []byte { data := copyFrame(); data[6] = 1; return data }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseFrame(tt.data); err == nil {
				t.Fatal("parseFrame accepted malformed input")
			}
		})
	}
}

func TestFrameEchoIsByteExact(t *testing.T) {
	frame, err := newProbeFrame(3)
	if err != nil {
		t.Fatalf("newProbeFrame: %v", err)
	}
	decoded, err := parseFrame(frame[:])
	if err != nil || !bytes.Equal(decoded[:], frame[:]) {
		t.Fatalf("frame was not byte exact")
	}
}
