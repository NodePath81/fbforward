package iplog

import (
	"context"
	"net"
	"strings"
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
	block   chan struct{}
}

func (w *fakeWriter) InsertBatch(records []EnrichedRecord) error {
	if w.block != nil {
		<-w.block
	}
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

func (w *fakeWriter) flatten() []EnrichedRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []EnrichedRecord
	for _, batch := range w.batches {
		out = append(out, batch...)
	}
	return out
}

func TestPipelineFlushesOnShutdown(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      10,
		FlushInterval:  config.Duration(250 * time.Millisecond),
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

func TestPipelineFlushesOnBatchSize(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      2,
		FlushInterval:  config.Duration(time.Hour),
	}, nil, writer, metrics.NewMetrics(nil), nil)
	pipeline.Start()

	if !pipeline.Emit(CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: time.Now()}) {
		t.Fatalf("expected first emit to succeed")
	}
	if !pipeline.Emit(CloseEvent{IP: "10.0.0.2", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: time.Now()}) {
		t.Fatalf("expected second emit to succeed")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if writer.count() == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if writer.count() != 2 {
		t.Fatalf("expected batch flush on threshold, got %d", writer.count())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
}

func TestPipelineFlushesOnTimer(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      10,
		FlushInterval:  config.Duration(25 * time.Millisecond),
	}, nil, writer, metrics.NewMetrics(nil), nil)
	pipeline.Start()

	if !pipeline.Emit(CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: time.Now()}) {
		t.Fatalf("expected emit to succeed")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if writer.count() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if writer.count() != 1 {
		t.Fatalf("expected timer flush, got %d", writer.count())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
}

func TestPipelineWritesPartialGeoIPData(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      1,
		FlushInterval:  config.Duration(time.Hour),
	}, fakeLookup{
		result: geoip.LookupResult{
			Country:          "US",
			CountryAvailable: true,
		},
	}, writer, metrics.NewMetrics(nil), nil)
	pipeline.Start()

	if !pipeline.Emit(CloseEvent{IP: "1.1.1.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: time.Now()}) {
		t.Fatalf("expected emit to succeed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}

	records := writer.flatten()
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	if records[0].Country != "US" || records[0].ASN != 0 || records[0].ASOrg != "" {
		t.Fatalf("expected partial GeoIP enrichment, got %+v", records[0])
	}
}

func TestPipelineWriteQueueOverflowIncrementsDropMetric(t *testing.T) {
	writer := &fakeWriter{}
	m := metrics.NewMetrics(nil)
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   2,
		WriteQueueSize: 1,
		BatchSize:      10,
		FlushInterval:  config.Duration(time.Hour),
	}, nil, writer, m, nil)

	pipeline.writeCh <- EnrichedRecord{CloseEvent: CloseEvent{IP: "10.0.0.0"}}
	pipeline.geoCh <- CloseEvent{IP: "10.0.0.1"}
	close(pipeline.geoCh)

	pipeline.runGeoWorker()

	rendered := m.Render()
	if !strings.Contains(rendered, "fbforward_iplog_events_dropped_total 1") {
		t.Fatalf("expected dropped metric after write queue overflow, got:\n%s", rendered)
	}
}

func TestPipelineShutdownWithEmptyQueues(t *testing.T) {
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   1,
		WriteQueueSize: 1,
		BatchSize:      1,
		FlushInterval:  config.Duration(time.Second),
	}, nil, &fakeWriter{}, metrics.NewMetrics(nil), nil)
	pipeline.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
}
