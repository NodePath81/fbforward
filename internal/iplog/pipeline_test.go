package iplog

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
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
	mu               sync.Mutex
	batches          [][]EnrichedRecord
	rejectionBatches [][]EnrichedRejectionRecord
	block            chan struct{}
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

func (w *fakeWriter) InsertRejectionBatch(records []EnrichedRejectionRecord) error {
	if w.block != nil {
		<-w.block
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	copied := append([]EnrichedRejectionRecord(nil), records...)
	w.rejectionBatches = append(w.rejectionBatches, copied)
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

func (w *fakeWriter) rejectionCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for _, batch := range w.rejectionBatches {
		total += len(batch)
	}
	return total
}

func (w *fakeWriter) flattenRejections() []EnrichedRejectionRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []EnrichedRejectionRecord
	for _, batch := range w.rejectionBatches {
		out = append(out, batch...)
	}
	return out
}

func newStartedPipeline(t *testing.T, cfg config.IPLogConfig, lookup geoip.LookupProvider) (*Pipeline, *fakeWriter) {
	t.Helper()
	writer := &fakeWriter{}
	pipeline := NewPipeline(cfg, lookup, writer, metrics.NewMetrics(nil), nil)
	pipeline.Start()
	return pipeline, writer
}

func shutdownPipeline(t *testing.T, pipeline *Pipeline) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
}

func waitForWritten(t *testing.T, writer *fakeWriter, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && writer.count() < want {
		time.Sleep(10 * time.Millisecond)
	}
	if got := writer.count(); got != want {
		t.Fatalf("expected %d written records, got %d", want, got)
	}
}

func TestPipelineFlushesOnShutdown(t *testing.T) {
	pipeline, writer := newStartedPipeline(t, config.IPLogConfig{
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
	})

	if !pipeline.Emit(CloseEvent{
		IP:         "1.1.1.1",
		Protocol:   "tcp",
		Upstream:   "primary",
		Port:       9000,
		RecordedAt: time.Now(),
	}) {
		t.Fatalf("expected emit to succeed")
	}

	shutdownPipeline(t, pipeline)
	if writer.count() != 1 {
		t.Fatalf("expected one written record, got %d", writer.count())
	}
}

func TestPipelineObserverMapsFlowSummary(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		GeoQueueSize:   2,
		WriteQueueSize: 2,
		BatchSize:      1,
		FlushInterval:  config.Duration(time.Hour),
	}, nil, writer, metrics.NewMetrics(nil), nil)
	pipeline.Start()
	started := time.Unix(100, 0).UTC()
	ended := time.Unix(103, 0).UTC()
	id, err := flow.NewID()
	if err != nil {
		t.Fatalf("NewID error: %v", err)
	}
	pipeline.Close(flow.Summary{
		Meta: flow.Meta{
			ID:         id,
			Protocol:   flow.ProtocolUDP,
			ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"),
			Listener:   "127.0.0.1:9000",
			Route:      "simple",
			Upstream:   "primary",
			StartedAt:  started,
		},
		EndedAt:      ended,
		LastActivity: time.Unix(102, 0).UTC(),
		BytesUp:      10,
		BytesDown:    20,
		CloseReason:  "idle_timeout",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	records := writer.flatten()
	if len(records) != 1 {
		t.Fatalf("expected one Flow record, got %d", len(records))
	}
	record := records[0]
	if record.FlowID != id.String() || record.IP != "192.0.2.1" || record.Port != 9000 {
		t.Fatalf("unexpected Flow identity: %+v", record)
	}
	if record.Route != "simple" || record.CloseReason != "idle_timeout" || record.BytesUp != 10 || record.BytesDown != 20 {
		t.Fatalf("unexpected Flow summary mapping: %+v", record)
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
	pipeline.geoCh <- geoQueueItem{flow: &CloseEvent{IP: "10.0.0.1"}}

	if pipeline.Emit(CloseEvent{IP: "10.0.0.2"}) {
		t.Fatalf("expected emit to fail when queue is full")
	}
}

func TestPipelineFlushesOnBatchSize(t *testing.T) {
	pipeline, writer := newStartedPipeline(t, config.IPLogConfig{
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      2,
		FlushInterval:  config.Duration(time.Hour),
	}, nil)

	if !pipeline.Emit(CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: time.Now()}) {
		t.Fatalf("expected first emit to succeed")
	}
	if !pipeline.Emit(CloseEvent{IP: "10.0.0.2", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: time.Now()}) {
		t.Fatalf("expected second emit to succeed")
	}

	waitForWritten(t, writer, 2)
	shutdownPipeline(t, pipeline)
}

func TestPipelineFlushesOnTimer(t *testing.T) {
	pipeline, writer := newStartedPipeline(t, config.IPLogConfig{
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      10,
		FlushInterval:  config.Duration(25 * time.Millisecond),
	}, nil)

	if !pipeline.Emit(CloseEvent{IP: "10.0.0.1", Protocol: "tcp", Upstream: "a", Port: 1, RecordedAt: time.Now()}) {
		t.Fatalf("expected emit to succeed")
	}

	waitForWritten(t, writer, 1)
	shutdownPipeline(t, pipeline)
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

	pipeline.writeCh <- writeQueueItem{flow: &EnrichedRecord{CloseEvent: CloseEvent{IP: "10.0.0.0"}}}
	pipeline.geoCh <- geoQueueItem{flow: &CloseEvent{IP: "10.0.0.1"}}
	close(pipeline.geoCh)

	pipeline.runGeoWorker()

	rendered := m.Render()
	if !strings.Contains(rendered, "fbforward_iplog_events_dropped_total 1") {
		t.Fatalf("expected dropped metric after write queue overflow, got:\n%s", rendered)
	}
}

func TestPipelineEmitRejectionWritesOncePerDedupeWindow(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		Enabled:        true,
		LogRejections:  boolPtr(true),
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      1,
		FlushInterval:  config.Duration(time.Hour),
	}, nil, writer, metrics.NewMetrics(nil), nil)
	pipeline.Start()

	now := time.Now().UTC()
	if !pipeline.EmitRejection(RejectionEvent{
		IP:               "10.0.0.1",
		Protocol:         "tcp",
		Port:             9000,
		Reason:           "firewall_deny",
		MatchedRuleType:  "cidr",
		MatchedRuleValue: "10.0.0.0/8",
		RecordedAt:       now,
	}) {
		t.Fatalf("expected first rejection emit to succeed")
	}
	if pipeline.EmitRejection(RejectionEvent{
		IP:               "10.0.0.1",
		Protocol:         "tcp",
		Port:             9000,
		Reason:           "firewall_deny",
		MatchedRuleType:  "cidr",
		MatchedRuleValue: "10.0.0.0/8",
		RecordedAt:       now.Add(30 * time.Second),
	}) {
		t.Fatalf("expected duplicate rejection inside dedupe window to be suppressed")
	}
	if !pipeline.EmitRejection(RejectionEvent{
		IP:               "10.0.0.1",
		Protocol:         "tcp",
		Port:             9000,
		Reason:           "firewall_deny",
		MatchedRuleType:  "cidr",
		MatchedRuleValue: "10.0.0.0/8",
		RecordedAt:       now.Add(61 * time.Second),
	}) {
		t.Fatalf("expected rejection after dedupe window to be accepted")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if writer.rejectionCount() != 2 {
		t.Fatalf("expected two rejection records, got %d", writer.rejectionCount())
	}
}

func TestPipelineEmitRejectionGeoEnrichesRecord(t *testing.T) {
	writer := &fakeWriter{}
	pipeline := NewPipeline(config.IPLogConfig{
		Enabled:        true,
		LogRejections:  boolPtr(true),
		GeoQueueSize:   4,
		WriteQueueSize: 4,
		BatchSize:      1,
		FlushInterval:  config.Duration(time.Hour),
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

	if !pipeline.EmitRejection(RejectionEvent{
		IP:         "1.1.1.1",
		Protocol:   "udp",
		Port:       9000,
		Reason:     "udp_mapping_limit",
		RecordedAt: time.Now(),
	}) {
		t.Fatalf("expected rejection emit to succeed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := pipeline.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}

	records := writer.flattenRejections()
	if len(records) != 1 {
		t.Fatalf("expected one rejection record, got %d", len(records))
	}
	if records[0].ASN != 13335 || records[0].ASOrg != "Cloudflare" || records[0].Country != "US" {
		t.Fatalf("expected enriched rejection record, got %+v", records[0])
	}
}

func boolPtr(value bool) *bool {
	return &value
}
