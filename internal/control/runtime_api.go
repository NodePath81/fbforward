package control

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/notify"
	"github.com/NodePath81/fbforward/internal/util"
)

func (c *ControlServer) rpcRestart(ctx *rpcContext, _ json.RawMessage) (any, *rpcFault) {
	go func(requestID string) {
		if c.restartFn == nil {
			return
		}
		if err := c.restartFn(); err != nil {
			util.Event(c.logger, slogLevelWarn(), "control.rpc.restart_completed", "request.id", requestID, "rpc.method", "Restart", "result", "failed", "error", err)
			return
		}
		util.Event(c.logger, slogLevelInfo(), "control.rpc.restart_completed", "request.id", requestID, "rpc.method", "Restart", "result", "success")
	}(ctx.Meta.id)
	util.Event(c.logger, slogLevelInfo(), "control.rpc.restart_requested", "rpc.method", "Restart")
	return rpcOK(nil)
}

func flowContextIdentityView(identities []config.FlowContextIdentity) []map[string]any {
	result := make([]map[string]any, 0, len(identities))
	for _, identity := range identities {
		result = append(result, map[string]any{
			"id":         identity.ID,
			"routes":     append([]string(nil), identity.Routes...),
			"upstreams":  append([]string(nil), identity.Upstreams...),
			"namespaces": append([]string(nil), identity.Namespaces...),
		})
	}
	return result
}

func (c *ControlServer) rpcSendTestNotification(_ *rpcContext, _ json.RawMessage) (any, *rpcFault) {
	c.notifierMu.RLock()
	notifier := c.notifier
	c.notifierMu.RUnlock()
	if notifier == nil {
		return rpcError(http.StatusServiceUnavailable, "notifications not configured")
	}
	if !notifier.Emit("system.test_notification", notify.SeverityInfo, map[string]any{"test.origin": "manual", "test.service": "fbforward"}) {
		return rpcError(http.StatusServiceUnavailable, "notification enqueue failed")
	}
	return rpcOK(nil)
}

func (c *ControlServer) rpcGetMeasurementConfig(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	return rpcOK(c.getMeasurementConfig())
}

func (c *ControlServer) rpcGetRuntimeConfig(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	return rpcOK(c.getRuntimeConfig())
}

func (c *ControlServer) rpcGetScheduleStatus(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	return rpcOK(c.getScheduleStatus())
}

func (c *ControlServer) rpcGetGeoIPStatus(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	c.geoipMu.RLock()
	manager := c.geoipMgr
	c.geoipMu.RUnlock()
	if manager == nil {
		return rpcError(http.StatusServiceUnavailable, "geoip manager not available")
	}
	return rpcOK(manager.Status())
}

func (c *ControlServer) rpcReloadGeoIP(ctx *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	c.geoipMu.RLock()
	manager := c.geoipMgr
	c.geoipMu.RUnlock()
	if manager == nil {
		return rpcError(http.StatusServiceUnavailable, "geoip manager not available")
	}
	reloader, ok := manager.(geoipReloader)
	if !ok {
		return rpcError(http.StatusServiceUnavailable, "geoip reload is not available")
	}
	if err := reloader.Reload(ctx.Request.Context()); err != nil {
		return rpcError(http.StatusInternalServerError, err.Error())
	}
	return rpcOK(manager.Status())
}
func (c *ControlServer) getMeasurementConfig() map[string]interface{} {
	cfg := c.measurement
	return map[string]interface{}{
		"probe_timeout": cfg.ProbeTimeout.Duration().String(),
		"schedule": map[string]interface{}{
			"interval": map[string]interface{}{
				"min": cfg.Schedule.Interval.Min.Duration().String(),
				"max": cfg.Schedule.Interval.Max.Duration().String(),
			},
			"upstream_gap": cfg.Schedule.UpstreamGap.Duration().String(),
		},
		"protocols": map[string]interface{}{
			"tcp": map[string]interface{}{
				"enabled": util.BoolValue(cfg.Protocols.TCP.Enabled, true),
			},
			"udp": map[string]interface{}{
				"enabled": util.BoolValue(cfg.Protocols.UDP.Enabled, true),
			},
		},
	}
}

func (c *ControlServer) getRuntimeConfig() map[string]interface{} {
	cfg := c.fullCfg

	listeners := make([]map[string]interface{}, 0, len(cfg.Forwarding.Listeners))
	for _, ln := range cfg.Forwarding.Listeners {
		entry := map[string]interface{}{
			"name":      ln.Name,
			"bind_addr": ln.BindAddr,
			"bind_port": ln.BindPort,
			"protocol":  ln.Protocol,
			"route":     ln.Route,
		}
		listeners = append(listeners, entry)
	}

	modernListeners := make([]map[string]interface{}, 0, len(cfg.Listeners))
	for _, ln := range cfg.Listeners {
		modernListeners = append(modernListeners, map[string]interface{}{
			"name": ln.Name, "bind": ln.Bind, "protocol": ln.Protocol, "route": ln.Route,
		})
	}
	routes := make([]map[string]interface{}, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		routes = append(routes, map[string]interface{}{
			"name": route.Name, "strategy": route.Strategy, "upstreams": append([]string(nil), route.Upstreams...), "default_upstream": route.DefaultUpstream,
		})
	}

	upstreams := make([]map[string]interface{}, 0, len(cfg.Upstreams))
	for _, up := range cfg.Upstreams {
		entry := map[string]interface{}{
			"tag": up.Tag,
			"destination": map[string]interface{}{
				"host": up.Destination.Host,
			},
			"measurement": map[string]interface{}{
				"host": up.Measurement.Host,
				"port": up.Measurement.Port,
			},
			"priority": up.Priority,
		}
		upstreams = append(upstreams, entry)
	}

	return map[string]interface{}{
		"hostname":  cfg.Hostname,
		"listeners": modernListeners,
		"routes":    routes,
		"forwarding": map[string]interface{}{
			"listeners": listeners,
			"limits": map[string]interface{}{
				"max_tcp_connections": cfg.Forwarding.Limits.MaxTCPConnections,
				"max_udp_mappings":    cfg.Forwarding.Limits.MaxUDPMappings,
			},
			"idle_timeout": map[string]interface{}{
				"tcp": cfg.Forwarding.IdleTimeout.TCP.Duration().String(),
				"udp": cfg.Forwarding.IdleTimeout.UDP.Duration().String(),
			},
		},
		"upstreams": upstreams,
		"dns": map[string]interface{}{
			"servers":  cfg.DNS.Servers,
			"strategy": cfg.DNS.Strategy,
		},
		"measurement": c.getMeasurementConfig(),
		"health": map[string]interface{}{
			"rtt_ewma_alpha":     cfg.Health.RTTEWMAAlpha,
			"failure_threshold":  cfg.Health.FailureThreshold,
			"recovery_threshold": cfg.Health.RecoveryThreshold,
			"stale_threshold":    cfg.Health.StaleThreshold.Duration().String(),
		},
		"control": map[string]interface{}{
			"bind_addr": cfg.Control.BindAddr,
			"bind_port": cfg.Control.BindPort,
			"metrics": map[string]interface{}{
				"enabled": cfg.Control.Metrics.IsEnabled(),
			},
		},
		"webhook": map[string]interface{}{
			"enabled":         cfg.Notify.Enabled,
			"endpoint":        cfg.Notify.Endpoint,
			"source_instance": cfg.Notify.SourceInstance,
		},
		"geoip": map[string]interface{}{
			"enabled":         cfg.GeoIP.Enabled,
			"asn_db_path":     cfg.GeoIP.ASNDBPath,
			"country_db_path": cfg.GeoIP.CountryDBPath,
		},
		"ip_log": map[string]interface{}{
			"enabled":          cfg.IPLog.Enabled,
			"log_rejections":   util.BoolValue(cfg.IPLog.LogRejections, cfg.IPLog.Enabled),
			"db_path":          cfg.IPLog.DBPath,
			"retention":        cfg.IPLog.Retention.Duration().String(),
			"geo_queue_size":   cfg.IPLog.GeoQueueSize,
			"write_queue_size": cfg.IPLog.WriteQueueSize,
			"batch_size":       cfg.IPLog.BatchSize,
			"flush_interval":   cfg.IPLog.FlushInterval.Duration().String(),
			"prune_interval":   cfg.IPLog.PruneInterval.Duration().String(),
		},
		"flow_context": map[string]interface{}{
			"enabled":    cfg.FlowContext.Enabled,
			"max_ttl":    cfg.FlowContext.MaxTTL.Duration().String(),
			"identities": flowContextIdentityView(cfg.FlowContext.Identities),
		},
		"firewall": map[string]interface{}{
			"enabled":              cfg.Firewall.Enabled,
			"policy_file":          cfg.Firewall.PolicyFile,
			"fail_on_initial_load": cfg.Firewall.ShouldFailOnInitialLoad(),
		},
	}
}

func (c *ControlServer) getScheduleStatus() map[string]interface{} {
	c.schedulerMu.RLock()
	scheduler := c.scheduler
	c.schedulerMu.RUnlock()
	if scheduler == nil {
		return map[string]interface{}{
			"queue_length":      0,
			"next_scheduled":    nil,
			"last_measurements": map[string]time.Time{},
		}
	}
	status := scheduler.Status()
	result := map[string]interface{}{
		"queue_length":      status.QueueLength,
		"next_scheduled":    nil,
		"last_measurements": status.LastRun,
	}
	if !status.NextScheduled.IsZero() {
		result["next_scheduled"] = status.NextScheduled
	}
	return result
}
