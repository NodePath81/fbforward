package main

import (
	"errors"
	"math"
	"math/rand"
	"net"
	"sync"
)

type Mode int

const (
	ModeAuto Mode = iota
	ModeManual
)

func (m Mode) String() string {
	if m == ModeManual {
		return "manual"
	}
	return "auto"
}

type UpstreamStats struct {
	RTTMs    float64 `json:"rtt_ms"`
	JitterMs float64 `json:"jitter_ms"`
	Loss     float64 `json:"loss"`
	Score    float64 `json:"score"`
	Usable   bool    `json:"usable"`
}

type Upstream struct {
	Tag      string
	Host     string
	IPs      []net.IP
	ActiveIP net.IP

	stats     UpstreamStats
	rttInit   bool
	jitInit   bool
	lossInit  bool
}

type WindowMetrics struct {
	Loss     float64
	AvgRTTMs float64
	JitterMs float64
	HasRTT   bool
}

type UpstreamManager struct {
	mu         sync.RWMutex
	upstreams  map[string]*Upstream
	order      []string
	mode       Mode
	manualTag  string
	activeTag  string
	rng        *rand.Rand
	onSelect   func(oldTag, newTag string)
	onStateChange func(tag string, usable bool)
}

func NewUpstreamManager(upstreams []*Upstream, rng *rand.Rand) *UpstreamManager {
	m := &UpstreamManager{
		upstreams: make(map[string]*Upstream, len(upstreams)),
		order:     make([]string, 0, len(upstreams)),
		mode:      ModeAuto,
		rng:       rng,
	}
	for _, up := range upstreams {
		m.upstreams[up.Tag] = up
		m.order = append(m.order, up.Tag)
	}
	return m
}

func (m *UpstreamManager) SetCallbacks(onSelect func(oldTag, newTag string), onStateChange func(tag string, usable bool)) {
	m.onSelect = onSelect
	m.onStateChange = onStateChange
}

func (m *UpstreamManager) PickInitial() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeTag != "" {
		return
	}
	usable := make([]string, 0, len(m.order))
	for _, tag := range m.order {
		if m.upstreams[tag].stats.Usable {
			usable = append(usable, tag)
		}
	}
	if len(usable) == 0 {
		return
	}
	chosen := usable[m.rng.Intn(len(usable))]
	m.setActiveLocked(chosen)
}

func (m *UpstreamManager) Mode() Mode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mode
}

func (m *UpstreamManager) ActiveTag() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeTag
}

func (m *UpstreamManager) SetAuto() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = ModeAuto
	m.manualTag = ""
	best := m.selectBestLocked()
	m.setActiveLocked(best)
}

func (m *UpstreamManager) SetManual(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return errors.New("unknown upstream tag")
	}
	if !up.stats.Usable {
		return errors.New("selected upstream is unusable")
	}
	m.mode = ModeManual
	m.manualTag = tag
	m.setActiveLocked(tag)
	return nil
}

func (m *UpstreamManager) SelectUpstream() (*Upstream, error) {
	m.mu.RLock()
	mode := m.mode
	manual := m.manualTag
	active := m.activeTag
	m.mu.RUnlock()

	if mode == ModeManual {
		m.mu.RLock()
		up, ok := m.upstreams[manual]
		usable := ok && up.stats.Usable
		m.mu.RUnlock()
		if !ok {
			return nil, errors.New("manual upstream not found")
		}
		if !usable {
			return nil, errors.New("manual upstream unusable")
		}
		return up, nil
	}
	if active != "" {
		m.mu.RLock()
		up := m.upstreams[active]
		usable := up != nil && up.stats.Usable
		m.mu.RUnlock()
		if usable {
			return up, nil
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	best := m.selectBestLocked()
	if best == "" {
		return nil, errors.New("no usable upstreams")
	}
	m.setActiveLocked(best)
	return m.upstreams[best], nil
}

func (m *UpstreamManager) UpdateWindow(tag string, wm WindowMetrics, scoring ScoringConfig) UpstreamStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return UpstreamStats{}
	}

	usable := wm.Loss < 1
	prevUsable := up.stats.Usable

	up.stats.Loss = applyEMA(wm.Loss, up.stats.Loss, scoring.EMAAlpha, &up.lossInit)
	if wm.HasRTT {
		up.stats.RTTMs = applyEMA(wm.AvgRTTMs, up.stats.RTTMs, scoring.EMAAlpha, &up.rttInit)
		up.stats.JitterMs = applyEMA(wm.JitterMs, up.stats.JitterMs, scoring.EMAAlpha, &up.jitInit)
	}
	up.stats.Usable = usable
	up.stats.Score = computeScore(up.stats, scoring)

	if prevUsable != usable && m.onStateChange != nil {
		m.onStateChange(tag, usable)
	}

	if m.mode == ModeAuto {
		best := m.selectBestLocked()
		m.setActiveLocked(best)
	}
	return up.stats
}

func (m *UpstreamManager) Snapshot() []UpstreamSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]UpstreamSnapshot, 0, len(m.order))
	for _, tag := range m.order {
		up := m.upstreams[tag]
		ips := make([]string, 0, len(up.IPs))
		for _, ip := range up.IPs {
			ips = append(ips, ip.String())
		}
		out = append(out, UpstreamSnapshot{
			Tag:      up.Tag,
			Host:     up.Host,
			IPs:      ips,
			ActiveIP: up.ActiveIP.String(),
			Stats:    up.stats,
		})
	}
	return out
}

func (m *UpstreamManager) selectBestLocked() string {
	var bestTag string
	bestScore := -1.0
	for _, tag := range m.order {
		up := m.upstreams[tag]
		if !up.stats.Usable {
			continue
		}
		score := up.stats.Score
		if bestTag == "" || score > bestScore+0.0001 {
			bestTag = tag
			bestScore = score
		} else if math.Abs(score-bestScore) <= 0.0001 && tag == m.activeTag {
			bestTag = tag
		}
	}
	return bestTag
}

func (m *UpstreamManager) setActiveLocked(tag string) {
	if tag == m.activeTag {
		return
	}
	old := m.activeTag
	m.activeTag = tag
	if m.onSelect != nil {
		m.onSelect(old, tag)
	}
}

func computeScore(stats UpstreamStats, scoring ScoringConfig) float64 {
	loss := stats.Loss
	if loss < 0 {
		loss = 0
	}
	if loss > 1 {
		loss = 1
	}
	srtt := math.Exp(-stats.RTTMs / scoring.MetricRefRTTMs)
	sjit := math.Exp(-stats.JitterMs / scoring.MetricRefJitterMs)
	slos := math.Exp(-loss / scoring.MetricRefLoss)
	score := 100 *
		math.Pow(srtt, scoring.Weights.RTT) *
		math.Pow(sjit, scoring.Weights.Jitter) *
		math.Pow(slos, scoring.Weights.Loss)
	if score < 0 {
		return 0
	}
	return score
}

func applyEMA(newValue, oldValue, alpha float64, initialized *bool) float64 {
	if !*initialized {
		*initialized = true
		return newValue
	}
	return alpha*newValue + (1-alpha)*oldValue
}

type UpstreamSnapshot struct {
	Tag      string        `json:"tag"`
	Host     string        `json:"host"`
	IPs      []string      `json:"ips"`
	ActiveIP string        `json:"active_ip"`
	Stats    UpstreamStats `json:"stats"`
}
