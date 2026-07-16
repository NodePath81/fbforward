package control

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
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
	c.attachFlowTags(tcp)
	c.attachFlowTags(udp)
	return rpcOK(activeFlowsResponse{
		CapturedAt: time.Now().UTC().UnixMilli(),
		TCP:        tcp,
		UDP:        udp,
	})
}

func (c *ControlServer) attachFlowTags(entries []StatusEntry) {
	store := c.auditStore()
	if store == nil || len(entries) == 0 {
		return
	}
	lookups := make([]audit.FlowTagLookup, 0, len(entries))
	for _, entry := range entries {
		clientIP := ""
		if addr, err := netip.ParseAddrPort(entry.ClientAddr); err == nil {
			clientIP = addr.Addr().String()
		}
		lookups = append(lookups, audit.FlowTagLookup{FlowID: entry.ID, ClientIP: clientIP})
	}
	tags, err := store.QueryEffectiveTags(lookups)
	if err != nil {
		return
	}
	for i := range entries {
		for _, tag := range tags[entries[i].ID] {
			entries[i].Tags = append(entries[i].Tags, FlowTagView{Tag: tag.Tag, Scope: tag.Scope})
		}
	}
}
