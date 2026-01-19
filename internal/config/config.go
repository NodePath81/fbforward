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
	defaultReachabilityInterval   = 1 * time.Second
	defaultReachabilityWindowSize = 5

	defaultMeasurementStartupDelay           = 10 * time.Second
	defaultMeasurementStaleThreshold         = 60 * time.Minute
	defaultMeasurementFallbackToICMPOnStale  = true
	defaultMeasurementScheduleMinInterval    = 15 * time.Minute
	defaultMeasurementScheduleMaxInterval    = 45 * time.Minute
	defaultMeasurementScheduleUpstreamGap    = 5 * time.Second
	defaultMeasurementScheduleMaxUtilization = 0.7
	defaultMeasurementRequiredFreeBandwidth  = "0"
	defaultFastStartEnabled                  = true
	defaultFastStartTimeout                  = 500 * time.Millisecond
	defaultWarmupDuration                    = 15 * time.Second

	defaultMeasurementTargetUp   = "10m"
	defaultMeasurementTargetDown = "50m"
	defaultMeasurementChunkSize  = "1200"
	defaultMeasurementSampleSize = "500kb"
	defaultMeasurementSampleCnt  = 1
	defaultMeasurementPerSample  = 10 * time.Second
	defaultMeasurementPerCycle   = 30 * time.Second
	defaultMeasurementTCPEnabled = true
	defaultMeasurementUDPEnabled = true
	defaultMeasurementTCPAlt     = true

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
	defaultUtilizationWindow    = 5 * time.Second
	defaultUtilizationUpdate    = 1 * time.Second
	defaultUtilizationThreshold = 0.7
	defaultUtilizationMinMult   = 0.3
	defaultUtilizationExponent  = 2.0
	defaultBiasKappa            = 0.693147

	defaultSwitchConfirmDuration = 15 * time.Second
	defaultSwitchScoreDelta      = 5.0
	defaultSwitchMinHold         = 30 * time.Second
	defaultFailureLoss           = 0.2
	defaultFailureRetrans        = 0.2

	defaultForwardingMaxTCPConnections = 50
	defaultForwardingMaxUDPMappings    = 500
	defaultForwardingTCPIdle           = 60 * time.Second
	defaultForwardingUDPIdle           = 30 * time.Second

	defaultControlAddr           = "127.0.0.1"
	defaultControlPort           = 8080
	defaultControlWebUIEnabled   = true
	defaultControlMetricsEnabled = true

	defaultMeasurePort    = 9876
	DefaultShapingIFB     = "ifb0"
	DefaultAggregateLimit = "1g"
	maxListeners          = 45

	DNSStrategyIPv4Only = "ipv4_only"
	DNSStrategyPreferV6 = "prefer_ipv6"
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
	Hostname     string             `yaml:"hostname"`
	Forwarding   ForwardingConfig   `yaml:"forwarding"`
	Upstreams    []UpstreamConfig   `yaml:"upstreams"`
	DNS          DNSConfig          `yaml:"dns"`
	Reachability ReachabilityConfig `yaml:"reachability"`
	Measurement  MeasurementConfig  `yaml:"measurement"`
	Scoring      ScoringConfig      `yaml:"scoring"`
	Switching    SwitchingConfig    `yaml:"switching"`
	Control      ControlConfig      `yaml:"control"`
	Shaping      ShapingConfig      `yaml:"shaping"`
}

type ForwardingConfig struct {
	Listeners   []ListenerConfig       `yaml:"listeners"`
	Limits      ForwardingLimitsConfig `yaml:"limits"`
	IdleTimeout IdleTimeoutConfig      `yaml:"idle_timeout"`
}

type ForwardingLimitsConfig struct {
	MaxTCPConnections int `yaml:"max_tcp_connections"`
	MaxUDPMappings    int `yaml:"max_udp_mappings"`
}

type IdleTimeoutConfig struct {
	TCP Duration `yaml:"tcp"`
	UDP Duration `yaml:"udp"`
}

type ListenerConfig struct {
	BindAddr string              `yaml:"bind_addr"`
	BindPort int                 `yaml:"bind_port"`
	Protocol string              `yaml:"protocol"`
	Shaping  *ShapingLimitConfig `yaml:"shaping"`
}

type UpstreamConfig struct {
	Tag         string                    `yaml:"tag"`
	Destination DestinationConfig         `yaml:"destination"`
	Measurement UpstreamMeasurementConfig `yaml:"measurement"`
	Priority    float64                   `yaml:"priority"`
	Bias        float64                   `yaml:"bias"`
	Shaping     *ShapingLimitConfig       `yaml:"shaping"`
}

type DestinationConfig struct {
	Host string `yaml:"host"`
}

type UpstreamMeasurementConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DNSConfig struct {
	Servers  []string `yaml:"servers"`
	Strategy string   `yaml:"strategy"`
}

type ReachabilityConfig struct {
	ProbeInterval Duration `yaml:"probe_interval"`
	WindowSize    int      `yaml:"window_size"`
	StartupDelay  Duration `yaml:"startup_delay"`
}

type MeasurementConfig struct {
	StartupDelay          Duration                   `yaml:"startup_delay"`
	StaleThreshold        Duration                   `yaml:"stale_threshold"`
	FallbackToICMPOnStale *bool                      `yaml:"fallback_to_icmp_on_stale"`
	Schedule              MeasurementScheduleConfig  `yaml:"schedule"`
	FastStart             MeasurementFastStartConfig `yaml:"fast_start"`
	Protocols             MeasurementProtocolsConfig `yaml:"protocols"`
}

type MeasurementScheduleConfig struct {
	Interval    MeasurementIntervalConfig `yaml:"interval"`
	UpstreamGap Duration                  `yaml:"upstream_gap"`
	Headroom    MeasurementHeadroomConfig `yaml:"headroom"`
}

type MeasurementIntervalConfig struct {
	Min Duration `yaml:"min"`
	Max Duration `yaml:"max"`
}

type MeasurementHeadroomConfig struct {
	MaxLinkUtilization    float64 `yaml:"max_link_utilization"`
	RequiredFreeBandwidth string  `yaml:"required_free_bandwidth"`
}

type MeasurementFastStartConfig struct {
	Enabled        *bool    `yaml:"enabled"`
	Timeout        Duration `yaml:"timeout"`
	WarmupDuration Duration `yaml:"warmup_duration"`
}

type MeasurementProtocolsConfig struct {
	TCP MeasurementProtocolConfig `yaml:"tcp"`
	UDP MeasurementProtocolConfig `yaml:"udp"`
}

type MeasurementProtocolConfig struct {
	Enabled         *bool                      `yaml:"enabled"`
	Alternate       *bool                      `yaml:"alternate"`
	TargetBandwidth MeasurementBandwidthConfig `yaml:"target_bandwidth"`
	ChunkSize       string                     `yaml:"chunk_size"`
	SampleSize      string                     `yaml:"sample_size"`
	SampleCount     int                        `yaml:"sample_count"`
	Timeout         MeasurementTimeoutConfig   `yaml:"timeout"`
}

type MeasurementBandwidthConfig struct {
	Upload   string `yaml:"upload"`
	Download string `yaml:"download"`
}

type MeasurementTimeoutConfig struct {
	PerSample Duration `yaml:"per_sample"`
	PerCycle  Duration `yaml:"per_cycle"`
}

type ScoringConfig struct {
	Smoothing          ScoringSmoothingConfig   `yaml:"smoothing"`
	Reference          ScoringReferenceConfig   `yaml:"reference"`
	Weights            ScoringWeightsConfig     `yaml:"weights"`
	UtilizationPenalty UtilizationPenaltyConfig `yaml:"utilization_penalty"`
	BiasTransform      BiasTransformConfig      `yaml:"bias_transform"`
}

type ScoringSmoothingConfig struct {
	Alpha float64 `yaml:"alpha"`
}

type ScoringReferenceConfig struct {
	TCP ProtocolReferenceConfig `yaml:"tcp"`
	UDP ProtocolReferenceConfig `yaml:"udp"`
}

type ProtocolReferenceConfig struct {
	Bandwidth      ReferenceBandwidthConfig `yaml:"bandwidth"`
	Latency        ReferenceLatencyConfig   `yaml:"latency"`
	RetransmitRate float64                  `yaml:"retransmit_rate"`
	LossRate       float64                  `yaml:"loss_rate"`
}

type ReferenceBandwidthConfig struct {
	Upload   string `yaml:"upload"`
	Download string `yaml:"download"`
}

type ReferenceLatencyConfig struct {
	RTT    float64 `yaml:"rtt"`
	Jitter float64 `yaml:"jitter"`
}

type ScoringWeightsConfig struct {
	TCP           WeightsTCPConfig    `yaml:"tcp"`
	UDP           WeightsUDPConfig    `yaml:"udp"`
	ProtocolBlend ProtocolBlendConfig `yaml:"protocol_blend"`
}

type WeightsTCPConfig struct {
	BandwidthUpload   float64 `yaml:"bandwidth_upload"`
	BandwidthDownload float64 `yaml:"bandwidth_download"`
	RTT               float64 `yaml:"rtt"`
	Jitter            float64 `yaml:"jitter"`
	RetransmitRate    float64 `yaml:"retransmit_rate"`
}

type WeightsUDPConfig struct {
	BandwidthUpload   float64 `yaml:"bandwidth_upload"`
	BandwidthDownload float64 `yaml:"bandwidth_download"`
	RTT               float64 `yaml:"rtt"`
	Jitter            float64 `yaml:"jitter"`
	LossRate          float64 `yaml:"loss_rate"`
}

type ProtocolBlendConfig struct {
	TCPWeight float64 `yaml:"tcp_weight"`
	UDPWeight float64 `yaml:"udp_weight"`
}

type UtilizationPenaltyConfig struct {
	Enabled        *bool    `yaml:"enabled"`
	WindowDuration Duration `yaml:"window_duration"`
	UpdateInterval Duration `yaml:"update_interval"`
	Threshold      float64  `yaml:"threshold"`
	MinMultiplier  float64  `yaml:"min_multiplier"`
	Exponent       float64  `yaml:"exponent"`
}

type BiasTransformConfig struct {
	Kappa float64 `yaml:"kappa"`
}

type SwitchingConfig struct {
	Auto                 SwitchingAutoConfig     `yaml:"auto"`
	Failover             SwitchingFailoverConfig `yaml:"failover"`
	CloseFlowsOnFailover bool                    `yaml:"close_flows_on_failover"`
}

type SwitchingAutoConfig struct {
	ConfirmDuration     Duration `yaml:"confirm_duration"`
	ScoreDeltaThreshold float64  `yaml:"score_delta_threshold"`
	MinHoldTime         Duration `yaml:"min_hold_time"`
}

type SwitchingFailoverConfig struct {
	LossRateThreshold       float64 `yaml:"loss_rate_threshold"`
	RetransmitRateThreshold float64 `yaml:"retransmit_rate_threshold"`
}

type ControlConfig struct {
	BindAddr  string               `yaml:"bind_addr"`
	BindPort  int                  `yaml:"bind_port"`
	AuthToken string               `yaml:"auth_token"`
	WebUI     ControlWebUIConfig   `yaml:"webui"`
	Metrics   ControlMetricsConfig `yaml:"metrics"`
}

type ControlWebUIConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type ControlMetricsConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type ShapingConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Interface      string `yaml:"interface"`
	IFBDevice      string `yaml:"ifb_device"`
	AggregateLimit string `yaml:"aggregate_limit"`

	AggregateLimitBits uint64 `yaml:"-"`
}

type ShapingLimitConfig struct {
	UploadLimit   string `yaml:"upload_limit"`
	DownloadLimit string `yaml:"download_limit"`
}

func (w ControlWebUIConfig) IsEnabled() bool {
	return util.BoolValue(w.Enabled, defaultControlWebUIEnabled)
}

func (m ControlMetricsConfig) IsEnabled() bool {
	return util.BoolValue(m.Enabled, defaultControlMetricsEnabled)
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
	if c.Forwarding.Limits.MaxTCPConnections == 0 {
		c.Forwarding.Limits.MaxTCPConnections = defaultForwardingMaxTCPConnections
	}
	if c.Forwarding.Limits.MaxUDPMappings == 0 {
		c.Forwarding.Limits.MaxUDPMappings = defaultForwardingMaxUDPMappings
	}
	if c.Forwarding.IdleTimeout.TCP == 0 {
		c.Forwarding.IdleTimeout.TCP = Duration(defaultForwardingTCPIdle)
	}
	if c.Forwarding.IdleTimeout.UDP == 0 {
		c.Forwarding.IdleTimeout.UDP = Duration(defaultForwardingUDPIdle)
	}

	if c.Reachability.ProbeInterval == 0 {
		c.Reachability.ProbeInterval = Duration(defaultReachabilityInterval)
	}
	if c.Reachability.WindowSize == 0 {
		c.Reachability.WindowSize = defaultReachabilityWindowSize
	}
	if c.Reachability.StartupDelay == 0 {
		c.Reachability.StartupDelay = Duration(time.Duration(c.Reachability.WindowSize) * c.Reachability.ProbeInterval.Duration())
	}

	if c.Measurement.StartupDelay == 0 {
		c.Measurement.StartupDelay = Duration(defaultMeasurementStartupDelay)
	}
	if c.Measurement.StaleThreshold == 0 {
		c.Measurement.StaleThreshold = Duration(defaultMeasurementStaleThreshold)
	}
	if c.Measurement.FallbackToICMPOnStale == nil {
		val := defaultMeasurementFallbackToICMPOnStale
		c.Measurement.FallbackToICMPOnStale = &val
	}

	if c.Measurement.Schedule.Interval.Min == 0 {
		c.Measurement.Schedule.Interval.Min = Duration(defaultMeasurementScheduleMinInterval)
	}
	if c.Measurement.Schedule.Interval.Max == 0 {
		c.Measurement.Schedule.Interval.Max = Duration(defaultMeasurementScheduleMaxInterval)
	}
	if c.Measurement.Schedule.UpstreamGap == 0 {
		c.Measurement.Schedule.UpstreamGap = Duration(defaultMeasurementScheduleUpstreamGap)
	}
	if c.Measurement.Schedule.Headroom.MaxLinkUtilization == 0 {
		c.Measurement.Schedule.Headroom.MaxLinkUtilization = defaultMeasurementScheduleMaxUtilization
	}
	if c.Measurement.Schedule.Headroom.RequiredFreeBandwidth == "" {
		c.Measurement.Schedule.Headroom.RequiredFreeBandwidth = defaultMeasurementRequiredFreeBandwidth
	}

	if c.Measurement.FastStart.Enabled == nil {
		val := defaultFastStartEnabled
		c.Measurement.FastStart.Enabled = &val
	}
	if c.Measurement.FastStart.Timeout == 0 {
		c.Measurement.FastStart.Timeout = Duration(defaultFastStartTimeout)
	}
	if c.Measurement.FastStart.WarmupDuration == 0 {
		c.Measurement.FastStart.WarmupDuration = Duration(defaultWarmupDuration)
	}

	setProtocolDefaults(&c.Measurement.Protocols.TCP, true)
	setProtocolDefaults(&c.Measurement.Protocols.UDP, false)

	if c.Scoring.Smoothing.Alpha == 0 {
		c.Scoring.Smoothing.Alpha = defaultEMAAlpha
	}
	if c.Scoring.Reference.TCP.Bandwidth.Upload == "" {
		c.Scoring.Reference.TCP.Bandwidth.Upload = defaultRefBandwidthUp
	}
	if c.Scoring.Reference.TCP.Bandwidth.Download == "" {
		c.Scoring.Reference.TCP.Bandwidth.Download = defaultRefBandwidthDn
	}
	if c.Scoring.Reference.TCP.Latency.RTT == 0 {
		c.Scoring.Reference.TCP.Latency.RTT = defaultRefRTTMs
	}
	if c.Scoring.Reference.TCP.Latency.Jitter == 0 {
		c.Scoring.Reference.TCP.Latency.Jitter = defaultRefJitterMs
	}
	if c.Scoring.Reference.TCP.RetransmitRate == 0 {
		c.Scoring.Reference.TCP.RetransmitRate = defaultRefRetransRate
	}
	if c.Scoring.Reference.UDP.Bandwidth.Upload == "" {
		c.Scoring.Reference.UDP.Bandwidth.Upload = defaultRefBandwidthUp
	}
	if c.Scoring.Reference.UDP.Bandwidth.Download == "" {
		c.Scoring.Reference.UDP.Bandwidth.Download = defaultRefBandwidthDn
	}
	if c.Scoring.Reference.UDP.Latency.RTT == 0 {
		c.Scoring.Reference.UDP.Latency.RTT = defaultRefRTTMs
	}
	if c.Scoring.Reference.UDP.Latency.Jitter == 0 {
		c.Scoring.Reference.UDP.Latency.Jitter = defaultRefJitterMs
	}
	if c.Scoring.Reference.UDP.LossRate == 0 {
		c.Scoring.Reference.UDP.LossRate = defaultRefLossRate
	}

	if weightsTCPZero(c.Scoring.Weights.TCP) {
		c.Scoring.Weights.TCP = WeightsTCPConfig{
			BandwidthUpload:   defaultWeightTCPBwUp,
			BandwidthDownload: defaultWeightTCPBwDn,
			RTT:               defaultWeightTCPRTT,
			Jitter:            defaultWeightTCPJitter,
			RetransmitRate:    defaultWeightTCPRetrans,
		}
	}
	if weightsUDPZero(c.Scoring.Weights.UDP) {
		c.Scoring.Weights.UDP = WeightsUDPConfig{
			BandwidthUpload:   defaultWeightUDPBwUp,
			BandwidthDownload: defaultWeightUDPBwDn,
			RTT:               defaultWeightUDPRTT,
			Jitter:            defaultWeightUDPJitter,
			LossRate:          defaultWeightUDPLoss,
		}
	}
	if c.Scoring.Weights.ProtocolBlend.TCPWeight == 0 && c.Scoring.Weights.ProtocolBlend.UDPWeight == 0 {
		c.Scoring.Weights.ProtocolBlend.TCPWeight = defaultProtocolWeightTCP
		c.Scoring.Weights.ProtocolBlend.UDPWeight = defaultProtocolWeightUDP
	}

	if c.Scoring.UtilizationPenalty.Enabled == nil {
		val := defaultUtilizationEnabled
		c.Scoring.UtilizationPenalty.Enabled = &val
	}
	if c.Scoring.UtilizationPenalty.WindowDuration == 0 {
		c.Scoring.UtilizationPenalty.WindowDuration = Duration(defaultUtilizationWindow)
	}
	if c.Scoring.UtilizationPenalty.UpdateInterval == 0 {
		c.Scoring.UtilizationPenalty.UpdateInterval = Duration(defaultUtilizationUpdate)
	}
	if c.Scoring.UtilizationPenalty.Threshold == 0 {
		c.Scoring.UtilizationPenalty.Threshold = defaultUtilizationThreshold
	}
	if c.Scoring.UtilizationPenalty.MinMultiplier == 0 {
		c.Scoring.UtilizationPenalty.MinMultiplier = defaultUtilizationMinMult
	}
	if c.Scoring.UtilizationPenalty.Exponent == 0 {
		c.Scoring.UtilizationPenalty.Exponent = defaultUtilizationExponent
	}
	if c.Scoring.BiasTransform.Kappa == 0 {
		c.Scoring.BiasTransform.Kappa = defaultBiasKappa
	}

	if c.Switching.Auto.ConfirmDuration == 0 {
		c.Switching.Auto.ConfirmDuration = Duration(defaultSwitchConfirmDuration)
	}
	if c.Switching.Auto.ScoreDeltaThreshold == 0 {
		c.Switching.Auto.ScoreDeltaThreshold = defaultSwitchScoreDelta
	}
	if c.Switching.Auto.MinHoldTime == 0 {
		c.Switching.Auto.MinHoldTime = Duration(defaultSwitchMinHold)
	}
	if c.Switching.Failover.LossRateThreshold == 0 {
		c.Switching.Failover.LossRateThreshold = defaultFailureLoss
	}
	if c.Switching.Failover.RetransmitRateThreshold == 0 {
		c.Switching.Failover.RetransmitRateThreshold = defaultFailureRetrans
	}

	if c.Control.BindAddr == "" {
		c.Control.BindAddr = defaultControlAddr
	}
	if c.Control.BindPort == 0 {
		c.Control.BindPort = defaultControlPort
	}
	if c.Control.WebUI.Enabled == nil {
		enabled := defaultControlWebUIEnabled
		c.Control.WebUI.Enabled = &enabled
	}
	if c.Control.Metrics.Enabled == nil {
		enabled := defaultControlMetricsEnabled
		c.Control.Metrics.Enabled = &enabled
	}

	for i := range c.Upstreams {
		up := &c.Upstreams[i]
		if up.Measurement.Host == "" {
			up.Measurement.Host = up.Destination.Host
		}
		if up.Measurement.Port == 0 {
			up.Measurement.Port = defaultMeasurePort
		}
	}

	if c.Shaping.Enabled {
		if c.Shaping.IFBDevice == "" {
			c.Shaping.IFBDevice = DefaultShapingIFB
		}
		if c.Shaping.AggregateLimit == "" {
			c.Shaping.AggregateLimit = DefaultAggregateLimit
		}
	}
}

func weightsTCPZero(cfg WeightsTCPConfig) bool {
	return cfg.BandwidthUpload == 0 &&
		cfg.BandwidthDownload == 0 &&
		cfg.RTT == 0 &&
		cfg.Jitter == 0 &&
		cfg.RetransmitRate == 0
}

func weightsUDPZero(cfg WeightsUDPConfig) bool {
	return cfg.BandwidthUpload == 0 &&
		cfg.BandwidthDownload == 0 &&
		cfg.RTT == 0 &&
		cfg.Jitter == 0 &&
		cfg.LossRate == 0
}

func setProtocolDefaults(cfg *MeasurementProtocolConfig, isTCP bool) {
	if cfg.Enabled == nil {
		val := defaultMeasurementTCPEnabled
		if !isTCP {
			val = defaultMeasurementUDPEnabled
		}
		cfg.Enabled = &val
	}
	if isTCP && cfg.Alternate == nil {
		val := defaultMeasurementTCPAlt
		cfg.Alternate = &val
	}
	if cfg.TargetBandwidth.Upload == "" {
		cfg.TargetBandwidth.Upload = defaultMeasurementTargetUp
	}
	if cfg.TargetBandwidth.Download == "" {
		cfg.TargetBandwidth.Download = defaultMeasurementTargetDown
	}
	if cfg.ChunkSize == "" {
		cfg.ChunkSize = defaultMeasurementChunkSize
	}
	if cfg.SampleSize == "" {
		cfg.SampleSize = defaultMeasurementSampleSize
	}
	if cfg.SampleCount == 0 {
		cfg.SampleCount = defaultMeasurementSampleCnt
	}
	if cfg.Timeout.PerSample == 0 {
		cfg.Timeout.PerSample = Duration(defaultMeasurementPerSample)
	}
	if cfg.Timeout.PerCycle == 0 {
		cfg.Timeout.PerCycle = Duration(defaultMeasurementPerCycle)
	}
}

func (c *Config) validate() error {
	if len(c.Forwarding.Listeners) == 0 {
		return errors.New("forwarding.listeners must not be empty")
	}
	if len(c.Forwarding.Listeners) > maxListeners {
		return fmt.Errorf("too many listeners: %d (max %d)", len(c.Forwarding.Listeners), maxListeners)
	}
	if len(c.Upstreams) == 0 {
		return errors.New("upstreams must not be empty")
	}
	if c.Forwarding.Limits.MaxTCPConnections <= 0 || c.Forwarding.Limits.MaxUDPMappings <= 0 {
		return errors.New("forwarding.limits.max_tcp_connections and max_udp_mappings must be > 0")
	}
	if c.Forwarding.IdleTimeout.TCP.Duration() <= 0 || c.Forwarding.IdleTimeout.UDP.Duration() <= 0 {
		return errors.New("forwarding.idle_timeout.tcp and udp must be > 0")
	}

	seenTags := make(map[string]struct{}, len(c.Upstreams))
	for i := range c.Upstreams {
		up := &c.Upstreams[i]
		up.Tag = strings.TrimSpace(up.Tag)
		if up.Tag == "" {
			return errors.New("upstreams.tag must not be empty")
		}
		if _, ok := seenTags[up.Tag]; ok {
			return fmt.Errorf("duplicate upstream tag: %s", up.Tag)
		}
		seenTags[up.Tag] = struct{}{}
		up.Destination.Host = strings.TrimSpace(up.Destination.Host)
		if up.Destination.Host == "" {
			return fmt.Errorf("upstreams[%s].destination.host must not be empty", up.Tag)
		}
		if up.Measurement.Port <= 0 || up.Measurement.Port > 65535 {
			return fmt.Errorf("upstreams[%s].measurement.port must be in 1..65535", up.Tag)
		}
		if up.Priority < 0 {
			return fmt.Errorf("upstreams[%s].priority must be >= 0", up.Tag)
		}
		if up.Bias < -1 || up.Bias > 1 {
			return fmt.Errorf("upstreams[%s].bias must be in [-1,1]", up.Tag)
		}
		if up.Shaping != nil && !c.Shaping.Enabled {
			return fmt.Errorf("upstreams[%s].shaping requires shaping.enabled", up.Tag)
		}
		if up.Shaping != nil {
			if err := validateShapingLimits(up.Shaping, fmt.Sprintf("upstreams[%s].shaping", up.Tag)); err != nil {
				return err
			}
		}
	}

	seenListeners := make(map[string]struct{}, len(c.Forwarding.Listeners))
	for i := range c.Forwarding.Listeners {
		ln := &c.Forwarding.Listeners[i]
		ln.Protocol = strings.ToLower(strings.TrimSpace(ln.Protocol))
		key := fmt.Sprintf("%s:%d:%s", ln.BindAddr, ln.BindPort, ln.Protocol)
		if _, ok := seenListeners[key]; ok {
			return fmt.Errorf("duplicate listener: %s", key)
		}
		seenListeners[key] = struct{}{}
		if ln.BindPort <= 0 || ln.BindPort > 65535 {
			return fmt.Errorf("listener %s:%d bind_port must be in 1..65535", ln.BindAddr, ln.BindPort)
		}
		if ln.Protocol != "tcp" && ln.Protocol != "udp" {
			return fmt.Errorf("listener %s:%d protocol must be tcp or udp", ln.BindAddr, ln.BindPort)
		}
		if ln.Shaping != nil && !c.Shaping.Enabled {
			return fmt.Errorf("listener %s:%d shaping requires shaping.enabled", ln.BindAddr, ln.BindPort)
		}
		if ln.Shaping != nil {
			if err := validateShapingLimits(ln.Shaping, fmt.Sprintf("forwarding.listeners[%d].shaping", i)); err != nil {
				return err
			}
		}
	}

	if c.Control.AuthToken == "" {
		return errors.New("control.auth_token must not be empty")
	}
	if c.Control.BindPort <= 0 || c.Control.BindPort > 65535 {
		return errors.New("control.bind_port must be in 1..65535")
	}

	if c.Reachability.ProbeInterval.Duration() <= 0 {
		return errors.New("reachability.probe_interval must be > 0")
	}
	if c.Reachability.WindowSize <= 0 {
		return errors.New("reachability.window_size must be > 0")
	}
	if c.Reachability.StartupDelay.Duration() < 0 {
		return errors.New("reachability.startup_delay must be >= 0")
	}

	if c.Measurement.StartupDelay.Duration() < 0 {
		return errors.New("measurement.startup_delay must be >= 0")
	}
	if c.Measurement.StaleThreshold.Duration() <= 0 {
		return errors.New("measurement.stale_threshold must be > 0")
	}
	if c.Measurement.FastStart.Timeout.Duration() <= 0 {
		return errors.New("measurement.fast_start.timeout must be > 0")
	}
	if c.Measurement.FastStart.WarmupDuration.Duration() < 0 {
		return errors.New("measurement.fast_start.warmup_duration must be >= 0")
	}

	if c.Measurement.Schedule.Interval.Min.Duration() <= 0 {
		return errors.New("measurement.schedule.interval.min must be > 0")
	}
	if c.Measurement.Schedule.Interval.Max.Duration() <= 0 {
		return errors.New("measurement.schedule.interval.max must be > 0")
	}
	if c.Measurement.Schedule.Interval.Max.Duration() < c.Measurement.Schedule.Interval.Min.Duration() {
		return errors.New("measurement.schedule.interval.max must be >= min")
	}
	if c.Measurement.Schedule.UpstreamGap.Duration() < 0 {
		return errors.New("measurement.schedule.upstream_gap must be >= 0")
	}
	if c.Measurement.Schedule.Headroom.MaxLinkUtilization <= 0 || c.Measurement.Schedule.Headroom.MaxLinkUtilization > 1 {
		return errors.New("measurement.schedule.headroom.max_link_utilization must be in (0,1]")
	}
	if _, err := ParseBandwidth(c.Measurement.Schedule.Headroom.RequiredFreeBandwidth); err != nil {
		return fmt.Errorf("measurement.schedule.headroom.required_free_bandwidth: %w", err)
	}

	tcpEnabled := util.BoolValue(c.Measurement.Protocols.TCP.Enabled, defaultMeasurementTCPEnabled)
	udpEnabled := util.BoolValue(c.Measurement.Protocols.UDP.Enabled, defaultMeasurementUDPEnabled)
	if !tcpEnabled && !udpEnabled {
		return errors.New("measurement requires at least one protocol enabled")
	}
	if err := validateProtocolConfig("tcp", c.Measurement.Protocols.TCP, tcpEnabled); err != nil {
		return err
	}
	if err := validateProtocolConfig("udp", c.Measurement.Protocols.UDP, udpEnabled); err != nil {
		return err
	}

	if c.Scoring.Smoothing.Alpha <= 0 || c.Scoring.Smoothing.Alpha > 1 {
		return errors.New("scoring.smoothing.alpha must be in (0,1]")
	}
	if err := validateReferenceConfig("tcp", c.Scoring.Reference.TCP); err != nil {
		return err
	}
	if err := validateReferenceConfig("udp", c.Scoring.Reference.UDP); err != nil {
		return err
	}

	if err := normalizeTCPWeights(&c.Scoring.Weights.TCP); err != nil {
		return err
	}
	if err := normalizeUDPWeights(&c.Scoring.Weights.UDP); err != nil {
		return err
	}
	if err := normalizeProtocolBlend(&c.Scoring.Weights.ProtocolBlend); err != nil {
		return err
	}

	if c.Scoring.UtilizationPenalty.MinMultiplier <= 0 || c.Scoring.UtilizationPenalty.MinMultiplier > 1 {
		return errors.New("scoring.utilization_penalty.min_multiplier must be in (0,1]")
	}
	if c.Scoring.UtilizationPenalty.Threshold <= 0 {
		return errors.New("scoring.utilization_penalty.threshold must be > 0")
	}
	if c.Scoring.UtilizationPenalty.Exponent <= 0 {
		return errors.New("scoring.utilization_penalty.exponent must be > 0")
	}
	if c.Scoring.UtilizationPenalty.WindowDuration.Duration() <= 0 {
		return errors.New("scoring.utilization_penalty.window_duration must be > 0")
	}
	if c.Scoring.UtilizationPenalty.UpdateInterval.Duration() <= 0 {
		return errors.New("scoring.utilization_penalty.update_interval must be > 0")
	}
	if c.Scoring.BiasTransform.Kappa <= 0 {
		return errors.New("scoring.bias_transform.kappa must be > 0")
	}

	if c.Switching.Auto.ConfirmDuration.Duration() < 0 {
		return errors.New("switching.auto.confirm_duration must be >= 0")
	}
	if c.Switching.Auto.MinHoldTime.Duration() < 0 {
		return errors.New("switching.auto.min_hold_time must be >= 0")
	}
	if c.Switching.Failover.LossRateThreshold <= 0 || c.Switching.Failover.LossRateThreshold > 1 {
		return errors.New("switching.failover.loss_rate_threshold must be in (0,1]")
	}
	if c.Switching.Failover.RetransmitRateThreshold <= 0 || c.Switching.Failover.RetransmitRateThreshold > 1 {
		return errors.New("switching.failover.retransmit_rate_threshold must be in (0,1]")
	}

	c.DNS.Strategy = strings.ToLower(strings.TrimSpace(c.DNS.Strategy))
	if c.DNS.Strategy != "" {
		switch c.DNS.Strategy {
		case DNSStrategyIPv4Only, DNSStrategyPreferV6:
		default:
			return errors.New("dns.strategy must be ipv4_only or prefer_ipv6")
		}
	}

	if c.Shaping.Enabled {
		c.Shaping.Interface = strings.TrimSpace(c.Shaping.Interface)
		c.Shaping.IFBDevice = strings.TrimSpace(c.Shaping.IFBDevice)
		c.Shaping.AggregateLimit = strings.TrimSpace(c.Shaping.AggregateLimit)
		if c.Shaping.Interface == "" {
			return errors.New("shaping.interface is required")
		}
		if c.Shaping.IFBDevice == "" {
			return errors.New("shaping.ifb_device is required")
		}
		if c.Shaping.AggregateLimit != "" {
			if _, err := ParseBandwidth(c.Shaping.AggregateLimit); err != nil {
				return fmt.Errorf("shaping.aggregate_limit: %w", err)
			}
		}
	}

	return nil
}

func validateProtocolConfig(proto string, cfg MeasurementProtocolConfig, enabled bool) error {
	if !enabled {
		return nil
	}
	bwUp, err := ParseBandwidth(cfg.TargetBandwidth.Upload)
	if err != nil {
		return fmt.Errorf("measurement.protocols.%s.target_bandwidth.upload: %w", proto, err)
	}
	if bwUp <= 0 {
		return fmt.Errorf("measurement.protocols.%s.target_bandwidth.upload must be > 0", proto)
	}
	bwDown, err := ParseBandwidth(cfg.TargetBandwidth.Download)
	if err != nil {
		return fmt.Errorf("measurement.protocols.%s.target_bandwidth.download: %w", proto, err)
	}
	if bwDown <= 0 {
		return fmt.Errorf("measurement.protocols.%s.target_bandwidth.download must be > 0", proto)
	}
	chunkSize, err := ParseSize(cfg.ChunkSize)
	if err != nil {
		return fmt.Errorf("measurement.protocols.%s.chunk_size: %w", proto, err)
	}
	if chunkSize <= 0 {
		return fmt.Errorf("measurement.protocols.%s.chunk_size must be > 0", proto)
	}
	sampleSize, err := ParseSize(cfg.SampleSize)
	if err != nil {
		return fmt.Errorf("measurement.protocols.%s.sample_size: %w", proto, err)
	}
	if sampleSize <= 0 {
		return fmt.Errorf("measurement.protocols.%s.sample_size must be > 0", proto)
	}
	if cfg.SampleCount <= 0 {
		return fmt.Errorf("measurement.protocols.%s.sample_count must be > 0", proto)
	}
	if cfg.Timeout.PerSample.Duration() <= 0 {
		return fmt.Errorf("measurement.protocols.%s.timeout.per_sample must be > 0", proto)
	}
	if cfg.Timeout.PerCycle.Duration() <= 0 {
		return fmt.Errorf("measurement.protocols.%s.timeout.per_cycle must be > 0", proto)
	}
	return nil
}

func validateReferenceConfig(proto string, cfg ProtocolReferenceConfig) error {
	bwUp, err := ParseBandwidth(cfg.Bandwidth.Upload)
	if err != nil {
		return fmt.Errorf("scoring.reference.%s.bandwidth.upload: %w", proto, err)
	}
	if bwUp <= 0 {
		return fmt.Errorf("scoring.reference.%s.bandwidth.upload must be > 0", proto)
	}
	bwDown, err := ParseBandwidth(cfg.Bandwidth.Download)
	if err != nil {
		return fmt.Errorf("scoring.reference.%s.bandwidth.download: %w", proto, err)
	}
	if bwDown <= 0 {
		return fmt.Errorf("scoring.reference.%s.bandwidth.download must be > 0", proto)
	}
	if cfg.Latency.RTT <= 0 || cfg.Latency.Jitter <= 0 {
		return fmt.Errorf("scoring.reference.%s.latency.rtt/jitter must be > 0", proto)
	}
	if proto == "tcp" {
		if cfg.RetransmitRate <= 0 || cfg.RetransmitRate > 1 {
			return fmt.Errorf("scoring.reference.%s.retransmit_rate must be in (0,1]", proto)
		}
	}
	if proto == "udp" {
		if cfg.LossRate <= 0 || cfg.LossRate > 1 {
			return fmt.Errorf("scoring.reference.%s.loss_rate must be in (0,1]", proto)
		}
	}
	return nil
}

func validateShapingLimits(cfg *ShapingLimitConfig, path string) error {
	if cfg.UploadLimit == "" && cfg.DownloadLimit == "" {
		return fmt.Errorf("%s must specify upload_limit or download_limit", path)
	}
	if cfg.UploadLimit != "" {
		bits, err := ParseBandwidth(cfg.UploadLimit)
		if err != nil {
			return fmt.Errorf("%s.upload_limit: %w", path, err)
		}
		if bits == 0 {
			return fmt.Errorf("%s.upload_limit must be > 0", path)
		}
	}
	if cfg.DownloadLimit != "" {
		bits, err := ParseBandwidth(cfg.DownloadLimit)
		if err != nil {
			return fmt.Errorf("%s.download_limit: %w", path, err)
		}
		if bits == 0 {
			return fmt.Errorf("%s.download_limit must be > 0", path)
		}
	}
	return nil
}

func normalizeTCPWeights(cfg *WeightsTCPConfig) error {
	sum := cfg.BandwidthUpload + cfg.BandwidthDownload + cfg.RTT + cfg.Jitter + cfg.RetransmitRate
	if sum <= 0 {
		return errors.New("scoring.weights.tcp must sum to > 0")
	}
	if diff := sum - 1; diff > 0.001 || diff < -0.001 {
		cfg.BandwidthUpload /= sum
		cfg.BandwidthDownload /= sum
		cfg.RTT /= sum
		cfg.Jitter /= sum
		cfg.RetransmitRate /= sum
	}
	return nil
}

func normalizeUDPWeights(cfg *WeightsUDPConfig) error {
	sum := cfg.BandwidthUpload + cfg.BandwidthDownload + cfg.RTT + cfg.Jitter + cfg.LossRate
	if sum <= 0 {
		return errors.New("scoring.weights.udp must sum to > 0")
	}
	if diff := sum - 1; diff > 0.001 || diff < -0.001 {
		cfg.BandwidthUpload /= sum
		cfg.BandwidthDownload /= sum
		cfg.RTT /= sum
		cfg.Jitter /= sum
		cfg.LossRate /= sum
	}
	return nil
}

func normalizeProtocolBlend(cfg *ProtocolBlendConfig) error {
	sum := cfg.TCPWeight + cfg.UDPWeight
	if sum <= 0 {
		return errors.New("scoring.weights.protocol_blend must sum to > 0")
	}
	if diff := sum - 1; diff > 0.001 || diff < -0.001 {
		cfg.TCPWeight /= sum
		cfg.UDPWeight /= sum
	}
	return nil
}

func DefaultSwitchingConfig() SwitchingConfig {
	return SwitchingConfig{
		Auto: SwitchingAutoConfig{
			ConfirmDuration:     Duration(defaultSwitchConfirmDuration),
			ScoreDeltaThreshold: defaultSwitchScoreDelta,
			MinHoldTime:         Duration(defaultSwitchMinHold),
		},
		Failover: SwitchingFailoverConfig{
			LossRateThreshold:       defaultFailureLoss,
			RetransmitRateThreshold: defaultFailureRetrans,
		},
		CloseFlowsOnFailover: false,
	}
}
