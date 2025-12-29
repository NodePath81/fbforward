package main

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

type TCPListener struct {
	cfg     ListenerConfig
	manager *UpstreamManager
	metrics *Metrics
	status  *StatusStore
	timeout time.Duration
	sem     chan struct{}
	logger  Logger

	listener net.Listener
}

func NewTCPListener(cfg ListenerConfig, limits LimitsConfig, timeout time.Duration, manager *UpstreamManager, metrics *Metrics, status *StatusStore, logger Logger) *TCPListener {
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
	addr := net.JoinHostPort(l.cfg.Addr, formatPort(l.cfg.Port))
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
		l.logger.Info("tcp listener stopped", "addr", net.JoinHostPort(l.cfg.Addr, formatPort(l.cfg.Port)))
		return err
	}
	return nil
}

func (l *TCPListener) handleConn(ctx context.Context, client net.Conn) {
	defer func() { <-l.sem }()
	up, err := l.manager.SelectUpstream()
	if err != nil {
		l.logger.Debug("tcp upstream selection failed", "error", err)
		_ = client.Close()
		return
	}
	remoteAddr := net.JoinHostPort(up.ActiveIP.String(), formatPort(l.cfg.Port))
	upConn, err := net.Dial("tcp", remoteAddr)
	if err != nil {
		l.logger.Debug("tcp dial failed", "upstream", up.Tag, "error", err)
		_ = client.Close()
		return
	}

	conn := &tcpConn{
		client:  client,
		upstream: upConn,
		upstreamTag: up.Tag,
		timeout: l.timeout,
		metrics: l.metrics,
		status:  l.status,
		logger:  l.logger,
	}
	conn.start(ctx)
}

type tcpConn struct {
	client      net.Conn
	upstream    net.Conn
	upstreamTag string
	timeout     time.Duration
	metrics     *Metrics
	status      *StatusStore
	logger      Logger

	id         string
	closeOnce  sync.Once
	activityCh chan struct{}
	done       chan struct{}
}

func (c *tcpConn) start(ctx context.Context) {
	clientAddr := c.client.RemoteAddr().String()
	c.activityCh = make(chan struct{}, 1)
	c.done = make(chan struct{})
	c.id = c.status.AddTCP(clientAddr, c.upstreamTag, c.close)
	c.logger.Debug("tcp connection added", "id", c.id, "client", clientAddr, "upstream", c.upstreamTag)

	go c.proxy(ctx, c.upstream, c.client, true)
	go c.proxy(ctx, c.client, c.upstream, false)
	go c.idleWatcher(ctx)
}

func (c *tcpConn) proxy(ctx context.Context, dst, src net.Conn, up bool) {
	buf := make([]byte, 32*1024)
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

func (c *tcpConn) touch(n uint64, up bool) {
	if up {
		c.metrics.AddBytesUp(c.upstreamTag, n)
		c.status.UpdateTCP(c.id, n, 0)
	} else {
		c.metrics.AddBytesDown(c.upstreamTag, n)
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
