package measure

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/NodePath81/fbforward/pkg/fbmeasure"
)

const retryDelay = 30 * time.Second

const defaultProbeTimeout = 2 * time.Second

type Collector struct {
	cfg            config.MeasurementConfig
	manager        *upstream.UpstreamManager
	metrics        *metrics.Metrics
	logger         util.Logger
	scheduler      *Scheduler
	OnTestComplete func(upstream, protocol string, startTime time.Time, duration time.Duration, success bool, result *TestResultMetrics, errMsg string)

	runningMu   sync.RWMutex
	running     map[string]*RunningTest
	nextCycleID uint64
}

type RunningTest struct {
	Upstream  string
	Protocol  string
	StartTime time.Time
}

type TestResultMetrics struct {
	RTTMs float64
}

func NewCollector(cfg config.MeasurementConfig, manager *upstream.UpstreamManager, metrics *metrics.Metrics, scheduler *Scheduler, logger util.Logger) *Collector {
	return &Collector{
		cfg:       cfg,
		manager:   manager,
		metrics:   metrics,
		scheduler: scheduler,
		logger:    logger,
		running:   make(map[string]*RunningTest),
	}
}

func (c *Collector) RunLoop(ctx context.Context) {
	c.scheduler.Schedule()
	c.syncUpstreamMetrics()
	c.runReady(ctx)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.scheduler.Schedule()
			c.syncUpstreamMetrics()
			c.runReady(ctx)
		}
	}
}

func (c *Collector) runReady(ctx context.Context) {
	c.runReadyWith(ctx, c.RunProtocol)
}

func (c *Collector) runReadyWith(ctx context.Context, run func(context.Context, *upstream.Upstream, string) error) {
	next, ok := c.scheduler.NextReady()
	if !ok {
		return
	}
	measurement := *next
	if err := run(ctx, measurement.upstream, measurement.protocol); err != nil {
		c.scheduler.Requeue(measurement, retryDelay)
	} else {
		c.scheduler.MarkRun(measurement)
	}
	c.syncUpstreamMetrics()
}

func (c *Collector) syncUpstreamMetrics() {
	if c.metrics == nil {
		return
	}
	if c.manager != nil {
		for tag, stats := range c.manager.StatsSnapshot() {
			c.metrics.SetUpstreamMetrics(tag, stats)
		}
	}
	c.syncScheduleMetrics()
}

func (c *Collector) syncScheduleMetrics() {
	if c.metrics == nil || c.scheduler == nil {
		return
	}
	status := c.scheduler.Status()
	c.metrics.SetScheduleMetrics(metrics.ScheduleMetrics{
		QueueSize:     status.QueueLength,
		NextScheduled: status.NextScheduled,
		LastRun:       status.LastRun,
	})
}

func (c *Collector) RunProtocol(ctx context.Context, up *upstream.Upstream, network string) error {
	if _, err := c.protocolConfig(network); err != nil {
		return err
	}
	probeTimeout := c.cfg.ProbeTimeout.Duration()
	if probeTimeout <= 0 {
		probeTimeout = defaultProbeTimeout
	}
	cycleCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	result, err := c.runMeasurement(cycleCtx, up, network, probeTimeout)
	if err != nil {
		return c.handleMeasurementFailure(up.Tag, err)
	}
	c.handleMeasurementSuccess(up.Tag, result)
	return nil
}

func (c *Collector) handleMeasurementFailure(tag string, err error) error {
	stats := c.manager.RecordProbeFailure(tag, time.Now())
	if c.metrics != nil {
		c.metrics.RecordProbe(tag, false)
		c.metrics.SetUpstreamMetrics(tag, stats)
	}
	util.Event(c.logger, slog.LevelWarn, "measure.probe_failed", "upstream", tag, "error", err)
	return err
}

func (c *Collector) handleMeasurementSuccess(tag string, result *upstream.MeasurementResult) {
	stats := c.manager.UpdateMeasurement(tag, result)
	if c.metrics != nil {
		c.metrics.RecordProbe(tag, true)
		c.metrics.SetUpstreamMetrics(tag, stats)
	}
}

func (c *Collector) runMeasurement(ctx context.Context, up *upstream.Upstream, network string, timeout time.Duration) (*upstream.MeasurementResult, error) {
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

	client, err := fbmeasure.NewClient(fbmeasure.ClientConfig{Address: addr, Timeout: timeout})
	if err != nil {
		errMsg = err.Error()
		return nil, err
	}
	defer client.Close()

	var probeResult fbmeasure.Result
	if network == "tcp" {
		probeResult, err = client.ProbeTCP(ctx)
	} else {
		probeResult, err = client.ProbeUDP(ctx)
	}
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
		Timestamp: probeResult.ObservedAt,
		RTTMs:     float64(probeResult.RTT) / float64(time.Millisecond),
	}
	resultMetrics.RTTMs = result.RTTMs

	success = true
	util.Event(c.logger, slog.LevelInfo, "measure.completed",
		"measure.cycle_id", cycleID,
		"upstream", up.Tag,
		"network.protocol", network,
		"measure.duration_ms", time.Since(startTime).Milliseconds(),
		"measure.rtt_ms", result.RTTMs,
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

func (c *Collector) newCycleID() string {
	return fmt.Sprintf("m-%d", atomic.AddUint64(&c.nextCycleID, 1))
}
