package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	defaultProbeInterval   = 1 * time.Second
	defaultProbeWindowSize = 5

	defaultMeasurementInterval       = 2 * time.Second
	defaultMeasurementDiscoveryDelay = 10 * time.Second
	defaultMeasurementTargetUp       = "10m"
	defaultMeasurementTargetDown     = "50m"
	defaultMeasurementSampleBytes    = "500KB"
	defaultMeasurementSamples        = 1
	defaultMeasurementTCPEnabled     = true
	defaultMeasurementUDPEnabled     = true
	defaultMeasurementAlternateTCP   = true
	defaultMeasurementMaxSample      = 10 * time.Second
	defaultMeasurementMaxCycle       = 30 * time.Second
	defaultMeasurementFastStart      = 500 * time.Millisecond
	defaultMeasurementWarmup         = 15 * time.Second
	defaultMeasurementStale          = 120 * time.Second
	defaultMeasurementFallbackICMP   = true
	defaultScheduleMinInterval       = 15 * time.Minute
	defaultScheduleMaxInterval       = 45 * time.Minute
	defaultScheduleInterGap          = 5 * time.Second
	defaultScheduleMaxUtil           = 0.7
	defaultScheduleHeadroom          = "0"

	defaultEMAAlpha             = 0.2
	defaultRefBandwidthUp       = "10m"
	defaultRefBandwidthDn       = "50m"
	defaultRefRTTMs             = 50
	defaultRefJitterMs          = 10
	defaultRefRetransRate       = 0.01
	defaultRefLossRate          = 0.01
	defaultWeightTCPBwUp        = 0.15
	defaultWeightTCPBwDn        = 0.25
	defaultWeightTCPRTT         = 0.25
	defaultWeightTCPJitter      = 0.10
	defaultWeightTCPRetrans     = 0.25
	defaultWeightUDPBwUp        = 0.10
	defaultWeightUDPBwDn        = 0.30
	defaultWeightUDPRTT         = 0.15
	defaultWeightUDPJitter      = 0.30
	defaultWeightUDPLoss        = 0.15
	defaultProtocolWeightTCP    = 0.5
	defaultProtocolWeightUDP    = 0.5
	defaultUtilizationEnabled   = true
	defaultUtilizationMinMult   = 0.3
	defaultUtilizationThreshold = 0.7
	defaultUtilizationExponent  = 2.0
	defaultBiasKappa            = 0.693147
	defaultUtilizationWindowSec = 5
	defaultUtilizationUpdateSec = 1

	defaultMaxTCPConns        = 50
	defaultMaxUDPMappings     = 500
	defaultTCPIdleSeconds     = 60
	defaultUDPIdleSeconds     = 30
	defaultControlAddr        = "127.0.0.1"
	defaultControlPort        = 8080
	defaultConfirmDuration    = 15 * time.Second
	defaultFailureLoss        = 0.2
	defaultFailureRetrans     = 0.2
	defaultSwitchThreshold    = 5.0
	defaultMinHoldSeconds     = 30
	defaultMeasurePort        = 9876
	DefaultShapingIFB         = "ifb0"
	DefaultAggregateBandwidth = "1g"
	maxListeners              = 45
	ResolverStrategyIPv4Only  = "ipv4_only"
	ResolverStrategyPreferV6  = "prefer_ipv6"
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
	Hostname    string            `yaml:"hostname"`
	Listeners   []ListenerConfig  `yaml:"listeners"`
	Upstreams   []UpstreamConfig  `yaml:"upstreams"`
	Resolver    ResolverConfig    `yaml:"resolver"`
	Probe       ProbeConfig       `yaml:"probe"`
	Measurement MeasurementConfig `yaml:"measurement"`
	Scoring     ScoringConfig     `yaml:"scoring"`
	Switching   SwitchingConfig   `yaml:"switching"`
	Limits      LimitsConfig      `yaml:"limits"`
	Timeouts    TimeoutsConfig    `yaml:"timeouts"`
	Control     ControlConfig     `yaml:"control"`
	WebUI       WebUIConfig       `yaml:"webui"`
	Shaping     ShapingConfig     `yaml:"shaping"`
}

type ListenerConfig struct {
	Addr     string           `yaml:"addr"`
	Port     int              `yaml:"port"`
	Protocol string           `yaml:"protocol"`
	Ingress  *BandwidthConfig `yaml:"ingress"`
	Egress   *BandwidthConfig `yaml:"egress"`
}

type UpstreamConfig struct {
	Tag         string           `yaml:"tag"`
	Host        string           `yaml:"host"`
	MeasureHost string           `yaml:"measure_host"`
	MeasurePort int              `yaml:"measure_port"`
	Priority    float64          `yaml:"priority"`
	Bias        float64          `yaml:"bias"`
	Ingress     *BandwidthConfig `yaml:"ingress"`
	Egress      *BandwidthConfig `yaml:"egress"`
}

type ResolverConfig struct {
	Servers  []string `yaml:"servers"`
	Strategy string   `yaml:"strategy"`
}

type ProbeConfig struct {
	Interval       Duration `yaml:"interval"`
	WindowSize     int      `yaml:"window_size"`
	DiscoveryDelay Duration `yaml:"discovery_delay"`
}

type MeasurementScheduleConfig struct {
	MinInterval      Duration `yaml:"min_interval"`
	MaxInterval      Duration `yaml:"max_interval"`
	InterUpstreamGap Duration `yaml:"inter_upstream_gap"`
	MaxUtilization   float64  `yaml:"max_utilization"`
	RequiredHeadroom string   `yaml:"required_headroom"`
}

type MeasurementConfig struct {
	Interval       Duration                  `yaml:"interval"`
	DiscoveryDelay Duration                  `yaml:"discovery_delay"`
	Schedule       MeasurementScheduleConfig `yaml:"schedule"`

	TargetBandwidthUp      string `yaml:"target_bandwidth_up"`
	TargetBandwidthDown    string `yaml:"target_bandwidth_down"`
	TCPTargetBandwidthUp   string `yaml:"tcp_target_bandwidth_up"`
	TCPTargetBandwidthDown string `yaml:"tcp_target_bandwidth_down"`
	UDPTargetBandwidthUp   string `yaml:"udp_target_bandwidth_up"`
	UDPTargetBandwidthDown string `yaml:"udp_target_bandwidth_down"`
	SampleBytes            string `yaml:"sample_bytes"`
	Samples                int    `yaml:"samples"`

	TCPEnabled   *bool `yaml:"tcp_enabled"`
	UDPEnabled   *bool `yaml:"udp_enabled"`
	AlternateTCP *bool `yaml:"alternate_tcp"`

	MaxSampleDuration Duration `yaml:"max_sample_duration"`
	MaxCycleDuration  Duration `yaml:"max_cycle_duration"`

	FastStartTimeout Duration `yaml:"fast_start_timeout"`
	WarmupDuration   Duration `yaml:"warmup_duration"`

	StaleThreshold Duration `yaml:"stale_threshold"`

	FallbackToICMP *bool `yaml:"fallback_to_icmp"`
}

type ScoringConfig struct {
	EMAAlpha float64 `yaml:"ema_alpha"`

	RefBandwidthUp   string  `yaml:"ref_bandwidth_up"`
	RefBandwidthDown string  `yaml:"ref_bandwidth_down"`
	RefRTTMs         float64 `yaml:"ref_rtt_ms"`
	RefJitterMs      float64 `yaml:"ref_jitter_ms"`
	RefRetransRate   float64 `yaml:"ref_retrans_rate"`
	RefLossRate      float64 `yaml:"ref_loss_rate"`

	WeightsTCP WeightsTCPConfig `yaml:"weights_tcp"`
	WeightsUDP WeightsUDPConfig `yaml:"weights_udp"`

	ProtocolWeightTCP float64 `yaml:"protocol_weight_tcp"`
	ProtocolWeightUDP float64 `yaml:"protocol_weight_udp"`

	UtilizationEnabled   *bool   `yaml:"utilization_enabled"`
	UtilizationMinMult   float64 `yaml:"utilization_min_mult"`
	UtilizationThresh    float64 `yaml:"utilization_threshold"`
	UtilizationExponent  float64 `yaml:"utilization_exponent"`
	UtilizationWindowSec int     `yaml:"utilization_window_sec"`
	UtilizationUpdateSec int     `yaml:"utilization_update_sec"`

	BiasKappa float64 `yaml:"bias_kappa"`

	MetricRefRTTMs    float64       `yaml:"metric_ref_rtt_ms,omitempty"`
	MetricRefJitterMs float64       `yaml:"metric_ref_jitter_ms,omitempty"`
	MetricRefLoss     float64       `yaml:"metric_ref_loss,omitempty"`
	Weights           WeightsLegacy `yaml:"weights,omitempty"`
}

type WeightsTCPConfig struct {
	BandwidthUp   float64 `yaml:"bandwidth_up"`
	BandwidthDown float64 `yaml:"bandwidth_down"`
	RTT           float64 `yaml:"rtt"`
	Jitter        float64 `yaml:"jitter"`
	Retrans       float64 `yaml:"retrans"`
}

type WeightsUDPConfig struct {
	BandwidthUp   float64 `yaml:"bandwidth_up"`
	BandwidthDown float64 `yaml:"bandwidth_down"`
	RTT           float64 `yaml:"rtt"`
	Jitter        float64 `yaml:"jitter"`
	Loss          float64 `yaml:"loss"`
}

type WeightsLegacy struct {
	RTT    float64 `yaml:"rtt"`
	Jitter float64 `yaml:"jitter"`
	Loss   float64 `yaml:"loss"`
}

type SwitchingConfig struct {
	ConfirmDuration      Duration `yaml:"confirm_duration"`
	SwitchThreshold      float64  `yaml:"switch_threshold"`
	MinHoldSeconds       int      `yaml:"min_hold_seconds"`
	FailureLossThreshold float64  `yaml:"failure_loss_threshold"`
	FailureRetransThresh float64  `yaml:"failure_retrans_threshold"`
	CloseFlowsOnUnusable bool     `yaml:"close_flows_on_unusable"`

	ConfirmWindows int `yaml:"confirm_windows,omitempty"`
}

func DefaultSwitchingConfig() SwitchingConfig {
	return SwitchingConfig{
		ConfirmDuration:      Duration(defaultConfirmDuration),
		SwitchThreshold:      defaultSwitchThreshold,
		MinHoldSeconds:       defaultMinHoldSeconds,
		FailureLossThreshold: defaultFailureLoss,
		FailureRetransThresh: defaultFailureRetrans,
		CloseFlowsOnUnusable: false,
	}
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

	AggregateBandwidthBits uint64 `yaml:"-"`
}

type BandwidthConfig struct {
	Rate   string `yaml:"rate"`
	Ceil   string `yaml:"ceil"`
	Burst  string `yaml:"burst"`
	Cburst string `yaml:"cburst"`

	RateBits    uint64 `yaml:"-"`
	CeilBits    uint64 `yaml:"-"`
	BurstBytes  uint32 `yaml:"-"`
	CburstBytes uint32 `yaml:"-"`
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
	cfg.applyMigration()
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

	if c.Measurement.Interval == 0 {
		c.Measurement.Interval = Duration(defaultMeasurementInterval)
	}
	if c.Measurement.DiscoveryDelay == 0 {
		c.Measurement.DiscoveryDelay = Duration(defaultMeasurementDiscoveryDelay)
	}
	if c.Measurement.TargetBandwidthUp == "" {
		c.Measurement.TargetBandwidthUp = defaultMeasurementTargetUp
	}
	if c.Measurement.TargetBandwidthDown == "" {
		c.Measurement.TargetBandwidthDown = defaultMeasurementTargetDown
	}
	if c.Measurement.TCPTargetBandwidthUp == "" {
		c.Measurement.TCPTargetBandwidthUp = defaultMeasurementTargetUp
	}
	if c.Measurement.TCPTargetBandwidthDown == "" {
		c.Measurement.TCPTargetBandwidthDown = defaultMeasurementTargetDown
	}
	if c.Measurement.UDPTargetBandwidthUp == "" {
		c.Measurement.UDPTargetBandwidthUp = defaultMeasurementTargetUp
	}
	if c.Measurement.UDPTargetBandwidthDown == "" {
		c.Measurement.UDPTargetBandwidthDown = defaultMeasurementTargetDown
	}
	if c.Measurement.SampleBytes == "" {
		c.Measurement.SampleBytes = defaultMeasurementSampleBytes
	}
	if c.Measurement.Samples == 0 {
		c.Measurement.Samples = defaultMeasurementSamples
	}
	if c.Measurement.TCPEnabled == nil {
		val := defaultMeasurementTCPEnabled
		c.Measurement.TCPEnabled = &val
	}
	if c.Measurement.UDPEnabled == nil {
		val := defaultMeasurementUDPEnabled
		c.Measurement.UDPEnabled = &val
	}
	if c.Measurement.AlternateTCP == nil {
		val := defaultMeasurementAlternateTCP
		c.Measurement.AlternateTCP = &val
	}
	if c.Measurement.MaxSampleDuration == 0 {
		c.Measurement.MaxSampleDuration = Duration(defaultMeasurementMaxSample)
	}
	if c.Measurement.MaxCycleDuration == 0 {
		c.Measurement.MaxCycleDuration = Duration(defaultMeasurementMaxCycle)
	}
	if c.Measurement.FastStartTimeout == 0 {
		c.Measurement.FastStartTimeout = Duration(defaultMeasurementFastStart)
	}
	if c.Measurement.WarmupDuration == 0 {
		c.Measurement.WarmupDuration = Duration(defaultMeasurementWarmup)
	}
	if c.Measurement.StaleThreshold == 0 {
		c.Measurement.StaleThreshold = Duration(defaultMeasurementStale)
	}
	if c.Measurement.FallbackToICMP == nil {
		val := defaultMeasurementFallbackICMP
		c.Measurement.FallbackToICMP = &val
	}
	if c.Measurement.Schedule.MinInterval == 0 {
		c.Measurement.Schedule.MinInterval = Duration(defaultScheduleMinInterval)
	}
	if c.Measurement.Schedule.MaxInterval == 0 {
		c.Measurement.Schedule.MaxInterval = Duration(defaultScheduleMaxInterval)
	}
	if c.Measurement.Schedule.InterUpstreamGap == 0 {
		c.Measurement.Schedule.InterUpstreamGap = Duration(defaultScheduleInterGap)
	}
	if c.Measurement.Schedule.MaxUtilization == 0 {
		c.Measurement.Schedule.MaxUtilization = defaultScheduleMaxUtil
	}
	if c.Measurement.Schedule.RequiredHeadroom == "" {
		c.Measurement.Schedule.RequiredHeadroom = defaultScheduleHeadroom
	}

	if c.Scoring.EMAAlpha == 0 {
		c.Scoring.EMAAlpha = defaultEMAAlpha
	}
	if c.Scoring.RefBandwidthUp == "" {
		c.Scoring.RefBandwidthUp = defaultRefBandwidthUp
	}
	if c.Scoring.RefBandwidthDown == "" {
		c.Scoring.RefBandwidthDown = defaultRefBandwidthDn
	}
	if c.Scoring.RefRTTMs == 0 {
		c.Scoring.RefRTTMs = defaultRefRTTMs
	}
	if c.Scoring.RefJitterMs == 0 {
		c.Scoring.RefJitterMs = defaultRefJitterMs
	}
	if c.Scoring.RefRetransRate == 0 {
		c.Scoring.RefRetransRate = defaultRefRetransRate
	}
	if c.Scoring.RefLossRate == 0 {
		c.Scoring.RefLossRate = defaultRefLossRate
	}
	if weightsTCPZero(c.Scoring.WeightsTCP) {
		c.Scoring.WeightsTCP = WeightsTCPConfig{
			BandwidthUp:   defaultWeightTCPBwUp,
			BandwidthDown: defaultWeightTCPBwDn,
			RTT:           defaultWeightTCPRTT,
			Jitter:        defaultWeightTCPJitter,
			Retrans:       defaultWeightTCPRetrans,
		}
	}
	if weightsUDPZero(c.Scoring.WeightsUDP) {
		c.Scoring.WeightsUDP = WeightsUDPConfig{
			BandwidthUp:   defaultWeightUDPBwUp,
			BandwidthDown: defaultWeightUDPBwDn,
			RTT:           defaultWeightUDPRTT,
			Jitter:        defaultWeightUDPJitter,
			Loss:          defaultWeightUDPLoss,
		}
	}
	if c.Scoring.ProtocolWeightTCP == 0 && c.Scoring.ProtocolWeightUDP == 0 {
		c.Scoring.ProtocolWeightTCP = defaultProtocolWeightTCP
		c.Scoring.ProtocolWeightUDP = defaultProtocolWeightUDP
	}
	if c.Scoring.UtilizationEnabled == nil {
		val := defaultUtilizationEnabled
		c.Scoring.UtilizationEnabled = &val
	}
	if c.Scoring.UtilizationMinMult == 0 {
		c.Scoring.UtilizationMinMult = defaultUtilizationMinMult
	}
	if c.Scoring.UtilizationThresh == 0 {
		c.Scoring.UtilizationThresh = defaultUtilizationThreshold
	}
	if c.Scoring.UtilizationExponent == 0 {
		c.Scoring.UtilizationExponent = defaultUtilizationExponent
	}
	if c.Scoring.UtilizationWindowSec == 0 {
		c.Scoring.UtilizationWindowSec = defaultUtilizationWindowSec
	}
	if c.Scoring.UtilizationUpdateSec == 0 {
		c.Scoring.UtilizationUpdateSec = defaultUtilizationUpdateSec
	}
	if c.Scoring.BiasKappa == 0 {
		c.Scoring.BiasKappa = defaultBiasKappa
	}

	if c.Switching.ConfirmDuration == 0 {
		c.Switching.ConfirmDuration = Duration(defaultConfirmDuration)
	}
	if c.Switching.FailureLossThreshold == 0 {
		c.Switching.FailureLossThreshold = defaultFailureLoss
	}
	if c.Switching.FailureRetransThresh == 0 {
		c.Switching.FailureRetransThresh = defaultFailureRetrans
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
	for i := range c.Upstreams {
		up := &c.Upstreams[i]
		if up.MeasureHost == "" {
			up.MeasureHost = up.Host
		}
		if up.MeasurePort == 0 {
			up.MeasurePort = defaultMeasurePort
		}
	}
	if c.Shaping.Enabled {
		if c.Shaping.IFB == "" {
			c.Shaping.IFB = DefaultShapingIFB
		}
		if c.Shaping.AggregateBandwidth == "" {
			c.Shaping.AggregateBandwidth = DefaultAggregateBandwidth
		}
	}
}

func (c *Config) applyMigration() {
	if c.Measurement.Interval.Duration() == 0 && c.Probe.Interval.Duration() != 0 {
		c.Measurement.Interval = c.Probe.Interval
		fmt.Fprintln(os.Stderr, "DEPRECATED: probe.interval migrated to measurement.interval")
	}
	if c.Measurement.TargetBandwidthUp != "" || c.Measurement.TargetBandwidthDown != "" {
		usedLegacy := false
		if c.Measurement.TCPTargetBandwidthUp == "" && c.Measurement.TargetBandwidthUp != "" {
			c.Measurement.TCPTargetBandwidthUp = c.Measurement.TargetBandwidthUp
			usedLegacy = true
		}
		if c.Measurement.TCPTargetBandwidthDown == "" && c.Measurement.TargetBandwidthDown != "" {
			c.Measurement.TCPTargetBandwidthDown = c.Measurement.TargetBandwidthDown
			usedLegacy = true
		}
		if c.Measurement.UDPTargetBandwidthUp == "" && c.Measurement.TargetBandwidthUp != "" {
			c.Measurement.UDPTargetBandwidthUp = c.Measurement.TargetBandwidthUp
			usedLegacy = true
		}
		if c.Measurement.UDPTargetBandwidthDown == "" && c.Measurement.TargetBandwidthDown != "" {
			c.Measurement.UDPTargetBandwidthDown = c.Measurement.TargetBandwidthDown
			usedLegacy = true
		}
		if usedLegacy {
			fmt.Fprintln(os.Stderr, "DEPRECATED: measurement.target_bandwidth_* migrated to tcp_/udp_target_bandwidth_*")
		}
	}
	if c.Measurement.Interval.Duration() != 0 && c.Measurement.Schedule.MinInterval.Duration() == 0 {
		c.Measurement.Schedule.MinInterval = c.Measurement.Interval
		c.Measurement.Schedule.MaxInterval = Duration(c.Measurement.Interval.Duration() * 2)
		fmt.Fprintln(os.Stderr, "DEPRECATED: measurement.interval migrated to measurement.schedule.min_interval/max_interval")
	}
	if weightsLegacyZero(c.Scoring.Weights) {
		// no legacy weights set
	} else if weightsTCPZero(c.Scoring.WeightsTCP) {
		c.Scoring.WeightsTCP.RTT = c.Scoring.Weights.RTT
		c.Scoring.WeightsTCP.Jitter = c.Scoring.Weights.Jitter
		c.Scoring.WeightsTCP.Retrans = c.Scoring.Weights.Loss
		fmt.Fprintln(os.Stderr, "DEPRECATED: scoring.weights migrated to scoring.weights_tcp")
	}
	if c.Scoring.MetricRefRTTMs != 0 && c.Scoring.RefRTTMs == 0 {
		c.Scoring.RefRTTMs = c.Scoring.MetricRefRTTMs
		c.Scoring.RefJitterMs = c.Scoring.MetricRefJitterMs
		c.Scoring.RefLossRate = c.Scoring.MetricRefLoss
		fmt.Fprintln(os.Stderr, "DEPRECATED: scoring.metric_ref_* migrated to scoring.ref_*")
	}
	if c.Switching.ConfirmWindows != 0 && c.Switching.ConfirmDuration.Duration() == 0 {
		interval := c.Measurement.Interval.Duration()
		if interval == 0 {
			interval = defaultMeasurementInterval
		}
		c.Switching.ConfirmDuration = Duration(time.Duration(c.Switching.ConfirmWindows) * interval)
		fmt.Fprintln(os.Stderr, "DEPRECATED: switching.confirm_windows migrated to switching.confirm_duration")
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
		up.MeasureHost = strings.TrimSpace(up.MeasureHost)
		if up.Tag == "" || up.Host == "" {
			return fmt.Errorf("upstream tag and host are required")
		}
		if up.MeasureHost == "" {
			up.MeasureHost = up.Host
		}
		if up.MeasurePort <= 0 || up.MeasurePort > 65535 {
			return fmt.Errorf("upstream %s measure_port must be in 1..65535", up.Tag)
		}
		if up.Priority < 0 {
			return fmt.Errorf("upstream %s priority must be >= 0", up.Tag)
		}
		if up.Bias < -1 || up.Bias > 1 {
			return fmt.Errorf("upstream %s bias must be in [-1,1]", up.Tag)
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
			if _, err := ParseBandwidth(up.Ingress.Rate); err != nil {
				return fmt.Errorf("upstream %s ingress.rate: %w", up.Tag, err)
			}
			if up.Ingress.Ceil != "" {
				if _, err := ParseBandwidth(up.Ingress.Ceil); err != nil {
					return fmt.Errorf("upstream %s ingress.ceil: %w", up.Tag, err)
				}
			}
			if up.Ingress.Burst != "" {
				if _, err := ParseSize(up.Ingress.Burst); err != nil {
					return fmt.Errorf("upstream %s ingress.burst: %w", up.Tag, err)
				}
			}
			if up.Ingress.Cburst != "" {
				if _, err := ParseSize(up.Ingress.Cburst); err != nil {
					return fmt.Errorf("upstream %s ingress.cburst: %w", up.Tag, err)
				}
			}
		}
		if up.Egress != nil {
			if strings.TrimSpace(up.Egress.Rate) == "" {
				return fmt.Errorf("upstream %s egress.rate is required", up.Tag)
			}
			if _, err := ParseBandwidth(up.Egress.Rate); err != nil {
				return fmt.Errorf("upstream %s egress.rate: %w", up.Tag, err)
			}
			if up.Egress.Ceil != "" {
				if _, err := ParseBandwidth(up.Egress.Ceil); err != nil {
					return fmt.Errorf("upstream %s egress.ceil: %w", up.Tag, err)
				}
			}
			if up.Egress.Burst != "" {
				if _, err := ParseSize(up.Egress.Burst); err != nil {
					return fmt.Errorf("upstream %s egress.burst: %w", up.Tag, err)
				}
			}
			if up.Egress.Cburst != "" {
				if _, err := ParseSize(up.Egress.Cburst); err != nil {
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
			if _, err := ParseBandwidth(ln.Ingress.Rate); err != nil {
				return fmt.Errorf("listener %s ingress.rate: %w", key, err)
			}
			if ln.Ingress.Ceil != "" {
				if _, err := ParseBandwidth(ln.Ingress.Ceil); err != nil {
					return fmt.Errorf("listener %s ingress.ceil: %w", key, err)
				}
			}
			if ln.Ingress.Burst != "" {
				if _, err := ParseSize(ln.Ingress.Burst); err != nil {
					return fmt.Errorf("listener %s ingress.burst: %w", key, err)
				}
			}
			if ln.Ingress.Cburst != "" {
				if _, err := ParseSize(ln.Ingress.Cburst); err != nil {
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
			if _, err := ParseBandwidth(ln.Egress.Rate); err != nil {
				return fmt.Errorf("listener %s egress.rate: %w", key, err)
			}
			if ln.Egress.Ceil != "" {
				if _, err := ParseBandwidth(ln.Egress.Ceil); err != nil {
					return fmt.Errorf("listener %s egress.ceil: %w", key, err)
				}
			}
			if ln.Egress.Burst != "" {
				if _, err := ParseSize(ln.Egress.Burst); err != nil {
					return fmt.Errorf("listener %s egress.burst: %w", key, err)
				}
			}
			if ln.Egress.Cburst != "" {
				if _, err := ParseSize(ln.Egress.Cburst); err != nil {
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
	if c.Measurement.Interval.Duration() <= 0 {
		return errors.New("measurement.interval must be > 0")
	}
	if c.Measurement.DiscoveryDelay.Duration() < 0 {
		return errors.New("measurement.discovery_delay must be >= 0")
	}
	if c.Measurement.Samples <= 0 {
		return errors.New("measurement.samples must be > 0")
	}
	if c.Measurement.MaxSampleDuration.Duration() <= 0 {
		return errors.New("measurement.max_sample_duration must be > 0")
	}
	if c.Measurement.MaxCycleDuration.Duration() <= 0 {
		return errors.New("measurement.max_cycle_duration must be > 0")
	}
	if c.Measurement.FastStartTimeout.Duration() <= 0 {
		return errors.New("measurement.fast_start_timeout must be > 0")
	}
	if c.Measurement.WarmupDuration.Duration() < 0 {
		return errors.New("measurement.warmup_duration must be >= 0")
	}
	if c.Measurement.StaleThreshold.Duration() <= 0 {
		return errors.New("measurement.stale_threshold must be > 0")
	}
	if _, err := ParseBandwidth(c.Measurement.TCPTargetBandwidthUp); err != nil {
		return fmt.Errorf("measurement.tcp_target_bandwidth_up: %w", err)
	}
	if _, err := ParseBandwidth(c.Measurement.TCPTargetBandwidthDown); err != nil {
		return fmt.Errorf("measurement.tcp_target_bandwidth_down: %w", err)
	}
	if _, err := ParseBandwidth(c.Measurement.UDPTargetBandwidthUp); err != nil {
		return fmt.Errorf("measurement.udp_target_bandwidth_up: %w", err)
	}
	if _, err := ParseBandwidth(c.Measurement.UDPTargetBandwidthDown); err != nil {
		return fmt.Errorf("measurement.udp_target_bandwidth_down: %w", err)
	}
	if _, err := ParseSize(c.Measurement.SampleBytes); err != nil {
		return fmt.Errorf("measurement.sample_bytes: %w", err)
	}
	if c.Measurement.Schedule.MinInterval.Duration() <= 0 {
		return errors.New("measurement.schedule.min_interval must be > 0")
	}
	if c.Measurement.Schedule.MaxInterval.Duration() <= 0 {
		return errors.New("measurement.schedule.max_interval must be > 0")
	}
	if c.Measurement.Schedule.MaxInterval.Duration() < c.Measurement.Schedule.MinInterval.Duration() {
		return errors.New("measurement.schedule.max_interval must be >= min_interval")
	}
	if c.Measurement.Schedule.InterUpstreamGap.Duration() < 0 {
		return errors.New("measurement.schedule.inter_upstream_gap must be >= 0")
	}
	if c.Measurement.Schedule.MaxUtilization <= 0 || c.Measurement.Schedule.MaxUtilization > 1 {
		return errors.New("measurement.schedule.max_utilization must be in (0,1]")
	}
	if _, err := ParseBandwidth(c.Measurement.Schedule.RequiredHeadroom); err != nil {
		return fmt.Errorf("measurement.schedule.required_headroom: %w", err)
	}
	tcpEnabled := util.BoolValue(c.Measurement.TCPEnabled, defaultMeasurementTCPEnabled)
	udpEnabled := util.BoolValue(c.Measurement.UDPEnabled, defaultMeasurementUDPEnabled)
	if !tcpEnabled && !udpEnabled {
		return errors.New("measurement requires at least one protocol (tcp_enabled or udp_enabled)")
	}
	if c.Scoring.EMAAlpha <= 0 || c.Scoring.EMAAlpha > 1 {
		return errors.New("scoring.ema_alpha must be in (0,1]")
	}
	if _, err := ParseBandwidth(c.Scoring.RefBandwidthUp); err != nil {
		return fmt.Errorf("scoring.ref_bandwidth_up: %w", err)
	}
	if _, err := ParseBandwidth(c.Scoring.RefBandwidthDown); err != nil {
		return fmt.Errorf("scoring.ref_bandwidth_down: %w", err)
	}
	if c.Scoring.RefRTTMs <= 0 || c.Scoring.RefJitterMs <= 0 {
		return errors.New("scoring ref rtt/jitter must be > 0")
	}
	if c.Scoring.RefRetransRate <= 0 || c.Scoring.RefLossRate <= 0 {
		return errors.New("scoring ref retrans/loss must be > 0")
	}
	tcpWeightSum := c.Scoring.WeightsTCP.BandwidthUp + c.Scoring.WeightsTCP.BandwidthDown + c.Scoring.WeightsTCP.RTT + c.Scoring.WeightsTCP.Jitter + c.Scoring.WeightsTCP.Retrans
	if tcpWeightSum <= 0 {
		return errors.New("scoring.weights_tcp must sum to > 0")
	}
	if diff := tcpWeightSum - 1; diff > 0.001 || diff < -0.001 {
		c.Scoring.WeightsTCP.BandwidthUp /= tcpWeightSum
		c.Scoring.WeightsTCP.BandwidthDown /= tcpWeightSum
		c.Scoring.WeightsTCP.RTT /= tcpWeightSum
		c.Scoring.WeightsTCP.Jitter /= tcpWeightSum
		c.Scoring.WeightsTCP.Retrans /= tcpWeightSum
	}
	udpWeightSum := c.Scoring.WeightsUDP.BandwidthUp + c.Scoring.WeightsUDP.BandwidthDown + c.Scoring.WeightsUDP.RTT + c.Scoring.WeightsUDP.Jitter + c.Scoring.WeightsUDP.Loss
	if udpWeightSum <= 0 {
		return errors.New("scoring.weights_udp must sum to > 0")
	}
	if diff := udpWeightSum - 1; diff > 0.001 || diff < -0.001 {
		c.Scoring.WeightsUDP.BandwidthUp /= udpWeightSum
		c.Scoring.WeightsUDP.BandwidthDown /= udpWeightSum
		c.Scoring.WeightsUDP.RTT /= udpWeightSum
		c.Scoring.WeightsUDP.Jitter /= udpWeightSum
		c.Scoring.WeightsUDP.Loss /= udpWeightSum
	}
	protocolSum := c.Scoring.ProtocolWeightTCP + c.Scoring.ProtocolWeightUDP
	if protocolSum <= 0 {
		return errors.New("scoring protocol weights must sum to > 0")
	}
	if diff := protocolSum - 1; diff > 0.001 || diff < -0.001 {
		c.Scoring.ProtocolWeightTCP /= protocolSum
		c.Scoring.ProtocolWeightUDP /= protocolSum
	}
	if c.Scoring.UtilizationMinMult <= 0 || c.Scoring.UtilizationMinMult > 1 {
		return errors.New("scoring.utilization_min_mult must be in (0,1]")
	}
	if c.Scoring.UtilizationThresh <= 0 {
		return errors.New("scoring.utilization_threshold must be > 0")
	}
	if c.Scoring.UtilizationExponent <= 0 {
		return errors.New("scoring.utilization_exponent must be > 0")
	}
	if c.Scoring.UtilizationWindowSec <= 0 {
		return errors.New("scoring.utilization_window_sec must be > 0")
	}
	if c.Scoring.UtilizationUpdateSec <= 0 {
		return errors.New("scoring.utilization_update_sec must be > 0")
	}
	if c.Scoring.BiasKappa <= 0 {
		return errors.New("scoring.bias_kappa must be > 0")
	}
	if c.Limits.MaxTCPConns <= 0 || c.Limits.MaxUDPMappings <= 0 {
		return errors.New("limits must be > 0")
	}
	if c.Timeouts.TCPIdleSeconds <= 0 || c.Timeouts.UDPIdleSeconds <= 0 {
		return errors.New("timeouts must be > 0")
	}
	if c.Switching.ConfirmDuration.Duration() < 0 {
		return errors.New("switching.confirm_duration must be >= 0")
	}
	if c.Switching.FailureLossThreshold <= 0 || c.Switching.FailureLossThreshold > 1 {
		return errors.New("switching.failure_loss_threshold must be in (0,1]")
	}
	if c.Switching.FailureRetransThresh <= 0 || c.Switching.FailureRetransThresh > 1 {
		return errors.New("switching.failure_retrans_threshold must be in (0,1]")
	}
	if c.Switching.SwitchThreshold < 0 {
		return errors.New("switching.switch_threshold must be >= 0")
	}
	if c.Switching.MinHoldSeconds < 0 {
		return errors.New("switching.min_hold_seconds must be >= 0")
	}
	c.Resolver.Strategy = strings.ToLower(strings.TrimSpace(c.Resolver.Strategy))
	if c.Resolver.Strategy != "" {
		switch c.Resolver.Strategy {
		case ResolverStrategyIPv4Only, ResolverStrategyPreferV6:
		default:
			return fmt.Errorf("resolver.strategy must be %q or %q", ResolverStrategyIPv4Only, ResolverStrategyPreferV6)
		}
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
			if _, err := ParseBandwidth(c.Shaping.AggregateBandwidth); err != nil {
				return fmt.Errorf("shaping.aggregate_bandwidth: %w", err)
			}
		}
	}
	return nil
}

func weightsTCPZero(cfg WeightsTCPConfig) bool {
	return cfg.BandwidthUp == 0 && cfg.BandwidthDown == 0 && cfg.RTT == 0 && cfg.Jitter == 0 && cfg.Retrans == 0
}

func weightsUDPZero(cfg WeightsUDPConfig) bool {
	return cfg.BandwidthUp == 0 && cfg.BandwidthDown == 0 && cfg.RTT == 0 && cfg.Jitter == 0 && cfg.Loss == 0
}

func weightsLegacyZero(cfg WeightsLegacy) bool {
	return cfg.RTT == 0 && cfg.Jitter == 0 && cfg.Loss == 0
}
