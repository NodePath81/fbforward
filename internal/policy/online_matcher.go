package policy

import (
	"encoding/json"
	"fmt"
	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/flow"
	"net/netip"
	"sort"
	"strings"
)

type runtimeOnlineRule struct {
	Stored            audit.OnlineRule
	Matcher           OnlineMatcher
	Params            OnlineParams
	SourceCIDR        *netip.Prefix
	SourceIP          *netip.Addr
	Available         bool
	UnavailableReason string
}

type onlineSnapshot struct {
	rules []runtimeOnlineRule
}

func compileStoredRules(stored []audit.OnlineRule, upstreamAvailable func(string) bool) ([]runtimeOnlineRule, error) {
	result := make([]runtimeOnlineRule, 0, len(stored))
	for _, rule := range stored {
		compiled, err := compileStoredRule(rule, upstreamAvailable)
		if err != nil {
			return nil, err
		}
		result = append(result, compiled)
	}
	sortRuntimeRules(result)
	return result, nil
}

func compileStoredRule(rule audit.OnlineRule, upstreamAvailable func(string) bool) (runtimeOnlineRule, error) {
	matcher := OnlineMatcher{Protocol: strings.ToLower(strings.TrimSpace(rule.Protocol)), Port: rule.Port}
	if rule.MatcherJSON != "" {
		if err := json.Unmarshal([]byte(rule.MatcherJSON), &matcher); err != nil {
			return runtimeOnlineRule{}, fmt.Errorf("decode online rule matcher %q: %w", rule.RuleID, err)
		}
	} else {
		switch rule.RuleType {
		case "cidr", "source_cidr":
			matcher.SourceCIDR = rule.RuleValue
		case "ip", "source_ip":
			matcher.SourceIP = rule.RuleValue
		}
	}
	params := OnlineParams{}
	paramsJSON := rule.ParamsJSON
	if paramsJSON == "" {
		paramsJSON = rule.PayloadJSON
	}
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			return runtimeOnlineRule{}, fmt.Errorf("decode online rule params %q: %w", rule.RuleID, err)
		}
	}
	normalized := normalizeMatcher(matcher)
	compiled := runtimeOnlineRule{Stored: rule, Matcher: normalized, Params: params, Available: true}
	if reason := unavailableReason(rule, upstreamAvailable); reason != "" {
		compiled.Available = false
		compiled.UnavailableReason = reason
	}
	if normalized.SourceCIDR != "" {
		prefix, err := netip.ParsePrefix(normalized.SourceCIDR)
		if err != nil {
			return runtimeOnlineRule{}, err
		}
		prefix = prefix.Masked()
		compiled.SourceCIDR = &prefix
	}
	if normalized.SourceIP != "" {
		addr, err := netip.ParseAddr(normalized.SourceIP)
		if err != nil {
			return runtimeOnlineRule{}, err
		}
		addr = addr.Unmap()
		compiled.SourceIP = &addr
	}
	return compiled, nil
}

func normalizeMatcher(matcher OnlineMatcher) OnlineMatcher {
	matcher.SourceCIDR = strings.TrimSpace(matcher.SourceCIDR)
	if matcher.SourceCIDR != "" {
		if prefix, err := netip.ParsePrefix(matcher.SourceCIDR); err == nil {
			matcher.SourceCIDR = prefix.Masked().String()
		}
	}
	matcher.SourceIP = strings.TrimSpace(matcher.SourceIP)
	if matcher.SourceIP != "" {
		if addr, err := netip.ParseAddr(matcher.SourceIP); err == nil {
			matcher.SourceIP = addr.Unmap().String()
		}
	}
	matcher.Protocol = strings.ToLower(strings.TrimSpace(matcher.Protocol))
	return matcher
}

func legacyMatcherFields(matcher OnlineMatcher) (string, string) {
	if matcher.SourceCIDR != "" {
		return "source_cidr", matcher.SourceCIDR
	}
	if matcher.SourceIP != "" {
		return "source_ip", matcher.SourceIP
	}
	if matcher.Protocol != "" {
		return "protocol", matcher.Protocol
	}
	if matcher.Port != nil {
		return "port", fmt.Sprintf("%d", *matcher.Port)
	}
	return "", ""
}

func sortRuntimeRules(rules []runtimeOnlineRule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Stored.Priority != rules[j].Stored.Priority {
			return rules[i].Stored.Priority > rules[j].Stored.Priority
		}
		if !rules[i].Stored.CreatedAt.Equal(rules[j].Stored.CreatedAt) {
			return rules[i].Stored.CreatedAt.Before(rules[j].Stored.CreatedAt)
		}
		return rules[i].Stored.RuleID < rules[j].Stored.RuleID
	})
}

func matchesOnlineRule(rule runtimeOnlineRule, meta flow.Meta) bool {
	if !rule.Available {
		return false
	}
	if rule.Matcher.Protocol != "" && rule.Matcher.Protocol != meta.Protocol {
		return false
	}
	if rule.Matcher.Port != nil {
		port := listenerPort(meta.Listener)
		if port == 0 || port != *rule.Matcher.Port {
			return false
		}
	}
	addr := meta.ClientAddr.Addr().Unmap()
	if rule.SourceCIDR != nil && !rule.SourceCIDR.Contains(addr) {
		return false
	}
	if rule.SourceIP != nil && *rule.SourceIP != addr {
		return false
	}
	return true
}

func unavailableReason(rule audit.OnlineRule, upstreamAvailable func(string) bool) string {
	if rule.Action != "route_override" || upstreamAvailable == nil {
		return ""
	}
	params := OnlineParams{}
	raw := rule.ParamsJSON
	if raw == "" {
		raw = rule.PayloadJSON
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			return "invalid route override parameters"
		}
	}
	if params.Upstream == "" || !upstreamAvailable(params.Upstream) {
		return "upstream is not configured"
	}
	return ""
}

func listenerPort(listener string) int {
	if index := strings.LastIndex(listener, ":"); index >= 0 {
		var port int
		_, _ = fmt.Sscanf(listener[index+1:], "%d", &port)
		return port
	}
	return 0
}

func evaluationFromRule(rule runtimeOnlineRule, allowed bool) OnlineEvaluation {
	return OnlineEvaluation{Matched: true, Allowed: allowed, RuleID: rule.Stored.RuleID, RuleType: rule.Stored.RuleType, RuleValue: rule.Stored.RuleValue, Action: rule.Stored.Action, RateLimitBPS: rule.Params.LimitBPS, UpstreamOverride: rule.Params.Upstream}
}
