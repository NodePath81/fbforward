package harness

import (
	"fmt"
	"os"
	"path/filepath"
)

// Harness orchestrates one scenario run end-to-end.
type Harness struct {
	WorkDir   string
	Scenario  *Scenario
	Topology  *Topology
	Processes *ProcessManager
	Metrics   *MetricsCollector
	AuthToken string
}

// NewHarness builds a harness for a loaded scenario.
func NewHarness(workDir string, scenario *Scenario) *Harness {
	return &Harness{
		WorkDir:   workDir,
		Scenario:  scenario,
		Processes: NewProcessManager(),
	}
}

// Setup prepares namespaces, shaping, configs, and metrics collector.
func (h *Harness) Setup() error {
	if h.Scenario == nil {
		return fmt.Errorf("nil scenario")
	}
	ns0, err := LaunchNamespaceShell()
	if err != nil {
		return fmt.Errorf("launch ns0: %w", err)
	}
	h.Topology = &Topology{
		Internet:   ns0,
		Namespaces: map[string]*Namespace{"ns0": ns0},
	}
	// Child namespaces will be created on demand by topology helpers in tests; keep minimal for now.
	return nil
}

// Start would launch processes (fbforward, fbmeasure, iperf3). Stubbed to ensure compilation.
func (h *Harness) Start() error {
	return nil
}

// Run executes scenario timeline actions. Stubbed for now.
func (h *Harness) Run() error {
	return nil
}

// Verify evaluates assertions using collected metrics.
func (h *Harness) Verify() error {
	return nil
}

// ExportArtifacts writes any logs/outputs. Currently no-op.
func (h *Harness) ExportArtifacts() error {
	return nil
}

// Cleanup tears down namespaces and processes.
func (h *Harness) Cleanup() {
	if h.Processes != nil {
		h.Processes.StopAll()
	}
	if h.Topology != nil && h.Topology.Internet != nil {
		_ = h.Topology.Internet.Cleanup()
	}
}

// EnsureWorkDir creates the working directory.
func (h *Harness) EnsureWorkDir() error {
	if h.WorkDir == "" {
		return fmt.Errorf("workdir not set")
	}
	return os.MkdirAll(h.WorkDir, 0o755)
}

// ConfigPath returns path inside workdir.
func (h *Harness) ConfigPath(name string) string {
	return filepath.Join(h.WorkDir, name)
}
