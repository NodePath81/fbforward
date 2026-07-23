package metrics

import (
	"sync"

	"github.com/NodePath81/fbforward/internal/flow"
)

type flowMetricState struct {
	protocol  string
	upstream  string
	bytesUp   uint64
	bytesDown uint64
}

// FlowObserver adapts cumulative Flow events to the existing delta-based
// Metrics counters.
type FlowObserver struct {
	metrics *Metrics
	mu      sync.Mutex
	flows   map[flow.ID]flowMetricState
}

func NewFlowObserver(metrics *Metrics) *FlowObserver {
	return &FlowObserver{
		metrics: metrics,
		flows:   make(map[flow.ID]flowMetricState),
	}
}

func (o *FlowObserver) Open(meta flow.Meta) {
	if o == nil || o.metrics == nil {
		return
	}
	o.mu.Lock()
	o.flows[meta.ID] = flowMetricState{protocol: meta.Protocol, upstream: meta.Upstream}
	o.mu.Unlock()
	o.metrics.IncActive(meta.Protocol)
	o.metrics.RecordFlowEvent(meta.Protocol, "open", "")
}

func (o *FlowObserver) Update(id flow.ID, counters flow.Counters) {
	if o == nil || o.metrics == nil {
		return
	}
	o.mu.Lock()
	state, ok := o.flows[id]
	if !ok {
		o.mu.Unlock()
		return
	}
	upDelta := uint64(0)
	if counters.BytesUp > state.bytesUp {
		upDelta = counters.BytesUp - state.bytesUp
	}
	downDelta := uint64(0)
	if counters.BytesDown > state.bytesDown {
		downDelta = counters.BytesDown - state.bytesDown
	}
	state.bytesUp = counters.BytesUp
	state.bytesDown = counters.BytesDown
	o.flows[id] = state
	o.mu.Unlock()
	o.addBytes(state, upDelta, downDelta)
}

func (o *FlowObserver) Close(summary flow.Summary) {
	if o == nil || o.metrics == nil {
		return
	}
	o.mu.Lock()
	state, ok := o.flows[summary.ID]
	if ok {
		delete(o.flows, summary.ID)
	}
	upDelta := uint64(0)
	downDelta := uint64(0)
	if ok {
		if summary.BytesUp > state.bytesUp {
			upDelta = summary.BytesUp - state.bytesUp
		}
		if summary.BytesDown > state.bytesDown {
			downDelta = summary.BytesDown - state.bytesDown
		}
	}
	o.mu.Unlock()
	if !ok {
		return
	}
	o.addBytes(state, upDelta, downDelta)
	o.metrics.DecActive(state.protocol)
	o.metrics.RecordFlowEvent(state.protocol, "close", summary.CloseReason)
}

func (o *FlowObserver) Reject(rejection flow.Rejection) {
	if o == nil || o.metrics == nil {
		return
	}
	o.metrics.RecordFlowEvent(rejection.Protocol, "reject", rejection.Reason)
}

func (o *FlowObserver) addBytes(state flowMetricState, upDelta, downDelta uint64) {
	if upDelta > 0 {
		o.metrics.AddTraffic(state.upstream, state.protocol, "up", upDelta)
	}
	if downDelta > 0 {
		o.metrics.AddTraffic(state.upstream, state.protocol, "down", downDelta)
	}
}
