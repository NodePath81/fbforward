package policy

import (
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/util"
)

var (
	ErrDisabled     = errors.New("firewall policy is disabled")
	ErrNoPolicyFile = errors.New("firewall policy file is not configured")
)

type Snapshot struct {
	Document   Document
	Source     string
	Hash       string
	Generation uint64
	LoadedAt   time.Time
	Engine     *Engine
}

type Status struct {
	Enabled      bool
	PolicyFile   string
	Source       string
	Loaded       bool
	State        string
	Version      int
	Hash         string
	Generation   uint64
	LoadedAt     time.Time
	LastError    string
	LastReloadAt time.Time
}

type ValidationResult struct {
	Document Document
	Hash     string
}

type Provider struct {
	enabled     bool
	policyFile  string
	failInitial bool
	lookup      geoip.LookupProvider
	metrics     *metrics.Metrics
	logger      util.Logger
	current     atomic.Pointer[Snapshot]
	statusMu    sync.RWMutex
	status      Status
	reloadMu    sync.Mutex
}

func NewProvider(cfg config.FirewallConfig, lookup geoip.LookupProvider, metricSet *metrics.Metrics, logger util.Logger) (*Provider, error) {
	p := &Provider{
		enabled:     cfg.Enabled,
		policyFile:  cfg.PolicyFile,
		failInitial: cfg.ShouldFailOnInitialLoad(),
		lookup:      lookup,
		metrics:     metricSet,
		logger:      logger,
		status: Status{
			Enabled:    cfg.Enabled,
			PolicyFile: cfg.PolicyFile,
			State:      "disabled",
		},
	}
	if !cfg.Enabled {
		doc := Document{Version: SchemaVersion, Default: "allow"}
		engine, err := Compile(doc, lookup, metricSet, logger)
		if err != nil {
			return nil, err
		}
		p.install(doc, engine, "disabled", "disabled", time.Now().UTC())
		return p, nil
	}
	if cfg.PolicyFile == "" {
		return p.installLegacy(cfg)
	}
	if err := p.reloadLocked(); err != nil {
		if p.failInitial {
			return nil, err
		}
		fallback := Document{Version: SchemaVersion, Default: "deny"}
		engine, compileErr := Compile(fallback, lookup, metricSet, logger)
		if compileErr != nil {
			return nil, compileErr
		}
		p.install(fallback, engine, "fallback:deny-all", "degraded", time.Now().UTC())
		p.setError(err)
		return p, nil
	}
	return p, nil
}

func (p *Provider) installLegacy(cfg config.FirewallConfig) (*Provider, error) {
	doc := LegacyDocument(cfg)
	engine, err := Compile(doc, p.lookup, p.metrics, p.logger)
	if err != nil {
		return nil, err
	}
	p.install(doc, engine, "legacy-inline", "legacy", time.Now().UTC())
	return p, nil
}

func (p *Provider) Decide(ip net.IP) Decision {
	snapshot := p.current.Load()
	if snapshot == nil || snapshot.Engine == nil {
		return Decision{Allowed: true}
	}
	return snapshot.Engine.Decide(ip)
}

func (p *Provider) Reload() error {
	if p == nil || !p.enabled {
		return ErrDisabled
	}
	p.reloadMu.Lock()
	defer p.reloadMu.Unlock()
	return p.reloadLocked()
}

func (p *Provider) reloadLocked() error {
	if p.policyFile == "" {
		return ErrNoPolicyFile
	}
	doc, raw, err := ParseFile(p.policyFile)
	if err != nil {
		p.setError(err)
		return err
	}
	engine, err := Compile(doc, p.lookup, p.metrics, p.logger)
	if err != nil {
		p.setError(err)
		return err
	}
	now := time.Now().UTC()
	p.install(doc, engine, p.policyFile, "active", now, Hash(raw))
	return nil
}

func (p *Provider) Validate(raw []byte) (ValidationResult, error) {
	doc, err := Parse(raw)
	if err != nil {
		return ValidationResult{}, err
	}
	if _, err := Compile(doc, p.lookup, nil, nil); err != nil {
		return ValidationResult{}, err
	}
	return ValidationResult{Document: doc, Hash: Hash(raw)}, nil
}

func (p *Provider) ValidateFile() (ValidationResult, error) {
	if p == nil || p.policyFile == "" {
		return ValidationResult{}, ErrNoPolicyFile
	}
	doc, raw, err := ParseFile(p.policyFile)
	if err != nil {
		return ValidationResult{}, err
	}
	if _, err := Compile(doc, p.lookup, nil, nil); err != nil {
		return ValidationResult{}, err
	}
	return ValidationResult{Document: doc, Hash: Hash(raw)}, nil
}

func (p *Provider) Policy() Snapshot {
	if p == nil {
		return Snapshot{}
	}
	snapshot := p.current.Load()
	if snapshot == nil {
		return Snapshot{}
	}
	copy := *snapshot
	copy.Document = cloneDocument(snapshot.Document)
	return copy
}

func (p *Provider) Status() Status {
	if p == nil {
		return Status{}
	}
	p.statusMu.RLock()
	defer p.statusMu.RUnlock()
	return p.status
}

func (p *Provider) install(doc Document, engine *Engine, source, state string, loadedAt time.Time, hashes ...string) {
	hash := ""
	if len(hashes) > 0 {
		hash = hashes[0]
	} else if raw, err := json.Marshal(doc); err == nil {
		hash = Hash(raw)
	}
	p.statusMu.Lock()
	generation := p.status.Generation + 1
	p.status = Status{
		Enabled:      p.enabled,
		PolicyFile:   p.policyFile,
		Source:       source,
		Loaded:       true,
		State:        state,
		Version:      doc.Version,
		Hash:         hash,
		Generation:   generation,
		LoadedAt:     loadedAt,
		LastReloadAt: loadedAt,
	}
	p.statusMu.Unlock()
	p.current.Store(&Snapshot{Document: cloneDocument(doc), Source: source, Hash: hash, Generation: generation, LoadedAt: loadedAt, Engine: engine})
}

func (p *Provider) setError(err error) {
	if p == nil || err == nil {
		return
	}
	p.statusMu.Lock()
	p.status.LastError = err.Error()
	if p.status.Loaded && p.status.State == "active" {
		// Keep the active snapshot and expose the failed reload in status.
		p.status.State = "active"
	}
	p.statusMu.Unlock()
}

func cloneDocument(doc Document) Document {
	copy := doc
	copy.Rules = make([]Rule, len(doc.Rules))
	for i, rule := range doc.Rules {
		copy.Rules[i] = rule
		if rule.Match.SourceASN != nil {
			asn := *rule.Match.SourceASN
			copy.Rules[i].Match.SourceASN = &asn
		}
	}
	return copy
}
