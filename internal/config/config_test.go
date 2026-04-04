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
