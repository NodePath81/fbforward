package forwarding

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/control"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
)

type TCPListener struct {
	cfg     config.ListenerConfig
	manager *upstream.UpstreamManager
	metrics *metrics.Metrics
	status  *control.StatusStore
	timeout time.Duration
	sem     chan struct{}
	logger  util.Logger

	listener net.Listener
}

const (
	tcpDialTimeout         = 5 * time.Second
	tcpDialFailureCooldown = 5 * time.Second
	tcpBufferSize          = 32 * 1024
)

var tcpBufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, tcpBufferSize)
		return &buf
	},
}

func NewTCPListener(cfg config.ListenerConfig, limits config.LimitsConfig, timeout time.Duration, manager *upstream.UpstreamManager, metrics *metrics.Metrics, status *control.StatusStore, logger util.Logger) *TCPListener {
	return &TCPListener{
		cfg:     cfg,
		manager: manager,
		metrics: metrics,
		status:  status,
		timeout: timeout,
		sem:     make(chan struct{}, limits.MaxTCPConns),
		logger:  logger,
	}
}

func (l *TCPListener) Start(ctx context.Context, wg *sync.WaitGroup) error {
	addr := net.JoinHostPort(l.cfg.Addr, util.FormatPort(l.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	l.listener = ln
	l.logger.Info("tcp listener started", "addr", addr)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
					l.logger.Error("tcp accept error", "error", err)
					continue
				}
			}
			select {
			case l.sem <- struct{}{}:
				go l.handleConn(ctx, conn)
			default:
				l.logger.Debug("tcp limit reached, closing connection", "client", conn.RemoteAddr().String())
				_ = conn.Close()
			}
		}
	}()
	return nil
}

func (l *TCPListener) Close() error {
	if l.listener != nil {
		err := l.listener.Close()
		l.logger.Info("tcp listener stopped", "addr", net.JoinHostPort(l.cfg.Addr, util.FormatPort(l.cfg.Port)))
		return err
	}
	return nil
}

func (l *TCPListener) handleConn(ctx context.Context, client net.Conn) {
	defer func() { <-l.sem }()
	if tcpConn, ok := client.(*net.TCPConn); ok {
		applyTCPOptions(tcpConn)
	}
	up, err := l.manager.SelectUpstream()
	if err != nil {
		l.logger.Debug("tcp upstream selection failed", "error", err)
		_ = client.Close()
		return
	}
	ip := up.ActiveIP()
	if ip == nil {
		l.logger.Debug("tcp upstream missing IP", "upstream", up.Tag)
		_ = client.Close()
		return
	}
	remoteAddr := net.JoinHostPort(ip.String(), util.FormatPort(l.cfg.Port))
	upConn, err := dialTCPWithRetry(ctx, remoteAddr, 2, 150*time.Millisecond)
	if err != nil {
		l.logger.Debug("tcp dial failed", "upstream", up.Tag, "error", err)
		l.manager.MarkDialFailure(up.Tag, tcpDialFailureCooldown)
		_ = client.Close()
		return
	}
	l.manager.ClearDialFailure(up.Tag)

	conn := &tcpConn{
		client:      client,
		upstream:    upConn,
		upstreamTag: up.Tag,
		listenPort:  l.cfg.Port,
		timeout:     l.timeout,
		metrics:     l.metrics,
		status:      l.status,
		logger:      l.logger,
	}
	conn.start(ctx)
}

type tcpConn struct {
	client      net.Conn
	upstream    net.Conn
	upstreamTag string
	listenPort  int
	timeout     time.Duration
	metrics     *metrics.Metrics
	status      *control.StatusStore
	logger      util.Logger

	id         string
	closeOnce  sync.Once
	activityCh chan struct{}
	done       chan struct{}
}

func (c *tcpConn) start(ctx context.Context) {
	clientAddr := c.client.RemoteAddr().String()
	c.activityCh = make(chan struct{}, 1)
	c.done = make(chan struct{})
	c.id = c.status.AddTCP(clientAddr, c.upstreamTag, c.listenPort, c.close)
	c.logger.Debug("tcp connection added", "id", c.id, "client", clientAddr, "upstream", c.upstreamTag)

	go c.proxy(ctx, c.upstream, c.client, true)
	go c.proxy(ctx, c.client, c.upstream, false)
	go c.idleWatcher(ctx)
}

func (c *tcpConn) proxy(ctx context.Context, dst, src net.Conn, up bool) {
	bufPtr := tcpBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer tcpBufPool.Put(bufPtr)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if werr := writeAll(dst, buf[:n]); werr != nil {
				c.closeWithReason("write_error")
				return
			}
			c.touch(uint64(n), up)
		}
		if err != nil {
			if err != io.EOF {
				c.closeWithReason("read_error")
			} else {
				c.closeWithReason("eof")
			}
			return
		}
		select {
		case <-ctx.Done():
			c.closeWithReason("context_done")
			return
		case <-c.done:
			return
		default:
		}
	}
}

func writeAll(dst net.Conn, buf []byte) error {
	for len(buf) > 0 {
		n, err := dst.Write(buf)
		if err != nil {
			return err
		}
		buf = buf[n:]
	}
	return nil
}

func dialTCPWithRetry(ctx context.Context, addr string, attempts int, backoff time.Duration) (net.Conn, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		dialer := net.Dialer{Timeout: tcpDialTimeout}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				applyTCPOptions(tcpConn)
			}
			return conn, nil
		}
		lastErr = err
		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return nil, lastErr
}

func applyTCPOptions(conn *net.TCPConn) {
	_ = conn.SetNoDelay(true)
	_ = conn.SetKeepAlive(true)
	_ = conn.SetKeepAlivePeriod(30 * time.Second)
}

func (c *tcpConn) touch(n uint64, up bool) {
	if up {
		c.metrics.AddBytesUp(c.upstreamTag, n, "tcp")
		c.status.UpdateTCP(c.id, n, 0)
	} else {
		c.metrics.AddBytesDown(c.upstreamTag, n, "tcp")
		c.status.UpdateTCP(c.id, 0, n)
	}
	select {
	case c.activityCh <- struct{}{}:
	default:
	}
}

func (c *tcpConn) idleWatcher(ctx context.Context) {
	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			c.closeWithReason("context_done")
			return
		case <-c.done:
			return
		case <-timer.C:
			c.closeWithReason("idle_timeout")
			return
		case <-c.activityCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.timeout)
		}
	}
}

func (c *tcpConn) closeWithReason(reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.client.Close()
		_ = c.upstream.Close()
		c.status.RemoveTCP(c.id)
		c.logger.Debug("tcp connection closed", "id", c.id, "reason", reason)
	})
}

func (c *tcpConn) close() {
	c.closeWithReason("upstream_unusable")
}
