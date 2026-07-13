package measure

import (
	"errors"
	"strings"
	"testing"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
)

func TestCollectorFailureUpdatesHealthAndMetrics(t *testing.T) {
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{{Tag: "primary"}}, nil)
	metricSet := metrics.NewMetrics([]string{"primary"})
	collector := NewCollector(config.MeasurementConfig{}, manager, metricSet, nil, nil)

	if err := collector.handleMeasurementFailure("primary", errors.New("probe timeout")); err == nil {
		t.Fatal("expected measurement failure")
	}
	stats, ok := manager.Health("primary")
	if !ok || stats.ConsecutiveFailures != 1 {
		t.Fatalf("unexpected health after failure: ok=%v snapshot=%+v", ok, stats)
	}
	upstreamMetrics, ok := metricSet.GetUpstreamMetrics("primary")
	if !ok || upstreamMetrics.ConsecutiveFailures != 1 {
		t.Fatalf("metrics did not receive failed observation: ok=%v metrics=%+v", ok, upstreamMetrics)
	}
	rendered := metricSet.Render()
	if !strings.Contains(rendered, `fbforward_upstream_probe_failures_total{upstream="primary"} 1`) {
		t.Fatalf("missing probe failure counter:\n%s", rendered)
	}
}
