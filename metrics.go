package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type UpstreamMetrics struct {
	RTTMs    float64
	JitterMs float64
	Loss     float64
	Score    float64
	Unusable bool
}

type Metrics struct {
	mu               sync.Mutex
	upstreams        map[string]*UpstreamMetrics
	mode             Mode
	activeTag        string
	tcpActive        int
	udpActive        int
	bytesUpTotal     map[string]*atomic.Uint64
	bytesDownTotal   map[string]*atomic.Uint64
	bytesUpPerSec    map[string]uint64
	bytesDownPerSec  map[string]uint64
	lastBytesUpTotal map[string]uint64
	lastBytesDownTotal map[string]uint64
}

func NewMetrics(tags []string) *Metrics {
	upstreams := make(map[string]*UpstreamMetrics, len(tags))
	bytesUpTotal := make(map[string]*atomic.Uint64, len(tags))
	bytesDownTotal := make(map[string]*atomic.Uint64, len(tags))
	bytesUpPerSec := make(map[string]uint64, len(tags))
	bytesDownPerSec := make(map[string]uint64, len(tags))
	lastBytesUp := make(map[string]uint64, len(tags))
	lastBytesDown := make(map[string]uint64, len(tags))
	for _, tag := range tags {
		upstreams[tag] = &UpstreamMetrics{}
		bytesUpTotal[tag] = &atomic.Uint64{}
		bytesDownTotal[tag] = &atomic.Uint64{}
		bytesUpPerSec[tag] = 0
		bytesDownPerSec[tag] = 0
		lastBytesUp[tag] = 0
		lastBytesDown[tag] = 0
	}
	return &Metrics{
		upstreams:          upstreams,
		bytesUpTotal:       bytesUpTotal,
		bytesDownTotal:     bytesDownTotal,
		bytesUpPerSec:      bytesUpPerSec,
		bytesDownPerSec:    bytesDownPerSec,
		lastBytesUpTotal:   lastBytesUp,
		lastBytesDownTotal: lastBytesDown,
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
	m.mu.Lock()
	defer m.mu.Unlock()
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
}

func (m *Metrics) SetUpstreamMetrics(tag string, stats UpstreamStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return
	}
	up.RTTMs = stats.RTTMs
	up.JitterMs = stats.JitterMs
	up.Loss = stats.Loss
	up.Score = stats.Score
	up.Unusable = !stats.Usable
}

func (m *Metrics) SetMode(mode Mode) {
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

func (m *Metrics) AddBytesUp(tag string, n uint64) {
	if counter, ok := m.bytesUpTotal[tag]; ok {
		counter.Add(n)
	}
}

func (m *Metrics) AddBytesDown(tag string, n uint64) {
	if counter, ok := m.bytesDownTotal[tag]; ok {
		counter.Add(n)
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
	b.WriteString("# TYPE fbforward_upstream_loss gauge\n")
	for _, tag := range tags {
		b.WriteString("fbforward_upstream_loss{upstream=\"")
		b.WriteString(tag)
		b.WriteString("\"} ")
		b.WriteString(formatFloat(upstreams[tag].Loss))
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
	if mode == ModeManual {
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
