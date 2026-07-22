package audit

import (
	"context"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
)

func TestPipelineWritesFlowAndCheckpoint(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "pipeline.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.IPLogConfig{Enabled: true, GeoQueueSize: 8, WriteQueueSize: 8, BatchSize: 1, FlushInterval: config.Duration(10 * time.Millisecond)}
	pipeline := NewPipeline(cfg, nil, store, nil, nil)
	pipeline.Start()
	id, err := flow.NewID()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Second)
	pipeline.Open(flow.Meta{ID: id, Protocol: flow.ProtocolTCP, ClientAddr: netip.MustParseAddrPort("192.0.2.10:1234"), Listener: ":9000", Upstream: "primary", StartedAt: started})
	pipeline.Update(id, flow.Counters{LastActivity: started.Add(500 * time.Millisecond), BytesUp: 10, BytesDown: 20, SegmentsUp: 1, SegmentsDown: 2})
	if got := waitForCheckpointCount(t, store, id.String(), 1); got != 1 {
		t.Fatalf("active checkpoint count = %d, want 1", got)
	}
	pipeline.Close(flow.Summary{Meta: flow.Meta{ID: id, Protocol: flow.ProtocolTCP, ClientAddr: netip.MustParseAddrPort("192.0.2.10:1234"), Listener: ":9000", Upstream: "primary", StartedAt: started}, EndedAt: started.Add(time.Second), LastActivity: started.Add(500 * time.Millisecond), BytesUp: 10, BytesDown: 20, CloseReason: "eof"})
	if err := pipeline.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := store.Query(QueryParams{Limit: 10})
	if err != nil || result.Total != 1 || result.Records[0].FlowID != id.String() {
		t.Fatalf("flow result = %+v err=%v", result, err)
	}
	var checkpoints int
	if err := store.readDB.QueryRow(`SELECT COUNT(*) FROM flow_checkpoints WHERE flow_id = ?`, id.String()).Scan(&checkpoints); err != nil {
		t.Fatal(err)
	}
	if checkpoints < 2 {
		t.Fatalf("expected active and final checkpoints, got %d", checkpoints)
	}
}

func waitForCheckpointCount(t *testing.T, store *Store, flowID string, want int) int {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	read := func() int {
		var count int
		if err := store.readDB.QueryRow(`SELECT COUNT(*) FROM flow_checkpoints WHERE flow_id = ?`, flowID).Scan(&count); err != nil {
			t.Fatalf("read checkpoint count: %v", err)
		}
		return count
	}
	if count := read(); count >= want {
		return count
	}
	for {
		select {
		case <-ticker.C:
			if count := read(); count >= want {
				return count
			}
		case <-deadline.C:
			return read()
		}
	}
}

func TestPipelinePersistsContextSnapshotAndGracePeriod(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "snapshot.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.IPLogConfig{Enabled: true, GeoQueueSize: 8, WriteQueueSize: 8, BatchSize: 1, FlushInterval: config.Duration(10 * time.Millisecond)}
	pipeline := NewPipeline(cfg, nil, store, nil, nil)
	pipeline.Start()
	started := time.Now().UTC().Add(-time.Second)
	ended := started.Add(time.Second)
	resolveUntil := ended.Add(30 * time.Second)
	pipeline.PublishEntity(FlowEntity{
		FlowID: "snapshot-flow", Protocol: "tcp", ClientIP: "192.0.2.10", ClientPort: 1234,
		Listener: ":9000", Route: "web", Upstream: "primary", BackendKey: "primary@192.0.2.10:443",
		BackendProtocol: "tcp", BackendLocal: "10.0.0.1:43122", BackendRemote: "192.0.2.10:443",
		CreatedAt: started, State: "active", Generation: 1, LastActivity: started,
	})
	pipeline.PublishEntity(FlowEntity{
		FlowID: "snapshot-flow", Protocol: "tcp", ClientIP: "192.0.2.10", ClientPort: 1234,
		Listener: ":9000", Route: "web", Upstream: "primary", CreatedAt: started, EndedAt: &ended,
		ResolveUntil: &resolveUntil, State: "closed", Generation: 1, LastActivity: ended, BytesUp: 20,
	})
	if err := pipeline.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var state, backendKey, backendLocal string
	var resolve int64
	if err := store.readDB.QueryRow(`SELECT state, backend_key, backend_local, resolve_until FROM flow_entities WHERE flow_id = ?`, "snapshot-flow").Scan(&state, &backendKey, &backendLocal, &resolve); err != nil {
		t.Fatal(err)
	}
	if state != "closed" || backendKey == "" || backendLocal == "" || resolve != resolveUntil.UnixMilli() {
		t.Fatalf("unexpected persisted snapshot: state=%s backend=%s local=%s resolve=%d", state, backendKey, backendLocal, resolve)
	}
}
