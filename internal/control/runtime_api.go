package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/NodePath81/fbforward/internal/geoip"
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

func (c *ControlServer) rpcRefreshGeoIP(ctx *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	c.geoipMu.RLock()
	manager := c.geoipMgr
	c.geoipMu.RUnlock()
	if manager == nil {
		return rpcError(http.StatusServiceUnavailable, "geoip manager not available")
	}
	result, err := manager.RefreshNow(ctx.Request.Context())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, geoip.ErrNoConfiguredDatabases) {
			status = http.StatusServiceUnavailable
		}
		return rpcError(status, err.Error())
	}
	return rpcOK(result)
}
func (c *ControlServer) getMeasurementConfig() map[string]interface{} {
	cfg := c.measurement
	return map[string]interface{}{
		"startup_delay":             cfg.StartupDelay.Duration().String(),
		"stale_threshold":           cfg.StaleThreshold.Duration().String(),
		"fallback_to_icmp_on_stale": util.BoolValue(cfg.FallbackToICMPOnStale, true),
		"schedule": map[string]interface{}{
			"interval": map[string]interface{}{
				"min": cfg.Schedule.Interval.Min.Duration().String(),
				"max": cfg.Schedule.Interval.Max.Duration().String(),
			},
			"upstream_gap": cfg.Schedule.UpstreamGap.Duration().String(),
		},
		"fast_start": map[string]interface{}{
			"enabled":         util.BoolValue(cfg.FastStart.Enabled, true),
			"timeout":         cfg.FastStart.Timeout.Duration().String(),
			"warmup_duration": cfg.FastStart.WarmupDuration.Duration().String(),
		},
		"security": map[string]interface{}{
			"mode":        cfg.Security.Mode,
			"server_name": cfg.Security.ServerName,
		},
		"protocols": map[string]interface{}{
			"tcp": map[string]interface{}{
				"enabled":          util.BoolValue(cfg.Protocols.TCP.Enabled, true),
				"ping_count":       cfg.Protocols.TCP.PingCount,
				"retransmit_bytes": cfg.Protocols.TCP.RetransmitBytes,
				"timeout": map[string]interface{}{
					"per_sample": cfg.Protocols.TCP.Timeout.PerSample.Duration().String(),
					"per_cycle":  cfg.Protocols.TCP.Timeout.PerCycle.Duration().String(),
				},
			},
			"udp": map[string]interface{}{
				"enabled":      util.BoolValue(cfg.Protocols.UDP.Enabled, true),
				"ping_count":   cfg.Protocols.UDP.PingCount,
				"loss_packets": cfg.Protocols.UDP.LossPackets,
				"packet_size":  cfg.Protocols.UDP.PacketSize,
				"timeout": map[string]interface{}{
					"per_sample": cfg.Protocols.UDP.Timeout.PerSample.Duration().String(),
					"per_cycle":  cfg.Protocols.UDP.Timeout.PerCycle.Duration().String(),
				},
			},
		},
	}
}

func (c *ControlServer) getRuntimeConfig() map[string]interface{} {
	cfg := c.fullCfg

	listeners := make([]map[string]interface{}, 0, len(cfg.Forwarding.Listeners))
	for _, ln := range cfg.Forwarding.Listeners {
		entry := map[string]interface{}{
			"bind_addr": ln.BindAddr,
			"bind_port": ln.BindPort,
			"protocol":  ln.Protocol,
		}
		if ln.Shaping != nil {
			entry["shaping"] = map[string]interface{}{
				"upload_limit":   ln.Shaping.UploadLimit,
				"download_limit": ln.Shaping.DownloadLimit,
			}
		}
		listeners = append(listeners, entry)
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
			"bias":     up.Bias,
		}
		if up.Shaping != nil {
			entry["shaping"] = map[string]interface{}{
				"upload_limit":   up.Shaping.UploadLimit,
				"download_limit": up.Shaping.DownloadLimit,
			}
		}
		upstreams = append(upstreams, entry)
	}

	return map[string]interface{}{
		"hostname": cfg.Hostname,
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
		"reachability": map[string]interface{}{
			"probe_interval": cfg.Reachability.ProbeInterval.Duration().String(),
			"window_size":    cfg.Reachability.WindowSize,
			"startup_delay":  cfg.Reachability.StartupDelay.Duration().String(),
		},
		"measurement": c.getMeasurementConfig(),
		"scoring": map[string]interface{}{
			"smoothing": map[string]interface{}{
				"alpha": cfg.Scoring.Smoothing.Alpha,
			},
			"reference": map[string]interface{}{
				"tcp": map[string]interface{}{
					"latency": map[string]interface{}{
						"rtt":    cfg.Scoring.Reference.TCP.Latency.RTT,
						"jitter": cfg.Scoring.Reference.TCP.Latency.Jitter,
					},
					"retransmit_rate": cfg.Scoring.Reference.TCP.RetransmitRate,
					"loss_rate":       cfg.Scoring.Reference.TCP.LossRate,
				},
				"udp": map[string]interface{}{
					"latency": map[string]interface{}{
						"rtt":    cfg.Scoring.Reference.UDP.Latency.RTT,
						"jitter": cfg.Scoring.Reference.UDP.Latency.Jitter,
					},
					"retransmit_rate": cfg.Scoring.Reference.UDP.RetransmitRate,
					"loss_rate":       cfg.Scoring.Reference.UDP.LossRate,
				},
			},
			"weights": map[string]interface{}{
				"tcp": map[string]interface{}{
					"rtt":             cfg.Scoring.Weights.TCP.RTT,
					"jitter":          cfg.Scoring.Weights.TCP.Jitter,
					"retransmit_rate": cfg.Scoring.Weights.TCP.RetransmitRate,
				},
				"udp": map[string]interface{}{
					"rtt":       cfg.Scoring.Weights.UDP.RTT,
					"jitter":    cfg.Scoring.Weights.UDP.Jitter,
					"loss_rate": cfg.Scoring.Weights.UDP.LossRate,
				},
				"protocol_blend": map[string]interface{}{
					"tcp_weight": cfg.Scoring.Weights.ProtocolBlend.TCPWeight,
					"udp_weight": cfg.Scoring.Weights.ProtocolBlend.UDPWeight,
				},
			},
			"bias_transform": map[string]interface{}{
				"kappa": cfg.Scoring.BiasTransform.Kappa,
			},
		},
		"switching": map[string]interface{}{
			"auto": map[string]interface{}{
				"confirm_duration":      cfg.Switching.Auto.ConfirmDuration.Duration().String(),
				"score_delta_threshold": cfg.Switching.Auto.ScoreDeltaThreshold,
				"min_hold_time":         cfg.Switching.Auto.MinHoldTime.Duration().String(),
			},
			"failover": map[string]interface{}{
				"loss_rate_threshold":       cfg.Switching.Failover.LossRateThreshold,
				"retransmit_rate_threshold": cfg.Switching.Failover.RetransmitRateThreshold,
			},
			"close_flows_on_failover": cfg.Switching.CloseFlowsOnFailover,
		},
		"control": map[string]interface{}{
			"bind_addr": cfg.Control.BindAddr,
			"bind_port": cfg.Control.BindPort,
			"webui": map[string]interface{}{
				"enabled": cfg.Control.WebUI.IsEnabled(),
			},
			"metrics": map[string]interface{}{
				"enabled": cfg.Control.Metrics.IsEnabled(),
			},
		},
		"notify": map[string]interface{}{
			"enabled":              cfg.Notify.Enabled,
			"endpoint":             cfg.Notify.Endpoint,
			"key_id":               cfg.Notify.KeyID,
			"source_instance":      cfg.Notify.SourceInstance,
			"startup_grace_period": cfg.Notify.StartupGracePeriod.Duration().String(),
			"unusable_interval":    cfg.Notify.UnusableInterval.Duration().String(),
			"notify_interval":      cfg.Notify.NotifyInterval.Duration().String(),
		},
		"coordination": map[string]interface{}{
			"endpoint":           cfg.Coordination.Endpoint,
			"heartbeat_interval": cfg.Coordination.HeartbeatInterval.Duration().String(),
		},
		"geoip": map[string]interface{}{
			"enabled":          cfg.GeoIP.Enabled,
			"asn_db_url":       cfg.GeoIP.ASNDBURL,
			"asn_db_path":      cfg.GeoIP.ASNDBPath,
			"country_db_url":   cfg.GeoIP.CountryDBURL,
			"country_db_path":  cfg.GeoIP.CountryDBPath,
			"refresh_interval": cfg.GeoIP.RefreshInterval.Duration().String(),
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
		"firewall": map[string]interface{}{
			"enabled": cfg.Firewall.Enabled,
			"default": cfg.Firewall.Default,
			"rules":   cfg.Firewall.Rules,
		},
		"shaping": map[string]interface{}{
			"enabled":         cfg.Shaping.Enabled,
			"interface":       cfg.Shaping.Interface,
			"ifb_device":      cfg.Shaping.IFBDevice,
			"aggregate_limit": cfg.Shaping.AggregateLimit,
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
