package forwarding

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/util"
)

type UDPListener struct {
	cfg          config.ListenerConfig
	picker       UpstreamPicker
	policy       AdmissionPolicy
	timeout      time.Duration
	observer     FlowObserver
	registry     *flow.Registry
	binder       BackendBinder
	dropRecorder RateLimitDropRecorder
	sem          chan struct{}
	logger       util.Logger

	conn     *net.UDPConn
	mu       sync.Mutex
	mappings map[string]*udpMapping
	pending  map[string]*udpMappingReservation
	ipCounts map[string]int
	maxPerIP int

	dropMu            sync.Mutex
	lastDropLogTime   time.Time
	dropsSinceLastLog int64
}

const udpDialFailureCooldown = 5 * time.Second

const (
	udpPacketQueueSize  = 1024
	udpMaxMappingsPerIP = 50
)

var errUDPUpstreamSelection = errors.New("udp upstream selection failed")
var errUDPMappingLimit = errors.New("udp mapping limit reached")
var errUDPPerIPLimit = errors.New("udp per-ip mapping limit reached")
var errUDPRateLimited = errors.New("udp packet rate limited")

var udpPacketPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 65535)
		return &buf
	},
}

func NewUDPListener(cfg config.ListenerConfig, limits config.ForwardingLimitsConfig, timeout time.Duration, picker UpstreamPicker, policy AdmissionPolicy, observer FlowObserver, registry *flow.Registry, binder BackendBinder, logger util.Logger) *UDPListener {
	return &UDPListener{
		cfg:      cfg,
		picker:   picker,
		policy:   policy,
		timeout:  timeout,
		observer: observer,
		registry: registry,
		binder:   binder,
		sem:      make(chan struct{}, limits.MaxUDPMappings),
		logger:   util.ComponentLogger(logger, util.CompForwardUDP),
		mappings: make(map[string]*udpMapping),
		pending:  make(map[string]*udpMappingReservation),
		ipCounts: make(map[string]int),
		maxPerIP: udpMaxMappingsPerIP,
	}
}

func (l *UDPListener) SetRateLimitDropRecorder(recorder RateLimitDropRecorder) {
	l.dropRecorder = recorder
}

func (l *UDPListener) Start(ctx context.Context, wg *sync.WaitGroup) error {
	addr := net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort))
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	l.conn = conn
	util.Event(l.logger, slog.LevelInfo, "forward.udp.listener_started", "listen.addr", addr)

	packetCh := make(chan udpPacket, udpPacketQueueSize)
	workerCount := runtime.GOMAXPROCS(0)
	if workerCount < 1 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case pkt, ok := <-packetCh:
					if !ok {
						return
					}
					l.handlePacket(ctx, pkt.addr, pkt.data)
					udpPacketPool.Put(pkt.bufPtr)
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(packetCh)
		for {
			bufPtr := udpPacketPool.Get().(*[]byte)
			buf := *bufPtr
			n, clientAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				udpPacketPool.Put(bufPtr)
				if errors.Is(err, net.ErrClosed) {
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
					util.Event(l.logger, slog.LevelError, "forward.udp.read_error", "error", err)
					continue
				}
			}
			payload := buf[:n]
			select {
			case packetCh <- udpPacket{addr: clientAddr, data: payload, bufPtr: bufPtr}:
			default:
				udpPacketPool.Put(bufPtr)
				l.noteQueueDrop(len(packetCh))
			}
		}
	}()
	return nil
}

func (l *UDPListener) Close() error {
	if l.conn != nil {
		err := l.conn.Close()
		util.Event(l.logger, slog.LevelInfo, "forward.udp.listener_stopped",
			"listen.addr", net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort)),
		)
		return err
	}
	return nil
}

func (l *UDPListener) handlePacket(ctx context.Context, clientAddr *net.UDPAddr, payload []byte) {
	key := clientAddr.String()
	clientIP := clientAddr.IP.String()
	if mapping := l.lookupMapping(key); mapping != nil {
		if err := mapping.forwardToUpstream(payload); err != nil && !errors.Is(err, errUDPRateLimited) {
			mapping.closeWithReason("upstream_write_error")
		}
		return
	}
	candidate, err := newCandidateMeta(flow.ProtocolUDP, key, l.listenAddr(), l.cfg.Route)
	if err != nil {
		return
	}
	decision := Decision{Allowed: true}
	if l.policy != nil {
		decision = l.policy.Decide(candidate)
		if !decision.Allowed {
			emitRejection(l.observer, flow.ProtocolUDP, l.listenAddr(), key, "firewall_deny", decision)
			return
		}
	}
	mapping, reservation, err := l.getOrReserveMapping(key, clientIP)
	if err != nil {
		switch {
		case errors.Is(err, errUDPPerIPLimit):
			emitRejection(l.observer, flow.ProtocolUDP, l.listenAddr(), key, "udp_per_ip_mapping_limit", Decision{})
			util.Event(l.logger, slog.LevelWarn, "forward.udp.mapping_per_ip_limit_reached",
				"client.addr", key,
				"client.ip", clientIP,
			)
		case errors.Is(err, errUDPMappingLimit):
			emitRejection(l.observer, flow.ProtocolUDP, l.listenAddr(), key, "udp_mapping_limit", Decision{})
			util.Event(l.logger, slog.LevelWarn, "forward.udp.mapping_limit_reached", "client.addr", key)
		case errors.Is(err, errUDPUpstreamSelection):
			util.Event(l.logger, slog.LevelWarn, "forward.udp.upstream_selection_failed", "error", err)
		}
		return
	}
	if reservation != nil {
		mapping, err = l.buildMapping(clientAddr, candidate, decision)
		l.finishReservation(key, clientIP, reservation, mapping, err)
		if err != nil {
			if errors.Is(err, errUDPUpstreamSelection) {
				util.Event(l.logger, slog.LevelWarn, "forward.udp.upstream_selection_failed", "error", err)
			}
			return
		}
		l.activateMapping(ctx, mapping)
	}
	if err := mapping.forwardToUpstream(payload); err != nil && !errors.Is(err, errUDPRateLimited) {
		mapping.closeWithReason("upstream_write_error")
	}
}

func (l *UDPListener) lookupMapping(key string) *udpMapping {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mappings[key]
}

func (l *UDPListener) getOrReserveMapping(key, clientIP string) (*udpMapping, *udpMappingReservation, error) {
	for {
		l.mu.Lock()
		if mapping := l.mappings[key]; mapping != nil {
			l.mu.Unlock()
			return mapping, nil, nil
		}
		if pending := l.pending[key]; pending != nil {
			l.mu.Unlock()
			<-pending.ready
			if pending.err != nil {
				return nil, nil, pending.err
			}
			return pending.mapping, nil, nil
		}
		if clientIP != "" && l.ipCounts[clientIP] >= l.maxPerIP {
			l.mu.Unlock()
			return nil, nil, errUDPPerIPLimit
		}
		select {
		case l.sem <- struct{}{}:
		default:
			l.mu.Unlock()
			return nil, nil, errUDPMappingLimit
		}
		pending := &udpMappingReservation{ready: make(chan struct{})}
		l.pending[key] = pending
		if clientIP != "" {
			l.ipCounts[clientIP]++
		}
		l.mu.Unlock()
		return nil, pending, nil
	}
}

func (l *UDPListener) buildMapping(clientAddr *net.UDPAddr, candidate flow.Meta, decisions ...Decision) (*udpMapping, error) {
	decision := Decision{Allowed: true}
	if len(decisions) > 0 {
		decision = decisions[0]
	}
	if l.picker == nil {
		return nil, errors.New("upstream picker is unavailable")
	}
	var selected Upstream
	var err error
	if decision.UpstreamOverride != "" {
		picker, ok := l.picker.(OverridePicker)
		if !ok {
			return nil, errors.New("upstream override is not supported")
		}
		selected, err = picker.PickOverride(candidate, decision.UpstreamOverride)
	} else {
		selected, err = l.picker.Pick(candidate)
	}
	if err != nil {
		return nil, errors.Join(errUDPUpstreamSelection, err)
	}
	if !selected.Addr.IsValid() {
		return nil, errors.Join(errUDPUpstreamSelection, errors.New("upstream has no resolved IP"))
	}
	upstreamIP := selected.Addr.String()
	util.Event(l.logger, slog.LevelDebug, "forward.udp.upstream_selected",
		"upstream", selected.Tag,
		"upstream.ip", upstreamIP,
	)
	upAddr := &net.UDPAddr{IP: net.ParseIP(upstreamIP), Port: l.cfg.BindPort}
	upConn, err := net.DialUDP("udp", nil, upAddr)
	if err != nil {
		util.Event(l.logger, slog.LevelWarn, "forward.udp.dial_failed",
			"upstream", selected.Tag,
			"error", err,
			"result", "failed",
		)
		if feedback, ok := l.picker.(DialFeedback); ok {
			feedback.MarkDialFailure(selected, udpDialFailureCooldown)
		}
		return nil, err
	}
	if feedback, ok := l.picker.(DialFeedback); ok {
		feedback.ClearDialFailure(selected)
	}
	clientAddrStr := clientAddr.String()
	listenAddr := net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort))
	mapping := &udpMapping{
		parent:        l,
		clientAddr:    clientAddr,
		clientAddrStr: clientAddrStr,
		clientIP:      clientIPFromAddr(clientAddrStr),
		upstreamTag:   selected.Tag,
		upstreamConn:  upConn,
		timeout:       l.timeout,
		logger:        l.logger,
		observer:      l.observer,
		registry:      l.registry,
		binder:        l.binder,
		rateLimiter:   newByteRateLimiter(decision.RateLimitBPS),
		activityCh:    make(chan struct{}, 1),
		done:          make(chan struct{}),
		created:       candidate.StartedAt,
		upstreamIP:    upstreamIP,
		upstreamAddr:  upAddr.String(),
		listenAddr:    listenAddr,
		route:         l.cfg.Route,
	}
	clientEndpoint, err := netip.ParseAddrPort(clientAddrStr)
	if err != nil {
		_ = upConn.Close()
		return nil, err
	}
	mapping.id, err = flow.NewID()
	if err != nil {
		_ = upConn.Close()
		return nil, err
	}
	mapping.lifecycle = flow.NewLifecycle(flow.Meta{
		ID:         mapping.id,
		Protocol:   flow.ProtocolUDP,
		ClientAddr: clientEndpoint,
		Listener:   listenAddr,
		Route:      mapping.route,
		Upstream:   selected.Tag,
		StartedAt:  candidate.StartedAt,
	}, mapping.observer, mapping.registry, mapping.close)
	mapping.lifecycle.Open()
	if mapping.registry != nil {
		mapping.registry.SetControls(mapping.id, flow.Controls{
			Block:      func() bool { return mapping.closeWithReason("backend_blocked") },
			SetLimit:   mapping.setRateLimit,
			ClearLimit: mapping.clearRateLimit,
		})
	}
	if l.binder != nil {
		if tuple, bindErr := backendTuple(flow.ProtocolUDP, mapping.upstreamTag, upConn.LocalAddr(), upConn.RemoteAddr()); bindErr != nil {
			util.Event(l.logger, slog.LevelWarn, "forward.udp.backend_bind_failed", "flow.id", mapping.id, "error", bindErr)
		} else if bindErr := l.binder.Bind(mapping.id, tuple); bindErr != nil {
			util.Event(l.logger, slog.LevelWarn, "forward.udp.backend_bind_failed", "flow.id", mapping.id, "error", bindErr)
		}
	}
	return mapping, nil
}

func (l *UDPListener) activateMapping(ctx context.Context, mapping *udpMapping) {
	util.Event(mapping.logger, slog.LevelInfo, "forward.udp.mapping_created",
		"flow.id", mapping.id,
		"request.protocol", "udp",
		"client.addr", mapping.clientAddrStr,
		"client.ip", mapping.clientIP,
		"listen.addr", mapping.listenAddr,
		"flow.listen_port", l.cfg.BindPort,
		"upstream", mapping.upstreamTag,
		"upstream.ip", mapping.upstreamIP,
		"upstream.addr", mapping.upstreamAddr,
		"result", "connected",
	)
	go mapping.readLoop(ctx)
	go mapping.idleWatcher(ctx)
}

func (l *UDPListener) finishReservation(key, clientIP string, pending *udpMappingReservation, mapping *udpMapping, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pending, key)
	if err != nil {
		if clientIP != "" {
			if l.ipCounts[clientIP] > 1 {
				l.ipCounts[clientIP]--
			} else {
				delete(l.ipCounts, clientIP)
			}
		}
		select {
		case <-l.sem:
		default:
		}
		pending.err = err
		close(pending.ready)
		return
	}
	l.mappings[key] = mapping
	pending.mapping = mapping
	close(pending.ready)
}

type udpMapping struct {
	parent        *UDPListener
	clientAddr    *net.UDPAddr
	clientAddrStr string
	clientIP      string
	upstreamTag   string
	upstreamConn  *net.UDPConn
	timeout       time.Duration
	logger        util.Logger
	observer      FlowObserver
	registry      *flow.Registry
	binder        BackendBinder
	rateLimiter   *byteRateLimiter
	created       time.Time
	upstreamIP    string
	upstreamAddr  string
	listenAddr    string
	route         string

	id         flow.ID
	closeOnce  sync.Once
	controlMu  sync.Mutex
	closed     bool
	activityCh chan struct{}
	done       chan struct{}
	lifecycle  *flow.Lifecycle
}

type udpMappingReservation struct {
	ready   chan struct{}
	mapping *udpMapping
	err     error
}

func (m *udpMapping) forwardToUpstream(payload []byte) error {
	if m.rateLimiter != nil && !m.rateLimiter.Try(len(payload)) {
		m.parent.recordRateLimitDrop(len(payload))
		util.Event(m.logger, slog.LevelDebug, "forward.udp.rate_limited", "flow.id", m.id, "bytes", len(payload), "result", "dropped")
		return errUDPRateLimited
	}
	if _, err := m.upstreamConn.Write(payload); err != nil {
		return err
	}
	n := uint64(len(payload))
	if m.lifecycle != nil {
		m.lifecycle.Add(n, 0, 1, 0)
	}
	m.touch()
	return nil
}

func (m *udpMapping) readLoop(ctx context.Context) {
	buf := make([]byte, 65535)
	for {
		_ = m.upstreamConn.SetReadDeadline(time.Now().Add(m.timeout))
		n, err := m.upstreamConn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					m.closeWithReason("context_done")
					return
				case <-m.done:
					return
				default:
					continue
				}
			}
			m.closeWithReason("upstream_read_error")
			return
		}
		if n > 0 {
			if m.rateLimiter != nil && !m.rateLimiter.Try(n) {
				m.parent.recordRateLimitDrop(n)
				util.Event(m.logger, slog.LevelDebug, "forward.udp.rate_limited", "flow.id", m.id, "bytes", n, "result", "dropped")
				continue
			}
			_, werr := m.parent.conn.WriteToUDP(buf[:n], m.clientAddr)
			if werr != nil {
				m.closeWithReason("client_write_error")
				return
			}
			down := uint64(n)
			if m.lifecycle != nil {
				m.lifecycle.Add(0, down, 0, 1)
			}
			m.touch()
		}
		select {
		case <-ctx.Done():
			m.closeWithReason("context_done")
			return
		case <-m.done:
			return
		default:
		}
	}
}

func (l *UDPListener) recordRateLimitDrop(bytes int) {
	if l != nil && l.dropRecorder != nil && bytes > 0 {
		l.dropRecorder.RecordRateLimitDrop(flow.ProtocolUDP, uint64(bytes))
	}
}

func (m *udpMapping) touch() {
	select {
	case m.activityCh <- struct{}{}:
	default:
	}
}

func (m *udpMapping) idleWatcher(ctx context.Context) {
	timer := time.NewTimer(m.timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			m.closeWithReason("context_done")
			return
		case <-m.done:
			return
		case <-timer.C:
			m.closeWithReason("idle_timeout")
			return
		case <-m.activityCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(m.timeout)
		}
	}
}

func (m *udpMapping) setRateLimit(rateBPS uint64) bool {
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	if m.closed {
		return false
	}
	m.rateLimiter.SetOverride(rateBPS)
	return true
}

func (m *udpMapping) clearRateLimit() bool {
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	if m.closed {
		return false
	}
	m.rateLimiter.ClearOverride()
	return true
}

func (m *udpMapping) closeWithReason(reason string) bool {
	m.controlMu.Lock()
	if m.closed {
		m.controlMu.Unlock()
		return false
	}
	m.closed = true
	m.controlMu.Unlock()
	closed := false
	m.closeOnce.Do(func() {
		closed = true
		close(m.done)
		_ = m.upstreamConn.Close()
		m.parent.removeMapping(m.clientAddr.String(), m.clientIP)
		durationMs := int64(0)
		if !m.created.IsZero() {
			durationMs = time.Since(m.created).Milliseconds()
		}
		counters := flow.Counters{}
		if m.lifecycle != nil {
			counters = m.lifecycle.Snapshot()
			m.lifecycle.Close(reason)
		}
		util.Event(m.logger, slog.LevelInfo, "forward.udp.mapping_closed",
			"flow.id", m.id,
			"request.protocol", "udp",
			"client.addr", m.clientAddrStr,
			"client.ip", m.clientIP,
			"upstream", m.upstreamTag,
			"upstream.ip", m.upstreamIP,
			"upstream.addr", m.upstreamAddr,
			"flow.close_reason", reason,
			"flow.bytes_up", counters.BytesUp,
			"flow.bytes_down", counters.BytesDown,
			"flow.duration_ms", durationMs,
			"result", "closed",
		)
		<-m.parent.sem
	})
	return closed
}

func (m *udpMapping) close() {
	m.closeWithReason("upstream_unusable")
}

func (l *UDPListener) removeMapping(key, clientIP string) {
	l.mu.Lock()
	delete(l.mappings, key)
	if clientIP != "" {
		if l.ipCounts[clientIP] > 1 {
			l.ipCounts[clientIP]--
		} else {
			delete(l.ipCounts, clientIP)
		}
	}
	l.mu.Unlock()
}

type udpPacket struct {
	addr   *net.UDPAddr
	data   []byte
	bufPtr *[]byte
}

func (l *UDPListener) noteQueueDrop(queueDepth int) {
	atomic.AddInt64(&l.dropsSinceLastLog, 1)
	now := time.Now()

	l.dropMu.Lock()
	if !l.lastDropLogTime.IsZero() && now.Sub(l.lastDropLogTime) < time.Second {
		l.dropMu.Unlock()
		return
	}
	l.lastDropLogTime = now
	drops := atomic.SwapInt64(&l.dropsSinceLastLog, 0)
	l.dropMu.Unlock()
	if drops <= 0 {
		return
	}
	util.Event(l.logger, slog.LevelWarn, "forward.udp.packet_dropped_queue_full",
		"queue.capacity", udpPacketQueueSize,
		"queue.depth", queueDepth,
		"result", "dropped",
	)
}

func (l *UDPListener) listenAddr() string {
	return net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort))
}
