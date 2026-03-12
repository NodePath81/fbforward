package forwarding

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/control"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

type UDPListener struct {
	cfg     config.ListenerConfig
	manager upstream.UpstreamSelector
	metrics *metrics.Metrics
	status  *control.StatusStore
	timeout time.Duration
	sem     chan struct{}
	logger  util.Logger

	conn     *net.UDPConn
	mu       sync.Mutex
	mappings map[string]*udpMapping

	dropMu            sync.Mutex
	lastDropLogTime   time.Time
	dropsSinceLastLog int64
}

const udpDialFailureCooldown = 5 * time.Second

const (
	udpPacketQueueSize = 1024
)

var errUDPUpstreamSelection = errors.New("udp upstream selection failed")

var udpPacketPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 65535)
		return &buf
	},
}

func NewUDPListener(cfg config.ListenerConfig, limits config.ForwardingLimitsConfig, timeout time.Duration, manager upstream.UpstreamSelector, metrics *metrics.Metrics, status *control.StatusStore, logger util.Logger) *UDPListener {
	return &UDPListener{
		cfg:      cfg,
		manager:  manager,
		metrics:  metrics,
		status:   status,
		timeout:  timeout,
		sem:      make(chan struct{}, limits.MaxUDPMappings),
		logger:   util.ComponentLogger(logger, util.CompForwardUDP),
		mappings: make(map[string]*udpMapping),
	}
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
	mapping := l.getMapping(key)
	if mapping == nil {
		select {
		case l.sem <- struct{}{}:
		default:
			util.Event(l.logger, slog.LevelWarn, "forward.udp.mapping_limit_reached", "client.addr", key)
			return
		}
		var err error
		mapping, err = l.createMapping(ctx, clientAddr)
		if err != nil {
			<-l.sem
			if errors.Is(err, errUDPUpstreamSelection) {
				util.Event(l.logger, slog.LevelWarn, "forward.udp.upstream_selection_failed", "error", err)
			}
			return
		}
	}
	if err := mapping.forwardToUpstream(payload); err != nil {
		mapping.closeWithReason("upstream_write_error")
	}
}

func (l *UDPListener) getMapping(key string) *udpMapping {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mappings[key]
}

func (l *UDPListener) createMapping(ctx context.Context, clientAddr *net.UDPAddr) (*udpMapping, error) {
	up, err := l.manager.SelectUpstream()
	if err != nil {
		return nil, errors.Join(errUDPUpstreamSelection, err)
	}
	ip := up.ActiveIP()
	if ip == nil {
		return nil, errors.Join(errUDPUpstreamSelection, errors.New("upstream has no resolved IP"))
	}
	upstreamIP := ip.String()
	util.Event(l.logger, slog.LevelDebug, "forward.udp.upstream_selected",
		"upstream", up.Tag,
		"upstream.ip", upstreamIP,
	)
	upAddr := &net.UDPAddr{IP: ip, Port: l.cfg.BindPort}
	upConn, err := net.DialUDP("udp", nil, upAddr)
	if err != nil {
		util.Event(l.logger, slog.LevelWarn, "forward.udp.dial_failed",
			"upstream", up.Tag,
			"error", err,
			"result", "failed",
		)
		l.manager.MarkDialFailure(up.Tag, udpDialFailureCooldown)
		return nil, err
	}
	l.manager.ClearDialFailure(up.Tag)
	clientAddrStr := clientAddr.String()
	listenAddr := net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort))
	mapping := &udpMapping{
		parent:        l,
		clientAddr:    clientAddr,
		clientAddrStr: clientAddrStr,
		clientIP:      clientIPFromAddr(clientAddrStr),
		upstreamTag:   up.Tag,
		upstreamConn:  upConn,
		timeout:       l.timeout,
		metrics:       l.metrics,
		status:        l.status,
		logger:        l.logger,
		activityCh:    make(chan struct{}, 1),
		done:          make(chan struct{}),
		created:       time.Now(),
		upstreamIP:    upstreamIP,
		upstreamAddr:  upAddr.String(),
		listenAddr:    listenAddr,
	}
	mapping.id = l.status.AddUDP(clientAddrStr, up.Tag, l.cfg.BindPort, mapping.close)
	l.mu.Lock()
	l.mappings[clientAddrStr] = mapping
	l.mu.Unlock()
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
	return mapping, nil
}

type udpMapping struct {
	parent        *UDPListener
	clientAddr    *net.UDPAddr
	clientAddrStr string
	clientIP      string
	upstreamTag   string
	upstreamConn  *net.UDPConn
	timeout       time.Duration
	metrics       *metrics.Metrics
	status        *control.StatusStore
	logger        util.Logger
	created       time.Time
	totalUp       uint64
	totalDown     uint64
	upstreamIP    string
	upstreamAddr  string
	listenAddr    string

	id         string
	closeOnce  sync.Once
	activityCh chan struct{}
	done       chan struct{}
}

func (m *udpMapping) forwardToUpstream(payload []byte) error {
	if _, err := m.upstreamConn.Write(payload); err != nil {
		return err
	}
	n := uint64(len(payload))
	atomic.AddUint64(&m.totalUp, n)
	m.metrics.AddBytesUp(m.upstreamTag, n, "udp")
	m.status.UpdateUDP(m.id, n, 0, 1, 0)
	m.touch()
	return nil
}

func (m *udpMapping) readLoop(ctx context.Context) {
	buf := make([]byte, 65535)
	for {
		n, err := m.upstreamConn.Read(buf)
		if err != nil {
			m.closeWithReason("upstream_read_error")
			return
		}
		if n > 0 {
			_, werr := m.parent.conn.WriteToUDP(buf[:n], m.clientAddr)
			if werr != nil {
				m.closeWithReason("client_write_error")
				return
			}
			down := uint64(n)
			atomic.AddUint64(&m.totalDown, down)
			m.metrics.AddBytesDown(m.upstreamTag, down, "udp")
			m.status.UpdateUDP(m.id, 0, down, 0, 1)
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

func (m *udpMapping) closeWithReason(reason string) {
	m.closeOnce.Do(func() {
		close(m.done)
		_ = m.upstreamConn.Close()
		m.parent.removeMapping(m.clientAddr.String())
		m.status.RemoveUDP(m.id)
		durationMs := int64(0)
		if !m.created.IsZero() {
			durationMs = time.Since(m.created).Milliseconds()
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
			"flow.bytes_up", atomic.LoadUint64(&m.totalUp),
			"flow.bytes_down", atomic.LoadUint64(&m.totalDown),
			"flow.duration_ms", durationMs,
			"result", "closed",
		)
		<-m.parent.sem
	})
}

func (m *udpMapping) close() {
	m.closeWithReason("upstream_unusable")
}

func (l *UDPListener) removeMapping(key string) {
	l.mu.Lock()
	delete(l.mappings, key)
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
