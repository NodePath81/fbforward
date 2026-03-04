package measure

import (
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/upstream"
)

type SchedulerConfig struct {
	MinInterval      time.Duration
	MaxInterval      time.Duration
	InterUpstreamGap time.Duration
	Protocols        []string
}

type Scheduler struct {
	cfg           SchedulerConfig
	upstreams     []*upstream.Upstream
	mu            sync.Mutex
	queue         []scheduledMeasurement
	lastRun       map[string]time.Time
	rng           *rand.Rand
	nextAvailable time.Time
}

type scheduledMeasurement struct {
	upstream  *upstream.Upstream
	protocol  string
	direction string
	dueAt     time.Time
}

type SchedulerStatus struct {
	QueueLength   int
	NextScheduled time.Time
	LastRun       map[string]time.Time
	Pending       []PendingItem
}

type PendingItem struct {
	Upstream    string
	Protocol    string
	Direction   string
	ScheduledAt time.Time
}

func NewScheduler(cfg SchedulerConfig, upstreams []*upstream.Upstream, rng *rand.Rand) *Scheduler {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Scheduler{
		cfg:       cfg,
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
		queued[s.key(item.upstream.Tag, item.protocol, item.direction)] = struct{}{}
	}

	directions := []string{"upload", "download"}
	for _, up := range s.upstreams {
		for _, proto := range s.cfg.Protocols {
			for _, direction := range directions {
				key := s.key(up.Tag, proto, direction)
				if _, ok := queued[key]; ok {
					continue
				}
				if last, ok := s.lastRun[key]; ok && now.Sub(last) < s.cfg.MinInterval {
					continue
				}
				dueAt := now.Add(s.nextInterval())
				s.queue = append(s.queue, scheduledMeasurement{
					upstream:  up,
					protocol:  proto,
					direction: direction,
					dueAt:     dueAt,
				})
				queued[key] = struct{}{}
			}
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

	s.queue = s.queue[1:]
	if s.cfg.InterUpstreamGap > 0 {
		s.nextAvailable = now.Add(s.cfg.InterUpstreamGap)
	}
	return &next, true
}

func (s *Scheduler) MarkRun(measurement scheduledMeasurement) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun[s.key(measurement.upstream.Tag, measurement.protocol, measurement.direction)] = time.Now()
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

func (s *Scheduler) Status() SchedulerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := SchedulerStatus{
		QueueLength: len(s.queue),
		Pending:     make([]PendingItem, 0, len(s.queue)),
	}
	if len(s.queue) > 0 {
		status.NextScheduled = s.queue[0].dueAt
		for _, item := range s.queue {
			status.Pending = append(status.Pending, PendingItem{
				Upstream:    item.upstream.Tag,
				Protocol:    item.protocol,
				Direction:   item.direction,
				ScheduledAt: item.dueAt,
			})
		}
	}
	if len(s.lastRun) > 0 {
		status.LastRun = make(map[string]time.Time, len(s.lastRun))
		for key, ts := range s.lastRun {
			status.LastRun[key] = ts
		}
	}
	return status
}

func (s *Scheduler) nextInterval() time.Duration {
	if s.cfg.MaxInterval <= s.cfg.MinInterval {
		return s.cfg.MinInterval
	}
	delta := s.cfg.MaxInterval - s.cfg.MinInterval
	jitter := time.Duration(s.rng.Int63n(int64(delta)))
	return s.cfg.MinInterval + jitter
}

func (s *Scheduler) key(tag, protocol, direction string) string {
	return tag + ":" + protocol + ":" + direction
}
