package forwarding

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/control"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

type UDPListener struct {
	cfg     config.ListenerConfig
	manager *upstream.UpstreamManager
	metrics *metrics.Metrics
	status  *control.StatusStore
	timeout time.Duration
	sem     chan struct{}
	logger  util.Logger

	conn     *net.UDPConn
	mu       sync.Mutex
	mappings map[string]*udpMapping
}

const udpDialFailureCooldown = 5 * time.Second

const (
	udpPacketQueueSize = 1024
)

var udpPacketPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 65535)
		return &buf
	},
}

func NewUDPListener(cfg config.ListenerConfig, limits config.LimitsConfig, timeout time.Duration, manager *upstream.UpstreamManager, metrics *metrics.Metrics, status *control.StatusStore, logger util.Logger) *UDPListener {
	return &UDPListener{
		cfg:      cfg,
		manager:  manager,
		metrics:  metrics,
		status:   status,
		timeout:  timeout,
		sem:      make(chan struct{}, limits.MaxUDPMappings),
		logger:   logger,
		mappings: make(map[string]*udpMapping),
	}
}

func (l *UDPListener) Start(ctx context.Context, wg *sync.WaitGroup) error {
	addr := net.JoinHostPort(l.cfg.Addr, util.FormatPort(l.cfg.Port))
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	l.conn = conn
	l.logger.Info("udp listener started", "addr", addr)

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
					l.logger.Error("udp read error", "error", err)
					continue
				}
			}
			payload := buf[:n]
			select {
			case packetCh <- udpPacket{addr: clientAddr, data: payload, bufPtr: bufPtr}:
			default:
				udpPacketPool.Put(bufPtr)
				l.logger.Debug("udp packet dropped, queue full", "client", clientAddr.String())
			}
		}
	}()
	return nil
}

func (l *UDPListener) Close() error {
	if l.conn != nil {
		err := l.conn.Close()
		l.logger.Info("udp listener stopped", "addr", net.JoinHostPort(l.cfg.Addr, util.FormatPort(l.cfg.Port)))
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
			l.logger.Debug("udp mapping limit reached, dropping packet", "client", key)
			return
		}
		var err error
		mapping, err = l.createMapping(ctx, clientAddr)
		if err != nil {
			<-l.sem
			l.logger.Debug("udp mapping creation failed", "error", err)
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
		return nil, err
	}
	ip := up.ActiveIP()
	if ip == nil {
		return nil, errors.New("upstream has no resolved IP")
	}
	upAddr := &net.UDPAddr{IP: ip, Port: l.cfg.Port}
	upConn, err := net.DialUDP("udp", nil, upAddr)
	if err != nil {
		l.manager.MarkDialFailure(up.Tag, udpDialFailureCooldown)
		return nil, err
	}
	l.manager.ClearDialFailure(up.Tag)
	mapping := &udpMapping{
		parent:       l,
		clientAddr:   clientAddr,
		upstreamTag:  up.Tag,
		upstreamConn: upConn,
		timeout:      l.timeout,
		metrics:      l.metrics,
		status:       l.status,
		logger:       l.logger,
		activityCh:   make(chan struct{}, 1),
		done:         make(chan struct{}),
	}
	mapping.id = l.status.AddUDP(clientAddr.String(), up.Tag, l.cfg.Port, mapping.close)
	l.mu.Lock()
	l.mappings[clientAddr.String()] = mapping
	l.mu.Unlock()

	mapping.logger.Debug("udp mapping added", "id", mapping.id, "client", clientAddr.String(), "upstream", up.Tag)
	go mapping.readLoop(ctx)
	go mapping.idleWatcher(ctx)
	return mapping, nil
}

type udpMapping struct {
	parent       *UDPListener
	clientAddr   *net.UDPAddr
	upstreamTag  string
	upstreamConn *net.UDPConn
	timeout      time.Duration
	metrics      *metrics.Metrics
	status       *control.StatusStore
	logger       util.Logger

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
	m.metrics.AddBytesUp(m.upstreamTag, n, "udp")
	m.status.UpdateUDP(m.id, n, 0)
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
			m.metrics.AddBytesDown(m.upstreamTag, down, "udp")
			m.status.UpdateUDP(m.id, 0, down)
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
		m.logger.Debug("udp mapping closed", "id", m.id, "reason", reason)
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
