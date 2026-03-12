package upstream

import (
	"time"

	"github.com/NodePath81/fbforward/internal/config"
)

// Scorer encapsulates upstream scoring logic for isolated testing.
type Scorer interface {
	ComputeScore(stats UpstreamStats, cfg config.ScoringConfig, bias float64, staleThreshold time.Duration) (tcp, udp, overall float64)
}

// DefaultScorer uses computeFullScore, preserving existing scoring behavior.
type DefaultScorer struct{}

func (DefaultScorer) ComputeScore(stats UpstreamStats, cfg config.ScoringConfig, bias float64, staleThreshold time.Duration) (float64, float64, float64) {
	return computeFullScore(stats, cfg, bias, staleThreshold)
}

// UpstreamSelector is the minimal forwarding-side dependency.
type UpstreamSelector interface {
	SelectUpstream() (*Upstream, error)
	MarkDialFailure(tag string, cooldown time.Duration)
	ClearDialFailure(tag string)
}

// UpstreamStateReader is the minimal control-plane dependency.
type UpstreamStateReader interface {
	SetAuto()
	SetManual(tag string) error
	Snapshot() []UpstreamSnapshot
	Mode() Mode
	ActiveTag() string
	Get(tag string) *Upstream
}
