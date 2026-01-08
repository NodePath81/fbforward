package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultProbeInterval      = 1 * time.Second
	defaultProbeWindowSize    = 5
	defaultEMAAlpha           = 0.357
	defaultMetricRefRTTMs     = 7
	defaultMetricRefJitterMs  = 1
	defaultMetricRefLoss      = 0.05
	defaultWeightRTT          = 0.2
	defaultWeightJitter       = 0.45
	defaultWeightLoss         = 0.35
	defaultMaxTCPConns        = 50
	defaultMaxUDPMappings     = 500
	defaultTCPIdleSeconds     = 60
	defaultUDPIdleSeconds     = 30
	defaultControlAddr        = "127.0.0.1"
	defaultControlPort        = 8080
	defaultConfirmWindows     = 3
	defaultFailureLoss        = 0.8
	defaultSwitchThreshold    = 1.0
	defaultMinHoldSeconds     = 5
	defaultShapingIFB         = "ifb0"
	defaultAggregateBandwidth = "1g"
	maxListeners              = 45
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar")
	}
	switch value.Tag {
	case "!!int", "!!float":
		var secs float64
		if err := value.Decode(&secs); err != nil {
			return err
		}
		*d = Duration(time.Duration(secs * float64(time.Second)))
		return nil
	default:
		var raw string
		if err := value.Decode(&raw); err != nil {
			return err
		}
		if raw == "" {
			*d = 0
			return nil
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return err
		}
		*d = Duration(parsed)
		return nil
	}
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

type Config struct {
	Hostname  string           `yaml:"hostname"`
	Listeners []ListenerConfig `yaml:"listeners"`
	Upstreams []UpstreamConfig `yaml:"upstreams"`
	Resolver  ResolverConfig   `yaml:"resolver"`
	Probe     ProbeConfig      `yaml:"probe"`
	Scoring   ScoringConfig    `yaml:"scoring"`
	Switching SwitchingConfig  `yaml:"switching"`
	Limits    LimitsConfig     `yaml:"limits"`
	Timeouts  TimeoutsConfig   `yaml:"timeouts"`
	Control   ControlConfig    `yaml:"control"`
	WebUI     WebUIConfig      `yaml:"webui"`
	Shaping   ShapingConfig    `yaml:"shaping"`
}

type ListenerConfig struct {
	Addr     string           `yaml:"addr"`
	Port     int              `yaml:"port"`
	Protocol string           `yaml:"protocol"`
	Ingress  *BandwidthConfig `yaml:"ingress"`
	Egress   *BandwidthConfig `yaml:"egress"`
}

type UpstreamConfig struct {
	Tag     string           `yaml:"tag"`
	Host    string           `yaml:"host"`
	Ingress *BandwidthConfig `yaml:"ingress"`
	Egress  *BandwidthConfig `yaml:"egress"`
}

type ResolverConfig struct {
	Servers []string `yaml:"servers"`
}

type ProbeConfig struct {
	Interval       Duration `yaml:"interval"`
	WindowSize     int      `yaml:"window_size"`
	DiscoveryDelay Duration `yaml:"discovery_delay"`
}

type ScoringConfig struct {
	EMAAlpha          float64       `yaml:"ema_alpha"`
	MetricRefRTTMs    float64       `yaml:"metric_ref_rtt_ms"`
	MetricRefJitterMs float64       `yaml:"metric_ref_jitter_ms"`
	MetricRefLoss     float64       `yaml:"metric_ref_loss"`
	Weights           WeightsConfig `yaml:"weights"`
}

type WeightsConfig struct {
	RTT    float64 `yaml:"rtt"`
	Jitter float64 `yaml:"jitter"`
	Loss   float64 `yaml:"loss"`
}

type SwitchingConfig struct {
	ConfirmWindows       int     `yaml:"confirm_windows"`
	FailureLossThreshold float64 `yaml:"failure_loss_threshold"`
	SwitchThreshold      float64 `yaml:"switch_threshold"`
	MinHoldSeconds       int     `yaml:"min_hold_seconds"`
}

type LimitsConfig struct {
	MaxTCPConns    int `yaml:"max_tcp_conns"`
	MaxUDPMappings int `yaml:"max_udp_mappings"`
}

type TimeoutsConfig struct {
	TCPIdleSeconds int `yaml:"tcp_idle_seconds"`
	UDPIdleSeconds int `yaml:"udp_idle_seconds"`
}

type ControlConfig struct {
	Addr  string `yaml:"addr"`
	Port  int    `yaml:"port"`
	Token string `yaml:"token"`
}

type WebUIConfig struct {
	Enabled *bool `yaml:"enabled"`
}

func (w WebUIConfig) IsEnabled() bool {
	if w.Enabled == nil {
		return true
	}
	return *w.Enabled
}

type ShapingConfig struct {
	Enabled            bool   `yaml:"enabled"`
	Device             string `yaml:"device"`
	IFB                string `yaml:"ifb"`
	AggregateBandwidth string `yaml:"aggregate_bandwidth"`

	aggregateBandwidthBits uint64
}

type BandwidthConfig struct {
	Rate   string `yaml:"rate"`
	Ceil   string `yaml:"ceil"`
	Burst  string `yaml:"burst"`
	Cburst string `yaml:"cburst"`

	rateBits    uint64
	ceilBits    uint64
	burstBytes  uint32
	cburstBytes uint32
}

func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) setDefaults() {
	if c.Probe.Interval == 0 {
		c.Probe.Interval = Duration(defaultProbeInterval)
	}
	if c.Probe.WindowSize == 0 {
		c.Probe.WindowSize = defaultProbeWindowSize
	}
	if c.Probe.DiscoveryDelay == 0 {
		c.Probe.DiscoveryDelay = Duration(time.Duration(c.Probe.WindowSize) * c.Probe.Interval.Duration())
	}

	if c.Scoring.EMAAlpha == 0 {
		c.Scoring.EMAAlpha = defaultEMAAlpha
	}
	if c.Scoring.MetricRefRTTMs == 0 {
		c.Scoring.MetricRefRTTMs = defaultMetricRefRTTMs
	}
	if c.Scoring.MetricRefJitterMs == 0 {
		c.Scoring.MetricRefJitterMs = defaultMetricRefJitterMs
	}
	if c.Scoring.MetricRefLoss == 0 {
		c.Scoring.MetricRefLoss = defaultMetricRefLoss
	}
	if c.Scoring.Weights.RTT == 0 && c.Scoring.Weights.Jitter == 0 && c.Scoring.Weights.Loss == 0 {
		c.Scoring.Weights.RTT = defaultWeightRTT
		c.Scoring.Weights.Jitter = defaultWeightJitter
		c.Scoring.Weights.Loss = defaultWeightLoss
	}

	if c.Switching.ConfirmWindows == 0 {
		c.Switching.ConfirmWindows = defaultConfirmWindows
	}
	if c.Switching.FailureLossThreshold == 0 {
		c.Switching.FailureLossThreshold = defaultFailureLoss
	}
	if c.Switching.SwitchThreshold == 0 {
		c.Switching.SwitchThreshold = defaultSwitchThreshold
	}
	if c.Switching.MinHoldSeconds == 0 {
		c.Switching.MinHoldSeconds = defaultMinHoldSeconds
	}

	if c.Limits.MaxTCPConns == 0 {
		c.Limits.MaxTCPConns = defaultMaxTCPConns
	}
	if c.Limits.MaxUDPMappings == 0 {
		c.Limits.MaxUDPMappings = defaultMaxUDPMappings
	}
	if c.Timeouts.TCPIdleSeconds == 0 {
		c.Timeouts.TCPIdleSeconds = defaultTCPIdleSeconds
	}
	if c.Timeouts.UDPIdleSeconds == 0 {
		c.Timeouts.UDPIdleSeconds = defaultUDPIdleSeconds
	}

	if c.Control.Addr == "" {
		c.Control.Addr = defaultControlAddr
	}
	if c.Control.Port == 0 {
		c.Control.Port = defaultControlPort
	}
	if c.WebUI.Enabled == nil {
		enabled := true
		c.WebUI.Enabled = &enabled
	}
	if c.Shaping.Enabled {
		if c.Shaping.IFB == "" {
			c.Shaping.IFB = defaultShapingIFB
		}
		if c.Shaping.AggregateBandwidth == "" {
			c.Shaping.AggregateBandwidth = defaultAggregateBandwidth
		}
	}
}

func (c *Config) validate() error {
	c.Hostname = strings.TrimSpace(c.Hostname)
	if len(c.Listeners) == 0 {
		return errors.New("at least one listener is required")
	}
	if len(c.Listeners) > maxListeners {
		return fmt.Errorf("listeners cannot exceed %d entries", maxListeners)
	}
	if len(c.Upstreams) == 0 {
		return errors.New("at least one upstream is required")
	}
	seenTags := make(map[string]struct{}, len(c.Upstreams))
	for i := range c.Upstreams {
		up := &c.Upstreams[i]
		up.Tag = strings.TrimSpace(up.Tag)
		up.Host = strings.TrimSpace(up.Host)
		if up.Tag == "" || up.Host == "" {
			return fmt.Errorf("upstream tag and host are required")
		}
		if _, exists := seenTags[up.Tag]; exists {
			return fmt.Errorf("duplicate upstream tag: %s", up.Tag)
		}
		seenTags[up.Tag] = struct{}{}

		// Validate upstream shaping configs
		hasShaping := up.Ingress != nil || up.Egress != nil
		if hasShaping && !c.Shaping.Enabled {
			return fmt.Errorf("upstream %s has shaping config but shaping.enabled is false", up.Tag)
		}
		if up.Ingress != nil {
			if strings.TrimSpace(up.Ingress.Rate) == "" {
				return fmt.Errorf("upstream %s ingress.rate is required", up.Tag)
			}
			if _, err := parseBandwidth(up.Ingress.Rate); err != nil {
				return fmt.Errorf("upstream %s ingress.rate: %w", up.Tag, err)
			}
			if up.Ingress.Ceil != "" {
				if _, err := parseBandwidth(up.Ingress.Ceil); err != nil {
					return fmt.Errorf("upstream %s ingress.ceil: %w", up.Tag, err)
				}
			}
			if up.Ingress.Burst != "" {
				if _, err := parseSize(up.Ingress.Burst); err != nil {
					return fmt.Errorf("upstream %s ingress.burst: %w", up.Tag, err)
				}
			}
			if up.Ingress.Cburst != "" {
				if _, err := parseSize(up.Ingress.Cburst); err != nil {
					return fmt.Errorf("upstream %s ingress.cburst: %w", up.Tag, err)
				}
			}
		}
		if up.Egress != nil {
			if strings.TrimSpace(up.Egress.Rate) == "" {
				return fmt.Errorf("upstream %s egress.rate is required", up.Tag)
			}
			if _, err := parseBandwidth(up.Egress.Rate); err != nil {
				return fmt.Errorf("upstream %s egress.rate: %w", up.Tag, err)
			}
			if up.Egress.Ceil != "" {
				if _, err := parseBandwidth(up.Egress.Ceil); err != nil {
					return fmt.Errorf("upstream %s egress.ceil: %w", up.Tag, err)
				}
			}
			if up.Egress.Burst != "" {
				if _, err := parseSize(up.Egress.Burst); err != nil {
					return fmt.Errorf("upstream %s egress.burst: %w", up.Tag, err)
				}
			}
			if up.Egress.Cburst != "" {
				if _, err := parseSize(up.Egress.Cburst); err != nil {
					return fmt.Errorf("upstream %s egress.cburst: %w", up.Tag, err)
				}
			}
		}
	}
	seenListeners := make(map[string]struct{}, len(c.Listeners))
	shapeKeys := make(map[string]struct{})
	for i := range c.Listeners {
		ln := &c.Listeners[i]
		ln.Protocol = strings.ToLower(strings.TrimSpace(ln.Protocol))
		ln.Addr = strings.TrimSpace(ln.Addr)
		if ln.Addr == "" || ln.Port <= 0 {
			return fmt.Errorf("listener addr and port are required")
		}
		if ln.Port > 65535 {
			return fmt.Errorf("listener port must be <= 65535")
		}
		if ln.Protocol != "tcp" && ln.Protocol != "udp" {
			return fmt.Errorf("listener protocol must be tcp or udp")
		}
		key := fmt.Sprintf("%s:%d:%s", ln.Addr, ln.Port, ln.Protocol)
		if _, exists := seenListeners[key]; exists {
			return fmt.Errorf("duplicate listener: %s", key)
		}
		seenListeners[key] = struct{}{}

		hasShaping := ln.Ingress != nil || ln.Egress != nil
		if hasShaping && !c.Shaping.Enabled {
			return fmt.Errorf("listener %s has shaping config but shaping.enabled is false", key)
		}
		if ln.Ingress != nil {
			if strings.TrimSpace(ln.Ingress.Rate) == "" {
				return fmt.Errorf("listener %s ingress.rate is required", key)
			}
			if _, err := parseBandwidth(ln.Ingress.Rate); err != nil {
				return fmt.Errorf("listener %s ingress.rate: %w", key, err)
			}
			if ln.Ingress.Ceil != "" {
				if _, err := parseBandwidth(ln.Ingress.Ceil); err != nil {
					return fmt.Errorf("listener %s ingress.ceil: %w", key, err)
				}
			}
			if ln.Ingress.Burst != "" {
				if _, err := parseSize(ln.Ingress.Burst); err != nil {
					return fmt.Errorf("listener %s ingress.burst: %w", key, err)
				}
			}
			if ln.Ingress.Cburst != "" {
				if _, err := parseSize(ln.Ingress.Cburst); err != nil {
					return fmt.Errorf("listener %s ingress.cburst: %w", key, err)
				}
			}
			shapeKey := fmt.Sprintf("ingress:%s:%d", ln.Protocol, ln.Port)
			if _, exists := shapeKeys[shapeKey]; exists {
				return fmt.Errorf("duplicate ingress shaping rule for %s:%d", ln.Protocol, ln.Port)
			}
			shapeKeys[shapeKey] = struct{}{}
		}
		if ln.Egress != nil {
			if strings.TrimSpace(ln.Egress.Rate) == "" {
				return fmt.Errorf("listener %s egress.rate is required", key)
			}
			if _, err := parseBandwidth(ln.Egress.Rate); err != nil {
				return fmt.Errorf("listener %s egress.rate: %w", key, err)
			}
			if ln.Egress.Ceil != "" {
				if _, err := parseBandwidth(ln.Egress.Ceil); err != nil {
					return fmt.Errorf("listener %s egress.ceil: %w", key, err)
				}
			}
			if ln.Egress.Burst != "" {
				if _, err := parseSize(ln.Egress.Burst); err != nil {
					return fmt.Errorf("listener %s egress.burst: %w", key, err)
				}
			}
			if ln.Egress.Cburst != "" {
				if _, err := parseSize(ln.Egress.Cburst); err != nil {
					return fmt.Errorf("listener %s egress.cburst: %w", key, err)
				}
			}
			shapeKey := fmt.Sprintf("egress:%s:%d", ln.Protocol, ln.Port)
			if _, exists := shapeKeys[shapeKey]; exists {
				return fmt.Errorf("duplicate egress shaping rule for %s:%d", ln.Protocol, ln.Port)
			}
			shapeKeys[shapeKey] = struct{}{}
		}
	}
	if c.Control.Token == "" {
		return errors.New("control.token is required")
	}
	if c.Control.Port <= 0 || c.Control.Port > 65535 {
		return errors.New("control.port must be in 1..65535")
	}
	if c.Probe.Interval.Duration() <= 0 {
		return errors.New("probe.interval must be > 0")
	}
	if c.Probe.WindowSize <= 0 {
		return errors.New("probe.window_size must be > 0")
	}
	if c.Probe.DiscoveryDelay.Duration() < 0 {
		return errors.New("probe.discovery_delay must be >= 0")
	}
	if c.Scoring.EMAAlpha <= 0 || c.Scoring.EMAAlpha > 1 {
		return errors.New("scoring.ema_alpha must be in (0,1]")
	}
	if c.Scoring.MetricRefRTTMs <= 0 || c.Scoring.MetricRefJitterMs <= 0 || c.Scoring.MetricRefLoss <= 0 {
		return errors.New("scoring metric refs must be > 0")
	}
	weightSum := c.Scoring.Weights.RTT + c.Scoring.Weights.Jitter + c.Scoring.Weights.Loss
	if weightSum <= 0 {
		return errors.New("scoring weights must sum to > 0")
	}
	if diff := weightSum - 1; diff > 0.001 || diff < -0.001 {
		c.Scoring.Weights.RTT /= weightSum
		c.Scoring.Weights.Jitter /= weightSum
		c.Scoring.Weights.Loss /= weightSum
	}
	if c.Limits.MaxTCPConns <= 0 || c.Limits.MaxUDPMappings <= 0 {
		return errors.New("limits must be > 0")
	}
	if c.Timeouts.TCPIdleSeconds <= 0 || c.Timeouts.UDPIdleSeconds <= 0 {
		return errors.New("timeouts must be > 0")
	}
	if c.Switching.ConfirmWindows <= 0 {
		return errors.New("switching.confirm_windows must be > 0")
	}
	if c.Switching.FailureLossThreshold <= 0 || c.Switching.FailureLossThreshold > 1 {
		return errors.New("switching.failure_loss_threshold must be in (0,1]")
	}
	if c.Switching.SwitchThreshold < 0 {
		return errors.New("switching.switch_threshold must be >= 0")
	}
	if c.Switching.MinHoldSeconds < 0 {
		return errors.New("switching.min_hold_seconds must be >= 0")
	}
	if c.Shaping.Enabled {
		c.Shaping.Device = strings.TrimSpace(c.Shaping.Device)
		c.Shaping.IFB = strings.TrimSpace(c.Shaping.IFB)
		c.Shaping.AggregateBandwidth = strings.TrimSpace(c.Shaping.AggregateBandwidth)
		if c.Shaping.Device == "" {
			return errors.New("shaping.device is required when shaping.enabled is true")
		}
		if c.Shaping.IFB == "" {
			return errors.New("shaping.ifb is required when shaping.enabled is true")
		}
		if c.Shaping.AggregateBandwidth != "" {
			if _, err := parseBandwidth(c.Shaping.AggregateBandwidth); err != nil {
				return fmt.Errorf("shaping.aggregate_bandwidth: %w", err)
			}
		}
	}
	return nil
}
