package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModernTopologyNormalizesListenersAndRoutes(t *testing.T) {
	cfg := Config{
		Listeners: []ListenerSpec{
			{Name: "web", Bind: "0.0.0.0:443", Protocol: "tcp", Route: "web"},
			{Name: "dns", Bind: "[::]:53", Protocol: "udp", Route: "dns"},
		},
		Routes: []RouteConfig{
			{Name: "web", Strategy: "static", Upstreams: []string{"local"}},
			{Name: "dns", Strategy: "adaptive", Upstreams: []string{"dns-a", "dns-b"}},
		},
		Upstreams: []UpstreamConfig{
			{Tag: "local", Destination: DestinationConfig{Host: "127.0.0.1"}, Measurement: UpstreamMeasurementConfig{Port: 9876}},
			{Tag: "dns-a", Destination: DestinationConfig{Host: "127.0.0.1"}, Measurement: UpstreamMeasurementConfig{Port: 9876}},
			{Tag: "dns-b", Destination: DestinationConfig{Host: "127.0.0.1"}, Measurement: UpstreamMeasurementConfig{Port: 9876}},
		},
	}
	cfg.Forwarding.Limits = ForwardingLimitsConfig{MaxTCPConnections: 1, MaxUDPMappings: 1}
	cfg.Forwarding.IdleTimeout = IdleTimeoutConfig{TCP: Duration(time.Second), UDP: Duration(time.Second)}
	cfg.Control.AuthToken = "0123456789abcdef"
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Forwarding.Listeners) != 2 || cfg.Forwarding.Listeners[0].BindPort != 443 || cfg.Forwarding.Listeners[1].BindAddr != "::" {
		t.Fatalf("unexpected normalized listeners: %+v", cfg.Forwarding.Listeners)
	}
	if cfg.Routes[0].DefaultUpstream != "local" {
		t.Fatalf("expected single static route default, got %q", cfg.Routes[0].DefaultUpstream)
	}
}

func TestStaticRouteDefaultAndMultiUpstreamValidation(t *testing.T) {
	base := func(route RouteConfig) Config {
		cfg := Config{
			Listeners: []ListenerSpec{{Name: "web", Bind: ":443", Protocol: "tcp", Route: "web"}},
			Routes:    []RouteConfig{route},
			Upstreams: []UpstreamConfig{
				{Tag: "a", Destination: DestinationConfig{Host: "127.0.0.1"}, Measurement: UpstreamMeasurementConfig{Port: 9876}},
				{Tag: "b", Destination: DestinationConfig{Host: "127.0.0.2"}, Measurement: UpstreamMeasurementConfig{Port: 9876}},
			},
		}
		cfg.Forwarding.Limits = ForwardingLimitsConfig{MaxTCPConnections: 1, MaxUDPMappings: 1}
		cfg.Forwarding.IdleTimeout = IdleTimeoutConfig{TCP: Duration(time.Second), UDP: Duration(time.Second)}
		cfg.Control.AuthToken = "0123456789abcdef"
		cfg.setDefaults()
		return cfg
	}
	for _, test := range []struct {
		name  string
		route RouteConfig
		want  string
	}{
		{name: "multiple requires default", route: RouteConfig{Name: "web", Strategy: "static", Upstreams: []string{"a", "b"}}, want: "must explicitly set default_upstream"},
		{name: "default must be a member", route: RouteConfig{Name: "web", Strategy: "static", Upstreams: []string{"a", "b"}, DefaultUpstream: "missing"}, want: "is not in route upstreams"},
		{name: "adaptive rejects default", route: RouteConfig{Name: "web", Strategy: "adaptive", Upstreams: []string{"a", "b"}, DefaultUpstream: "a"}, want: "only valid for static"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := base(test.route)
			if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestModernTopologyValidationRules(t *testing.T) {
	cfg := Config{
		Listeners: []ListenerSpec{{Name: "web", Bind: ":443", Protocol: "tcp", Route: "web"}},
		Routes:    []RouteConfig{{Name: "web", Strategy: "adaptive", Upstreams: []string{"only"}}},
		Upstreams: []UpstreamConfig{{Tag: "only", Destination: DestinationConfig{Host: "127.0.0.1"}, Measurement: UpstreamMeasurementConfig{Port: 9876}}},
	}
	cfg.Forwarding.Limits = ForwardingLimitsConfig{MaxTCPConnections: 1, MaxUDPMappings: 1}
	cfg.Forwarding.IdleTimeout = IdleTimeoutConfig{TCP: Duration(time.Second), UDP: Duration(time.Second)}
	cfg.Control.AuthToken = "0123456789abcdef"
	cfg.setDefaults()
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "at least two upstreams") {
		t.Fatalf("expected adaptive cardinality error, got %v", err)
	}
}

func TestModernTopologyRejectsMissingReferencesAndDuplicates(t *testing.T) {
	base := func(route RouteConfig, listeners []ListenerSpec) Config {
		cfg := Config{
			Listeners: listeners,
			Routes:    []RouteConfig{route},
			Upstreams: []UpstreamConfig{{Tag: "local", Destination: DestinationConfig{Host: "127.0.0.1"}, Measurement: UpstreamMeasurementConfig{Port: 9876}}},
		}
		cfg.Forwarding.Limits = ForwardingLimitsConfig{MaxTCPConnections: 1, MaxUDPMappings: 1}
		cfg.Forwarding.IdleTimeout = IdleTimeoutConfig{TCP: Duration(time.Second), UDP: Duration(time.Second)}
		cfg.Control.AuthToken = "0123456789abcdef"
		cfg.setDefaults()
		return cfg
	}
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"unknown upstream", base(RouteConfig{Name: "web", Strategy: "static", Upstreams: []string{"missing"}}, []ListenerSpec{{Name: "web", Bind: ":443", Protocol: "tcp", Route: "web"}}), "unknown upstream"},
		{"duplicate listener name", base(RouteConfig{Name: "web", Strategy: "static", Upstreams: []string{"local"}}, []ListenerSpec{{Name: "same", Bind: ":443", Protocol: "tcp", Route: "web"}, {Name: "same", Bind: ":8443", Protocol: "tcp", Route: "web"}}), "duplicate listener name"},
		{"unknown route", base(RouteConfig{Name: "web", Strategy: "static", Upstreams: []string{"local"}}, []ListenerSpec{{Name: "web", Bind: ":443", Protocol: "tcp", Route: "missing"}}), "unknown route"},
		{"missing route", base(RouteConfig{Name: "web", Strategy: "static", Upstreams: []string{"local"}}, []ListenerSpec{{Name: "web", Bind: ":443", Protocol: "tcp"}}), "route must not be empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.cfg.validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestLoadConfigRejectsRemovedScoringSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	raw := `listeners:
  - name: web
    bind: 127.0.0.1:8080
    protocol: tcp
    route: web
routes:
  - name: web
    strategy: static
    upstreams: [primary]
upstreams:
  - tag: primary
    destination: {host: 127.0.0.1}
    measurement: {port: 9876}
scoring: {}
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected removed scoring section to be rejected")
	}
}

func TestLegacyTopologyMigrationCreatesRoutesAndWarning(t *testing.T) {
	cfg := testConfig()
	cfg.configLoaded = true
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Listeners) != 1 || len(cfg.Routes) != 1 || cfg.Routes[0].Strategy != "static" {
		t.Fatalf("unexpected migrated topology: listeners=%+v routes=%+v", cfg.Listeners, cfg.Routes)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "forwarding.listeners is deprecated") {
		t.Fatalf("expected migration warning, got %#v", cfg.Warnings)
	}
}

func TestModernTopologyStrictUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `
control:
  auth_token: 0123456789abcdef
forwarding:
  limits: {max_tcp_connections: 1, max_udp_mappings: 1}
  idle_timeout: {tcp: 1s, udp: 1s}
upstreams:
  - tag: local
    destination: {host: 127.0.0.1}
    measurement: {port: 9876}
listeners:
  - name: web
    bind: 127.0.0.1:443
    protocol: tcp
    route: web
    typo: true
routes:
  - name: web
    strategy: static
    upstreams: [local]
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "field typo not found") {
		t.Fatalf("expected strict unknown-field error, got %v", err)
	}
}
