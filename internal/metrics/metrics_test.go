package metrics

import (
	"strings"
	"sync"
	"testing"
)

func TestRenderContract(t *testing.T) {
	m := NewMetrics([]string{"primary"})
	m.IncActive("tcp")
	m.RecordFlowEvent("tcp", "open", "")
	m.RecordFlowEvent("tcp", "reject", "client provided detail")
	m.AddTraffic("primary", "tcp", "up", 12)
	m.AddTraffic("primary", "tcp", "down", 8)
	m.SetRouteSelected("default", "primary")
	m.RecordProbe("primary", "tcp", true)
	m.RecordProbe("primary", "udp", false)
	m.AddAuditReceived(1)
	m.AddAuditWritten(1)
	m.AddAuditDropped(1)
	m.RecordRateLimitDrop("udp", 1200)
	m.SetOnlineRulesActive(2)
	m.IncOnlineRuleExpiryError()
	m.IncWebhookDelivery("success")
	m.IncWebhookDelivery("failed")
	m.IncWebhookDropped()
	m.IncFirewallDenied("cidr")

	rendered := m.Render()
	for _, needle := range []string{
		"fbforward_uptime_seconds ",
		`fbforward_flows_active{protocol="tcp"} 1`,
		`fbforward_flow_events_total{protocol="tcp",event="open",reason="none"} 1`,
		`fbforward_flow_events_total{protocol="tcp",event="reject",reason="other"} 1`,
		`fbforward_traffic_bytes_total{upstream="primary",protocol="tcp",direction="up"} 12`,
		`fbforward_route_selected_upstream{route="default",upstream="primary"} 1`,
		`fbforward_upstream_probes_total{upstream="primary",protocol="tcp",result="success"} 1`,
		`fbforward_audit_records_total{result="written"} 1`,
		`fbforward_udp_rate_limit_drops_total 1`,
		`fbforward_online_rules_active 2`,
		`fbforward_webhook_deliveries_total{result="dropped"} 1`,
		`fbforward_firewall_denied_total{rule_type="cidr"} 1`,
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected metrics output to contain %q\n%s", needle, rendered)
		}
	}
	for _, removed := range []string{
		"fbforward_bytes_up_total",
		"fbforward_bytes_up_per_second",
		"fbforward_mode",
		"fbforward_active_upstream",
		"fbforward_iplog_batch_size",
		"rule_value=",
	} {
		if strings.Contains(rendered, removed) {
			t.Fatalf("removed metric or label %q is still rendered\n%s", removed, rendered)
		}
	}
	types := make(map[string]bool)
	for _, line := range strings.Split(rendered, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 4 && fields[0] == "#" && fields[1] == "TYPE" {
			if types[fields[2]] {
				t.Fatalf("metric family %q has duplicate TYPE declaration", fields[2])
			}
			types[fields[2]] = true
		}
	}
	expectedFamilies := []string{
		"fbforward_uptime_seconds",
		"fbforward_flows_active",
		"fbforward_flow_events_total",
		"fbforward_route_selected_upstream",
		"fbforward_upstream_health_state",
		"fbforward_upstream_rtt_seconds",
		"fbforward_upstream_last_success_timestamp_seconds",
		"fbforward_upstream_probes_total",
		"fbforward_traffic_bytes_total",
		"fbforward_audit_records_total",
		"fbforward_udp_rate_limit_drops_total",
		"fbforward_online_rules_active",
		"fbforward_online_rule_errors_total",
		"fbforward_webhook_deliveries_total",
		"fbforward_firewall_denied_total",
	}
	if len(types) != len(expectedFamilies) {
		t.Fatalf("metric family count = %d, want %d", len(types), len(expectedFamilies))
	}
	for _, family := range expectedFamilies {
		if !types[family] {
			t.Fatalf("missing metric family %q", family)
		}
	}
}

func TestRenderEscapesLabels(t *testing.T) {
	upstreamTag := "upstream\"\\\nline"
	route := "route\"\\\n"
	m := NewMetrics([]string{upstreamTag})
	m.SetRouteSelected(route, upstreamTag)
	rendered := m.Render()
	if !strings.Contains(rendered, `upstream="upstream\"\\\nline"`) {
		t.Fatalf("upstream label was not escaped:\n%s", rendered)
	}
	if !strings.Contains(rendered, `route="route\"\\\n"`) {
		t.Fatalf("route label was not escaped:\n%s", rendered)
	}
}

func TestInitialUpstreamHealthIsUnknown(t *testing.T) {
	rendered := NewMetrics([]string{"primary"}).Render()
	if !strings.Contains(rendered, `fbforward_upstream_health_state{upstream="primary",state="unknown"} 1`) {
		t.Fatalf("initial health was not unknown:\n%s", rendered)
	}
	if strings.Contains(rendered, `fbforward_upstream_health_state{upstream="primary",state="healthy"} 1`) {
		t.Fatal("initial health unexpectedly reported healthy")
	}
}

func TestNormalizeFlowReasons(t *testing.T) {
	tests := map[string]string{
		"eof":                      "eof",
		"idle_timeout":             "timeout",
		"firewall_deny":            "policy",
		"backend_blocked":          "policy",
		"tcp_connection_limit":     "capacity",
		"udp_mapping_limit":        "capacity",
		"udp_per_ip_mapping_limit": "capacity",
		"write_error":              "io_error",
		"read_error":               "io_error",
		"upstream_write_error":     "io_error",
		"upstream_read_error":      "io_error",
		"client_write_error":       "io_error",
		"context_done":             "canceled",
		"upstream_unusable":        "upstream_unusable",
		"dial_failed":              "dial_failed",
		"shutdown":                 "shutdown",
		"peer detail":              "other",
	}
	for input, want := range tests {
		if got := normalizeReason(input); got != want {
			t.Errorf("normalizeReason(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestConcurrentUpdateAndRender(t *testing.T) {
	m := NewMetrics([]string{"primary"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				m.AddTraffic("primary", "tcp", "up", 1)
				m.RecordFlowEvent("tcp", "close", "eof")
				_ = m.Render()
			}
		}()
	}
	wg.Wait()
	if !strings.Contains(m.Render(), `fbforward_traffic_bytes_total{upstream="primary",protocol="tcp",direction="up"} 8000`) {
		t.Fatal("concurrent traffic updates were lost")
	}
}

func BenchmarkAddTraffic(b *testing.B) {
	m := NewMetrics([]string{"primary"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.AddTraffic("primary", "tcp", "up", 1024)
	}
}

func BenchmarkRender(b *testing.B) {
	tags := make([]string, 16)
	for i := range tags {
		tags[i] = "upstream-" + string(rune('a'+i))
	}
	m := NewMetrics(tags)
	for i, tag := range tags {
		m.SetRouteSelected("route-"+string(rune('a'+i%8)), tag)
		m.AddTraffic(tag, "tcp", "up", 1024)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Render()
	}
}
