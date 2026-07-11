package policy

import (
	"encoding/json"
	"fmt"
	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/google/uuid"
	"net/netip"
	"strings"
	"time"
)

func BuildOnlineRule(spec OnlineRuleSpec, now time.Time) (audit.OnlineRule, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := ValidateOnlineRuleSpec(spec); err != nil {
		return audit.OnlineRule{}, err
	}
	ruleID := strings.TrimSpace(spec.RuleID)
	if ruleID == "" {
		ruleID = "online-" + uuid.NewString()
	}
	matcher := normalizeMatcher(spec.Matcher)
	paramsJSON, err := json.Marshal(spec.Params)
	if err != nil {
		return audit.OnlineRule{}, err
	}
	matcherJSON, err := json.Marshal(matcher)
	if err != nil {
		return audit.OnlineRule{}, err
	}
	if len(matcherJSON) > MaxOnlineMatcherJSON {
		return audit.OnlineRule{}, fmt.Errorf("%w: matcher is too large", ErrOnlineRuleInvalid)
	}
	if len(paramsJSON) > MaxOnlineParamsJSON {
		return audit.OnlineRule{}, fmt.Errorf("%w: params are too large", ErrOnlineRuleInvalid)
	}
	ruleType, ruleValue := legacyMatcherFields(matcher)
	source := spec.Source
	if source == "" {
		source = "online"
	}
	expires := now.Add(spec.TTL)
	return audit.OnlineRule{
		RuleID: ruleID, Version: OnlineRuleVersion, Action: strings.ToLower(strings.TrimSpace(spec.Action)),
		RuleType: ruleType, RuleValue: ruleValue, Protocol: matcher.Protocol, Port: matcher.Port,
		Priority: spec.Priority, Enabled: true, ExpiresAt: &expires, Source: source,
		CreatedBy: spec.CreatedBy, Reason: strings.TrimSpace(spec.Reason), TicketRef: strings.TrimSpace(spec.TicketRef),
		MatcherJSON: string(matcherJSON), ParamsJSON: string(paramsJSON), PayloadJSON: string(paramsJSON),
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func ValidateOnlineRuleSpec(spec OnlineRuleSpec) error {
	if err := validateOnlineText("rule_id", spec.RuleID, MaxOnlineRuleID); err != nil {
		return err
	}
	if err := validateOnlineText("reason", spec.Reason, MaxOnlineReason); err != nil {
		return err
	}
	if err := validateOnlineText("ticket_ref", spec.TicketRef, MaxOnlineTicket); err != nil {
		return err
	}
	if err := validateOnlineText("upstream", spec.Params.Upstream, MaxOnlineUpstream); err != nil {
		return err
	}
	if spec.Priority < MinOnlinePriority || spec.Priority > MaxOnlinePriority {
		return fmt.Errorf("%w: priority must be between %d and %d", ErrOnlineRuleInvalid, MinOnlinePriority, MaxOnlinePriority)
	}
	action := strings.ToLower(strings.TrimSpace(spec.Action))
	if action != "deny" && action != "rate_limit" && action != "route_override" {
		return fmt.Errorf("%w: action must be deny, rate_limit, or route_override", ErrOnlineRuleInvalid)
	}
	if spec.TTL <= 0 || spec.TTL > MaxOnlineRuleTTL {
		return fmt.Errorf("%w: ttl must be between 1s and 24h", ErrOnlineRuleInvalid)
	}
	matcher := normalizeMatcher(spec.Matcher)
	if matcher.SourceCIDR == "" && matcher.SourceIP == "" && matcher.Protocol == "" && matcher.Port == nil {
		return fmt.Errorf("%w: matcher must not be empty", ErrOnlineRuleInvalid)
	}
	if matcher.SourceCIDR != "" && matcher.SourceIP != "" {
		return fmt.Errorf("%w: source_ip and source_cidr are mutually exclusive", ErrOnlineRuleInvalid)
	}
	if matcher.SourceCIDR != "" {
		if _, err := netip.ParsePrefix(matcher.SourceCIDR); err != nil {
			return fmt.Errorf("%w: invalid source_cidr", ErrOnlineRuleInvalid)
		}
	}
	if matcher.SourceIP != "" {
		if _, err := netip.ParseAddr(matcher.SourceIP); err != nil {
			return fmt.Errorf("%w: invalid source_ip", ErrOnlineRuleInvalid)
		}
	}
	if matcher.Protocol != "" && matcher.Protocol != "tcp" && matcher.Protocol != "udp" {
		return fmt.Errorf("%w: protocol must be tcp or udp", ErrOnlineRuleInvalid)
	}
	if matcher.Port != nil && (*matcher.Port < 1 || *matcher.Port > 65535) {
		return fmt.Errorf("%w: port must be between 1 and 65535", ErrOnlineRuleInvalid)
	}
	switch action {
	case "deny":
		if spec.Params.LimitBPS != 0 || spec.Params.Upstream != "" {
			return fmt.Errorf("%w: deny does not accept action parameters", ErrOnlineRuleInvalid)
		}
	case "rate_limit":
		if spec.Params.LimitBPS == 0 || spec.Params.Upstream != "" {
			return fmt.Errorf("%w: rate_limit requires limit_bps", ErrOnlineRuleInvalid)
		}
	case "route_override":
		if strings.TrimSpace(spec.Params.Upstream) == "" || spec.Params.LimitBPS != 0 {
			return fmt.Errorf("%w: route_override requires upstream", ErrOnlineRuleInvalid)
		}
	}
	return nil
}

func validateOnlineText(field, value string, max int) error {
	if len([]rune(value)) > max {
		return fmt.Errorf("%w: %s must be at most %d characters", ErrOnlineRuleInvalid, field, max)
	}
	return nil
}
