package forwarding

import (
	"net/netip"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

// Decision is the forwarding-layer result of an admission policy. Concrete
// firewall implementations are adapted to this value at the application
// boundary.
type Decision struct {
	Allowed          bool
	RuleType         string
	RuleValue        string
	RuleID           string
	Action           string
	RateLimitBPS     uint64
	UpstreamOverride string
}

// AdmissionPolicy decides whether a candidate Flow may be admitted. Candidate
// metadata has no ID or upstream yet; those are assigned after admission.
type AdmissionPolicy interface {
	Decide(flow.Meta) Decision
}

// Upstream is the minimal value needed by the data plane to dial a selected
// upstream. The listener's port remains the destination port for compatibility
// with the existing forwarding configuration.
type Upstream struct {
	Tag  string
	Addr netip.Addr
}

// UpstreamPicker selects one upstream for a new Flow.
type UpstreamPicker interface {
	Pick(flow.Meta) (Upstream, error)
}

// OverridePicker selects a named upstream for route_override online rules.
// It is optional so simple pickers remain valid for ordinary forwarding.
type OverridePicker interface {
	PickOverride(flow.Meta, string) (Upstream, error)
}

// DialFeedback is optional. Pickers that implement it retain the existing
// dial-failure cooldown behavior without making it part of the selection API.
type DialFeedback interface {
	MarkDialFailure(Upstream, time.Duration)
	ClearDialFailure(Upstream)
}

// FlowObserver is intentionally declared in forwarding. Implementations from
// flow, control, metrics, and iplog satisfy it structurally without making the
// data plane depend on those packages.
type FlowObserver interface {
	Open(flow.Meta)
	Update(flow.ID, flow.Counters)
	Close(flow.Summary)
	Reject(flow.Rejection)
}

// BackendBinder records the socket tuple used to reach an upstream. It is
// optional: a binder failure must never tear down an otherwise healthy Flow.
type BackendBinder interface {
	Bind(flow.ID, flow.BackendTuple) error
}

// RateLimitDropRecorder is optional telemetry for UDP packets rejected by a
// Flow rate limiter. It keeps the forwarding package independent of the
// concrete metrics implementation.
type RateLimitDropRecorder interface {
	RecordRateLimitDrop(protocol string, bytes uint64)
}
