package upstream

import (
	"errors"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/util"
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

	RTTMs    float64 `json:"rtt_ms"`
	RTTTcpMs float64 `json:"rtt_tcp_ms"`
	RTTUdpMs float64 `json:"rtt_udp_ms"`
	JitterMs float64 `json:"jitter_ms"`

	RetransRate   float64   `json:"retrans_rate"`
	LastTCPUpdate time.Time `json:"last_tcp_update"`

	LossRate      float64   `json:"loss_rate"`
	LastUDPUpdate time.Time `json:"last_udp_update"`

	ScoreTCP float64 `json:"score_tcp"`
	ScoreUDP float64 `json:"score_udp"`

	Usable bool `json:"usable"`

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
	rttInit       bool
	rttTCPInit    bool
	rttUDPInit    bool
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
	mu             sync.RWMutex
	upstreams      map[string]*Upstream
	order          []string
	mode           Mode
	manualTag      string
	activeTag      string
	rng            *rand.Rand
	onSelect       func(oldTag, newTag string)
	onStateChange  func(tag string, usable bool)
	switching      config.SwitchingConfig
	staleThreshold time.Duration
	scorer         Scorer
	logger         util.Logger

	pendingSwitch  string
	pendingSince   time.Time
	lastSwitchTime time.Time
	inWarmup       bool
	warmupStart    time.Time
	warmupDuration time.Duration
	warmupLogged   bool
}

func NewUpstreamManager(upstreams []*Upstream, rng *rand.Rand, logger util.Logger) *UpstreamManager {
	m := &UpstreamManager{
		upstreams: make(map[string]*Upstream, len(upstreams)),
		order:     make([]string, 0, len(upstreams)),
		mode:      ModeAuto,
		rng:       rng,
		switching: config.DefaultSwitchingConfig(),
		scorer:    DefaultScorer{},
		logger:    logger,
	}
	for _, up := range upstreams {
		m.upstreams[up.Tag] = up
		m.order = append(m.order, up.Tag)
	}
	return m
}

func (m *UpstreamManager) SetScorer(scorer Scorer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scorer == nil {
		m.scorer = DefaultScorer{}
		return
	}
	m.scorer = scorer
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
	if m.switching.Auto.ConfirmDuration.Duration() < 0 {
		m.switching.Auto.ConfirmDuration = defaults.Auto.ConfirmDuration
	}
	if m.switching.Failover.LossRateThreshold <= 0 || m.switching.Failover.LossRateThreshold > 1 {
		m.switching.Failover.LossRateThreshold = defaults.Failover.LossRateThreshold
	}
	if m.switching.Failover.RetransmitRateThreshold <= 0 || m.switching.Failover.RetransmitRateThreshold > 1 {
		m.switching.Failover.RetransmitRateThreshold = defaults.Failover.RetransmitRateThreshold
	}
	if m.switching.Auto.ScoreDeltaThreshold < 0 {
		m.switching.Auto.ScoreDeltaThreshold = defaults.Auto.ScoreDeltaThreshold
	}
	if m.switching.Auto.MinHoldTime.Duration() < 0 {
		m.switching.Auto.MinHoldTime = defaults.Auto.MinHoldTime
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
}

func (m *UpstreamManager) StartWarmup(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inWarmup = true
	m.warmupStart = time.Now()
	m.warmupDuration = duration
	m.warmupLogged = false
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
	m.setActiveLocked(chosen, "score_delta")
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

func (m *UpstreamManager) Get(tag string) *Upstream {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upstreams[tag]
}

func (m *UpstreamManager) SetAuto() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = ModeAuto
	m.manualTag = ""
	m.resetPendingLocked()
	best, _ := m.selectBestLocked("")
	m.setActiveLocked(best, "manual")
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
	m.setActiveLocked(tag, "manual")
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
	m.setActiveLocked(bestTag, "fast_start")
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
	if prevUsable != up.stats.Usable {
		if up.stats.Usable {
			util.Event(m.logger, slog.LevelInfo, "upstream.recovered", "upstream", tag)
		} else {
			util.Event(m.logger, slog.LevelWarn, "upstream.became_unusable", "upstream", tag, "switch.reason", m.usabilityReason(up.stats))
		}
	}
	if prevUsable != up.stats.Usable && m.onStateChange != nil {
		m.onStateChange(tag, up.stats.Usable)
	}
	return up.stats
}

func (m *UpstreamManager) UpdateMeasurement(tag string, result *MeasurementResult, scoring config.ScoringConfig) UpstreamStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok || result == nil {
		return UpstreamStats{}
	}
	if !sanitizeMeasurementResult(result) {
		util.Event(m.logger, slog.LevelWarn, "upstream.measurement_rejected",
			"upstream", tag,
			"network.protocol", result.Network,
		)
		return up.stats
	}

	now := result.Timestamp
	up.stats.RTTMs = applyEMA(result.RTTMs, up.stats.RTTMs, scoring.Smoothing.Alpha, &up.rttInit)
	up.stats.JitterMs = applyEMA(result.JitterMs, up.stats.JitterMs, scoring.Smoothing.Alpha, &up.jitInit)

	if result.Network == "tcp" {
		up.stats.RTTTcpMs = applyEMA(result.RTTMs, up.stats.RTTTcpMs, scoring.Smoothing.Alpha, &up.rttTCPInit)
		up.stats.RetransRate = applyEMA(result.RetransRate, up.stats.RetransRate, scoring.Smoothing.Alpha, &up.retransInit)
		up.stats.LastTCPUpdate = now
	} else {
		up.stats.RTTUdpMs = applyEMA(result.RTTMs, up.stats.RTTUdpMs, scoring.Smoothing.Alpha, &up.rttUDPInit)
		up.stats.LossRate = applyEMA(result.LossRate, up.stats.LossRate, scoring.Smoothing.Alpha, &up.lossInit)
		up.stats.LastUDPUpdate = now
	}

	up.stats.Loss = maxFloat(up.stats.RetransRate, up.stats.LossRate)

	prevUsable := up.stats.Usable
	prevOverall := up.stats.Score
	up.stats.Usable = up.stats.ComputeUsable(m.staleThreshold)
	up.stats.ScoreTCP, up.stats.ScoreUDP, up.stats.Score = m.scorer.ComputeScore(up.stats, scoring, up.Bias, m.staleThreshold)
	util.Event(m.logger, slog.LevelDebug, "upstream.score_updated",
		"upstream", tag,
		"score.tcp", up.stats.ScoreTCP,
		"score.udp", up.stats.ScoreUDP,
		"score.overall", up.stats.Score,
		"score.previous", prevOverall,
	)

	if prevUsable != up.stats.Usable {
		if up.stats.Usable {
			util.Event(m.logger, slog.LevelInfo, "upstream.recovered", "upstream", tag)
		} else {
			util.Event(m.logger, slog.LevelWarn, "upstream.became_unusable", "upstream", tag, "switch.reason", m.usabilityReason(up.stats))
		}
		if m.onStateChange != nil {
			m.onStateChange(tag, up.stats.Usable)
		}
	}

	m.evaluateSwitching(tag, up.stats)
	return up.stats
}

func (m *UpstreamManager) evaluateSwitching(updatedTag string, stats UpstreamStats) {
	if m.mode != ModeAuto {
		return
	}

	if m.warmupDuration > 0 && !m.isInWarmupLocked() && !m.warmupStart.IsZero() && !m.warmupLogged {
		util.Event(m.logger, slog.LevelInfo, "upstream.warmup_ended")
		m.warmupLogged = true
	}

	active := m.upstreams[m.activeTag]
	if active != nil && updatedTag == m.activeTag {
		if stats.LossRate >= m.switching.Failover.LossRateThreshold {
			m.immediateFailoverLocked("failover_loss")
			return
		}
		if stats.RetransRate >= m.switching.Failover.RetransmitRateThreshold {
			m.immediateFailoverLocked("failover_retrans")
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
		activeScore = active.stats.Score
	}

	threshold := m.switching.Auto.ScoreDeltaThreshold
	inWarmup := m.isInWarmupLocked()
	if inWarmup {
		threshold /= 2
	}
	if bestScore-activeScore < threshold {
		m.resetPendingLocked()
		return
	}

	if m.pendingSwitch != bestTag {
		util.Event(m.logger, slog.LevelDebug, "upstream.pending_switch_started",
			"switch.from", m.activeTag,
			"switch.to", bestTag,
		)
		m.pendingSwitch = bestTag
		m.pendingSince = time.Now()
		return
	}

	confirmDur := m.switching.Auto.ConfirmDuration.Duration()
	if inWarmup {
		confirmDur = 0
	}
	if time.Since(m.pendingSince) < confirmDur {
		return
	}

	holdDur := m.switching.Auto.MinHoldTime.Duration()
	if inWarmup {
		holdDur = 0
	}
	if !m.lastSwitchTime.IsZero() && time.Since(m.lastSwitchTime) < holdDur {
		return
	}

	reason := "score_delta"
	if inWarmup {
		reason = "warmup"
	}
	m.setActiveLocked(bestTag, reason)
}

func (m *UpstreamManager) immediateFailoverLocked(reason string) {
	m.resetPendingLocked()
	best, _ := m.selectBestLocked(m.activeTag)
	if best != "" {
		m.setActiveLocked(best, reason)
		return
	}
	active := m.upstreams[m.activeTag]
	if active != nil && !active.stats.Usable {
		m.setActiveLocked("", reason)
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
	util.Event(m.logger, slog.LevelWarn, "upstream.dial_failure_marked",
		"upstream", tag,
		"dial_fail_count", up.dialFailCount,
	)
	if m.mode == ModeAuto && tag == m.activeTag && up.dialFailCount >= dialFailSwitchCount {
		m.immediateFailoverLocked("failover_dial")
	}
}

func (m *UpstreamManager) ClearDialFailure(tag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok {
		return
	}
	if up.dialFailCount > 0 || !up.dialFailUntil.IsZero() {
		util.Event(m.logger, slog.LevelDebug, "upstream.dial_failure_cleared", "upstream", tag)
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
			Tag:       up.Tag,
			Host:      up.Host,
			IPs:       ips,
			ActiveIP:  activeIP,
			Active:    active,
			Usable:    up.stats.Usable,
			Reachable: up.stats.Reachable,
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

func (m *UpstreamManager) setActiveLocked(tag, reason string) {
	if tag == m.activeTag {
		return
	}
	old := m.activeTag
	prevScore := 0.0
	nextScore := 0.0
	if oldUp := m.upstreams[old]; oldUp != nil {
		prevScore = oldUp.stats.Score
	}
	if nextUp := m.upstreams[tag]; nextUp != nil {
		nextScore = nextUp.stats.Score
	}
	m.activeTag = tag
	m.lastSwitchTime = time.Now()
	m.resetPendingLocked()
	if tag != "" {
		if up := m.upstreams[tag]; up != nil {
			up.dialFailCount = 0
		}
	}
	if tag == "" {
		util.Event(m.logger, slog.LevelWarn, "upstream.active_cleared",
			"switch.from", old,
			"switch.reason", reason,
		)
	} else {
		util.Event(m.logger, slog.LevelInfo, "upstream.active_changed",
			"switch.from", old,
			"switch.to", tag,
			"switch.reason", reason,
			"score.previous", prevScore,
			"score.overall", nextScore,
		)
	}
	if m.onSelect != nil {
		m.onSelect(old, tag)
	}
}

func (m *UpstreamManager) resetPendingLocked() {
	if m.pendingSwitch != "" {
		util.Event(m.logger, slog.LevelDebug, "upstream.pending_switch_cancelled",
			"switch.from", m.activeTag,
			"switch.to", m.pendingSwitch,
		)
	}
	m.pendingSwitch = ""
	m.pendingSince = time.Time{}
}

func (m *UpstreamManager) usabilityReason(stats UpstreamStats) string {
	if stats.LossRate >= m.switching.Failover.LossRateThreshold {
		return "failover_loss"
	}
	if stats.RetransRate >= m.switching.Failover.RetransmitRateThreshold {
		return "failover_retrans"
	}
	return "score_delta"
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

func computeFullScore(stats UpstreamStats, cfg config.ScoringConfig, bias float64, staleThresh time.Duration) (float64, float64, float64) {
	const epsilon = 0.001

	tcpRtt := stats.RTTMs
	tcpJit := stats.JitterMs
	udpRtt := stats.RTTMs
	udpJit := stats.JitterMs
	retrans := stats.RetransRate
	loss := stats.LossRate

	tcpStale := staleThresh > 0 && (stats.LastTCPUpdate.IsZero() || time.Since(stats.LastTCPUpdate) > staleThresh)
	if tcpStale {
		tcpRtt = cfg.Reference.TCP.Latency.RTT * 2
		tcpJit = cfg.Reference.TCP.Latency.Jitter * 2
		retrans = cfg.Reference.TCP.RetransmitRate * 2
	}

	udpStale := staleThresh > 0 && (stats.LastUDPUpdate.IsZero() || time.Since(stats.LastUDPUpdate) > staleThresh)
	if udpStale {
		udpRtt = cfg.Reference.UDP.Latency.RTT * 2
		udpJit = cfg.Reference.UDP.Latency.Jitter * 2
		loss = cfg.Reference.UDP.LossRate * 2
	}

	sRTTTCP := maxFloat(math.Exp(-tcpRtt/cfg.Reference.TCP.Latency.RTT), epsilon)
	sJitTCP := maxFloat(math.Exp(-tcpJit/cfg.Reference.TCP.Latency.Jitter), epsilon)
	sRetrans := maxFloat(math.Exp(-retrans/cfg.Reference.TCP.RetransmitRate), epsilon)

	sRTTUDP := maxFloat(math.Exp(-udpRtt/cfg.Reference.UDP.Latency.RTT), epsilon)
	sJitUDP := maxFloat(math.Exp(-udpJit/cfg.Reference.UDP.Latency.Jitter), epsilon)
	sLoss := maxFloat(math.Exp(-loss/cfg.Reference.UDP.LossRate), epsilon)

	wTCP := cfg.Weights.TCP
	tcpScore := 100 * math.Pow(sRTTTCP, wTCP.RTT) *
		math.Pow(sJitTCP, wTCP.Jitter) *
		math.Pow(sRetrans, wTCP.RetransmitRate)

	wUDP := cfg.Weights.UDP
	udpScore := 100 * math.Pow(sRTTUDP, wUDP.RTT) *
		math.Pow(sJitUDP, wUDP.Jitter) *
		math.Pow(sLoss, wUDP.LossRate)

	biasMult := math.Exp(cfg.BiasTransform.Kappa * bias)
	biasMult = clampFloat(biasMult, 0.67, 1.5)

	tcpScore = clampFloat(tcpScore*biasMult, 0, 100)
	udpScore = clampFloat(udpScore*biasMult, 0, 100)

	overall := 0.0
	if tcpStale && !udpStale {
		overall = udpScore
	} else if udpStale && !tcpStale {
		overall = tcpScore
	} else {
		overall = cfg.Weights.ProtocolBlend.TCPWeight*tcpScore + cfg.Weights.ProtocolBlend.UDPWeight*udpScore
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

func sanitizeMeasurementResult(result *MeasurementResult) bool {
	if result == nil {
		return false
	}
	values := []*float64{
		&result.RTTMs,
		&result.JitterMs,
		&result.LossRate,
		&result.RetransRate,
	}
	for _, value := range values {
		if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
			return false
		}
	}
	result.RTTMs = clampFloat(result.RTTMs, 0.01, 10000)
	result.JitterMs = clampFloat(result.JitterMs, 0, 5000)
	result.LossRate = clampFloat(result.LossRate, 0, 1)
	result.RetransRate = clampFloat(result.RetransRate, 0, 1)
	return true
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
