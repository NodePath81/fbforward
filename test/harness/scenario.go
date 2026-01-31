package harness

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ShapingRule describes symmetric shaping parameters.
type ShapingRule struct {
	Bandwidth string `yaml:"bandwidth"`
	Latency   string `yaml:"latency"`
	Loss      string `yaml:"loss"`
}

// TimelineAssertion captures a single assertion in time.
type TimelineAssertion struct {
	Type     string `yaml:"type"`
	Upstream string `yaml:"upstream"`
	Reason   string `yaml:"reason,omitempty"`
}

// TimelineEvent is one step in the scenario.
type TimelineEvent struct {
	T          string              `yaml:"t"`
	Action     string              `yaml:"action"`
	Upstream   string              `yaml:"upstream,omitempty"`
	NewShaping *ShapingRule        `yaml:"new_shaping,omitempty"`
	Assertions []TimelineAssertion `yaml:"assertions,omitempty"`
}

// Scenario represents one integration test scenario.
type Scenario struct {
	Name              string                 `yaml:"name"`
	Overrides         map[string]any         `yaml:"overrides"`
	Shaping           map[string]ShapingRule `yaml:"shaping"`
	Timeline          []TimelineEvent        `yaml:"timeline"`
	IntervalSeconds   int                    `yaml:"interval_seconds"`
	ConvergenceCycles int                    `yaml:"convergence_cycles"`
	Metadata          map[string]string      `yaml:"metadata,omitempty"`
	Raw               map[string]any         `yaml:",inline"`
	LoadedAt          time.Time              `yaml:"-"`
}

// LoadScenario reads a scenario YAML file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	s.LoadedAt = time.Now()
	return &s, nil
}
