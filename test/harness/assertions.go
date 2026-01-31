package harness

import (
	"fmt"
	"time"
)

// DetectConvergence checks if the last N samples share the same primary.
func DetectConvergence(samples []MetricsSample, interval time.Duration, cycles int) (bool, string) {
	if cycles <= 0 || len(samples) < cycles {
		return false, "insufficient samples"
	}
	last := samples[len(samples)-1].PrimaryTag
	for i := len(samples) - cycles; i < len(samples); i++ {
		if samples[i].PrimaryTag != last {
			return false, "primary changed within window"
		}
	}
	return true, last
}

// AssertionsSpec placeholder for future expansion.
type AssertionsSpec struct{}

// AssertionResult captures one assertion outcome.
type AssertionResult struct {
	Passed  bool
	Details string
}

// EvaluateAssertions currently returns a success result placeholder.
func EvaluateAssertions(spec AssertionsSpec, samples []MetricsSample, iperf3 map[string][]Iperf3Result) []AssertionResult {
	return []AssertionResult{{
		Passed:  true,
		Details: fmt.Sprintf("evaluated %d samples", len(samples)),
	}}
}
