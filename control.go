package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type ControlServer struct {
	cfg          ControlConfig
	webUIEnabled bool
	manager      *UpstreamManager
	metrics      *Metrics
	status       *StatusStore
	restartFn    func() error
	logger       Logger
	server       *http.Server
}

func NewControlServer(cfg ControlConfig, webUIEnabled bool, manager *UpstreamManager, metrics *Metrics, status *StatusStore, restartFn func() error, logger Logger) *ControlServer {
	return &ControlServer{
		cfg:          cfg,
		webUIEnabled: webUIEnabled,
		manager:      manager,
		metrics:      metrics,
		status:       status,
		restartFn:    restartFn,
		logger:       logger,
	}
}

func (c *ControlServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", c.metrics.Handler)
	mux.HandleFunc("/rpc", c.handleRPC)
	mux.HandleFunc("/status", c.handleStatus)
	mux.Handle("/", WebUIHandler(c.webUIEnabled))

	addr := netJoin(c.cfg.Addr, c.cfg.Port)
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

type statusResponse struct {
	Mode           string            `json:"mode"`
	ActiveUpstream string            `json:"active_upstream"`
	Upstreams      []UpstreamSnapshot `json:"upstreams"`
	Counts         statusCounts      `json:"counts"`
}

type statusCounts struct {
	TCPActive int `json:"tcp_active"`
	UDPActive int `json:"udp_active"`
}

func (c *ControlServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	if !c.checkAuth(r) {
		writeJSON(w, http.StatusUnauthorized, rpcResponse{Ok: false, Error: "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, rpcResponse{Ok: false, Error: "method not allowed"})
		return
	}
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
			c.metrics.SetMode(ModeAuto)
			c.logger.Info("manual override cleared")
			writeJSON(w, http.StatusOK, rpcResponse{Ok: true})
			return
		}
		if mode == "manual" {
			if err := c.manager.SetManual(params.Tag); err != nil {
				writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: err.Error()})
				return
			}
			c.metrics.SetMode(ModeManual)
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
		tcp, udp := c.status.Snapshot()
		resp := statusResponse{
			Mode:           c.manager.Mode().String(),
			ActiveUpstream: c.manager.ActiveTag(),
			Upstreams:      upstreams,
			Counts: statusCounts{
				TCPActive: len(tcp),
				UDPActive: len(udp),
			},
		}
		writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: resp})
	case "ListUpstreams":
		writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: c.manager.Snapshot()})
	default:
		writeJSON(w, http.StatusBadRequest, rpcResponse{Ok: false, Error: "unknown method"})
	}
}

func (c *ControlServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	// Browsers can't set Authorization headers for WebSocket upgrades, so allow ?token=.
	if !c.checkAuth(r) && !c.checkTokenQuery(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &statusClient{send: make(chan []byte, 32)}
	c.status.hub.Register(client)

	go func() {
		defer func() {
			c.status.hub.Unregister(client)
			close(client.send)
			_ = conn.Close()
		}()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			if req.Type == "snapshot" {
				tcp, udp := c.status.Snapshot()
				snapshot := statusMessage{Type: "snapshot", TCP: tcp, UDP: udp}
				data, _ := json.Marshal(snapshot)
				select {
				case client.send <- data:
				default:
				}
			}
		}
	}()

	go func() {
		for data := range client.send {
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}()
}

func (c *ControlServer) checkAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	return token != "" && token == c.cfg.Token
}

func (c *ControlServer) checkTokenQuery(r *http.Request) bool {
	if token := r.URL.Query().Get("token"); token != "" {
		return token == c.cfg.Token
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
