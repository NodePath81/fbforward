package flowcontext

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/NodePath81/fbforward/internal/util"
)

var (
	ErrFlowController = errors.New("flow controller unavailable")
	ErrFlowNotActive  = errors.New("flow context flow is not active")
	ErrInvalidRate    = errors.New("flow limit must be greater than zero")
)

type FlowIDRequest struct {
	FlowID string `json:"flow_id"`
}

type FlowLimitRequest struct {
	FlowID  string `json:"flow_id"`
	RateBPS uint64 `json:"rate_bps"`
}

type FlowActionRequest struct {
	FlowID string `json:"flow_id"`
	Reason string `json:"reason,omitempty"`
}

func (s *Service) SetFlowLimit(ctx context.Context, request FlowLimitRequest, identity Identity) (map[string]any, error) {
	if request.RateBPS == 0 {
		return nil, ErrInvalidRate
	}
	value, err := s.authorizedActiveFlow(request.FlowID, identity)
	if err != nil {
		return nil, err
	}
	if s.controller == nil {
		return nil, ErrFlowController
	}
	if err := s.ensureControlStore(); err != nil {
		return nil, err
	}
	if !s.controller.SetLimit(value.FlowID, request.RateBPS) {
		return nil, ErrFlowNotActive
	}
	s.auditControl("set_flow_limit", value, identity, request.RateBPS, "")
	return map[string]any{"flow_id": value.FlowID.String(), "rate_bps": request.RateBPS}, nil
}

func (s *Service) ClearFlowLimit(ctx context.Context, request FlowIDRequest, identity Identity) (map[string]any, error) {
	value, err := s.authorizedActiveFlow(request.FlowID, identity)
	if err != nil {
		return nil, err
	}
	if s.controller == nil {
		return nil, ErrFlowController
	}
	if err := s.ensureControlStore(); err != nil {
		return nil, err
	}
	if !s.controller.ClearLimit(value.FlowID) {
		return nil, ErrFlowNotActive
	}
	s.auditControl("clear_flow_limit", value, identity, 0, "")
	return map[string]any{"flow_id": value.FlowID.String(), "cleared": true}, nil
}

func (s *Service) BlockFlow(ctx context.Context, request FlowActionRequest, identity Identity) (map[string]any, error) {
	reason := strings.TrimSpace(request.Reason)
	if len(reason) > 256 || strings.ContainsAny(reason, "\r\n") {
		return nil, ErrInvalidParams
	}
	value, err := s.authorizedActiveFlow(request.FlowID, identity)
	if err != nil {
		return nil, err
	}
	if s.controller == nil {
		return nil, ErrFlowController
	}
	if err := s.ensureControlStore(); err != nil {
		return nil, err
	}
	if !s.controller.Block(value.FlowID) {
		return nil, ErrFlowNotActive
	}
	s.auditControl("block_flow", value, identity, 0, reason)
	return map[string]any{"flow_id": value.FlowID.String(), "blocked": true}, nil
}

func (s *Service) authorizedActiveFlow(flowID string, identity Identity) (Context, error) {
	value, err := s.authorizedFlow(context.Background(), flowID, identity)
	if err != nil {
		return Context{}, err
	}
	if value.State != StateActive {
		return Context{}, ErrFlowNotActive
	}
	return value, nil
}

func (s *Service) ensureControlStore() error {
	if s.store == nil {
		return ErrTagStore
	}
	return nil
}

func (s *Service) auditControl(event string, value Context, identity Identity, rateBPS uint64, reason string) {
	if s.logger == nil {
		return
	}
	attrs := []interface{}{
		"flow.id", value.FlowID,
		"flow.protocol", value.Protocol,
		"flow.route", value.Route,
		"flow.upstream", value.Upstream,
		"backend.identity", identity.ID,
		"result", "applied",
	}
	if rateBPS > 0 {
		attrs = append(attrs, "rate_bps", rateBPS)
	}
	if reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	util.Event(s.logger, slog.LevelInfo, event, attrs...)
}
