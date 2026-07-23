package audit

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/google/uuid"
)

const (
	rejectionDedupeTTL             = time.Minute
	rejectionDedupeCleanupInterval = time.Minute
	rejectionDedupeMaxEntries      = 4096
)

type pipelineItem struct {
	entity     *FlowEntity
	flow       *FlowRecord
	checkpoint *FlowCheckpoint
	rejection  *RejectionRow
}

func (i pipelineItem) recordCount() uint64 {
	var count uint64
	if i.entity != nil {
		count++
	}
	if i.flow != nil {
		count++
	}
	if i.checkpoint != nil {
		count++
	}
	if i.rejection != nil {
		count++
	}
	return count
}

type activeFlow struct {
	meta           flow.Meta
	lastCounters   flow.Counters
	lastCheckpoint time.Time
}

type Pipeline struct {
	geoCh         chan pipelineItem
	writeCh       chan pipelineItem
	lookup        geoip.LookupProvider
	store         *Store
	metrics       *metrics.Metrics
	logger        util.Logger
	batchSize     int
	flushInterval time.Duration
	logRejections bool

	mu        sync.Mutex
	active    map[flow.ID]activeFlow
	closed    bool
	closeOnce sync.Once
	wg        sync.WaitGroup
	rejectMu  sync.Mutex
	recent    map[string]time.Time
	lastSweep time.Time
}

func NewPipeline(cfg config.IPLogConfig, lookup geoip.LookupProvider, store *Store, metricSet *metrics.Metrics, logger util.Logger) *Pipeline {
	geoSize := cfg.GeoQueueSize
	if geoSize <= 0 {
		geoSize = 256
	}
	writeSize := cfg.WriteQueueSize
	if writeSize <= 0 {
		writeSize = 256
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	flush := cfg.FlushInterval.Duration()
	if flush <= 0 {
		flush = 5 * time.Second
	}
	return &Pipeline{
		geoCh: make(chan pipelineItem, geoSize), writeCh: make(chan pipelineItem, writeSize),
		lookup: lookup, store: store, metrics: metricSet, logger: util.ComponentLogger(logger, util.CompIPLog),
		batchSize: batchSize, flushInterval: flush, logRejections: util.BoolValue(cfg.LogRejections, cfg.Enabled),
		active: make(map[flow.ID]activeFlow), recent: make(map[string]time.Time),
	}
}

func (p *Pipeline) Start() {
	if p == nil {
		return
	}
	p.wg.Add(2)
	go func() { defer p.wg.Done(); p.runGeoWorker() }()
	go func() { defer p.wg.Done(); p.runWriteWorker() }()
}

func (p *Pipeline) Open(meta flow.Meta) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.active[meta.ID] = activeFlow{meta: meta}
	p.mu.Unlock()
	p.enqueue(pipelineItem{entity: &FlowEntity{
		FlowID: meta.ID.String(), Protocol: meta.Protocol, ClientIP: meta.ClientAddr.Addr().String(), ClientPort: int(meta.ClientAddr.Port()),
		Listener: meta.Listener, Route: meta.Route, Upstream: meta.Upstream, CreatedAt: meta.StartedAt, State: "active", LastActivity: meta.StartedAt,
	}})
}

func (p *Pipeline) PublishEntity(entity FlowEntity) {
	if p == nil {
		return
	}
	p.enqueue(pipelineItem{entity: &entity})
}

func (p *Pipeline) Update(id flow.ID, counters flow.Counters) {
	if p == nil {
		return
	}
	now := counters.LastActivity
	if now.IsZero() {
		now = time.Now().UTC()
	}
	p.mu.Lock()
	current, ok := p.active[id]
	if !ok || p.closed {
		p.mu.Unlock()
		return
	}
	current.lastCounters = counters
	if current.lastCheckpoint.IsZero() || now.Sub(current.lastCheckpoint) >= p.flushInterval {
		current.lastCheckpoint = now
		p.active[id] = current
		checkpoint := FlowCheckpoint{FlowID: id.String(), RecordedAt: now, LastActivity: counters.LastActivity, BytesUp: counters.BytesUp, BytesDown: counters.BytesDown, SegmentsUp: counters.SegmentsUp, SegmentsDown: counters.SegmentsDown}
		p.mu.Unlock()
		p.enqueue(pipelineItem{checkpoint: &checkpoint})
		return
	}
	p.active[id] = current
	p.mu.Unlock()
}

func (p *Pipeline) Close(summary flow.Summary) {
	if p == nil {
		return
	}
	p.mu.Lock()
	current := p.active[summary.ID]
	delete(p.active, summary.ID)
	p.mu.Unlock()
	ended := defaultTime(summary.EndedAt)
	started := summary.StartedAt
	if started.IsZero() {
		started = current.meta.StartedAt
	}
	last := summary.LastActivity
	if last.IsZero() {
		last = current.lastCounters.LastActivity
	}
	if last.IsZero() {
		last = started
	}
	record := FlowRecord{FlowID: summary.ID.String(), Protocol: summary.Protocol, ClientIP: summary.ClientAddr.Addr().String(), ClientPort: int(summary.ClientAddr.Port()), Listener: summary.Listener, Route: summary.Route, Upstream: summary.Upstream, StartedAt: started, EndedAt: ended, LastActivity: last, BytesUp: summary.BytesUp, BytesDown: summary.BytesDown, CloseReason: summary.CloseReason}
	checkpoint := FlowCheckpoint{FlowID: record.FlowID, RecordedAt: ended, LastActivity: last, BytesUp: summary.BytesUp, BytesDown: summary.BytesDown, SegmentsUp: current.lastCounters.SegmentsUp, SegmentsDown: current.lastCounters.SegmentsDown}
	p.enqueue(pipelineItem{entity: &FlowEntity{
		FlowID: record.FlowID, Protocol: record.Protocol, ClientIP: record.ClientIP, ClientPort: record.ClientPort,
		Listener: record.Listener, Route: record.Route, Upstream: record.Upstream, CreatedAt: started, EndedAt: &ended,
		State: "closed", LastActivity: last, BytesUp: record.BytesUp, BytesDown: record.BytesDown,
	}, flow: &record, checkpoint: &checkpoint})
}

func (p *Pipeline) Reject(rejection flow.Rejection) {
	if p == nil || !p.logRejections {
		return
	}
	event := RejectionRow{EventID: uuidLike(), Protocol: rejection.Protocol, ClientIP: rejection.ClientAddr.Addr().String(), ClientPort: int(rejection.ClientAddr.Port()), Listener: rejection.Listener, Port: listenerPort(rejection.Listener), Reason: rejection.Reason, MatchedRuleType: rejection.MatchedRuleType, MatchedRuleValue: rejection.MatchedRuleValue, RecordedAt: defaultTime(rejection.RecordedAt)}
	key := event.ClientIP + "|" + event.Protocol + "|" + event.Reason + "|" + event.MatchedRuleType + "|" + event.MatchedRuleValue
	if !p.allowRejection(key, event.RecordedAt) {
		return
	}
	p.enqueue(pipelineItem{rejection: &event})
}

func (p *Pipeline) allowRejection(key string, now time.Time) bool {
	p.rejectMu.Lock()
	defer p.rejectMu.Unlock()

	if p.lastSweep.IsZero() || now.Sub(p.lastSweep) >= rejectionDedupeCleanupInterval {
		for existingKey, seenAt := range p.recent {
			if now.Sub(seenAt) >= rejectionDedupeTTL {
				delete(p.recent, existingKey)
			}
		}
		p.lastSweep = now
	}
	if seenAt, ok := p.recent[key]; ok && now.Sub(seenAt) < rejectionDedupeTTL {
		return false
	}
	if len(p.recent) >= rejectionDedupeMaxEntries {
		oldestKey := ""
		var oldestAt time.Time
		for existingKey, seenAt := range p.recent {
			if oldestKey == "" || seenAt.Before(oldestAt) {
				oldestKey, oldestAt = existingKey, seenAt
			}
		}
		if oldestKey != "" {
			delete(p.recent, oldestKey)
		}
	}
	p.recent[key] = now
	return true
}

func (p *Pipeline) enqueue(item pipelineItem) bool {
	p.mu.Lock()
	closed := p.closed
	if !closed {
		select {
		case p.geoCh <- item:
			if p.metrics != nil {
				p.metrics.AddIPLogEvents(item.recordCount())
			}
			p.mu.Unlock()
			return true
		default:
		}
	}
	p.mu.Unlock()
	if p.metrics != nil {
		p.metrics.AddIPLogDrops(item.recordCount())
	}
	return !closed && false
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
	go func() { p.wg.Wait(); close(done) }()
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
		if item.flow != nil {
			p.enrich(&item.flow.ASN, &item.flow.ASOrg, &item.flow.Country, item.flow.ClientIP)
		}
		if item.rejection != nil {
			p.enrich(&item.rejection.ASN, &item.rejection.ASOrg, &item.rejection.Country, item.rejection.ClientIP)
		}
		select {
		case p.writeCh <- item:
		default:
			if p.metrics != nil {
				p.metrics.AddIPLogDrops(item.recordCount())
			}
		}
	}
}

func (p *Pipeline) runWriteWorker() {
	entities := make([]FlowEntity, 0, p.batchSize)
	flows := make([]FlowRecord, 0, p.batchSize)
	checkpoints := make([]FlowCheckpoint, 0, p.batchSize)
	rejections := make([]RejectionRow, 0, p.batchSize)
	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()
	recordResult := func(event string, count int, err error) {
		if count == 0 {
			return
		}
		if err != nil {
			if p.metrics != nil {
				p.metrics.AddIPLogDrops(uint64(count))
			}
			util.Event(p.logger, slog.LevelError, event, "batch.size", count, "error", err)
			return
		}
		if p.metrics != nil {
			p.metrics.AddIPLogWrites(uint64(count))
		}
	}
	flush := func() {
		if p.store == nil {
			if p.metrics != nil {
				p.metrics.AddIPLogDrops(uint64(len(entities) + len(flows) + len(checkpoints) + len(rejections)))
			}
			entities = entities[:0]
			flows = flows[:0]
			checkpoints = checkpoints[:0]
			rejections = rejections[:0]
			return
		}
		if len(entities) > 0 {
			recordResult("audit.flow_entity_write_failed", len(entities), p.store.InsertFlowEntities(entities))
			entities = entities[:0]
		}
		if len(flows) > 0 {
			recordResult("audit.flow_write_failed", len(flows), p.store.InsertFlows(flows))
			flows = flows[:0]
		}
		if len(checkpoints) > 0 {
			recordResult("audit.checkpoint_write_failed", len(checkpoints), p.store.InsertCheckpoints(checkpoints))
			checkpoints = checkpoints[:0]
		}
		if len(rejections) > 0 {
			recordResult("audit.rejection_write_failed", len(rejections), p.store.InsertRejections(rejections))
			rejections = rejections[:0]
		}
	}
	for {
		select {
		case item, ok := <-p.writeCh:
			if !ok {
				flush()
				return
			}
			if item.entity != nil {
				entities = append(entities, *item.entity)
			}
			if item.flow != nil {
				flows = append(flows, *item.flow)
			}
			if item.checkpoint != nil {
				checkpoints = append(checkpoints, *item.checkpoint)
			}
			if item.rejection != nil {
				rejections = append(rejections, *item.rejection)
			}
			if len(entities)+len(flows)+len(checkpoints)+len(rejections) >= p.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (p *Pipeline) enrich(asn *int, asOrg, country *string, ip string) {
	if p.lookup == nil {
		return
	}
	result := p.lookup.Lookup(net.ParseIP(ip))
	*asn, *asOrg, *country = result.ASN, result.ASOrg, result.Country
}

func listenerPort(listener string) int {
	if _, port, err := net.SplitHostPort(listener); err == nil {
		if value, err := strconv.Atoi(port); err == nil {
			return value
		}
	}
	return 0
}

func uuidLike() string { return uuid.NewString() }
