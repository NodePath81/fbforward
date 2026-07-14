package forwarding

import (
	"context"
	"math"
	"sync"
	"time"
)

const (
	rateLimitBurstWindow = 250 * time.Millisecond
	minRateLimitBurst    = 1500
)

// byteRateLimiter is a small per-flow token bucket. The configured public
// limit is bits/sec; the limiter stores bytes/sec internally.
type byteRateLimiter struct {
	mu          sync.Mutex
	baseBPS     uint64
	overrideBPS uint64
	rate        float64
	burst       float64
	tokens      float64
	last        time.Time
	changed     chan struct{}
}

func newByteRateLimiter(limitBPS uint64) *byteRateLimiter {
	l := &byteRateLimiter{
		baseBPS: limitBPS,
		last:    time.Now(),
		changed: make(chan struct{}),
	}
	l.recomputeLocked(l.last, true)
	return l
}

// SetOverride applies a temporary backend limit. A backend can only tighten
// the policy limit that created this Flow, never widen it.
func (l *byteRateLimiter) SetOverride(limitBPS uint64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.overrideBPS = limitBPS
	l.recomputeLocked(time.Now(), false)
	l.mu.Unlock()
}

func (l *byteRateLimiter) ClearOverride() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.overrideBPS = 0
	l.recomputeLocked(time.Now(), false)
	l.mu.Unlock()
}

func (l *byteRateLimiter) recomputeLocked(now time.Time, initial bool) {
	effective := l.baseBPS
	if effective == 0 || (l.overrideBPS > 0 && l.overrideBPS < effective) {
		effective = l.overrideBPS
	}
	previousRate := l.rate
	l.rate = float64(effective) / 8
	l.burst = l.rate * rateLimitBurstWindow.Seconds()
	if l.burst < minRateLimitBurst && l.rate > 0 {
		l.burst = minRateLimitBurst
	}
	if l.rate == 0 {
		l.burst = 0
		l.tokens = 0
	} else if initial || previousRate == 0 {
		l.tokens = l.burst
	} else if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
	if !initial {
		old := l.changed
		l.changed = make(chan struct{})
		close(old)
	}
}

func (l *byteRateLimiter) replenish(now time.Time) {
	if l.last.IsZero() {
		l.last = now
	}
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens += elapsed * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
}

func (l *byteRateLimiter) Wait(ctx context.Context, size int) error {
	if l == nil || size <= 0 {
		return nil
	}
	l.mu.Lock()
	disabled := l.rate == 0
	l.mu.Unlock()
	if disabled {
		return nil
	}
	remaining := size
	for remaining > 0 {
		chunk := remaining
		if float64(chunk) > l.burst {
			chunk = int(math.Floor(l.burst))
		}
		if err := l.waitChunk(ctx, chunk); err != nil {
			return err
		}
		remaining -= chunk
	}
	return nil
}

func (l *byteRateLimiter) waitChunk(ctx context.Context, size int) error {
	for {
		now := time.Now()
		l.mu.Lock()
		l.replenish(now)
		if l.rate == 0 {
			l.mu.Unlock()
			return nil
		}
		if l.tokens >= float64(size) {
			l.tokens -= float64(size)
			l.mu.Unlock()
			return nil
		}
		wait := time.Duration((float64(size) - l.tokens) / l.rate * float64(time.Second))
		changed := l.changed
		l.mu.Unlock()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-changed:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (l *byteRateLimiter) Try(size int) bool {
	if l == nil || size <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rate == 0 {
		return true
	}
	l.replenish(time.Now())
	if l.tokens < float64(size) {
		return false
	}
	l.tokens -= float64(size)
	return true
}
