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
