package policy

import (
	"net"

	"github.com/NodePath81/fbforward/internal/firewall"
)

// Decision is kept as an alias so forwarding and the legacy firewall package
// continue to exchange the same decision metadata.
type Decision = firewall.Decision

// Engine is immutable after construction. Its evaluator is the existing
// GeoIP-aware firewall evaluator, compiled from a validated policy document.
type Engine struct {
	evaluator *firewall.Engine
}

func (e *Engine) Decide(ip net.IP) firewall.Decision {
	if e == nil || e.evaluator == nil {
		return firewall.Decision{Allowed: true}
	}
	return e.evaluator.Decide(ip)
}

func (e *Engine) Check(ip net.IP) bool { return e.Decide(ip).Allowed }
