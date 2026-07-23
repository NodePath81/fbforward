package control

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/flow"
)

func TestGetActiveFlowsRPCReturnsSnapshot(t *testing.T) {
	server := newTestControlServer(t)
	id, err := flow.NewID()
	if err != nil {
		t.Fatalf("NewID error: %v", err)
	}
	meta := flow.Meta{
		ID:         id,
		Protocol:   flow.ProtocolTCP,
		ClientAddr: netip.MustParseAddrPort("192.0.2.10:4321"),
		Listener:   "127.0.0.1:8443",
		Route:      "web",
		Upstream:   "primary",
		StartedAt:  time.Now().UTC(),
	}
	server.status.Open(meta)

	rec := callTestRPC(t, server, "0123456789abcdef", "GetActiveFlows", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Ok     bool `json:"ok"`
		Result struct {
			CapturedAt int64         `json:"captured_at"`
			TCP        []StatusEntry `json:"tcp"`
			UDP        []StatusEntry `json:"udp"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Ok || response.Result.CapturedAt == 0 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if len(response.Result.TCP) != 1 || response.Result.TCP[0].ID != id.String() {
		t.Fatalf("unexpected flow snapshot: %+v", response.Result.TCP)
	}
	if response.Result.TCP[0].Route != "web" || response.Result.TCP[0].Upstream != "primary" {
		t.Fatalf("missing flow metadata: %+v", response.Result.TCP[0])
	}
}

func TestGetActiveFlowsSnapshotConcurrentWithUpdates(t *testing.T) {
	store := NewStatusStore()
	id, err := flow.NewID()
	if err != nil {
		t.Fatalf("NewID error: %v", err)
	}
	meta := flow.Meta{ID: id, Protocol: flow.ProtocolUDP, ClientAddr: netip.MustParseAddrPort("192.0.2.2:5000"), StartedAt: time.Now().UTC()}
	store.Open(meta)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			store.Update(id, flow.Counters{BytesUp: uint64(i), LastActivity: time.Now().UTC()})
			_, _ = store.Snapshot()
		}(i)
	}
	group.Wait()
	tcp, udp := store.Snapshot()
	if len(tcp) != 0 || len(udp) != 1 || udp[0].ID != id.String() {
		t.Fatalf("unexpected concurrent snapshot: tcp=%v udp=%v", tcp, udp)
	}
}

func TestGetActiveFlowsIncludesEffectiveTags(t *testing.T) {
	server := newTestControlServer(t)
	store := newTestAuditStore(t, server)
	id, err := flow.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.UpsertFlowEntity(audit.FlowEntity{FlowID: id.String(), Protocol: "tcp", ClientIP: "192.0.2.15", CreatedAt: now, LastActivity: now, State: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(audit.FlowTag{FlowID: id.String(), Tag: "app:user=alice", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertClientTag(audit.ClientTag{ClientIP: "192.0.2.15", Tag: "app:risk=trusted", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	udpID, err := flow.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowEntity(audit.FlowEntity{FlowID: udpID.String(), Protocol: "udp", ClientIP: "192.0.2.16", CreatedAt: now, LastActivity: now, State: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFlowTag(audit.FlowTag{FlowID: udpID.String(), Tag: "app:udp", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server.status.Open(flow.Meta{ID: id, Protocol: flow.ProtocolTCP, ClientAddr: netip.MustParseAddrPort("192.0.2.15:4321"), StartedAt: now})
	server.status.Open(flow.Meta{ID: udpID, Protocol: flow.ProtocolUDP, ClientAddr: netip.MustParseAddrPort("192.0.2.16:4322"), StartedAt: now})
	rec := callTestRPC(t, server, "0123456789abcdef", "GetActiveFlows", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetActiveFlows status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Result struct {
			TCP []StatusEntry `json:"tcp"`
			UDP []StatusEntry `json:"udp"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.TCP) != 1 || len(response.Result.TCP[0].Tags) != 2 {
		t.Fatalf("active flow tags = %+v", response.Result.TCP)
	}
	if len(response.Result.UDP) != 1 || len(response.Result.UDP[0].Tags) != 1 || response.Result.UDP[0].Tags[0].Tag != "app:udp" {
		t.Fatalf("active UDP flow tags = %+v", response.Result.UDP)
	}
}
