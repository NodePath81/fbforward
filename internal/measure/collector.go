package measure

import (
	"context"
	"fmt"
	"time"

	probe "github.com/NodePath81/fbforward/bwprobe/pkg"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

const maxConsecutiveFailures = 3

type Collector struct {
	upstream *upstream.Upstream
	cfg      config.MeasurementConfig
	scoring  config.ScoringConfig
	manager  *upstream.UpstreamManager
	metrics  *metrics.Metrics
	logger   util.Logger

	nextTCP             bool
	consecutiveFailures int
	inFallbackMode      bool
}

func NewCollector(up *upstream.Upstream, cfg config.MeasurementConfig, scoring config.ScoringConfig, manager *upstream.UpstreamManager, metrics *metrics.Metrics, logger util.Logger) *Collector {
	return &Collector{
		upstream: up,
		cfg:      cfg,
		scoring:  scoring,
		manager:  manager,
		metrics:  metrics,
		logger:   logger,
		nextTCP:  true,
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
	ticker := time.NewTicker(c.cfg.Interval.Duration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runCycle(ctx)
		}
	}
}

func (c *Collector) runCycle(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, c.cfg.MaxCycleDuration.Duration())
	defer cancel()

	if boolValue(c.cfg.AlternateTCP, true) {
		tcpEnabled := boolValue(c.cfg.TCPEnabled, true)
		udpEnabled := boolValue(c.cfg.UDPEnabled, true)
		if tcpEnabled && udpEnabled {
			if c.nextTCP {
				c.runProtocol(cycleCtx, "tcp")
			} else {
				c.runProtocol(cycleCtx, "udp")
			}
			c.nextTCP = !c.nextTCP
			return
		}
		if tcpEnabled {
			c.runProtocol(cycleCtx, "tcp")
			return
		}
		if udpEnabled {
			c.runProtocol(cycleCtx, "udp")
			return
		}
		return
	}

	if boolValue(c.cfg.TCPEnabled, true) {
		c.runProtocol(cycleCtx, "tcp")
	}
	if boolValue(c.cfg.UDPEnabled, true) {
		c.runProtocol(cycleCtx, "udp")
	}
}

func (c *Collector) runProtocol(ctx context.Context, network string) {
	result, err := c.runMeasurement(ctx, network)
	if err != nil {
		c.consecutiveFailures++
		c.logger.Warn("measurement failed", "upstream", c.upstream.Tag, "network", network, "error", err, "failures", c.consecutiveFailures)
		if boolValue(c.cfg.FallbackToICMP, true) && c.consecutiveFailures >= maxConsecutiveFailures {
			if !c.inFallbackMode {
				c.logger.Warn("falling back to ICMP-only mode", "upstream", c.upstream.Tag)
			}
			c.inFallbackMode = true
		}
		return
	}

	c.consecutiveFailures = 0
	if c.inFallbackMode {
		c.logger.Info("recovered from ICMP fallback", "upstream", c.upstream.Tag)
		c.inFallbackMode = false
	}

	utilization := 0.0
	if c.metrics != nil {
		utilization = c.metrics.GetUtilization(c.upstream.Tag, result.BandwidthUpBps, result.BandwidthDownBps, c.cfg.Interval.Duration())
	}
	stats := c.manager.UpdateMeasurement(c.upstream.Tag, result, c.scoring, utilization)
	if c.metrics != nil {
		c.metrics.SetUpstreamMetrics(c.upstream.Tag, stats)
	}
}

func (c *Collector) runMeasurement(ctx context.Context, network string) (*upstream.MeasurementResult, error) {
	target := c.upstream.MeasureHost
	if target == "" {
		target = c.upstream.Host
	}
	port := c.upstream.MeasurePort
	if port == 0 {
		port = 9876
	}

	bwUp, err := config.ParseBandwidth(c.cfg.TargetBandwidthUp)
	if err != nil {
		return nil, fmt.Errorf("parse target_bandwidth_up: %w", err)
	}
	bwDown, err := config.ParseBandwidth(c.cfg.TargetBandwidthDown)
	if err != nil {
		return nil, fmt.Errorf("parse target_bandwidth_down: %w", err)
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

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
