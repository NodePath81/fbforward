package app

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/control"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/flowcontext"
	"github.com/NodePath81/fbforward/internal/forwarding"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/measure"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/notify"
	"github.com/NodePath81/fbforward/internal/policy"
	"github.com/NodePath81/fbforward/internal/resolver"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

const dnsRefreshInterval = 30 * time.Second

type Runtime struct {
	cfg                config.Config
	ctx                context.Context
	cancel             context.CancelFunc
	logger             util.Logger
	resolver           *resolver.Resolver
	manager            *upstream.UpstreamManager
	metrics            *metrics.Metrics
	status             *control.StatusStore
	flowObserver       forwarding.FlowObserver
	flowRegistry       *flow.Registry
	flowContext        *flowcontext.Registry
	flowContextService *flowcontext.Service
	picker             forwarding.UpstreamPicker
	policy             forwarding.AdmissionPolicy
	control            *control.ControlServer
	geoipMgr           *geoip.Manager
	auditStore         *audit.Store
	auditPipeline      *audit.Pipeline
	firewall           *policy.Provider
	onlinePolicy       *policy.OnlineProvider
	upstreams          []*upstream.Upstream
	listeners          []closer
	collector          *measure.Collector
	notifier           *notify.Client
	notifyPolicy       *notify.Policy
	wg                 sync.WaitGroup
}

type closer interface {
	Close() error
}

type auditContextSink struct {
	pipeline *audit.Pipeline
}

func (s auditContextSink) Publish(value flowcontext.Context) {
	if s.pipeline != nil {
		s.pipeline.PublishEntity(value.AuditEntity())
	}
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
	metricSet := metrics.NewMetrics(tags)
	status := control.NewStatusStore()
	flowRegistry := flow.NewRegistry()
	flowContextRegistry := flowcontext.NewRegistry(flowcontext.DefaultOptions())
	initialized := false
	defer func() {
		if !initialized {
			_ = flowContextRegistry.Shutdown()
		}
	}()
	flowObservers := flow.MultiObserver{status, metrics.NewFlowObserver(metricSet), flowContextRegistry}
	manager := upstream.NewUpstreamManager(upstreams, util.ComponentLogger(logger, util.CompUpstream))
	manager.SetHealthConfig(cfg.Health)

	rt := &Runtime{
		cfg:          cfg,
		ctx:          ctx,
		cancel:       cancel,
		logger:       logger,
		resolver:     resolver,
		manager:      manager,
		metrics:      metricSet,
		status:       status,
		flowRegistry: flowRegistry,
		flowContext:  flowContextRegistry,
		picker:       newUpstreamPicker(manager, cfg.Routes),
		upstreams:    upstreams,
	}
	if picker, ok := rt.picker.(*upstreamPicker); ok {
		picker.metrics = metricSet
	}
	if cfg.GeoIP.Enabled {
		geoMgr, err := geoip.NewManager(cfg.GeoIP, logger)
		if err != nil {
			cancel()
			return nil, err
		}
		rt.geoipMgr = geoMgr
	}
	fw, err := policy.NewProvider(cfg.Firewall, rt.geoipMgr, metricSet, logger)
	if err != nil {
		cancel()
		if rt.geoipMgr != nil {
			_ = rt.geoipMgr.Close()
		}
		return nil, err
	}
	rt.firewall = fw
	if cfg.IPLog.Enabled {
		store, err := audit.NewStore(cfg.IPLog.DBPath)
		if err != nil {
			cancel()
			return nil, err
		}
		rt.auditStore = store
		rt.auditStore.StartRetention(ctx, cfg.IPLog.Retention.Duration(), cfg.IPLog.PruneInterval.Duration())
		rt.auditPipeline = audit.NewPipeline(cfg.IPLog, rt.geoipMgr, store, metricSet, logger)
		flowObservers = append(flowObservers, rt.auditPipeline)
		flowContextRegistry.SetSnapshotSink(auditContextSink{pipeline: rt.auditPipeline})
		onlinePolicy, onlineErr := policy.NewOnlineProvider(rt.auditStore, policy.OnlineProviderOptions{
			UpstreamAvailable: func(tag string) bool { return manager.Get(tag) != nil },
			Logger:            util.ComponentLogger(logger, util.CompControl),
			Telemetry:         metricSet,
		})
		if onlineErr != nil {
			cancel()
			_ = rt.auditStore.Close()
			return nil, onlineErr
		}
		rt.onlinePolicy = onlinePolicy
	}
	if cfg.FlowContext.Enabled {
		identities := make([]flowcontext.Identity, 0, len(cfg.FlowContext.Identities))
		for _, identity := range cfg.FlowContext.Identities {
			identities = append(identities, flowcontext.Identity{
				ID: identity.ID, Token: identity.Token,
				Routes:     append([]string(nil), identity.Routes...),
				Upstreams:  append([]string(nil), identity.Upstreams...),
				Namespaces: append([]string(nil), identity.Namespaces...),
			})
		}
		rt.flowContextService = flowcontext.NewService(flowContextRegistry, rt.auditStore, flowcontext.HTTPOptions{
			Identities: identities,
			MaxTTL:     cfg.FlowContext.MaxTTL.Duration(),
		}, logger)
		rt.flowContextService.SetFlowController(flowRegistry)
	}
	rt.flowObserver = flowObservers
	rt.policy = &firewallPolicy{provider: rt.firewall, onlineProvider: rt.onlinePolicy}
	if cfg.Notify.Enabled {
		notifyLogger := util.ComponentLogger(logger, util.CompNotify)
		notifier, err := notify.NewClient(notify.Config{
			Endpoint:       cfg.Notify.Endpoint,
			BearerToken:    cfg.Notify.BearerToken,
			SourceInstance: cfg.Notify.SourceInstance,
			Timeout:        cfg.Notify.Timeout.Duration(),
			Logger:         notifyLogger,
			Telemetry:      metricSet,
		})
		if err != nil {
			cancel()
			return nil, err
		}
		rt.notifier = notifier
		rt.notifyPolicy = notify.NewPolicy(notifier, notify.PolicyConfig{
			StartTime:          time.Now(),
			StartupGracePeriod: cfg.Notify.StartupGracePeriod.Duration(),
			UnusableInterval:   cfg.Notify.UnusableInterval.Duration(),
			NotifyInterval:     cfg.Notify.NotifyInterval.Duration(),
		})
	}

	manager.SetCallbacks(nil, func(change upstream.UsabilityChange) {
		if rt.notifyPolicy != nil {
			rt.notifyPolicy.HandleUsabilityChange(change.Tag, change.Usable, change.Reason)
		}
	})

	manager.SetAuto()

	ctrl := control.NewControlServer(cfg, manager, metricSet, status, restartFn, logger)
	if rt.notifier != nil {
		ctrl.SetNotifier(rt.notifier)
	}
	if rt.geoipMgr != nil {
		ctrl.SetGeoIPManager(rt.geoipMgr)
	}
	if rt.auditStore != nil {
		ctrl.SetAuditStore(rt.auditStore)
	}
	ctrl.SetFirewallProvider(rt.firewall)
	ctrl.SetOnlinePolicyProvider(rt.onlinePolicy)
	ctrl.SetFlowContextService(rt.flowContextService)
	if picker, ok := rt.picker.(*upstreamPicker); ok {
		ctrl.SetRouteStateReader(picker)
	}
	rt.control = ctrl

	initialized = true
	return rt, nil
}

func (r *Runtime) Start() error {
	if err := r.control.Start(r.ctx); err != nil {
		return err
	}
	if r.geoipMgr != nil {
		r.geoipMgr.Start(r.ctx)
	}
	if r.auditPipeline != nil {
		r.auditPipeline.Start()
	}
	if r.onlinePolicy != nil {
		r.onlinePolicy.Start(r.ctx.Done())
	}

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
	if r.control != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = r.control.Shutdown(ctx)
		cancel()
	}
	if r.flowRegistry != nil {
		r.flowRegistry.CloseAll()
	}
	for _, ln := range r.listeners {
		_ = ln.Close()
	}
	// Drain forwarding handlers before shutting down the observers and stores
	// they may still be using during their final close transition.
	r.wait()
	if r.flowContext != nil {
		if err := r.flowContext.Shutdown(); err != nil {
			util.Event(r.logger, slog.LevelWarn, "flowcontext.shutdown_failed", "error", err)
		}
	}
	if r.auditPipeline != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.auditPipeline.Shutdown(ctx); err != nil {
			util.Event(r.logger, slog.LevelWarn, "audit.shutdown_failed", "error", err)
		}
		cancel()
	}
	if r.geoipMgr != nil {
		if err := r.geoipMgr.Close(); err != nil {
			util.Event(r.logger, slog.LevelWarn, "geoip.close_failed", "error", err)
		}
	}
	if r.auditStore != nil {
		if err := r.auditStore.Close(); err != nil {
			util.Event(r.logger, slog.LevelWarn, "audit.store_close_failed", "error", err)
		}
	}
	if r.notifyPolicy != nil {
		r.notifyPolicy.Close()
	}
	if r.notifier != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.notifier.Close(ctx); err != nil {
			util.Event(r.logger, slog.LevelWarn, "notify.shutdown_failed", "error", err)
		}
		cancel()
	}
}

func (r *Runtime) startMeasurement() {
	measurementUpstreams := r.measurementUpstreams()
	if len(measurementUpstreams) == 0 {
		return
	}
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
	}, measurementUpstreams, nil)
	if r.control != nil {
		r.control.SetScheduler(scheduler)
	}

	r.collector = measure.NewCollector(r.cfg.Measurement, r.manager, r.metrics, scheduler, measureLogger)
	if r.control != nil {
		r.control.SetCollector(r.collector)
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.collector.RunLoop(r.ctx)
	}()
}

func (r *Runtime) measurementUpstreams() []*upstream.Upstream {
	needed := make(map[string]struct{})
	for _, route := range r.cfg.Routes {
		if route.Strategy != "adaptive" {
			continue
		}
		for _, tag := range route.Upstreams {
			needed[tag] = struct{}{}
		}
	}
	if len(needed) == 0 {
		return nil
	}
	result := make([]*upstream.Upstream, 0, len(needed))
	for _, up := range r.upstreams {
		if _, ok := needed[up.Tag]; ok {
			result = append(result, up)
		}
	}
	return result
}

func (r *Runtime) startListeners() error {
	r.listeners = nil
	for _, ln := range r.cfg.Forwarding.Listeners {
		switch ln.Protocol {
		case "tcp":
			tcpListener := forwarding.NewTCPListener(ln, r.cfg.Forwarding.Limits, r.cfg.Forwarding.IdleTimeout.TCP.Duration(), r.picker, r.policy, r.flowObserver, r.flowRegistry, r.flowContext, r.logger)
			if err := tcpListener.Start(r.ctx, &r.wg); err != nil {
				return err
			}
			r.listeners = append(r.listeners, tcpListener)
		case "udp":
			udpListener := forwarding.NewUDPListener(ln, r.cfg.Forwarding.Limits, r.cfg.Forwarding.IdleTimeout.UDP.Duration(), r.picker, r.policy, r.flowObserver, r.flowRegistry, r.flowContext, r.logger)
			udpListener.SetRateLimitDropRecorder(r.metrics)
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
			IPs:         ips,
		}
		up.SetActiveIP(ips[0])
		upstreams = append(upstreams, up)
	}
	return upstreams, nil
}
