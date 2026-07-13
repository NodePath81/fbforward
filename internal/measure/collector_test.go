package measure

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
)

func TestCollectorRunsReadyProbeImmediately(t *testing.T) {
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{{Tag: "primary"}}, nil)
	scheduler := NewScheduler(SchedulerConfig{MinInterval: time.Minute, MaxInterval: time.Minute, Protocols: []string{"tcp"}}, []*upstream.Upstream{manager.Get("primary")}, nil)
	collector := NewCollector(config.MeasurementConfig{}, manager, nil, scheduler, nil)
	scheduler.Schedule()

	called := false
	collector.runReadyWith(context.Background(), func(_ context.Context, got *upstream.Upstream, protocol string) error {
		called = got != nil && got.Tag == "primary" && protocol == "tcp"
		return nil
	})
	if !called {
		t.Fatal("expected the ready probe to run without waiting for the ticker")
	}
	if status := scheduler.Status(); status.QueueLength != 1 || len(status.LastRun) != 1 {
		t.Fatalf("expected one scheduled follow-up after the first probe, got %+v", status)
	}
}

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

func TestCollectorSyncsStaleHealthToMetrics(t *testing.T) {
	manager := upstream.NewUpstreamManager([]*upstream.Upstream{{Tag: "primary"}}, nil)
	manager.SetHealthConfig(config.HealthConfig{
		RTTEWMAAlpha:      0.25,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		StaleThreshold:    config.Duration(time.Second),
	})
	manager.UpdateMeasurement("primary", &upstream.MeasurementResult{
		RTTMs:     10,
		Timestamp: time.Now().Add(-2 * time.Second),
	})
	metricSet := metrics.NewMetrics([]string{"primary"})
	collector := NewCollector(config.MeasurementConfig{}, manager, metricSet, nil, nil)

	collector.syncUpstreamMetrics()
	got, ok := metricSet.GetUpstreamMetrics("primary")
	if !ok || got.HealthState != string(upstream.HealthStale) {
		t.Fatalf("expected metrics to expose stale health, ok=%v metrics=%+v", ok, got)
	}
	if !strings.Contains(metricSet.Render(), `fbforward_upstream_health_state{upstream="primary",state="stale"} 1`) {
		t.Fatal("rendered metrics did not expose stale health")
	}
}
