package forwarding

import (
	"net/netip"
	"time"

	"github.com/NodePath81/fbforward/internal/firewall"
	"github.com/NodePath81/fbforward/internal/flow"
)

func emitRejection(observer flow.Observer, protocol, listener, clientAddress, reason string, decision firewall.Decision) {
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

func parseClientAddr(raw string) netip.AddrPort {
	if addr, err := netip.ParseAddrPort(raw); err == nil {
		return addr
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		return netip.AddrPortFrom(addr, 0)
	}
	return netip.AddrPort{}
}
