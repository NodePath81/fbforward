package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NodePath81/fbforward/internal/audit"
)

type listFlowContextTagsParams struct {
	Query  string `json:"query,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

type listFlowContextActionsParams struct {
	Query  string `json:"query,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

type flowContextTagsResponse struct {
	Records []audit.EffectiveTag `json:"records"`
	HasMore bool                 `json:"has_more"`
}

type flowContextActionsResponse struct {
	Records []audit.FlowTagAction `json:"records"`
	HasMore bool                  `json:"has_more"`
}

func (c *ControlServer) rpcListFlowContextTags(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	var params listFlowContextTagsParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	records, hasMore, err := store.QueryCurrentTags(strings.TrimSpace(params.Query), params.Scope, params.Limit, params.Offset)
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	return rpcOK(flowContextTagsResponse{Records: records, HasMore: hasMore})
}

func (c *ControlServer) rpcListFlowContextActions(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	var params listFlowContextActionsParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	records, hasMore, err := store.QueryTagActions(strings.TrimSpace(params.Query), params.Limit, params.Offset)
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	return rpcOK(flowContextActionsResponse{Records: records, HasMore: hasMore})
}
