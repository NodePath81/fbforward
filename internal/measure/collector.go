package measure

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

	failuresMu sync.Mutex
	failures   map[string]*failureState
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
	delay := c.cfg.StartupDelay.Duration()
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
			if c.metrics != nil {
				status := c.scheduler.Status()
				c.metrics.SetScheduleMetrics(metrics.ScheduleMetrics{
					QueueSize:     status.QueueLength,
					SkippedTotal:  status.SkippedTotal,
					NextScheduled: status.NextScheduled,
					LastRun:       status.LastRun,
				})
			}
			next, ok := c.scheduler.NextReady()
			if !ok {
				continue
			}
			measurement := *next
			if err := c.RunProtocol(ctx, measurement.upstream, measurement.protocol); err != nil {
				c.scheduler.Requeue(measurement, retryDelay)
				continue
			}
			c.scheduler.MarkRun(measurement)
		}
	}
}

// RunProtocol executes a measurement cycle for the given upstream and protocol.
func (c *Collector) RunProtocol(ctx context.Context, up *upstream.Upstream, network string) error {
	protoCfg, err := c.protocolConfig(network)
	if err != nil {
		return err
	}

	cycleCtx, cancel := context.WithTimeout(ctx, protoCfg.Timeout.PerCycle.Duration())
	defer cancel()

	result, err := c.runMeasurement(cycleCtx, up, network, protoCfg)
	if err != nil {
		state := c.failure(up.Tag)
		state.consecutiveFailures++
		c.logger.Warn("measurement failed", "upstream", up.Tag, "network", network, "error", err, "failures", state.consecutiveFailures)
		if util.BoolValue(c.cfg.FallbackToICMPOnStale, true) && state.consecutiveFailures >= maxConsecutiveFailures {
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
		utilWindow := c.scoring.UtilizationPenalty.WindowDuration.Duration()
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

func (c *Collector) runMeasurement(ctx context.Context, up *upstream.Upstream, network string, protoCfg config.MeasurementProtocolConfig) (*upstream.MeasurementResult, error) {
	target := up.MeasureHost
	if target == "" {
		target = up.Host
	}
	port := up.MeasurePort
	if port == 0 {
		port = 9876
	}

	bwUp, err := config.ParseBandwidth(protoCfg.TargetBandwidth.Upload)
	if err != nil {
		return nil, fmt.Errorf("parse %s target_bandwidth.upload: %w", network, err)
	}
	bwDown, err := config.ParseBandwidth(protoCfg.TargetBandwidth.Download)
	if err != nil {
		return nil, fmt.Errorf("parse %s target_bandwidth.download: %w", network, err)
	}
	sampleBytes, err := config.ParseSize(protoCfg.SampleSize)
	if err != nil {
		return nil, fmt.Errorf("parse sample_size: %w", err)
	}
	chunkSize, err := config.ParseSize(protoCfg.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("parse %s chunk_size: %w", network, err)
	}

	result := &upstream.MeasurementResult{
		Timestamp: time.Now(),
		Network:   network,
	}

	c.logger.Info("measurement started",
		"upstream", up.Tag,
		"network", network,
		"direction", "upload",
		"target_bps", bwUp,
		"sample_bytes", sampleBytes,
	)

	upCfg := probe.Config{
		Target:       target,
		Port:         port,
		Network:      network,
		BandwidthBps: int64(bwUp),
		Reverse:      false,
		SampleBytes:  int64(sampleBytes),
		Samples:      protoCfg.SampleCount,
		ChunkSize:    int64(chunkSize),
	}

	sampleCtx, cancel := context.WithTimeout(ctx, protoCfg.Timeout.PerSample.Duration())
	uploadStart := time.Now()
	upResult, err := probe.Run(sampleCtx, upCfg)
	cancel()
	uploadDuration := time.Since(uploadStart)
	if err != nil {
		c.logger.Warn("measurement upload failed",
			"upstream", up.Tag,
			"network", network,
			"direction", "upload",
			"duration_ms", uploadDuration.Milliseconds(),
			"error", err,
		)
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

	lossOrRetrans := result.LossRate
	if network == "tcp" {
		lossOrRetrans = result.RetransRate
	}
	c.logger.Info("measurement upload completed",
		"upstream", up.Tag,
		"network", network,
		"direction", "upload",
		"duration_ms", uploadDuration.Milliseconds(),
		"bandwidth_bps", result.BandwidthUpBps,
		"rtt_ms", result.RTTMs,
		"jitter_ms", result.JitterMs,
		"loss_or_retrans", lossOrRetrans,
	)

	dnCfg := probe.Config{
		Target:       target,
		Port:         port,
		Network:      network,
		BandwidthBps: int64(bwDown),
		Reverse:      true,
		SampleBytes:  int64(sampleBytes),
		Samples:      protoCfg.SampleCount,
		ChunkSize:    int64(chunkSize),
	}

	c.logger.Info("measurement started",
		"upstream", up.Tag,
		"network", network,
		"direction", "download",
		"target_bps", bwDown,
		"sample_bytes", sampleBytes,
	)

	sampleCtx, cancel = context.WithTimeout(ctx, protoCfg.Timeout.PerSample.Duration())
	downloadStart := time.Now()
	dnResult, err := probe.Run(sampleCtx, dnCfg)
	cancel()
	downloadDuration := time.Since(downloadStart)
	if err != nil {
		c.logger.Warn("measurement download failed",
			"upstream", up.Tag,
			"network", network,
			"direction", "download",
			"duration_ms", downloadDuration.Milliseconds(),
			"error", err,
		)
		return nil, err
	}

	result.BandwidthDownBps = dnResult.Throughput.AchievedBps
	c.logger.Info("measurement download completed",
		"upstream", up.Tag,
		"network", network,
		"direction", "download",
		"duration_ms", downloadDuration.Milliseconds(),
		"bandwidth_bps", result.BandwidthDownBps,
	)

	return result, nil
}

func (c *Collector) protocolConfig(network string) (config.MeasurementProtocolConfig, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "tcp":
		return c.cfg.Protocols.TCP, nil
	case "udp":
		return c.cfg.Protocols.UDP, nil
	default:
		return config.MeasurementProtocolConfig{}, fmt.Errorf("unsupported protocol %q", network)
	}
}

func (c *Collector) failure(tag string) *failureState {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	if c.failures[tag] == nil {
		c.failures[tag] = &failureState{}
	}
	return c.failures[tag]
}
