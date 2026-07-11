package app

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/forwarding"
	"github.com/NodePath81/fbforward/internal/policy"
	"github.com/NodePath81/fbforward/internal/upstream"
)

type upstreamPicker struct {
	manager *upstream.UpstreamManager
}

func (p *upstreamPicker) Pick(_ flow.Meta) (forwarding.Upstream, error) {
	if p == nil || p.manager == nil {
		return forwarding.Upstream{}, fmt.Errorf("upstream picker is unavailable")
	}
	selected, err := p.manager.SelectUpstream()
	if err != nil {
		return forwarding.Upstream{}, err
	}
	if selected == nil {
		return forwarding.Upstream{}, fmt.Errorf("upstream picker returned nil upstream")
	}
	ip := selected.ActiveIP()
	if ip == nil {
		return forwarding.Upstream{}, fmt.Errorf("upstream %q has no active IP", selected.Tag)
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return forwarding.Upstream{}, fmt.Errorf("upstream %q has invalid active IP %q", selected.Tag, ip.String())
	}
	addr = addr.Unmap()
	return forwarding.Upstream{Tag: selected.Tag, Addr: addr}, nil
}

func (p *upstreamPicker) PickOverride(_ flow.Meta, tag string) (forwarding.Upstream, error) {
	if p == nil || p.manager == nil {
		return forwarding.Upstream{}, fmt.Errorf("upstream picker is unavailable")
	}
	selected := p.manager.Get(tag)
	if selected == nil {
		return forwarding.Upstream{}, fmt.Errorf("upstream %q not found", tag)
	}
	ip := selected.ActiveIP()
	if ip == nil {
		return forwarding.Upstream{}, fmt.Errorf("upstream %q has no active IP", tag)
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return forwarding.Upstream{}, fmt.Errorf("upstream %q has invalid active IP %q", tag, ip.String())
	}
	return forwarding.Upstream{Tag: selected.Tag, Addr: addr.Unmap()}, nil
}

func (p *upstreamPicker) MarkDialFailure(selected forwarding.Upstream, cooldown time.Duration) {
	if p != nil && p.manager != nil {
		p.manager.MarkDialFailure(selected.Tag, cooldown)
	}
}

func (p *upstreamPicker) ClearDialFailure(selected forwarding.Upstream) {
	if p != nil && p.manager != nil {
		p.manager.ClearDialFailure(selected.Tag)
	}
}

type firewallPolicy struct {
	provider       *policy.Provider
	onlineProvider *policy.OnlineProvider
}

func (p *firewallPolicy) Decide(meta flow.Meta) forwarding.Decision {
	if p == nil || !meta.ClientAddr.IsValid() {
		return forwarding.Decision{Allowed: true}
	}
	if p.onlineProvider != nil {
		if online := p.onlineProvider.DecideDeny(meta); online.Matched {
			return onlineDecision(online)
		}
	}
	persistent := forwarding.Decision{Allowed: true}
	if p.provider != nil {
		decision := p.provider.Decide(meta.ClientAddr.Addr().AsSlice())
		persistent = forwarding.Decision{
			Allowed:   decision.Allowed,
			RuleType:  decision.RuleType,
			RuleValue: decision.RuleValue,
		}
	}
	if !persistent.Allowed || p.onlineProvider == nil {
		return persistent
	}
	if online := p.onlineProvider.DecideAction(meta); online.Matched {
		return onlineDecision(online)
	}
	return persistent
}

func onlineDecision(online policy.OnlineEvaluation) forwarding.Decision {
	return forwarding.Decision{
		Allowed:          online.Allowed,
		RuleType:         online.RuleType,
		RuleValue:        online.RuleValue,
		RuleID:           online.RuleID,
		Action:           online.Action,
		RateLimitBPS:     online.RateLimitBPS,
		UpstreamOverride: online.UpstreamOverride,
	}
}
