package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	defaultReachabilityInterval   = 1 * time.Second
	defaultReachabilityWindowSize = 5

	defaultMeasurementStartupDelay          = 10 * time.Second
	defaultMeasurementStaleThreshold        = 60 * time.Minute
	defaultMeasurementFallbackToICMPOnStale = true
	defaultMeasurementScheduleMinInterval   = 15 * time.Minute
	defaultMeasurementScheduleMaxInterval   = 45 * time.Minute
	defaultMeasurementScheduleUpstreamGap   = 5 * time.Second
	defaultFastStartEnabled                 = true
	defaultFastStartTimeout                 = 500 * time.Millisecond
	defaultWarmupDuration                   = 15 * time.Second

	defaultMeasurementPingCount    = 5
	defaultMeasurementRetransmit   = "500kb"
	defaultMeasurementLossPackets  = 64
	defaultMeasurementPacketSize   = "1200"
	defaultMeasurementPerSample    = 10 * time.Second
	defaultMeasurementPerCycle     = 30 * time.Second
	defaultMeasurementTCPEnabled   = true
	defaultMeasurementUDPEnabled   = true
	defaultMeasurementSecurityMode = "off"

	defaultEMAAlpha          = 0.2
	defaultRefRTTMs          = 50
	defaultRefJitterMs       = 10
	defaultRefRetransRate    = 0.01
	defaultRefLossRate       = 0.01
	defaultWeightTCPRTT      = 0.25
	defaultWeightTCPJitter   = 0.10
	defaultWeightTCPRetrans  = 0.25
	defaultWeightUDPRTT      = 0.15
	defaultWeightUDPJitter   = 0.30
	defaultWeightUDPLoss     = 0.15
	defaultProtocolWeightTCP = 0.5
	defaultProtocolWeightUDP = 0.5
	defaultBiasKappa         = 0.693147

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
	defaultCoordinationHeartbeat = 10 * time.Second
	defaultLoggingLevel          = "info"
	defaultLoggingFormat         = "text"
	defaultGeoIPRefreshInterval  = 24 * time.Hour
	defaultIPLogGeoQueueSize     = 4096
	defaultIPLogWriteQueueSize   = 4096
	defaultIPLogBatchSize        = 100
	defaultIPLogFlushInterval    = 5 * time.Second
	defaultIPLogPruneInterval    = 1 * time.Hour

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
	Coordination CoordinationConfig `yaml:"coordination"`
	Logging      LoggingConfig      `yaml:"logging"`
	Shaping      ShapingConfig      `yaml:"shaping"`
	GeoIP        GeoIPConfig        `yaml:"geoip"`
	IPLog        IPLogConfig        `yaml:"ip_log"`
	Firewall     FirewallConfig     `yaml:"firewall"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
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
	Security              MeasurementSecurityConfig  `yaml:"security"`
}

type MeasurementSecurityConfig struct {
	Mode           string `yaml:"mode"`
	CAFile         string `yaml:"ca_file"`
	ServerName     string `yaml:"server_name"`
	ClientCertFile string `yaml:"client_cert_file"`
	ClientKeyFile  string `yaml:"client_key_file"`
}

type MeasurementScheduleConfig struct {
	Interval    MeasurementIntervalConfig `yaml:"interval"`
	UpstreamGap Duration                  `yaml:"upstream_gap"`
}

type MeasurementIntervalConfig struct {
	Min Duration `yaml:"min"`
	Max Duration `yaml:"max"`
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
	Enabled         *bool                    `yaml:"enabled"`
	PingCount       int                      `yaml:"ping_count"`
	RetransmitBytes string                   `yaml:"retransmit_bytes"`
	LossPackets     int                      `yaml:"loss_packets"`
	PacketSize      string                   `yaml:"packet_size"`
	Timeout         MeasurementTimeoutConfig `yaml:"timeout"`
}

type MeasurementTimeoutConfig struct {
	PerSample Duration `yaml:"per_sample"`
	PerCycle  Duration `yaml:"per_cycle"`
}

type ScoringConfig struct {
	Smoothing     ScoringSmoothingConfig `yaml:"smoothing"`
	Reference     ScoringReferenceConfig `yaml:"reference"`
	Weights       ScoringWeightsConfig   `yaml:"weights"`
	BiasTransform BiasTransformConfig    `yaml:"bias_transform"`
}

type ScoringSmoothingConfig struct {
	Alpha float64 `yaml:"alpha"`
}

type ScoringReferenceConfig struct {
	TCP ProtocolReferenceConfig `yaml:"tcp"`
	UDP ProtocolReferenceConfig `yaml:"udp"`
}

type ProtocolReferenceConfig struct {
	Latency        ReferenceLatencyConfig `yaml:"latency"`
	RetransmitRate float64                `yaml:"retransmit_rate"`
	LossRate       float64                `yaml:"loss_rate"`
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
	RTT            float64 `yaml:"rtt"`
	Jitter         float64 `yaml:"jitter"`
	RetransmitRate float64 `yaml:"retransmit_rate"`
}

type WeightsUDPConfig struct {
	RTT      float64 `yaml:"rtt"`
	Jitter   float64 `yaml:"jitter"`
	LossRate float64 `yaml:"loss_rate"`
}

type ProtocolBlendConfig struct {
	TCPWeight float64 `yaml:"tcp_weight"`
	UDPWeight float64 `yaml:"udp_weight"`
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

type CoordinationConfig struct {
	Endpoint          string   `yaml:"endpoint"`
	Pool              string   `yaml:"pool"`
	NodeID            string   `yaml:"node_id"`
	Token             string   `yaml:"token"`
	HeartbeatInterval Duration `yaml:"heartbeat_interval"`
}

type GeoIPConfig struct {
	Enabled         bool     `yaml:"enabled"`
	ASNDBURL        string   `yaml:"asn_db_url"`
	ASNDBPath       string   `yaml:"asn_db_path"`
	CountryDBURL    string   `yaml:"country_db_url"`
	CountryDBPath   string   `yaml:"country_db_path"`
	RefreshInterval Duration `yaml:"refresh_interval"`
}

type IPLogConfig struct {
	Enabled        bool     `yaml:"enabled"`
	DBPath         string   `yaml:"db_path"`
	Retention      Duration `yaml:"retention"`
	GeoQueueSize   int      `yaml:"geo_queue_size"`
	WriteQueueSize int      `yaml:"write_queue_size"`
	BatchSize      int      `yaml:"batch_size"`
	FlushInterval  Duration `yaml:"flush_interval"`
	PruneInterval  Duration `yaml:"prune_interval"`
}

type FirewallConfig struct {
	Enabled bool           `yaml:"enabled"`
	Default string         `yaml:"default"`
	Rules   []FirewallRule `yaml:"rules"`
}

type FirewallRule struct {
	Action  string `yaml:"action"`
	CIDR    string `yaml:"cidr,omitempty"`
	ASN     int    `yaml:"asn,omitempty"`
	Country string `yaml:"country,omitempty"`
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

func (c CoordinationConfig) HasAnyField() bool {
	return strings.TrimSpace(c.Endpoint) != "" ||
		strings.TrimSpace(c.Pool) != "" ||
		strings.TrimSpace(c.NodeID) != "" ||
		strings.TrimSpace(c.Token) != "" ||
		c.HeartbeatInterval != 0
}

func (c CoordinationConfig) IsConfigured() bool {
	return strings.TrimSpace(c.Endpoint) != "" &&
		strings.TrimSpace(c.Pool) != "" &&
		strings.TrimSpace(c.NodeID) != "" &&
		strings.TrimSpace(c.Token) != ""
}

func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if removed := detectRemovedConfigPaths(raw); len(removed) > 0 {
		return Config{}, fmt.Errorf("removed config keys are not supported: %s", strings.Join(removed, ", "))
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

func detectRemovedConfigPaths(raw []byte) []string {
	var root map[string]interface{}
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil
	}

	removed := make([]string, 0)
	collectRemovedTree(root, []string{"measurement", "schedule", "headroom"}, &removed)
	collectRemovedTree(root, []string{"measurement", "protocols", "tcp", "target_bandwidth"}, &removed)
	collectRemovedTree(root, []string{"measurement", "protocols", "udp", "target_bandwidth"}, &removed)
	collectSpecificRemovedKeys(root, []string{"measurement", "protocols", "tcp"}, []string{"alternate", "chunk_size", "sample_size", "sample_count"}, &removed)
	collectSpecificRemovedKeys(root, []string{"measurement", "protocols", "udp"}, []string{"chunk_size", "sample_size", "sample_count"}, &removed)
	collectRemovedTree(root, []string{"scoring", "reference", "tcp", "bandwidth"}, &removed)
	collectRemovedTree(root, []string{"scoring", "reference", "udp", "bandwidth"}, &removed)
	collectRemovedTree(root, []string{"scoring", "utilization_penalty"}, &removed)
	collectSpecificRemovedKeys(root, []string{"scoring", "weights", "tcp"}, []string{"bandwidth_upload", "bandwidth_download"}, &removed)
	collectSpecificRemovedKeys(root, []string{"scoring", "weights", "udp"}, []string{"bandwidth_upload", "bandwidth_download"}, &removed)

	if len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)
	return removed
}

func collectRemovedTree(root map[string]interface{}, path []string, removed *[]string) {
	node, ok := lookupPath(root, path)
	if !ok {
		return
	}
	collectLeafPaths(node, strings.Join(path, "."), removed)
}

func collectSpecificRemovedKeys(root map[string]interface{}, base []string, keys []string, removed *[]string) {
	node, ok := lookupPath(root, base)
	if !ok {
		return
	}
	m, ok := asMap(node)
	if !ok {
		return
	}
	basePath := strings.Join(base, ".")
	for _, key := range keys {
		if _, exists := m[key]; exists {
			*removed = append(*removed, basePath+"."+key)
		}
	}
}

func collectLeafPaths(node interface{}, prefix string, removed *[]string) {
	m, ok := asMap(node)
	if !ok || len(m) == 0 {
		*removed = append(*removed, prefix)
		return
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		collectLeafPaths(m[key], prefix+"."+key, removed)
	}
}

func lookupPath(root map[string]interface{}, path []string) (interface{}, bool) {
	var current interface{} = root
	for _, seg := range path {
		m, ok := asMap(current)
		if !ok {
			return nil, false
		}
		next, exists := m[seg]
		if !exists {
			return nil, false
		}
		current = next
	}
	return current, true
}

func asMap(v interface{}) (map[string]interface{}, bool) {
	switch t := v.(type) {
	case map[string]interface{}:
		return t, true
	case map[interface{}]interface{}:
		converted := make(map[string]interface{}, len(t))
		for k, v := range t {
			key, ok := k.(string)
			if !ok {
				continue
			}
			converted[key] = v
		}
		return converted, true
	default:
		return nil, false
	}
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
	if strings.TrimSpace(c.Measurement.Security.Mode) == "" {
		c.Measurement.Security.Mode = defaultMeasurementSecurityMode
	}

	setProtocolDefaults(&c.Measurement.Protocols.TCP, true)
	setProtocolDefaults(&c.Measurement.Protocols.UDP, false)

	if c.Scoring.Smoothing.Alpha == 0 {
		c.Scoring.Smoothing.Alpha = defaultEMAAlpha
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
			RTT:            defaultWeightTCPRTT,
			Jitter:         defaultWeightTCPJitter,
			RetransmitRate: defaultWeightTCPRetrans,
		}
	}
	if weightsUDPZero(c.Scoring.Weights.UDP) {
		c.Scoring.Weights.UDP = WeightsUDPConfig{
			RTT:      defaultWeightUDPRTT,
			Jitter:   defaultWeightUDPJitter,
			LossRate: defaultWeightUDPLoss,
		}
	}
	if c.Scoring.Weights.ProtocolBlend.TCPWeight == 0 && c.Scoring.Weights.ProtocolBlend.UDPWeight == 0 {
		c.Scoring.Weights.ProtocolBlend.TCPWeight = defaultProtocolWeightTCP
		c.Scoring.Weights.ProtocolBlend.UDPWeight = defaultProtocolWeightUDP
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
	if c.Coordination.HasAnyField() && c.Coordination.HeartbeatInterval == 0 {
		c.Coordination.HeartbeatInterval = Duration(defaultCoordinationHeartbeat)
	}
	if c.Logging.Level == "" {
		c.Logging.Level = defaultLoggingLevel
	}
	if c.Logging.Format == "" {
		c.Logging.Format = defaultLoggingFormat
	}
	if c.GeoIP.RefreshInterval == 0 {
		c.GeoIP.RefreshInterval = Duration(defaultGeoIPRefreshInterval)
	}
	if c.IPLog.GeoQueueSize == 0 {
		c.IPLog.GeoQueueSize = defaultIPLogGeoQueueSize
	}
	if c.IPLog.WriteQueueSize == 0 {
		c.IPLog.WriteQueueSize = defaultIPLogWriteQueueSize
	}
	if c.IPLog.BatchSize == 0 {
		c.IPLog.BatchSize = defaultIPLogBatchSize
	}
	if c.IPLog.FlushInterval == 0 {
		c.IPLog.FlushInterval = Duration(defaultIPLogFlushInterval)
	}
	if c.IPLog.PruneInterval == 0 {
		c.IPLog.PruneInterval = Duration(defaultIPLogPruneInterval)
	}
	if c.Firewall.Default == "" {
		c.Firewall.Default = "allow"
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
	return cfg.RTT == 0 &&
		cfg.Jitter == 0 &&
		cfg.RetransmitRate == 0
}

func weightsUDPZero(cfg WeightsUDPConfig) bool {
	return cfg.RTT == 0 &&
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
	if cfg.PingCount == 0 {
		cfg.PingCount = defaultMeasurementPingCount
	}
	if isTCP {
		if cfg.RetransmitBytes == "" {
			cfg.RetransmitBytes = defaultMeasurementRetransmit
		}
	} else {
		if cfg.LossPackets == 0 {
			cfg.LossPackets = defaultMeasurementLossPackets
		}
		if cfg.PacketSize == "" {
			cfg.PacketSize = defaultMeasurementPacketSize
		}
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

	c.Control.AuthToken = strings.TrimSpace(c.Control.AuthToken)
	if c.Control.AuthToken == "" {
		return errors.New("control.auth_token must not be empty")
	}
	if err := validateAuthTokenField(c.Control.AuthToken, "control.auth_token"); err != nil {
		return err
	}
	if c.Control.BindPort <= 0 || c.Control.BindPort > 65535 {
		return errors.New("control.bind_port must be in 1..65535")
	}

	c.Coordination.Endpoint = strings.TrimSpace(c.Coordination.Endpoint)
	c.Coordination.Pool = strings.TrimSpace(c.Coordination.Pool)
	c.Coordination.NodeID = strings.TrimSpace(c.Coordination.NodeID)
	c.Coordination.Token = strings.TrimSpace(c.Coordination.Token)
	if c.Coordination.HasAnyField() {
		if c.Coordination.Endpoint == "" || c.Coordination.Pool == "" || c.Coordination.NodeID == "" || c.Coordination.Token == "" {
			return errors.New("coordination.endpoint, pool, node_id, and token must be set together")
		}
		if c.Coordination.HeartbeatInterval.Duration() <= 0 {
			return errors.New("coordination.heartbeat_interval must be > 0")
		}
		if err := validateAuthTokenField(c.Coordination.Token, "coordination.token"); err != nil {
			return err
		}
	}

	c.Logging.Level = strings.ToLower(strings.TrimSpace(c.Logging.Level))
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("logging.level must be debug, info, warn, or error")
	}

	c.Logging.Format = strings.ToLower(strings.TrimSpace(c.Logging.Format))
	switch c.Logging.Format {
	case "text", "json":
	default:
		return errors.New("logging.format must be text or json")
	}

	if c.Reachability.ProbeInterval.Duration() <= 0 {
		return errors.New("reachability.probe_interval must be > 0")
	}
	if c.Reachability.ProbeInterval.Duration() < 100*time.Millisecond {
		return errors.New("reachability.probe_interval must be >= 100ms")
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
	if err := validateMeasurementSecurity(c.Measurement.Security); err != nil {
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

	c.GeoIP.ASNDBURL = strings.TrimSpace(c.GeoIP.ASNDBURL)
	c.GeoIP.ASNDBPath = strings.TrimSpace(c.GeoIP.ASNDBPath)
	c.GeoIP.CountryDBURL = strings.TrimSpace(c.GeoIP.CountryDBURL)
	c.GeoIP.CountryDBPath = strings.TrimSpace(c.GeoIP.CountryDBPath)
	if c.GeoIP.Enabled {
		if c.GeoIP.RefreshInterval.Duration() <= 0 {
			return errors.New("geoip.refresh_interval must be > 0")
		}
		if !geoDBConfigured(c.GeoIP.ASNDBURL, c.GeoIP.ASNDBPath) && !geoDBConfigured(c.GeoIP.CountryDBURL, c.GeoIP.CountryDBPath) {
			return errors.New("geoip.enabled requires at least one of asn_db_url/asn_db_path or country_db_url/country_db_path")
		}
		if err := validateGeoDBConfig("geoip.asn_db", c.GeoIP.ASNDBURL, c.GeoIP.ASNDBPath); err != nil {
			return err
		}
		if err := validateGeoDBConfig("geoip.country_db", c.GeoIP.CountryDBURL, c.GeoIP.CountryDBPath); err != nil {
			return err
		}
	}

	c.IPLog.DBPath = strings.TrimSpace(c.IPLog.DBPath)
	if c.IPLog.Enabled {
		if c.IPLog.DBPath == "" {
			return errors.New("ip_log.db_path is required when ip_log.enabled is true")
		}
		if c.IPLog.GeoQueueSize <= 0 {
			return errors.New("ip_log.geo_queue_size must be > 0")
		}
		if c.IPLog.WriteQueueSize <= 0 {
			return errors.New("ip_log.write_queue_size must be > 0")
		}
		if c.IPLog.BatchSize <= 0 {
			return errors.New("ip_log.batch_size must be > 0")
		}
		if c.IPLog.FlushInterval.Duration() <= 0 {
			return errors.New("ip_log.flush_interval must be > 0")
		}
		if c.IPLog.Retention.Duration() < 0 {
			return errors.New("ip_log.retention must be >= 0")
		}
		if c.IPLog.Retention.Duration() > 0 && c.IPLog.PruneInterval.Duration() <= 0 {
			return errors.New("ip_log.prune_interval must be > 0 when retention is enabled")
		}
	}

	c.Firewall.Default = strings.ToLower(strings.TrimSpace(c.Firewall.Default))
	if c.Firewall.Enabled {
		switch c.Firewall.Default {
		case "allow", "deny":
		default:
			return errors.New("firewall.default must be allow or deny")
		}
		for i := range c.Firewall.Rules {
			rule := &c.Firewall.Rules[i]
			rule.Action = strings.ToLower(strings.TrimSpace(rule.Action))
			switch rule.Action {
			case "allow", "deny":
			default:
				return fmt.Errorf("firewall.rules[%d].action must be allow or deny", i)
			}
			matcherCount := 0
			if strings.TrimSpace(rule.CIDR) != "" {
				rule.CIDR = strings.TrimSpace(rule.CIDR)
				matcherCount++
			}
			if rule.ASN != 0 {
				matcherCount++
			}
			rule.Country = strings.ToUpper(strings.TrimSpace(rule.Country))
			if rule.Country != "" {
				matcherCount++
			}
			if matcherCount != 1 {
				return fmt.Errorf("firewall.rules[%d] must specify exactly one matcher", i)
			}
		}
	}

	return nil
}

func geoDBConfigured(url, path string) bool {
	return strings.TrimSpace(url) != "" || strings.TrimSpace(path) != ""
}

func validateGeoDBConfig(prefix, url, path string) error {
	url = strings.TrimSpace(url)
	path = strings.TrimSpace(path)
	if url == "" && path == "" {
		return nil
	}
	if url == "" || path == "" {
		return fmt.Errorf("%s requires both url and path", prefix)
	}
	return nil
}

func validateAuthToken(token string) error {
	return validateAuthTokenField(token, "control.auth_token")
}

func validateAuthTokenField(token, field string) error {
	if strings.EqualFold(token, "change-me") {
		return fmt.Errorf("%s must not use the default placeholder value", field)
	}
	if len(token) < 16 {
		return fmt.Errorf("%s must be at least 16 characters long", field)
	}
	return nil
}

func validateMeasurementSecurity(cfg MeasurementSecurityConfig) error {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "", defaultMeasurementSecurityMode, "tls", "mtls":
	default:
		return fmt.Errorf("measurement.security.mode must be one of off, tls, or mtls")
	}
	if (cfg.ClientCertFile == "") != (cfg.ClientKeyFile == "") {
		return errors.New("measurement.security.client_cert_file and client_key_file must be set together")
	}
	if mode == "mtls" && (cfg.ClientCertFile == "" || cfg.ClientKeyFile == "") {
		return errors.New("measurement.security.mode mtls requires client_cert_file and client_key_file")
	}
	return nil
}

func validateProtocolConfig(proto string, cfg MeasurementProtocolConfig, enabled bool) error {
	if !enabled {
		return nil
	}
	if cfg.PingCount <= 0 {
		return fmt.Errorf("measurement.protocols.%s.ping_count must be > 0", proto)
	}
	switch proto {
	case "tcp":
		retransmitBytes, err := ParseSize(cfg.RetransmitBytes)
		if err != nil {
			return fmt.Errorf("measurement.protocols.%s.retransmit_bytes: %w", proto, err)
		}
		if retransmitBytes == 0 {
			return fmt.Errorf("measurement.protocols.%s.retransmit_bytes must be > 0", proto)
		}
	case "udp":
		if cfg.LossPackets <= 0 {
			return fmt.Errorf("measurement.protocols.%s.loss_packets must be > 0", proto)
		}
		packetSize, err := ParseSize(cfg.PacketSize)
		if err != nil {
			return fmt.Errorf("measurement.protocols.%s.packet_size: %w", proto, err)
		}
		if packetSize == 0 {
			return fmt.Errorf("measurement.protocols.%s.packet_size must be > 0", proto)
		}
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
	sum := cfg.RTT + cfg.Jitter + cfg.RetransmitRate
	if sum <= 0 {
		return errors.New("scoring.weights.tcp must sum to > 0")
	}
	if diff := sum - 1; diff > 0.001 || diff < -0.001 {
		cfg.RTT /= sum
		cfg.Jitter /= sum
		cfg.RetransmitRate /= sum
	}
	return nil
}

func normalizeUDPWeights(cfg *WeightsUDPConfig) error {
	sum := cfg.RTT + cfg.Jitter + cfg.LossRate
	if sum <= 0 {
		return errors.New("scoring.weights.udp must sum to > 0")
	}
	if diff := sum - 1; diff > 0.001 || diff < -0.001 {
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

// DefaultScoringConfig returns a ScoringConfig populated with the built-in defaults.
// This is primarily used by tests to construct a valid scoring configuration without parsing YAML.
func DefaultScoringConfig() ScoringConfig {
	return ScoringConfig{
		Smoothing: ScoringSmoothingConfig{
			Alpha: defaultEMAAlpha,
		},
		Reference: ScoringReferenceConfig{
			TCP: ProtocolReferenceConfig{
				Latency: ReferenceLatencyConfig{
					RTT:    defaultRefRTTMs,
					Jitter: defaultRefJitterMs,
				},
				RetransmitRate: defaultRefRetransRate,
			},
			UDP: ProtocolReferenceConfig{
				Latency: ReferenceLatencyConfig{
					RTT:    defaultRefRTTMs,
					Jitter: defaultRefJitterMs,
				},
				LossRate: defaultRefLossRate,
			},
		},
		Weights: ScoringWeightsConfig{
			TCP: WeightsTCPConfig{
				RTT:            defaultWeightTCPRTT,
				Jitter:         defaultWeightTCPJitter,
				RetransmitRate: defaultWeightTCPRetrans,
			},
			UDP: WeightsUDPConfig{
				RTT:      defaultWeightUDPRTT,
				Jitter:   defaultWeightUDPJitter,
				LossRate: defaultWeightUDPLoss,
			},
			ProtocolBlend: ProtocolBlendConfig{
				TCPWeight: defaultProtocolWeightTCP,
				UDPWeight: defaultProtocolWeightUDP,
			},
		},
		BiasTransform: BiasTransformConfig{
			Kappa: defaultBiasKappa,
		},
	}
}
