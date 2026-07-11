package policy

import (
	"context"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/flow"
)

func TestOnlineProviderPriorityAndPersistentPrecedence(t *testing.T) {
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := NewOnlineProvider(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	deny, err := BuildOnlineRule(OnlineRuleSpec{RuleID: "deny", Action: "deny", Matcher: OnlineMatcher{SourceCIDR: "198.51.100.0/24", Protocol: "tcp", Port: intPtr(443)}, Priority: 10, TTL: time.Hour}, now)
	if err != nil {
		t.Fatal(err)
	}
	route, err := BuildOnlineRule(OnlineRuleSpec{RuleID: "route", Action: "route_override", Matcher: OnlineMatcher{SourceIP: "198.51.100.10"}, Params: OnlineParams{Upstream: "backup"}, Priority: 100, TTL: time.Hour}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Create(deny, audit.OnlineRuleEvent{Operation: "create"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Create(route, audit.OnlineRuleEvent{Operation: "create"}); err != nil {
		t.Fatal(err)
	}
	meta := flow.Meta{Protocol: "tcp", ClientAddr: netip.MustParseAddrPort("198.51.100.10:1234"), Listener: ":443"}
	evaluation := provider.Evaluate(meta, true)
	if evaluation.Action != "deny" || evaluation.RuleID != "deny" || evaluation.Allowed {
		t.Fatalf("online deny did not win: %+v", evaluation)
	}
	if got := provider.Evaluate(meta, false); got.Allowed {
		t.Fatalf("persistent deny was bypassed: %+v", got)
	}
}

func TestOnlineProviderRateLimitAndTTL(t *testing.T) {
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := NewOnlineProvider(store)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := BuildOnlineRule(OnlineRuleSpec{Action: "rate_limit", Matcher: OnlineMatcher{SourceIP: "192.0.2.1"}, Params: OnlineParams{LimitBPS: 8000}, TTL: time.Second}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Create(rule, audit.OnlineRuleEvent{Operation: "create"}); err != nil {
		t.Fatal(err)
	}
	evaluation := provider.Evaluate(flow.Meta{Protocol: "udp", ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"), Listener: ":53"}, true)
	if evaluation.Action != "rate_limit" || evaluation.RateLimitBPS != 8000 {
		t.Fatalf("unexpected rate limit evaluation: %+v", evaluation)
	}
	if err := provider.Expire(rule.RuleID, time.Now().UTC(), audit.OnlineRuleEvent{Operation: "expire"}); err != nil {
		t.Fatal(err)
	}
	if evaluation := provider.Evaluate(flow.Meta{Protocol: "udp", ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"), Listener: ":53"}, true); evaluation.Matched {
		t.Fatalf("expired rule remained in memory: %+v", evaluation)
	}
}

func TestOnlineMatcherRequiresEveryConfiguredField(t *testing.T) {
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := NewOnlineProvider(store)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := BuildOnlineRule(OnlineRuleSpec{
		RuleID: "tcp-443", Action: "deny",
		Matcher: OnlineMatcher{SourceIP: "192.0.2.1", Protocol: "tcp", Port: intPtr(443)},
		TTL:     time.Hour,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Create(rule, audit.OnlineRuleEvent{Operation: "create"}); err != nil {
		t.Fatal(err)
	}
	if got := provider.Evaluate(flow.Meta{Protocol: "tcp", ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"), Listener: ":8443"}, true); got.Matched {
		t.Fatalf("port mismatch unexpectedly matched: %+v", got)
	}
	if got := provider.Evaluate(flow.Meta{Protocol: "udp", ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"), Listener: ":443"}, true); got.Matched {
		t.Fatalf("protocol mismatch unexpectedly matched: %+v", got)
	}
	if got := provider.Evaluate(flow.Meta{Protocol: "tcp", ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234"), Listener: ":443"}, true); !got.Matched || got.Action != "deny" {
		t.Fatalf("expected complete matcher to match: %+v", got)
	}
}

func TestOnlineProviderRestoresOnlyActiveRules(t *testing.T) {
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	active, err := BuildOnlineRule(OnlineRuleSpec{RuleID: "active", Action: "deny", Matcher: OnlineMatcher{SourceIP: "192.0.2.1"}, TTL: time.Hour}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOnlineRule(active, audit.OnlineRuleEvent{Operation: "create"}); err != nil {
		t.Fatal(err)
	}
	expired, err := BuildOnlineRule(OnlineRuleSpec{RuleID: "expired", Action: "deny", Matcher: OnlineMatcher{SourceIP: "192.0.2.2"}, TTL: time.Hour}, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOnlineRule(expired, audit.OnlineRuleEvent{Operation: "create"}); err != nil {
		t.Fatal(err)
	}
	provider, err := NewOnlineProvider(store)
	if err != nil {
		t.Fatal(err)
	}
	if !provider.DecideDeny(flow.Meta{Protocol: "tcp", ClientAddr: netip.MustParseAddrPort("192.0.2.1:1")}).Matched {
		t.Fatal("active rule was not restored")
	}
	if provider.DecideDeny(flow.Meta{Protocol: "tcp", ClientAddr: netip.MustParseAddrPort("192.0.2.2:1")}).Matched {
		t.Fatal("expired rule was restored")
	}
}

func TestOnlineProviderAutomaticExpiryAuditsAndUpdatesStatus(t *testing.T) {
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := NewOnlineProvider(store, OnlineProviderOptions{ExpiryInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := BuildOnlineRule(OnlineRuleSpec{RuleID: "soon", Action: "deny", Matcher: OnlineMatcher{SourceIP: "192.0.2.3"}, TTL: 10 * time.Millisecond}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Create(rule, audit.OnlineRuleEvent{Operation: "create"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.Start(ctx.Done())
	deadline := time.Now().Add(500 * time.Millisecond)
	for provider.Status().ActiveRules != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if provider.Status().ActiveRules != 0 {
		t.Fatalf("rule did not expire: %+v", provider.Status())
	}
	events, err := store.QueryOnlineRuleEvents(rule.RuleID)
	if err != nil || len(events) != 2 || events[1].Operation != "expire" || events[1].Actor != "system" {
		t.Fatalf("automatic expiry event missing: %+v err=%v", events, err)
	}
}

func TestOnlineProviderExpiryFailureIsObservable(t *testing.T) {
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewOnlineProvider(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	provider.expireDue(time.Now().UTC())
	status := provider.Status()
	if status.ExpiryErrorsTotal != 1 || status.LastError == "" {
		t.Fatalf("expiry failure was not recorded: %+v", status)
	}
}

func TestOnlineProviderMarksUnavailableRouteOverride(t *testing.T) {
	store, err := audit.NewStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	available := false
	provider, err := NewOnlineProvider(store, OnlineProviderOptions{UpstreamAvailable: func(string) bool { return available }})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := BuildOnlineRule(OnlineRuleSpec{RuleID: "missing-route", Action: "route_override", Matcher: OnlineMatcher{SourceIP: "192.0.2.4"}, Params: OnlineParams{Upstream: "gone"}, TTL: time.Hour}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Create(rule, audit.OnlineRuleEvent{Operation: "create"}); err != nil {
		t.Fatal(err)
	}
	state, reason := provider.RuleState(rule, time.Now().UTC())
	if state != "unavailable" || reason == "" {
		t.Fatalf("unexpected route state=%q reason=%q", state, reason)
	}
	if provider.DecideAction(flow.Meta{Protocol: "tcp", ClientAddr: netip.MustParseAddrPort("192.0.2.4:1")}).Matched {
		t.Fatal("unavailable route override was applied")
	}
	available = true
	if evaluation := provider.DecideAction(flow.Meta{Protocol: "tcp", ClientAddr: netip.MustParseAddrPort("192.0.2.4:1")}); !evaluation.Matched || evaluation.UpstreamOverride != "gone" {
		t.Fatalf("route override did not recover when upstream became available: %+v", evaluation)
	}
}

func TestOnlineRuleValidationBounds(t *testing.T) {
	base := OnlineRuleSpec{Action: "deny", Matcher: OnlineMatcher{SourceIP: "192.0.2.5"}, TTL: time.Minute}
	tooLongID := base
	tooLongID.RuleID = strings.Repeat("x", MaxOnlineRuleID+1)
	if err := ValidateOnlineRuleSpec(tooLongID); err == nil {
		t.Fatal("expected rule id length validation")
	}
	badPriority := base
	badPriority.Priority = MaxOnlinePriority + 1
	if err := ValidateOnlineRuleSpec(badPriority); err == nil {
		t.Fatal("expected priority validation")
	}
	badAction := base
	badAction.Action = "allow"
	if err := ValidateOnlineRuleSpec(badAction); err == nil {
		t.Fatal("expected online allow rejection")
	}
}

func intPtr(value int) *int { return &value }
