package iplog

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/util"
)

type batchWriter interface {
	InsertBatch([]EnrichedRecord) error
}

type Pipeline struct {
	geoCh         chan CloseEvent
	writeCh       chan EnrichedRecord
	lookup        geoip.LookupProvider
	store         batchWriter
	metrics       *metrics.Metrics
	logger        util.Logger
	batchSize     int
	flushInterval time.Duration
	closeOnce     sync.Once
	wg            sync.WaitGroup
	closed        bool
	mu            sync.RWMutex
}

func NewPipeline(cfg config.IPLogConfig, lookup geoip.LookupProvider, store batchWriter, metrics *metrics.Metrics, logger util.Logger) *Pipeline {
	return &Pipeline{
		geoCh:         make(chan CloseEvent, cfg.GeoQueueSize),
		writeCh:       make(chan EnrichedRecord, cfg.WriteQueueSize),
		lookup:        lookup,
		store:         store,
		metrics:       metrics,
		logger:        util.ComponentLogger(logger, util.CompIPLog),
		batchSize:     DefaultBatchSize,
		flushInterval: DefaultFlushAfter,
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
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return false
	}
	select {
	case p.geoCh <- event:
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
	for event := range p.geoCh {
		record := EnrichedRecord{CloseEvent: event}
		if p.lookup != nil {
			result := p.lookup.Lookup(net.ParseIP(event.IP))
			record.ASN = result.ASN
			record.ASOrg = result.ASOrg
			record.Country = result.Country
		}
		select {
		case p.writeCh <- record:
		default:
			if p.metrics != nil {
				p.metrics.IncIPLogEventDropped()
			}
			util.Event(p.logger, slog.LevelWarn, "iplog.write_queue_full", "queue.capacity", cap(p.writeCh), "result", "dropped")
		}
	}
}

func (p *Pipeline) runWriteWorker() {
	batch := make([]EnrichedRecord, 0, p.batchSize)
	timer := time.NewTimer(p.flushInterval)
	defer timer.Stop()
	timerActive := false

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := p.store.InsertBatch(batch); err != nil {
			util.Event(p.logger, slog.LevelError, "iplog.batch_write_failed", "batch.size", len(batch), "error", err)
		} else if p.metrics != nil {
			p.metrics.AddIPLogWrites(uint64(len(batch)))
			p.metrics.ObserveIPLogBatchSize(len(batch))
		}
		batch = batch[:0]
	}

	for {
		select {
		case record, ok := <-p.writeCh:
			if !ok {
				flush()
				return
			}
			batch = append(batch, record)
			if len(batch) == 1 && !timerActive {
				timer.Reset(p.flushInterval)
				timerActive = true
			}
			if len(batch) >= p.batchSize {
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
