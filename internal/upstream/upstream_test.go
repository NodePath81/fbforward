package upstream

import (
	"math/rand"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
)

func TestComputeFullScoreOrdering(t *testing.T) {
	cfg := config.DefaultScoringConfig()
	now := time.Now()

	better := UpstreamStats{
		Reachable:     true,
		RTTMs:         10,
		JitterMs:      5,
		LossRate:      0.0,
		RetransRate:   0.0,
		LastTCPUpdate: now,
		LastUDPUpdate: now,
	}
	worse := better
	worse.RTTMs = 30
	worse.JitterMs = 15
	worse.LossRate = 0.1
	worse.RetransRate = 0.1

	_, _, scoreBetter := computeFullScore(better, cfg, 0, 0)
	_, _, scoreWorse := computeFullScore(worse, cfg, 0, 0)
	if scoreBetter <= scoreWorse {
		t.Fatalf("expected better upstream to score higher (better=%v worse=%v)", scoreBetter, scoreWorse)
	}
}

func TestComputeFullScoreStalenessPenalty(t *testing.T) {
	cfg := config.DefaultScoringConfig()
	now := time.Now()

	fresh := UpstreamStats{
		Reachable:     true,
		RTTMs:         10,
		JitterMs:      5,
		LossRate:      0.0,
		RetransRate:   0.0,
		LastTCPUpdate: now,
		LastUDPUpdate: now,
	}
	stale := fresh
	stale.LastTCPUpdate = now.Add(-time.Hour)

	_, _, freshScore := computeFullScore(fresh, cfg, 0, 30*time.Second)
	_, _, staleScore := computeFullScore(stale, cfg, 0, 30*time.Second)
	if staleScore >= freshScore {
		t.Fatalf("expected stale score < fresh score, got stale=%v fresh=%v", staleScore, freshScore)
	}
}

func TestComputeFullScoreBiasTransform(t *testing.T) {
	cfg := config.DefaultScoringConfig()
	now := time.Now()
	stats := UpstreamStats{
		Reachable:     true,
		RTTMs:         15,
		JitterMs:      5,
		LossRate:      0.0,
		RetransRate:   0.0,
		LastTCPUpdate: now,
		LastUDPUpdate: now,
	}
	_, _, neutral := computeFullScore(stats, cfg, 0, 0)
	_, _, biasedUp := computeFullScore(stats, cfg, 0.5, 0)
	_, _, biasedDown := computeFullScore(stats, cfg, -0.5, 0)

	if biasedUp <= neutral {
		t.Fatalf("expected positive bias to increase score (neutral=%v up=%v)", neutral, biasedUp)
	}
	if biasedDown >= neutral {
		t.Fatalf("expected negative bias to decrease score (neutral=%v down=%v)", neutral, biasedDown)
	}
}

func TestApplyEMA(t *testing.T) {
	init := false
	val1 := applyEMA(100, 0, 0.2, &init)
	if !init || val1 != 100 {
		t.Fatalf("expected first call to return new value and initialize, got %v init=%v", val1, init)
	}
	val2 := applyEMA(50, val1, 0.2, &init)
	if val2 != 90 {
		t.Fatalf("expected EMA 0.2 between 100 and 50 to be 90, got %v", val2)
	}
	val3 := applyEMA(100, val2, 0.2, &init)
	if val3 != 92 {
		t.Fatalf("expected EMA second iteration to be 92, got %v", val3)
	}
}

func TestModeStringCoordination(t *testing.T) {
	if got := ModeCoordination.String(); got != "coordination" {
		t.Fatalf("expected coordination mode string, got %q", got)
	}
}

func TestRankedTagsExcludesUnavailableAndPreservesConfigOrder(t *testing.T) {
	manager := newTestManager(
		testUpstream("alpha", 80, true),
		testUpstream("beta", 80, true),
		testUpstream("gamma", 95, true),
		testUpstream("delta", 70, true),
	)
	manager.upstreams["delta"].dialFailUntil = time.Now().Add(time.Minute)

	got := manager.RankedTags()
	want := []string{"gamma", "alpha", "beta"}
	if len(got) != len(want) {
		t.Fatalf("expected %v ranked tags, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected ranked tags %v, got %v", want, got)
		}
	}
}

func TestCoordinationPickApplied(t *testing.T) {
	manager := newTestManager(
		testUpstream("alpha", 90, true),
		testUpstream("beta", 70, true),
	)

	manager.SetCoordination()
	applied, err := manager.ApplyCoordinationPick(1, "beta")
	if err != nil {
		t.Fatalf("expected valid coordination pick, got error %v", err)
	}
	if !applied {
		t.Fatalf("expected coordination pick to apply")
	}

	state := manager.CoordinationState()
	if state.FallbackActive || state.SelectedUpstream != "beta" || state.Version != 1 {
		t.Fatalf("unexpected coordination state: %+v", state)
	}
	if manager.ActiveTag() != "beta" {
		t.Fatalf("expected active upstream beta, got %q", manager.ActiveTag())
	}
}

func TestCoordinationStaleVersionIgnored(t *testing.T) {
	manager := newTestManager(
		testUpstream("alpha", 90, true),
		testUpstream("beta", 70, true),
	)

	manager.SetCoordination()
	if _, err := manager.ApplyCoordinationPick(2, "beta"); err != nil {
		t.Fatalf("expected first coordination pick to apply: %v", err)
	}
	applied, err := manager.ApplyCoordinationPick(1, "alpha")
	if err == nil {
		t.Fatalf("expected stale coordination version error")
	}
	if applied {
		t.Fatalf("expected stale coordination pick to be ignored")
	}
	if manager.ActiveTag() != "beta" {
		t.Fatalf("expected active upstream to remain beta, got %q", manager.ActiveTag())
	}
	if got := manager.CoordinationState().Version; got != 2 {
		t.Fatalf("expected coordination version 2, got %d", got)
	}
}

func TestCoordinationModeWithoutPickFallsBackToAutoSelection(t *testing.T) {
	manager := newTestManager(
		testUpstream("alpha", 95, true),
		testUpstream("beta", 65, true),
	)

	manager.SetCoordination()

	state := manager.CoordinationState()
	if !state.FallbackActive {
		t.Fatalf("expected fallback to local auto selection, got %+v", state)
	}
	if manager.ActiveTag() != "alpha" {
		t.Fatalf("expected best local upstream alpha during fallback, got %q", manager.ActiveTag())
	}
}

func TestCoordinationInvalidPickFallsBackToAutoSelection(t *testing.T) {
	manager := newTestManager(
		testUpstream("alpha", 95, true),
		testUpstream("beta", 65, false),
	)

	manager.SetCoordination()
	applied, err := manager.ApplyCoordinationPick(1, "beta")
	if err == nil {
		t.Fatalf("expected invalid coordination pick to be rejected")
	}
	if applied {
		t.Fatalf("expected invalid coordination pick to be ignored")
	}

	state := manager.CoordinationState()
	if !state.FallbackActive {
		t.Fatalf("expected fallback state after invalid coordination pick, got %+v", state)
	}
	if manager.ActiveTag() != "alpha" {
		t.Fatalf("expected fallback to best local upstream alpha, got %q", manager.ActiveTag())
	}
}

func TestSwitchingAwayFromCoordinationClearsCoordinationState(t *testing.T) {
	manager := newTestManager(
		testUpstream("alpha", 95, true),
		testUpstream("beta", 65, true),
	)

	manager.SetCoordination()
	if _, err := manager.ApplyCoordinationPick(3, "beta"); err != nil {
		t.Fatalf("expected coordination pick to apply: %v", err)
	}
	manager.SetAuto()

	state := manager.CoordinationState()
	if state.Connected || state.SelectedUpstream != "" || state.Version != 0 || state.FallbackActive {
		t.Fatalf("expected coordination state to be cleared, got %+v", state)
	}
	if manager.Mode() != ModeAuto {
		t.Fatalf("expected auto mode, got %s", manager.Mode().String())
	}
}

func newTestManager(upstreams ...*Upstream) *UpstreamManager {
	manager := NewUpstreamManager(upstreams, rand.New(rand.NewSource(1)), nil)
	manager.staleThreshold = time.Minute
	return manager
}

func testUpstream(tag string, score float64, usable bool) *Upstream {
	now := time.Now()
	return &Upstream{
		Tag: tag,
		stats: UpstreamStats{
			Reachable:     usable,
			Usable:        usable,
			Score:         score,
			LastTCPUpdate: now,
			LastUDPUpdate: now,
		},
	}
}
