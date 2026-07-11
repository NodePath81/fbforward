package app

import (
	"math/rand"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/forwarding"
	"github.com/NodePath81/fbforward/internal/policy"
	"github.com/NodePath81/fbforward/internal/upstream"
)

func TestFirewallPolicyAdapterMapsDecision(t *testing.T) {
	provider, err := policy.NewProvider(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules:   []config.FirewallRule{{Action: "deny", CIDR: "10.0.0.0/8"}},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProvider error: %v", err)
	}
	policy := &firewallPolicy{provider: provider}
	decision := policy.Decide(flow.Meta{ClientAddr: netip.MustParseAddrPort("10.1.2.3:1234")})
	if decision.Allowed || decision.RuleType != "cidr" || decision.RuleValue != "10.0.0.0/8" {
		t.Fatalf("unexpected policy decision: %+v", decision)
	}

	allowed := policy.Decide(flow.Meta{ClientAddr: netip.MustParseAddrPort("192.0.2.1:1234")})
	if !allowed.Allowed {
		t.Fatalf("expected non-matching address to be allowed: %+v", allowed)
	}
}

func TestUpstreamPickerAdapterMapsActiveIPAndDialFeedback(t *testing.T) {
	selected := &upstream.Upstream{Tag: "primary"}
	selected.SetActiveIP(net.ParseIP("203.0.113.10"))
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{selected}, rand.New(rand.NewSource(1)), nil)
	manager.UpdateReachability("primary", true)
	if err := manager.SetManual("primary"); err != nil {
		t.Fatalf("SetManual error: %v", err)
	}
	picker := &upstreamPicker{manager: manager}

	got, err := picker.Pick(flow.Meta{})
	if err != nil {
		t.Fatalf("Pick error: %v", err)
	}
	if got.Tag != "primary" || got.Addr != netip.MustParseAddr("203.0.113.10") {
		t.Fatalf("unexpected picked upstream: %+v", got)
	}

	picker.MarkDialFailure(got, time.Second)
	if _, err := picker.Pick(flow.Meta{}); err == nil {
		t.Fatalf("expected dial failure feedback to make upstream unavailable")
	}
	picker.ClearDialFailure(got)
	if _, err := picker.Pick(flow.Meta{}); err != nil {
		t.Fatalf("expected clear dial failure to restore upstream: %v", err)
	}
}

func TestUpstreamPickerScopesAdaptiveRoutesAndKeepsStaticFixed(t *testing.T) {
	makeUpstream := func(tag, ip string) *upstream.Upstream {
		up := &upstream.Upstream{Tag: tag}
		up.SetActiveIP(net.ParseIP(ip))
		return up
	}
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{
		makeUpstream("outside", "203.0.113.10"),
		makeUpstream("route-a", "203.0.113.11"),
		makeUpstream("route-b", "203.0.113.12"),
		makeUpstream("static", "203.0.113.13"),
	}, rand.New(rand.NewSource(1)), nil)
	for _, tag := range []string{"outside", "route-a", "route-b", "static"} {
		manager.UpdateReachability(tag, true)
	}
	if err := manager.SetManual("outside"); err != nil {
		t.Fatal(err)
	}
	picker := newUpstreamPicker(manager, []config.RouteConfig{
		{Name: "adaptive", Strategy: "adaptive", Upstreams: []string{"route-a", "route-b"}},
		{Name: "static", Strategy: "static", Upstreams: []string{"static"}},
	})
	adaptive, err := picker.Pick(flow.Meta{Route: "adaptive"})
	if err != nil {
		t.Fatal(err)
	}
	if adaptive.Tag != "route-a" && adaptive.Tag != "route-b" {
		t.Fatalf("adaptive route escaped its upstream set: %+v", adaptive)
	}
	fixed, err := picker.Pick(flow.Meta{Route: "static"})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Tag != "static" {
		t.Fatalf("static route changed with global active selection: %+v", fixed)
	}
}

func TestUpstreamPickerRejectsUnknownRoute(t *testing.T) {
	manager := upstream.NewUpstreamManager(nil, rand.New(rand.NewSource(1)), nil)
	picker := newUpstreamPicker(manager, []config.RouteConfig{{Name: "known", Strategy: "static", Upstreams: []string{"missing"}}})
	if _, err := picker.Pick(flow.Meta{Route: "unknown"}); err == nil {
		t.Fatal("expected unknown route error")
	}
}

var _ forwarding.AdmissionPolicy = (*firewallPolicy)(nil)
var _ forwarding.UpstreamPicker = (*upstreamPicker)(nil)
var _ forwarding.DialFeedback = (*upstreamPicker)(nil)
var _ forwarding.OverridePicker = (*upstreamPicker)(nil)
