package app

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"log/slog"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/control"
	"github.com/NodePath81/fbforward/internal/coordination"
	"github.com/NodePath81/fbforward/internal/firewall"
	"github.com/NodePath81/fbforward/internal/forwarding"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/iplog"
	"github.com/NodePath81/fbforward/internal/measure"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/notify"
	"github.com/NodePath81/fbforward/internal/probe"
	"github.com/NodePath81/fbforward/internal/resolver"
	"github.com/NodePath81/fbforward/internal/shaping"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

const dnsRefreshInterval = 30 * time.Second

type Runtime struct {
	cfg           config.Config
	ctx           context.Context
	cancel        context.CancelFunc
	logger        util.Logger
	resolver      *resolver.Resolver
	manager       *upstream.UpstreamManager
	metrics       *metrics.Metrics
	status        *control.StatusStore
	control       *control.ControlServer
	coord         *coordination.Controller
	shaper        *shaping.TrafficShaper
	geoipMgr      *geoip.Manager
	iplogStore    *iplog.Store
	iplogPipeline *iplog.Pipeline
	firewall      *firewall.Engine
	upstreams     []*upstream.Upstream
	listeners     []closer
	collector     *measure.Collector
	notifier      *notify.Client
	notifyPolicy  *notify.Policy
	wg            sync.WaitGroup
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
	statusHub := control.NewStatusHub(ctx.Done(), util.ComponentLogger(logger, util.CompControl))
	status := control.NewStatusStore(statusHub, metrics)
	seed := time.Now().UnixNano()
	var seedBuf [8]byte
	if _, err := crand.Read(seedBuf[:]); err == nil {
		seed = int64(binary.LittleEndian.Uint64(seedBuf[:]))
	}
	rng := rand.New(rand.NewSource(seed))
	manager := upstream.NewUpstreamManager(upstreams, rng, util.ComponentLogger(logger, util.CompUpstream))
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
	if cfg.GeoIP.Enabled {
		geoMgr, err := geoip.NewManager(cfg.GeoIP, logger)
		if err != nil {
			cancel()
			return nil, err
		}
		rt.geoipMgr = geoMgr
	}
	if cfg.IPLog.Enabled {
		store, err := iplog.NewStore(cfg.IPLog.DBPath)
		if err != nil {
			cancel()
			return nil, err
		}
		rt.iplogStore = store
		rt.iplogStore.StartRetention(ctx, cfg.IPLog.Retention.Duration(), cfg.IPLog.PruneInterval.Duration())
		rt.iplogPipeline = iplog.NewPipeline(cfg.IPLog, rt.geoipMgr, store, metrics, logger)
	}
	if cfg.Firewall.Enabled {
		fw, err := firewall.NewEngine(cfg.Firewall, rt.geoipMgr, metrics, logger)
		if err != nil {
			cancel()
			if rt.iplogStore != nil {
				_ = rt.iplogStore.Close()
			}
			return nil, err
		}
		rt.firewall = fw
	}
	if cfg.Shaping.Enabled {
		// Build upstream shaping entries with resolved IPs
		upstreamShaping := buildUpstreamShapingEntries(cfg.Upstreams, upstreams)
		rt.shaper = shaping.NewTrafficShaper(cfg.Shaping, cfg.Forwarding.Listeners, upstreamShaping, logger)
	}

	if cfg.Notify.Enabled {
		notifyLogger := util.ComponentLogger(logger, util.CompNotify)
		notifier, err := notify.NewClient(notify.Config{
			Endpoint:       cfg.Notify.Endpoint,
			KeyID:          cfg.Notify.KeyID,
			Token:          cfg.Notify.Token,
			SourceService:  "fbforward",
			SourceInstance: cfg.Notify.SourceInstance,
			Logger:         notifyLogger,
		})
		if err != nil {
			cancel()
			return nil, err
		}
		rt.notifier = notifier
		rt.notifyPolicy = notify.NewPolicy(notifier, notify.PolicyConfig{
			StartTime:            time.Now(),
			CoordinationEndpoint: cfg.Coordination.Endpoint,
			StartupGracePeriod:   cfg.Notify.StartupGracePeriod.Duration(),
			UnusableInterval:     cfg.Notify.UnusableInterval.Duration(),
			NotifyInterval:       cfg.Notify.NotifyInterval.Duration(),
		})
	}

	manager.SetCallbacks(func(change upstream.ActiveChange) {
		if change.OldTag != change.NewTag {
			metrics.SetActive(change.NewTag)
		}
	}, func(change upstream.UsabilityChange) {
		if !change.Usable && cfg.Switching.CloseFlowsOnFailover {
			status.CloseByUpstream(change.Tag)
		}
		if rt.notifyPolicy != nil {
			rt.notifyPolicy.HandleUsabilityChange(change.Tag, change.Usable, change.Reason)
		}
	})

	metrics.SetMode(upstream.ModeAuto)
	metrics.SetCoordinationState(manager.CoordinationState())
	manager.SetAuto()
	metrics.Start(ctx.Done())

	var coordCtrl *coordination.Controller
	if cfg.Coordination.IsConfigured() {
		coordCtrl = coordination.NewController(ctx, cfg.Coordination, manager, metrics, util.ComponentLogger(logger, util.CompCoord))
		if rt.notifyPolicy != nil {
			coordCtrl.SetConnectionCallback(rt.notifyPolicy.HandleCoordinationConnection)
		}
	}
	ctrl := control.NewControlServer(cfg, manager, metrics, status, coordCtrl, restartFn, logger)
	if rt.notifier != nil {
		ctrl.SetNotifier(rt.notifier)
	}
	if rt.geoipMgr != nil {
		ctrl.SetGeoIPManager(rt.geoipMgr)
	}
	if rt.iplogStore != nil {
		ctrl.SetIPLogStore(rt.iplogStore)
	}
	rt.control = ctrl
	rt.coord = coordCtrl

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
	if r.geoipMgr != nil {
		r.geoipMgr.Start(r.ctx)
	}
	if r.iplogPipeline != nil {
		r.iplogPipeline.Start()
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
	if r.iplogPipeline != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.iplogPipeline.Shutdown(ctx); err != nil {
			util.Event(r.logger, slog.LevelWarn, "iplog.shutdown_failed", "error", err)
		}
		cancel()
	}
	if r.geoipMgr != nil {
		if err := r.geoipMgr.Close(); err != nil {
			util.Event(r.logger, slog.LevelWarn, "geoip.close_failed", "error", err)
		}
	}
	if r.iplogStore != nil {
		if err := r.iplogStore.Close(); err != nil {
			util.Event(r.logger, slog.LevelWarn, "iplog.store_close_failed", "error", err)
		}
	}
	if r.shaper != nil {
		if err := r.shaper.Cleanup(); err != nil {
			util.Event(r.logger, slog.LevelError, "shaping.cleanup_failed", "error", err)
		}
	}
	if r.control != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = r.control.Shutdown(ctx)
		cancel()
	}
	if r.notifyPolicy != nil {
		r.notifyPolicy.Close()
	}
	if r.coord != nil {
		r.coord.Close()
	}
	if r.notifier != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.notifier.Close(ctx); err != nil {
			util.Event(r.logger, slog.LevelWarn, "notify.shutdown_failed", "error", err)
		}
		cancel()
	}
	r.wait()
}

func (r *Runtime) startProbes() {
	for _, up := range r.upstreams {
		r.wg.Add(1)
		go func(u *upstream.Upstream) {
			defer r.wg.Done()
			probe.ProbeLoop(r.ctx, u, r.cfg.Reachability, r.manager, r.metrics, util.ComponentLogger(r.logger, util.CompProbe))
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

	measureLogger := util.ComponentLogger(r.logger, util.CompMeasure)
	scheduler := measure.NewScheduler(measure.SchedulerConfig{
		MinInterval:      r.cfg.Measurement.Schedule.Interval.Min.Duration(),
		MaxInterval:      r.cfg.Measurement.Schedule.Interval.Max.Duration(),
		InterUpstreamGap: r.cfg.Measurement.Schedule.UpstreamGap.Duration(),
		Protocols:        protocols,
	}, r.upstreams, nil)
	if r.control != nil {
		r.control.SetScheduler(scheduler)
	}

	r.collector = measure.NewCollector(r.cfg.Measurement, r.cfg.Scoring, r.manager, r.metrics, scheduler, measureLogger)
	r.collector.OnTestComplete = func(upstream, protocol string, startTime time.Time, duration time.Duration, success bool, result *measure.TestResultMetrics, errMsg string) {
		if r.status == nil {
			return
		}
		payload := control.TestHistoryPayload{
			Upstream:   upstream,
			Protocol:   protocol,
			Timestamp:  startTime.UnixMilli(),
			DurationMs: duration.Milliseconds(),
			Success:    success,
		}
		if result != nil {
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
	rtts := make(map[string]float64)
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
			rtts[u.Tag] = rtt
			mu.Unlock()
		}(up)
	}
	wg.Wait()

	r.manager.SelectByFastStart(scores)
	active := r.manager.ActiveTag()
	if active != "" {
		util.Event(util.ComponentLogger(r.logger, util.CompUpstream), slog.LevelInfo, "upstream.fast_start_selected",
			"upstream", active,
			"measure.rtt_ms", rtts[active],
		)
	}
	r.manager.StartWarmup(r.cfg.Measurement.FastStart.WarmupDuration.Duration())
}

func (r *Runtime) startListeners() error {
	r.listeners = nil
	for _, ln := range r.cfg.Forwarding.Listeners {
		switch ln.Protocol {
		case "tcp":
			tcpListener := forwarding.NewTCPListener(ln, r.cfg.Forwarding.Limits, r.cfg.Forwarding.IdleTimeout.TCP.Duration(), r.manager, r.metrics, r.status, r.firewall, r.iplogPipeline, r.logger)
			if err := tcpListener.Start(r.ctx, &r.wg); err != nil {
				return err
			}
			r.listeners = append(r.listeners, tcpListener)
		case "udp":
			udpListener := forwarding.NewUDPListener(ln, r.cfg.Forwarding.Limits, r.cfg.Forwarding.IdleTimeout.UDP.Duration(), r.manager, r.metrics, r.status, r.firewall, r.iplogPipeline, r.logger)
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
	dnsLogger := util.ComponentLogger(r.logger, util.CompDNS)
	shapingLogger := util.ComponentLogger(r.logger, util.CompShaping)
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
						util.Event(dnsLogger, slog.LevelWarn, "dns.resolve_failed",
							"upstream", up.Tag,
							"dns.host", up.Host,
							"error", err,
						)
						continue
					}
					changed := r.manager.UpdateResolved(up.Tag, ips)
					if changed {
						active := up.ActiveIP()
						activeStr := ""
						if active != nil {
							activeStr = active.String()
						}
						resolved := make([]string, 0, len(ips))
						for _, ip := range ips {
							resolved = append(resolved, ip.String())
						}
						util.Event(dnsLogger, slog.LevelInfo, "dns.resolve_changed",
							"upstream", up.Tag,
							"dns.host", up.Host,
							"upstream.ip", activeStr,
							"dns.resolved_ips", resolved,
						)
						if r.shaper != nil && upstreamHasShaping(r.cfg.Upstreams, up.Tag) {
							upstreamShaping := buildUpstreamShapingEntries(r.cfg.Upstreams, r.upstreams)
							if err := r.shaper.UpdateUpstreams(upstreamShaping); err != nil {
								util.Event(shapingLogger, slog.LevelError, "shaping.reapply_failed",
									"upstream", up.Tag,
									"error", err,
								)
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
