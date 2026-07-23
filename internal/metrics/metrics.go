package metrics

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/upstream"
)

// UpstreamMetrics is the health snapshot rendered for operations.
type UpstreamMetrics struct {
	HealthState string
	RTTMs       float64
	LastSuccess time.Time
}

type trafficCounters struct {
	tcpUp   atomic.Uint64
	tcpDown atomic.Uint64
	udpUp   atomic.Uint64
	udpDown atomic.Uint64
}

type upstreamState struct {
	metrics UpstreamMetrics
	traffic trafficCounters
}

type flowEventKey struct {
	protocol string
	event    string
	reason   string
}

type probeKey struct {
	upstream string
	protocol string
	result   string
}

type auditCounters struct {
	received uint64
	written  uint64
	dropped  uint64
}

// Metrics stores bounded operational state and renders the Prometheus text
// exposition format without a background sampler or an external dependency.
type Metrics struct {
	mu sync.RWMutex

	upstreams map[string]*upstreamState
	routes    map[string]string

	tcpActive  atomic.Int64
	udpActive  atomic.Int64
	flowEvents map[flowEventKey]uint64
	probes     map[probeKey]uint64

	audit            auditCounters
	rateLimitDrops   uint64
	onlineRules      int
	onlineRuleErrors uint64
	webhook          map[string]uint64
	firewallDenied   map[string]uint64

	startedAt time.Time
}

func NewMetrics(tags []string) *Metrics {
	upstreams := make(map[string]*upstreamState, len(tags))
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		upstreams[tag] = &upstreamState{metrics: UpstreamMetrics{
			HealthState: string(upstream.HealthUnknown),
		}}
	}
	return &Metrics{
		upstreams:      upstreams,
		routes:         make(map[string]string),
		flowEvents:     make(map[flowEventKey]uint64),
		probes:         make(map[probeKey]uint64),
		webhook:        make(map[string]uint64),
		firewallDenied: make(map[string]uint64),
		startedAt:      time.Now(),
	}
}

func (m *Metrics) SetUpstreamMetrics(tag string, stats upstream.UpstreamStats) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.upstreams[tag]
	if !ok || state == nil {
		return
	}
	state.metrics = UpstreamMetrics{
		HealthState: string(stats.HealthState),
		RTTMs:       stats.RTTMs,
		LastSuccess: stats.LastReachable,
	}
}

func (m *Metrics) RecordProbe(tag, protocol string, success bool) {
	if m == nil {
		return
	}
	protocol = normalizeProtocol(protocol)
	if protocol == "other" {
		return
	}
	result := "failure"
	if success {
		result = "success"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.upstreams[tag]; !ok {
		return
	}
	m.probes[probeKey{upstream: tag, protocol: protocol, result: result}]++
}

func (m *Metrics) SetRouteSelected(route, tag string) {
	if m == nil || route == "" || tag == "" {
		return
	}
	m.mu.Lock()
	m.routes[route] = tag
	m.mu.Unlock()
}

func (m *Metrics) IncActive(protocol string) {
	if m == nil {
		return
	}
	switch normalizeProtocol(protocol) {
	case "tcp":
		m.tcpActive.Add(1)
	case "udp":
		m.udpActive.Add(1)
	}
}

func (m *Metrics) DecActive(protocol string) {
	if m == nil {
		return
	}
	switch normalizeProtocol(protocol) {
	case "tcp":
		decrementNonNegative(&m.tcpActive)
	case "udp":
		decrementNonNegative(&m.udpActive)
	}
}

func decrementNonNegative(value *atomic.Int64) {
	for {
		current := value.Load()
		if current <= 0 || value.CompareAndSwap(current, current-1) {
			return
		}
	}
}

// RecordFlowEvent accepts only a bounded reason vocabulary. Unknown values
// become "other" so a peer-controlled error cannot create metric labels.
func (m *Metrics) RecordFlowEvent(protocol, event, reason string) {
	if m == nil {
		return
	}
	protocol = normalizeProtocol(protocol)
	if protocol != "tcp" && protocol != "udp" {
		return
	}
	switch event {
	case "open", "close", "reject":
	default:
		return
	}
	if event == "open" {
		reason = "none"
	} else {
		reason = normalizeReason(reason)
	}
	m.mu.Lock()
	m.flowEvents[flowEventKey{protocol: protocol, event: event, reason: reason}]++
	m.mu.Unlock()
}

func normalizeProtocol(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "tcp" || protocol == "udp" {
		return protocol
	}
	return "other"
}

func normalizeReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "eof":
		return "eof"
	case "idle_timeout", "timeout":
		return "timeout"
	case "firewall_deny", "backend_blocked", "policy":
		return "policy"
	case "tcp_connection_limit", "udp_mapping_limit", "udp_per_ip_mapping_limit", "capacity":
		return "capacity"
	case "read_error", "write_error", "upstream_write_error", "upstream_read_error", "client_write_error", "io_error":
		return "io_error"
	case "context_done", "canceled":
		return "canceled"
	case "upstream_unusable":
		return "upstream_unusable"
	case "dial_failed":
		return "dial_failed"
	case "shutdown":
		return "shutdown"
	case "none":
		return "none"
	default:
		return "other"
	}
}

func (m *Metrics) AddTraffic(upstreamTag, protocol, direction string, n uint64) {
	if m == nil || n == 0 {
		return
	}
	state, ok := m.upstreams[upstreamTag]
	if !ok || state == nil {
		return
	}
	switch normalizeProtocol(protocol) {
	case "tcp":
		if strings.EqualFold(direction, "up") {
			state.traffic.tcpUp.Add(n)
		} else if strings.EqualFold(direction, "down") {
			state.traffic.tcpDown.Add(n)
		}
	case "udp":
		if strings.EqualFold(direction, "up") {
			state.traffic.udpUp.Add(n)
		} else if strings.EqualFold(direction, "down") {
			state.traffic.udpDown.Add(n)
		}
	}
}

func (m *Metrics) AddAuditReceived(n uint64) {
	if m == nil || n == 0 {
		return
	}
	m.mu.Lock()
	m.audit.received += n
	m.mu.Unlock()
}

func (m *Metrics) AddAuditDropped(n uint64) {
	if m == nil || n == 0 {
		return
	}
	m.mu.Lock()
	m.audit.dropped += n
	m.mu.Unlock()
}

func (m *Metrics) AddAuditWritten(n uint64) {
	if m == nil || n == 0 {
		return
	}
	m.mu.Lock()
	m.audit.written += n
	m.mu.Unlock()
}

func (m *Metrics) RecordRateLimitDrop(_ string, _ uint64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.rateLimitDrops++
	m.mu.Unlock()
}

func (m *Metrics) SetOnlineRulesActive(count int) {
	if m == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	m.mu.Lock()
	m.onlineRules = count
	m.mu.Unlock()
}

func (m *Metrics) IncOnlineRuleExpiryError() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.onlineRuleErrors++
	m.mu.Unlock()
}

func (m *Metrics) IncWebhookDelivery(result string) {
	if m == nil {
		return
	}
	var ok bool
	result, ok = normalizeWebhookResult(result)
	if !ok {
		return
	}
	m.mu.Lock()
	m.webhook[result]++
	m.mu.Unlock()
}

func (m *Metrics) IncWebhookDropped() {
	m.IncWebhookDelivery("dropped")
}

func normalizeWebhookResult(result string) (string, bool) {
	switch result {
	case "success", "failed", "dropped":
		return result, true
	default:
		return "", false
	}
}

func (m *Metrics) IncFirewallDenied(ruleType string) {
	if m == nil {
		return
	}
	ruleType = normalizeRuleType(ruleType)
	m.mu.Lock()
	m.firewallDenied[ruleType]++
	m.mu.Unlock()
}

func normalizeRuleType(ruleType string) string {
	switch strings.ToLower(strings.TrimSpace(ruleType)) {
	case "ip", "cidr", "asn", "country", "protocol":
		return strings.ToLower(strings.TrimSpace(ruleType))
	default:
		return "other"
	}
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(m.Render()))
}

type metricLabel struct {
	name  string
	value string
}

func writeType(b *strings.Builder, name, kind string) {
	b.WriteString("# TYPE ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(kind)
	b.WriteByte('\n')
}

func writeSample(b *strings.Builder, name string, labels []metricLabel, value string) {
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i, label := range labels {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(label.name)
			b.WriteString("=\"")
			writeEscaped(b, label.value)
			b.WriteString("\"")
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(value)
	b.WriteByte('\n')
}

func writeEscaped(b *strings.Builder, value string) {
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
}

func (m *Metrics) Render() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	tags := sortedKeys(m.upstreams)
	routes := copyStringMap(m.routes)
	upstreams := make(map[string]UpstreamMetrics, len(m.upstreams))
	traffic := make(map[string][4]uint64, len(m.upstreams))
	for tag, state := range m.upstreams {
		upstreams[tag] = state.metrics
		traffic[tag] = [4]uint64{
			state.traffic.tcpUp.Load(), state.traffic.tcpDown.Load(),
			state.traffic.udpUp.Load(), state.traffic.udpDown.Load(),
		}
	}
	flowEvents := copyFlowEvents(m.flowEvents)
	probes := copyProbes(m.probes)
	audit := m.audit
	rateLimitDrops := m.rateLimitDrops
	onlineRules := m.onlineRules
	onlineRuleErrors := m.onlineRuleErrors
	webhook := copyUint64Map(m.webhook)
	firewallDenied := copyUint64Map(m.firewallDenied)
	tcpActive := m.tcpActive.Load()
	udpActive := m.udpActive.Load()
	startedAt := m.startedAt
	m.mu.RUnlock()

	var b strings.Builder
	writeType(&b, "fbforward_uptime_seconds", "gauge")
	writeSample(&b, "fbforward_uptime_seconds", nil, formatFloat(time.Since(startedAt).Seconds()))

	writeType(&b, "fbforward_flows_active", "gauge")
	writeSample(&b, "fbforward_flows_active", []metricLabel{{"protocol", "tcp"}}, strconv.FormatInt(tcpActive, 10))
	writeSample(&b, "fbforward_flows_active", []metricLabel{{"protocol", "udp"}}, strconv.FormatInt(udpActive, 10))

	writeType(&b, "fbforward_flow_events_total", "counter")
	for _, key := range sortedFlowEventKeys(flowEvents) {
		writeSample(&b, "fbforward_flow_events_total", []metricLabel{
			{"protocol", key.protocol}, {"event", key.event}, {"reason", key.reason},
		}, strconv.FormatUint(flowEvents[key], 10))
	}

	writeType(&b, "fbforward_route_selected_upstream", "gauge")
	for _, route := range sortedStringKeys(routes) {
		writeSample(&b, "fbforward_route_selected_upstream", []metricLabel{{"route", route}, {"upstream", routes[route]}}, "1")
	}

	writeType(&b, "fbforward_upstream_health_state", "gauge")
	for _, tag := range tags {
		for _, state := range []string{"healthy", "stale", "unknown", "down"} {
			value := "0"
			if upstreams[tag].HealthState == state {
				value = "1"
			}
			writeSample(&b, "fbforward_upstream_health_state", []metricLabel{{"upstream", tag}, {"state", state}}, value)
		}
	}

	writeType(&b, "fbforward_upstream_rtt_seconds", "gauge")
	for _, tag := range tags {
		writeSample(&b, "fbforward_upstream_rtt_seconds", []metricLabel{{"upstream", tag}}, formatFloat(upstreams[tag].RTTMs/1000))
	}

	writeType(&b, "fbforward_upstream_last_success_timestamp_seconds", "gauge")
	for _, tag := range tags {
		value := float64(0)
		if !upstreams[tag].LastSuccess.IsZero() {
			value = float64(upstreams[tag].LastSuccess.UnixNano()) / 1e9
		}
		writeSample(&b, "fbforward_upstream_last_success_timestamp_seconds", []metricLabel{{"upstream", tag}}, formatFloat(value))
	}

	writeType(&b, "fbforward_upstream_probes_total", "counter")
	for _, key := range sortedProbeKeys(probes) {
		writeSample(&b, "fbforward_upstream_probes_total", []metricLabel{{"upstream", key.upstream}, {"protocol", key.protocol}, {"result", key.result}}, strconv.FormatUint(probes[key], 10))
	}

	writeType(&b, "fbforward_traffic_bytes_total", "counter")
	for _, tag := range tags {
		values := traffic[tag]
		writeSample(&b, "fbforward_traffic_bytes_total", []metricLabel{{"upstream", tag}, {"protocol", "tcp"}, {"direction", "up"}}, strconv.FormatUint(values[0], 10))
		writeSample(&b, "fbforward_traffic_bytes_total", []metricLabel{{"upstream", tag}, {"protocol", "tcp"}, {"direction", "down"}}, strconv.FormatUint(values[1], 10))
		writeSample(&b, "fbforward_traffic_bytes_total", []metricLabel{{"upstream", tag}, {"protocol", "udp"}, {"direction", "up"}}, strconv.FormatUint(values[2], 10))
		writeSample(&b, "fbforward_traffic_bytes_total", []metricLabel{{"upstream", tag}, {"protocol", "udp"}, {"direction", "down"}}, strconv.FormatUint(values[3], 10))
	}

	writeType(&b, "fbforward_audit_records_total", "counter")
	writeSample(&b, "fbforward_audit_records_total", []metricLabel{{"result", "received"}}, strconv.FormatUint(audit.received, 10))
	writeSample(&b, "fbforward_audit_records_total", []metricLabel{{"result", "written"}}, strconv.FormatUint(audit.written, 10))
	writeSample(&b, "fbforward_audit_records_total", []metricLabel{{"result", "dropped"}}, strconv.FormatUint(audit.dropped, 10))

	writeType(&b, "fbforward_udp_rate_limit_drops_total", "counter")
	writeSample(&b, "fbforward_udp_rate_limit_drops_total", nil, strconv.FormatUint(rateLimitDrops, 10))

	writeType(&b, "fbforward_online_rules_active", "gauge")
	writeSample(&b, "fbforward_online_rules_active", nil, strconv.Itoa(onlineRules))
	writeType(&b, "fbforward_online_rule_errors_total", "counter")
	writeSample(&b, "fbforward_online_rule_errors_total", []metricLabel{{"operation", "expire"}}, strconv.FormatUint(onlineRuleErrors, 10))

	writeType(&b, "fbforward_webhook_deliveries_total", "counter")
	for _, result := range []string{"success", "failed", "dropped"} {
		writeSample(&b, "fbforward_webhook_deliveries_total", []metricLabel{{"result", result}}, strconv.FormatUint(webhook[result], 10))
	}

	writeType(&b, "fbforward_firewall_denied_total", "counter")
	for _, ruleType := range sortedUint64Keys(firewallDenied) {
		writeSample(&b, "fbforward_firewall_denied_total", []metricLabel{{"rule_type", ruleType}}, strconv.FormatUint(firewallDenied[ruleType], 10))
	}
	return b.String()
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func sortedKeys(values map[string]*upstreamState) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func copyStringMap(values map[string]string) map[string]string {
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func copyUint64Map(values map[string]uint64) map[string]uint64 {
	copy := make(map[string]uint64, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func copyFlowEvents(values map[flowEventKey]uint64) map[flowEventKey]uint64 {
	copy := make(map[flowEventKey]uint64, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func copyProbes(values map[probeKey]uint64) map[probeKey]uint64 {
	copy := make(map[probeKey]uint64, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func sortedFlowEventKeys(values map[flowEventKey]uint64) []flowEventKey {
	keys := make([]flowEventKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].protocol != keys[j].protocol {
			return keys[i].protocol < keys[j].protocol
		}
		if keys[i].event != keys[j].event {
			return keys[i].event < keys[j].event
		}
		return keys[i].reason < keys[j].reason
	})
	return keys
}

func sortedProbeKeys(values map[probeKey]uint64) []probeKey {
	keys := make([]probeKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].upstream != keys[j].upstream {
			return keys[i].upstream < keys[j].upstream
		}
		if keys[i].protocol != keys[j].protocol {
			return keys[i].protocol < keys[j].protocol
		}
		return keys[i].result < keys[j].result
	})
	return keys
}

func sortedUint64Keys(values map[string]uint64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
