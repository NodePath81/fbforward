package harness

import (
	"fmt"
	"time"
)

// DetectConvergence checks if samples have a stable primary across N cycles.
func DetectConvergence(samples []MetricsSample, interval time.Duration, cycles int) (bool, string) {
	if cycles <= 0 || len(samples) == 0 {
		return false, "insufficient samples"
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	window := time.Duration(cycles) * interval
	end := samples[len(samples)-1].Timestamp
	start := end.Add(-window)
	if end.Sub(samples[0].Timestamp) < window {
		return false, "insufficient time window"
	}
	primary := ""
	for _, s := range samples {
		if s.Timestamp.Before(start) {
			continue
		}
		if s.PrimaryTag == "" {
			continue
		}
		if primary == "" {
			primary = s.PrimaryTag
			continue
		}
		if s.PrimaryTag != primary {
			return false, "primary changed within window"
		}
	}
	if primary == "" {
		return false, "primary not observed"
	}
	return true, primary
}

// AssertionResult captures one assertion outcome.
type AssertionResult struct {
	Type    string
	Passed  bool
	Reason  string
	Details string
}

// EvaluateAssertions runs timeline assertions against collected metrics.
func EvaluateAssertions(assertions []TimelineAssertion, metrics *MetricsCollector, interval time.Duration, eventOffset time.Duration, baseline time.Time) []AssertionResult {
	results := make([]AssertionResult, 0, len(assertions))
	for _, assertion := range assertions {
		switch assertion.Type {
		case "primary_is":
			results = append(results, assertPrimaryIs(assertion, metrics, interval, eventOffset))
		case "switch_count_lte":
			results = append(results, assertSwitchCount(assertion, metrics))
		case "stability_ok":
			results = append(results, assertStability(metrics, baseline))
		default:
			results = append(results, AssertionResult{
				Type:    assertion.Type,
				Passed:  false,
				Reason:  assertion.Reason,
				Details: "unknown assertion type",
			})
		}
	}
	return results
}

func assertPrimaryIs(assertion TimelineAssertion, metrics *MetricsCollector, interval time.Duration, eventOffset time.Duration) AssertionResult {
	if assertion.Upstream == "" {
		return AssertionResult{Type: assertion.Type, Passed: false, Reason: assertion.Reason, Details: "missing upstream"}
	}
	tolerance := time.Duration(float64(eventOffset) * 0.1)
	deadline := time.Now().Add(tolerance + interval)
	for {
		sample, ok := metrics.Latest()
		if ok && sample.PrimaryTag == assertion.Upstream {
			return AssertionResult{Type: assertion.Type, Passed: true, Reason: assertion.Reason, Details: fmt.Sprintf("primary=%s", sample.PrimaryTag)}
		}
		if time.Now().After(deadline) {
			last := "(none)"
			if ok {
				last = sample.PrimaryTag
			}
			return AssertionResult{Type: assertion.Type, Passed: false, Reason: assertion.Reason, Details: fmt.Sprintf("expected %s, observed %s", assertion.Upstream, last)}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func assertSwitchCount(assertion TimelineAssertion, metrics *MetricsCollector) AssertionResult {
	max := assertion.Max
	count := metrics.DeriveSwitchCount()
	passed := count <= max
	return AssertionResult{
		Type:    assertion.Type,
		Passed:  passed,
		Reason:  assertion.Reason,
		Details: fmt.Sprintf("switch_count=%d max=%d", count, max),
	}
}

func assertStability(metrics *MetricsCollector, baseline time.Time) AssertionResult {
	samples := metrics.Samples()
	if len(samples) == 0 {
		return AssertionResult{Type: "stability_ok", Passed: false, Details: "no samples"}
	}
	if baseline.IsZero() {
		return AssertionResult{Type: "stability_ok", Passed: false, Details: "baseline time not set"}
	}
	var baseSample *MetricsSample
	for i := range samples {
		if !samples[i].Timestamp.Before(baseline) {
			baseSample = &samples[i]
			break
		}
	}
	if baseSample == nil {
		return AssertionResult{Type: "stability_ok", Passed: false, Details: "baseline sample not found"}
	}
	last := samples[len(samples)-1]
	memDelta := int64(last.MemoryBytes) - int64(baseSample.MemoryBytes)
	gorDelta := last.Goroutines - baseSample.Goroutines
	switchCount := metrics.DeriveSwitchCount()

	if memDelta > 50*1024*1024 {
		return AssertionResult{Type: "stability_ok", Passed: false, Details: fmt.Sprintf("memory delta %d bytes exceeds 50MB", memDelta)}
	}
	if gorDelta > 20 {
		return AssertionResult{Type: "stability_ok", Passed: false, Details: fmt.Sprintf("goroutine delta %d exceeds 20", gorDelta)}
	}
	if switchCount >= 20 {
		return AssertionResult{Type: "stability_ok", Passed: false, Details: fmt.Sprintf("switch count %d exceeds 20", switchCount)}
	}
	for _, s := range samples {
		if s.MetricsLatency > 100*time.Millisecond {
			return AssertionResult{Type: "stability_ok", Passed: false, Details: fmt.Sprintf("metrics latency %s exceeds 100ms", s.MetricsLatency)}
		}
		if s.RPCLatency > 0 && s.RPCLatency > 100*time.Millisecond {
			return AssertionResult{Type: "stability_ok", Passed: false, Details: fmt.Sprintf("rpc latency %s exceeds 100ms", s.RPCLatency)}
		}
	}
	return AssertionResult{Type: "stability_ok", Passed: true, Details: fmt.Sprintf("mem_delta=%d gor_delta=%d switches=%d", memDelta, gorDelta, switchCount)}
}
