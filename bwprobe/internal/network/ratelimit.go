package network

import (
	"sync"
	"time"
)

// Limiter provides a leaky bucket rate limiter (constant drain rate).
type Limiter struct {
	rate float64
	next time.Time
	mu   sync.Mutex
}

// New creates a limiter with a given rate (bytes/sec).
func New(rate float64) *Limiter {
	return &Limiter{
		rate: rate,
	}
}

// Wait blocks until it is time to send n bytes at the configured rate.
func (l *Limiter) Wait(n int) {
	if l == nil || l.rate <= 0 || n <= 0 {
		return
	}

	wait := time.Duration(0)
	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	wait = l.next.Sub(now)
	l.next = l.next.Add(time.Duration(float64(n)/l.rate*1e9) * time.Nanosecond)
	l.mu.Unlock()

	if wait > 0 {
		time.Sleep(wait)
	}
}
