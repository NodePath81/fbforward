package notify

import (
	"sync"
	"time"
)

const (
	defaultStartupGracePeriod        = 5 * time.Minute
	defaultUnusableInterval          = 30 * time.Second
	defaultNotifyInterval            = 30 * time.Minute
	coordinationDisconnectAlertDelay = 30 * time.Second
)

type PolicyConfig struct {
	StartTime            time.Time
	CoordinationEndpoint string
	StartupGracePeriod   time.Duration
	UnusableInterval     time.Duration
	NotifyInterval       time.Duration
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

	startupGracePeriod time.Duration
	unusableInterval   time.Duration
	notifyInterval     time.Duration

	mu                   sync.Mutex
	closed               bool
	startTime            time.Time
	coordinationEndpoint string
	unusable             map[string]*unusableAlertState
	coordEverConnected   bool
	coordConnected       bool
	coordAlerted         bool
	coordTimer           timer
}

type unusableAlertState struct {
	reason string
	timer  timer
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
	startupGracePeriod := cfg.StartupGracePeriod
	if startupGracePeriod <= 0 {
		startupGracePeriod = defaultStartupGracePeriod
	}
	unusableInterval := cfg.UnusableInterval
	if unusableInterval <= 0 {
		unusableInterval = defaultUnusableInterval
	}
	notifyInterval := cfg.NotifyInterval
	if notifyInterval <= 0 {
		notifyInterval = defaultNotifyInterval
	}
	return &Policy{
		emitter:              emitter,
		now:                  nowFn,
		after:                afterFn,
		startupGracePeriod:   startupGracePeriod,
		unusableInterval:     unusableInterval,
		notifyInterval:       notifyInterval,
		startTime:            startTime,
		coordinationEndpoint: cfg.CoordinationEndpoint,
		unusable:             make(map[string]*unusableAlertState),
	}
}

func (p *Policy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, state := range p.unusable {
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
	}
	p.stopCoordTimerLocked()
}

func (p *Policy) HandleUsabilityChange(tag string, usable bool, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}

	if usable {
		p.clearUnusableLocked(tag)
		return
	}

	state := p.unusable[tag]
	if state == nil {
		state = &unusableAlertState{}
		p.unusable[tag] = state
	}
	state.reason = reason
	if state.timer != nil {
		return
	}
	delay := p.initialUnusableDelayLocked()
	state.timer = p.after(delay, func() {
		p.fireUnusableAlert(tag)
	})
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
		return
	}

	p.coordConnected = false
	p.stopCoordTimerLocked()
	if !p.coordEverConnected || p.coordAlerted {
		return
	}
	p.coordTimer = p.after(coordinationDisconnectAlertDelay, func() {
		p.fireCoordinationAlert()
	})
}

func (p *Policy) fireUnusableAlert(tag string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	state := p.unusable[tag]
	if state == nil {
		return
	}
	state.timer = nil

	uptime := p.now().Sub(p.startTime)
	if uptime < p.startupGracePeriod {
		state.timer = p.after(p.startupGracePeriod-uptime, func() {
			p.fireUnusableAlert(tag)
		})
		return
	}
	p.emitLocked("upstream.unusable", SeverityWarn, map[string]any{
		"upstream.tag":    tag,
		"upstream.reason": state.reason,
	})
	state.timer = p.after(p.notifyInterval, func() {
		p.fireUnusableAlert(tag)
	})
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

func (p *Policy) initialUnusableDelayLocked() time.Duration {
	delay := p.unusableInterval
	uptime := p.now().Sub(p.startTime)
	if uptime < p.startupGracePeriod {
		remainingGrace := p.startupGracePeriod - uptime
		if remainingGrace > delay {
			delay = remainingGrace
		}
	}
	return delay
}

func (p *Policy) clearUnusableLocked(tag string) {
	state := p.unusable[tag]
	if state == nil {
		return
	}
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	delete(p.unusable, tag)
}

func (p *Policy) stopCoordTimerLocked() {
	if p.coordTimer != nil {
		p.coordTimer.Stop()
		p.coordTimer = nil
	}
}

func (p *Policy) emitLocked(eventName string, severity Severity, attributes map[string]any) {
	if p.emitter == nil {
		return
	}
	p.emitter.Emit(eventName, severity, attributes)
}
