package forwarding

import (
	"time"

	"github.com/NodePath81/fbforward/internal/firewall"
	"github.com/NodePath81/fbforward/internal/iplog"
)

func emitRejection(pipeline *iplog.Pipeline, protocol string, port int, ip, reason string, decision firewall.Decision) {
	if pipeline == nil || ip == "" {
		return
	}
	pipeline.EmitRejection(iplog.RejectionEvent{
		IP:               ip,
		Protocol:         protocol,
		Port:             port,
		Reason:           reason,
		MatchedRuleType:  decision.RuleType,
		MatchedRuleValue: decision.RuleValue,
		RecordedAt:       time.Now(),
	})
}
