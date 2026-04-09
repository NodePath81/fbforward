package iplog

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/util"
)

const (
	rejectionDedupeTTL             = 60 * time.Second
	rejectionDedupeCleanupInterval = 60 * time.Second
)

type batchWriter interface {
	InsertBatch([]EnrichedRecord) error
	InsertRejectionBatch([]EnrichedRejectionRecord) error
}

type geoQueueItem struct {
	flow      *CloseEvent
	rejection *RejectionEvent
}

type writeQueueItem struct {
	flow      *EnrichedRecord
	rejection *EnrichedRejectionRecord
}

type Pipeline struct {
	geoCh         chan geoQueueItem
	writeCh       chan writeQueueItem
	lookup        geoip.LookupProvider
	store         batchWriter
	metrics       *metrics.Metrics
	logger        util.Logger
	batchSize     int
	flushInterval time.Duration
	logRejections bool

	rejectionMu        sync.Mutex
	recentRejections   map[string]time.Time
	lastRejectionSweep time.Time

	closeOnce sync.Once
	wg        sync.WaitGroup
	closed    bool
	mu        sync.RWMutex
}

func NewPipeline(cfg config.IPLogConfig, lookup geoip.LookupProvider, store batchWriter, metrics *metrics.Metrics, logger util.Logger) *Pipeline {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	flushInterval := cfg.FlushInterval.Duration()
	if flushInterval <= 0 {
		flushInterval = 5 * time.Second
	}
	return &Pipeline{
		geoCh:              make(chan geoQueueItem, cfg.GeoQueueSize),
		writeCh:            make(chan writeQueueItem, cfg.WriteQueueSize),
		lookup:             lookup,
		store:              store,
		metrics:            metrics,
		logger:             util.ComponentLogger(logger, util.CompIPLog),
		batchSize:          batchSize,
		flushInterval:      flushInterval,
		logRejections:      util.BoolValue(cfg.LogRejections, cfg.Enabled),
		recentRejections:   make(map[string]time.Time),
		lastRejectionSweep: time.Now(),
	}
}

func (p *Pipeline) Start() {
	if p == nil {
		return
	}
	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		p.runGeoWorker()
	}()
	go func() {
		defer p.wg.Done()
		p.runWriteWorker()
	}()
}

func (p *Pipeline) Emit(event CloseEvent) bool {
	if p == nil {
		return false
	}
	return p.enqueueGeoItem(geoQueueItem{flow: &event})
}

func (p *Pipeline) EmitRejection(event RejectionEvent) bool {
	if p == nil || !p.logRejections {
		return false
	}
	if event.RecordedAt.IsZero() {
		event.RecordedAt = time.Now()
	}
	if !p.allowRejectionEvent(event) {
		return false
	}
	return p.enqueueGeoItem(geoQueueItem{rejection: &event})
}

func (p *Pipeline) enqueueGeoItem(item geoQueueItem) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return false
	}
	select {
	case p.geoCh <- item:
		if p.metrics != nil {
			p.metrics.IncIPLogEvent()
		}
		return true
	default:
		if p.metrics != nil {
			p.metrics.IncIPLogEventDropped()
		}
		util.Event(p.logger, slog.LevelWarn, "iplog.geo_queue_full", "queue.capacity", cap(p.geoCh), "result", "dropped")
		return false
	}
}

func (p *Pipeline) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.geoCh)
		p.mu.Unlock()
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.wg.Wait()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (p *Pipeline) runGeoWorker() {
	defer close(p.writeCh)
	for item := range p.geoCh {
		switch {
		case item.flow != nil:
			record := EnrichedRecord{CloseEvent: *item.flow}
			p.enrich(&record.ASN, &record.ASOrg, &record.Country, item.flow.IP)
			if !p.enqueueWriteItem(writeQueueItem{flow: &record}) {
				continue
			}
		case item.rejection != nil:
			record := EnrichedRejectionRecord{RejectionEvent: *item.rejection}
			p.enrich(&record.ASN, &record.ASOrg, &record.Country, item.rejection.IP)
			if !p.enqueueWriteItem(writeQueueItem{rejection: &record}) {
				continue
			}
		}
	}
}

func (p *Pipeline) runWriteWorker() {
	flowBatch := make([]EnrichedRecord, 0, p.batchSize)
	rejectionBatch := make([]EnrichedRejectionRecord, 0, p.batchSize)
	timer := time.NewTimer(p.flushInterval)
	defer timer.Stop()
	timerActive := false

	flush := func() {
		written := 0
		if len(flowBatch) > 0 {
			if err := p.store.InsertBatch(flowBatch); err != nil {
				util.Event(p.logger, slog.LevelError, "iplog.batch_write_failed", "event.type", EntryTypeFlow, "batch.size", len(flowBatch), "error", err)
			} else {
				written += len(flowBatch)
			}
			flowBatch = flowBatch[:0]
		}
		if len(rejectionBatch) > 0 {
			if err := p.store.InsertRejectionBatch(rejectionBatch); err != nil {
				util.Event(p.logger, slog.LevelError, "iplog.batch_write_failed", "event.type", EntryTypeRejection, "batch.size", len(rejectionBatch), "error", err)
			} else {
				written += len(rejectionBatch)
			}
			rejectionBatch = rejectionBatch[:0]
		}
		if written > 0 && p.metrics != nil {
			p.metrics.AddIPLogWrites(uint64(written))
			p.metrics.ObserveIPLogBatchSize(written)
		}
	}

	for {
		select {
		case item, ok := <-p.writeCh:
			if !ok {
				flush()
				return
			}
			if item.flow != nil {
				flowBatch = append(flowBatch, *item.flow)
			}
			if item.rejection != nil {
				rejectionBatch = append(rejectionBatch, *item.rejection)
			}
			if len(flowBatch)+len(rejectionBatch) == 1 && !timerActive {
				timer.Reset(p.flushInterval)
				timerActive = true
			}
			if len(flowBatch)+len(rejectionBatch) >= p.batchSize {
				if timerActive && !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timerActive = false
				flush()
			}
		case <-timer.C:
			timerActive = false
			flush()
		}
	}
}

func (p *Pipeline) enrich(asn *int, asOrg, country *string, ip string) {
	if p.lookup == nil {
		return
	}
	result := p.lookup.Lookup(net.ParseIP(ip))
	*asn = result.ASN
	*asOrg = result.ASOrg
	*country = result.Country
}

func (p *Pipeline) enqueueWriteItem(item writeQueueItem) bool {
	select {
	case p.writeCh <- item:
		return true
	default:
		if p.metrics != nil {
			p.metrics.IncIPLogEventDropped()
		}
		util.Event(p.logger, slog.LevelWarn, "iplog.write_queue_full", "queue.capacity", cap(p.writeCh), "result", "dropped")
		return false
	}
}

func (p *Pipeline) allowRejectionEvent(event RejectionEvent) bool {
	key := rejectionDedupeKey(event)
	now := event.RecordedAt
	p.rejectionMu.Lock()
	defer p.rejectionMu.Unlock()

	if !p.lastRejectionSweep.IsZero() && now.Sub(p.lastRejectionSweep) >= rejectionDedupeCleanupInterval {
		for existingKey, seenAt := range p.recentRejections {
			if now.Sub(seenAt) >= rejectionDedupeTTL {
				delete(p.recentRejections, existingKey)
			}
		}
		p.lastRejectionSweep = now
	}

	if seenAt, ok := p.recentRejections[key]; ok && now.Sub(seenAt) < rejectionDedupeTTL {
		return false
	}
	p.recentRejections[key] = now
	if p.lastRejectionSweep.IsZero() {
		p.lastRejectionSweep = now
	}
	return true
}

func rejectionDedupeKey(event RejectionEvent) string {
	return strings.Join([]string{
		event.IP,
		event.Protocol,
		fmt.Sprintf("%d", event.Port),
		event.Reason,
		event.MatchedRuleType,
		event.MatchedRuleValue,
	}, "|")
}
