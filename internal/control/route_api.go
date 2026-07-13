package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NodePath81/fbforward/internal/upstream"
)

type routeStateReader interface {
	RouteStatus() []upstream.RouteStatus
	SetRouteOverride(route, tag string) error
	ClearRouteOverride(route string) error
}

type routeOverrideParams struct {
	Route    string `json:"route"`
	Upstream string `json:"upstream"`
}

type routeNameParams struct {
	Route string `json:"route"`
}

func (c *ControlServer) rpcGetRouteStatus(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	if c.routes == nil {
		return rpcError(http.StatusServiceUnavailable, "route selector unavailable")
	}
	return rpcOK(c.routes.RouteStatus())
}

func (c *ControlServer) rpcSetRouteOverride(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	var params routeOverrideParams
	if fault := decodeRequiredParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	if c.routes == nil {
		return rpcError(http.StatusServiceUnavailable, "route selector unavailable")
	}
	err := c.routes.SetRouteOverride(strings.TrimSpace(params.Route), strings.TrimSpace(params.Upstream))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "route") && strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		return rpcError(status, err.Error())
	}
	return rpcOK(nil)
}

func (c *ControlServer) rpcClearRouteOverride(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	var params routeNameParams
	if fault := decodeRequiredParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	if c.routes == nil {
		return rpcError(http.StatusServiceUnavailable, "route selector unavailable")
	}
	if err := c.routes.ClearRouteOverride(strings.TrimSpace(params.Route)); err != nil {
		return rpcError(http.StatusNotFound, err.Error())
	}
	return rpcOK(nil)
}
