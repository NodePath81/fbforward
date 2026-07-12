package upstream

import (
	"time"

	"github.com/NodePath81/fbforward/internal/config"
)

// HealthState is the only health dimension used by route selection.
type HealthState string

const (
	HealthUnknown HealthState = "unknown"
	HealthHealthy HealthState = "healthy"
	HealthDown    HealthState = "down"
	HealthStale   HealthState = "stale"
)

type ProbeObservation struct {
	Success    bool
	RTT        time.Duration
	Protocol   string
	ObservedAt time.Time
}

type HealthSnapshot struct {
	State                HealthState
	RTT                  time.Duration
	LastSuccessAt        time.Time
	LastAttemptAt        time.Time
	ConsecutiveSuccesses int
	ConsecutiveFailures  int
}

// ApplyObservation applies one completed measurement cycle. A cycle is
// successful when at least one enabled probe succeeded; protocol-specific
// details are intentionally not part of selection.
func ApplyObservation(previous HealthSnapshot, observation ProbeObservation, cfg config.HealthConfig) HealthSnapshot {
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now()
	}
	if cfg.RTTEWMAAlpha <= 0 || cfg.RTTEWMAAlpha > 1 {
		cfg.RTTEWMAAlpha = 0.25
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.RecoveryThreshold <= 0 {
		cfg.RecoveryThreshold = 2
	}

	next := previous
	next.LastAttemptAt = observation.ObservedAt
	if !observation.Success {
		next.ConsecutiveFailures++
		next.ConsecutiveSuccesses = 0
		if next.ConsecutiveFailures >= cfg.FailureThreshold {
			next.State = HealthDown
		}
		return next
	}

	next.ConsecutiveSuccesses++
	next.ConsecutiveFailures = 0
	next.LastSuccessAt = observation.ObservedAt
	if observation.RTT > 0 {
		if next.RTT <= 0 {
			next.RTT = observation.RTT
		} else {
			next.RTT = time.Duration(float64(next.RTT)*(1-cfg.RTTEWMAAlpha) + float64(observation.RTT)*cfg.RTTEWMAAlpha)
		}
	}
	if previous.State == HealthDown {
		if next.ConsecutiveSuccesses >= cfg.RecoveryThreshold {
			next.State = HealthHealthy
		}
	} else {
		next.State = HealthHealthy
	}
	return next
}

func EffectiveHealth(snapshot HealthSnapshot, now time.Time, stale time.Duration) HealthState {
	if snapshot.State == HealthDown {
		return HealthDown
	}
	if snapshot.LastSuccessAt.IsZero() {
		return snapshot.State
	}
	if stale > 0 && now.Sub(snapshot.LastSuccessAt) > stale {
		return HealthStale
	}
	return snapshot.State
}

func (s HealthSnapshot) RTTMs() float64 {
	if s.RTT <= 0 {
		return 0
	}
	return float64(s.RTT) / float64(time.Millisecond)
}
