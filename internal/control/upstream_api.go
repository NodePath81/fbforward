package control

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

type setUpstreamParams struct {
	Mode string `json:"mode"`
	Tag  string `json:"tag,omitempty"`
}

type runMeasurementParams struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

type statusResponse struct {
	Mode           string                      `json:"mode"`
	ActiveUpstream string                      `json:"active_upstream"`
	Upstreams      []upstream.UpstreamSnapshot `json:"upstreams"`
}

func (c *ControlServer) rpcSetUpstream(ctx *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	var params setUpstreamParams
	if fault := decodeRequiredParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	mode := strings.ToLower(params.Mode)
	switch mode {
	case "auto":
		c.manager.SetAuto()
		if c.metrics != nil {
			c.metrics.SetMode(upstream.ModeAuto)
		}
	case "manual":
		if err := c.manager.SetManual(params.Tag); err != nil {
			return rpcError(http.StatusBadRequest, err.Error())
		}
		if c.metrics != nil {
			c.metrics.SetMode(upstream.ModeManual)
		}
	default:
		return rpcError(http.StatusBadRequest, "invalid mode")
	}
	util.Event(c.logger, slog.LevelInfo, "control.rpc.set_upstream_applied",
		"rpc.method", ctx.Request.Method, "upstream", params.Tag, "upstream.mode", mode)
	return rpcOK(nil)
}

func (c *ControlServer) rpcGetStatus(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	return rpcOK(statusResponse{
		Mode:           c.manager.Mode().String(),
		ActiveUpstream: c.manager.ActiveTag(),
		Upstreams:      c.manager.Snapshot(),
	})
}

func (c *ControlServer) rpcListUpstreams(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	return rpcOK(c.manager.Snapshot())
}

func (c *ControlServer) rpcRunMeasurement(ctx *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	c.collectorMu.RLock()
	collector := c.collector
	c.collectorMu.RUnlock()
	if collector == nil {
		return rpcError(http.StatusServiceUnavailable, "collector not ready")
	}
	var params runMeasurementParams
	if fault := decodeRequiredParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	tag := strings.TrimSpace(params.Tag)
	protocol := strings.ToLower(strings.TrimSpace(params.Protocol))
	if protocol != "tcp" && protocol != "udp" {
		return rpcError(http.StatusBadRequest, "protocol must be tcp or udp")
	}
	up := c.manager.Get(tag)
	if up == nil {
		return rpcError(http.StatusNotFound, "upstream not found")
	}
	util.Event(c.logger, slog.LevelInfo, "control.rpc.run_measurement_requested",
		"rpc.method", "RunMeasurement", "upstream", tag, "network.protocol", protocol)
	go func(requestID string) {
		measureCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := collector.RunProtocol(measureCtx, up, protocol); err != nil {
			util.Event(c.logger, slog.LevelWarn, "control.rpc.run_measurement_completed",
				"request.id", requestID, "rpc.method", "RunMeasurement", "upstream", tag,
				"network.protocol", protocol, "result", "failed", "error", err)
			return
		}
		util.Event(c.logger, slog.LevelInfo, "control.rpc.run_measurement_completed",
			"request.id", requestID, "rpc.method", "RunMeasurement", "upstream", tag,
			"network.protocol", protocol, "result", "success")
	}(ctx.Meta.id)
	return rpcOK(nil)
}
