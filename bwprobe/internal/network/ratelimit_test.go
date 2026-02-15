package network

import (
	"testing"
	"time"
)

func TestLimiterWaitTiming(t *testing.T) {
	limiter := New(10 * 1024 * 1024) // 10 MB/s
	const iterations = 100
	const size = 1400
	start := time.Now()
	for i := 0; i < iterations; i++ {
		limiter.Wait(size)
	}
	elapsed := time.Since(start)

	expected := float64(iterations*size) / (10 * 1024 * 1024) // seconds
	max := expected * 5                                       // generous tolerance to avoid flakiness on busy CI
	if elapsed.Seconds() > max {
		t.Fatalf("elapsed %v exceeds max tolerance %fs", elapsed, max)
	}
}

func TestLimiterNoDelayWhenDisabled(t *testing.T) {
	start := time.Now()
	var limiter *Limiter
	limiter.Wait(1000)
	if time.Since(start) > time.Millisecond {
		t.Fatalf("nil limiter should not wait")
	}

	limiter = New(0)
	start = time.Now()
	limiter.Wait(1000)
	if time.Since(start) > time.Millisecond {
		t.Fatalf("zero rate limiter should not wait")
	}
}
