package metrics

import (
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/upstream"
)

type UpstreamMetrics struct {
	Reachable   bool
	RTTMs       float64
	RTTTcpMs    float64
	RTTUdpMs    float64
	JitterMs    float64
	RetransRate float64
	LossRate    float64
	Loss        float64
	ScoreTCP    float64
	ScoreUDP    float64
	Score       float64
	Unusable    bool
}

type protocolBytes struct {
	up   atomic.Uint64
	down atomic.Uint64
}

type Metrics struct {
	mu                 sync.Mutex
	upstreams          map[string]*UpstreamMetrics
	mode               upstream.Mode
	activeTag          string
	coordConnected     bool
	coordAuthoritative bool
	coordFallback      bool
	coordVersion       int64
	coordSelectedTag   string
	coordPicksReceived uint64
	coordPicksApplied  uint64
	coordPicksRejected uint64
	coordReconnects    uint64
	tcpActive          int
	udpActive          int
	bytesUpTotal       map[string]*atomic.Uint64
	bytesDownTotal     map[string]*atomic.Uint64
	bytesTCP           map[string]*protocolBytes
	bytesUDP           map[string]*protocolBytes
	bytesUpPerSec      map[string]uint64
	bytesDownPerSec    map[string]uint64
	bytesTCPUpPerSec   map[string]uint64
	bytesTCPDownPerSec map[string]uint64
	bytesUDPUpPerSec   map[string]uint64
	bytesUDPDownPerSec map[string]uint64
	lastBytesUpTotal   map[string]uint64
	lastBytesDownTotal map[string]uint64
	lastBytesTCPUp     map[string]uint64
	lastBytesTCPDown   map[string]uint64
	lastBytesUDPUp     map[string]uint64
	lastBytesUDPDown   map[string]uint64
	iplogEventsTotal   uint64
	iplogEventsDropped uint64
	iplogWritesTotal   uint64
	firewallDenied     map[string]uint64
	batchBuckets       []uint64
	batchCount         uint64
	batchSum           uint64
	schedule           ScheduleMetrics
	memoryAllocBytes   uint64
	startTime          time.Time
}

type ScheduleMetrics struct {
	QueueSize     int
	NextScheduled time.Time
	LastRun       map[string]time.Time
}

var iplogBatchBounds = []int{1, 5, 10, 25, 50, 100, 250, 500}

func NewMetrics(tags []string) *Metrics {
	upstreams := make(map[string]*UpstreamMetrics, len(tags))
	bytesUpTotal := make(map[string]*atomic.Uint64, len(tags))
	bytesDownTotal := make(map[string]*atomic.Uint64, len(tags))
	bytesTCP := make(map[string]*protocolBytes, len(tags))
	bytesUDP := make(map[string]*protocolBytes, len(tags))
	bytesUpPerSec := make(map[string]uint64, len(tags))
	bytesDownPerSec := make(map[string]uint64, len(tags))
	bytesTCPUpPerSec := make(map[string]uint64, len(tags))
	bytesTCPDownPerSec := make(map[string]uint64, len(tags))
	bytesUDPUpPerSec := make(map[string]uint64, len(tags))
	bytesUDPDownPerSec := make(map[string]uint64, len(tags))
	lastBytesUp := make(map[string]uint64, len(tags))
	lastBytesDown := make(map[string]uint64, len(tags))
	lastBytesTCPUp := make(map[string]uint64, len(tags))
	lastBytesTCPDown := make(map[string]uint64, len(tags))
	lastBytesUDPUp := make(map[string]uint64, len(tags))
	lastBytesUDPDown := make(map[string]uint64, len(tags))
	for _, tag := range tags {
		upstreams[tag] = &UpstreamMetrics{}
		bytesUpTotal[tag] = &atomic.Uint64{}
		bytesDownTotal[tag] = &atomic.Uint64{}
		bytesTCP[tag] = &protocolBytes{}
		bytesUDP[tag] = &protocolBytes{}
		bytesUpPerSec[tag] = 0
		bytesDownPerSec[tag] = 0
		bytesTCPUpPerSec[tag] = 0
		bytesTCPDownPerSec[tag] = 0
		bytesUDPUpPerSec[tag] = 0
		bytesUDPDownPerSec[tag] = 0
		lastBytesUp[tag] = 0
		lastBytesDown[tag] = 0
		lastBytesTCPUp[tag] = 0
		lastBytesTCPDown[tag] = 0
		lastBytesUDPUp[tag] = 0
		lastBytesUDPDown[tag] = 0
	}
	return &Metrics{
		upstreams:          upstreams,
		bytesUpTotal:       bytesUpTotal,
		bytesDownTotal:     bytesDownTotal,
		bytesTCP:           bytesTCP,
		bytesUDP:           bytesUDP,
		bytesUpPerSec:      bytesUpPerSec,
		bytesDownPerSec:    bytesDownPerSec,
		bytesTCPUpPerSec:   bytesTCPUpPerSec,
		bytesTCPDownPerSec: bytesTCPDownPerSec,
		bytesUDPUpPerSec:   bytesUDPUpPerSec,
		bytesUDPDownPerSec: bytesUDPDownPerSec,
		lastBytesUpTotal:   lastBytesUp,
		lastBytesDownTotal: lastBytesDown,
		lastBytesTCPUp:     lastBytesTCPUp,
		lastBytesTCPDown:   lastBytesTCPDown,
		lastBytesUDPUp:     lastBytesUDPUp,
		lastBytesUDPDown:   lastBytesUDPDown,
		firewallDenied:     make(map[string]uint64),
		batchBuckets:       make([]uint64, len(iplogBatchBounds)),
		startTime:          time.Now(),
	}
}

func (m *Metrics) Start(ctxDone <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctxDone:
				return
			case <-ticker.C:
				m.updatePerSecond()
			}
		}
	}()
}

func (m *Metrics) updatePerSecond() {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.memoryAllocBytes = mem.Alloc
	for tag, total := range m.bytesUpTotal {
		current := total.Load()
		prev := m.lastBytesUpTotal[tag]
		m.bytesUpPerSec[tag] = current - prev
		m.lastBytesUpTotal[tag] = current
	}
	for tag, total := range m.bytesDownTotal {
		current := total.Load()
		prev := m.lastBytesDownTotal[tag]
		m.bytesDownPerSec[tag] = current - prev
		m.lastBytesDownTotal[tag] = current
	}
	for tag := range m.upstreams {
		if counter, ok := m.bytesTCP[tag]; ok {
			current := counter.up.Load()
			prev := m.lastBytesTCPUp[tag]
			tcpUpDelta := current - prev
			m.lastBytesTCPUp[tag] = current
			m.bytesTCPUpPerSec[tag] = tcpUpDelta

			current = counter.down.Load()
			prev = m.lastBytesTCPDown[tag]
			tcpDownDelta := current - prev
			m.lastBytesTCPDown[tag] = current
			m.bytesTCPDownPerSec[tag] = tcpDownDelta
		}
		if counter, ok := m.bytesUDP[tag]; ok {
			current := counter.up.Load()
			prev := m.lastBytesUDPUp[tag]
			udpUpDelta := current - prev
			m.lastBytesUDPUp[tag] = current
			m.bytesUDPUpPerSec[tag] = udpUpDelta

			current = counter.down.Load()
			prev = m.lastBytesUDPDown[tag]
			udpDownDelta := current - prev
			m.lastBytesUDPDown[tag] = current
			m.bytesUDPDownPerSec[tag] = udpDownDelta
		}
		if _, ok := m.bytesTCP[tag]; !ok {
			m.bytesTCPUpPerSec[tag] = 0
			m.bytesTCPDownPerSec[tag] = 0
		}
		if _, ok := m.bytesUDP[tag]; !ok {
			m.bytesUDPUpPerSec[tag] = 0
			m.bytesUDPDownPerSec[tag] = 0
		}
	}
}

func (m *Metrics) SetUpstreamMetrics(tag string, stats upstream.UpstreamStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return
	}
	up.Reachable = stats.Reachable
	up.RTTMs = stats.RTTMs
	up.RTTTcpMs = stats.RTTTcpMs
	up.RTTUdpMs = stats.RTTUdpMs
	up.JitterMs = stats.JitterMs
	up.RetransRate = stats.RetransRate
	up.LossRate = stats.LossRate
	up.Loss = stats.Loss
	up.ScoreTCP = stats.ScoreTCP
	up.ScoreUDP = stats.ScoreUDP
	up.Score = stats.Score
	up.Unusable = !stats.Usable
}

func (m *Metrics) GetUpstreamMetrics(tag string) (UpstreamMetrics, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok || up == nil {
		return UpstreamMetrics{}, false
	}
	return *up, true
}

func (m *Metrics) SetScheduleMetrics(stats ScheduleMetrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schedule.QueueSize = stats.QueueSize
	m.schedule.NextScheduled = stats.NextScheduled
	if stats.LastRun == nil {
		m.schedule.LastRun = nil
		return
	}
	copied := make(map[string]time.Time, len(stats.LastRun))
	for key, ts := range stats.LastRun {
		copied[key] = ts
	}
	m.schedule.LastRun = copied
}

func (m *Metrics) SetMode(mode upstream.Mode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = mode
}

func (m *Metrics) SetActive(tag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeTag = tag
}

func (m *Metrics) SetCoordinationState(state upstream.CoordinationState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coordConnected = state.Connected
	m.coordAuthoritative = state.Authoritative
	m.coordFallback = state.FallbackActive
	m.coordVersion = state.Version
	m.coordSelectedTag = state.SelectedUpstream
}

func (m *Metrics) IncCoordinationPicksReceived() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coordPicksReceived++
}

func (m *Metrics) IncCoordinationPicksApplied() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coordPicksApplied++
}

func (m *Metrics) IncCoordinationPicksRejected() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coordPicksRejected++
}

func (m *Metrics) IncCoordinationReconnects() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coordReconnects++
}

func (m *Metrics) IncTCPActive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tcpActive++
}

func (m *Metrics) DecTCPActive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tcpActive > 0 {
		m.tcpActive--
	}
}

func (m *Metrics) IncUDPActive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.udpActive++
}

func (m *Metrics) DecUDPActive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.udpActive > 0 {
		m.udpActive--
	}
}

func (m *Metrics) AddBytesUp(tag string, n uint64, proto string) {
	if counter, ok := m.bytesUpTotal[tag]; ok {
		counter.Add(n)
	}
	switch strings.ToLower(proto) {
	case "tcp":
		if counter, ok := m.bytesTCP[tag]; ok {
			counter.up.Add(n)
		}
	case "udp":
		if counter, ok := m.bytesUDP[tag]; ok {
			counter.up.Add(n)
		}
	}
}

func (m *Metrics) AddBytesDown(tag string, n uint64, proto string) {
	if counter, ok := m.bytesDownTotal[tag]; ok {
		counter.Add(n)
	}
	switch strings.ToLower(proto) {
	case "tcp":
		if counter, ok := m.bytesTCP[tag]; ok {
			counter.down.Add(n)
		}
	case "udp":
		if counter, ok := m.bytesUDP[tag]; ok {
			counter.down.Add(n)
		}
	}
}

func (m *Metrics) IncIPLogEvent() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.iplogEventsTotal++
}

func (m *Metrics) IncIPLogEventDropped() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.iplogEventsDropped++
}

func (m *Metrics) AddIPLogWrites(n uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.iplogWritesTotal += n
}

func (m *Metrics) ObserveIPLogBatchSize(n int) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, bound := range iplogBatchBounds {
		if n <= bound {
			m.batchBuckets[i]++
		}
	}
	m.batchCount++
	m.batchSum += uint64(n)
}

func (m *Metrics) IncFirewallDenied(ruleType, ruleValue string) {
	key := ruleType + ":" + ruleValue
	m.mu.Lock()
	defer m.mu.Unlock()
	m.firewallDenied[key]++
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(m.Render()))
}

func (m *Metrics) Render() string {
	m.mu.Lock()
	tags := make([]string, 0, len(m.upstreams))
	for tag := range m.upstreams {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	mode := m.mode
	active := m.activeTag
	coordConnected := m.coordConnected
	coordAuthoritative := m.coordAuthoritative
	coordFallback := m.coordFallback
	coordVersion := m.coordVersion
	coordSelectedTag := m.coordSelectedTag
	coordPicksReceived := m.coordPicksReceived
	coordPicksApplied := m.coordPicksApplied
	coordPicksRejected := m.coordPicksRejected
	coordReconnects := m.coordReconnects
	tcpActive := m.tcpActive
	udpActive := m.udpActive
	upstreams := make(map[string]UpstreamMetrics, len(m.upstreams))
	for tag, stat := range m.upstreams {
		upstreams[tag] = *stat
	}
	bytesUpPerSec := copyUint64Map(m.bytesUpPerSec)
	bytesDownPerSec := copyUint64Map(m.bytesDownPerSec)
	bytesTCPUpPerSec := copyUint64Map(m.bytesTCPUpPerSec)
	bytesTCPDownPerSec := copyUint64Map(m.bytesTCPDownPerSec)
	bytesUDPUpPerSec := copyUint64Map(m.bytesUDPUpPerSec)
	bytesUDPDownPerSec := copyUint64Map(m.bytesUDPDownPerSec)
	iplogEventsTotal := m.iplogEventsTotal
	iplogEventsDropped := m.iplogEventsDropped
	iplogWritesTotal := m.iplogWritesTotal
	firewallDenied := copyUint64Map(m.firewallDenied)
	batchBuckets := append([]uint64(nil), m.batchBuckets...)
	batchCount := m.batchCount
	batchSum := m.batchSum
	schedule := m.schedule
	memoryAlloc := m.memoryAllocBytes
	startTime := m.startTime
	m.mu.Unlock()
	bytesUpTotal := make(map[string]uint64, len(tags))
	bytesDownTotal := make(map[string]uint64, len(tags))
	for _, tag := range tags {
		if counter, ok := m.bytesUpTotal[tag]; ok {
			bytesUpTotal[tag] = counter.Load()
		}
		if counter, ok := m.bytesDownTotal[tag]; ok {
			bytesDownTotal[tag] = counter.Load()
		}
	}

	var b strings.Builder
	b.WriteString("# TYPE fbforward_upstream_rtt_ms gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_rtt_ms{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].RTTMs))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_rtt_tcp_ms gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_rtt_tcp_ms{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].RTTTcpMs))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_rtt_udp_ms gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_rtt_udp_ms{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].RTTUdpMs))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_jitter_ms gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_jitter_ms{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].JitterMs))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_retrans_rate gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_retrans_rate{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].RetransRate))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_loss_rate gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_loss_rate{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].LossRate))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_loss gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_loss{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].Loss))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_score_tcp gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_score_tcp{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].ScoreTCP))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_score_udp gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_score_udp{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].ScoreUDP))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_score gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_score{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].Score))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_reachable gauge\n")
	for _, tag := range tags {
		val := "0"
		if upstreams[tag].Reachable {
			val = "1"
		}
		b.WriteString("fbforward_upstream_reachable{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(val)
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_unusable gauge\n")
	for _, tag := range tags {
		val := "0"
		if upstreams[tag].Unusable {
			val = "1"
		}
		b.WriteString("fbforward_upstream_unusable{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(val)
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_mode gauge\n")
	b.WriteString("fbforward_mode ")
	switch mode {
	case upstream.ModeManual:
		b.WriteString("1\n")
	case upstream.ModeCoordination:
		b.WriteString("2\n")
	default:
		b.WriteString("0\n")
	}
	b.WriteString("# TYPE fbforward_coord_connected gauge\n")
	if coordConnected {
		b.WriteString("fbforward_coord_connected 1\n")
	} else {
		b.WriteString("fbforward_coord_connected 0\n")
	}
	b.WriteString("# TYPE fbforward_coord_authoritative gauge\n")
	if coordAuthoritative {
		b.WriteString("fbforward_coord_authoritative 1\n")
	} else {
		b.WriteString("fbforward_coord_authoritative 0\n")
	}
	b.WriteString("# TYPE fbforward_coord_fallback_active gauge\n")
	if coordFallback {
		b.WriteString("fbforward_coord_fallback_active 1\n")
	} else {
		b.WriteString("fbforward_coord_fallback_active 0\n")
	}
	b.WriteString("# TYPE fbforward_coord_version gauge\n")
	b.WriteString("fbforward_coord_version ")
	b.WriteString(strconv.FormatInt(coordVersion, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_coord_selected_upstream gauge\n")
	for _, tag := range tags {
		val := "0"
		if tag == coordSelectedTag && coordSelectedTag != "" {
			val = "1"
		}
		b.WriteString("fbforward_coord_selected_upstream{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(val)
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_coord_picks_received_total counter\n")
	b.WriteString("fbforward_coord_picks_received_total ")
	b.WriteString(strconv.FormatUint(coordPicksReceived, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_coord_picks_applied_total counter\n")
	b.WriteString("fbforward_coord_picks_applied_total ")
	b.WriteString(strconv.FormatUint(coordPicksApplied, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_coord_picks_rejected_total counter\n")
	b.WriteString("fbforward_coord_picks_rejected_total ")
	b.WriteString(strconv.FormatUint(coordPicksRejected, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_coord_reconnects_total counter\n")
	b.WriteString("fbforward_coord_reconnects_total ")
	b.WriteString(strconv.FormatUint(coordReconnects, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_active_upstream gauge\n")
	for _, tag := range tags {
		val := "0"
		if tag == active && active != "" {
			val = "1"
		}
		b.WriteString("fbforward_active_upstream{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(val)
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_tcp_active gauge\n")
	b.WriteString("fbforward_tcp_active ")
	b.WriteString(strconv.Itoa(tcpActive))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_udp_mappings_active gauge\n")
	b.WriteString("fbforward_udp_mappings_active ")
	b.WriteString(strconv.Itoa(udpActive))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_bytes_up_total counter\n")
	for _, tag := range tags {
		b.WriteString("fbforward_bytes_up_total{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(strconv.FormatUint(bytesUpTotal[tag], 10))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_bytes_down_total counter\n")
	for _, tag := range tags {
		b.WriteString("fbforward_bytes_down_total{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(strconv.FormatUint(bytesDownTotal[tag], 10))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_bytes_up_per_second gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_bytes_up_per_second{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(strconv.FormatUint(bytesUpPerSec[tag], 10))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_bytes_down_per_second gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_bytes_down_per_second{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(strconv.FormatUint(bytesDownPerSec[tag], 10))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_tcp_up_rate_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_tcp_up_rate_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(float64(bytesTCPUpPerSec[tag]) * 8))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_tcp_down_rate_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_tcp_down_rate_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(float64(bytesTCPDownPerSec[tag]) * 8))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_udp_up_rate_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_udp_up_rate_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(float64(bytesUDPUpPerSec[tag]) * 8))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_udp_down_rate_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_udp_down_rate_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(float64(bytesUDPDownPerSec[tag]) * 8))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_measurement_queue_size gauge\n")
	b.WriteString("fbforward_measurement_queue_size ")
	b.WriteString(strconv.Itoa(schedule.QueueSize))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_measurement_last_run_seconds gauge\n")
	now := time.Now()
	for key, ts := range schedule.LastRun {
		if ts.IsZero() {
			continue
		}
		tag, proto, ok := splitScheduleKey(key)
		if !ok {
			continue
		}
		b.WriteString("fbforward_measurement_last_run_seconds{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\",protocol=\"")
		b.WriteString(proto)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(now.Sub(ts).Seconds()))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_memory_alloc_bytes gauge\n")
	b.WriteString("fbforward_memory_alloc_bytes ")
	b.WriteString(strconv.FormatUint(memoryAlloc, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_goroutines gauge\n")
	b.WriteString("fbforward_goroutines ")
	b.WriteString(strconv.Itoa(runtime.NumGoroutine()))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_uptime_seconds gauge\n")
	b.WriteString("fbforward_uptime_seconds ")
	if startTime.IsZero() {
		b.WriteString("0\n")
	} else {
		b.WriteString(formatFloat(time.Since(startTime).Seconds()))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_iplog_events_total counter\n")
	b.WriteString("fbforward_iplog_events_total ")
	b.WriteString(strconv.FormatUint(iplogEventsTotal, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_iplog_events_dropped_total counter\n")
	b.WriteString("fbforward_iplog_events_dropped_total ")
	b.WriteString(strconv.FormatUint(iplogEventsDropped, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_iplog_writes_total counter\n")
	b.WriteString("fbforward_iplog_writes_total ")
	b.WriteString(strconv.FormatUint(iplogWritesTotal, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_firewall_denied_total counter\n")
	firewallKeys := make([]string, 0, len(firewallDenied))
	for key := range firewallDenied {
		firewallKeys = append(firewallKeys, key)
	}
	sort.Strings(firewallKeys)
	for _, key := range firewallKeys {
		ruleType, ruleValue := splitFirewallKey(key)
		b.WriteString("fbforward_firewall_denied_total{rule_type=\"")
		b.WriteString(ruleType)
		b.WriteString("\",rule_value=\"")
		b.WriteString(ruleValue)
		b.WriteString("\"} ")
		b.WriteString(strconv.FormatUint(firewallDenied[key], 10))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_iplog_batch_size histogram\n")
	for i, bound := range iplogBatchBounds {
		b.WriteString("fbforward_iplog_batch_size_bucket{le=\"")
		b.WriteString(strconv.Itoa(bound))
		b.WriteString("\"} ")
		b.WriteString(strconv.FormatUint(batchBuckets[i], 10))
		b.WriteString("\n")
	}
	b.WriteString("fbforward_iplog_batch_size_bucket{le=\"+Inf\"} ")
	b.WriteString(strconv.FormatUint(batchCount, 10))
	b.WriteString("\n")
	b.WriteString("fbforward_iplog_batch_size_sum ")
	b.WriteString(strconv.FormatUint(batchSum, 10))
	b.WriteString("\n")
	b.WriteString("fbforward_iplog_batch_size_count ")
	b.WriteString(strconv.FormatUint(batchCount, 10))
	b.WriteString("\n")
	return b.String()
}

func copyUint64Map(src map[string]uint64) map[string]uint64 {
	dst := make(map[string]uint64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func formatFloat(val float64) string {
	return strconv.FormatFloat(val, 'f', 6, 64)
}

func splitScheduleKey(key string) (string, string, bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 2 {
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func splitFirewallKey(key string) (string, string) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}
