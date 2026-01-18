package upstream

import (
	"errors"
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
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
	Reachable     bool      `json:"reachable"`
	LastReachable time.Time `json:"last_reachable"`

	BandwidthUpBps      float64 `json:"bandwidth_up_bps"`
	BandwidthDownBps    float64 `json:"bandwidth_down_bps"`
	BandwidthUpBpsTCP   float64 `json:"bandwidth_up_bps_tcp"`
	BandwidthDownBpsTCP float64 `json:"bandwidth_down_bps_tcp"`
	BandwidthUpBpsUDP   float64 `json:"bandwidth_up_bps_udp"`
	BandwidthDownBpsUDP float64 `json:"bandwidth_down_bps_udp"`

	RTTMs    float64 `json:"rtt_ms"`
	JitterMs float64 `json:"jitter_ms"`

	RetransRate   float64   `json:"retrans_rate"`
	LastTCPUpdate time.Time `json:"last_tcp_update"`

	LossRate      float64   `json:"loss_rate"`
	LastUDPUpdate time.Time `json:"last_udp_update"`

	ScoreTCP     float64 `json:"score_tcp"`
	ScoreUDP     float64 `json:"score_udp"`
	ScoreOverall float64 `json:"score_overall"`

	Usable      bool    `json:"usable"`
	Utilization float64 `json:"utilization"`

	Loss  float64 `json:"loss"`
	Score float64 `json:"score"`
}

type Upstream struct {
	Tag         string
	Host        string
	MeasureHost string
	MeasurePort int
	Priority    float64
	Bias        float64
	IPs         []net.IP
	activeIP    atomic.Value

	stats         UpstreamStats
	bwUpInit      bool
	bwDnInit      bool
	rttInit       bool
	jitInit       bool
	retransInit   bool
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

type MeasurementResult struct {
	BandwidthUpBps   float64
	BandwidthDownBps float64
	RTTMs            float64
	JitterMs         float64
	LossRate         float64
	RetransRate      float64
	Timestamp        time.Time
	Network          string
}

type UpstreamManager struct {
	mu                  sync.RWMutex
	upstreams           map[string]*Upstream
	order               []string
	mode                Mode
	manualTag           string
	activeTag           string
	rng                 *rand.Rand
	onSelect            func(oldTag, newTag string)
	onStateChange       func(tag string, usable bool)
	switching           config.SwitchingConfig
	staleThreshold      time.Duration
	measurementInterval time.Duration

	pendingSwitch  string
	pendingSince   time.Time
	lastSwitchTime time.Time
	inWarmup       bool
	warmupStart    time.Time
	warmupDuration time.Duration
}

func NewUpstreamManager(upstreams []*Upstream, rng *rand.Rand) *UpstreamManager {
	m := &UpstreamManager{
		upstreams: make(map[string]*Upstream, len(upstreams)),
		order:     make([]string, 0, len(upstreams)),
		mode:      ModeAuto,
		rng:       rng,
		switching: config.DefaultSwitchingConfig(),
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

func (m *UpstreamManager) SetSwitching(cfg config.SwitchingConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defaults := config.DefaultSwitchingConfig()
	m.switching = cfg
	if m.switching.ConfirmDuration.Duration() < 0 {
		m.switching.ConfirmDuration = defaults.ConfirmDuration
	}
	if m.switching.FailureLossThreshold <= 0 || m.switching.FailureLossThreshold > 1 {
		m.switching.FailureLossThreshold = defaults.FailureLossThreshold
	}
	if m.switching.FailureRetransThresh <= 0 || m.switching.FailureRetransThresh > 1 {
		m.switching.FailureRetransThresh = defaults.FailureRetransThresh
	}
	if m.switching.SwitchThreshold < 0 {
		m.switching.SwitchThreshold = defaults.SwitchThreshold
	}
	if m.switching.MinHoldSeconds < 0 {
		m.switching.MinHoldSeconds = defaults.MinHoldSeconds
	}
	m.resetPendingLocked()
}

func (m *UpstreamManager) SetMeasurementConfig(cfg config.MeasurementConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.staleThreshold = cfg.StaleThreshold.Duration()
	if m.staleThreshold <= 0 {
		m.staleThreshold = 2 * time.Minute
	}
	m.measurementInterval = cfg.Interval.Duration()
	if m.measurementInterval <= 0 {
		m.measurementInterval = 2 * time.Second
	}
}

func (m *UpstreamManager) StartWarmup(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inWarmup = true
	m.warmupStart = time.Now()
	m.warmupDuration = duration
}

func (m *UpstreamManager) IsInWarmup() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.inWarmup {
		return false
	}
	if time.Since(m.warmupStart) > m.warmupDuration {
		m.inWarmup = false
		return false
	}
	return true
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

func (m *UpstreamManager) SelectByFastStart(scores map[string]float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bestTag := ""
	bestScore := -1.0
	for tag, score := range scores {
		if score > bestScore {
			bestScore = score
			bestTag = tag
		}
	}
	if bestTag == "" {
		return
	}
	m.setActiveLocked(bestTag)
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
		return up, nil
	}
	if m.activeTag == "" {
		return nil, errors.New("no active upstream")
	}
	up := m.upstreams[m.activeTag]
	if up == nil {
		return nil, errors.New("active upstream not found")
	}
	if up.dialFailUntil.After(now) {
		return nil, errors.New("active upstream temporarily unavailable")
	}
	return up, nil
}

func (m *UpstreamManager) UpdateReachability(tag string, reachable bool) UpstreamStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return UpstreamStats{}
	}

	prevUsable := up.stats.Usable
	up.stats.Reachable = reachable
	if reachable {
		up.stats.LastReachable = time.Now()
	}
	up.stats.Usable = up.stats.ComputeUsable(m.staleThreshold)
	if prevUsable != up.stats.Usable && m.onStateChange != nil {
		m.onStateChange(tag, up.stats.Usable)
	}
	return up.stats
}

func (m *UpstreamManager) UpdateMeasurement(tag string, result *MeasurementResult, scoring config.ScoringConfig, utilization float64) UpstreamStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok || result == nil {
		return UpstreamStats{}
	}

	now := result.Timestamp
	up.stats.BandwidthUpBps = applyEMA(result.BandwidthUpBps, up.stats.BandwidthUpBps, scoring.EMAAlpha, &up.bwUpInit)
	up.stats.BandwidthDownBps = applyEMA(result.BandwidthDownBps, up.stats.BandwidthDownBps, scoring.EMAAlpha, &up.bwDnInit)
	up.stats.RTTMs = applyEMA(result.RTTMs, up.stats.RTTMs, scoring.EMAAlpha, &up.rttInit)
	up.stats.JitterMs = applyEMA(result.JitterMs, up.stats.JitterMs, scoring.EMAAlpha, &up.jitInit)

	if result.Network == "tcp" {
		up.stats.BandwidthUpBpsTCP = result.BandwidthUpBps
		up.stats.BandwidthDownBpsTCP = result.BandwidthDownBps
		up.stats.RetransRate = applyEMA(result.RetransRate, up.stats.RetransRate, scoring.EMAAlpha, &up.retransInit)
		up.stats.LastTCPUpdate = now
	} else {
		up.stats.BandwidthUpBpsUDP = result.BandwidthUpBps
		up.stats.BandwidthDownBpsUDP = result.BandwidthDownBps
		up.stats.LossRate = applyEMA(result.LossRate, up.stats.LossRate, scoring.EMAAlpha, &up.lossInit)
		up.stats.LastUDPUpdate = now
	}

	up.stats.Loss = maxFloat(up.stats.RetransRate, up.stats.LossRate)
	up.stats.Utilization = utilization

	prevUsable := up.stats.Usable
	up.stats.Usable = up.stats.ComputeUsable(m.staleThreshold)
	up.stats.ScoreTCP, up.stats.ScoreUDP, up.stats.ScoreOverall = computeFullScore(up.stats, scoring, up.Bias, utilization, m.staleThreshold)
	up.stats.Score = up.stats.ScoreOverall

	if prevUsable != up.stats.Usable && m.onStateChange != nil {
		m.onStateChange(tag, up.stats.Usable)
	}

	m.evaluateSwitching(tag, up.stats)
	return up.stats
}

func (m *UpstreamManager) evaluateSwitching(updatedTag string, stats UpstreamStats) {
	if m.mode != ModeAuto {
		return
	}

	active := m.upstreams[m.activeTag]
	if active != nil && updatedTag == m.activeTag {
		if stats.RetransRate >= m.switching.FailureRetransThresh || stats.LossRate >= m.switching.FailureLossThreshold {
			m.immediateFailoverLocked()
			return
		}
	}

	bestTag, bestScore := m.selectBestLocked("")
	if bestTag == "" || bestTag == m.activeTag {
		m.resetPendingLocked()
		return
	}

	activeScore := 0.0
	if active != nil {
		activeScore = active.stats.ScoreOverall
	}

	threshold := m.switching.SwitchThreshold
	if m.isInWarmupLocked() {
		threshold /= 2
	}
	if bestScore-activeScore < threshold {
		m.resetPendingLocked()
		return
	}

	if m.pendingSwitch != bestTag {
		m.pendingSwitch = bestTag
		m.pendingSince = time.Now()
		return
	}

	confirmDur := m.switching.ConfirmDuration.Duration()
	if m.isInWarmupLocked() {
		confirmDur = 0
	}
	if time.Since(m.pendingSince) < confirmDur {
		return
	}

	holdDur := time.Duration(m.switching.MinHoldSeconds) * time.Second
	if m.isInWarmupLocked() {
		holdDur = 0
	}
	if !m.lastSwitchTime.IsZero() && time.Since(m.lastSwitchTime) < holdDur {
		return
	}

	m.setActiveLocked(bestTag)
}

func (m *UpstreamManager) immediateFailoverLocked() {
	m.resetPendingLocked()
	best, _ := m.selectBestLocked(m.activeTag)
	if best != "" {
		m.setActiveLocked(best)
		return
	}
	active := m.upstreams[m.activeTag]
	if active != nil && !active.stats.Usable {
		m.setActiveLocked("")
	}
}

func (m *UpstreamManager) isInWarmupLocked() bool {
	if !m.inWarmup {
		return false
	}
	if time.Since(m.warmupStart) > m.warmupDuration {
		m.inWarmup = false
		return false
	}
	return true
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
		m.immediateFailoverLocked()
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
		active := tag == m.activeTag
		out = append(out, UpstreamSnapshot{
			Tag:                 up.Tag,
			Host:                up.Host,
			IPs:                 ips,
			ActiveIP:            activeIP,
			Active:              active,
			Usable:              up.stats.Usable,
			Reachable:           up.stats.Reachable,
			BandwidthUpBps:      up.stats.BandwidthUpBps,
			BandwidthDownBps:    up.stats.BandwidthDownBps,
			BandwidthUpBpsTCP:   up.stats.BandwidthUpBpsTCP,
			BandwidthDownBpsTCP: up.stats.BandwidthDownBpsTCP,
			BandwidthUpBpsUDP:   up.stats.BandwidthUpBpsUDP,
			BandwidthDownBpsUDP: up.stats.BandwidthDownBpsUDP,
			RTTMs:               up.stats.RTTMs,
			JitterMs:            up.stats.JitterMs,
			RetransRate:         up.stats.RetransRate,
			LossRate:            up.stats.LossRate,
			Loss:                up.stats.Loss,
			ScoreTCP:            up.stats.ScoreTCP,
			ScoreUDP:            up.stats.ScoreUDP,
			Score:               up.stats.ScoreOverall,
			Utilization:         up.stats.Utilization,
			LastTCPUpdate:       up.stats.LastTCPUpdate,
			LastUDPUpdate:       up.stats.LastUDPUpdate,
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
		score := up.stats.ScoreOverall
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
	m.lastSwitchTime = time.Now()
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
	m.pendingSwitch = ""
	m.pendingSince = time.Time{}
}

func (s *UpstreamStats) ComputeUsable(staleThresh time.Duration) bool {
	if !s.Reachable {
		return false
	}
	if staleThresh <= 0 {
		return true
	}
	tcpStale := s.LastTCPUpdate.IsZero() || time.Since(s.LastTCPUpdate) > staleThresh
	udpStale := s.LastUDPUpdate.IsZero() || time.Since(s.LastUDPUpdate) > staleThresh
	if tcpStale && udpStale {
		return false
	}
	return true
}

func computeFullScore(stats UpstreamStats, cfg config.ScoringConfig, bias float64, utilization float64, staleThresh time.Duration) (float64, float64, float64) {
	const epsilon = 0.001

	refBwUp, err := config.ParseBandwidth(cfg.RefBandwidthUp)
	if err != nil || refBwUp == 0 {
		refBwUp = 1
	}
	refBwDn, err := config.ParseBandwidth(cfg.RefBandwidthDown)
	if err != nil || refBwDn == 0 {
		refBwDn = 1
	}

	bwUp := stats.BandwidthUpBps
	bwDn := stats.BandwidthDownBps
	rtt := stats.RTTMs
	jit := stats.JitterMs
	retrans := stats.RetransRate
	loss := stats.LossRate

	tcpStale := staleThresh > 0 && (stats.LastTCPUpdate.IsZero() || time.Since(stats.LastTCPUpdate) > staleThresh)
	if tcpStale {
		bwUp = float64(refBwUp) * 0.5
		bwDn = float64(refBwDn) * 0.5
		rtt = cfg.RefRTTMs * 2
		jit = cfg.RefJitterMs * 2
		retrans = cfg.RefRetransRate * 2
	}

	udpStale := staleThresh > 0 && (stats.LastUDPUpdate.IsZero() || time.Since(stats.LastUDPUpdate) > staleThresh)
	if udpStale {
		loss = cfg.RefLossRate * 2
	}

	sBwUp := maxFloat(1-math.Exp(-bwUp/float64(refBwUp)), epsilon)
	sBwDn := maxFloat(1-math.Exp(-bwDn/float64(refBwDn)), epsilon)
	sRTT := maxFloat(math.Exp(-rtt/cfg.RefRTTMs), epsilon)
	sJit := maxFloat(math.Exp(-jit/cfg.RefJitterMs), epsilon)
	sRetrans := maxFloat(math.Exp(-retrans/cfg.RefRetransRate), epsilon)
	sLoss := maxFloat(math.Exp(-loss/cfg.RefLossRate), epsilon)

	wTCP := cfg.WeightsTCP
	tcpScore := 100 * math.Pow(sBwUp, wTCP.BandwidthUp) *
		math.Pow(sBwDn, wTCP.BandwidthDown) *
		math.Pow(sRTT, wTCP.RTT) *
		math.Pow(sJit, wTCP.Jitter) *
		math.Pow(sRetrans, wTCP.Retrans)

	wUDP := cfg.WeightsUDP
	udpScore := 100 * math.Pow(sBwUp, wUDP.BandwidthUp) *
		math.Pow(sBwDn, wUDP.BandwidthDown) *
		math.Pow(sRTT, wUDP.RTT) *
		math.Pow(sJit, wUDP.Jitter) *
		math.Pow(sLoss, wUDP.Loss)

	mult := 1.0
	utilEnabled := true
	if cfg.UtilizationEnabled != nil {
		utilEnabled = *cfg.UtilizationEnabled
	}
	if utilEnabled && utilization > 0 {
		mMin := cfg.UtilizationMinMult
		u0 := cfg.UtilizationThresh
		p := cfg.UtilizationExponent
		mult = mMin + (1-mMin)*math.Exp(-math.Pow(utilization/u0, p))
	}

	biasMult := math.Exp(cfg.BiasKappa * bias)
	biasMult = clampFloat(biasMult, 0.67, 1.5)

	tcpScore = clampFloat(tcpScore*mult*biasMult, 0, 100)
	udpScore = clampFloat(udpScore*mult*biasMult, 0, 100)

	overall := 0.0
	if tcpStale && !udpStale {
		overall = udpScore
	} else if udpStale && !tcpStale {
		overall = tcpScore
	} else {
		overall = cfg.ProtocolWeightTCP*tcpScore + cfg.ProtocolWeightUDP*udpScore
	}

	return tcpScore, udpScore, overall
}

func applyEMA(newValue, oldValue, alpha float64, initialized *bool) float64 {
	if !*initialized {
		*initialized = true
		return newValue
	}
	return alpha*newValue + (1-alpha)*oldValue
}

func clampFloat(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

type UpstreamSnapshot struct {
	Tag       string   `json:"tag"`
	Host      string   `json:"host"`
	IPs       []string `json:"ips"`
	ActiveIP  string   `json:"active_ip"`
	Active    bool     `json:"active"`
	Usable    bool     `json:"usable"`
	Reachable bool     `json:"reachable"`

	BandwidthUpBps      float64 `json:"bandwidth_up_bps"`
	BandwidthDownBps    float64 `json:"bandwidth_down_bps"`
	BandwidthUpBpsTCP   float64 `json:"bandwidth_up_bps_tcp"`
	BandwidthDownBpsTCP float64 `json:"bandwidth_down_bps_tcp"`
	BandwidthUpBpsUDP   float64 `json:"bandwidth_up_bps_udp"`
	BandwidthDownBpsUDP float64 `json:"bandwidth_down_bps_udp"`

	RTTMs    float64 `json:"rtt_ms"`
	JitterMs float64 `json:"jitter_ms"`

	RetransRate float64 `json:"retrans_rate"`
	LossRate    float64 `json:"loss_rate"`
	Loss        float64 `json:"loss"`

	ScoreTCP float64 `json:"score_tcp"`
	ScoreUDP float64 `json:"score_udp"`
	Score    float64 `json:"score"`

	Utilization   float64   `json:"utilization"`
	LastTCPUpdate time.Time `json:"last_tcp_update,omitempty"`
	LastUDPUpdate time.Time `json:"last_udp_update,omitempty"`
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
