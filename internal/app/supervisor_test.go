package app

import (
	"os"
	"testing"
)

func TestRestartKeepsRuntimeWhenConfigurationIsInvalid(t *testing.T) {
	configPath := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(configPath, []byte("control: [invalid"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	supervisor := NewSupervisor(configPath, nil)
	sentinel := &Runtime{}
	supervisor.runtime = sentinel
	if err := supervisor.Restart(); err == nil {
		t.Fatal("Restart accepted invalid configuration")
	}
	supervisor.mu.Lock()
	got := supervisor.runtime
	supervisor.mu.Unlock()
	if got != sentinel {
		t.Fatal("Restart discarded the current runtime after configuration validation failed")
	}
}
