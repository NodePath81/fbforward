package upstream

import (
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
)

func TestHealthTransitionsAndEWMA(t *testing.T) {
	cfg := config.HealthConfig{RTTEWMAAlpha: 0.5, FailureThreshold: 2, RecoveryThreshold: 2, StaleThreshold: config.Duration(time.Minute)}
	var state HealthSnapshot
	state = ApplyObservation(state, ProbeObservation{Success: true, RTT: 100 * time.Millisecond, ObservedAt: time.Unix(1, 0)}, cfg)
	if state.State != HealthHealthy || state.RTT != 100*time.Millisecond {
		t.Fatalf("unexpected first health snapshot: %+v", state)
	}
	state = ApplyObservation(state, ProbeObservation{Success: true, RTT: 40 * time.Millisecond, ObservedAt: time.Unix(2, 0)}, cfg)
	if state.RTT != 70*time.Millisecond {
		t.Fatalf("expected EWMA RTT 70ms, got %s", state.RTT)
	}
	state = ApplyObservation(state, ProbeObservation{ObservedAt: time.Unix(3, 0)}, cfg)
	state = ApplyObservation(state, ProbeObservation{ObservedAt: time.Unix(4, 0)}, cfg)
	if state.State != HealthDown {
		t.Fatalf("expected down after failures, got %s", state.State)
	}
	state = ApplyObservation(state, ProbeObservation{Success: true, RTT: 20 * time.Millisecond, ObservedAt: time.Unix(5, 0)}, cfg)
	if state.State != HealthDown {
		t.Fatalf("expected recovery threshold to require two successes, got %s", state.State)
	}
	state = ApplyObservation(state, ProbeObservation{Success: true, RTT: 20 * time.Millisecond, ObservedAt: time.Unix(6, 0)}, cfg)
	if state.State != HealthHealthy {
		t.Fatalf("expected recovery, got %s", state.State)
	}
}

func TestEffectiveHealthStale(t *testing.T) {
	s := HealthSnapshot{State: HealthHealthy, LastSuccessAt: time.Unix(1, 0)}
	if got := EffectiveHealth(s, time.Unix(62, 0), time.Minute); got != HealthStale {
		t.Fatalf("expected stale, got %s", got)
	}
}

func TestStatsSnapshotRefreshesStaleState(t *testing.T) {
	m := NewUpstreamManager([]*Upstream{{Tag: "primary"}}, nil)
	m.SetHealthConfig(config.HealthConfig{
		RTTEWMAAlpha:      0.25,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		StaleThreshold:    config.Duration(time.Second),
	})
	m.UpdateMeasurement("primary", &MeasurementResult{RTTMs: 5, Timestamp: time.Now().Add(-2 * time.Second)})

	stats := m.StatsSnapshot()
	if got := stats["primary"].HealthState; got != HealthStale {
		t.Fatalf("expected stale state in refreshed stats, got %s", got)
	}
}

func TestRouteSelectionUsesHealthRTTPriorityAndOrder(t *testing.T) {
	m := NewUpstreamManager([]*Upstream{
		testUpstream("slow", HealthHealthy, 100*time.Millisecond, 1),
		testUpstream("fast", HealthHealthy, 20*time.Millisecond, 0),
		testUpstream("down", HealthDown, 1*time.Millisecond, 100),
	}, nil)
	up, err := m.SelectUpstreamFrom([]string{"slow", "fast", "down"})
	if err != nil || up.Tag != "fast" {
		t.Fatalf("expected fast healthy upstream, got %v, %v", up, err)
	}
	up, err = m.SelectUpstreamFrom([]string{"slow"})
	if err != nil || up.Tag != "slow" {
		t.Fatalf("expected static route pinning, got %v, %v", up, err)
	}
}

func TestRouteSelectionPrefersHealthyOverLowerRTTStale(t *testing.T) {
	m := NewUpstreamManager([]*Upstream{
		testUpstream("healthy", HealthHealthy, 100*time.Millisecond, 0),
		testUpstream("stale", HealthStale, 1*time.Millisecond, 100),
	}, nil)
	up, err := m.SelectUpstreamFrom([]string{"healthy", "stale"})
	if err != nil || up.Tag != "healthy" {
		t.Fatalf("expected healthy upstream to win over stale RTT, got %v, %v", up, err)
	}
}

func TestCoordinationPickAndFallback(t *testing.T) {
	m := NewUpstreamManager([]*Upstream{
		testUpstream("alpha", HealthHealthy, 20*time.Millisecond, 1),
		testUpstream("beta", HealthHealthy, 30*time.Millisecond, 0),
	}, nil)
	m.SetCoordination()
	m.SetCoordinationConnected(true)
	if _, err := m.ApplyCoordinationPick(1, "beta"); err != nil {
		t.Fatal(err)
	}
	if m.ActiveTag() != "beta" || !m.CoordinationState().Authoritative {
		t.Fatalf("coordination pick not applied: %+v", m.CoordinationState())
	}
	m.RecordProbeFailure("beta", time.Now())
	m.RecordProbeFailure("beta", time.Now())
	m.RecordProbeFailure("beta", time.Now())
	if !m.CoordinationState().FallbackActive {
		t.Fatalf("expected fallback after coordinated upstream down")
	}
}

func testUpstream(tag string, state HealthState, rtt time.Duration, priority float64) *Upstream {
	health := HealthSnapshot{State: state, RTT: rtt}
	if state == HealthHealthy || state == HealthStale {
		health.LastSuccessAt = time.Now()
		if state == HealthStale {
			health.LastSuccessAt = time.Now().Add(-2 * time.Minute)
		}
	}
	return &Upstream{Tag: tag, Priority: priority, health: health, stats: UpstreamStats{HealthState: state, Usable: state != HealthDown, Reachable: state == HealthHealthy, RTTMs: float64(rtt) / float64(time.Millisecond)}}
}
