package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRemovedConfigKeysRejected(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"webui", "control:\n  " + "web" + "ui" + ":\n    enabled: true\n"},
		{"coordination", "coord" + "ination" + ":\n  endpoint: https://removed.example\n  token: token\n"},
		{"shaping", "shaping:\n  enabled: true\n  interface: eth0\n"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			path := t.TempDir() + "/config.yaml"
			if err := os.WriteFile(path, []byte(testCase.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatalf("expected removed %s configuration key to be rejected", testCase.name)
			}
		})
	}
}

func TestNotifyConfigAcceptsTokenAndDefaultsSourceInstance(t *testing.T) {
	cfg := testConfig()
	cfg.Notify = NotifyConfig{
		Enabled:     true,
		Endpoint:    "https://notify.example/v1/events",
		BearerToken: "node-token-abcdefghijklmnopqrstuvwxyz123456",
	}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected webhook config to validate: %v", err)
	}
	if cfg.Notify.BearerToken != "node-token-abcdefghijklmnopqrstuvwxyz123456" {
		t.Fatalf("expected webhook bearer token to be preserved, got %q", cfg.Notify.BearerToken)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("Hostname error: %v", err)
	}
	if cfg.Notify.SourceInstance != hostname {
		t.Fatalf("expected notify source instance %q, got %q", hostname, cfg.Notify.SourceInstance)
	}
	if got := cfg.Notify.StartupGracePeriod.Duration(); got != defaultNotifyStartupGrace {
		t.Fatalf("expected default notify.startup_grace_period %s, got %s", defaultNotifyStartupGrace, got)
	}
	if got := cfg.Notify.UnusableInterval.Duration(); got != defaultNotifyUnusableDelay {
		t.Fatalf("expected default notify.unusable_interval %s, got %s", defaultNotifyUnusableDelay, got)
	}
	if got := cfg.Notify.NotifyInterval.Duration(); got != defaultNotifyInterval {
		t.Fatalf("expected default notify.notify_interval %s, got %s", defaultNotifyInterval, got)
	}
}

func TestLoadConfigRejectsLegacyNotifyTokenEnv(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	raw := `
hostname: node-1
forwarding:
  listeners:
    - bind_port: 9000
      protocol: tcp
upstreams:
  - tag: us-1
    destination:
      host: 203.0.113.10
    measurement:
      host: 203.0.113.10
      port: 9876
control:
  auth_token: "0123456789abcdef0123456789abcdef"
` + "not" + `ify:
  enabled: true
  endpoint: https://notify.example/v1/events
  key_id: key-1
  token_env: NOTIFY_TOKEN
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatalf("expected legacy notify.token_env to be rejected")
	}
	if !strings.Contains(err.Error(), "notify.token_env") {
		t.Fatalf("expected notify.token_env removed-key error, got %v", err)
	}
}

func TestGeoIPConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*GeoIPConfig)
		want string
	}{
		{"missing databases", func(cfg *GeoIPConfig) { cfg.Enabled = true }, "geoip.enabled requires at least one"},
		{"url without local path", func(cfg *GeoIPConfig) { cfg.Enabled = true; cfg.ASNDBURL = "https://example.com/GeoLite2-ASN.mmdb" }, "requires at least one local database path"},
		{"asn local path", func(cfg *GeoIPConfig) { cfg.Enabled = true; cfg.ASNDBPath = "/tmp/GeoLite2-ASN.mmdb" }, ""},
		{"country local path", func(cfg *GeoIPConfig) { cfg.Enabled = true; cfg.CountryDBPath = "/tmp/Country-without-asn.mmdb" }, ""},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := testConfig()
			testCase.mut(&cfg.GeoIP)
			cfg.setDefaults()
			err := cfg.validate()
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("expected geoip config to validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("expected %q, got %v", testCase.want, err)
			}
		})
	}
}

func TestFlowContextConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{
			name: "requires audit store",
			mut: func(cfg *Config) {
				cfg.FlowContext.Enabled = true
				cfg.FlowContext.Identities = []FlowContextIdentity{{ID: "caddy", Token: "abcdef0123456789", Routes: []string{"web"}, Upstreams: []string{"primary"}, Namespaces: []string{"app"}}}
			},
			want: "ip_log.enabled",
		},
		{
			name: "requires namespace allowlist",
			mut: func(cfg *Config) {
				cfg.IPLog.Enabled, cfg.IPLog.DBPath = true, "/tmp/flow-context.sqlite"
				cfg.FlowContext.Enabled = true
			},
			want: "identities",
		},
		{
			name: "rejects shared control token",
			mut: func(cfg *Config) {
				cfg.IPLog.Enabled, cfg.IPLog.DBPath = true, "/tmp/flow-context.sqlite"
				cfg.FlowContext.Enabled = true
				cfg.FlowContext.Identities = []FlowContextIdentity{{ID: "backend", Token: cfg.Control.AuthToken, Routes: []string{"web"}, Upstreams: []string{"primary"}, Namespaces: []string{"app"}}}
			},
			want: "must differ",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := testConfig()
			testCase.mut(&cfg)
			cfg.setDefaults()
			err := cfg.validate()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("expected %q, got %v", testCase.want, err)
			}
		})
	}
	cfg := testConfig()
	cfg.IPLog.Enabled, cfg.IPLog.DBPath = true, "/tmp/flow-context.sqlite"
	cfg.FlowContext.Enabled = true
	cfg.FlowContext.Identities = []FlowContextIdentity{{ID: "caddy", Token: "abcdef0123456789", Routes: []string{"web"}, Upstreams: []string{"primary"}, Namespaces: []string{"app"}}}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected valid flow context config: %v", err)
	}
}

func TestListenerRouteDefaultsAndExplicitValue(t *testing.T) {
	cfg := testConfig()
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Forwarding.Listeners[0].Route, "0.0.0.0:9000"; got != want {
		t.Fatalf("default route=%q want %q", got, want)
	}
	cfg = testConfig()
	cfg.Forwarding.Listeners[0].Route = "web"
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Forwarding.Listeners[0].Route; got != "web" {
		t.Fatalf("explicit route=%q", got)
	}
}

func TestFirewallRejectsInvalidPolicy(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*FirewallConfig)
		want string
	}{
		{name: "default", mut: func(cfg *FirewallConfig) {
			cfg.Default = "maybe"
			cfg.Rules = []FirewallRule{{Action: "deny", CIDR: "10.0.0.0/8"}}
		}, want: "firewall.default must be allow or deny"},
		{name: "action", mut: func(cfg *FirewallConfig) { cfg.Rules = []FirewallRule{{Action: "block", CIDR: "10.0.0.0/8"}} }, want: "firewall.rules[0].action must be allow or deny"},
		{name: "multiple matchers", mut: func(cfg *FirewallConfig) {
			cfg.Rules = []FirewallRule{{Action: "deny", CIDR: "10.0.0.0/8", Country: "us"}}
		}, want: "must specify exactly one matcher"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Firewall.Enabled = true
			testCase.mut(&cfg.Firewall)
			cfg.setDefaults()
			err := cfg.validate()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("expected %q, got %v", testCase.want, err)
			}
		})
	}
}

func TestFirewallPolicyFileRejectsLegacyRules(t *testing.T) {
	cfg := testConfig()
	cfg.Firewall.Enabled = true
	cfg.Firewall.PolicyFile = "/etc/fbforward/firewall.yaml"
	cfg.Firewall.Rules = []FirewallRule{{Action: "deny", CIDR: "10.0.0.0/8"}}
	cfg.setDefaults()
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected policy file and legacy rules conflict, got %v", err)
	}
}

func TestFirewallLegacyRulesProduceDeprecationWarning(t *testing.T) {
	cfg := testConfig()
	cfg.Firewall.Enabled = true
	cfg.Firewall.Rules = []FirewallRule{{Action: "deny", CIDR: "10.0.0.0/8"}}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected legacy firewall config to remain valid: %v", err)
	}
	found := false
	for _, warning := range cfg.Warnings {
		if strings.Contains(warning, "firewall.policy_file") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected firewall migration warning, got %#v", cfg.Warnings)
	}
}

func testConfig() Config {
	return Config{
		Forwarding: ForwardingConfig{
			Listeners: []ListenerConfig{
				{
					BindAddr: "0.0.0.0",
					BindPort: 9000,
					Protocol: "tcp",
				},
			},
			Limits: ForwardingLimitsConfig{
				MaxTCPConnections: 10,
				MaxUDPMappings:    10,
			},
			IdleTimeout: IdleTimeoutConfig{
				TCP: Duration(time.Second),
				UDP: Duration(time.Second),
			},
		},
		Upstreams: []UpstreamConfig{
			{
				Tag: "alpha",
				Destination: DestinationConfig{
					Host: "203.0.113.10",
				},
				Measurement: UpstreamMeasurementConfig{
					Port: 9876,
				},
			},
		},
		Control: ControlConfig{
			AuthToken: "0123456789abcdef",
		},
	}
}
