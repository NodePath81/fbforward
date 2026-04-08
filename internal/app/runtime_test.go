package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
)

func TestNewRuntimeWithIPLogAndFirewallCleansUp(t *testing.T) {
	cfg := config.Config{
		Hostname: "test-node",
		Forwarding: config.ForwardingConfig{
			Listeners: []config.ListenerConfig{{
				BindAddr: "127.0.0.1",
				BindPort: 0,
				Protocol: "tcp",
			}},
			Limits: config.ForwardingLimitsConfig{
				MaxTCPConnections: 10,
				MaxUDPMappings:    10,
			},
			IdleTimeout: config.IdleTimeoutConfig{
				TCP: config.Duration(time.Second),
				UDP: config.Duration(time.Second),
			},
		},
		Upstreams: []config.UpstreamConfig{{
			Tag: "primary",
			Destination: config.DestinationConfig{
				Host: "127.0.0.1",
			},
			Measurement: config.UpstreamMeasurementConfig{
				Host: "127.0.0.1",
				Port: 9876,
			},
		}},
		Reachability: config.ReachabilityConfig{
			ProbeInterval: config.Duration(time.Second),
			WindowSize:    5,
		},
		Control: config.ControlConfig{
			BindAddr:  "127.0.0.1",
			BindPort:  0,
			AuthToken: "0123456789abcdef",
		},
		GeoIP: config.GeoIPConfig{
			Enabled:         true,
			ASNDBURL:        "https://example.test/GeoLite2-ASN.mmdb",
			ASNDBPath:       filepath.Join(t.TempDir(), "GeoLite2-ASN.mmdb"),
			RefreshInterval: config.Duration(24 * time.Hour),
		},
		IPLog: config.IPLogConfig{
			Enabled:        true,
			DBPath:         filepath.Join(t.TempDir(), "iplog.sqlite"),
			GeoQueueSize:   8,
			WriteQueueSize: 8,
			BatchSize:      4,
			FlushInterval:  config.Duration(time.Second),
			PruneInterval:  config.Duration(time.Hour),
		},
		Firewall: config.FirewallConfig{
			Enabled: true,
			Default: "allow",
			Rules: []config.FirewallRule{{
				Action: "deny",
				CIDR:   "10.0.0.0/8",
			}},
		},
	}
	rt, err := NewRuntime(cfg, nil, func() error { return nil })
	if err != nil {
		t.Fatalf("NewRuntime error: %v", err)
	}
	if rt.geoipMgr == nil || rt.iplogStore == nil || rt.iplogPipeline == nil || rt.firewall == nil {
		t.Fatalf("expected runtime to wire geoip/iplog/firewall components")
	}

	rt.Stop()
}
