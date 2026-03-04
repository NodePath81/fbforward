package measure

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	probe "github.com/NodePath81/fbforward/bwprobe/pkg"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

const (
	maxConsecutiveFailures = 3
	retryDelay             = 30 * time.Second
)

const (
	defaultProbeRateUp   = "10m"
	defaultProbeRateDown = "50m"
)

type Collector struct {
	cfg            config.MeasurementConfig
	scoring        config.ScoringConfig
	manager        *upstream.UpstreamManager
	metrics        *metrics.Metrics
	logger         util.Logger
	scheduler      *Scheduler
	OnTestComplete func(upstream, protocol, direction string, startTime time.Time, duration time.Duration, success bool, result *TestResultMetrics, errMsg string)

	failuresMu  sync.Mutex
	failures    map[string]*failureState
	runningMu   sync.RWMutex
	running     map[string]*RunningTest
	nextCycleID uint64
}

type failureState struct {
	consecutiveFailures int
	inFallbackMode      bool
}

type RunningTest struct {
	Upstream  string
	Protocol  string
	Direction string
	StartTime time.Time
}

type TestResultMetrics struct {
	RTTMs       float64
	JitterMs    float64
	LossRate    float64
	RetransRate float64
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
		running:   make(map[string]*RunningTest),
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
					NextScheduled: status.NextScheduled,
					LastRun:       status.LastRun,
				})
			}
			next, ok := c.scheduler.NextReady()
			if !ok {
				continue
			}
			measurement := *next
			if err := c.RunDirection(ctx, measurement.upstream, measurement.protocol, measurement.direction); err != nil {
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
		return c.handleMeasurementFailure(up.Tag, err)
	}
	c.handleMeasurementSuccess(up.Tag, result)
	return nil
}

// RunDirection executes a single-direction measurement.
func (c *Collector) RunDirection(ctx context.Context, up *upstream.Upstream, network, direction string) error {
	protoCfg, err := c.protocolConfig(network)
	if err != nil {
		return err
	}

	cycleCtx, cancel := context.WithTimeout(ctx, protoCfg.Timeout.PerCycle.Duration())
	defer cancel()

	result, err := c.runSingleDirection(cycleCtx, up, network, direction, protoCfg)
	if err != nil {
		return c.handleMeasurementFailure(up.Tag, err)
	}
	c.handleMeasurementSuccess(up.Tag, result)
	return nil
}

func (c *Collector) handleMeasurementFailure(tag string, err error) error {
	state := c.failure(tag)
	state.consecutiveFailures++
	if util.BoolValue(c.cfg.FallbackToICMPOnStale, true) && state.consecutiveFailures >= maxConsecutiveFailures {
		if !state.inFallbackMode {
			util.Event(c.logger, slog.LevelWarn, "measure.fallback_entered",
				"upstream", tag,
				"consecutive_failures", state.consecutiveFailures,
			)
		}
		state.inFallbackMode = true
	}
	return err
}

func (c *Collector) handleMeasurementSuccess(tag string, result *upstream.MeasurementResult) {
	state := c.failure(tag)
	state.consecutiveFailures = 0
	if state.inFallbackMode {
		util.Event(c.logger, slog.LevelInfo, "measure.fallback_recovered", "upstream", tag)
		state.inFallbackMode = false
	}
	stats := c.manager.UpdateMeasurement(tag, result, c.scoring)
	if c.metrics != nil {
		c.metrics.SetUpstreamMetrics(tag, stats)
	}
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

	bwUp, err := config.ParseBandwidth(defaultProbeRateUp)
	if err != nil {
		return nil, fmt.Errorf("parse internal probe upload rate: %w", err)
	}
	bwDown, err := config.ParseBandwidth(defaultProbeRateDown)
	if err != nil {
		return nil, fmt.Errorf("parse internal probe download rate: %w", err)
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

	uploadCycleID := c.newCycleID()
	util.Event(c.logger, slog.LevelInfo, "measure.started",
		"measure.cycle_id", uploadCycleID,
		"upstream", up.Tag,
		"network.protocol", network,
		"network.direction", "upload",
		"measure.target_bps", bwUp,
	)

	c.setRunning(up.Tag, network, "upload")

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
	c.clearRunning(up.Tag, network, "upload")
	if c.OnTestComplete != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		c.OnTestComplete(
			up.Tag,
			network,
			"upload",
			uploadStart,
			uploadDuration,
			err == nil,
			buildTestResultMetrics(network, "upload", upResult),
			errMsg,
		)
	}
	if err != nil {
		state := c.failure(up.Tag)
		util.Event(c.logger, slog.LevelWarn, "measure.failed",
			"measure.cycle_id", uploadCycleID,
			"upstream", up.Tag,
			"network.protocol", network,
			"network.direction", "upload",
			"measure.duration_ms", uploadDuration.Milliseconds(),
			"consecutive_failures", state.consecutiveFailures+1,
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
	util.Event(c.logger, slog.LevelInfo, "measure.completed",
		"measure.cycle_id", uploadCycleID,
		"upstream", up.Tag,
		"network.protocol", network,
		"network.direction", "upload",
		"measure.duration_ms", uploadDuration.Milliseconds(),
		"measure.achieved_bps", result.BandwidthUpBps,
		"measure.rtt_ms", result.RTTMs,
		"measure.jitter_ms", result.JitterMs,
		"measure.loss_rate", lossOrRetrans,
	)

	c.setRunning(up.Tag, network, "download")

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

	downloadCycleID := c.newCycleID()
	util.Event(c.logger, slog.LevelInfo, "measure.started",
		"measure.cycle_id", downloadCycleID,
		"upstream", up.Tag,
		"network.protocol", network,
		"network.direction", "download",
		"measure.target_bps", bwDown,
	)

	sampleCtx, cancel = context.WithTimeout(ctx, protoCfg.Timeout.PerSample.Duration())
	downloadStart := time.Now()
	dnResult, err := probe.Run(sampleCtx, dnCfg)
	cancel()
	downloadDuration := time.Since(downloadStart)
	c.clearRunning(up.Tag, network, "download")
	if c.OnTestComplete != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		c.OnTestComplete(
			up.Tag,
			network,
			"download",
			downloadStart,
			downloadDuration,
			err == nil,
			buildTestResultMetrics(network, "download", dnResult),
			errMsg,
		)
	}
	if err != nil {
		state := c.failure(up.Tag)
		util.Event(c.logger, slog.LevelWarn, "measure.failed",
			"measure.cycle_id", downloadCycleID,
			"upstream", up.Tag,
			"network.protocol", network,
			"network.direction", "download",
			"measure.duration_ms", downloadDuration.Milliseconds(),
			"consecutive_failures", state.consecutiveFailures+1,
			"error", err,
		)
		return nil, err
	}

	result.BandwidthDownBps = dnResult.Throughput.AchievedBps
	downRTTMs := float64(dnResult.RTT.Mean) / float64(time.Millisecond)
	downJitterMs := float64(dnResult.RTT.Jitter) / float64(time.Millisecond)
	downLossOrRetrans := dnResult.Loss.LossRate
	util.Event(c.logger, slog.LevelInfo, "measure.completed",
		"measure.cycle_id", downloadCycleID,
		"upstream", up.Tag,
		"network.protocol", network,
		"network.direction", "download",
		"measure.duration_ms", downloadDuration.Milliseconds(),
		"measure.achieved_bps", result.BandwidthDownBps,
		"measure.rtt_ms", downRTTMs,
		"measure.jitter_ms", downJitterMs,
		"measure.loss_rate", downLossOrRetrans,
	)

	return result, nil
}

func (c *Collector) runSingleDirection(ctx context.Context, up *upstream.Upstream, network, direction string, protoCfg config.MeasurementProtocolConfig) (*upstream.MeasurementResult, error) {
	target := up.MeasureHost
	if target == "" {
		target = up.Host
	}
	port := up.MeasurePort
	if port == 0 {
		port = 9876
	}

	var targetBps uint64
	reverse := false
	switch direction {
	case "upload":
		bw, err := config.ParseBandwidth(defaultProbeRateUp)
		if err != nil {
			return nil, fmt.Errorf("parse internal probe upload rate: %w", err)
		}
		targetBps = bw
	case "download":
		bw, err := config.ParseBandwidth(defaultProbeRateDown)
		if err != nil {
			return nil, fmt.Errorf("parse internal probe download rate: %w", err)
		}
		targetBps = bw
		reverse = true
	default:
		return nil, fmt.Errorf("invalid direction: %s", direction)
	}

	sampleBytes, err := config.ParseSize(protoCfg.SampleSize)
	if err != nil {
		return nil, fmt.Errorf("parse sample_size: %w", err)
	}
	chunkSize, err := config.ParseSize(protoCfg.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("parse %s chunk_size: %w", network, err)
	}

	cycleID := c.newCycleID()
	util.Event(c.logger, slog.LevelInfo, "measure.started",
		"measure.cycle_id", cycleID,
		"upstream", up.Tag,
		"network.protocol", network,
		"network.direction", direction,
		"measure.target_bps", targetBps,
	)

	c.setRunning(up.Tag, network, direction)
	defer c.clearRunning(up.Tag, network, direction)

	cfg := probe.Config{
		Target:       target,
		Port:         port,
		Network:      network,
		BandwidthBps: int64(targetBps),
		Reverse:      reverse,
		SampleBytes:  int64(sampleBytes),
		Samples:      protoCfg.SampleCount,
		ChunkSize:    int64(chunkSize),
	}

	sampleCtx, cancel := context.WithTimeout(ctx, protoCfg.Timeout.PerSample.Duration())
	testStart := time.Now()
	testResult, err := probe.Run(sampleCtx, cfg)
	cancel()
	testDuration := time.Since(testStart)
	if c.OnTestComplete != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		c.OnTestComplete(
			up.Tag,
			network,
			direction,
			testStart,
			testDuration,
			err == nil,
			buildTestResultMetrics(network, direction, testResult),
			errMsg,
		)
	}
	if err != nil {
		state := c.failure(up.Tag)
		util.Event(c.logger, slog.LevelWarn, "measure.failed",
			"measure.cycle_id", cycleID,
			"upstream", up.Tag,
			"network.protocol", network,
			"network.direction", direction,
			"measure.duration_ms", testDuration.Milliseconds(),
			"consecutive_failures", state.consecutiveFailures+1,
			"error", err,
		)
		return nil, err
	}

	result := &upstream.MeasurementResult{
		Timestamp: time.Now(),
		Network:   network,
	}

	if direction == "upload" {
		result.BandwidthUpBps = testResult.Throughput.AchievedBps
	} else {
		result.BandwidthDownBps = testResult.Throughput.AchievedBps
	}

	result.RTTMs = float64(testResult.RTT.Mean) / float64(time.Millisecond)
	result.JitterMs = float64(testResult.RTT.Jitter) / float64(time.Millisecond)

	if network == "tcp" {
		result.RetransRate = testResult.Loss.LossRate
	} else {
		result.LossRate = testResult.Loss.LossRate
	}

	lossOrRetrans := result.LossRate
	if network == "tcp" {
		lossOrRetrans = result.RetransRate
	}

	util.Event(c.logger, slog.LevelInfo, "measure.completed",
		"measure.cycle_id", cycleID,
		"upstream", up.Tag,
		"network.protocol", network,
		"network.direction", direction,
		"measure.duration_ms", testDuration.Milliseconds(),
		"measure.achieved_bps", testResult.Throughput.AchievedBps,
		"measure.rtt_ms", result.RTTMs,
		"measure.jitter_ms", result.JitterMs,
		"measure.loss_rate", lossOrRetrans,
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

func buildTestResultMetrics(network, _ string, result *probe.Results) *TestResultMetrics {
	if result == nil {
		return nil
	}
	metrics := &TestResultMetrics{
		RTTMs:    float64(result.RTT.Mean) / float64(time.Millisecond),
		JitterMs: float64(result.RTT.Jitter) / float64(time.Millisecond),
	}
	if network == "tcp" {
		metrics.RetransRate = result.Loss.LossRate
	} else {
		metrics.LossRate = result.Loss.LossRate
	}
	return metrics
}

func (c *Collector) setRunning(tag, protocol, direction string) {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	key := tag + ":" + protocol + ":" + direction
	c.running[key] = &RunningTest{
		Upstream:  tag,
		Protocol:  protocol,
		Direction: direction,
		StartTime: time.Now(),
	}
}

func (c *Collector) clearRunning(tag, protocol, direction string) {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	key := tag + ":" + protocol + ":" + direction
	delete(c.running, key)
}

func (c *Collector) RunningTests() []RunningTest {
	c.runningMu.RLock()
	defer c.runningMu.RUnlock()
	result := make([]RunningTest, 0, len(c.running))
	for _, test := range c.running {
		result = append(result, *test)
	}
	return result
}

func (c *Collector) failure(tag string) *failureState {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()
	if c.failures[tag] == nil {
		c.failures[tag] = &failureState{}
	}
	return c.failures[tag]
}

func (c *Collector) newCycleID() string {
	return fmt.Sprintf("m-%d", atomic.AddUint64(&c.nextCycleID, 1))
}
