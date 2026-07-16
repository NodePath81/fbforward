package flowcontext

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/util"
)

const maxFlowContextBodyBytes int64 = 64 << 10

type HTTPOptions struct {
	Identities      []Identity
	MaxTTL          time.Duration
	ResolveTimeout  time.Duration
	MaxBodyBytes    int64
	MaxKeyLength    int
	MaxValueLength  int
	RateLimitBurst  int
	RateLimitWindow time.Duration
}

func (o HTTPOptions) normalized() HTTPOptions {
	if o.MaxTTL <= 0 {
		o.MaxTTL = 24 * time.Hour
	}
	if o.ResolveTimeout <= 0 {
		o.ResolveTimeout = 5 * time.Second
	}
	if o.MaxBodyBytes <= 0 || o.MaxBodyBytes > maxFlowContextBodyBytes {
		o.MaxBodyBytes = maxFlowContextBodyBytes
	}
	if o.MaxKeyLength <= 0 {
		o.MaxKeyLength = 64
	}
	if o.MaxValueLength <= 0 {
		o.MaxValueLength = 256
	}
	if o.RateLimitBurst <= 0 {
		o.RateLimitBurst = 60
	}
	if o.RateLimitWindow <= 0 {
		o.RateLimitWindow = time.Minute
	}
	return o
}

// Service exposes Flow Context through the ControlServer's TCP HTTP listener.
// Authentication and authorization are independent from the control token.
type Service struct {
	registry   *Registry
	store      *audit.Store
	controller FlowController
	options    HTTPOptions
	identities []Identity
	limiter    *identityRateLimiter
	logger     util.Logger
}

type rpcAuditMeta struct {
	RequestID string
	Method    string
}

type rpcAuditMetaKey struct{}

func contextAuditMeta(ctx context.Context) (rpcAuditMeta, bool) {
	if ctx == nil {
		return rpcAuditMeta{}, false
	}
	meta, ok := ctx.Value(rpcAuditMetaKey{}).(rpcAuditMeta)
	return meta, ok
}

// FlowController applies the small set of direct controls exposed to a
// trusted backend. The data-plane implementation is injected by Runtime.
type FlowController interface {
	Block(flow.ID) bool
	SetLimit(flow.ID, uint64) bool
	ClearLimit(flow.ID) bool
}

func NewService(registry *Registry, store *audit.Store, options HTTPOptions, logger util.Logger) *Service {
	options = options.normalized()
	identities := make([]Identity, len(options.Identities))
	copy(identities, options.Identities)
	return &Service{
		registry:   registry,
		store:      store,
		options:    options,
		identities: identities,
		limiter:    newIdentityRateLimiter(options.RateLimitBurst, options.RateLimitWindow),
		logger:     util.ComponentLogger(logger, util.CompControl),
	}
}

func (s *Service) SetFlowController(controller FlowController) {
	if s != nil {
		s.controller = controller
	}
}

func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(s.HandleResolve)
}

type flowResponse struct {
	FlowID       string `json:"flow_id"`
	Protocol     string `json:"protocol"`
	ClientAddr   string `json:"client_addr"`
	Listener     string `json:"listener"`
	Route        string `json:"route"`
	Upstream     string `json:"upstream"`
	BackendKey   string `json:"backend_key"`
	CreatedAt    int64  `json:"created_at"`
	EndedAt      int64  `json:"ended_at"`
	ResolveUntil int64  `json:"resolve_until"`
	State        string `json:"state"`
}

type resolveResponse struct {
	Ok   bool          `json:"ok"`
	Flow *flowResponse `json:"flow,omitempty"`
}

type errorResponse struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
}

type httpRPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type httpRPCResponse struct {
	Ok     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s *Service) HandleResolve(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	identity, ok := s.authenticate(r)
	if !ok {
		writeFlowError(w, http.StatusUnauthorized, ErrUnauthorized.Error())
		s.auditRequest(r, "", http.StatusUnauthorized, ErrUnauthorized.Error(), started)
		return
	}
	if !s.limiter.Allow(identity.ID) {
		writeFlowError(w, http.StatusTooManyRequests, ErrRateLimited.Error())
		s.auditRequest(r, identity.ID, http.StatusTooManyRequests, ErrRateLimited.Error(), started)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeFlowError(w, http.StatusMethodNotAllowed, "method not allowed")
		s.auditRequest(r, identity.ID, http.StatusMethodNotAllowed, "method not allowed", started)
		return
	}
	var request ResolveFlowRequest
	if err := decodeBody(w, r, &request, s.options.MaxBodyBytes); err != nil {
		status := flowErrorStatus(err)
		writeFlowError(w, status, err.Error())
		s.auditRequest(r, identity.ID, status, err.Error(), started)
		return
	}
	result, err := s.ResolveFlow(r.Context(), request, identity)
	if err != nil {
		status := flowErrorStatus(err)
		writeFlowError(w, status, flowErrorMessage(err))
		s.auditRequest(r, identity.ID, status, flowErrorMessage(err), started)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resolveResponse{Ok: true, Flow: newFlowResponse(result)})
	s.auditRequest(r, identity.ID, http.StatusOK, "", started)
}

func (s *Service) HandleRPC(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	identity, ok := s.authenticate(r)
	if !ok {
		writeFlowError(w, http.StatusUnauthorized, ErrUnauthorized.Error())
		s.auditRequest(r, "", http.StatusUnauthorized, ErrUnauthorized.Error(), started)
		return
	}
	if !s.limiter.Allow(identity.ID) {
		writeFlowError(w, http.StatusTooManyRequests, ErrRateLimited.Error())
		s.auditRequest(r, identity.ID, http.StatusTooManyRequests, ErrRateLimited.Error(), started)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeFlowError(w, http.StatusMethodNotAllowed, "method not allowed")
		s.auditRequest(r, identity.ID, http.StatusMethodNotAllowed, "method not allowed", started)
		return
	}
	var request httpRPCRequest
	if err := decodeBody(w, r, &request, s.options.MaxBodyBytes); err != nil {
		status := flowErrorStatus(err)
		writeFlowError(w, status, err.Error())
		s.auditRequest(r, identity.ID, status, err.Error(), started)
		return
	}
	method := strings.TrimSpace(request.Method)
	rpcCtx := context.WithValue(r.Context(), rpcAuditMetaKey{}, rpcAuditMeta{
		RequestID: r.Header.Get("X-Request-ID"),
		Method:    method,
	})
	result, err := s.dispatch(rpcCtx, method, request.Params, identity)
	if err != nil {
		status := flowErrorStatus(err)
		message := flowErrorMessage(err)
		writeFlowError(w, status, message)
		s.auditRequestMethod(r, identity.ID, status, message, started, method)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(httpRPCResponse{Ok: true, Result: result})
	s.auditRequestMethod(r, identity.ID, http.StatusOK, "", started, method)
}

func (s *Service) authenticate(r *http.Request) (Identity, bool) {
	token, ok := bearerToken(r)
	if !ok {
		return Identity{}, false
	}
	for _, identity := range s.identities {
		if identity.Token != "" && len(token) == len(identity.Token) && subtle.ConstantTimeCompare([]byte(token), []byte(identity.Token)) == 1 {
			return identity, true
		}
	}
	return Identity{}, false
}

func (s *Service) dispatch(ctx context.Context, method string, raw json.RawMessage, identity Identity) (any, error) {
	switch method {
	case "Ping":
		params := strings.TrimSpace(string(raw))
		if params != "" && params != "null" && params != "{}" {
			return nil, ErrInvalidParams
		}
		return map[string]any{"pong": true}, nil
	case "ResolveFlow":
		var request ResolveFlowRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.ResolveFlow(ctx, request, identity)
		if err != nil {
			return nil, err
		}
		return map[string]any{"flow": newFlowResponse(result)}, nil
	case "SetFlowTag":
		var request TagRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.SetFlowTag(ctx, request, identity)
		return map[string]any{"tag": result}, err
	case "UnsetFlowTag":
		var request TagRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		err := s.UnsetFlowTag(ctx, request, identity)
		return map[string]any{"removed": err == nil}, err
	case "SetClientTag":
		var request TagRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.SetClientTag(ctx, request, identity)
		return map[string]any{"tag": result}, err
	case "UnsetClientTag":
		var request TagRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		err := s.UnsetClientTag(ctx, request, identity)
		return map[string]any{"removed": err == nil}, err
	case "ListFlowTags":
		var request ListFlowTagsRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.ListFlowTags(ctx, request, identity)
		return map[string]any{"tags": result}, err
	case "SetFlowLimit":
		var request FlowLimitRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.SetFlowLimit(ctx, request, identity)
		return result, err
	case "ClearFlowLimit":
		var request FlowIDRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.ClearFlowLimit(ctx, request, identity)
		return result, err
	case "BlockFlow":
		var request FlowActionRequest
		if err := decodeParams(raw, &request); err != nil {
			return nil, err
		}
		result, err := s.BlockFlow(ctx, request, identity)
		return result, err
	default:
		return nil, ErrUnknownMethod
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	if r.ContentLength > limit {
		return errors.New("request body too large")
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errors.New("request body too large")
		}
		return errors.New("invalid JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid JSON")
	}
	return nil
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return ErrInvalidParams
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return ErrInvalidParams
	}
	return nil
}

func flowErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrInvalidTag), errors.Is(err, ErrInvalidTuple), errors.Is(err, ErrInvalidParams):
		return http.StatusBadRequest
	case errors.Is(err, ErrUnknownMethod):
		return http.StatusBadRequest
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrFlowNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrFlowNotActive):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidRate):
		return http.StatusBadRequest
	case errors.Is(err, ErrFlowController):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrClosed), errors.Is(err, ErrTagStore):
		return http.StatusServiceUnavailable
	default:
		if err != nil && err.Error() == "request body too large" {
			return http.StatusRequestEntityTooLarge
		}
		if err != nil && err.Error() == "invalid JSON" {
			return http.StatusBadRequest
		}
		return http.StatusInternalServerError
	}
}

func flowErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrInvalidParams) {
		return "invalid params"
	}
	if errors.Is(err, ErrInvalidTuple) {
		return "invalid backend tuple"
	}
	if errors.Is(err, ErrFlowNotFound) {
		return "flow context not found"
	}
	return err.Error()
}

func newFlowResponse(value Context) *flowResponse {
	return &flowResponse{
		FlowID: value.FlowID.String(), Protocol: value.Protocol, ClientAddr: value.ClientAddr,
		Listener: value.Listener, Route: value.Route, Upstream: value.Upstream,
		BackendKey: value.BackendKey, CreatedAt: unixMilli(value.CreatedAt),
		EndedAt: unixMilli(value.EndedAt), ResolveUntil: unixMilli(value.ResolveUntil), State: value.State,
	}
}

func unixMilli(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func writeFlowError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Ok: false, Error: message})
}

func (s *Service) auditRequest(r *http.Request, identity string, status int, message string, started time.Time) {
	s.auditRequestMethod(r, identity, status, message, started, "")
}

func (s *Service) auditRequestMethod(r *http.Request, identity string, status int, message string, started time.Time, rpcMethod string) {
	if s.logger == nil {
		return
	}
	result := "success"
	if status >= 400 && status < 500 {
		result = "denied"
	} else if status >= 500 {
		result = "failed"
	}
	attrs := []interface{}{"request.id", r.Header.Get("X-Request-ID"), "request.method", r.Method, "request.path", r.URL.Path, "client.addr", r.RemoteAddr, "auth.identity", identity, "http.status_code", status, "result", result, "latency_ms", time.Since(started).Milliseconds()}
	if rpcMethod != "" {
		attrs = append(attrs, "rpc.method", rpcMethod)
	}
	if message != "" {
		attrs = append(attrs, "error", message)
	}
	util.Event(s.logger, slog.LevelInfo, "flow_context.request_completed", attrs...)
}

type identityRateLimiter struct {
	mu      sync.Mutex
	entries map[string]identityRateEntry
	burst   int
	window  time.Duration
}

type identityRateEntry struct {
	started time.Time
	count   int
}

func newIdentityRateLimiter(burst int, window time.Duration) *identityRateLimiter {
	return &identityRateLimiter{entries: make(map[string]identityRateEntry), burst: burst, window: window}
}

func (l *identityRateLimiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
		l.entries[key] = identityRateEntry{started: now, count: 1}
		return true
	}
	if entry.count >= l.burst {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}

var _ interface {
	Open(flow.Meta)
	Update(flow.ID, flow.Counters)
	Close(flow.Summary)
	Reject(flow.Rejection)
} = (*Registry)(nil)
