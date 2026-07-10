package metrics

import (
	"net/netip"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

func TestFlowObserverConvertsCumulativeCountersOnce(t *testing.T) {
	m := NewMetrics([]string{"primary"})
	observer := NewFlowObserver(m)
	id, err := flow.NewID()
	if err != nil {
		t.Fatalf("NewID error: %v", err)
	}
	meta := flow.Meta{
		ID:         id,
		Protocol:   flow.ProtocolTCP,
		ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"),
		Upstream:   "primary",
		StartedAt:  time.Now().UTC(),
	}
	observer.Open(meta)
	observer.Update(id, flow.Counters{BytesUp: 10, BytesDown: 4})
	observer.Update(id, flow.Counters{BytesUp: 15, BytesDown: 5})
	observer.Close(flow.Summary{Meta: meta, BytesUp: 20, BytesDown: 9})
	observer.Close(flow.Summary{Meta: meta, BytesUp: 40, BytesDown: 20})

	if got := m.bytesUpTotal["primary"].Load(); got != 20 {
		t.Fatalf("expected 20 upstream bytes, got %d", got)
	}
	if got := m.bytesDownTotal["primary"].Load(); got != 9 {
		t.Fatalf("expected 9 downstream bytes, got %d", got)
	}
	if got := m.bytesTCP["primary"].up.Load(); got != 20 {
		t.Fatalf("expected 20 TCP upstream bytes, got %d", got)
	}
	if got := m.bytesTCP["primary"].down.Load(); got != 9 {
		t.Fatalf("expected 9 TCP downstream bytes, got %d", got)
	}
	if m.tcpActive != 0 {
		t.Fatalf("expected no active TCP flows, got %d", m.tcpActive)
	}
}
