package metrics

import (
	"math"
	"sync"
	"time"
)

// Stats summarizes RTT samples.
type Stats struct {
	Mean    time.Duration
	Min     time.Duration
	Max     time.Duration
	StdDev  time.Duration
	Samples int
}

// RTTSampler collects RTT samples at a fixed rate.
type RTTSampler struct {
	rate int
	mu   sync.Mutex

	stopOnce sync.Once

	count int
	mean  float64
	m2    float64
	min   time.Duration
	max   time.Duration

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewRTTSampler creates a sampler for the given rate (samples/sec).
func NewRTTSampler(rate int) *RTTSampler {
	if rate <= 0 {
		rate = 1
	}
	return &RTTSampler{
		rate:   rate,
		min:    0,
		max:    0,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start begins sampling using the provided ping function.
func (s *RTTSampler) Start(ping func() (time.Duration, error)) {
	interval := time.Second / time.Duration(s.rate)
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer close(s.doneCh)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				rtt, err := ping()
				if err != nil || rtt <= 0 {
					continue
				}
				s.addSample(rtt)
			}
		}
	}()
}

// Stop ends sampling and waits for completion.
func (s *RTTSampler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	<-s.doneCh
}

// Stats returns the current sampling statistics.
func (s *RTTSampler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.count == 0 {
		return Stats{}
	}

	mean := time.Duration(s.mean) * time.Microsecond
	stddev := time.Duration(0)
	if s.count > 1 {
		stddev = time.Duration(math.Sqrt(s.m2/float64(s.count-1))) * time.Microsecond
	}

	return Stats{
		Mean:    mean,
		Min:     s.min,
		Max:     s.max,
		StdDev:  stddev,
		Samples: s.count,
	}
}

func (s *RTTSampler) addSample(rtt time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.count == 0 || rtt < s.min {
		s.min = rtt
	}
	if s.count == 0 || rtt > s.max {
		s.max = rtt
	}

	value := float64(rtt.Microseconds())
	s.count++
	delta := value - s.mean
	s.mean += delta / float64(s.count)
	delta2 := value - s.mean
	s.m2 += delta * delta2
}
