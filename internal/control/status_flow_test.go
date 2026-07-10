package control

import (
	"net/netip"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

func TestStatusStoreProjectsFlowLifecycle(t *testing.T) {
	store := NewStatusStore(nil)
	id, err := flow.NewID()
	if err != nil {
		t.Fatalf("NewID error: %v", err)
	}
	meta := flow.Meta{
		ID:         id,
		Protocol:   flow.ProtocolTCP,
		ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"),
		Listener:   "127.0.0.1:9000",
		Route:      "simple",
		Upstream:   "primary",
		StartedAt:  time.Now().UTC(),
	}
	store.Open(meta)
	tcp, udp := store.Snapshot()
	if len(tcp) != 1 || len(udp) != 0 {
		t.Fatalf("unexpected open snapshot: tcp=%d udp=%d", len(tcp), len(udp))
	}
	store.Update(id, flow.Counters{BytesUp: 10, BytesDown: 20, SegmentsUp: 1, SegmentsDown: 2})
	tcp, _ = store.Snapshot()
	if tcp[0].BytesUp != 10 || tcp[0].BytesDown != 20 || tcp[0].Port != 9000 {
		t.Fatalf("unexpected status update: %+v", tcp[0])
	}
	store.Close(flow.Summary{Meta: meta, CloseReason: "eof"})
	tcp, udp = store.Snapshot()
	if len(tcp) != 0 || len(udp) != 0 {
		t.Fatalf("expected empty snapshot after close: tcp=%d udp=%d", len(tcp), len(udp))
	}
}
