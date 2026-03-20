package measure

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/fbmeasure"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

const (
	maxConsecutiveFailures = 3
	retryDelay             = 30 * time.Second
)

type Collector struct {
	cfg            config.MeasurementConfig
	scoring        config.ScoringConfig
	manager        *upstream.UpstreamManager
	metrics        *metrics.Metrics
	logger         util.Logger
	scheduler      *Scheduler
	OnTestComplete func(upstream, protocol string, startTime time.Time, duration time.Duration, success bool, result *TestResultMetrics, errMsg string)

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
			if err := c.RunProtocol(ctx, measurement.upstream, measurement.protocol); err != nil {
				c.scheduler.Requeue(measurement, retryDelay)
				continue
			}
			c.scheduler.MarkRun(measurement)
		}
	}
}

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

func (c *Collector) handleMeasurementFailure(tag string, err error) error {
	c.failuresMu.Lock()
	state := c.failureLocked(tag)
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
	c.failuresMu.Unlock()
	return err
}

func (c *Collector) handleMeasurementSuccess(tag string, result *upstream.MeasurementResult) {
	c.failuresMu.Lock()
	state := c.failureLocked(tag)
	state.consecutiveFailures = 0
	if state.inFallbackMode {
		util.Event(c.logger, slog.LevelInfo, "measure.fallback_recovered", "upstream", tag)
		state.inFallbackMode = false
	}
	c.failuresMu.Unlock()
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
	addr := net.JoinHostPort(target, strconv.Itoa(port))

	startTime := time.Now()
	cycleID := c.newCycleID()
	util.Event(c.logger, slog.LevelInfo, "measure.started",
		"measure.cycle_id", cycleID,
		"upstream", up.Tag,
		"network.protocol", network,
	)

	c.setRunning(up.Tag, network)
	defer c.clearRunning(up.Tag, network)

	resultMetrics := &TestResultMetrics{}
	success := false
	var errMsg string
	defer func() {
		if c.OnTestComplete == nil {
			return
		}
		if !success && errMsg == "" {
			errMsg = "measurement failed"
		}
		c.OnTestComplete(up.Tag, network, startTime, time.Since(startTime), success, resultMetrics, errMsg)
	}()

	client, err := fbmeasure.Dial(ctx, addr, fbmeasure.ClientSecurityConfig{
		Mode:           c.cfg.Security.Mode,
		CAFile:         c.cfg.Security.CAFile,
		ServerName:     c.cfg.Security.ServerName,
		ClientCertFile: c.cfg.Security.ClientCertFile,
		ClientKeyFile:  c.cfg.Security.ClientKeyFile,
	})
	if err != nil {
		errMsg = err.Error()
		util.Event(c.logger, slog.LevelWarn, "measure.failed",
			"measure.cycle_id", cycleID,
			"upstream", up.Tag,
			"network.protocol", network,
			"error", err,
		)
		return nil, err
	}
	defer client.Close()

	rttCtx, cancel := context.WithTimeout(ctx, protoCfg.Timeout.PerSample.Duration())
	var rttStats fbmeasure.RTTStats
	if network == "tcp" {
		rttStats, err = client.PingTCP(rttCtx, protoCfg.PingCount)
	} else {
		rttStats, err = client.PingUDP(rttCtx, protoCfg.PingCount)
	}
	cancel()
	if err != nil {
		errMsg = err.Error()
		util.Event(c.logger, slog.LevelWarn, "measure.failed",
			"measure.cycle_id", cycleID,
			"upstream", up.Tag,
			"network.protocol", network,
			"error", err,
		)
		return nil, err
	}

	result := &upstream.MeasurementResult{
		Timestamp: time.Now(),
		Network:   network,
		RTTMs:     float64(rttStats.Mean) / float64(time.Millisecond),
		JitterMs:  float64(rttStats.Jitter) / float64(time.Millisecond),
	}
	resultMetrics.RTTMs = result.RTTMs
	resultMetrics.JitterMs = result.JitterMs

	sampleCtx, cancel := context.WithTimeout(ctx, protoCfg.Timeout.PerSample.Duration())
	switch network {
	case "tcp":
		retransBytes, parseErr := config.ParseSize(protoCfg.RetransmitBytes)
		if parseErr != nil {
			cancel()
			err = fmt.Errorf("parse retransmit_bytes: %w", parseErr)
			errMsg = err.Error()
			return nil, err
		}
		retrans, callErr := client.TCPRetrans(sampleCtx, uint64(retransBytes))
		cancel()
		if callErr != nil {
			errMsg = callErr.Error()
			return nil, callErr
		}
		if err = validateRetransResult(retrans); err != nil {
			errMsg = err.Error()
			return nil, err
		}
		result.RetransRate = retrans.Rate()
		resultMetrics.RetransRate = result.RetransRate
	case "udp":
		packetSize, parseErr := config.ParseSize(protoCfg.PacketSize)
		if parseErr != nil {
			cancel()
			err = fmt.Errorf("parse packet_size: %w", parseErr)
			errMsg = err.Error()
			return nil, err
		}
		loss, callErr := client.UDPLoss(sampleCtx, protoCfg.LossPackets, int(packetSize))
		cancel()
		if callErr != nil {
			errMsg = callErr.Error()
			return nil, callErr
		}
		if err = validateLossResult(loss); err != nil {
			errMsg = err.Error()
			return nil, err
		}
		result.LossRate = loss.LossRate
		resultMetrics.LossRate = result.LossRate
	default:
		cancel()
		return nil, fmt.Errorf("unsupported protocol %q", network)
	}

	success = true
	util.Event(c.logger, slog.LevelInfo, "measure.completed",
		"measure.cycle_id", cycleID,
		"upstream", up.Tag,
		"network.protocol", network,
		"measure.duration_ms", time.Since(startTime).Milliseconds(),
		"measure.rtt_ms", result.RTTMs,
		"measure.jitter_ms", result.JitterMs,
		"measure.loss_rate", maxMetric(result.LossRate, result.RetransRate),
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

func (c *Collector) setRunning(tag, protocol string) {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	key := tag + ":" + protocol
	c.running[key] = &RunningTest{
		Upstream:  tag,
		Protocol:  protocol,
		StartTime: time.Now(),
	}
}

func (c *Collector) clearRunning(tag, protocol string) {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	key := tag + ":" + protocol
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

func (c *Collector) failureLocked(tag string) *failureState {
	if c.failures[tag] == nil {
		c.failures[tag] = &failureState{}
	}
	return c.failures[tag]
}

func (c *Collector) newCycleID() string {
	return fmt.Sprintf("m-%d", atomic.AddUint64(&c.nextCycleID, 1))
}

func maxMetric(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func validateRetransResult(result fbmeasure.RetransResult) error {
	if math.IsNaN(result.Rate()) || math.IsInf(result.Rate(), 0) {
		return fmt.Errorf("invalid tcp retransmission rate")
	}
	if result.Retransmits > result.SegmentsSent {
		return fmt.Errorf("invalid tcp retransmission counters")
	}
	if result.SegmentsSent == 0 && result.Retransmits != 0 {
		return fmt.Errorf("invalid tcp retransmission counters")
	}
	return nil
}

func validateLossResult(result fbmeasure.LossResult) error {
	if result.PacketsRecv > result.PacketsSent {
		return fmt.Errorf("invalid udp loss counters")
	}
	if result.PacketsLost > result.PacketsSent {
		return fmt.Errorf("invalid udp loss counters")
	}
	if math.IsNaN(result.LossRate) || math.IsInf(result.LossRate, 0) || result.LossRate < 0 || result.LossRate > 1 {
		return fmt.Errorf("invalid udp loss rate")
	}
	return nil
}
