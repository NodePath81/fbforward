package iplog

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
)

type fakeLookup struct {
	result geoip.LookupResult
}

func (f fakeLookup) Lookup(net.IP) geoip.LookupResult {
	return f.result
}

func (f fakeLookup) Availability() geoip.Availability {
	return geoip.Availability{
		ASNDBAvailable:   f.result.ASNDBAvailable,
		CountryAvailable: f.result.CountryAvailable,
	}
}

type fakeWriter struct {
	mu      sync.Mutex
	batches [][]EnrichedRecord
}

func (w *fakeWriter) InsertBatch(records []EnrichedRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	copied := append([]EnrichedRecord(nil), records...)
	w.batches = append(w.batches, copied)
	return nil
}

func (w *fakeWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for _, batch := range w.batches {
		total += len(batch)
	}
	return total
}

func TestPipelineFlushesOnShutdown(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   4,
		WriteQueueSize: 4,
	}, fakeLookup{
		result: geoip.LookupResult{
			ASN:              13335,
			ASOrg:            "Cloudflare",
			Country:          "US",
			ASNDBAvailable:   true,
			CountryAvailable: true,
		},
	}, writer, metrics.NewMetrics(nil), nil)
	pipeline.Start()

	if !pipeline.Emit(CloseEvent{
		IP:         "1.1.1.1",
		Protocol:   "tcp",
		Upstream:   "primary",
		Port:       9000,
		RecordedAt: time.Now(),
	}) {
		t.Fatalf("expected emit to succeed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if writer.count() != 1 {
		t.Fatalf("expected one written record, got %d", writer.count())
	}
}

func TestPipelineDropsWhenGeoQueueIsFull(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   1,
		WriteQueueSize: 1,
	}, nil, writer, metrics.NewMetrics(nil), nil)
	pipeline.mu.Lock()
	pipeline.closed = false
	pipeline.mu.Unlock()
	pipeline.geoCh <- CloseEvent{IP: "10.0.0.1"}

	if pipeline.Emit(CloseEvent{IP: "10.0.0.2"}) {
		t.Fatalf("expected emit to fail when queue is full")
	}
}
