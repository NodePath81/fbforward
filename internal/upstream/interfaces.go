package upstream

import "time"

type ActiveChange struct {
	OldTag string
	NewTag string
	Reason string
}

type UsabilityChange struct {
	Tag    string
	Usable bool
	Reason string
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
	SetCoordination()
	Snapshot() []UpstreamSnapshot
	Mode() Mode
	ActiveTag() string
	Get(tag string) *Upstream
	CoordinationState() CoordinationState
}
