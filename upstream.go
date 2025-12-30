package main

import (
	"errors"
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Mode int

const (
	ModeAuto Mode = iota
	ModeManual
)

const (
	scoreEpsilon        = 0.0001
	dialFailSwitchCount = 2
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
	activeIP atomic.Value

	stats         UpstreamStats
	rttInit       bool
	jitInit       bool
	lossInit      bool
	dialFailUntil time.Time
	dialFailCount int
}

type WindowMetrics struct {
	Loss     float64
	AvgRTTMs float64
	JitterMs float64
	HasRTT   bool
}

type UpstreamManager struct {
	mu            sync.RWMutex
	upstreams     map[string]*Upstream
	order         []string
	mode          Mode
	manualTag     string
	activeTag     string
	rng           *rand.Rand
	onSelect      func(oldTag, newTag string)
	onStateChange func(tag string, usable bool)
	switching     SwitchingConfig
	pendingTag    string
	pendingCount  int
	lastSwitch    time.Time
}

func NewUpstreamManager(upstreams []*Upstream, rng *rand.Rand) *UpstreamManager {
	m := &UpstreamManager{
		upstreams: make(map[string]*Upstream, len(upstreams)),
		order:     make([]string, 0, len(upstreams)),
		mode:      ModeAuto,
		rng:       rng,
		switching: SwitchingConfig{
			ConfirmWindows:       defaultConfirmWindows,
			FailureLossThreshold: defaultFailureLoss,
			SwitchThreshold:      defaultSwitchThreshold,
			MinHoldSeconds:       defaultMinHoldSeconds,
		},
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

func (m *UpstreamManager) SetSwitching(cfg SwitchingConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.switching = cfg
	if m.switching.ConfirmWindows < 1 {
		m.switching.ConfirmWindows = 1
	}
	if m.switching.FailureLossThreshold <= 0 || m.switching.FailureLossThreshold > 1 {
		m.switching.FailureLossThreshold = defaultFailureLoss
	}
	if m.switching.SwitchThreshold < 0 {
		m.switching.SwitchThreshold = 0
	}
	if m.switching.MinHoldSeconds < 0 {
		m.switching.MinHoldSeconds = 0
	}
	m.resetPendingLocked()
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
	m.resetPendingLocked()
	best, _ := m.selectBestLocked("")
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
	m.resetPendingLocked()
	m.setActiveLocked(tag)
	return nil
}

func (m *UpstreamManager) SelectUpstream() (*Upstream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()

	if m.mode == ModeManual {
		up, ok := m.upstreams[m.manualTag]
		if !ok {
			return nil, errors.New("manual upstream not found")
		}
		if up.dialFailUntil.After(now) {
			return nil, errors.New("manual upstream temporarily unavailable")
		}
		if !up.stats.Usable {
			return nil, errors.New("manual upstream unusable")
		}
		return up, nil
	}
	if m.activeTag != "" {
		up := m.upstreams[m.activeTag]
		if up != nil && up.stats.Usable && !up.dialFailUntil.After(now) {
			return up, nil
		}
	}
	best, _ := m.selectBestLocked("")
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

	if m.mode != ModeAuto {
		return up.stats
	}

	now := time.Now()
	if tag == m.activeTag && wm.Loss >= m.switching.FailureLossThreshold {
		m.resetPendingLocked()
		best, _ := m.selectBestLocked(m.activeTag)
		if best != "" {
			m.setActiveLocked(best)
		} else if !usable {
			m.setActiveLocked("")
		}
		return up.stats
	}

	active := m.upstreams[m.activeTag]
	if m.activeTag == "" || active == nil || !active.stats.Usable || active.dialFailUntil.After(now) {
		m.resetPendingLocked()
		best, _ := m.selectBestLocked("")
		m.setActiveLocked(best)
		return up.stats
	}

	if m.switching.MinHoldSeconds > 0 && !m.lastSwitch.IsZero() {
		hold := time.Duration(m.switching.MinHoldSeconds) * time.Second
		if now.Sub(m.lastSwitch) < hold {
			m.resetPendingLocked()
			return up.stats
		}
	}

	bestTag, bestScore := m.selectBestLocked("")
	if bestTag == "" || bestTag == m.activeTag {
		m.resetPendingLocked()
		return up.stats
	}

	if bestScore < active.stats.Score+m.switching.SwitchThreshold {
		m.resetPendingLocked()
		return up.stats
	}

	if m.pendingTag != bestTag {
		m.pendingTag = bestTag
		m.pendingCount = 0
	}
	if tag == bestTag {
		m.pendingCount++
	}
	if m.pendingCount >= m.switching.ConfirmWindows {
		m.setActiveLocked(bestTag)
	}
	return up.stats
}

func (m *UpstreamManager) UpdateResolved(tag string, ips []net.IP) bool {
	if len(ips) == 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return false
	}
	changed := !sameIPs(up.IPs, ips)
	oldActive := up.ActiveIP()
	up.IPs = ips
	if oldActive == nil || !containsIP(ips, oldActive) {
		up.SetActiveIP(ips[0])
		if oldActive == nil || !ips[0].Equal(oldActive) {
			changed = true
		}
	}
	return changed
}

func (m *UpstreamManager) MarkDialFailure(tag string, cooldown time.Duration) {
	if cooldown <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return
	}
	up.dialFailUntil = time.Now().Add(cooldown)
	up.dialFailCount++
	if m.mode == ModeAuto && tag == m.activeTag && up.dialFailCount >= dialFailSwitchCount {
		best, _ := m.selectBestLocked(m.activeTag)
		if best != "" {
			m.setActiveLocked(best)
		}
	}
}

func (m *UpstreamManager) ClearDialFailure(tag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return
	}
	up.dialFailUntil = time.Time{}
	up.dialFailCount = 0
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
		activeIP := ""
		if ip := up.ActiveIP(); ip != nil {
			activeIP = ip.String()
		}
		out = append(out, UpstreamSnapshot{
			Tag:      up.Tag,
			Host:     up.Host,
			IPs:      ips,
			ActiveIP: activeIP,
			Stats:    up.stats,
		})
	}
	return out
}

func (m *UpstreamManager) selectBestLocked(exclude string) (string, float64) {
	var bestTag string
	bestScore := -1.0
	now := time.Now()
	for _, tag := range m.order {
		if tag == exclude {
			continue
		}
		up := m.upstreams[tag]
		if !up.stats.Usable || up.dialFailUntil.After(now) {
			continue
		}
		score := up.stats.Score
		if bestTag == "" || score > bestScore+scoreEpsilon {
			bestTag = tag
			bestScore = score
		} else if math.Abs(score-bestScore) <= scoreEpsilon && tag == m.activeTag {
			bestTag = tag
		}
	}
	return bestTag, bestScore
}

func (m *UpstreamManager) setActiveLocked(tag string) {
	if tag == m.activeTag {
		return
	}
	old := m.activeTag
	m.activeTag = tag
	m.lastSwitch = time.Now()
	m.resetPendingLocked()
	if tag != "" {
		if up := m.upstreams[tag]; up != nil {
			up.dialFailCount = 0
		}
	}
	if m.onSelect != nil {
		m.onSelect(old, tag)
	}
}

func (m *UpstreamManager) resetPendingLocked() {
	m.pendingTag = ""
	m.pendingCount = 0
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

func (u *Upstream) SetActiveIP(ip net.IP) {
	if ip == nil {
		return
	}
	clone := make(net.IP, len(ip))
	copy(clone, ip)
	u.activeIP.Store(clone)
}

func (u *Upstream) ActiveIP() net.IP {
	val := u.activeIP.Load()
	if val == nil {
		return nil
	}
	ip, ok := val.(net.IP)
	if !ok {
		return nil
	}
	return ip
}

func sameIPs(a, b []net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func containsIP(list []net.IP, target net.IP) bool {
	for _, ip := range list {
		if ip.Equal(target) {
			return true
		}
	}
	return false
}
