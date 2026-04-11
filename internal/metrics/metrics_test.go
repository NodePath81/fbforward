package metrics

import (
	"strings"
	"testing"

	"github.com/NodePath81/fbforward/internal/upstream"
)

func TestRenderIncludesIPLogAndFirewallMetrics(t *testing.T) {
	m := NewMetrics(nil)
	m.SetCoordinationState(upstream.CoordinationState{
		Connected:     true,
		Authoritative: true,
	})
	m.IncIPLogEvent()
	m.IncIPLogEventDropped()
	m.AddIPLogWrites(3)
	m.ObserveIPLogBatchSize(3)
	m.IncFirewallDenied("cidr", "10.0.0.0/8")

	rendered := m.Render()
	for _, needle := range []string{
		"fbforward_iplog_events_total 1",
		"fbforward_iplog_events_dropped_total 1",
		"fbforward_iplog_writes_total 3",
		"fbforward_coord_connected 1",
		"fbforward_coord_authoritative 1",
		`fbforward_firewall_denied_total{rule_type="cidr",rule_value="10.0.0.0/8"} 1`,
		"fbforward_iplog_batch_size_count 1",
		"fbforward_iplog_batch_size_sum 3",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected metrics output to contain %q\n%s", needle, rendered)
		}
	}
}
