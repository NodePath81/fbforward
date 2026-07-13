package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	defaultMeasurementScheduleMinInterval = 15 * time.Minute
	defaultMeasurementScheduleMaxInterval = 45 * time.Minute
	defaultMeasurementScheduleUpstreamGap = 5 * time.Second

	defaultMeasurementPingCount    = 5
	defaultMeasurementPerSample    = 10 * time.Second
	defaultMeasurementPerCycle     = 30 * time.Second
	defaultMeasurementTCPEnabled   = true
	defaultMeasurementUDPEnabled   = true
	defaultMeasurementSecurityMode = "off"

	defaultHealthAlpha    = 0.25
	defaultHealthFailure  = 3
	defaultHealthRecovery = 2
	defaultHealthStale    = 60 * time.Second

	defaultForwardingMaxTCPConnections = 50
	defaultForwardingMaxUDPMappings    = 500
	defaultForwardingTCPIdle           = 60 * time.Second
	defaultForwardingUDPIdle           = 30 * time.Second

	defaultControlAddr           = "127.0.0.1"
	defaultControlPort           = 8080
	defaultControlMetricsEnabled = true
	defaultNotifyStartupGrace    = 5 * time.Minute
	defaultNotifyUnusableDelay   = 30 * time.Second
	defaultNotifyInterval        = 30 * time.Minute
	defaultLoggingLevel          = "info"
	defaultLoggingFormat         = "text"
	defaultGeoIPRefreshInterval  = 24 * time.Hour
	defaultIPLogGeoQueueSize     = 4096
	defaultIPLogWriteQueueSize   = 4096
	defaultIPLogBatchSize        = 100
	defaultIPLogFlushInterval    = 5 * time.Second
	defaultIPLogPruneInterval    = 1 * time.Hour
	defaultFlowContextMaxTTL     = 24 * time.Hour

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
	Hostname           string            `yaml:"hostname"`
	Listeners          []ListenerSpec    `yaml:"listeners"`
	Routes             []RouteConfig     `yaml:"routes"`
	Forwarding         ForwardingConfig  `yaml:"forwarding"`
	Upstreams          []UpstreamConfig  `yaml:"upstreams"`
	DNS                DNSConfig         `yaml:"dns"`
	Measurement        MeasurementConfig `yaml:"measurement"`
	Health             HealthConfig      `yaml:"health"`
	Control            ControlConfig     `yaml:"control"`
	Notify             NotifyConfig      `yaml:"notify"`
	Logging            LoggingConfig     `yaml:"logging"`
	Shaping            ShapingConfig     `yaml:"shaping"`
	GeoIP              GeoIPConfig       `yaml:"geoip"`
	IPLog              IPLogConfig       `yaml:"ip_log"`
	FlowContext        FlowContextConfig `yaml:"flow_context"`
	Firewall           FirewallConfig    `yaml:"firewall"`
	Warnings           []string          `yaml:"-"`
	topologyMode       string            `yaml:"-"`
	topologyNormalized bool              `yaml:"-"`
	configLoaded       bool              `yaml:"-"`
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
	Name     string              `yaml:"name,omitempty"`
	BindAddr string              `yaml:"bind_addr"`
	BindPort int                 `yaml:"bind_port"`
	Protocol string              `yaml:"protocol"`
	Route    string              `yaml:"route,omitempty"`
	Shaping  *ShapingLimitConfig `yaml:"shaping"`
}

// ListenerSpec is the explicit listener form used by the current topology
// configuration. It is converted to ListenerConfig after parsing so the
// forwarding data plane continues to use a normalized address and port.
type ListenerSpec struct {
	Name     string              `yaml:"name"`
	Bind     string              `yaml:"bind"`
	Protocol string              `yaml:"protocol"`
	Route    string              `yaml:"route"`
	Shaping  *ShapingLimitConfig `yaml:"shaping"`
}

type RouteConfig struct {
	Name      string   `yaml:"name"`
	Strategy  string   `yaml:"strategy"`
	Upstreams []string `yaml:"upstreams"`
}

type UpstreamConfig struct {
	Tag         string                    `yaml:"tag"`
	Destination DestinationConfig         `yaml:"destination"`
	Measurement UpstreamMeasurementConfig `yaml:"measurement"`
	Priority    float64                   `yaml:"priority"`
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

type MeasurementConfig struct {
	Schedule  MeasurementScheduleConfig  `yaml:"schedule"`
	Protocols MeasurementProtocolsConfig `yaml:"protocols"`
	Security  MeasurementSecurityConfig  `yaml:"security"`
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

type MeasurementProtocolsConfig struct {
	TCP MeasurementProtocolConfig `yaml:"tcp"`
	UDP MeasurementProtocolConfig `yaml:"udp"`
}

type MeasurementProtocolConfig struct {
	Enabled   *bool                    `yaml:"enabled"`
	PingCount int                      `yaml:"ping_count"`
	Timeout   MeasurementTimeoutConfig `yaml:"timeout"`
}

type MeasurementTimeoutConfig struct {
	PerSample Duration `yaml:"per_sample"`
	PerCycle  Duration `yaml:"per_cycle"`
}

type HealthConfig struct {
	RTTEWMAAlpha      float64  `yaml:"rtt_ewma_alpha"`
	FailureThreshold  int      `yaml:"failure_threshold"`
	RecoveryThreshold int      `yaml:"recovery_threshold"`
	StaleThreshold    Duration `yaml:"stale_threshold"`
}

type ControlConfig struct {
	BindAddr  string               `yaml:"bind_addr"`
	BindPort  int                  `yaml:"bind_port"`
	AuthToken string               `yaml:"auth_token"`
	Metrics   ControlMetricsConfig `yaml:"metrics"`
}

type ControlMetricsConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type NotifyConfig struct {
	Enabled            bool     `yaml:"enabled"`
	Endpoint           string   `yaml:"endpoint"`
	KeyID              string   `yaml:"key_id"`
	Token              string   `yaml:"token"`
	SourceInstance     string   `yaml:"source_instance"`
	StartupGracePeriod Duration `yaml:"startup_grace_period"`
	UnusableInterval   Duration `yaml:"unusable_interval"`
	NotifyInterval     Duration `yaml:"notify_interval"`
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
	LogRejections  *bool    `yaml:"log_rejections"`
	DBPath         string   `yaml:"db_path"`
	Retention      Duration `yaml:"retention"`
	GeoQueueSize   int      `yaml:"geo_queue_size"`
	WriteQueueSize int      `yaml:"write_queue_size"`
	BatchSize      int      `yaml:"batch_size"`
	FlushInterval  Duration `yaml:"flush_interval"`
	PruneInterval  Duration `yaml:"prune_interval"`
}

type FlowContextConfig struct {
	Enabled    bool                  `yaml:"enabled"`
	MaxTTL     Duration              `yaml:"max_ttl"`
	Identities []FlowContextIdentity `yaml:"identities"`
}

type FlowContextIdentity struct {
	ID         string   `yaml:"id"`
	Token      string   `yaml:"token"`
	Routes     []string `yaml:"routes"`
	Upstreams  []string `yaml:"upstreams"`
	Namespaces []string `yaml:"namespaces"`
}

type FirewallConfig struct {
	Enabled           bool   `yaml:"enabled"`
	PolicyFile        string `yaml:"policy_file"`
	FailOnInitialLoad *bool  `yaml:"fail_on_initial_load"`
	// Default and Rules are retained for one migration period. New
	// configurations should use PolicyFile instead.
	Default string         `yaml:"default"`
	Rules   []FirewallRule `yaml:"rules"`
}

func (c FirewallConfig) ShouldFailOnInitialLoad() bool {
	if c.FailOnInitialLoad == nil {
		return true
	}
	return *c.FailOnInitialLoad
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

func (m ControlMetricsConfig) IsEnabled() bool {
	return util.BoolValue(m.Enabled, defaultControlMetricsEnabled)
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
	cfg.configLoaded = true
	cfg.topologyMode = "legacy"
	if hasModernTopology(raw) {
		cfg.topologyMode = "modern"
	}
	if err := decodeConfig(raw, &cfg, true); err != nil {
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
	collectSpecificRemovedKeys(root, []string{"notify"}, []string{"token_env"}, &removed)

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

	if c.Measurement.Schedule.Interval.Min == 0 {
		c.Measurement.Schedule.Interval.Min = Duration(defaultMeasurementScheduleMinInterval)
	}
	if c.Measurement.Schedule.Interval.Max == 0 {
		c.Measurement.Schedule.Interval.Max = Duration(defaultMeasurementScheduleMaxInterval)
	}
	if c.Measurement.Schedule.UpstreamGap == 0 {
		c.Measurement.Schedule.UpstreamGap = Duration(defaultMeasurementScheduleUpstreamGap)
	}

	if strings.TrimSpace(c.Measurement.Security.Mode) == "" {
		c.Measurement.Security.Mode = defaultMeasurementSecurityMode
	}

	setProtocolDefaults(&c.Measurement.Protocols.TCP, true)
	setProtocolDefaults(&c.Measurement.Protocols.UDP, false)

	if c.Health.RTTEWMAAlpha == 0 {
		c.Health.RTTEWMAAlpha = defaultHealthAlpha
	}
	if c.Health.FailureThreshold == 0 {
		c.Health.FailureThreshold = defaultHealthFailure
	}
	if c.Health.RecoveryThreshold == 0 {
		c.Health.RecoveryThreshold = defaultHealthRecovery
	}
	if c.Health.StaleThreshold == 0 {
		c.Health.StaleThreshold = Duration(defaultHealthStale)
	}

	if c.Control.BindAddr == "" {
		c.Control.BindAddr = defaultControlAddr
	}
	if c.Control.BindPort == 0 {
		c.Control.BindPort = defaultControlPort
	}
	if c.Control.Metrics.Enabled == nil {
		enabled := defaultControlMetricsEnabled
		c.Control.Metrics.Enabled = &enabled
	}
	if c.Notify.StartupGracePeriod == 0 {
		c.Notify.StartupGracePeriod = Duration(defaultNotifyStartupGrace)
	}
	if c.Notify.UnusableInterval == 0 {
		c.Notify.UnusableInterval = Duration(defaultNotifyUnusableDelay)
	}
	if c.Notify.NotifyInterval == 0 {
		c.Notify.NotifyInterval = Duration(defaultNotifyInterval)
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
	if c.FlowContext.MaxTTL == 0 {
		c.FlowContext.MaxTTL = Duration(defaultFlowContextMaxTTL)
	}
	if c.IPLog.LogRejections == nil && c.IPLog.Enabled {
		enabled := true
		c.IPLog.LogRejections = &enabled
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
	if cfg.Timeout.PerSample == 0 {
		cfg.Timeout.PerSample = Duration(defaultMeasurementPerSample)
	}
	if cfg.Timeout.PerCycle == 0 {
		cfg.Timeout.PerCycle = Duration(defaultMeasurementPerCycle)
	}
}

func (c *Config) validate() error {
	c.Warnings = nil
	if err := c.normalizeTopology(); err != nil {
		return err
	}

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
	seenListenerNames := make(map[string]struct{}, len(c.Forwarding.Listeners))
	for i := range c.Forwarding.Listeners {
		ln := &c.Forwarding.Listeners[i]
		ln.Name = strings.TrimSpace(ln.Name)
		if ln.Name == "" && c.topologyMode == "modern" {
			return fmt.Errorf("forwarding.listeners[%d].name must not be empty", i)
		}
		if ln.Name != "" {
			if _, ok := seenListenerNames[ln.Name]; ok {
				return fmt.Errorf("duplicate listener name: %s", ln.Name)
			}
			seenListenerNames[ln.Name] = struct{}{}
		}
		ln.Route = strings.TrimSpace(ln.Route)
		if ln.Route == "" {
			if c.topologyMode == "modern" {
				return fmt.Errorf("listeners[%d].route must not be empty", i)
			}
			ln.Route = fmt.Sprintf("%s:%d", ln.BindAddr, ln.BindPort)
		}
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

	seenRoutes := make(map[string]struct{}, len(c.Routes))
	upstreamTags := make(map[string]struct{}, len(c.Upstreams))
	for _, upstream := range c.Upstreams {
		upstreamTags[upstream.Tag] = struct{}{}
	}
	for i := range c.Routes {
		route := &c.Routes[i]
		route.Name = strings.TrimSpace(route.Name)
		route.Strategy = strings.ToLower(strings.TrimSpace(route.Strategy))
		if route.Name == "" {
			return fmt.Errorf("routes[%d].name must not be empty", i)
		}
		if _, ok := seenRoutes[route.Name]; ok {
			return fmt.Errorf("duplicate route name: %s", route.Name)
		}
		seenRoutes[route.Name] = struct{}{}
		switch route.Strategy {
		case "static":
			if len(route.Upstreams) != 1 {
				return fmt.Errorf("routes[%s].upstreams must contain exactly one upstream for static strategy", route.Name)
			}
		case "adaptive":
			if len(route.Upstreams) < 2 {
				return fmt.Errorf("routes[%s].upstreams must contain at least two upstreams for adaptive strategy", route.Name)
			}
		default:
			return fmt.Errorf("routes[%s].strategy must be static or adaptive", route.Name)
		}
		seenRouteUpstreams := make(map[string]struct{}, len(route.Upstreams))
		for j, tag := range route.Upstreams {
			tag = strings.TrimSpace(tag)
			route.Upstreams[j] = tag
			if tag == "" {
				return fmt.Errorf("routes[%s].upstreams must not contain empty tags", route.Name)
			}
			if _, ok := seenRouteUpstreams[tag]; ok {
				return fmt.Errorf("duplicate upstream %s in route %s", tag, route.Name)
			}
			seenRouteUpstreams[tag] = struct{}{}
			if _, ok := upstreamTags[tag]; !ok {
				return fmt.Errorf("routes[%s].upstreams references unknown upstream %s", route.Name, tag)
			}
		}
	}
	for i := range c.Forwarding.Listeners {
		listener := &c.Forwarding.Listeners[i]
		if _, ok := seenRoutes[listener.Route]; !ok {
			return fmt.Errorf("listener %s references unknown route %s", listener.Name, listener.Route)
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

	c.Notify.Endpoint = strings.TrimSpace(c.Notify.Endpoint)
	c.Notify.KeyID = strings.TrimSpace(c.Notify.KeyID)
	c.Notify.Token = strings.TrimSpace(c.Notify.Token)
	c.Notify.SourceInstance = strings.TrimSpace(c.Notify.SourceInstance)
	if c.Notify.Enabled {
		if c.Notify.StartupGracePeriod.Duration() <= 0 {
			return errors.New("notify.startup_grace_period must be > 0")
		}
		if c.Notify.UnusableInterval.Duration() <= 0 {
			return errors.New("notify.unusable_interval must be > 0")
		}
		if c.Notify.NotifyInterval.Duration() <= 0 {
			return errors.New("notify.notify_interval must be > 0")
		}
		if c.Notify.Endpoint == "" {
			return errors.New("notify.endpoint is required when notify.enabled is true")
		}
		parsedNotifyEndpoint, err := url.Parse(c.Notify.Endpoint)
		if err != nil || parsedNotifyEndpoint.Host == "" {
			return errors.New("notify.endpoint must be a valid URL")
		}
		if parsedNotifyEndpoint.Scheme != "http" && parsedNotifyEndpoint.Scheme != "https" {
			return errors.New("notify.endpoint must use http or https")
		}
		if err := validateNotifyIdentifier(c.Notify.KeyID, "notify.key_id"); err != nil {
			return err
		}
		if c.Notify.Token == "" {
			return errors.New("notify.token is required when notify.enabled is true")
		}
		if err := validateAuthTokenField(c.Notify.Token, "notify.token"); err != nil {
			return err
		}
		if c.Notify.SourceInstance == "" {
			c.Notify.SourceInstance = strings.TrimSpace(c.Hostname)
			if c.Notify.SourceInstance == "" {
				hostname, err := os.Hostname()
				if err != nil {
					return fmt.Errorf("resolve notify source instance: %w", err)
				}
				c.Notify.SourceInstance = strings.TrimSpace(hostname)
			}
		}
		if err := validateNotifyIdentifier(c.Notify.SourceInstance, "notify.source_instance"); err != nil {
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

	if c.Health.RTTEWMAAlpha <= 0 || c.Health.RTTEWMAAlpha > 1 {
		return errors.New("health.rtt_ewma_alpha must be in (0,1]")
	}
	if c.Health.FailureThreshold <= 0 {
		return errors.New("health.failure_threshold must be > 0")
	}
	if c.Health.RecoveryThreshold <= 0 {
		return errors.New("health.recovery_threshold must be > 0")
	}
	if c.Health.StaleThreshold.Duration() <= 0 {
		return errors.New("health.stale_threshold must be > 0")
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

	if c.FlowContext.Enabled {
		if !c.IPLog.Enabled {
			return errors.New("flow_context.enabled requires ip_log.enabled")
		}
		if len(c.FlowContext.Identities) == 0 {
			return errors.New("flow_context.identities must not be empty")
		}
		if c.FlowContext.MaxTTL.Duration() <= 0 {
			return errors.New("flow_context.max_ttl must be > 0")
		}
		seenIDs := make(map[string]struct{}, len(c.FlowContext.Identities))
		seenTokens := make(map[string]struct{}, len(c.FlowContext.Identities))
		for i := range c.FlowContext.Identities {
			identity := &c.FlowContext.Identities[i]
			identity.ID = strings.TrimSpace(identity.ID)
			identity.Token = strings.TrimSpace(identity.Token)
			if identity.ID == "" {
				return fmt.Errorf("flow_context.identities[%d].id must not be empty", i)
			}
			if _, exists := seenIDs[identity.ID]; exists {
				return fmt.Errorf("duplicate flow_context identity: %s", identity.ID)
			}
			seenIDs[identity.ID] = struct{}{}
			if identity.Token == "" {
				return fmt.Errorf("flow_context.identities[%d].token must not be empty", i)
			}
			if err := validateAuthTokenField(identity.Token, fmt.Sprintf("flow_context.identities[%d].token", i)); err != nil {
				return err
			}
			if identity.Token == c.Control.AuthToken {
				return fmt.Errorf("flow_context.identities[%d].token must differ from control.auth_token", i)
			}
			if _, exists := seenTokens[identity.Token]; exists {
				return fmt.Errorf("duplicate flow_context identity token")
			}
			seenTokens[identity.Token] = struct{}{}
			if err := normalizeFlowContextScope(&identity.Routes, "routes", i); err != nil {
				return err
			}
			if err := normalizeFlowContextScope(&identity.Upstreams, "upstreams", i); err != nil {
				return err
			}
			if err := normalizeFlowContextScope(&identity.Namespaces, "namespaces", i); err != nil {
				return err
			}
		}
	}

	c.Firewall.PolicyFile = strings.TrimSpace(c.Firewall.PolicyFile)
	if c.Firewall.PolicyFile != "" && len(c.Firewall.Rules) > 0 {
		return errors.New("firewall.policy_file cannot be combined with legacy firewall.rules")
	}
	if c.Firewall.PolicyFile == "" && c.Firewall.Enabled {
		c.Warnings = append(c.Warnings, "firewall.default/rules are deprecated; use firewall.policy_file")
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

func validateNotifyIdentifier(value, field string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if len(trimmed) > 128 {
		return fmt.Errorf("%s must be at most 128 characters", field)
	}
	for _, ch := range trimmed {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '.', ch == '_', ch == ':', ch == '-':
		default:
			return fmt.Errorf("%s contains unsupported characters", field)
		}
	}
	return nil
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

func normalizeFlowContextScope(values *[]string, field string, index int) error {
	if values == nil || len(*values) == 0 {
		return fmt.Errorf("flow_context.identities[%d].%s must not be empty", index, field)
	}
	seen := make(map[string]struct{}, len(*values))
	for i, value := range *values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("flow_context.identities[%d].%s[%d] must not be empty", index, field, i)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate flow_context identity %s: %s", field, value)
		}
		seen[value] = struct{}{}
		(*values)[i] = value
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
	if cfg.Timeout.PerSample.Duration() <= 0 {
		return fmt.Errorf("measurement.protocols.%s.timeout.per_sample must be > 0", proto)
	}
	if cfg.Timeout.PerCycle.Duration() <= 0 {
		return fmt.Errorf("measurement.protocols.%s.timeout.per_cycle must be > 0", proto)
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
