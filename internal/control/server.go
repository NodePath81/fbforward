package control

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flowcontext"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/measure"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/notify"
	"github.com/NodePath81/fbforward/internal/policy"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/NodePath81/fbforward/internal/version"
	"github.com/NodePath81/fbforward/web"
)

const (
	maxRPCBodyBytes  = 1 << 20
	rpcRatePerSecond = 5
	rpcRateBurst     = 10
	rpcRateTTL       = 5 * time.Minute
)

type ControlServer struct {
	fullCfg     config.Config
	cfg         config.ControlConfig
	measurement config.MeasurementConfig
	hostname    string
	manager     upstream.UpstreamStateReader
	routes      routeStateReader
	metrics     *metrics.Metrics
	status      *StatusStore
	restartFn   func() error
	logger      util.Logger
	server      *http.Server
	limiter     *rateLimiter
	notifierMu  sync.RWMutex
	notifier    notify.Emitter
	schedulerMu sync.RWMutex
	scheduler   *measure.Scheduler
	collectorMu sync.RWMutex
	collector   *measure.Collector
	geoipMu     sync.RWMutex
	geoipMgr    geoipManager
	auditMu     sync.RWMutex
	auditStore  *audit.Store
	firewallMu  sync.RWMutex
	firewall    *policy.Provider
	onlineMu    sync.RWMutex
	online      *policy.OnlineProvider
	flowContext *flowcontext.Service
	nextReqID   uint64
	rpcs        *rpcRegistry
}

type geoipManager interface {
	Status() geoip.Status
}

type geoipReloader interface {
	Reload(context.Context) error
}

func NewControlServer(cfg config.Config, manager upstream.UpstreamStateReader, metrics *metrics.Metrics, status *StatusStore, restartFn func() error, logger util.Logger) *ControlServer {
	c := &ControlServer{
		fullCfg:     cfg,
		cfg:         cfg.Control,
		measurement: cfg.Measurement,
		hostname:    cfg.Hostname,
		manager:     manager,
		metrics:     metrics,
		status:      status,
		restartFn:   restartFn,
		logger:      util.ComponentLogger(logger, util.CompControl),
		limiter:     newRateLimiter(rpcRatePerSecond, rpcRateBurst, rpcRateTTL),
	}
	c.rpcs = newRPCRegistry()
	c.registerRPCHandlers()
	return c
}

func (c *ControlServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	if c.cfg.Metrics.IsEnabled() {
		mux.HandleFunc("/metrics", c.handleMetrics)
	}
	if c.flowContext != nil {
		mux.HandleFunc("/flow-context/resolve", c.flowContext.HandleResolve)
		mux.HandleFunc("/flow-context/rpc", c.flowContext.HandleRPC)
	}
	mux.HandleFunc("/rpc", c.handleRPC)
	mux.HandleFunc("/identity", c.handleIdentity)
	mux.Handle("/", web.Handler())

	addr := util.NetJoin(c.cfg.BindAddr, c.cfg.BindPort)
	c.server = &http.Server{
		Addr:    addr,
		Handler: c.httpMiddleware(mux),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = c.server.Shutdown(shutdownCtx)
	}()
	go c.limiter.RunCleanup(ctx.Done(), rpcRateTTL)

	go func() {
		_ = c.server.ListenAndServe()
	}()
	util.Event(c.logger, slog.LevelInfo, "control.server_started", "listen.addr", addr)
	return nil
}

func (c *ControlServer) Shutdown(ctx context.Context) error {
	if c.server == nil {
		return nil
	}
	return c.server.Shutdown(ctx)
}

func (c *ControlServer) SetScheduler(scheduler *measure.Scheduler) {
	c.schedulerMu.Lock()
	defer c.schedulerMu.Unlock()
	c.scheduler = scheduler
}

func (c *ControlServer) SetNotifier(notifier notify.Emitter) {
	c.notifierMu.Lock()
	defer c.notifierMu.Unlock()
	c.notifier = notifier
}

func (c *ControlServer) SetCollector(collector *measure.Collector) {
	c.collectorMu.Lock()
	defer c.collectorMu.Unlock()
	c.collector = collector
}

func (c *ControlServer) SetGeoIPManager(manager geoipManager) {
	c.geoipMu.Lock()
	defer c.geoipMu.Unlock()
	c.geoipMgr = manager
}

func (c *ControlServer) SetAuditStore(store *audit.Store) {
	c.auditMu.Lock()
	defer c.auditMu.Unlock()
	c.auditStore = store
}

func (c *ControlServer) SetFirewallProvider(provider *policy.Provider) {
	c.firewallMu.Lock()
	defer c.firewallMu.Unlock()
	c.firewall = provider
}

func (c *ControlServer) firewallProvider() *policy.Provider {
	c.firewallMu.RLock()
	defer c.firewallMu.RUnlock()
	return c.firewall
}

func (c *ControlServer) SetOnlinePolicyProvider(provider *policy.OnlineProvider) {
	c.onlineMu.Lock()
	defer c.onlineMu.Unlock()
	c.online = provider
}

func (c *ControlServer) onlinePolicyProvider() *policy.OnlineProvider {
	c.onlineMu.RLock()
	defer c.onlineMu.RUnlock()
	return c.online
}

// SetFlowContextService installs the backend Flow Context HTTP API. It is
// deliberately separate from the control RPC registry because it has its own
// identity tokens and route/upstream authorization.
func (c *ControlServer) SetFlowContextService(service *flowcontext.Service) {
	c.flowContext = service
}

func (c *ControlServer) SetRouteStateReader(routes routeStateReader) {
	c.routes = routes
}

type identityResponse struct {
	Hostname string   `json:"hostname"`
	IPs      []string `json:"ips"`
	Version  string   `json:"version"`
}

func (c *ControlServer) handleIdentity(w http.ResponseWriter, r *http.Request) {
	authOK := c.checkAuth(r)
	reqCtx := c.newRequestCtx(r, "http", authOK)
	sw := &statusWriter{ResponseWriter: w}
	completionErr := ""
	defer c.auditCompletion(sw, reqCtx, &completionErr, "control.identity.request_completed")
	c.auditPolicy(reqCtx, "control.identity.access_policy_decision")

	if !authOK {
		completionErr = "unauthorized"
		c.auditFailure(reqCtx, "control.identity.auth_failed", completionErr, http.StatusUnauthorized, "denied")
		writeAPIError(sw, http.StatusUnauthorized, completionErr)
		return
	}
	if !c.limiter.Allow(reqCtx.clientIP) {
		completionErr = "rate limit exceeded"
		writeAPIError(sw, http.StatusTooManyRequests, completionErr)
		return
	}
	if r.Method != http.MethodGet {
		completionErr = "method not allowed"
		writeAPIError(sw, http.StatusMethodNotAllowed, completionErr)
		return
	}
	name := strings.TrimSpace(c.hostname)
	if name == "" {
		name, _ = os.Hostname()
	}
	writeJSON(sw, http.StatusOK, rpcResponse{Ok: true, Result: identityResponse{
		Hostname: name,
		IPs:      listActiveIPs(),
		Version:  version.Version,
	}})
}

func listActiveIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	addrs := collectIPs(ifaces, func(iface net.Interface) bool {
		return iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0
	})
	if len(addrs) > 0 {
		return addrs
	}
	return collectIPs(ifaces, func(iface net.Interface) bool {
		return iface.Flags&net.FlagLoopback == 0
	})
}

func collectIPs(ifaces []net.Interface, filter func(net.Interface) bool) []string {
	ips := make([]string, 0)
	for _, iface := range ifaces {
		if !filter(iface) {
			continue
		}
		addrList, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrList {
			ip := addrToIP(addr)
			if ip != "" {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

func addrToIP(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.IPNet:
		if v.IP != nil {
			return v.IP.String()
		}
	case *net.IPAddr:
		if v.IP != nil {
			return v.IP.String()
		}
	}
	return ""
}

func (c *ControlServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	authOK := c.checkAuth(r)
	reqCtx := c.newRequestCtx(r, "http", authOK)
	sw := &statusWriter{ResponseWriter: w}
	completionErr := ""
	defer c.auditCompletion(sw, reqCtx, &completionErr, "control.metrics.request_completed")
	c.auditPolicy(reqCtx, "control.metrics.access_policy_decision")

	if !authOK {
		completionErr = "unauthorized"
		c.auditFailure(reqCtx, "control.metrics.auth_failed", completionErr, http.StatusUnauthorized, "denied")
		writeAPIError(sw, http.StatusUnauthorized, completionErr)
		return
	}
	if !c.limiter.Allow(reqCtx.clientIP) {
		completionErr = "rate limit exceeded"
		writeAPIError(sw, http.StatusTooManyRequests, completionErr)
		return
	}
	if c.metrics == nil {
		completionErr = "metrics not available"
		writeAPIError(sw, http.StatusServiceUnavailable, completionErr)
		return
	}
	c.metrics.Handler(sw, r)
}
