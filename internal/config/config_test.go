package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCoordinationConfigOptional(t *testing.T) {
	cfg := testConfig()
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected config without coordination to be valid: %v", err)
	}
}

func TestCoordinationConfigRequiresFieldsTogether(t *testing.T) {
	cfg := testConfig()
	cfg.Coordination.Endpoint = "https://fbcoord.example.workers.dev"
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatalf("expected partial coordination config to be rejected")
	}
	if !strings.Contains(err.Error(), "coordination.endpoint and token must be set together") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestCoordinationConfigAcceptsCompleteBlock(t *testing.T) {
	cfg := testConfig()
	cfg.Coordination = CoordinationConfig{
		Endpoint: "https://fbcoord.example.workers.dev",
		Token:    "0123456789abcdef",
	}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected complete coordination config to validate: %v", err)
	}
	if got := cfg.Coordination.HeartbeatInterval.Duration(); got != defaultCoordinationHeartbeat {
		t.Fatalf("expected default coordination heartbeat %s, got %s", defaultCoordinationHeartbeat, got)
	}
}

func TestCoordinationConfigIgnoresLegacyPoolAndNodeIDWithWarnings(t *testing.T) {
	cfg := testConfig()
	cfg.Coordination = CoordinationConfig{
		Pool:   "default",
		NodeID: "fbforward-01",
	}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected legacy-only coordination fields to be ignored, got %v", err)
	}
	if len(cfg.Warnings) != 2 {
		t.Fatalf("expected warnings for ignored pool/node_id, got %#v", cfg.Warnings)
	}
}

func TestNotifyConfigRequiresTokenWhenEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.Notify = NotifyConfig{
		Enabled:  true,
		Endpoint: "https://notify.example/v1/events",
		KeyID:    "key-1",
	}
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "notify.token is required when notify.enabled is true") {
		t.Fatalf("expected missing notify token error, got %v", err)
	}
}

func TestNotifyConfigAcceptsTokenAndDefaultsSourceInstance(t *testing.T) {
	cfg := testConfig()
	cfg.Notify = NotifyConfig{
		Enabled:  true,
		Endpoint: "https://notify.example/v1/events",
		KeyID:    "key-1",
		Token:    "node-token-abcdefghijklmnopqrstuvwxyz123456",
	}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected notify config to validate: %v", err)
	}
	if cfg.Notify.Token != "node-token-abcdefghijklmnopqrstuvwxyz123456" {
		t.Fatalf("expected notify token to be preserved, got %q", cfg.Notify.Token)
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

func TestNotifyConfigRejectsInvalidDurationsWhenEnabled(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{
			name: "startup grace",
			mut: func(cfg *Config) {
				cfg.Notify.StartupGracePeriod = Duration(-time.Second)
			},
			want: "notify.startup_grace_period must be > 0",
		},
		{
			name: "unusable interval",
			mut: func(cfg *Config) {
				cfg.Notify.UnusableInterval = Duration(0)
			},
			want: "notify.unusable_interval must be > 0",
		},
		{
			name: "notify interval",
			mut: func(cfg *Config) {
				cfg.Notify.NotifyInterval = Duration(-time.Second)
			},
			want: "notify.notify_interval must be > 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Notify = NotifyConfig{
				Enabled:  true,
				Endpoint: "https://notify.example/v1/events",
				KeyID:    "key-1",
				Token:    "node-token-abcdefghijklmnopqrstuvwxyz123456",
			}
			cfg.setDefaults()
			tc.mut(&cfg)
			err := cfg.validate()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in error, got %v", tc.want, err)
			}
		})
	}
}

func TestNotifyConfigRejectsInvalidToken(t *testing.T) {
	cfg := testConfig()
	cfg.Notify = NotifyConfig{
		Enabled:  true,
		Endpoint: "https://notify.example/v1/events",
		KeyID:    "key-1",
		Token:    "short-token",
	}
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatalf("expected invalid token validation error")
	}
	if !strings.Contains(err.Error(), "notify.token") {
		t.Fatalf("expected notify.token validation error, got %v", err)
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
notify:
  enabled: true
  endpoint: https://notify.example/v1/events
  key_id: key-1
  token_env: FBNOTIFY_TOKEN
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

func TestGeoIPConfigRequiresOneCompletePairWhenEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.GeoIP.Enabled = true
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatalf("expected geoip validation error")
	}
	if !strings.Contains(err.Error(), "geoip.enabled requires at least one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGeoIPConfigRejectsIncompletePair(t *testing.T) {
	cfg := testConfig()
	cfg.GeoIP.Enabled = true
	cfg.GeoIP.ASNDBURL = "https://example.com/GeoLite2-ASN.mmdb"
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatalf("expected incomplete geoip pair to fail")
	}
	if !strings.Contains(err.Error(), "geoip.asn_db requires both url and path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGeoIPConfigAcceptsSingleCompletePair(t *testing.T) {
	cfg := testConfig()
	cfg.GeoIP.Enabled = true
	cfg.GeoIP.ASNDBURL = "https://example.com/GeoLite2-ASN.mmdb"
	cfg.GeoIP.ASNDBPath = "/tmp/GeoLite2-ASN.mmdb"
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected geoip config to validate: %v", err)
	}
	if got := cfg.GeoIP.RefreshInterval.Duration(); got != defaultGeoIPRefreshInterval {
		t.Fatalf("expected default geoip refresh interval %s, got %s", defaultGeoIPRefreshInterval, got)
	}
}

func TestIPLogConfigRequiresDBPathWhenEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.IPLog.Enabled = true
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatalf("expected missing ip_log.db_path to fail")
	}
	if !strings.Contains(err.Error(), "ip_log.db_path is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIPLogConfigDefaultsQueueSizes(t *testing.T) {
	cfg := testConfig()
	cfg.IPLog.Enabled = true
	cfg.IPLog.DBPath = "/tmp/iplog.sqlite"
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected ip_log config to validate: %v", err)
	}
	if cfg.IPLog.GeoQueueSize != defaultIPLogGeoQueueSize {
		t.Fatalf("expected default geo queue size %d, got %d", defaultIPLogGeoQueueSize, cfg.IPLog.GeoQueueSize)
	}
	if cfg.IPLog.WriteQueueSize != defaultIPLogWriteQueueSize {
		t.Fatalf("expected default write queue size %d, got %d", defaultIPLogWriteQueueSize, cfg.IPLog.WriteQueueSize)
	}
	if cfg.IPLog.BatchSize != defaultIPLogBatchSize {
		t.Fatalf("expected default batch size %d, got %d", defaultIPLogBatchSize, cfg.IPLog.BatchSize)
	}
	if got := cfg.IPLog.FlushInterval.Duration(); got != defaultIPLogFlushInterval {
		t.Fatalf("expected default flush interval %s, got %s", defaultIPLogFlushInterval, got)
	}
	if got := cfg.IPLog.PruneInterval.Duration(); got != defaultIPLogPruneInterval {
		t.Fatalf("expected default prune interval %s, got %s", defaultIPLogPruneInterval, got)
	}
	if cfg.IPLog.LogRejections == nil || !*cfg.IPLog.LogRejections {
		t.Fatalf("expected log_rejections default to true when ip_log is enabled, got %#v", cfg.IPLog.LogRejections)
	}
}

func TestIPLogConfigPreservesExplicitLogRejectionsFalse(t *testing.T) {
	cfg := testConfig()
	cfg.IPLog.Enabled = true
	cfg.IPLog.DBPath = "/tmp/iplog.sqlite"
	disabled := false
	cfg.IPLog.LogRejections = &disabled
	cfg.setDefaults()
	if cfg.IPLog.LogRejections == nil || *cfg.IPLog.LogRejections {
		t.Fatalf("expected explicit log_rejections=false to be preserved, got %#v", cfg.IPLog.LogRejections)
	}
}

func TestIPLogConfigRejectsInvalidTuning(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{
			name: "batch size",
			mut: func(cfg *Config) {
				cfg.IPLog.BatchSize = -1
			},
			want: "ip_log.batch_size must be > 0",
		},
		{
			name: "flush interval",
			mut: func(cfg *Config) {
				cfg.IPLog.FlushInterval = Duration(-time.Second)
			},
			want: "ip_log.flush_interval must be > 0",
		},
		{
			name: "prune interval",
			mut: func(cfg *Config) {
				cfg.IPLog.Retention = Duration(24 * time.Hour)
				cfg.IPLog.PruneInterval = Duration(0)
			},
			want: "ip_log.prune_interval must be > 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.IPLog.Enabled = true
			cfg.IPLog.DBPath = "/tmp/iplog.sqlite"
			cfg.setDefaults()
			tc.mut(&cfg)
			err := cfg.validate()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in error, got %v", tc.want, err)
			}
		})
	}
}

func TestGeoIPConfigRejectsPathWithoutURL(t *testing.T) {
	cfg := testConfig()
	cfg.GeoIP.Enabled = true
	cfg.GeoIP.CountryDBPath = "/tmp/Country-without-asn.mmdb"
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatalf("expected incomplete geoip pair to fail")
	}
	if !strings.Contains(err.Error(), "geoip.country_db requires both url and path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFirewallRuleRequiresExactlyOneMatcher(t *testing.T) {
	cfg := testConfig()
	cfg.Firewall.Enabled = true
	cfg.Firewall.Rules = []FirewallRule{{
		Action:  "deny",
		CIDR:    "10.0.0.0/8",
		Country: "us",
	}}
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatalf("expected firewall rule validation error")
	}
	if !strings.Contains(err.Error(), "must specify exactly one matcher") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFirewallCountryNormalizedToUppercase(t *testing.T) {
	cfg := testConfig()
	cfg.Firewall.Enabled = true
	cfg.Firewall.Rules = []FirewallRule{{
		Action:  "allow",
		Country: "us",
	}}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected firewall config to validate: %v", err)
	}
	if got := cfg.Firewall.Rules[0].Country; got != "US" {
		t.Fatalf("expected country to normalize to US, got %q", got)
	}
}

func TestFirewallRejectsInvalidDefaultAndAction(t *testing.T) {
	cfg := testConfig()
	cfg.Firewall.Enabled = true
	cfg.Firewall.Default = "maybe"
	cfg.Firewall.Rules = []FirewallRule{{Action: "block", CIDR: "10.0.0.0/8"}}
	cfg.setDefaults()

	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "firewall.default must be allow or deny") {
		t.Fatalf("expected invalid firewall default error, got %v", err)
	}

	cfg = testConfig()
	cfg.Firewall.Enabled = true
	cfg.Firewall.Rules = []FirewallRule{{Action: "block", CIDR: "10.0.0.0/8"}}
	cfg.setDefaults()
	err = cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "firewall.rules[0].action must be allow or deny") {
		t.Fatalf("expected invalid firewall action error, got %v", err)
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
		Reachability: ReachabilityConfig{
			ProbeInterval: Duration(time.Second),
			WindowSize:    5,
		},
		Control: ControlConfig{
			AuthToken: "0123456789abcdef",
		},
	}
}
