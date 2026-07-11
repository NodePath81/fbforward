package policy

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/util"
)

type OnlineProvider struct {
	store    *audit.Store
	options  OnlineProviderOptions
	current  atomic.Pointer[onlineSnapshot]
	writeMu  sync.Mutex
	statusMu sync.RWMutex
	status   OnlineProviderStatus
}

func NewOnlineProvider(store *audit.Store, options ...OnlineProviderOptions) (*OnlineProvider, error) {
	if store == nil {
		return nil, ErrOnlineStoreUnavailable
	}
	var opts OnlineProviderOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.MaxRules <= 0 {
		opts.MaxRules = MaxOnlineRules
	}
	if opts.ExpiryInterval <= 0 {
		opts.ExpiryInterval = time.Minute
	}
	p := &OnlineProvider{store: store, options: opts}
	if err := p.Refresh(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *OnlineProvider) Refresh() error {
	if p == nil || p.store == nil {
		return ErrOnlineStoreUnavailable
	}
	rules, err := p.store.ListOnlineRules(time.Now().UTC(), false)
	if err != nil {
		return err
	}
	compiled, err := compileStoredRules(rules, p.options.UpstreamAvailable)
	if err != nil {
		return err
	}
	p.storeSnapshot(compiled)
	p.setActiveRules(len(compiled))
	return nil
}

func (p *OnlineProvider) List(now time.Time, includeExpired bool) ([]audit.OnlineRule, error) {
	if p == nil || p.store == nil {
		return nil, ErrOnlineStoreUnavailable
	}
	return p.store.ListOnlineRules(now, includeExpired)
}

func (p *OnlineProvider) Start(ctxDone <-chan struct{}) {
	if p == nil || p.store == nil || ctxDone == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(p.options.ExpiryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctxDone:
				return
			case now := <-ticker.C:
				p.expireDue(now.UTC())
			}
		}
	}()
}

func (p *OnlineProvider) Create(rule audit.OnlineRule, event audit.OnlineRuleEvent) error {
	if p == nil || p.store == nil {
		return ErrOnlineStoreUnavailable
	}
	compiled, err := compileStoredRule(rule, p.options.UpstreamAvailable)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	activeCount := 0
	now := time.Now().UTC()
	for _, existing := range p.snapshotRules() {
		if existing.Stored.ExpiresAt == nil || existing.Stored.ExpiresAt.After(now) {
			activeCount++
		}
	}
	if activeCount >= p.options.MaxRules {
		return fmt.Errorf("%w: maximum of %d rules reached", ErrOnlineRuleCapacity, p.options.MaxRules)
	}
	if err := p.store.CreateOnlineRule(rule, event); err != nil {
		return err
	}
	snapshot := p.snapshotRules()
	snapshot = append(snapshot, compiled)
	p.storeSnapshot(snapshot)
	p.setActiveRules(len(snapshot))
	return nil
}

func (p *OnlineProvider) Delete(ruleID string, event audit.OnlineRuleEvent) error {
	if p == nil || p.store == nil {
		return ErrOnlineStoreUnavailable
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if err := p.store.DeleteOnlineRule(ruleID, event); err != nil {
		return err
	}
	rules := p.snapshotRules()
	filtered := rules[:0]
	for _, rule := range rules {
		if rule.Stored.RuleID != ruleID {
			filtered = append(filtered, rule)
		}
	}
	p.storeSnapshot(filtered)
	p.setActiveRules(len(filtered))
	return nil
}

func (p *OnlineProvider) Expire(ruleID string, now time.Time, event audit.OnlineRuleEvent) error {
	if p == nil || p.store == nil {
		return ErrOnlineStoreUnavailable
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if err := p.store.ExpireOnlineRule(ruleID, now, event); err != nil {
		return err
	}
	rules := p.snapshotRules()
	filtered := rules[:0]
	for _, rule := range rules {
		if rule.Stored.RuleID != ruleID {
			filtered = append(filtered, rule)
		}
	}
	p.storeSnapshot(filtered)
	p.setActiveRules(len(filtered))
	p.setExpiryAt(now)
	return nil
}

func (p *OnlineProvider) expireDue(now time.Time) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	rules, err := p.store.ExpireDueOnlineRules(now)
	if err != nil {
		p.recordExpiryError(err)
		return
	}
	if len(rules) == 0 {
		return
	}
	expired := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		expired[rule.RuleID] = struct{}{}
	}
	current := p.snapshotRules()
	filtered := current[:0]
	for _, rule := range current {
		if _, ok := expired[rule.Stored.RuleID]; !ok {
			filtered = append(filtered, rule)
		}
	}
	p.storeSnapshot(filtered)
	p.setActiveRules(len(filtered))
	p.setExpiryAt(now)
}

func (p *OnlineProvider) Evaluate(meta flow.Meta, persistentAllowed bool) OnlineEvaluation {
	if deny := p.DecideDeny(meta); deny.Matched {
		return deny
	}
	if !persistentAllowed {
		return OnlineEvaluation{Allowed: false}
	}
	if action := p.DecideAction(meta); action.Matched {
		return action
	}
	return OnlineEvaluation{Allowed: true}
}

func (p *OnlineProvider) DecideDeny(meta flow.Meta) OnlineEvaluation {
	for _, rule := range p.matchingRules(meta, true) {
		return evaluationFromRule(rule, false)
	}
	return OnlineEvaluation{Allowed: true}
}

func (p *OnlineProvider) DecideAction(meta flow.Meta) OnlineEvaluation {
	for _, rule := range p.matchingRules(meta, false) {
		return evaluationFromRule(rule, true)
	}
	return OnlineEvaluation{Allowed: true}
}

func (p *OnlineProvider) matchingRules(meta flow.Meta, denyOnly bool) []runtimeOnlineRule {
	if p == nil {
		return nil
	}
	snapshot := p.current.Load()
	if snapshot == nil {
		return nil
	}
	now := time.Now().UTC()
	matched := make([]runtimeOnlineRule, 0, 1)
	for _, rule := range snapshot.rules {
		if rule.Stored.ExpiresAt != nil && !rule.Stored.ExpiresAt.After(now) {
			continue
		}
		if !p.isRuleAvailable(rule) {
			continue
		}
		// A route override can become available after startup when the
		// upstream catalog is refreshed. Keep the immutable snapshot, but
		// evaluate this copy as available for the current decision.
		rule.Available = true
		if (rule.Stored.Action == "deny") != denyOnly || !matchesOnlineRule(rule, meta) {
			continue
		}
		matched = append(matched, rule)
	}
	return matched
}

func (p *OnlineProvider) isRuleAvailable(rule runtimeOnlineRule) bool {
	if rule.Available {
		return true
	}
	if p == nil || p.options.UpstreamAvailable == nil {
		return false
	}
	return unavailableReason(rule.Stored, p.options.UpstreamAvailable) == ""
}

func (p *OnlineProvider) Status() OnlineProviderStatus {
	if p == nil {
		return OnlineProviderStatus{}
	}
	p.statusMu.RLock()
	defer p.statusMu.RUnlock()
	return p.status
}

func (p *OnlineProvider) RuleState(rule audit.OnlineRule, now time.Time) (string, string) {
	if p == nil {
		return "unavailable", "online provider is unavailable"
	}
	if !rule.Enabled {
		return "disabled", ""
	}
	if rule.ExpiresAt != nil && !rule.ExpiresAt.After(now) {
		return "expired", ""
	}
	if reason := unavailableReason(rule, p.options.UpstreamAvailable); reason != "" {
		return "unavailable", reason
	}
	return "active", ""
}

func (p *OnlineProvider) snapshotRules() []runtimeOnlineRule {
	current := p.current.Load()
	if current == nil {
		return nil
	}
	return append([]runtimeOnlineRule(nil), current.rules...)
}

func (p *OnlineProvider) storeSnapshot(rules []runtimeOnlineRule) {
	sortRuntimeRules(rules)
	p.current.Store(&onlineSnapshot{rules: append([]runtimeOnlineRule(nil), rules...)})
}

func (p *OnlineProvider) setActiveRules(count int) {
	p.statusMu.Lock()
	p.status.ActiveRules = count
	p.statusMu.Unlock()
	if p.options.Telemetry != nil {
		p.options.Telemetry.SetOnlineRulesActive(count)
	}
}

func (p *OnlineProvider) setExpiryAt(at time.Time) {
	p.statusMu.Lock()
	p.status.LastExpiryAt = at
	p.status.LastError = ""
	p.statusMu.Unlock()
}

func (p *OnlineProvider) recordExpiryError(err error) {
	p.statusMu.Lock()
	p.status.LastError = err.Error()
	p.status.ExpiryErrorsTotal++
	p.statusMu.Unlock()
	if p.options.Telemetry != nil {
		p.options.Telemetry.IncOnlineRuleExpiryError()
	}
	if p.options.Logger != nil {
		util.Event(p.options.Logger, slog.LevelError, "online_rule_expire_failed", "error", err)
	}
}
