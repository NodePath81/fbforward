package engine

import (
	"math"
	"testing"
	"time"
)

func TestTrimmedMean(t *testing.T) {
	values := []float64{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000}
	if got := trimmedMean(values, 0.1); got != 550 {
		t.Fatalf("trimmedMean 10%% expected 550, got %v", got)
	}
	if got := trimmedMean(nil, 0.1); got != 0 {
		t.Fatalf("trimmedMean empty expected 0, got %v", got)
	}
	if got := trimmedMean(values, 0); got != 550 {
		t.Fatalf("trimmedMean frac 0 expected mean 550, got %v", got)
	}
	if got := trimmedMean([]float64{1, 2, 3}, 0.1); got != 2 {
		t.Fatalf("trimmedMean small slice expected 2, got %v", got)
	}
}

func TestPercentile(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	tests := []struct {
		pct  float64
		want float64
	}{
		{0.0, 1},
		{0.5, 5},
		{0.8, 8},
		{0.9, 9},
		{1.0, 10},
	}
	for _, tt := range tests {
		if got := percentile(values, tt.pct); got != tt.want {
			t.Fatalf("percentile %.1f expected %v, got %v", tt.pct, tt.want, got)
		}
	}
	if got := percentile([]float64{42}, 0.3); got != 42 {
		t.Fatalf("percentile single expected 42, got %v", got)
	}
}

func TestPeakRollingWindow(t *testing.T) {
	bytes := []float64{0, 1e6, 3e6, 4e6, 4.5e6}
	times := []float64{0, 0.5, 1.0, 1.5, 2.0}
	got := peakRollingWindow(bytes, times, time.Second)
	if math.Abs(got-24_000_000) > 1 {
		t.Fatalf("peakRollingWindow expected ~24Mbps, got %v", got)
	}
	if got := peakRollingWindow(bytes, times, 5*time.Second); got != 0 {
		t.Fatalf("peakRollingWindow insufficient data expected 0, got %v", got)
	}
}
