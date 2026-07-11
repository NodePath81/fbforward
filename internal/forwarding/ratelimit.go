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
	rate   float64
	burst  float64
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newByteRateLimiter(limitBPS uint64) *byteRateLimiter {
	if limitBPS == 0 {
		return nil
	}
	rate := float64(limitBPS) / 8
	burst := rate * rateLimitBurstWindow.Seconds()
	if burst < minRateLimitBurst {
		burst = minRateLimitBurst
	}
	return &byteRateLimiter{rate: rate, burst: burst, tokens: burst, last: time.Now()}
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
		if l.tokens >= float64(size) {
			l.tokens -= float64(size)
			l.mu.Unlock()
			return nil
		}
		wait := time.Duration((float64(size) - l.tokens) / l.rate * float64(time.Second))
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
	l.replenish(time.Now())
	if l.tokens < float64(size) {
		return false
	}
	l.tokens -= float64(size)
	return true
}
