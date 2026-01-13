package app

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/control"
	"github.com/NodePath81/fbforward/internal/forwarding"
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
	wg        sync.WaitGroup
}

type closer interface {
	Close() error
}

func NewRuntime(cfg config.Config, logger util.Logger, restartFn func() error) (*Runtime, error) {
	ctx, cancel := context.WithCancel(context.Background())
	resolver := resolver.NewResolver(cfg.Resolver)
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
	statusHub := control.NewStatusHub(ctx.Done())
	status := control.NewStatusStore(statusHub, metrics)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	manager := upstream.NewUpstreamManager(upstreams, rng)
	manager.SetSwitching(cfg.Switching)

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
		rt.shaper = shaping.NewTrafficShaper(cfg.Shaping, cfg.Listeners, upstreamShaping, logger)
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
		if usable {
			logger.Info("upstream marked usable", "upstream", tag)
			return
		}
		logger.Info("upstream marked unusable", "upstream", tag)
		status.CloseByUpstream(tag)
	})

	metrics.SetMode(upstream.ModeAuto)
	manager.SetAuto()
	metrics.Start(ctx.Done())

	ctrl := control.NewControlServer(cfg.Control, cfg.WebUI.IsEnabled(), cfg.Hostname, manager, metrics, status, restartFn, logger)
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

	r.startProbes()
	r.startDNSRefresh()

	delay := r.cfg.Probe.DiscoveryDelay.Duration()
	if delay > 0 {
		r.logger.Info("waiting for discovery delay", "delay", delay)
		select {
		case <-time.After(delay):
		case <-r.ctx.Done():
			return errors.New("runtime stopped during discovery delay")
		}
	}
	r.manager.PickInitial()

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
			probe.ProbeLoop(r.ctx, u, r.cfg.Probe, r.cfg.Scoring, r.manager, r.metrics, r.logger)
		}(up)
	}
}

func (r *Runtime) startListeners() error {
	r.listeners = nil
	for _, ln := range r.cfg.Listeners {
		switch ln.Protocol {
		case "tcp":
			tcpListener := forwarding.NewTCPListener(ln, r.cfg.Limits, time.Duration(r.cfg.Timeouts.TCPIdleSeconds)*time.Second, r.manager, r.metrics, r.status, r.logger)
			if err := tcpListener.Start(r.ctx, &r.wg); err != nil {
				return err
			}
			r.listeners = append(r.listeners, tcpListener)
		case "udp":
			udpListener := forwarding.NewUDPListener(ln, r.cfg.Limits, time.Duration(r.cfg.Timeouts.UDPIdleSeconds)*time.Second, r.manager, r.metrics, r.status, r.logger)
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
		ips, err := res.ResolveHost(ctx, item.Host)
		if err != nil {
			return nil, err
		}
		up := &upstream.Upstream{
			Tag:  item.Tag,
			Host: item.Host,
			IPs:  ips,
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
		if cfgUp.Ingress == nil && cfgUp.Egress == nil {
			continue
		}
		ips, ok := tagToIPs[cfgUp.Tag]
		if !ok || len(ips) == 0 {
			continue
		}
		entries = append(entries, shaping.UpstreamShapingEntry{
			Tag:     cfgUp.Tag,
			IPs:     ips,
			Ingress: cfgUp.Ingress,
			Egress:  cfgUp.Egress,
		})
	}
	return entries
}

func upstreamHasShaping(cfgUpstreams []config.UpstreamConfig, tag string) bool {
	for _, up := range cfgUpstreams {
		if up.Tag != tag {
			continue
		}
		return up.Ingress != nil || up.Egress != nil
	}
	return false
}
