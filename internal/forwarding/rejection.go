package forwarding

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/NodePath81/fbforward/internal/flow"
)

func emitRejection(observer FlowObserver, protocol, listener, clientAddress, reason string, decision Decision) {
	if observer == nil || clientAddress == "" {
		return
	}
	clientAddr := parseClientAddr(clientAddress)
	if !clientAddr.IsValid() {
		return
	}
	observer.Reject(flow.Rejection{
		Protocol:         protocol,
		ClientAddr:       clientAddr,
		Listener:         listener,
		Reason:           reason,
		MatchedRuleType:  decision.RuleType,
		MatchedRuleValue: decision.RuleValue,
		RecordedAt:       time.Now().UTC(),
	})
}

func newCandidateMeta(protocol, clientAddress, listener, route string) (flow.Meta, error) {
	addr := parseClientAddr(clientAddress)
	if !addr.IsValid() {
		return flow.Meta{}, fmt.Errorf("invalid client address %q", clientAddress)
	}
	return flow.Meta{
		Protocol:   protocol,
		ClientAddr: addr,
		Listener:   listener,
		Route:      route,
		StartedAt:  time.Now().UTC(),
	}, nil
}

func parseClientAddr(raw string) netip.AddrPort {
	if addr, err := netip.ParseAddrPort(raw); err == nil {
		return addr
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		return netip.AddrPortFrom(addr, 0)
	}
	return netip.AddrPort{}
}
