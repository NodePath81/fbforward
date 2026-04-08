package config

import (
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
	if !strings.Contains(err.Error(), "coordination.endpoint, pool, node_id, and token must be set together") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestCoordinationConfigAcceptsCompleteBlock(t *testing.T) {
	cfg := testConfig()
	cfg.Coordination = CoordinationConfig{
		Endpoint: "https://fbcoord.example.workers.dev",
		Pool:     "default",
		NodeID:   "fbforward-01",
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
