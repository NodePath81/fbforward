package app

import (
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/forwarding"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
)

func TestUpstreamPickerAdapterMapsActiveIPAndDialFeedback(t *testing.T) {
	selected := &upstream.Upstream{Tag: "primary"}
	selected.SetActiveIP(net.ParseIP("203.0.113.10"))
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{selected}, nil)
	if err := manager.SetManual("primary"); err != nil {
		t.Fatalf("SetManual error: %v", err)
	}
	picker := &upstreamPicker{manager: manager}

	got, err := picker.Pick(flow.Meta{})
	if err != nil {
		t.Fatalf("Pick error: %v", err)
	}
	if got.Tag != "primary" || got.Addr != netip.MustParseAddr("203.0.113.10") {
		t.Fatalf("unexpected picked upstream: %+v", got)
	}

	picker.MarkDialFailure(got, time.Second)
	if _, err := picker.Pick(flow.Meta{}); err == nil {
		t.Fatalf("expected dial failure feedback to make upstream unavailable")
	}
	picker.ClearDialFailure(got)
	if _, err := picker.Pick(flow.Meta{}); err != nil {
		t.Fatalf("expected clear dial failure to restore upstream: %v", err)
	}
}

func TestUpstreamPickerScopesAdaptiveRoutesAndKeepsStaticFixed(t *testing.T) {
	makeUpstream := func(tag, ip string) *upstream.Upstream {
		up := &upstream.Upstream{Tag: tag}
		up.SetActiveIP(net.ParseIP(ip))
		return up
	}
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{
		makeUpstream("outside", "203.0.113.10"),
		makeUpstream("route-a", "203.0.113.11"),
		makeUpstream("route-b", "203.0.113.12"),
		makeUpstream("static", "203.0.113.13"),
	}, nil)
	if err := manager.SetManual("outside"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		manager.RecordProbeFailure("static", time.Now())
	}
	picker := newUpstreamPicker(manager, []config.RouteConfig{
		{Name: "adaptive", Strategy: "adaptive", Upstreams: []string{"route-a", "route-b"}},
		{Name: "static", Strategy: "static", Upstreams: []string{"static"}},
	})
	adaptive, err := picker.Pick(flow.Meta{Route: "adaptive"})
	if err != nil {
		t.Fatal(err)
	}
	if adaptive.Tag != "route-a" && adaptive.Tag != "route-b" {
		t.Fatalf("adaptive route escaped its upstream set: %+v", adaptive)
	}
	fixed, err := picker.Pick(flow.Meta{Route: "static"})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Tag != "static" {
		t.Fatalf("static route changed with global active selection: %+v", fixed)
	}
}

func TestUpstreamPickerOverrideRecordsRouteSelection(t *testing.T) {
	selected := &upstream.Upstream{Tag: "primary"}
	selected.SetActiveIP(net.ParseIP("203.0.113.10"))
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{selected}, nil)
	metricSet := metrics.NewMetrics([]string{"primary"})
	picker := &upstreamPicker{manager: manager, metrics: metricSet}

	if _, err := picker.PickOverride(flow.Meta{Route: "override-route"}, "primary"); err != nil {
		t.Fatalf("PickOverride error: %v", err)
	}
	if got := metricSet.Render(); !strings.Contains(got, `fbforward_route_selected_upstream{route="override-route",upstream="primary"} 1`) {
		t.Fatalf("override route selection was not recorded:\n%s", got)
	}
}

var _ forwarding.AdmissionPolicy = (*firewallPolicy)(nil)
var _ forwarding.UpstreamPicker = (*upstreamPicker)(nil)
var _ forwarding.DialFeedback = (*upstreamPicker)(nil)
var _ forwarding.OverridePicker = (*upstreamPicker)(nil)
