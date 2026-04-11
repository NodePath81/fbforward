package control

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/coordination"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/iplog"
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
	rpcRateTTL        = 5 * time.Minute
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
	manager     upstream.UpstreamStateReader
	metrics     *metrics.Metrics
	status      *StatusStore
	coord       *coordination.Controller
	restartFn   func() error
	logger      util.Logger
	server      *http.Server
	limiter     *rateLimiter
	schedulerMu sync.RWMutex
	scheduler   *measure.Scheduler
	collectorMu sync.RWMutex
	collector   *measure.Collector
	geoipMu     sync.RWMutex
	geoipMgr    geoipManager
	iplogMu     sync.RWMutex
	iplogStore  *iplog.Store
	nextReqID   uint64
	nextWSID    uint64
}

type geoipManager interface {
	Status() geoip.Status
	RefreshNow(context.Context) (geoip.RefreshResult, error)
}

func NewControlServer(cfg config.Config, manager upstream.UpstreamStateReader, metrics *metrics.Metrics, status *StatusStore, coord *coordination.Controller, restartFn func() error, logger util.Logger) *ControlServer {
	return &ControlServer{
		fullCfg:     cfg,
		cfg:         cfg.Control,
		measurement: cfg.Measurement,
		hostname:    cfg.Hostname,
		manager:     manager,
		metrics:     metrics,
		status:      status,
		coord:       coord,
		restartFn:   restartFn,
		logger:      util.ComponentLogger(logger, util.CompControl),
		limiter:     newRateLimiter(rpcRatePerSecond, rpcRateBurst, rpcRateTTL),
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
	go c.limiter.RunCleanup(ctx.Done(), rpcRateTTL)

	go func() {
		_ = c.server.ListenAndServe()
	}()
	util.Event(c.logger, slog.LevelInfo, "control.server_started", "listen.addr", addr)
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

func (c *ControlServer) SetGeoIPManager(manager geoipManager) {
	c.geoipMu.Lock()
	defer c.geoipMu.Unlock()
	c.geoipMgr = manager
}

func (c *ControlServer) SetIPLogStore(store *iplog.Store) {
	c.iplogMu.Lock()
	defer c.iplogMu.Unlock()
	c.iplogStore = store
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

type queryIPLogParams struct {
	StartTime *int64 `json:"start_time,omitempty"`
	EndTime   *int64 `json:"end_time,omitempty"`
	CIDR      string `json:"cidr,omitempty"`
	ASN       *int   `json:"asn,omitempty"`
	Country   string `json:"country,omitempty"`
	SortBy    string `json:"sort_by,omitempty"`
	SortOrder string `json:"sort_order,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

type queryRejectionLogParams struct {
	StartTime        *int64 `json:"start_time,omitempty"`
	EndTime          *int64 `json:"end_time,omitempty"`
	CIDR             string `json:"cidr,omitempty"`
	ASN              *int   `json:"asn,omitempty"`
	Country          string `json:"country,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Protocol         string `json:"protocol,omitempty"`
	Port             *int   `json:"port,omitempty"`
	MatchedRuleType  string `json:"matched_rule_type,omitempty"`
	MatchedRuleValue string `json:"matched_rule_value,omitempty"`
	SortBy           string `json:"sort_by,omitempty"`
	SortOrder        string `json:"sort_order,omitempty"`
	Limit            int    `json:"limit,omitempty"`
	Offset           int    `json:"offset,omitempty"`
}

type queryLogEventsParams struct {
	StartTime        *int64 `json:"start_time,omitempty"`
	EndTime          *int64 `json:"end_time,omitempty"`
	CIDR             string `json:"cidr,omitempty"`
	ASN              *int   `json:"asn,omitempty"`
	Country          string `json:"country,omitempty"`
	Protocol         string `json:"protocol,omitempty"`
	Port             *int   `json:"port,omitempty"`
	Reason           string `json:"reason,omitempty"`
	MatchedRuleType  string `json:"matched_rule_type,omitempty"`
	MatchedRuleValue string `json:"matched_rule_value,omitempty"`
	EntryType        string `json:"entry_type,omitempty"`
	SortBy           string `json:"sort_by,omitempty"`
	SortOrder        string `json:"sort_order,omitempty"`
	Limit            int    `json:"limit,omitempty"`
	Offset           int    `json:"offset,omitempty"`
}

type ipLogStatusResponse struct {
	DBPath               string `json:"db_path"`
	FileSize             int64  `json:"file_size"`
	RecordCount          int    `json:"record_count"`
	FlowRecordCount      int    `json:"flow_record_count"`
	RejectionRecordCount int    `json:"rejection_record_count"`
	TotalRecordCount     int    `json:"total_record_count"`
	OldestRecordAt       int64  `json:"oldest_record_at"`
	NewestRecordAt       int64  `json:"newest_record_at"`
	Retention            string `json:"retention"`
	PruneInterval        string `json:"prune_interval"`
}

type statusResponse struct {
	Mode           string                      `json:"mode"`
	ActiveUpstream string                      `json:"active_upstream"`
	Upstreams      []upstream.UpstreamSnapshot `json:"upstreams"`
	Coordination   coordinationStatusResponse  `json:"coordination"`
}

type coordinationStatusResponse struct {
	Available        bool   `json:"available"`
	Connected        bool   `json:"connected"`
	Authoritative    bool   `json:"authoritative"`
	SelectedUpstream string `json:"selected_upstream"`
	Version          int64  `json:"version"`
	FallbackActive   bool   `json:"fallback_active"`
}

type runningTestEntry struct {
	Upstream  string `json:"upstream"`
	Protocol  string `json:"protocol"`
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
	NextDueMs     *int64              `json:"next_due_ms,omitempty"`
	Running       []runningTestEntry  `json:"running"`
	Pending       []queuePendingEntry `json:"pending"`
}

type queuePendingEntry struct {
	Upstream    string `json:"upstream"`
	Protocol    string `json:"protocol"`
	ScheduledAt int64  `json:"scheduled_at"`
}

type identityResponse struct {
	Hostname string   `json:"hostname"`
	IPs      []string `json:"ips"`
	Version  string   `json:"version"`
}

type requestCtx struct {
	id         string
	start      time.Time
	protocol   string
	method     string
	path       string
	clientAddr string
	clientIP   string
	userAgent  string
	authMethod string
	authOK     bool
}

type statusWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.written {
		sw.code = code
		sw.written = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.written {
		sw.code = http.StatusOK
		sw.written = true
	}
	return sw.ResponseWriter.Write(b)
}

func (sw *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := sw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (c *ControlServer) newRequestCtx(r *http.Request, protocol string, authOK bool) requestCtx {
	return requestCtx{
		id:         fmt.Sprintf("r-%d", atomic.AddUint64(&c.nextReqID, 1)),
		start:      time.Now(),
		protocol:   protocol,
		method:     r.Method,
		path:       r.URL.Path,
		clientAddr: r.RemoteAddr,
		clientIP:   clientIP(r),
		userAgent:  r.UserAgent(),
		authMethod: authMethodForRequest(r),
		authOK:     authOK,
	}
}

func authMethodForRequest(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth != "" {
		if _, ok := bearerToken(r); ok {
			return "bearer"
		}
		return "unknown"
	}
	if _, ok := tokenFromWebSocketProtocols(r); ok {
		return "ws_subprotocol"
	}
	if len(websocket.Subprotocols(r)) > 0 {
		return "unknown"
	}
	return "none"
}

func (c *ControlServer) policyAttrs() []any {
	return []any{
		"access.policy.name", "none",
		"access.policy.decision", "not_applicable",
		"access.policy.reason", "no_policy_configured",
	}
}

func requestAttrs(req requestCtx) []any {
	return []any{
		"request.id", req.id,
		"request.protocol", req.protocol,
		"request.method", req.method,
		"request.path", req.path,
		"client.addr", req.clientAddr,
		"client.ip", req.clientIP,
		"http.user_agent", req.userAgent,
		"auth.identity", "unknown",
		"auth.method", req.authMethod,
		"auth.authenticated", req.authOK,
	}
}

func completionResult(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return "success"
	case statusCode == 401 || statusCode == 403 || statusCode == 429:
		return "denied"
	case statusCode == 400 || statusCode == 404 || statusCode == 405:
		return "rejected"
	default:
		return "failed"
	}
}

func (c *ControlServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	authOK := c.checkAuth(r)
	reqCtx := c.newRequestCtx(r, "http", authOK)
	sw := &statusWriter{ResponseWriter: w}
	completionErr := ""

	defer func() {
		code := sw.code
		if !sw.written {
			code = 0
		}
		result := completionResult(code)
		if code == 0 {
			result = "failed"
		}
		attrs := append(requestAttrs(reqCtx),
			"http.status_code", code,
			"result", result,
			"latency_ms", time.Since(reqCtx.start).Milliseconds(),
		)
		if result != "success" && completionErr != "" {
			attrs = append(attrs, "error", completionErr)
		}
		util.Event(c.logger, slog.LevelInfo, "control.rpc.request_completed", attrs...)
	}()

	policyAttrs := append([]any{
		"request.id", reqCtx.id,
		"client.ip", reqCtx.clientIP,
		"request.method", reqCtx.method,
		"request.path", reqCtx.path,
		"http.user_agent", reqCtx.userAgent,
		"auth.identity", "unknown",
		"auth.method", reqCtx.authMethod,
		"auth.authenticated", reqCtx.authOK,
	}, c.policyAttrs()...)
	policyAttrs = append(policyAttrs, "result", "success")
	util.Event(c.logger, slog.LevelInfo, "control.rpc.access_policy_decision", policyAttrs...)

	if !c.limiter.Allow(reqCtx.clientIP) {
		completionErr = "rate limit exceeded"
		util.Event(c.logger, slog.LevelWarn, "control.rpc.rate_limited",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"request.method", reqCtx.method,
			"request.path", reqCtx.path,
			"http.user_agent", reqCtx.userAgent,
			"auth.identity", "unknown",
			"auth.method", reqCtx.authMethod,
			"auth.authenticated", reqCtx.authOK,
			"http.status_code", http.StatusTooManyRequests,
			"result", "denied",
		)
		writeJSON(sw, http.StatusTooManyRequests, rpcResponse{Ok: false, Error: completionErr})
		return
	}
	if !authOK {
		completionErr = "unauthorized"
		util.Event(c.logger, slog.LevelWarn, "control.rpc.auth_failed",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"request.method", reqCtx.method,
			"request.path", reqCtx.path,
			"http.user_agent", reqCtx.userAgent,
			"auth.identity", "unknown",
			"auth.method", reqCtx.authMethod,
			"auth.authenticated", false,
			"http.status_code", http.StatusUnauthorized,
			"result", "denied",
		)
		writeJSON(sw, http.StatusUnauthorized, rpcResponse{Ok: false, Error: completionErr})
		return
	}
	if r.Method != http.MethodPost {
		completionErr = "method not allowed"
		util.Event(c.logger, slog.LevelWarn, "control.rpc.request_invalid",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"error", completionErr,
			"http.status_code", http.StatusMethodNotAllowed,
			"result", "rejected",
		)
		writeJSON(sw, http.StatusMethodNotAllowed, rpcResponse{Ok: false, Error: completionErr})
		return
	}

	r.Body = http.MaxBytesReader(sw, r.Body, maxRPCBodyBytes)
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		completionErr = "invalid json"
		util.Event(c.logger, slog.LevelWarn, "control.rpc.request_invalid",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"error", completionErr,
			"http.status_code", http.StatusBadRequest,
			"result", "rejected",
		)
		writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
		return
	}

	util.Event(c.logger, slog.LevelInfo, "control.rpc.request_received",
		append(requestAttrs(reqCtx), "rpc.method", req.Method)...,
	)

	switch req.Method {
	case "SetUpstream":
		var params setUpstreamParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			completionErr = "invalid params"
			util.Event(c.logger, slog.LevelWarn, "control.rpc.request_invalid",
				"request.id", reqCtx.id,
				"client.addr", reqCtx.clientAddr,
				"client.ip", reqCtx.clientIP,
				"error", completionErr,
				"http.status_code", http.StatusBadRequest,
				"result", "rejected",
			)
			writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		mode := strings.ToLower(params.Mode)
		if mode == "auto" {
			c.manager.SetAuto()
			if c.coord != nil {
				c.coord.Disable()
			}
			c.metrics.SetMode(upstream.ModeAuto)
			c.metrics.SetCoordinationState(c.manager.CoordinationState())
			util.Event(c.logger, slog.LevelInfo, "control.rpc.set_upstream_applied",
				"rpc.method", req.Method,
				"upstream", "",
				"upstream.mode", mode,
			)
			writeJSON(sw, http.StatusOK, rpcResponse{Ok: true})
			return
		}
		if mode == "manual" {
			if err := c.manager.SetManual(params.Tag); err != nil {
				completionErr = err.Error()
				writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
				return
			}
			if c.coord != nil {
				c.coord.Disable()
			}
			c.metrics.SetMode(upstream.ModeManual)
			c.metrics.SetCoordinationState(c.manager.CoordinationState())
			util.Event(c.logger, slog.LevelInfo, "control.rpc.set_upstream_applied",
				"rpc.method", req.Method,
				"upstream", params.Tag,
				"upstream.mode", mode,
			)
			writeJSON(sw, http.StatusOK, rpcResponse{Ok: true})
			return
		}
		if mode == "coordination" {
			if c.coord == nil || !c.coord.Configured() {
				completionErr = "coordination mode is not configured"
				writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
				return
			}
			c.manager.SetCoordination()
			c.coord.Enable()
			c.metrics.SetMode(upstream.ModeCoordination)
			c.metrics.SetCoordinationState(c.manager.CoordinationState())
			util.Event(c.logger, slog.LevelInfo, "control.rpc.set_upstream_applied",
				"rpc.method", req.Method,
				"upstream", "",
				"upstream.mode", mode,
			)
			writeJSON(sw, http.StatusOK, rpcResponse{Ok: true})
			return
		}
		completionErr = "invalid mode"
		util.Event(c.logger, slog.LevelWarn, "control.rpc.request_invalid",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"error", completionErr,
			"http.status_code", http.StatusBadRequest,
			"result", "rejected",
		)
		writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
	case "Restart":
		util.Event(c.logger, slog.LevelInfo, "control.rpc.restart_requested", "rpc.method", req.Method)
		go func(requestID string) {
			if err := c.restartFn(); err != nil {
				util.Event(c.logger, slog.LevelError, "control.rpc.restart_completed",
					"request.id", requestID,
					"rpc.method", req.Method,
					"result", "failed",
					"error", err,
				)
				return
			}
			util.Event(c.logger, slog.LevelInfo, "control.rpc.restart_completed",
				"request.id", requestID,
				"rpc.method", req.Method,
				"result", "success",
			)
		}(reqCtx.id)
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true})
	case "GetStatus":
		upstreams := c.manager.Snapshot()
		coordState := c.manager.CoordinationState()
		resp := statusResponse{
			Mode:           c.manager.Mode().String(),
			ActiveUpstream: c.manager.ActiveTag(),
			Upstreams:      upstreams,
			Coordination: coordinationStatusResponse{
				Available:        c.fullCfg.Coordination.IsConfigured(),
				Connected:        coordState.Connected,
				Authoritative:    coordState.Authoritative,
				SelectedUpstream: coordState.SelectedUpstream,
				Version:          coordState.Version,
				FallbackActive:   coordState.FallbackActive,
			},
		}
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: resp})
	case "GetMeasurementConfig":
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: c.getMeasurementConfig()})
	case "GetRuntimeConfig":
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: c.getRuntimeConfig()})
	case "GetScheduleStatus":
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: c.getScheduleStatus()})
	case "GetGeoIPStatus":
		c.geoipMu.RLock()
		geoipMgr := c.geoipMgr
		c.geoipMu.RUnlock()
		if geoipMgr == nil {
			completionErr = "geoip manager not available"
			writeJSON(sw, http.StatusServiceUnavailable, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: geoipMgr.Status()})
	case "RefreshGeoIP":
		c.geoipMu.RLock()
		geoipMgr := c.geoipMgr
		c.geoipMu.RUnlock()
		if geoipMgr == nil {
			completionErr = "geoip manager not available"
			writeJSON(sw, http.StatusServiceUnavailable, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		result, err := geoipMgr.RefreshNow(r.Context())
		if err != nil {
			completionErr = err.Error()
			status := http.StatusInternalServerError
			if errors.Is(err, geoip.ErrNoConfiguredDatabases) {
				status = http.StatusServiceUnavailable
			}
			writeJSON(sw, status, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: result})
	case "GetIPLogStatus":
		c.iplogMu.RLock()
		store := c.iplogStore
		c.iplogMu.RUnlock()
		if store == nil {
			completionErr = "ip log store not available"
			writeJSON(sw, http.StatusServiceUnavailable, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		result, err := c.getIPLogStatus(store)
		if err != nil {
			completionErr = err.Error()
			writeJSON(sw, http.StatusInternalServerError, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: result})
	case "QueryIPLog":
		c.iplogMu.RLock()
		store := c.iplogStore
		c.iplogMu.RUnlock()
		if store == nil {
			completionErr = "ip log store not available"
			writeJSON(sw, http.StatusServiceUnavailable, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		var params queryIPLogParams
		if len(req.Params) > 0 && string(req.Params) != "null" {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				completionErr = "invalid params"
				writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
				return
			}
		}
		result, err := store.Query(iplog.QueryParams{
			StartTime: params.StartTime,
			EndTime:   params.EndTime,
			CIDR:      params.CIDR,
			ASN:       params.ASN,
			Country:   params.Country,
			SortBy:    params.SortBy,
			SortOrder: params.SortOrder,
			Limit:     params.Limit,
			Offset:    params.Offset,
		})
		if err != nil {
			completionErr = err.Error()
			writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: result})
	case "QueryRejectionLog":
		c.iplogMu.RLock()
		store := c.iplogStore
		c.iplogMu.RUnlock()
		if store == nil {
			completionErr = "ip log store not available"
			writeJSON(sw, http.StatusServiceUnavailable, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		var params queryRejectionLogParams
		if len(req.Params) > 0 && string(req.Params) != "null" {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				completionErr = "invalid params"
				writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
				return
			}
		}
		result, err := store.QueryRejections(iplog.RejectionQueryParams{
			StartTime:        params.StartTime,
			EndTime:          params.EndTime,
			CIDR:             params.CIDR,
			ASN:              params.ASN,
			Country:          params.Country,
			Reason:           params.Reason,
			Protocol:         params.Protocol,
			Port:             params.Port,
			MatchedRuleType:  params.MatchedRuleType,
			MatchedRuleValue: params.MatchedRuleValue,
			SortBy:           params.SortBy,
			SortOrder:        params.SortOrder,
			Limit:            params.Limit,
			Offset:           params.Offset,
		})
		if err != nil {
			completionErr = err.Error()
			writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: result})
	case "QueryLogEvents":
		c.iplogMu.RLock()
		store := c.iplogStore
		c.iplogMu.RUnlock()
		if store == nil {
			completionErr = "ip log store not available"
			writeJSON(sw, http.StatusServiceUnavailable, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		var params queryLogEventsParams
		if len(req.Params) > 0 && string(req.Params) != "null" {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				completionErr = "invalid params"
				writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
				return
			}
		}
		result, err := store.QueryLogEvents(iplog.LogEventQueryParams{
			StartTime:        params.StartTime,
			EndTime:          params.EndTime,
			CIDR:             params.CIDR,
			ASN:              params.ASN,
			Country:          params.Country,
			Protocol:         params.Protocol,
			Port:             params.Port,
			Reason:           params.Reason,
			MatchedRuleType:  params.MatchedRuleType,
			MatchedRuleValue: params.MatchedRuleValue,
			EntryType:        params.EntryType,
			SortBy:           params.SortBy,
			SortOrder:        params.SortOrder,
			Limit:            params.Limit,
			Offset:           params.Offset,
		})
		if err != nil {
			completionErr = err.Error()
			writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: result})
	case "ListUpstreams":
		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: c.manager.Snapshot()})
	case "RunMeasurement":
		c.collectorMu.RLock()
		collector := c.collector
		c.collectorMu.RUnlock()
		if collector == nil {
			completionErr = "collector not ready"
			writeJSON(sw, http.StatusServiceUnavailable, rpcResponse{Ok: false, Error: completionErr})
			return
		}

		var params runMeasurementParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			completionErr = "invalid params"
			util.Event(c.logger, slog.LevelWarn, "control.rpc.request_invalid",
				"request.id", reqCtx.id,
				"client.addr", reqCtx.clientAddr,
				"client.ip", reqCtx.clientIP,
				"error", completionErr,
				"http.status_code", http.StatusBadRequest,
				"result", "rejected",
			)
			writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		tag := strings.TrimSpace(params.Tag)
		protocol := strings.ToLower(strings.TrimSpace(params.Protocol))
		if protocol != "tcp" && protocol != "udp" {
			completionErr = "protocol must be tcp or udp"
			util.Event(c.logger, slog.LevelWarn, "control.rpc.request_invalid",
				"request.id", reqCtx.id,
				"client.addr", reqCtx.clientAddr,
				"client.ip", reqCtx.clientIP,
				"error", completionErr,
				"http.status_code", http.StatusBadRequest,
				"result", "rejected",
			)
			writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
			return
		}
		up := c.manager.Get(tag)
		if up == nil {
			completionErr = "upstream not found"
			writeJSON(sw, http.StatusNotFound, rpcResponse{Ok: false, Error: completionErr})
			return
		}

		util.Event(c.logger, slog.LevelInfo, "control.rpc.run_measurement_requested",
			"rpc.method", req.Method,
			"upstream", tag,
			"network.protocol", protocol,
		)
		go func(requestID string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := collector.RunProtocol(ctx, up, protocol); err != nil {
				util.Event(c.logger, slog.LevelWarn, "control.rpc.run_measurement_completed",
					"request.id", requestID,
					"rpc.method", req.Method,
					"upstream", tag,
					"network.protocol", protocol,
					"result", "failed",
					"error", err,
				)
				return
			}
			util.Event(c.logger, slog.LevelInfo, "control.rpc.run_measurement_completed",
				"request.id", requestID,
				"rpc.method", req.Method,
				"upstream", tag,
				"network.protocol", protocol,
				"result", "success",
			)
		}(reqCtx.id)

		writeJSON(sw, http.StatusOK, rpcResponse{Ok: true})
	default:
		completionErr = "unknown method"
		util.Event(c.logger, slog.LevelWarn, "control.rpc.request_invalid",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"error", completionErr,
			"http.status_code", http.StatusBadRequest,
			"result", "rejected",
		)
		writeJSON(sw, http.StatusBadRequest, rpcResponse{Ok: false, Error: completionErr})
	}
}

func (c *ControlServer) getIPLogStatus(store *iplog.Store) (ipLogStatusResponse, error) {
	stats, err := store.Stats()
	if err != nil {
		return ipLogStatusResponse{}, err
	}
	return ipLogStatusResponse{
		DBPath:               c.fullCfg.IPLog.DBPath,
		FileSize:             dbFileSize(c.fullCfg.IPLog.DBPath),
		RecordCount:          stats.TotalRecordCount,
		FlowRecordCount:      stats.FlowRecordCount,
		RejectionRecordCount: stats.RejectionRecordCount,
		TotalRecordCount:     stats.TotalRecordCount,
		OldestRecordAt:       stats.OldestRecordAt,
		NewestRecordAt:       stats.NewestRecordAt,
		Retention:            c.fullCfg.IPLog.Retention.Duration().String(),
		PruneInterval:        c.fullCfg.IPLog.PruneInterval.Duration().String(),
	}, nil
}

func dbFileSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
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
		NextDueMs:     nil,
		Running:       []runningTestEntry{},
		Pending:       []queuePendingEntry{},
	}

	if scheduler != nil {
		status := scheduler.Status()
		snapshot.Depth = status.QueueLength
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
				ElapsedMs: now.Sub(test.StartTime).Milliseconds(),
			})
		}
		snapshot.Running = entries
	}

	return snapshot
}

func (c *ControlServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	authOK := c.checkStatusAuth(r)
	reqCtx := c.newRequestCtx(r, "ws", authOK)
	sw := &statusWriter{ResponseWriter: w}
	connID := fmt.Sprintf("ws-%d", atomic.AddUint64(&c.nextWSID, 1))

	policyAttrs := append([]any{
		"request.id", reqCtx.id,
		"client.ip", reqCtx.clientIP,
		"request.method", reqCtx.method,
		"request.path", reqCtx.path,
		"http.user_agent", reqCtx.userAgent,
		"auth.identity", "unknown",
		"auth.method", reqCtx.authMethod,
		"auth.authenticated", reqCtx.authOK,
	}, c.policyAttrs()...)
	policyAttrs = append(policyAttrs, "result", "success")
	util.Event(c.logger, slog.LevelInfo, "control.ws.access_policy_decision", policyAttrs...)

	if !c.limiter.Allow(reqCtx.clientIP) {
		util.Event(c.logger, slog.LevelWarn, "control.ws.rate_limited",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"http.status_code", http.StatusTooManyRequests,
			"result", "denied",
		)
		http.Error(sw, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	if !c.status.hub.CanRegister() {
		util.Event(c.logger, slog.LevelWarn, "control.ws.connection_limit_reached",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"http.status_code", http.StatusServiceUnavailable,
			"result", "denied",
		)
		http.Error(sw, "too many websocket clients", http.StatusServiceUnavailable)
		return
	}
	if !c.originAllowed(r) {
		util.Event(c.logger, slog.LevelWarn, "control.ws.origin_rejected",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"request.method", reqCtx.method,
			"request.path", reqCtx.path,
			"http.user_agent", reqCtx.userAgent,
			"http.status_code", http.StatusForbidden,
			"result", "denied",
		)
		http.Error(sw, "forbidden", http.StatusForbidden)
		return
	}
	if !authOK {
		util.Event(c.logger, slog.LevelWarn, "control.ws.auth_failed",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"request.method", reqCtx.method,
			"request.path", reqCtx.path,
			"http.user_agent", reqCtx.userAgent,
			"auth.identity", "unknown",
			"auth.method", reqCtx.authMethod,
			"auth.authenticated", false,
			"http.status_code", http.StatusUnauthorized,
			"result", "denied",
		)
		http.Error(sw, "unauthorized", http.StatusUnauthorized)
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin:  c.originAllowed,
		Subprotocols: []string{wsPrimaryProtocol},
	}
	conn, err := upgrader.Upgrade(sw, r, nil)
	if err != nil {
		util.Event(c.logger, slog.LevelWarn, "control.ws.upgrade_failed",
			"ws.conn_id", connID,
			"client.addr", reqCtx.clientAddr,
			"error", err,
		)
		return
	}
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	client := &statusClient{send: make(chan []byte, 32), connID: connID}
	if !c.status.hub.TryRegister(client) {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "too many websocket clients"), time.Now().Add(wsWriteWait))
		_ = conn.Close()
		return
	}
	util.Event(c.logger, slog.LevelInfo, "control.ws.connected",
		"ws.conn_id", connID,
		"client.addr", reqCtx.clientAddr,
		"client.ip", reqCtx.clientIP,
	)

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
		util.Event(c.logger, slog.LevelDebug, "control.ws.subscribe_updated",
			"ws.conn_id", connID,
			"interval_ms", intervalMs,
		)
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
			util.Event(c.logger, slog.LevelInfo, "control.ws.disconnected",
				"ws.conn_id", connID,
				"client.addr", reqCtx.clientAddr,
			)
		})
	}

	go func() {
		defer cleanup()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				util.Event(c.logger, slog.LevelDebug, "control.ws.io_or_heartbeat_failed",
					"ws.conn_id", connID,
					"error", err,
				)
				return
			}
			var req struct {
				Type       string `json:"type"`
				IntervalMs int    `json:"interval_ms"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				util.Event(c.logger, slog.LevelDebug, "control.ws.request_invalid",
					"ws.conn_id", connID,
					"error", err,
				)
				continue
			}
			switch req.Type {
			case "subscribe":
				if req.IntervalMs != 1000 && req.IntervalMs != 2000 && req.IntervalMs != 5000 {
					util.Event(c.logger, slog.LevelDebug, "control.ws.request_invalid",
						"ws.conn_id", connID,
						"error", "invalid interval_ms",
					)
					sendError("invalid_interval", "interval_ms must be 1000, 2000, or 5000")
					continue
				}
				subMu.Lock()
				alreadySubscribed := client.subscribed
				subMu.Unlock()
				startTicker(req.IntervalMs, !alreadySubscribed)
			case "unsubscribe":
				stopTicker()
				util.Event(c.logger, slog.LevelDebug, "control.ws.unsubscribed", "ws.conn_id", connID)
			default:
				util.Event(c.logger, slog.LevelDebug, "control.ws.request_invalid",
					"ws.conn_id", connID,
					"error", "unknown request type",
				)
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
					util.Event(c.logger, slog.LevelDebug, "control.ws.io_or_heartbeat_failed",
						"ws.conn_id", connID,
						"error", err,
					)
					return
				}
			case data, ok := <-client.send:
				if !ok {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					util.Event(c.logger, slog.LevelDebug, "control.ws.io_or_heartbeat_failed",
						"ws.conn_id", connID,
						"error", err,
					)
					return
				}
			}
		}
	}()
}

func (c *ControlServer) handleIdentity(w http.ResponseWriter, r *http.Request) {
	authOK := c.checkAuth(r)
	reqCtx := c.newRequestCtx(r, "http", authOK)
	sw := &statusWriter{ResponseWriter: w}
	completionErr := ""
	defer func() {
		code := sw.code
		if !sw.written {
			code = 0
		}
		result := completionResult(code)
		if code == 0 {
			result = "failed"
		}
		attrs := append(requestAttrs(reqCtx),
			"http.status_code", code,
			"result", result,
			"latency_ms", time.Since(reqCtx.start).Milliseconds(),
		)
		if result != "success" && completionErr != "" {
			attrs = append(attrs, "error", completionErr)
		}
		util.Event(c.logger, slog.LevelInfo, "control.identity.request_completed", attrs...)
	}()

	policyAttrs := append([]any{
		"request.id", reqCtx.id,
		"client.ip", reqCtx.clientIP,
		"request.method", reqCtx.method,
		"request.path", reqCtx.path,
		"http.user_agent", reqCtx.userAgent,
		"auth.identity", "unknown",
		"auth.method", reqCtx.authMethod,
		"auth.authenticated", reqCtx.authOK,
	}, c.policyAttrs()...)
	policyAttrs = append(policyAttrs, "result", "success")
	util.Event(c.logger, slog.LevelInfo, "control.identity.access_policy_decision", policyAttrs...)

	if !authOK {
		completionErr = "unauthorized"
		util.Event(c.logger, slog.LevelWarn, "control.identity.auth_failed",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"request.method", reqCtx.method,
			"request.path", reqCtx.path,
			"http.user_agent", reqCtx.userAgent,
			"auth.identity", "unknown",
			"auth.method", reqCtx.authMethod,
			"auth.authenticated", false,
			"http.status_code", http.StatusUnauthorized,
			"result", "denied",
		)
		writeJSON(sw, http.StatusUnauthorized, rpcResponse{Ok: false, Error: completionErr})
		return
	}
	if !c.limiter.Allow(reqCtx.clientIP) {
		completionErr = "rate limit exceeded"
		writeJSON(sw, http.StatusTooManyRequests, rpcResponse{Ok: false, Error: completionErr})
		return
	}
	if r.Method != http.MethodGet {
		completionErr = "method not allowed"
		writeJSON(sw, http.StatusMethodNotAllowed, rpcResponse{Ok: false, Error: completionErr})
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
	writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: resp})
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
	authOK := c.checkAuth(r)
	reqCtx := c.newRequestCtx(r, "http", authOK)
	sw := &statusWriter{ResponseWriter: w}
	completionErr := ""
	defer func() {
		code := sw.code
		if !sw.written {
			code = 0
		}
		result := completionResult(code)
		if code == 0 {
			result = "failed"
		}
		attrs := append(requestAttrs(reqCtx),
			"http.status_code", code,
			"result", result,
			"latency_ms", time.Since(reqCtx.start).Milliseconds(),
		)
		if result != "success" && completionErr != "" {
			attrs = append(attrs, "error", completionErr)
		}
		util.Event(c.logger, slog.LevelInfo, "control.metrics.request_completed", attrs...)
	}()

	policyAttrs := append([]any{
		"request.id", reqCtx.id,
		"client.ip", reqCtx.clientIP,
		"request.method", reqCtx.method,
		"request.path", reqCtx.path,
		"http.user_agent", reqCtx.userAgent,
		"auth.identity", "unknown",
		"auth.method", reqCtx.authMethod,
		"auth.authenticated", reqCtx.authOK,
	}, c.policyAttrs()...)
	policyAttrs = append(policyAttrs, "result", "success")
	util.Event(c.logger, slog.LevelInfo, "control.metrics.access_policy_decision", policyAttrs...)

	if !authOK {
		completionErr = "unauthorized"
		util.Event(c.logger, slog.LevelWarn, "control.metrics.auth_failed",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"request.method", reqCtx.method,
			"request.path", reqCtx.path,
			"http.user_agent", reqCtx.userAgent,
			"auth.identity", "unknown",
			"auth.method", reqCtx.authMethod,
			"auth.authenticated", false,
			"http.status_code", http.StatusUnauthorized,
			"result", "denied",
		)
		http.Error(sw, completionErr, http.StatusUnauthorized)
		return
	}
	if !c.limiter.Allow(reqCtx.clientIP) {
		completionErr = "rate limit exceeded"
		http.Error(sw, completionErr, http.StatusTooManyRequests)
		return
	}
	c.metrics.Handler(sw, r)
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

func (r *rateLimiter) SweepExpired() {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, limiter := range r.clients {
		if limiter == nil || now.Sub(limiter.last) > r.ttl {
			delete(r.clients, key)
		}
	}
}

func (r *rateLimiter) RunCleanup(ctxDone <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = r.ttl
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctxDone:
			return
		case <-ticker.C:
			r.SweepExpired()
		}
	}
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
