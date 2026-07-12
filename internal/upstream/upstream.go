package upstream

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"sort"
	"strings"
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
	ModeCoordination
)

const dialFailSwitchCount = 2

func (m Mode) String() string {
	switch m {
	case ModeManual:
		return "manual"
	case ModeCoordination:
		return "coordination"
	default:
		return "auto"
	}
}

type CoordinationState struct {
	Connected        bool   `json:"connected"`
	Authoritative    bool   `json:"authoritative"`
	SelectedUpstream string `json:"selected_upstream"`
	Version          int64  `json:"version"`
	FallbackActive   bool   `json:"fallback_active"`
}

// UpstreamStats is a compact, selection-facing health snapshot. Probe
// protocol details remain in measurement history, not in route selection.
type UpstreamStats struct {
	HealthState          HealthState `json:"health_state"`
	Reachable            bool        `json:"reachable"`
	LastReachable        time.Time   `json:"last_reachable"`
	RTTMs                float64     `json:"rtt_ms"`
	Usable               bool        `json:"usable"`
	ConsecutiveSuccesses int         `json:"consecutive_successes"`
	ConsecutiveFailures  int         `json:"consecutive_failures"`
}

type Upstream struct {
	Tag         string
	Host        string
	MeasureHost string
	MeasurePort int
	Priority    float64
	IPs         []net.IP
	activeIP    atomic.Value

	stats         UpstreamStats
	health        HealthSnapshot
	dialFailUntil time.Time
	dialFailCount int
}

type WindowMetrics struct {
	Loss     float64
	AvgRTTMs float64
	JitterMs float64
	HasRTT   bool
}

// MeasurementResult is retained as the fbmeasure adapter boundary. Only
// RTT, timestamp and success are consumed by the health model.
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
	coordConnected bool
	coordTag       string
	coordVersion   int64
	coordFallback  bool
	rng            *rand.Rand
	onSelect       func(change ActiveChange)
	onStateChange  func(change UsabilityChange)
	onCoordState   func(state CoordinationState)
	switching      config.SwitchingConfig
	healthConfig   config.HealthConfig
	logger         util.Logger

	pendingSwitch string
	pendingSince  time.Time
	lastSwitch    time.Time
}

func NewUpstreamManager(upstreams []*Upstream, rng *rand.Rand, logger util.Logger) *UpstreamManager {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	m := &UpstreamManager{
		upstreams:    make(map[string]*Upstream, len(upstreams)),
		order:        make([]string, 0, len(upstreams)),
		mode:         ModeAuto,
		rng:          rng,
		switching:    config.DefaultSwitchingConfig(),
		healthConfig: config.HealthConfig{RTTEWMAAlpha: 0.25, FailureThreshold: 3, RecoveryThreshold: 2, StaleThreshold: config.Duration(time.Minute)},
		logger:       logger,
	}
	for _, up := range upstreams {
		if up == nil || up.Tag == "" {
			continue
		}
		m.upstreams[up.Tag] = up
		m.order = append(m.order, up.Tag)
	}
	return m
}

func (m *UpstreamManager) SetCallbacks(onSelect func(change ActiveChange), onStateChange func(change UsabilityChange)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSelect = onSelect
	m.onStateChange = onStateChange
}

func (m *UpstreamManager) SetCoordinationStateCallback(callback func(state CoordinationState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onCoordState = callback
}

func (m *UpstreamManager) SetHealthConfig(cfg config.HealthConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cfg.RTTEWMAAlpha <= 0 || cfg.RTTEWMAAlpha > 1 {
		cfg.RTTEWMAAlpha = 0.25
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.RecoveryThreshold <= 0 {
		cfg.RecoveryThreshold = 2
	}
	if cfg.StaleThreshold.Duration() <= 0 {
		cfg.StaleThreshold = config.Duration(time.Minute)
	}
	m.healthConfig = cfg
	for _, up := range m.upstreams {
		m.refreshStatsLocked(up)
	}
}

func (m *UpstreamManager) SetSwitching(cfg config.SwitchingConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defaults := config.DefaultSwitchingConfig()
	if cfg.ConfirmDuration.Duration() < 0 {
		cfg.ConfirmDuration = defaults.ConfirmDuration
	}
	if cfg.MinHoldTime.Duration() < 0 {
		cfg.MinHoldTime = defaults.MinHoldTime
	}
	if cfg.LatencyImprovement.Duration() < 0 {
		cfg.LatencyImprovement = defaults.LatencyImprovement
	}
	m.switching = cfg
	m.resetPendingLocked()
}

// SetMeasurementConfig is kept as a source-compatible no-op for integrations
// that used the old manager API. Health configuration is now explicit.
func (m *UpstreamManager) SetMeasurementConfig(_ config.MeasurementConfig) {}

func (m *UpstreamManager) PickInitial() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeTag != "" {
		return
	}
	if tag, _ := m.selectBestLocked(nil); tag != "" {
		m.setActiveLocked(tag, "initial")
	}
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

func (m *UpstreamManager) CoordinationState() CoordinationState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.coordinationStateLocked()
}

func (m *UpstreamManager) Get(tag string) *Upstream {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upstreams[tag]
}

func (m *UpstreamManager) Health(tag string) (HealthSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[tag]
	if !ok || up == nil {
		return HealthSnapshot{}, false
	}
	m.refreshStatsLocked(up)
	snapshot := up.health
	snapshot.State = up.stats.HealthState
	return snapshot, true
}

func (m *UpstreamManager) RankedTags() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, up := range m.upstreams {
		m.refreshStatsLocked(up)
	}
	tags := append([]string(nil), m.order...)
	sort.SliceStable(tags, func(i, j int) bool {
		return m.betterLocked(m.upstreams[tags[i]], m.upstreams[tags[j]], tags[i] == m.activeTag)
	})
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		up := m.upstreams[tag]
		if up != nil && m.selectableLocked(up, time.Now()) {
			result = append(result, tag)
		}
	}
	return result
}

func (m *UpstreamManager) SetAuto() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode, m.manualTag = ModeAuto, ""
	m.clearCoordinationLocked()
	m.resetPendingLocked()
	if best, _ := m.selectBestLocked(nil); best != "" {
		m.setActiveLocked(best, "auto")
	}
	m.emitCoordinationStateLocked()
}

func (m *UpstreamManager) SetManual(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	up, ok := m.upstreams[strings.TrimSpace(tag)]
	if !ok {
		return errors.New("unknown upstream tag")
	}
	if !m.selectableLocked(up, time.Now()) {
		return errors.New("selected upstream is unusable")
	}
	m.mode, m.manualTag = ModeManual, up.Tag
	m.clearCoordinationLocked()
	m.resetPendingLocked()
	m.setActiveLocked(up.Tag, "manual")
	m.emitCoordinationStateLocked()
	return nil
}

func (m *UpstreamManager) SetCoordination() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode, m.manualTag = ModeCoordination, ""
	m.clearCoordinationLocked()
	m.coordFallback = true
	m.resetPendingLocked()
	if best, _ := m.selectBestLocked(nil); best != "" {
		m.setActiveLocked(best, "coordination_fallback")
	}
	m.emitCoordinationStateLocked()
}

func (m *UpstreamManager) SetCoordinationConnected(connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coordConnected = connected
	if !connected {
		m.activateCoordinationFallbackLocked("coordination_fallback")
	} else {
		m.emitCoordinationStateLocked()
	}
}

func (m *UpstreamManager) ApplyCoordinationPick(version int64, tag string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if version < m.coordVersion || (version == m.coordVersion && !m.coordFallback && strings.TrimSpace(tag) != m.coordTag) {
		return false, errors.New("stale coordination version")
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		m.coordVersion = version
		m.activateCoordinationFallbackLocked("coordination_fallback")
		return true, nil
	}
	up, ok := m.upstreams[tag]
	if !ok {
		m.activateCoordinationFallbackLocked("coordination_fallback")
		return false, errors.New("coordinated upstream not found")
	}
	if !m.selectableLocked(up, time.Now()) {
		m.activateCoordinationFallbackLocked("coordination_fallback")
		return false, errors.New("coordinated upstream is unusable")
	}
	m.coordVersion, m.coordTag, m.coordFallback = version, tag, false
	if m.mode == ModeCoordination {
		m.setActiveLocked(tag, "coordination")
	}
	m.emitCoordinationStateLocked()
	return true, nil
}

func (m *UpstreamManager) SelectUpstream() (*Upstream, error) {
	return m.SelectUpstreamFrom(nil)
}

// SelectUpstreamFrom enforces route membership and ranks candidates by
// health, RTT, priority and stable configuration order.
func (m *UpstreamManager) SelectUpstreamFrom(tags []string) (*Upstream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, up := range m.upstreams {
		m.refreshStatsLocked(up)
	}
	allowed := make(map[string]struct{}, len(tags))
	ordered := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			if _, exists := allowed[tag]; !exists {
				ordered = append(ordered, tag)
			}
			allowed[tag] = struct{}{}
		}
	}
	contains := func(tag string) bool { return len(allowed) == 0 || hasTag(allowed, tag) }
	now := time.Now()
	if len(ordered) == 1 {
		up := m.upstreams[ordered[0]]
		if up == nil {
			return nil, fmt.Errorf("upstream %q is unavailable", ordered[0])
		}
		if !m.selectableLocked(up, now) {
			return nil, fmt.Errorf("upstream %q is unavailable", ordered[0])
		}
		return up, nil
	}
	if m.mode == ModeManual && contains(m.manualTag) {
		if up := m.upstreams[m.manualTag]; up != nil && m.selectableLocked(up, now) {
			return up, nil
		}
	}
	if m.mode == ModeCoordination && !m.coordFallback && contains(m.coordTag) {
		if up := m.upstreams[m.coordTag]; up != nil && m.selectableLocked(up, now) {
			return up, nil
		}
	}
	best, _ := m.selectBestLocked(ordered)
	if best == "" {
		return nil, errors.New("no usable upstream in route")
	}
	return m.upstreams[best], nil
}

func hasTag(tags map[string]struct{}, tag string) bool {
	_, ok := tags[tag]
	return ok
}

func (m *UpstreamManager) UpdateReachability(tag string, reachable bool) UpstreamStats {
	return m.applyObservation(tag, ProbeObservation{Success: reachable, Protocol: "icmp", ObservedAt: time.Now()})
}

func (m *UpstreamManager) UpdateMeasurement(tag string, result *MeasurementResult, health ...config.HealthConfig) UpstreamStats {
	if result == nil || !sanitizeMeasurementResult(result) {
		return UpstreamStats{}
	}
	return m.applyObservation(tag, ProbeObservation{
		Success:    result.RTTMs > 0,
		RTT:        time.Duration(result.RTTMs * float64(time.Millisecond)),
		Protocol:   result.Network,
		ObservedAt: result.Timestamp,
	}, health...)
}

func (m *UpstreamManager) RecordProbeFailure(tag, protocol string, observedAt time.Time) UpstreamStats {
	return m.applyObservation(tag, ProbeObservation{Protocol: protocol, ObservedAt: observedAt})
}

func (m *UpstreamManager) applyObservation(tag string, observation ProbeObservation, cfg ...config.HealthConfig) UpstreamStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	up := m.upstreams[tag]
	if up == nil {
		return UpstreamStats{}
	}
	if len(cfg) > 0 {
		m.healthConfig = cfg[0]
	}
	previous := up.stats
	up.health = ApplyObservation(up.health, observation, m.healthConfig)
	m.refreshStatsLocked(up)
	if previous.HealthState != up.stats.HealthState {
		reason := string(up.stats.HealthState)
		util.Event(m.logger, slog.LevelInfo, "upstream.health_changed", "upstream", tag, "health.state", reason)
		if m.onStateChange != nil {
			m.onStateChange(UsabilityChange{Tag: tag, Usable: up.stats.Usable, Reason: reason})
		}
		if tag == m.coordTag && !up.stats.Usable {
			m.activateCoordinationFallbackLocked("health_down")
		}
	}
	m.evaluateSwitchingLocked(tag)
	return up.stats
}

func (m *UpstreamManager) refreshStatsLocked(up *Upstream) {
	state := EffectiveHealth(up.health, time.Now(), m.healthConfig.StaleThreshold.Duration())
	up.stats.HealthState = state
	up.stats.Reachable = state == HealthHealthy || state == HealthStale
	up.stats.Usable = state != HealthDown
	up.stats.LastReachable = up.health.LastSuccessAt
	up.stats.RTTMs = up.health.RTTMs()
	up.stats.ConsecutiveSuccesses = up.health.ConsecutiveSuccesses
	up.stats.ConsecutiveFailures = up.health.ConsecutiveFailures
}

func (m *UpstreamManager) evaluateSwitchingLocked(updated string) {
	if m.mode == ModeManual || (m.mode == ModeCoordination && !m.coordFallback) {
		return
	}
	active := m.upstreams[m.activeTag]
	if active == nil || !m.selectableLocked(active, time.Now()) {
		if best, _ := m.selectBestLocked(nil); best != "" {
			m.setActiveLocked(best, "health_failover")
		}
		return
	}
	best, _ := m.selectBestLocked(nil)
	if best == "" || best == m.activeTag {
		m.resetPendingLocked()
		return
	}
	candidate := m.upstreams[best]
	if candidate.stats.HealthState != HealthHealthy || active.stats.HealthState != HealthHealthy || candidate.stats.RTTMs <= 0 || active.stats.RTTMs-candidate.stats.RTTMs < float64(m.switching.LatencyImprovement.Duration()/time.Millisecond) {
		m.resetPendingLocked()
		return
	}
	if m.pendingSwitch != best {
		m.pendingSwitch, m.pendingSince = best, time.Now()
		return
	}
	if time.Since(m.pendingSince) < m.switching.ConfirmDuration.Duration() || (!m.lastSwitch.IsZero() && time.Since(m.lastSwitch) < m.switching.MinHoldTime.Duration()) {
		return
	}
	m.setActiveLocked(best, "latency_improvement")
	_ = updated
}

func (m *UpstreamManager) MarkDialFailure(tag string, cooldown time.Duration) {
	if cooldown <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	up := m.upstreams[tag]
	if up == nil {
		return
	}
	up.dialFailUntil = time.Now().Add(cooldown)
	up.dialFailCount++
	util.Event(m.logger, slog.LevelWarn, "upstream.dial_failure_marked", "upstream", tag, "dial_fail_count", up.dialFailCount)
	if up.dialFailCount >= dialFailSwitchCount && tag == m.activeTag {
		if best, _ := m.selectBestLocked(nil); best != "" {
			m.setActiveLocked(best, "dial_failover")
		}
	}
}

func (m *UpstreamManager) ClearDialFailure(tag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if up := m.upstreams[tag]; up != nil {
		up.dialFailUntil = time.Time{}
		up.dialFailCount = 0
	}
}

func (m *UpstreamManager) UpdateResolved(tag string, ips []net.IP) bool {
	if len(ips) == 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	up := m.upstreams[tag]
	if up == nil {
		return false
	}
	changed := !sameIPs(up.IPs, ips)
	old := up.ActiveIP()
	up.IPs = ips
	if old == nil || !containsIP(ips, old) {
		up.SetActiveIP(ips[0])
		changed = true
	}
	return changed
}

func (m *UpstreamManager) Snapshot() []UpstreamSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, up := range m.upstreams {
		m.refreshStatsLocked(up)
	}
	out := make([]UpstreamSnapshot, 0, len(m.order))
	for _, tag := range m.order {
		up := m.upstreams[tag]
		if up == nil {
			continue
		}
		ips := make([]string, 0, len(up.IPs))
		for _, ip := range up.IPs {
			ips = append(ips, ip.String())
		}
		activeIP := ""
		if ip := up.ActiveIP(); ip != nil {
			activeIP = ip.String()
		}
		out = append(out, UpstreamSnapshot{Tag: up.Tag, Host: up.Host, IPs: ips, ActiveIP: activeIP, Active: tag == m.activeTag, Usable: up.stats.Usable, Reachable: up.stats.Reachable, HealthState: up.stats.HealthState, RTTMs: up.stats.RTTMs})
	}
	return out
}

func (m *UpstreamManager) selectBestLocked(tags []string) (string, float64) {
	allowed := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		allowed[tag] = struct{}{}
	}
	var best *Upstream
	bestTag := ""
	now := time.Now()
	for _, tag := range m.order {
		if len(allowed) > 0 && !hasTag(allowed, tag) {
			continue
		}
		up := m.upstreams[tag]
		if !m.selectableLocked(up, now) {
			continue
		}
		if best == nil || m.betterLocked(up, best, tag == m.activeTag) {
			best, bestTag = up, tag
		}
	}
	if best == nil {
		return "", 0
	}
	return bestTag, best.stats.RTTMs
}

func (m *UpstreamManager) selectableLocked(up *Upstream, now time.Time) bool {
	return up != nil && up.stats.HealthState != HealthDown && !up.dialFailUntil.After(now)
}

func healthRank(state HealthState) int {
	switch state {
	case HealthHealthy:
		return 0
	case HealthStale:
		return 1
	case HealthUnknown:
		return 2
	default:
		return 3
	}
}

func (m *UpstreamManager) betterLocked(a, b *Upstream, aCurrent bool) bool {
	ra, rb := healthRank(a.stats.HealthState), healthRank(b.stats.HealthState)
	if ra != rb {
		return ra < rb
	}
	if a.stats.RTTMs > 0 && b.stats.RTTMs > 0 && a.stats.RTTMs != b.stats.RTTMs {
		return a.stats.RTTMs < b.stats.RTTMs
	}
	if a.stats.RTTMs > 0 && b.stats.RTTMs == 0 {
		return true
	}
	if a.stats.RTTMs == 0 && b.stats.RTTMs > 0 {
		return false
	}
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if aCurrent != (b.Tag == m.activeTag) {
		return aCurrent
	}
	return false
}

func (m *UpstreamManager) setActiveLocked(tag, reason string) {
	if tag == m.activeTag {
		return
	}
	old := m.activeTag
	m.activeTag = tag
	m.lastSwitch = time.Now()
	m.resetPendingLocked()
	util.Event(m.logger, slog.LevelInfo, "upstream.active_changed", "switch.from", old, "switch.to", tag, "switch.reason", reason)
	if m.onSelect != nil {
		m.onSelect(ActiveChange{OldTag: old, NewTag: tag, Reason: reason})
	}
}

func (m *UpstreamManager) coordinationStateLocked() CoordinationState {
	return CoordinationState{Connected: m.coordConnected, Authoritative: m.coordConnected && !m.coordFallback && m.coordTag != "", SelectedUpstream: m.coordTag, Version: m.coordVersion, FallbackActive: m.coordFallback}
}

func (m *UpstreamManager) emitCoordinationStateLocked() {
	if m.onCoordState != nil {
		m.onCoordState(m.coordinationStateLocked())
	}
}

func (m *UpstreamManager) resetPendingLocked() {
	m.pendingSwitch = ""
	m.pendingSince = time.Time{}
}

func (m *UpstreamManager) clearCoordinationLocked() {
	m.coordConnected, m.coordTag, m.coordVersion, m.coordFallback = false, "", 0, false
}

func (m *UpstreamManager) activateCoordinationFallbackLocked(reason string) {
	m.coordTag = ""
	m.coordFallback = m.mode == ModeCoordination
	if m.coordFallback {
		if best, _ := m.selectBestLocked(nil); best != "" {
			m.setActiveLocked(best, reason)
		}
	}
	m.emitCoordinationStateLocked()
}

func (u *Upstream) SetActiveIP(ip net.IP) {
	if ip == nil {
		return
	}
	clone := append(net.IP(nil), ip...)
	u.activeIP.Store(clone)
}

func (u *Upstream) ActiveIP() net.IP {
	value := u.activeIP.Load()
	if value == nil {
		return nil
	}
	if ip, ok := value.(net.IP); ok {
		return append(net.IP(nil), ip...)
	}
	return nil
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

func containsIP(ips []net.IP, target net.IP) bool {
	for _, ip := range ips {
		if ip.Equal(target) {
			return true
		}
	}
	return false
}

func sanitizeMeasurementResult(result *MeasurementResult) bool {
	if result == nil || math.IsNaN(result.RTTMs) || math.IsInf(result.RTTMs, 0) || result.RTTMs <= 0 {
		return false
	}
	if result.Timestamp.IsZero() {
		result.Timestamp = time.Now()
	}
	if result.RTTMs > 10000 {
		result.RTTMs = 10000
	}
	return true
}

type UpstreamSnapshot struct {
	Tag         string      `json:"tag"`
	Host        string      `json:"host"`
	IPs         []string    `json:"ips"`
	ActiveIP    string      `json:"active_ip"`
	Active      bool        `json:"active"`
	Usable      bool        `json:"usable"`
	Reachable   bool        `json:"reachable"`
	HealthState HealthState `json:"health_state"`
	RTTMs       float64     `json:"rtt_ms"`
}
