package metrics

import (
	"testing"
	"time"
)

func TestRTTSamplerAddSample(t *testing.T) {
	s := &RTTSampler{}
	seq := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		15 * time.Millisecond,
		25 * time.Millisecond,
		12 * time.Millisecond,
	}
	for _, v := range seq {
		s.addSample(v)
	}

	stats := s.Stats()
	if stats.Samples != len(seq) {
		t.Fatalf("expected %d samples, got %d", len(seq), stats.Samples)
	}
	if stats.Min != 10*time.Millisecond || stats.Max != 25*time.Millisecond {
		t.Fatalf("min/max mismatch: got %v/%v", stats.Min, stats.Max)
	}
	if stats.Mean != 16400*time.Microsecond {
		t.Fatalf("mean mismatch: got %v", stats.Mean)
	}
	if stats.StdDev <= 0 {
		t.Fatalf("expected positive stddev, got %v", stats.StdDev)
	}
}

func TestRTTSamplerEmpty(t *testing.T) {
	s := &RTTSampler{}
	stats := s.Stats()
	if stats.Samples != 0 || stats.Mean != 0 || stats.StdDev != 0 {
		t.Fatalf("expected zero stats for empty sampler, got %+v", stats)
	}
}
