package firewall

import (
	"net"
	"strings"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
)

type fakeLookupProvider struct {
	result       geoip.LookupResult
	availability geoip.Availability
}

func (f fakeLookupProvider) Lookup(net.IP) geoip.LookupResult {
	if f.result.ASNDBAvailable || f.result.CountryAvailable {
		return f.result
	}
	return geoip.LookupResult{
		ASN:              f.result.ASN,
		ASOrg:            f.result.ASOrg,
		Country:          f.result.Country,
		ASNDBAvailable:   f.availability.ASNDBAvailable,
		CountryAvailable: f.availability.CountryAvailable,
	}
}

func (f fakeLookupProvider) Availability() geoip.Availability {
	return f.availability
}

func TestCIDRDenyRule(t *testing.T) {
	engine, err := NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules: []config.FirewallRule{{
			Action: "deny",
			CIDR:   "10.0.0.0/8",
		}},
	}, nil, metrics.NewMetrics(nil), nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}
	if engine.Check(net.ParseIP("10.1.2.3")) {
		t.Fatalf("expected CIDR deny")
	}
	if !engine.Check(net.ParseIP("192.168.1.10")) {
		t.Fatalf("expected default allow for non-matching IP")
	}
}

func TestFirstMatchWins(t *testing.T) {
	engine, err := NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "deny",
		Rules: []config.FirewallRule{
			{Action: "allow", CIDR: "10.0.0.0/8"},
			{Action: "deny", CIDR: "10.1.0.0/16"},
		},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}
	if !engine.Check(net.ParseIP("10.1.2.3")) {
		t.Fatalf("expected first matching allow rule to win")
	}
}

func TestASNRuleSkippedWhenDBUnavailable(t *testing.T) {
	engine, err := NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules: []config.FirewallRule{{
			Action: "deny",
			ASN:    13335,
		}},
	}, fakeLookupProvider{
		result:       geoip.LookupResult{ASN: 13335},
		availability: geoip.Availability{ASNDBAvailable: false},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}
	if !engine.Check(net.ParseIP("1.1.1.1")) {
		t.Fatalf("expected ASN rule to fail open when DB unavailable")
	}
}

func TestCountryRuleMatches(t *testing.T) {
	engine, err := NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "deny",
		Rules: []config.FirewallRule{{
			Action:  "allow",
			Country: "US",
		}},
	}, fakeLookupProvider{
		result:       geoip.LookupResult{Country: "US"},
		availability: geoip.Availability{CountryAvailable: true},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}
	if !engine.Check(net.ParseIP("8.8.8.8")) {
		t.Fatalf("expected country rule match")
	}
}

func TestCountryRuleSkippedWhenDBUnavailable(t *testing.T) {
	engine, err := NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules: []config.FirewallRule{{
			Action:  "deny",
			Country: "US",
		}},
	}, fakeLookupProvider{
		result:       geoip.LookupResult{Country: "US"},
		availability: geoip.Availability{CountryAvailable: false},
	}, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}
	if !engine.Check(net.ParseIP("8.8.8.8")) {
		t.Fatalf("expected country rule to fail open when DB unavailable")
	}
}

func TestIPv6CIDRRuleMatches(t *testing.T) {
	engine, err := NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules: []config.FirewallRule{{
			Action: "deny",
			CIDR:   "2001:db8::/32",
		}},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}
	if engine.Check(net.ParseIP("2001:db8::1")) {
		t.Fatalf("expected IPv6 CIDR deny")
	}
}

func TestDenyMetricUsesRuleLabels(t *testing.T) {
	m := metrics.NewMetrics(nil)
	engine, err := NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules: []config.FirewallRule{{
			Action: "deny",
			CIDR:   "10.0.0.0/8",
		}},
	}, nil, m, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}
	if engine.Check(net.ParseIP("10.1.2.3")) {
		t.Fatalf("expected deny")
	}
	rendered := m.Render()
	if !strings.Contains(rendered, `fbforward_firewall_denied_total{rule_type="cidr",rule_value="10.0.0.0/8"} 1`) {
		t.Fatalf("expected labeled firewall deny metric, got:\n%s", rendered)
	}
}

func TestDecideReturnsRuleMetadata(t *testing.T) {
	engine, err := NewEngine(config.FirewallConfig{
		Enabled: true,
		Default: "allow",
		Rules: []config.FirewallRule{{
			Action: "deny",
			CIDR:   "10.0.0.0/8",
		}},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine error: %v", err)
	}

	decision := engine.Decide(net.ParseIP("10.1.2.3"))
	if decision.Allowed {
		t.Fatalf("expected deny decision")
	}
	if decision.RuleType != "cidr" || decision.RuleValue != "10.0.0.0/8" {
		t.Fatalf("unexpected decision metadata: %+v", decision)
	}

	allowed := engine.Decide(net.ParseIP("192.168.1.10"))
	if !allowed.Allowed {
		t.Fatalf("expected allow decision")
	}
	if allowed.RuleType != "" || allowed.RuleValue != "" {
		t.Fatalf("expected allow decision to omit rule metadata, got %+v", allowed)
	}
}
