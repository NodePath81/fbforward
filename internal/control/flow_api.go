package control

import (
	"encoding/json"
	"net/http"
	"time"
)

// activeFlowsResponse is the small, snapshot-oriented payload used by the
// polling UI and other control-plane clients. It deliberately contains no
// queue or event history data.
type activeFlowsResponse struct {
	CapturedAt int64         `json:"captured_at"`
	TCP        []StatusEntry `json:"tcp"`
	UDP        []StatusEntry `json:"udp"`
}

func (c *ControlServer) rpcGetActiveFlows(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	if c.status == nil {
		return rpcError(http.StatusServiceUnavailable, "status store unavailable")
	}
	tcp, udp := c.status.Snapshot()
	return rpcOK(activeFlowsResponse{
		CapturedAt: time.Now().UTC().UnixMilli(),
		TCP:        tcp,
		UDP:        udp,
	})
}
