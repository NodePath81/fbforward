package upstream

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

// UpstreamStateReader is the minimal control-plane dependency.
type UpstreamStateReader interface {
	SetAuto()
	SetManual(tag string) error
	Snapshot() []UpstreamSnapshot
	Mode() Mode
	ActiveTag() string
	Get(tag string) *Upstream
}
