package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/policy"
	"github.com/NodePath81/fbforward/internal/util"
)

type createOnlineRuleParams struct {
	RuleID     string          `json:"rule_id,omitempty"`
	Action     string          `json:"action"`
	Matcher    json.RawMessage `json:"matcher"`
	Priority   int             `json:"priority"`
	LimitBPS   uint64          `json:"limit_bps,omitempty"`
	Upstream   string          `json:"upstream,omitempty"`
	TTLSeconds int64           `json:"ttl_seconds"`
	Reason     string          `json:"reason"`
	TicketRef  string          `json:"ticket_ref,omitempty"`
}

type onlineRuleIDParams struct {
	RuleID string `json:"rule_id"`
}

type listOnlineRulesParams struct {
	IncludeExpired bool `json:"include_expired"`
}

type onlineRuleResponse struct {
	RuleID      string               `json:"rule_id"`
	Action      string               `json:"action"`
	Matcher     policy.OnlineMatcher `json:"matcher"`
	Priority    int                  `json:"priority"`
	LimitBPS    uint64               `json:"limit_bps,omitempty"`
	Upstream    string               `json:"upstream,omitempty"`
	CreatedBy   string               `json:"created_by"`
	Reason      string               `json:"reason"`
	TicketRef   string               `json:"ticket_ref,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	ExpiresAt   *time.Time           `json:"expires_at,omitempty"`
	State       string               `json:"state"`
	StateReason string               `json:"state_reason,omitempty"`
}

func (c *ControlServer) rpcCreateOnlineRule(ctx *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	var params createOnlineRuleParams
	if fault := decodeRequiredOnlineParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	provider := c.onlinePolicyProvider()
	if provider == nil {
		return rpcError(http.StatusServiceUnavailable, "online rule store not available")
	}
	params.Upstream = strings.TrimSpace(params.Upstream)
	var matcher policy.OnlineMatcher
	if fault := decodeStrictOnlineMatcher(params.Matcher, &matcher); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	if strings.EqualFold(params.Action, "route_override") {
		if c.manager == nil {
			return rpcError(http.StatusServiceUnavailable, "upstream manager not available")
		}
		if c.manager.Get(params.Upstream) == nil {
			return rpcError(http.StatusBadRequest, "upstream not found")
		}
	}
	createdBy := "control"
	if ctx.Meta.clientIP != "" {
		createdBy += ":" + ctx.Meta.clientIP
	}
	spec := policy.OnlineRuleSpec{
		RuleID: params.RuleID, Action: params.Action, Matcher: matcher,
		Params:   policy.OnlineParams{LimitBPS: params.LimitBPS, Upstream: params.Upstream},
		Priority: params.Priority, TTL: time.Duration(params.TTLSeconds) * time.Second,
		Reason: params.Reason, TicketRef: params.TicketRef, CreatedBy: createdBy,
	}
	rule, err := policy.BuildOnlineRule(spec, time.Now().UTC())
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	event := audit.OnlineRuleEvent{RuleID: rule.RuleID, Operation: "create", Actor: createdBy, Reason: rule.Reason, TicketRef: rule.TicketRef}
	if err := provider.Create(rule, event); err != nil {
		status := onlineRuleErrorStatus(err)
		util.Event(c.logger, slogLevelWarn(), "online_rule.create", "request.id", ctx.Meta.id, "rule.id", rule.RuleID, "result", "failed", "error", err)
		return rpcError(status, err.Error())
	}
	util.Event(c.logger, slogLevelInfo(), "online_rule.create", "request.id", ctx.Meta.id, "rule.id", rule.RuleID, "result", "success")
	state, reason := provider.RuleState(rule, time.Now().UTC())
	return rpcOK(toOnlineRuleResponse(rule, state, reason))
}

func decodeStrictOnlineMatcher(raw json.RawMessage, target *policy.OnlineMatcher) *rpcFault {
	if len(raw) == 0 || string(raw) == "null" {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	return nil
}

func decodeRequiredOnlineParams(raw json.RawMessage, target any) *rpcFault {
	if len(raw) == 0 || string(raw) == "null" {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	return nil
}

func (c *ControlServer) rpcListOnlineRules(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	var params listOnlineRulesParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	provider := c.onlinePolicyProvider()
	if provider == nil {
		return rpcError(http.StatusServiceUnavailable, "online rule store not available")
	}
	rules, err := provider.List(time.Now().UTC(), params.IncludeExpired)
	if err != nil {
		return rpcError(onlineRuleErrorStatus(err), err.Error())
	}
	result := make([]onlineRuleResponse, 0, len(rules))
	for _, rule := range rules {
		state, reason := provider.RuleState(rule, time.Now().UTC())
		result = append(result, toOnlineRuleResponse(rule, state, reason))
	}
	return rpcOK(result)
}

func (c *ControlServer) rpcDeleteOnlineRule(ctx *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	var params onlineRuleIDParams
	if fault := decodeRequiredParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	provider := c.onlinePolicyProvider()
	if provider == nil {
		return rpcError(http.StatusServiceUnavailable, "online rule store not available")
	}
	actor := "control"
	if ctx.Meta.clientIP != "" {
		actor += ":" + ctx.Meta.clientIP
	}
	err := provider.Delete(strings.TrimSpace(params.RuleID), audit.OnlineRuleEvent{Operation: "delete", Actor: actor})
	if err != nil {
		return rpcError(onlineRuleErrorStatus(err), err.Error())
	}
	util.Event(c.logger, slogLevelInfo(), "online_rule.delete", "request.id", ctx.Meta.id, "rule.id", params.RuleID, "result", "success")
	return rpcOK(nil)
}

func (c *ControlServer) rpcExpireOnlineRule(ctx *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	var params onlineRuleIDParams
	if fault := decodeRequiredParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	provider := c.onlinePolicyProvider()
	if provider == nil {
		return rpcError(http.StatusServiceUnavailable, "online rule store not available")
	}
	actor := "control"
	if ctx.Meta.clientIP != "" {
		actor += ":" + ctx.Meta.clientIP
	}
	err := provider.Expire(strings.TrimSpace(params.RuleID), time.Now().UTC(), audit.OnlineRuleEvent{Operation: "expire", Actor: actor})
	if err != nil {
		return rpcError(onlineRuleErrorStatus(err), err.Error())
	}
	util.Event(c.logger, slogLevelInfo(), "online_rule.expire", "request.id", ctx.Meta.id, "rule.id", params.RuleID, "result", "success")
	return rpcOK(nil)
}

func toOnlineRuleResponse(rule audit.OnlineRule, state string, reasons ...string) onlineRuleResponse {
	matcher := policy.OnlineMatcher{Protocol: rule.Protocol, Port: rule.Port}
	if rule.MatcherJSON != "" {
		_ = json.Unmarshal([]byte(rule.MatcherJSON), &matcher)
	} else if rule.RuleType == "cidr" || rule.RuleType == "source_cidr" {
		matcher.SourceCIDR = rule.RuleValue
	} else if rule.RuleType == "ip" || rule.RuleType == "source_ip" {
		matcher.SourceIP = rule.RuleValue
	}
	params := policy.OnlineParams{}
	if raw := rule.ParamsJSON; raw != "" {
		_ = json.Unmarshal([]byte(raw), &params)
	} else if rule.PayloadJSON != "" {
		_ = json.Unmarshal([]byte(rule.PayloadJSON), &params)
	}
	stateReason := ""
	if len(reasons) > 0 {
		stateReason = reasons[0]
	}
	return onlineRuleResponse{
		RuleID: rule.RuleID, Action: rule.Action, Matcher: matcher, Priority: rule.Priority,
		LimitBPS: params.LimitBPS, Upstream: params.Upstream, CreatedBy: rule.CreatedBy,
		Reason: rule.Reason, TicketRef: rule.TicketRef, CreatedAt: rule.CreatedAt,
		UpdatedAt: rule.UpdatedAt, ExpiresAt: rule.ExpiresAt, State: state, StateReason: stateReason,
	}
}

func onlineRuleState(rule audit.OnlineRule, now time.Time) string {
	if !rule.Enabled {
		return "disabled"
	}
	if rule.ExpiresAt != nil && !rule.ExpiresAt.After(now) {
		return "expired"
	}
	return "active"
}

func onlineRuleErrorStatus(err error) int {
	switch {
	case errors.Is(err, policy.ErrOnlineStoreUnavailable), errors.Is(err, audit.ErrOnlineRuleNotFound):
		if errors.Is(err, audit.ErrOnlineRuleNotFound) {
			return http.StatusNotFound
		}
		return http.StatusServiceUnavailable
	case errors.Is(err, audit.ErrOnlineRuleExists):
		return http.StatusConflict
	case errors.Is(err, policy.ErrOnlineRuleCapacity):
		return http.StatusServiceUnavailable
	case errors.Is(err, policy.ErrOnlineRuleInvalid):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
