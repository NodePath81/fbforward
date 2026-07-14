package upstream

import (
	"net"
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
	m.RecordProbe("primary", ProbeObservation{Success: true, RTT: 5 * time.Millisecond, ObservedAt: time.Now().Add(-2 * time.Second)})

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

func TestRouteSelectorStaticOverrideDoesNotFallback(t *testing.T) {
	a := testUpstream("a", HealthDown, time.Millisecond, 0)
	b := testUpstream("b", HealthHealthy, time.Millisecond, 0)
	a.SetActiveIP(net.ParseIP("192.0.2.1"))
	b.SetActiveIP(net.ParseIP("192.0.2.2"))
	m := NewUpstreamManager([]*Upstream{a, b}, nil)
	selector := NewRouteSelector(m, []config.RouteConfig{{Name: "web", Strategy: "static", Upstreams: []string{"a", "b"}, DefaultUpstream: "a"}})
	if _, _, err := selector.Pick("web"); err != nil {
		t.Fatalf("static route should ignore health: %v", err)
	}
	if err := m.SetManual("a"); err != nil {
		// SetManual is expected to reject down upstreams; this branch only
		// verifies static selection does not rely on that global state.
		_ = err
	}
	m.MarkDialFailure("a", time.Minute)
	if _, _, err := selector.Pick("web"); err == nil {
		t.Fatal("expected static route to fail without fallback during cooldown")
	}
	if err := selector.SetOverride("web", "b"); err != nil {
		t.Fatal(err)
	}
	selected, status, err := selector.Pick("web")
	if err != nil || selected.Tag != "b" || status.OverrideState != OverrideActive {
		t.Fatalf("unexpected static override: selected=%v status=%+v err=%v", selected, status, err)
	}
}

func TestRouteSelectorAdaptiveOverrideFallsBackAndRecovers(t *testing.T) {
	a := &Upstream{Tag: "a"}
	b := &Upstream{Tag: "b"}
	a.SetActiveIP(net.ParseIP("192.0.2.1"))
	b.SetActiveIP(net.ParseIP("192.0.2.2"))
	m := NewUpstreamManager([]*Upstream{a, b}, nil)
	m.SetHealthConfig(config.HealthConfig{RTTEWMAAlpha: 0.25, FailureThreshold: 3, RecoveryThreshold: 2, StaleThreshold: config.Duration(time.Minute)})
	m.RecordProbe("b", ProbeObservation{Success: true, RTT: 10 * time.Millisecond, ObservedAt: time.Now()})
	for i := 0; i < 3; i++ {
		m.RecordProbeFailure("a", time.Now())
	}
	selector := NewRouteSelector(m, []config.RouteConfig{{Name: "proxy", Strategy: "adaptive", Upstreams: []string{"a", "b"}}})
	if err := selector.SetOverride("proxy", "a"); err != nil {
		t.Fatal(err)
	}
	selected, status, err := selector.Pick("proxy")
	if err != nil || selected.Tag != "b" || status.OverrideState != OverrideFallback {
		t.Fatalf("expected adaptive fallback: selected=%v status=%+v err=%v", selected, status, err)
	}
	m.RecordProbe("a", ProbeObservation{Success: true, RTT: 5 * time.Millisecond, ObservedAt: time.Now()})
	m.RecordProbe("a", ProbeObservation{Success: true, RTT: 5 * time.Millisecond, ObservedAt: time.Now()})
	selected, status, err = selector.Pick("proxy")
	if err != nil || selected.Tag != "a" || status.OverrideState != OverrideActive {
		t.Fatalf("expected override recovery: selected=%v status=%+v err=%v", selected, status, err)
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
