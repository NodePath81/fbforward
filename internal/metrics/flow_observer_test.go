package metrics

import (
	"net/netip"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

func TestFlowObserverLifecycleConvertsCumulativeCountersOnce(t *testing.T) {
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
	observer.Close(flow.Summary{Meta: meta, BytesUp: 20, BytesDown: 9, CloseReason: "eof"})
	observer.Close(flow.Summary{Meta: meta, BytesUp: 40, BytesDown: 20, CloseReason: "eof"})

	if got := m.upstreams["primary"].traffic.tcpUp.Load(); got != 20 {
		t.Fatalf("expected 20 upstream bytes, got %d", got)
	}
	if got := m.upstreams["primary"].traffic.tcpDown.Load(); got != 9 {
		t.Fatalf("expected 9 downstream bytes, got %d", got)
	}
	if got := m.tcpActive.Load(); got != 0 {
		t.Fatalf("expected no active TCP flows, got %d", got)
	}
	if got := m.flowEvents[flowEventKey{protocol: "tcp", event: "open", reason: "none"}]; got != 1 {
		t.Fatalf("expected one open event, got %d", got)
	}
	if got := m.flowEvents[flowEventKey{protocol: "tcp", event: "close", reason: "eof"}]; got != 1 {
		t.Fatalf("expected one close event, got %d", got)
	}
}

func TestFlowObserverRejectRecordsBoundedReason(t *testing.T) {
	m := NewMetrics(nil)
	observer := NewFlowObserver(m)
	observer.Reject(flow.Rejection{Protocol: flow.ProtocolUDP, Reason: "peer supplied error"})
	if got := m.flowEvents[flowEventKey{protocol: "udp", event: "reject", reason: "other"}]; got != 1 {
		t.Fatalf("expected bounded reject reason, got %d", got)
	}
}
