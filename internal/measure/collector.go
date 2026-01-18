package measure

import (
	"context"
	"fmt"
	"strings"
	"time"

	probe "github.com/NodePath81/fbforward/bwprobe/pkg"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

const maxConsecutiveFailures = 3

type Collector struct {
	cfg       config.MeasurementConfig
	scoring   config.ScoringConfig
	manager   *upstream.UpstreamManager
	metrics   *metrics.Metrics
	logger    util.Logger
	scheduler *Scheduler

	failures map[string]*failureState
}

type failureState struct {
	consecutiveFailures int
	inFallbackMode      bool
}

func NewCollector(cfg config.MeasurementConfig, scoring config.ScoringConfig, manager *upstream.UpstreamManager, metrics *metrics.Metrics, scheduler *Scheduler, logger util.Logger) *Collector {
	return &Collector{
		cfg:       cfg,
		scoring:   scoring,
		manager:   manager,
		metrics:   metrics,
		scheduler: scheduler,
		logger:    logger,
		failures:  make(map[string]*failureState),
	}
}

func (c *Collector) RunLoop(ctx context.Context) {
	delay := c.cfg.DiscoveryDelay.Duration()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.scheduler.Schedule()
			next, ok := c.scheduler.NextReady()
			if !ok {
				continue
			}
			measurement := *next
			if err := c.runProtocol(ctx, measurement.upstream, measurement.protocol); err != nil {
				c.scheduler.Requeue(measurement, retryDelay)
				continue
			}
			c.scheduler.MarkRun(measurement)
		}
	}
}

func (c *Collector) runProtocol(ctx context.Context, up *upstream.Upstream, network string) error {
	cycleCtx, cancel := context.WithTimeout(ctx, c.cfg.MaxCycleDuration.Duration())
	defer cancel()

	result, err := c.runMeasurement(cycleCtx, up, network)
	if err != nil {
		state := c.failure(up.Tag)
		state.consecutiveFailures++
		c.logger.Warn("measurement failed", "upstream", up.Tag, "network", network, "error", err, "failures", state.consecutiveFailures)
		if util.BoolValue(c.cfg.FallbackToICMP, true) && state.consecutiveFailures >= maxConsecutiveFailures {
			if !state.inFallbackMode {
				c.logger.Warn("falling back to ICMP-only mode", "upstream", up.Tag)
			}
			state.inFallbackMode = true
		}
		return err
	}

	state := c.failure(up.Tag)
	state.consecutiveFailures = 0
	if state.inFallbackMode {
		c.logger.Info("recovered from ICMP fallback", "upstream", up.Tag)
		state.inFallbackMode = false
	}

	utilization := 0.0
	if c.metrics != nil {
		utilWindow := time.Duration(c.scoring.UtilizationWindowSec) * time.Second
		if utilWindow <= 0 {
			utilWindow = 5 * time.Second
		}
		utilization = c.metrics.GetUtilization(up.Tag, result.BandwidthUpBps, result.BandwidthDownBps, utilWindow)
	}
	stats := c.manager.UpdateMeasurement(up.Tag, result, c.scoring, utilization)
	if c.metrics != nil {
		c.metrics.SetUpstreamMetrics(up.Tag, stats)
	}
	return nil
}

func (c *Collector) runMeasurement(ctx context.Context, up *upstream.Upstream, network string) (*upstream.MeasurementResult, error) {
	target := up.MeasureHost
	if target == "" {
		target = up.Host
	}
	port := up.MeasurePort
	if port == 0 {
		port = 9876
	}

	bwUp, err := c.targetBandwidth(network, "up")
	if err != nil {
		return nil, fmt.Errorf("parse %s target_bandwidth_up: %w", network, err)
	}
	bwDown, err := c.targetBandwidth(network, "down")
	if err != nil {
		return nil, fmt.Errorf("parse %s target_bandwidth_down: %w", network, err)
	}
	sampleBytes, err := config.ParseSize(c.cfg.SampleBytes)
	if err != nil {
		return nil, fmt.Errorf("parse sample_bytes: %w", err)
	}

	result := &upstream.MeasurementResult{
		Timestamp: time.Now(),
		Network:   network,
	}

	upCfg := probe.Config{
		Target:       target,
		Port:         port,
		Network:      network,
		BandwidthBps: int64(bwUp),
		Reverse:      false,
		SampleBytes:  int64(sampleBytes),
		Samples:      c.cfg.Samples,
	}

	sampleCtx, cancel := context.WithTimeout(ctx, c.cfg.MaxSampleDuration.Duration())
	upResult, err := probe.Run(sampleCtx, upCfg)
	cancel()
	if err != nil {
		return nil, err
	}

	result.BandwidthUpBps = upResult.Throughput.AchievedBps
	result.RTTMs = float64(upResult.RTT.Mean) / float64(time.Millisecond)
	result.JitterMs = float64(upResult.RTT.Jitter) / float64(time.Millisecond)

	if network == "tcp" {
		result.RetransRate = upResult.Loss.LossRate
	} else {
		result.LossRate = upResult.Loss.LossRate
	}

	dnCfg := probe.Config{
		Target:       target,
		Port:         port,
		Network:      network,
		BandwidthBps: int64(bwDown),
		Reverse:      true,
		SampleBytes:  int64(sampleBytes),
		Samples:      c.cfg.Samples,
	}

	sampleCtx, cancel = context.WithTimeout(ctx, c.cfg.MaxSampleDuration.Duration())
	dnResult, err := probe.Run(sampleCtx, dnCfg)
	cancel()
	if err != nil {
		return nil, err
	}

	result.BandwidthDownBps = dnResult.Throughput.AchievedBps

	return result, nil
}

func (c *Collector) targetBandwidth(network, direction string) (uint64, error) {
	network = strings.ToLower(network)
	direction = strings.ToLower(direction)
	var raw string
	switch network {
	case "tcp":
		if direction == "down" {
			raw = c.cfg.TCPTargetBandwidthDown
		} else {
			raw = c.cfg.TCPTargetBandwidthUp
		}
	case "udp":
		if direction == "down" {
			raw = c.cfg.UDPTargetBandwidthDown
		} else {
			raw = c.cfg.UDPTargetBandwidthUp
		}
	}
	if raw == "" {
		if direction == "down" {
			raw = c.cfg.TargetBandwidthDown
		} else {
			raw = c.cfg.TargetBandwidthUp
		}
	}
	return config.ParseBandwidth(raw)
}

func (c *Collector) failure(tag string) *failureState {
	if c.failures[tag] == nil {
		c.failures[tag] = &failureState{}
	}
	return c.failures[tag]
}
