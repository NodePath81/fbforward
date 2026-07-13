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
	"github.com/NodePath81/fbforward/internal/fbmeasure"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

const retryDelay = 30 * time.Second

type Collector struct {
	cfg            config.MeasurementConfig
	health         config.HealthConfig
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

func NewCollector(cfg config.MeasurementConfig, health config.HealthConfig, manager *upstream.UpstreamManager, metrics *metrics.Metrics, scheduler *Scheduler, logger util.Logger) *Collector {
	return &Collector{
		cfg:       cfg,
		health:    health,
		manager:   manager,
		metrics:   metrics,
		scheduler: scheduler,
		logger:    logger,
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
	c.manager.RecordProbeFailure(tag, "", time.Now())
	util.Event(c.logger, slog.LevelWarn, "measure.probe_failed", "upstream", tag, "error", err)
	return err
}

func (c *Collector) handleMeasurementSuccess(tag string, result *upstream.MeasurementResult) {
	stats := c.manager.UpdateMeasurement(tag, result, c.health)
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
