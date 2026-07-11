package policy

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
)

func TestParseStrictPolicyAndNormalizeMatchers(t *testing.T) {
	doc, err := Parse([]byte(`version: 1
default: DENY
rules:
  - id: office
    action: allow
    match:
      source_cidr: 203.0.113.8/24
  - id: us
    action: allow
    match:
      source_country: us
  - id: asn
    action: deny
    match:
      source_asn: 4134
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Default != "deny" || doc.Rules[0].Match.SourceCIDR != "203.0.113.0/24" || doc.Rules[1].Match.SourceCountry != "US" {
		t.Fatalf("unexpected normalized document: %+v", doc)
	}
}

func TestParseRejectsUnknownFieldsDuplicateIDsAndMultipleMatchers(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"unknown field", "version: 1\ndefault: allow\nextra: true\nrules: []\n"},
		{"duplicate id", "version: 1\ndefault: allow\nrules:\n- id: same\n  action: allow\n  match: {source_cidr: 10.0.0.0/8}\n- id: same\n  action: deny\n  match: {source_cidr: 192.0.2.0/24}\n"},
		{"multiple matchers", "version: 1\ndefault: allow\nrules:\n- id: bad\n  action: allow\n  match: {source_cidr: 10.0.0.0/8, source_country: US}\n"},
		{"trailing document", "version: 1\ndefault: allow\nrules: []\n---\nversion: 1\ndefault: deny\nrules: []\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.raw)); err == nil {
				t.Fatal("expected policy validation error")
			}
		})
	}
}

func TestCompilePreservesFirstMatchSemantics(t *testing.T) {
	doc, err := Parse([]byte(`version: 1
default: deny
rules:
  - id: allow-network
    action: allow
    match:
      source_cidr: 10.0.0.0/8
  - id: deny-subnet
    action: deny
    match:
      source_cidr: 10.1.0.0/16
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	engine, err := Compile(doc, nil, nil, nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !engine.Check(net.ParseIP("10.1.2.3")) {
		t.Fatal("expected first matching allow rule to win")
	}
	if engine.Check(net.ParseIP("192.0.2.1")) {
		t.Fatal("expected default deny")
	}
}

func TestProviderReloadFailureKeepsPreviousSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "firewall.yaml")
	initial := []byte("version: 1\ndefault: deny\nrules: []\n")
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewProvider(config.FirewallConfig{Enabled: true, PolicyFile: path}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	before := p.Status()
	if p.Decide(net.ParseIP("192.0.2.1")).Allowed {
		t.Fatal("expected initial deny policy")
	}
	if err := os.WriteFile(path, []byte("version: 1\ndefault: maybe\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Reload(); err == nil {
		t.Fatal("expected reload validation error")
	}
	after := p.Status()
	if after.Generation != before.Generation || after.Hash != before.Hash {
		t.Fatalf("failed reload changed active metadata: before=%+v after=%+v", before, after)
	}
	if p.Decide(net.ParseIP("192.0.2.1")).Allowed {
		t.Fatal("failed reload changed active decision")
	}
}

func TestProviderReloadUpdatesGenerationAndHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "firewall.yaml")
	if err := os.WriteFile(path, []byte("version: 1\ndefault: deny\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewProvider(config.FirewallConfig{Enabled: true, PolicyFile: path}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	old := p.Status()
	updated := []byte("version: 1\ndefault: allow\nrules: []\n")
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	status := p.Status()
	if status.Generation != old.Generation+1 || status.Hash == old.Hash || status.Version != SchemaVersion {
		t.Fatalf("unexpected reload status: old=%+v new=%+v", old, status)
	}
	if !p.Decide(net.ParseIP("192.0.2.1")).Allowed {
		t.Fatal("expected updated allow policy")
	}
}

func TestProviderInitialLoadFailureCanFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	fail := true
	if _, err := NewProvider(config.FirewallConfig{Enabled: true, PolicyFile: path, FailOnInitialLoad: &fail}, nil, nil, nil); err == nil {
		t.Fatal("expected fail_on_initial_load to reject startup")
	}
	fail = false
	p, err := NewProvider(config.FirewallConfig{Enabled: true, PolicyFile: path, FailOnInitialLoad: &fail}, nil, nil, nil)
	if err != nil {
		t.Fatalf("expected fail-closed fallback, got %v", err)
	}
	if p.Status().State != "degraded" || p.Decide(net.ParseIP("192.0.2.1")).Allowed {
		t.Fatalf("expected degraded deny-all fallback: status=%+v", p.Status())
	}
}

func TestProviderConcurrentDecideAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "firewall.yaml")
	if err := os.WriteFile(path, []byte("version: 1\ndefault: deny\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewProvider(config.FirewallConfig{Enabled: true, PolicyFile: path}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = p.Decide(net.ParseIP("192.0.2.1"))
			}
		}()
	}
	for i := 0; i < 20; i++ {
		if err := p.Reload(); err != nil {
			t.Fatalf("Reload: %v", err)
		}
	}
	wg.Wait()
	if strings.TrimSpace(p.Status().Hash) == "" {
		t.Fatal("expected policy hash")
	}
}
