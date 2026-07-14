package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

const frozenConfigExampleSHA256 = "c5a82368de4bc3114c3defcbca4e2324f025e6faaf7459c1751ff79c34bfc500"

func TestFrozenConfigExampleFixture(t *testing.T) {
	const path = "testdata/config.example.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen config fixture: %v", err)
	}
	hash := sha256.Sum256(raw)
	if got := hex.EncodeToString(hash[:]); got != frozenConfigExampleSHA256 {
		t.Fatalf("frozen config fixture changed: got sha256 %s, want %s", got, frozenConfigExampleSHA256)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse frozen config fixture: %v", err)
	}
	if len(cfg.Forwarding.Listeners) != 2 {
		t.Fatalf("expected two listeners in frozen config fixture, got %d", len(cfg.Forwarding.Listeners))
	}
	if len(cfg.Upstreams) != 2 {
		t.Fatalf("expected two upstreams in frozen config fixture, got %d", len(cfg.Upstreams))
	}
}
