package control

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/measure"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/NodePath81/fbforward/internal/version"
	"github.com/NodePath81/fbforward/web"
	"github.com/gorilla/websocket"
)

const (
	maxRPCBodyBytes   = 1 << 20
	rpcRatePerSecond  = 5
	rpcRateBurst      = 10
	wsTokenPrefix     = "fbforward-token."
	wsPrimaryProtocol = "fbforward"
	wsWriteWait       = 10 * time.Second
	wsPongWait        = 60 * time.Second
	wsPingInterval    = 30 * time.Second
)

type ControlServer struct {
	fullCfg     config.Config
	cfg         config.ControlConfig
	measurement config.MeasurementConfig
	hostname    string
	manager     *upstream.UpstreamManager
	metrics     *metrics.Metrics
	status      *StatusStore
	restartFn   func() error
	logger      util.Logger
	server      *http.Server
	limiter     *rateLimiter
	schedulerMu sync.RWMutex
	scheduler   *measure.Scheduler
	collectorMu sync.RWMutex
	collector   *measure.Collector
}

func NewControlServer(cfg config.Config, manager *upstream.UpstreamManager, metrics *metrics.Metrics, status *StatusStore, restartFn func() error, logger util.Logger) *ControlServer {
	return &ControlServer{
		fullCfg:     cfg,
		cfg:         cfg.Control,
		measurement: cfg.Measurement,
		hostname:    cfg.Hostname,
		manager:     manager,
		metrics:     metrics,
		status:      status,
		restartFn:   restartFn,
		logger:      logger,
		limiter:     newRateLimiter(rpcRatePerSecond, rpcRateBurst, 5*time.Minute),
	}
}

func (c *ControlServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	if c.cfg.Metrics.IsEnabled() {
		mux.HandleFunc("/metrics", c.handleMetrics)
	}
	mux.HandleFunc("/rpc", c.handleRPC)
	mux.HandleFunc("/status", c.handleStatus)
	mux.HandleFunc("/identity", c.handleIdentity)
	mux.Handle("/", web.WebUIHandler(c.cfg.WebUI.IsEnabled()))

	addr := util.NetJoin(c.cfg.BindAddr, c.cfg.BindPort)
	c.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = c.server.Shutdown(shutdownCtx)
	}()

	go func() {
		if err := c.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.Error("control server error", "error", err)
		}
	}()
	c.logger.Info("control server started", "addr", addr)
	return nil
}

func (c *ControlServer) Shutdown(ctx context.Context) error {
	if c.server == nil {
		return nil
	}
	return c.server.Shutdown(ctx)
}

func (c *ControlServer) SetScheduler(scheduler *measure.Scheduler) {
	c.schedulerMu.Lock()
	defer c.schedulerMu.Unlock()
	c.scheduler = scheduler
}

func (c *ControlServer) SetCollector(collector *measure.Collector) {
	c.collectorMu.Lock()
	defer c.collectorMu.Unlock()
	c.collector = collector
}

type rpcRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcResponse struct {
	Ok     bool        `json:"ok"`
	Error  string      `json:"error,omitempty"`
	Result interface{} `json:"result,omitempty"`
}

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

type runningTestEntry struct {
	Upstream  string `json:"upstream"`
	Protocol  string `json:"protocol"`
	Direction string `json:"direction"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

type connectionsSnapshotMessage struct {
	SchemaVersion int           `json:"schema_version"`
	Type          string        `json:"type"`
	Timestamp     int64         `json:"timestamp"`
	TCP           []StatusEntry `json:"tcp"`
	UDP           []StatusEntry `json:"udp"`
}

type queueSnapshotMessage struct {
	SchemaVersion int                 `json:"schema_version"`
	Type          string              `json:"type"`
	Timestamp     int64               `json:"timestamp"`
	Depth         int                 `json:"depth"`
	Skipped       uint64              `json:"skipped"`
	NextDueMs     *int64              `json:"next_due_ms,omitempty"`
	Running       []runningTestEntry  `json:"running"`
	Pending       []queuePendingEntry `json:"pending"`
}

type queuePendingEntry struct {
	Upstream    string `json:"upstream"`
	Protocol    string `json:"protocol"`
	Direction   string `json:"direction"`
	ScheduledAt int64  `json:"scheduled_at"`
}

type identityResponse struct {
	Hostname string   `json:"hostname"`
	IPs      []string `json:"ips"`
	Version  string   `json:"version"`
}

func (c *ControlServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	if !c.limiter.Allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, rpcResponse{Ok: false, Error: "rate limit exceeded"})
		return
	}
	if !c.checkAuth(r) {
		writeJSON(w, http.StatusUnauthorized, rpcResponse{Ok: false, Error: "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, rpcResponse{Ok: false, Error: "method not allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRPCBodyBytes)
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: "invalid json"})
		return
	}
	switch req.Method {
	case "SetUpstream":
		var params setUpstreamParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: "invalid params"})
			return
		}
		mode := strings.ToLower(params.Mode)
		if mode == "auto" {
			c.manager.SetAuto()
			c.metrics.SetMode(upstream.ModeAuto)
			c.logger.Info("manual override cleared")
			writeJSON(w, http.StatusOK, rpcResponse{Ok: true})
			return
		}
		if mode == "manual" {
			if err := c.manager.SetManual(params.Tag); err != nil {
				writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: err.Error()})
				return
			}
			c.metrics.SetMode(upstream.ModeManual)
			c.logger.Info("manual override set", "upstream", params.Tag)
			writeJSON(w, http.StatusOK, rpcResponse{Ok: true})
			return
		}
		writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: "invalid mode"})
	case "Restart":
		go func() {
			c.logger.Info("restart invoked")
			if err := c.restartFn(); err != nil {
				c.logger.Error("restart failed", "error", err)
			}
		}()
		writeJSON(w, http.StatusOK, rpcResponse{Ok: true})
	case "GetStatus":
		upstreams := c.manager.Snapshot()
		resp := statusResponse{
			Mode:           c.manager.Mode().String(),
			ActiveUpstream: c.manager.ActiveTag(),
			Upstreams:      upstreams,
		}
		writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: resp})
	case "GetMeasurementConfig":
		writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: c.getMeasurementConfig()})
	case "GetRuntimeConfig":
		writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: c.getRuntimeConfig()})
	case "GetScheduleStatus":
		writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: c.getScheduleStatus()})
	case "ListUpstreams":
		writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: c.manager.Snapshot()})
	case "RunMeasurement":
		c.collectorMu.RLock()
		collector := c.collector
		c.collectorMu.RUnlock()
		if collector == nil {
			writeJSON(w, http.StatusServiceUnavailable, rpcResponse{Ok: false, Error: "collector not ready"})
			return
		}

		var params runMeasurementParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: "invalid params"})
			return
		}
		tag := strings.TrimSpace(params.Tag)
		protocol := strings.ToLower(strings.TrimSpace(params.Protocol))
		if protocol != "tcp" && protocol != "udp" {
			writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: "protocol must be tcp or udp"})
			return
		}
		up := c.manager.Get(tag)
		if up == nil {
			writeJSON(w, http.StatusNotFound, rpcResponse{Ok: false, Error: "upstream not found"})
			return
		}

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c.logger.Info("manual measurement triggered", "upstream", tag, "protocol", protocol, "source", "ui")
			if err := collector.RunProtocol(ctx, up, protocol); err != nil {
				c.logger.Warn("manual measurement failed", "upstream", tag, "protocol", protocol, "error", err)
				return
			}
			c.logger.Info("manual measurement completed", "upstream", tag, "protocol", protocol)
		}()

		writeJSON(w, http.StatusOK, rpcResponse{Ok: true})
	default:
		writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: "unknown method"})
	}
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
			"headroom": map[string]interface{}{
				"max_link_utilization":    cfg.Schedule.Headroom.MaxLinkUtilization,
				"required_free_bandwidth": cfg.Schedule.Headroom.RequiredFreeBandwidth,
			},
		},
		"fast_start": map[string]interface{}{
			"enabled":         util.BoolValue(cfg.FastStart.Enabled, true),
			"timeout":         cfg.FastStart.Timeout.Duration().String(),
			"warmup_duration": cfg.FastStart.WarmupDuration.Duration().String(),
		},
		"protocols": map[string]interface{}{
			"tcp": map[string]interface{}{
				"enabled":   util.BoolValue(cfg.Protocols.TCP.Enabled, true),
				"alternate": util.BoolValue(cfg.Protocols.TCP.Alternate, true),
				"target_bandwidth": map[string]interface{}{
					"upload":   cfg.Protocols.TCP.TargetBandwidth.Upload,
					"download": cfg.Protocols.TCP.TargetBandwidth.Download,
				},
				"chunk_size":   cfg.Protocols.TCP.ChunkSize,
				"sample_size":  cfg.Protocols.TCP.SampleSize,
				"sample_count": cfg.Protocols.TCP.SampleCount,
				"timeout": map[string]interface{}{
					"per_sample": cfg.Protocols.TCP.Timeout.PerSample.Duration().String(),
					"per_cycle":  cfg.Protocols.TCP.Timeout.PerCycle.Duration().String(),
				},
			},
			"udp": map[string]interface{}{
				"enabled": util.BoolValue(cfg.Protocols.UDP.Enabled, true),
				"target_bandwidth": map[string]interface{}{
					"upload":   cfg.Protocols.UDP.TargetBandwidth.Upload,
					"download": cfg.Protocols.UDP.TargetBandwidth.Download,
				},
				"chunk_size":   cfg.Protocols.UDP.ChunkSize,
				"sample_size":  cfg.Protocols.UDP.SampleSize,
				"sample_count": cfg.Protocols.UDP.SampleCount,
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
					"bandwidth": map[string]interface{}{
						"upload":   cfg.Scoring.Reference.TCP.Bandwidth.Upload,
						"download": cfg.Scoring.Reference.TCP.Bandwidth.Download,
					},
					"latency": map[string]interface{}{
						"rtt":    cfg.Scoring.Reference.TCP.Latency.RTT,
						"jitter": cfg.Scoring.Reference.TCP.Latency.Jitter,
					},
					"retransmit_rate": cfg.Scoring.Reference.TCP.RetransmitRate,
					"loss_rate":       cfg.Scoring.Reference.TCP.LossRate,
				},
				"udp": map[string]interface{}{
					"bandwidth": map[string]interface{}{
						"upload":   cfg.Scoring.Reference.UDP.Bandwidth.Upload,
						"download": cfg.Scoring.Reference.UDP.Bandwidth.Download,
					},
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
					"bandwidth_upload":   cfg.Scoring.Weights.TCP.BandwidthUpload,
					"bandwidth_download": cfg.Scoring.Weights.TCP.BandwidthDownload,
					"rtt":                cfg.Scoring.Weights.TCP.RTT,
					"jitter":             cfg.Scoring.Weights.TCP.Jitter,
					"retransmit_rate":    cfg.Scoring.Weights.TCP.RetransmitRate,
				},
				"udp": map[string]interface{}{
					"bandwidth_upload":   cfg.Scoring.Weights.UDP.BandwidthUpload,
					"bandwidth_download": cfg.Scoring.Weights.UDP.BandwidthDownload,
					"rtt":                cfg.Scoring.Weights.UDP.RTT,
					"jitter":             cfg.Scoring.Weights.UDP.Jitter,
					"loss_rate":          cfg.Scoring.Weights.UDP.LossRate,
				},
				"protocol_blend": map[string]interface{}{
					"tcp_weight": cfg.Scoring.Weights.ProtocolBlend.TCPWeight,
					"udp_weight": cfg.Scoring.Weights.ProtocolBlend.UDPWeight,
				},
			},
			"utilization_penalty": map[string]interface{}{
				"enabled":         util.BoolValue(cfg.Scoring.UtilizationPenalty.Enabled, true),
				"window_duration": cfg.Scoring.UtilizationPenalty.WindowDuration.Duration().String(),
				"update_interval": cfg.Scoring.UtilizationPenalty.UpdateInterval.Duration().String(),
				"threshold":       cfg.Scoring.UtilizationPenalty.Threshold,
				"min_multiplier":  cfg.Scoring.UtilizationPenalty.MinMultiplier,
				"exponent":        cfg.Scoring.UtilizationPenalty.Exponent,
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
			"skipped_total":     0,
		}
	}
	status := scheduler.Status()
	result := map[string]interface{}{
		"queue_length":      status.QueueLength,
		"next_scheduled":    nil,
		"last_measurements": status.LastRun,
		"skipped_total":     status.SkippedTotal,
	}
	if !status.NextScheduled.IsZero() {
		result["next_scheduled"] = status.NextScheduled
	}
	return result
}

func (c *ControlServer) getQueueSnapshot(now time.Time) queueSnapshotMessage {
	c.schedulerMu.RLock()
	scheduler := c.scheduler
	c.schedulerMu.RUnlock()

	c.collectorMu.RLock()
	collector := c.collector
	c.collectorMu.RUnlock()

	snapshot := queueSnapshotMessage{
		SchemaVersion: 1,
		Type:          "queue_snapshot",
		Timestamp:     now.UnixMilli(),
		Depth:         0,
		Skipped:       0,
		NextDueMs:     nil,
		Running:       []runningTestEntry{},
		Pending:       []queuePendingEntry{},
	}

	if scheduler != nil {
		status := scheduler.Status()
		snapshot.Depth = status.QueueLength
		snapshot.Skipped = status.SkippedTotal
		if !status.NextScheduled.IsZero() {
			delta := status.NextScheduled.Sub(now).Milliseconds()
			if delta < 0 {
				delta = 0
			}
			snapshot.NextDueMs = &delta
		}
		entries := make([]queuePendingEntry, 0, len(status.Pending))
		for _, item := range status.Pending {
			entries = append(entries, queuePendingEntry{
				Upstream:    item.Upstream,
				Protocol:    item.Protocol,
				Direction:   item.Direction,
				ScheduledAt: item.ScheduledAt.UnixMilli(),
			})
		}
		snapshot.Pending = entries
	}

	if collector != nil {
		running := collector.RunningTests()
		entries := make([]runningTestEntry, 0, len(running))
		for _, test := range running {
			entries = append(entries, runningTestEntry{
				Upstream:  test.Upstream,
				Protocol:  test.Protocol,
				Direction: test.Direction,
				ElapsedMs: now.Sub(test.StartTime).Milliseconds(),
			})
		}
		snapshot.Running = entries
	}

	return snapshot
}

func (c *ControlServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !c.checkStatusAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin:  func(r *http.Request) bool { return c.originAllowed(r) },
		Subprotocols: []string{wsPrimaryProtocol},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	client := &statusClient{send: make(chan []byte, 32)}
	c.status.hub.Register(client)

	var closeOnce sync.Once
	done := make(chan struct{})
	closeConn := func() {
		closeOnce.Do(func() {
			close(done)
			_ = conn.Close()
		})
	}
	var subMu sync.Mutex
	stopTicker := func() {
		subMu.Lock()
		if client.tickerCancel != nil {
			client.tickerCancel()
			client.tickerCancel = nil
		}
		client.subscribed = false
		client.intervalMs = 0
		subMu.Unlock()
	}

	sendJSON := func(payload any) {
		select {
		case <-done:
			return
		default:
		}
		data, _ := json.Marshal(payload)
		select {
		case client.send <- data:
		default:
		}
	}

	sendError := func(code, message string) {
		sendJSON(statusMessage{
			SchemaVersion:      1,
			Type:               "error",
			statusErrorPayload: &statusErrorPayload{Code: code, Message: message},
		})
	}

	sendSnapshots := func() {
		select {
		case <-done:
			return
		default:
		}
		now := time.Now()
		tcp, udp := c.status.Snapshot()
		sendJSON(connectionsSnapshotMessage{
			SchemaVersion: 1,
			Type:          "connections_snapshot",
			Timestamp:     now.UnixMilli(),
			TCP:           tcp,
			UDP:           udp,
		})
		sendJSON(c.getQueueSnapshot(now))
	}

	startTicker := func(intervalMs int, sendInitial bool) {
		stopTicker()
		subMu.Lock()
		client.subscribed = true
		client.intervalMs = intervalMs
		ctx, cancel := context.WithCancel(context.Background())
		client.tickerCancel = cancel
		subMu.Unlock()
		if sendInitial {
			sendSnapshots()
		}
		go func() {
			ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-done:
					return
				case <-ticker.C:
					sendSnapshots()
				}
			}
		}()
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			stopTicker()
			closeConn()
			c.status.hub.Unregister(client)
		})
	}

	go func() {
		defer cleanup()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				Type       string `json:"type"`
				IntervalMs int    `json:"interval_ms"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			switch req.Type {
			case "subscribe":
				if req.IntervalMs != 1000 && req.IntervalMs != 2000 && req.IntervalMs != 5000 {
					sendError("invalid_interval", "interval_ms must be 1000, 2000, or 5000")
					continue
				}
				subMu.Lock()
				alreadySubscribed := client.subscribed
				subMu.Unlock()
				startTicker(req.IntervalMs, !alreadySubscribed)
			case "unsubscribe":
				stopTicker()
			}
		}
	}()

	go func() {
		defer cleanup()
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(wsWriteWait)); err != nil {
					return
				}
			case data, ok := <-client.send:
				if !ok {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}
			}
		}
	}()
}

func (c *ControlServer) handleIdentity(w http.ResponseWriter, r *http.Request) {
	if !c.checkAuth(r) {
		writeJSON(w, http.StatusUnauthorized, rpcResponse{Ok: false, Error: "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rpcResponse{Ok: false, Error: "method not allowed"})
		return
	}
	name := strings.TrimSpace(c.hostname)
	if name == "" {
		name, _ = os.Hostname()
	}
	resp := identityResponse{
		Hostname: name,
		IPs:      listActiveIPs(),
		Version:  version.Version,
	}
	writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: resp})
}

func listActiveIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	addrs := collectIPs(ifaces, func(iface net.Interface) bool {
		return iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0
	})
	if len(addrs) > 0 {
		return addrs
	}
	return collectIPs(ifaces, func(iface net.Interface) bool {
		return iface.Flags&net.FlagLoopback == 0
	})
}

func collectIPs(ifaces []net.Interface, filter func(net.Interface) bool) []string {
	ips := make([]string, 0)
	for _, iface := range ifaces {
		if !filter(iface) {
			continue
		}
		addrList, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrList {
			ip := addrToIP(addr)
			if ip == "" {
				continue
			}
			ips = append(ips, ip)
		}
	}
	return ips
}

func addrToIP(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.IPNet:
		if v.IP == nil {
			return ""
		}
		return v.IP.String()
	case *net.IPAddr:
		if v.IP == nil {
			return ""
		}
		return v.IP.String()
	default:
		return ""
	}
}

func (c *ControlServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !c.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c.metrics.Handler(w, r)
}

func (c *ControlServer) checkAuth(r *http.Request) bool {
	token, ok := bearerToken(r)
	if !ok {
		return false
	}
	return secureTokenEqual(token, c.cfg.AuthToken)
}

func (c *ControlServer) checkStatusAuth(r *http.Request) bool {
	if token, ok := bearerToken(r); ok {
		return secureTokenEqual(token, c.cfg.AuthToken)
	}
	if token, ok := tokenFromWebSocketProtocols(r); ok {
		return secureTokenEqual(token, c.cfg.AuthToken)
	}
	return false
}

func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

func tokenFromWebSocketProtocols(r *http.Request) (string, bool) {
	for _, proto := range websocket.Subprotocols(r) {
		if !strings.HasPrefix(proto, wsTokenPrefix) {
			continue
		}
		encoded := strings.TrimPrefix(proto, wsTokenPrefix)
		if encoded == "" {
			continue
		}
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(decoded) == 0 {
			continue
		}
		return string(decoded), true
	}
	return "", false
}

func secureTokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (c *ControlServer) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, status int, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

type rateLimiter struct {
	mu      sync.Mutex
	clients map[string]*clientLimiter
	rate    float64
	burst   float64
	ttl     time.Duration
}

type clientLimiter struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rate float64, burst int, ttl time.Duration) *rateLimiter {
	return &rateLimiter{
		clients: make(map[string]*clientLimiter),
		rate:    rate,
		burst:   float64(burst),
		ttl:     ttl,
	}
}

func (r *rateLimiter) Allow(key string) bool {
	if key == "" {
		return false
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	limiter := r.clients[key]
	if limiter != nil && now.Sub(limiter.last) > r.ttl {
		delete(r.clients, key)
		limiter = nil
	}
	if limiter == nil {
		r.clients[key] = &clientLimiter{
			tokens: r.burst - 1,
			last:   now,
		}
		return true
	}
	elapsed := now.Sub(limiter.last).Seconds()
	limiter.tokens = minFloat(r.burst, limiter.tokens+elapsed*r.rate)
	limiter.last = now
	if limiter.tokens < 1 {
		return false
	}
	limiter.tokens -= 1
	return true
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
