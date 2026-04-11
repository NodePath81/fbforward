package notify

import (
	"sync"
	"time"
)

const (
	sustainedAlertDelay = 30 * time.Second
	startupQuietPeriod  = 5 * time.Minute
)

type PolicyConfig struct {
	StartTime            time.Time
	CoordinationEndpoint string
	Now                  func() time.Time
	AfterFunc            func(time.Duration, func()) timer
}

type timer interface {
	Stop() bool
}

type afterFuncTimer struct {
	timer *time.Timer
}

func (t afterFuncTimer) Stop() bool {
	if t.timer == nil {
		return false
	}
	return t.timer.Stop()
}

type Policy struct {
	emitter Emitter
	now     func() time.Time
	after   func(time.Duration, func()) timer

	mu                   sync.Mutex
	closed               bool
	startTime            time.Time
	coordinationEndpoint string
	activeEmpty          bool
	outageAlerted        bool
	outageTimer          timer
	coordEverConnected   bool
	coordConnected       bool
	coordAlerted         bool
	coordTimer           timer
	coordAuthoritative   bool
	authorityAlerted     bool
	authorityTimer       timer
}

func NewPolicy(emitter Emitter, cfg PolicyConfig) *Policy {
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	afterFn := cfg.AfterFunc
	if afterFn == nil {
		afterFn = func(delay time.Duration, fn func()) timer {
			return afterFuncTimer{timer: time.AfterFunc(delay, fn)}
		}
	}
	startTime := cfg.StartTime
	if startTime.IsZero() {
		startTime = nowFn()
	}
	return &Policy{
		emitter:              emitter,
		now:                  nowFn,
		after:                afterFn,
		startTime:            startTime,
		coordinationEndpoint: cfg.CoordinationEndpoint,
	}
}

func (p *Policy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.stopOutageTimerLocked()
	p.stopCoordTimerLocked()
	p.stopAuthorityTimerLocked()
}

func (p *Policy) HandleActiveChange(oldTag, newTag, reason string, previousScore, nextScore float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}

	if newTag == "" {
		p.activeEmpty = true
		p.scheduleOutageLocked()
		return
	}

	p.activeEmpty = false
	p.stopOutageTimerLocked()

	switch reason {
	case "failover_loss", "failover_retrans", "failover_dial", "coordination_fallback":
		p.emitLocked("upstream.active_changed", SeverityWarn, map[string]any{
			"switch.from":   oldTag,
			"switch.to":     newTag,
			"switch.reason": reason,
		})
	}
}

func (p *Policy) HandleUsabilityChange(_ string, usable bool, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || !usable {
		return
	}

	p.stopOutageTimerLocked()
	if p.outageAlerted {
		p.emitLocked("upstream.active_cleared", SeverityInfo, map[string]any{
			"notification.state": "resolved",
		})
		p.outageAlerted = false
	}
}

func (p *Policy) HandleCoordinationConnection(connected bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}

	if connected {
		p.coordEverConnected = true
		p.coordConnected = true
		p.coordAlerted = false
		p.stopCoordTimerLocked()
		if !p.coordAuthoritative && !p.authorityAlerted && p.authorityTimer == nil {
			p.scheduleAuthorityLocked()
		}
		return
	}

	p.coordConnected = false
	p.stopCoordTimerLocked()
	if !p.coordEverConnected || p.coordAlerted {
		return
	}
	p.coordTimer = p.after(sustainedAlertDelay, func() {
		p.fireCoordinationAlert()
	})
}

func (p *Policy) HandleCoordinationAuthority(authoritative bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}

	p.coordAuthoritative = authoritative
	if authoritative {
		p.stopAuthorityTimerLocked()
		if p.authorityAlerted {
			p.emitLocked("coordination.authority_lost", SeverityInfo, map[string]any{
				"coordination.endpoint": p.coordinationEndpoint,
				"notification.state":    "resolved",
			})
			p.authorityAlerted = false
		}
		return
	}

	if !p.coordEverConnected || p.authorityAlerted || p.authorityTimer != nil {
		return
	}
	p.scheduleAuthorityLocked()
}

func (p *Policy) fireOutageAlert() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.outageTimer = nil
	if !p.activeEmpty || p.outageAlerted {
		return
	}

	uptime := p.now().Sub(p.startTime)
	if uptime < startupQuietPeriod {
		p.outageTimer = p.after(startupQuietPeriod-uptime, func() {
			p.fireOutageAlert()
		})
		return
	}

	p.emitLocked("upstream.active_cleared", SeverityCritical, map[string]any{
		"notification.state": "active",
	})
	p.outageAlerted = true
}

func (p *Policy) fireCoordinationAlert() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.coordTimer = nil
	if p.coordConnected || !p.coordEverConnected || p.coordAlerted {
		return
	}
	p.emitLocked("coordination.session_ended", SeverityWarn, map[string]any{
		"coordination.endpoint": p.coordinationEndpoint,
	})
	p.coordAlerted = true
}

func (p *Policy) fireAuthorityAlert() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.authorityTimer = nil
	if p.coordAuthoritative || !p.coordEverConnected || p.authorityAlerted {
		return
	}
	p.emitLocked("coordination.authority_lost", SeverityWarn, map[string]any{
		"coordination.endpoint": p.coordinationEndpoint,
		"notification.state":    "active",
	})
	p.authorityAlerted = true
}

func (p *Policy) scheduleOutageLocked() {
	if p.outageAlerted {
		return
	}
	p.stopOutageTimerLocked()
	delay := sustainedAlertDelay
	uptime := p.now().Sub(p.startTime)
	if uptime < startupQuietPeriod {
		remainingQuiet := startupQuietPeriod - uptime
		if remainingQuiet > delay {
			delay = remainingQuiet
		}
	}
	p.outageTimer = p.after(delay, func() {
		p.fireOutageAlert()
	})
}

func (p *Policy) stopOutageTimerLocked() {
	if p.outageTimer != nil {
		p.outageTimer.Stop()
		p.outageTimer = nil
	}
}

func (p *Policy) stopCoordTimerLocked() {
	if p.coordTimer != nil {
		p.coordTimer.Stop()
		p.coordTimer = nil
	}
}

func (p *Policy) scheduleAuthorityLocked() {
	p.authorityTimer = p.after(sustainedAlertDelay, func() {
		p.fireAuthorityAlert()
	})
}

func (p *Policy) stopAuthorityTimerLocked() {
	if p.authorityTimer != nil {
		p.authorityTimer.Stop()
		p.authorityTimer = nil
	}
}

func (p *Policy) emitLocked(eventName string, severity Severity, attributes map[string]any) {
	if p.emitter == nil {
		return
	}
	p.emitter.Emit(eventName, severity, attributes)
}
