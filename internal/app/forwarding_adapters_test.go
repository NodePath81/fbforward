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

var _ forwarding.AdmissionPolicy = (*firewallPolicy)(nil)
var _ forwarding.UpstreamPicker = (*upstreamPicker)(nil)
var _ forwarding.DialFeedback = (*upstreamPicker)(nil)
