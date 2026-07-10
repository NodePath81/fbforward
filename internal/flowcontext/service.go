package flowcontext

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

const maxResolveBodyBytes int64 = 64 << 10

// Service exposes the flow context resolver as an independent HTTP endpoint.
// It intentionally does not depend on ControlServer so it can be tested and
// embedded by other control planes.
type Service struct {
	registry *Registry
	token    string
}

func NewService(registry *Registry, token string) *Service {
	return &Service{registry: registry, token: token}
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

func (s *Service) HandleResolve(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.registry == nil {
		writeFlowError(w, http.StatusServiceUnavailable, "flow context registry unavailable")
		return
	}
	if !tokenMatches(r, s.token) {
		writeFlowError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeFlowError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.ContentLength > maxResolveBodyBytes {
		writeFlowError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxResolveBodyBytes)
	var request ResolveRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeFlowError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeFlowError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeFlowError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if request.WaitMS > int(s.registry.options.ResolveTimeout/time.Millisecond) {
		writeFlowError(w, http.StatusBadRequest, "wait_ms exceeds maximum")
		return
	}
	ctx := r.Context()
	if request.WaitMS > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, time.Duration(request.WaitMS)*time.Millisecond)
		defer cancel()
	}
	result, err := s.registry.ResolveRequest(ctx, request)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidTuple):
			writeFlowError(w, http.StatusBadRequest, "invalid backend tuple")
		case errors.Is(err, ErrClosed), errors.Is(err, ErrCapacityExceeded):
			writeFlowError(w, http.StatusServiceUnavailable, err.Error())
		default:
			writeFlowError(w, http.StatusNotFound, "flow context not found")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resolveResponse{Ok: true, Flow: newFlowResponse(result)})
}

func newFlowResponse(value Context) *flowResponse {
	return &flowResponse{
		FlowID:       value.FlowID.String(),
		Protocol:     value.Protocol,
		ClientAddr:   value.ClientAddr,
		Listener:     value.Listener,
		Route:        value.Route,
		Upstream:     value.Upstream,
		BackendKey:   value.BackendKey,
		CreatedAt:    unixMilli(value.CreatedAt),
		EndedAt:      unixMilli(value.EndedAt),
		ResolveUntil: unixMilli(value.ResolveUntil),
		State:        value.State,
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

var _ interface {
	Open(flow.Meta)
	Update(flow.ID, flow.Counters)
	Close(flow.Summary)
	Reject(flow.Rejection)
} = (*Registry)(nil)
