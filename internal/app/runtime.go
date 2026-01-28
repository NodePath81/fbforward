package app

import (
	"context"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/control"
	"github.com/NodePath81/fbforward/internal/forwarding"
	"github.com/NodePath81/fbforward/internal/measure"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/probe"
	"github.com/NodePath81/fbforward/internal/resolver"
	"github.com/NodePath81/fbforward/internal/shaping"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

const dnsRefreshInterval = 30 * time.Second

type Runtime struct {
	cfg       config.Config
	ctx       context.Context
	cancel    context.CancelFunc
	logger    util.Logger
	resolver  *resolver.Resolver
	manager   *upstream.UpstreamManager
	metrics   *metrics.Metrics
	status    *control.StatusStore
	control   *control.ControlServer
	shaper    *shaping.TrafficShaper
	upstreams []*upstream.Upstream
	listeners []closer
	collector *measure.Collector
	wg        sync.WaitGroup
}

type closer interface {
	Close() error
}

func NewRuntime(cfg config.Config, logger util.Logger, restartFn func() error) (*Runtime, error) {
	ctx, cancel := context.WithCancel(context.Background())
	resolver := resolver.NewResolver(cfg.DNS)
	upstreams, err := resolveUpstreams(ctx, cfg, resolver)
	if err != nil {
		cancel()
		return nil, err
	}
	tags := make([]string, 0, len(upstreams))
	for _, up := range upstreams {
		tags = append(tags, up.Tag)
	}
	metrics := metrics.NewMetrics(tags)
	utilWindow := cfg.Scoring.UtilizationPenalty.WindowDuration.Duration()
	metrics.SetUtilizationWindow(utilWindow)
	statusHub := control.NewStatusHub(ctx.Done())
	status := control.NewStatusStore(statusHub, metrics)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	manager := upstream.NewUpstreamManager(upstreams, rng)
	manager.SetSwitching(cfg.Switching)
	manager.SetMeasurementConfig(cfg.Measurement)

	rt := &Runtime{
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
		resolver:  resolver,
		manager:   manager,
		metrics:   metrics,
		status:    status,
		upstreams: upstreams,
	}
	if cfg.Shaping.Enabled {
		// Build upstream shaping entries with resolved IPs
		upstreamShaping := buildUpstreamShapingEntries(cfg.Upstreams, upstreams)
		rt.shaper = shaping.NewTrafficShaper(cfg.Shaping, cfg.Forwarding.Listeners, upstreamShaping, logger)
	}

	manager.SetCallbacks(func(oldTag, newTag string) {
		if oldTag != newTag {
			if newTag == "" {
				logger.Info("active upstream cleared")
			} else {
				logger.Info("active upstream changed", "from", oldTag, "to", newTag)
			}
			metrics.SetActive(newTag)
		}
	}, func(tag string, usable bool) {
		logger.Info("upstream state changed", "upstream", tag, "usable", usable)
		if !usable && cfg.Switching.CloseFlowsOnFailover {
			status.CloseByUpstream(tag)
		}
	})

	metrics.SetMode(upstream.ModeAuto)
	manager.SetAuto()
	metrics.Start(ctx.Done())

	ctrl := control.NewControlServer(cfg, manager, metrics, status, restartFn, logger)
	rt.control = ctrl

	return rt, nil
}

func (r *Runtime) Start() error {
	if err := r.control.Start(r.ctx); err != nil {
		return err
	}

	if r.shaper != nil {
		if err := r.shaper.Apply(); err != nil {
			r.Stop()
			return err
		}
	}

	r.runFastStart()
	r.startProbes()
	r.startMeasurement()
	r.startDNSRefresh()

	if err := r.startListeners(); err != nil {
		r.Stop()
		return err
	}
	return nil
}

func (r *Runtime) Stop() {
	r.cancel()
	if r.status != nil {
		r.status.CloseAll()
	}
	for _, ln := range r.listeners {
		_ = ln.Close()
	}
	if r.shaper != nil {
		if err := r.shaper.Cleanup(); err != nil {
			r.logger.Error("shaping cleanup failed", "error", err)
		}
	}
	if r.control != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = r.control.Shutdown(ctx)
		cancel()
	}
	r.wait()
}

func (r *Runtime) startProbes() {
	for _, up := range r.upstreams {
		r.wg.Add(1)
		go func(u *upstream.Upstream) {
			defer r.wg.Done()
			probe.ProbeLoop(r.ctx, u, r.cfg.Reachability, r.manager, r.metrics, r.logger)
		}(up)
	}
}

func (r *Runtime) startMeasurement() {
	tcpCfg := r.cfg.Measurement.Protocols.TCP
	udpCfg := r.cfg.Measurement.Protocols.UDP

	protocols := make([]string, 0, 2)
	if util.BoolValue(tcpCfg.Enabled, true) {
		protocols = append(protocols, "tcp")
	}
	if util.BoolValue(udpCfg.Enabled, true) {
		protocols = append(protocols, "udp")
	}
	if len(protocols) == 0 {
		return
	}

	tcpTargetUpBps, err := config.ParseBandwidth(tcpCfg.TargetBandwidth.Upload)
	if err != nil {
		r.logger.Error("invalid measurement protocols.tcp.target_bandwidth.upload", "error", err)
		return
	}
	tcpTargetDownBps, err := config.ParseBandwidth(tcpCfg.TargetBandwidth.Download)
	if err != nil {
		r.logger.Error("invalid measurement protocols.tcp.target_bandwidth.download", "error", err)
		return
	}
	udpTargetUpBps, err := config.ParseBandwidth(udpCfg.TargetBandwidth.Upload)
	if err != nil {
		r.logger.Error("invalid measurement protocols.udp.target_bandwidth.upload", "error", err)
		return
	}
	udpTargetDownBps, err := config.ParseBandwidth(udpCfg.TargetBandwidth.Download)
	if err != nil {
		r.logger.Error("invalid measurement protocols.udp.target_bandwidth.download", "error", err)
		return
	}
	requiredHeadroomBps, err := config.ParseBandwidth(r.cfg.Measurement.Schedule.Headroom.RequiredFreeBandwidth)
	if err != nil {
		r.logger.Error("invalid measurement schedule headroom.required_free_bandwidth", "error", err)
		return
	}

	rateWindow := r.cfg.Scoring.UtilizationPenalty.WindowDuration.Duration()
	if rateWindow <= 0 {
		rateWindow = 5 * time.Second
	}

	var aggregateLimitBps int64
	var upstreamLimits map[string]measure.UpstreamLimit
	if r.cfg.Shaping.Enabled {
		aggregateLimit := r.cfg.Shaping.AggregateLimit
		if aggregateLimit == "" {
			aggregateLimit = config.DefaultAggregateLimit
		}
		aggBits, err := config.ParseBandwidth(aggregateLimit)
		if err != nil {
			r.logger.Warn("invalid shaping.aggregate_limit", "value", aggregateLimit, "error", err)
		} else if aggBits == 0 {
			aggBits, err = config.ParseBandwidth(config.DefaultAggregateLimit)
			if err != nil {
				r.logger.Warn("invalid default shaping.aggregate_limit", "value", config.DefaultAggregateLimit, "error", err)
			}
		}
		aggregateLimitBps = int64(aggBits)

		upstreamLimits = make(map[string]measure.UpstreamLimit)
		for _, upCfg := range r.cfg.Upstreams {
			if upCfg.Shaping == nil {
				continue
			}
			var uploadBps int64
			var downloadBps int64
			if upCfg.Shaping.UploadLimit != "" {
				limit, err := config.ParseBandwidth(upCfg.Shaping.UploadLimit)
				if err != nil {
					r.logger.Warn("invalid upstream shaping upload_limit", "tag", upCfg.Tag, "value", upCfg.Shaping.UploadLimit, "error", err)
				} else if limit > 0 {
					uploadBps = int64(limit)
				}
			}
			if upCfg.Shaping.DownloadLimit != "" {
				limit, err := config.ParseBandwidth(upCfg.Shaping.DownloadLimit)
				if err != nil {
					r.logger.Warn("invalid upstream shaping download_limit", "tag", upCfg.Tag, "value", upCfg.Shaping.DownloadLimit, "error", err)
				} else if limit > 0 {
					downloadBps = int64(limit)
				}
			}
			if uploadBps > 0 || downloadBps > 0 {
				upstreamLimits[upCfg.Tag] = measure.UpstreamLimit{
					UploadLimitBps:   uploadBps,
					DownloadLimitBps: downloadBps,
				}
			}
		}
		if len(upstreamLimits) == 0 {
			upstreamLimits = nil
		}
	}

	scheduler := measure.NewScheduler(measure.SchedulerConfig{
		MinInterval:         r.cfg.Measurement.Schedule.Interval.Min.Duration(),
		MaxInterval:         r.cfg.Measurement.Schedule.Interval.Max.Duration(),
		InterUpstreamGap:    r.cfg.Measurement.Schedule.UpstreamGap.Duration(),
		MaxUtilization:      r.cfg.Measurement.Schedule.Headroom.MaxLinkUtilization,
		RequiredHeadroomBps: int64(requiredHeadroomBps),
		TCPTargetUpBps:      int64(tcpTargetUpBps),
		TCPTargetDownBps:    int64(tcpTargetDownBps),
		UDPTargetUpBps:      int64(udpTargetUpBps),
		UDPTargetDownBps:    int64(udpTargetDownBps),
		Protocols:           protocols,
		RateWindow:          rateWindow,
		AggregateLimitBps:   aggregateLimitBps,
		UpstreamLimits:      upstreamLimits,
	}, r.metrics, r.upstreams, nil)
	if r.control != nil {
		r.control.SetScheduler(scheduler)
	}

	r.collector = measure.NewCollector(r.cfg.Measurement, r.cfg.Scoring, r.manager, r.metrics, scheduler, r.logger)
	r.collector.OnTestComplete = func(upstream, protocol, direction string, startTime time.Time, duration time.Duration, success bool, result *measure.TestResultMetrics, errMsg string) {
		if r.status == nil {
			return
		}
		payload := control.TestHistoryPayload{
			Upstream:   upstream,
			Protocol:   protocol,
			Direction:  direction,
			Timestamp:  startTime.UnixMilli(),
			DurationMs: duration.Milliseconds(),
			Success:    success,
		}
		if result != nil {
			payload.BandwidthUpBps = result.BandwidthUpBps
			payload.BandwidthDownBps = result.BandwidthDownBps
			payload.RTTMs = result.RTTMs
			payload.JitterMs = result.JitterMs
			payload.LossRate = result.LossRate
			payload.RetransRate = result.RetransRate
		}
		if !success && errMsg != "" {
			payload.Error = errMsg
		}
		r.status.BroadcastTestHistoryEvent(payload)
	}
	if r.control != nil {
		r.control.SetCollector(r.collector)
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.collector.RunLoop(r.ctx)
	}()
}

func (r *Runtime) runFastStart() {
	if !util.BoolValue(r.cfg.Measurement.FastStart.Enabled, true) {
		return
	}
	timeout := r.cfg.Measurement.FastStart.Timeout.Duration()
	ctx, cancel := context.WithTimeout(r.ctx, timeout*time.Duration(len(r.upstreams)+1))
	defer cancel()

	var wg sync.WaitGroup
	scores := make(map[string]float64)
	var mu sync.Mutex

	for _, up := range r.upstreams {
		wg.Add(1)
		go func(u *upstream.Upstream) {
			defer wg.Done()
			host := u.MeasureHost
			if host == "" {
				host = u.Host
			}
			port := u.MeasurePort
			if port == 0 {
				port = 9876
			}
			rtt, reachable := measure.FastStartProbe(ctx, host, port, timeout)
			score := measure.FastStartScore(rtt, reachable, u.Priority)
			mu.Lock()
			scores[u.Tag] = score
			mu.Unlock()
		}(up)
	}
	wg.Wait()

	r.manager.SelectByFastStart(scores)
	r.manager.StartWarmup(r.cfg.Measurement.FastStart.WarmupDuration.Duration())
}

func (r *Runtime) startListeners() error {
	r.listeners = nil
	for _, ln := range r.cfg.Forwarding.Listeners {
		switch ln.Protocol {
		case "tcp":
			tcpListener := forwarding.NewTCPListener(ln, r.cfg.Forwarding.Limits, r.cfg.Forwarding.IdleTimeout.TCP.Duration(), r.manager, r.metrics, r.status, r.logger)
			if err := tcpListener.Start(r.ctx, &r.wg); err != nil {
				return err
			}
			r.listeners = append(r.listeners, tcpListener)
		case "udp":
			udpListener := forwarding.NewUDPListener(ln, r.cfg.Forwarding.Limits, r.cfg.Forwarding.IdleTimeout.UDP.Duration(), r.manager, r.metrics, r.status, r.logger)
			if err := udpListener.Start(r.ctx, &r.wg); err != nil {
				return err
			}
			r.listeners = append(r.listeners, udpListener)
		}
	}
	return nil
}

func (r *Runtime) wait() {
	r.wg.Wait()
}

func (r *Runtime) startDNSRefresh() {
	for _, upstream := range r.upstreams {
		if net.ParseIP(upstream.Host) != nil {
			continue
		}
		up := upstream
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			ticker := time.NewTicker(dnsRefreshInterval)
			defer ticker.Stop()
			for {
				select {
				case <-r.ctx.Done():
					return
				case <-ticker.C:
					ips, err := r.resolver.ResolveHost(r.ctx, up.Host)
					if err != nil {
						r.logger.Debug("upstream resolve failed", "upstream", up.Tag, "error", err)
						continue
					}
					changed := r.manager.UpdateResolved(up.Tag, ips)
					if changed {
						active := up.ActiveIP()
						activeStr := ""
						if active != nil {
							activeStr = active.String()
						}
						r.logger.Info("upstream resolved", "upstream", up.Tag, "active_ip", activeStr)
						if r.shaper != nil && upstreamHasShaping(r.cfg.Upstreams, up.Tag) {
							upstreamShaping := buildUpstreamShapingEntries(r.cfg.Upstreams, r.upstreams)
							if err := r.shaper.UpdateUpstreams(upstreamShaping); err != nil {
								r.logger.Error("shaping reapply failed", "upstream", up.Tag, "error", err)
							}
						}
					}
				}
			}
		}()
	}
}

func resolveUpstreams(ctx context.Context, cfg config.Config, res *resolver.Resolver) ([]*upstream.Upstream, error) {
	upstreams := make([]*upstream.Upstream, 0, len(cfg.Upstreams))
	for _, item := range cfg.Upstreams {
		ips, err := res.ResolveHost(ctx, item.Destination.Host)
		if err != nil {
			return nil, err
		}
		up := &upstream.Upstream{
			Tag:         item.Tag,
			Host:        item.Destination.Host,
			MeasureHost: item.Measurement.Host,
			MeasurePort: item.Measurement.Port,
			Priority:    item.Priority,
			Bias:        item.Bias,
			IPs:         ips,
		}
		up.SetActiveIP(ips[0])
		upstreams = append(upstreams, up)
	}
	return upstreams, nil
}

// buildUpstreamShapingEntries creates shaping entries from config and resolved upstreams.
func buildUpstreamShapingEntries(cfgUpstreams []config.UpstreamConfig, resolvedUpstreams []*upstream.Upstream) []shaping.UpstreamShapingEntry {
	// Create a map of tag -> resolved IPs for quick lookup
	tagToIPs := make(map[string][]string, len(resolvedUpstreams))
	for _, up := range resolvedUpstreams {
		ips := make([]string, 0, len(up.IPs))
		for _, ip := range up.IPs {
			ips = append(ips, ip.String())
		}
		tagToIPs[up.Tag] = ips
	}

	entries := make([]shaping.UpstreamShapingEntry, 0)
	for _, cfgUp := range cfgUpstreams {
		// Only include upstreams that have shaping config
		if cfgUp.Shaping == nil {
			continue
		}
		ips, ok := tagToIPs[cfgUp.Tag]
		if !ok || len(ips) == 0 {
			continue
		}
		entries = append(entries, shaping.UpstreamShapingEntry{
			Tag:           cfgUp.Tag,
			IPs:           ips,
			UploadLimit:   cfgUp.Shaping.UploadLimit,
			DownloadLimit: cfgUp.Shaping.DownloadLimit,
		})
	}
	return entries
}

func upstreamHasShaping(cfgUpstreams []config.UpstreamConfig, tag string) bool {
	for _, up := range cfgUpstreams {
		if up.Tag != tag {
			continue
		}
		return up.Shaping != nil
	}
	return false
}
