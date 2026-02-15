package upstream

import (
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
)

func TestComputeFullScoreOrdering(t *testing.T) {
	cfg := config.DefaultScoringConfig()
	now := time.Now()

	better := UpstreamStats{
		Reachable:         true,
		BandwidthUpBps:    100e6,
		BandwidthDownBps:  100e6,
		RTTMs:             10,
		JitterMs:          5,
		LossRate:          0.0,
		RetransRate:       0.0,
		LastTCPUpdate:     now,
		LastUDPUpdate:     now,
		BandwidthUpBpsTCP: 100e6,
	}
	worse := better
	worse.BandwidthUpBps = 50e6
	worse.BandwidthDownBps = 50e6
	worse.RTTMs = 30
	worse.JitterMs = 15

	_, _, scoreBetter := computeFullScore(better, cfg, 0, 0, 0)
	_, _, scoreWorse := computeFullScore(worse, cfg, 0, 0, 0)
	if scoreBetter <= scoreWorse {
		t.Fatalf("expected better upstream to score higher (better=%v worse=%v)", scoreBetter, scoreWorse)
	}
}

func TestComputeFullScoreStalenessPenalty(t *testing.T) {
	cfg := config.DefaultScoringConfig()
	now := time.Now()

	fresh := UpstreamStats{
		Reachable:        true,
		BandwidthUpBps:   100e6,
		BandwidthDownBps: 100e6,
		RTTMs:            10,
		JitterMs:         5,
		LossRate:         0.0,
		RetransRate:      0.0,
		LastTCPUpdate:    now,
		LastUDPUpdate:    now,
	}
	stale := fresh
	stale.LastTCPUpdate = now.Add(-time.Hour)

	_, _, freshScore := computeFullScore(fresh, cfg, 0, 0, 30*time.Second)
	_, _, staleScore := computeFullScore(stale, cfg, 0, 0, 30*time.Second)
	if staleScore >= freshScore {
		t.Fatalf("expected stale score < fresh score, got stale=%v fresh=%v", staleScore, freshScore)
	}
}

func TestComputeFullScoreBiasTransform(t *testing.T) {
	cfg := config.DefaultScoringConfig()
	now := time.Now()
	stats := UpstreamStats{
		Reachable:        true,
		BandwidthUpBps:   80e6,
		BandwidthDownBps: 80e6,
		RTTMs:            15,
		JitterMs:         5,
		LossRate:         0.0,
		RetransRate:      0.0,
		LastTCPUpdate:    now,
		LastUDPUpdate:    now,
	}
	_, _, neutral := computeFullScore(stats, cfg, 0, 0, 0)
	_, _, biasedUp := computeFullScore(stats, cfg, 0.5, 0, 0)
	_, _, biasedDown := computeFullScore(stats, cfg, -0.5, 0, 0)

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
