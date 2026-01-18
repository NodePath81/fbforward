package measure

import (
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
)

const retryDelay = 30 * time.Second

type SchedulerConfig struct {
	MinInterval         time.Duration
	MaxInterval         time.Duration
	InterUpstreamGap    time.Duration
	MaxUtilization      float64
	RequiredHeadroomBps int64
	TCPTargetUpBps      int64
	TCPTargetDownBps    int64
	UDPTargetUpBps      int64
	UDPTargetDownBps    int64
	Protocols           []string
	RateWindow          time.Duration
}

type Scheduler struct {
	cfg           SchedulerConfig
	metrics       *metrics.Metrics
	upstreams     []*upstream.Upstream
	mu            sync.Mutex
	queue         []scheduledMeasurement
	lastRun       map[string]time.Time
	rng           *rand.Rand
	nextAvailable time.Time
}

type scheduledMeasurement struct {
	upstream *upstream.Upstream
	protocol string
	dueAt    time.Time
}

func NewScheduler(cfg SchedulerConfig, metrics *metrics.Metrics, upstreams []*upstream.Upstream, rng *rand.Rand) *Scheduler {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Scheduler{
		cfg:       cfg,
		metrics:   metrics,
		upstreams: upstreams,
		lastRun:   make(map[string]time.Time),
		rng:       rng,
	}
}

func (s *Scheduler) Schedule() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	queued := make(map[string]struct{}, len(s.queue))
	for _, item := range s.queue {
		queued[s.key(item.upstream.Tag, item.protocol)] = struct{}{}
	}

	for _, up := range s.upstreams {
		for _, proto := range s.cfg.Protocols {
			key := s.key(up.Tag, proto)
			if _, ok := queued[key]; ok {
				continue
			}
			if last, ok := s.lastRun[key]; ok && now.Sub(last) < s.cfg.MinInterval {
				continue
			}
			dueAt := now.Add(s.nextInterval())
			s.queue = append(s.queue, scheduledMeasurement{
				upstream: up,
				protocol: proto,
				dueAt:    dueAt,
			})
			queued[key] = struct{}{}
		}
	}

	sort.Slice(s.queue, func(i, j int) bool {
		return s.queue[i].dueAt.Before(s.queue[j].dueAt)
	})
}

func (s *Scheduler) NextReady() (*scheduledMeasurement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.queue) == 0 {
		return nil, false
	}
	now := time.Now()
	if now.Before(s.nextAvailable) {
		return nil, false
	}
	next := s.queue[0]
	if now.Before(next.dueAt) {
		return nil, false
	}
	if !s.hasCapacityLocked(next.upstream, next.protocol) {
		next.dueAt = now.Add(retryDelay)
		s.queue[0] = next
		sort.Slice(s.queue, func(i, j int) bool {
			return s.queue[i].dueAt.Before(s.queue[j].dueAt)
		})
		return nil, false
	}

	s.queue = s.queue[1:]
	if s.cfg.InterUpstreamGap > 0 {
		s.nextAvailable = now.Add(s.cfg.InterUpstreamGap)
	}
	return &next, true
}

func (s *Scheduler) MarkRun(measurement scheduledMeasurement) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun[s.key(measurement.upstream.Tag, measurement.protocol)] = time.Now()
}

func (s *Scheduler) Requeue(measurement scheduledMeasurement, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	measurement.dueAt = time.Now().Add(delay)
	s.queue = append(s.queue, measurement)
	sort.Slice(s.queue, func(i, j int) bool {
		return s.queue[i].dueAt.Before(s.queue[j].dueAt)
	})
}

func (s *Scheduler) nextInterval() time.Duration {
	if s.cfg.MaxInterval <= s.cfg.MinInterval {
		return s.cfg.MinInterval
	}
	delta := s.cfg.MaxInterval - s.cfg.MinInterval
	jitter := time.Duration(s.rng.Int63n(int64(delta)))
	return s.cfg.MinInterval + jitter
}

func (s *Scheduler) hasCapacityLocked(up *upstream.Upstream, protocol string) bool {
	if s.metrics == nil {
		return true
	}
	window := s.cfg.RateWindow
	if window <= 0 {
		window = 5 * time.Second
	}
	rates := s.metrics.GetRates(up.Tag, window)
	metricsSnapshot, ok := s.metrics.GetUpstreamMetrics(up.Tag)

	targetUp := s.cfg.TCPTargetUpBps
	targetDown := s.cfg.TCPTargetDownBps
	if protocol == "udp" {
		targetUp = s.cfg.UDPTargetUpBps
		targetDown = s.cfg.UDPTargetDownBps
	}

	upCapacity := float64(targetUp)
	downCapacity := float64(targetDown)
	if ok {
		if metricsSnapshot.BandwidthUpBps > 0 {
			upCapacity = metricsSnapshot.BandwidthUpBps
		}
		if metricsSnapshot.BandwidthDownBps > 0 {
			downCapacity = metricsSnapshot.BandwidthDownBps
		}
	}

	required := float64(s.cfg.RequiredHeadroomBps)
	if required <= 0 {
		if targetUp > targetDown {
			required = float64(targetUp)
		} else {
			required = float64(targetDown)
		}
	}
	if required <= 0 {
		if upCapacity > downCapacity {
			required = upCapacity
		} else {
			required = downCapacity
		}
	}

	availableUp := upCapacity*s.cfg.MaxUtilization - rates.TotalUpBps
	availableDown := downCapacity*s.cfg.MaxUtilization - rates.TotalDownBps
	return availableUp > required && availableDown > required
}

func (s *Scheduler) key(tag, protocol string) string {
	return tag + ":" + protocol
}
