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
	if meta.Protocol == flow.ProtocolTCP {
		o.metrics.IncTCPActive()
	} else if meta.Protocol == flow.ProtocolUDP {
		o.metrics.IncUDPActive()
	}
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
	if state.protocol == flow.ProtocolTCP {
		o.metrics.DecTCPActive()
	} else if state.protocol == flow.ProtocolUDP {
		o.metrics.DecUDPActive()
	}
}

func (o *FlowObserver) Reject(flow.Rejection) {}

func (o *FlowObserver) addBytes(state flowMetricState, upDelta, downDelta uint64) {
	if upDelta > 0 {
		o.metrics.AddBytesUp(state.upstream, upDelta, state.protocol)
	}
	if downDelta > 0 {
		o.metrics.AddBytesDown(state.upstream, downDelta, state.protocol)
	}
}
