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
	Reachable           bool
	BandwidthUpBps      float64
	BandwidthDownBps    float64
	BandwidthTCPUpBps   float64
	BandwidthTCPDownBps float64
	BandwidthUDPUpBps   float64
	BandwidthUDPDownBps float64
	RTTMs               float64
	JitterMs            float64
	RetransRate         float64
	LossRate            float64
	Loss                float64
	ScoreTCP            float64
	ScoreUDP            float64
	ScoreOverall        float64
	Score               float64
	Utilization         float64
	Unusable            bool
}

type utilizationWindow struct {
	mu      sync.Mutex
	samples []rateSample
}

type rateSample struct {
	timestamp time.Time
	tcpUp     uint64
	tcpDown   uint64
	udpUp     uint64
	udpDown   uint64
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
	tcpActive          int
	udpActive          int
	bytesUpTotal       map[string]*atomic.Uint64
	bytesDownTotal     map[string]*atomic.Uint64
	bytesTCP           map[string]*protocolBytes
	bytesUDP           map[string]*protocolBytes
	bytesUpPerSec      map[string]uint64
	bytesDownPerSec    map[string]uint64
	lastBytesUpTotal   map[string]uint64
	lastBytesDownTotal map[string]uint64
	lastBytesTCPUp     map[string]uint64
	lastBytesTCPDown   map[string]uint64
	lastBytesUDPUp     map[string]uint64
	lastBytesUDPDown   map[string]uint64
	utilization        map[string]*utilizationWindow
	memoryAllocBytes   uint64
	startTime          time.Time
}

type UpstreamRates struct {
	TCPUpBps     float64
	TCPDownBps   float64
	UDPUpBps     float64
	UDPDownBps   float64
	TotalUpBps   float64
	TotalDownBps float64
}

func NewMetrics(tags []string) *Metrics {
	upstreams := make(map[string]*UpstreamMetrics, len(tags))
	bytesUpTotal := make(map[string]*atomic.Uint64, len(tags))
	bytesDownTotal := make(map[string]*atomic.Uint64, len(tags))
	bytesTCP := make(map[string]*protocolBytes, len(tags))
	bytesUDP := make(map[string]*protocolBytes, len(tags))
	bytesUpPerSec := make(map[string]uint64, len(tags))
	bytesDownPerSec := make(map[string]uint64, len(tags))
	lastBytesUp := make(map[string]uint64, len(tags))
	lastBytesDown := make(map[string]uint64, len(tags))
	lastBytesTCPUp := make(map[string]uint64, len(tags))
	lastBytesTCPDown := make(map[string]uint64, len(tags))
	lastBytesUDPUp := make(map[string]uint64, len(tags))
	lastBytesUDPDown := make(map[string]uint64, len(tags))
	utilization := make(map[string]*utilizationWindow, len(tags))
	for _, tag := range tags {
		upstreams[tag] = &UpstreamMetrics{}
		bytesUpTotal[tag] = &atomic.Uint64{}
		bytesDownTotal[tag] = &atomic.Uint64{}
		bytesTCP[tag] = &protocolBytes{}
		bytesUDP[tag] = &protocolBytes{}
		bytesUpPerSec[tag] = 0
		bytesDownPerSec[tag] = 0
		lastBytesUp[tag] = 0
		lastBytesDown[tag] = 0
		lastBytesTCPUp[tag] = 0
		lastBytesTCPDown[tag] = 0
		lastBytesUDPUp[tag] = 0
		lastBytesUDPDown[tag] = 0
		utilization[tag] = &utilizationWindow{}
	}
	return &Metrics{
		upstreams:          upstreams,
		bytesUpTotal:       bytesUpTotal,
		bytesDownTotal:     bytesDownTotal,
		bytesTCP:           bytesTCP,
		bytesUDP:           bytesUDP,
		bytesUpPerSec:      bytesUpPerSec,
		bytesDownPerSec:    bytesDownPerSec,
		lastBytesUpTotal:   lastBytesUp,
		lastBytesDownTotal: lastBytesDown,
		lastBytesTCPUp:     lastBytesTCPUp,
		lastBytesTCPDown:   lastBytesTCPDown,
		lastBytesUDPUp:     lastBytesUDPUp,
		lastBytesUDPDown:   lastBytesUDPDown,
		utilization:        utilization,
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
	now := time.Now()
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
		window := m.utilization[tag]
		if window == nil {
			continue
		}
		tcpUpDelta := uint64(0)
		tcpDownDelta := uint64(0)
		udpUpDelta := uint64(0)
		udpDownDelta := uint64(0)

		if counter, ok := m.bytesTCP[tag]; ok {
			current := counter.up.Load()
			prev := m.lastBytesTCPUp[tag]
			tcpUpDelta = current - prev
			m.lastBytesTCPUp[tag] = current

			current = counter.down.Load()
			prev = m.lastBytesTCPDown[tag]
			tcpDownDelta = current - prev
			m.lastBytesTCPDown[tag] = current
		}
		if counter, ok := m.bytesUDP[tag]; ok {
			current := counter.up.Load()
			prev := m.lastBytesUDPUp[tag]
			udpUpDelta = current - prev
			m.lastBytesUDPUp[tag] = current

			current = counter.down.Load()
			prev = m.lastBytesUDPDown[tag]
			udpDownDelta = current - prev
			m.lastBytesUDPDown[tag] = current
		}

		window.addSample(now, tcpUpDelta, tcpDownDelta, udpUpDelta, udpDownDelta)
	}
}

func (w *utilizationWindow) addSample(ts time.Time, tcpUp, tcpDown, udpUp, udpDown uint64) {
	w.mu.Lock()
	w.samples = append(w.samples, rateSample{
		timestamp: ts,
		tcpUp:     tcpUp,
		tcpDown:   tcpDown,
		udpUp:     udpUp,
		udpDown:   udpDown,
	})
	w.mu.Unlock()
}

func (m *Metrics) GetUtilization(tag string, empiricalBwUp, empiricalBwDn float64, window time.Duration) float64 {
	if empiricalBwUp <= 0 || empiricalBwDn <= 0 || window <= 0 {
		return 0
	}
	m.mu.Lock()
	util := m.utilization[tag]
	m.mu.Unlock()
	if util == nil {
		return 0
	}
	cutoff := time.Now().Add(-window)
	util.mu.Lock()
	defer util.mu.Unlock()
	start := 0
	for start < len(util.samples) && util.samples[start].timestamp.Before(cutoff) {
		start++
	}
	if start > 0 {
		util.samples = util.samples[start:]
	}
	var totalUp uint64
	var totalDown uint64
	for _, sample := range util.samples {
		totalUp += sample.tcpUp + sample.udpUp
		totalDown += sample.tcpDown + sample.udpDown
	}
	windowSeconds := window.Seconds()
	utilUp := float64(totalUp*8) / (empiricalBwUp * windowSeconds)
	utilDown := float64(totalDown*8) / (empiricalBwDn * windowSeconds)
	if utilDown > utilUp {
		return utilDown
	}
	return utilUp
}

func (m *Metrics) GetRates(tag string, window time.Duration) UpstreamRates {
	if window <= 0 {
		return UpstreamRates{}
	}
	m.mu.Lock()
	util := m.utilization[tag]
	m.mu.Unlock()
	if util == nil {
		return UpstreamRates{}
	}
	cutoff := time.Now().Add(-window)
	util.mu.Lock()
	start := 0
	for start < len(util.samples) && util.samples[start].timestamp.Before(cutoff) {
		start++
	}
	if start > 0 {
		util.samples = util.samples[start:]
	}
	var tcpUp uint64
	var tcpDown uint64
	var udpUp uint64
	var udpDown uint64
	for _, sample := range util.samples {
		tcpUp += sample.tcpUp
		tcpDown += sample.tcpDown
		udpUp += sample.udpUp
		udpDown += sample.udpDown
	}
	util.mu.Unlock()

	windowSeconds := window.Seconds()
	if windowSeconds <= 0 {
		return UpstreamRates{}
	}
	tcpUpBps := float64(tcpUp*8) / windowSeconds
	tcpDownBps := float64(tcpDown*8) / windowSeconds
	udpUpBps := float64(udpUp*8) / windowSeconds
	udpDownBps := float64(udpDown*8) / windowSeconds

	return UpstreamRates{
		TCPUpBps:     tcpUpBps,
		TCPDownBps:   tcpDownBps,
		UDPUpBps:     udpUpBps,
		UDPDownBps:   udpDownBps,
		TotalUpBps:   tcpUpBps + udpUpBps,
		TotalDownBps: tcpDownBps + udpDownBps,
	}
}

func (m *Metrics) GetAggregateRates(window time.Duration) UpstreamRates {
	if window <= 0 {
		return UpstreamRates{}
	}
	m.mu.Lock()
	tags := make([]string, 0, len(m.upstreams))
	for tag := range m.upstreams {
		tags = append(tags, tag)
	}
	m.mu.Unlock()

	var agg UpstreamRates
	for _, tag := range tags {
		rates := m.GetRates(tag, window)
		agg.TCPUpBps += rates.TCPUpBps
		agg.TCPDownBps += rates.TCPDownBps
		agg.UDPUpBps += rates.UDPUpBps
		agg.UDPDownBps += rates.UDPDownBps
		agg.TotalUpBps += rates.TotalUpBps
		agg.TotalDownBps += rates.TotalDownBps
	}
	return agg
}

func (m *Metrics) SetUpstreamMetrics(tag string, stats upstream.UpstreamStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return
	}
	up.Reachable = stats.Reachable
	up.BandwidthUpBps = stats.BandwidthUpBps
	up.BandwidthDownBps = stats.BandwidthDownBps
	up.BandwidthTCPUpBps = stats.BandwidthUpBpsTCP
	up.BandwidthTCPDownBps = stats.BandwidthDownBpsTCP
	up.BandwidthUDPUpBps = stats.BandwidthUpBpsUDP
	up.BandwidthUDPDownBps = stats.BandwidthDownBpsUDP
	up.RTTMs = stats.RTTMs
	up.JitterMs = stats.JitterMs
	up.RetransRate = stats.RetransRate
	up.LossRate = stats.LossRate
	up.Loss = stats.Loss
	up.ScoreTCP = stats.ScoreTCP
	up.ScoreUDP = stats.ScoreUDP
	up.ScoreOverall = stats.ScoreOverall
	up.Score = stats.ScoreOverall
	up.Utilization = stats.Utilization
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
	tcpActive := m.tcpActive
	udpActive := m.udpActive
	upstreams := make(map[string]UpstreamMetrics, len(m.upstreams))
	for tag, stat := range m.upstreams {
		upstreams[tag] = *stat
	}
	bytesUpPerSec := copyUint64Map(m.bytesUpPerSec)
	bytesDownPerSec := copyUint64Map(m.bytesDownPerSec)
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
	b.WriteString("# TYPE fbforward_upstream_jitter_ms gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_jitter_ms{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].JitterMs))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_bandwidth_up_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_bandwidth_up_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].BandwidthUpBps))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_bandwidth_down_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_bandwidth_down_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].BandwidthDownBps))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_bandwidth_tcp_up_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_bandwidth_tcp_up_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].BandwidthTCPUpBps))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_bandwidth_tcp_down_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_bandwidth_tcp_down_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].BandwidthTCPDownBps))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_bandwidth_udp_up_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_bandwidth_udp_up_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].BandwidthUDPUpBps))
		b.WriteString("\n")
	}
	b.WriteString("# TYPE fbforward_upstream_bandwidth_udp_down_bps gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_bandwidth_udp_down_bps{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].BandwidthUDPDownBps))
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
	b.WriteString("# TYPE fbforward_upstream_score_overall gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_score_overall{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].ScoreOverall))
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
	b.WriteString("# TYPE fbforward_upstream_utilization gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_utilization{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].Utilization))
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
	if mode == upstream.ModeManual {
		b.WriteString("1\n")
	} else {
		b.WriteString("0\n")
	}
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
	b.WriteString("# TYPE fbforward_memory_alloc_bytes gauge\n")
	b.WriteString("fbforward_memory_alloc_bytes ")
	b.WriteString(strconv.FormatUint(memoryAlloc, 10))
	b.WriteString("\n")
	b.WriteString("# TYPE fbforward_uptime_seconds gauge\n")
	b.WriteString("fbforward_uptime_seconds ")
	if startTime.IsZero() {
		b.WriteString("0\n")
	} else {
		b.WriteString(formatFloat(time.Since(startTime).Seconds()))
		b.WriteString("\n")
	}
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
