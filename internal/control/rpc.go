package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/NodePath81/fbforward/internal/util"
)

type rpcRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcResponse struct {
	Ok     bool        `json:"ok"`
	Error  string      `json:"error,omitempty"`
	Result interface{} `json:"result,omitempty"`
}

type rpcContext struct {
	Server  *ControlServer
	Request *http.Request
	Meta    requestCtx
}

type rpcFault struct {
	Status  int
	Message string
}

type RPCHandler func(*rpcContext, json.RawMessage) (any, *rpcFault)

type rpcRegistry struct {
	mu       sync.RWMutex
	handlers map[string]RPCHandler
}

func newRPCRegistry() *rpcRegistry {
	return &rpcRegistry{handlers: make(map[string]RPCHandler)}
}

func (r *rpcRegistry) Register(name string, handler RPCHandler) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("rpc handler name is empty")
	}
	if handler == nil {
		return errors.New("rpc handler is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[name]; exists {
		return errors.New("rpc handler already registered")
	}
	r.handlers[name] = handler
	return nil
}

func (r *rpcRegistry) Lookup(name string) (RPCHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[name]
	return handler, ok
}

func (c *ControlServer) registerRPCHandlers() {
	registrations := map[string]RPCHandler{
		"SetUpstream":          c.rpcSetUpstream,
		"GetStatus":            c.rpcGetStatus,
		"ListUpstreams":        c.rpcListUpstreams,
		"RunMeasurement":       c.rpcRunMeasurement,
		"Restart":              c.rpcRestart,
		"SendTestNotification": c.rpcSendTestNotification,
		"GetMeasurementConfig": c.rpcGetMeasurementConfig,
		"GetRuntimeConfig":     c.rpcGetRuntimeConfig,
		"GetScheduleStatus":    c.rpcGetScheduleStatus,
		"GetGeoIPStatus":       c.rpcGetGeoIPStatus,
		"RefreshGeoIP":         c.rpcRefreshGeoIP,
		"GetIPLogStatus":       c.rpcGetIPLogStatus,
		"QueryIPLog":           c.rpcQueryIPLog,
		"QueryRejectionLog":    c.rpcQueryRejectionLog,
		"QueryLogEvents":       c.rpcQueryLogEvents,
		"GetTopTalkers":        c.rpcGetTopTalkers,
	}
	for name, handler := range registrations {
		if err := c.rpcs.Register(name, handler); err != nil {
			panic(err)
		}
	}
	c.registerFirewallRPCs()
}

func decodeOptionalParams(raw json.RawMessage, target any) *rpcFault {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	return nil
}

func decodeRequiredParams(raw json.RawMessage, target any) *rpcFault {
	if len(raw) == 0 || string(raw) == "null" {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	return nil
}

func rpcOK(result any) (any, *rpcFault) { return result, nil }

func rpcError(status int, message string) (any, *rpcFault) {
	return nil, &rpcFault{Status: status, Message: message}
}

func (c *ControlServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	authOK := c.checkAuth(r)
	reqCtx := c.newRequestCtx(r, "http", authOK)
	sw := &statusWriter{ResponseWriter: w}
	completionErr := ""
	defer c.auditCompletion(sw, reqCtx, &completionErr, "control.rpc.request_completed")
	c.auditPolicy(reqCtx, "control.rpc.access_policy_decision")

	if !c.limiter.Allow(reqCtx.clientIP) {
		completionErr = "rate limit exceeded"
		c.auditFailure(reqCtx, "control.rpc.rate_limited", completionErr, http.StatusTooManyRequests, "denied")
		writeAPIError(sw, http.StatusTooManyRequests, completionErr)
		return
	}
	if !authOK {
		completionErr = "unauthorized"
		c.auditFailure(reqCtx, "control.rpc.auth_failed", completionErr, http.StatusUnauthorized, "denied")
		writeAPIError(sw, http.StatusUnauthorized, completionErr)
		return
	}
	if r.Method != http.MethodPost {
		completionErr = "method not allowed"
		c.auditFailure(reqCtx, "control.rpc.request_invalid", completionErr, http.StatusMethodNotAllowed, "rejected")
		writeAPIError(sw, http.StatusMethodNotAllowed, completionErr)
		return
	}
	if r.ContentLength > maxRPCBodyBytes {
		completionErr = "request body too large"
		c.auditFailure(reqCtx, "control.rpc.request_invalid", completionErr, http.StatusRequestEntityTooLarge, "rejected")
		writeAPIError(sw, http.StatusRequestEntityTooLarge, completionErr)
		return
	}

	r.Body = http.MaxBytesReader(sw, r.Body, maxRPCBodyBytes)
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		completionErr = "invalid json"
		status := http.StatusBadRequest
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			completionErr = "request body too large"
			status = http.StatusRequestEntityTooLarge
		}
		c.auditFailure(reqCtx, "control.rpc.request_invalid", completionErr, status, "rejected")
		writeAPIError(sw, status, completionErr)
		return
	}
	if req.Method == "" {
		completionErr = "unknown method"
		c.auditFailure(reqCtx, "control.rpc.request_invalid", completionErr, http.StatusBadRequest, "rejected")
		writeAPIError(sw, http.StatusBadRequest, completionErr)
		return
	}
	util.Event(c.logger, slogLevelInfo(), "control.rpc.request_received", append(requestAttrs(reqCtx), "rpc.method", req.Method)...)

	handler, ok := c.rpcs.Lookup(req.Method)
	if !ok {
		completionErr = "unknown method"
		c.auditFailure(reqCtx, "control.rpc.request_invalid", completionErr, http.StatusBadRequest, "rejected")
		writeAPIError(sw, http.StatusBadRequest, completionErr)
		return
	}
	result, fault := handler(&rpcContext{Server: c, Request: r, Meta: reqCtx}, req.Params)
	if fault != nil {
		completionErr = fault.Message
		status := fault.Status
		if status == 0 {
			status = http.StatusInternalServerError
		}
		writeAPIError(sw, status, fault.Message)
		return
	}
	writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: result})
}
