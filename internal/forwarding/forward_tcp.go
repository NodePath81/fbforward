package forwarding

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/util"
)

type TCPListener struct {
	cfg      config.ListenerConfig
	picker   UpstreamPicker
	policy   AdmissionPolicy
	timeout  time.Duration
	observer FlowObserver
	registry *flow.Registry
	binder   BackendBinder
	sem      chan struct{}
	logger   util.Logger

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

func NewTCPListener(cfg config.ListenerConfig, limits config.ForwardingLimitsConfig, timeout time.Duration, picker UpstreamPicker, policy AdmissionPolicy, observer FlowObserver, registry *flow.Registry, binder BackendBinder, logger util.Logger) *TCPListener {
	return &TCPListener{
		cfg:      cfg,
		picker:   picker,
		policy:   policy,
		timeout:  timeout,
		observer: observer,
		registry: registry,
		binder:   binder,
		sem:      make(chan struct{}, limits.MaxTCPConnections),
		logger:   util.ComponentLogger(logger, util.CompForwardTCP),
	}
}

func (l *TCPListener) Start(ctx context.Context, wg *sync.WaitGroup) error {
	addr := net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	l.listener = ln
	util.Event(l.logger, slog.LevelInfo, "forward.tcp.listener_started", "listen.addr", addr)

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
					util.Event(l.logger, slog.LevelError, "forward.tcp.accept_error", "error", err)
					continue
				}
			}
			select {
			case l.sem <- struct{}{}:
				go l.handleConn(ctx, conn)
			default:
				clientAddr := conn.RemoteAddr().String()
				emitRejection(l.observer, flow.ProtocolTCP, l.listenAddr(), clientAddr, "tcp_connection_limit", Decision{})
				util.Event(l.logger, slog.LevelWarn, "forward.tcp.connection_limit_reached",
					"client.addr", conn.RemoteAddr().String(),
				)
				_ = conn.Close()
			}
		}
	}()
	return nil
}

func (l *TCPListener) Close() error {
	if l.listener != nil {
		err := l.listener.Close()
		util.Event(l.logger, slog.LevelInfo, "forward.tcp.listener_stopped",
			"listen.addr", net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort)),
		)
		return err
	}
	return nil
}

func (l *TCPListener) handleConn(ctx context.Context, client net.Conn) {
	defer func() { <-l.sem }()
	if tcpConn, ok := client.(*net.TCPConn); ok {
		applyTCPOptions(tcpConn)
	}
	clientAddr := client.RemoteAddr().String()
	candidate, err := newCandidateMeta(flow.ProtocolTCP, clientAddr, l.listenAddr())
	if err != nil {
		_ = client.Close()
		return
	}
	if l.policy != nil {
		decision := l.policy.Decide(candidate)
		if !decision.Allowed {
			emitRejection(l.observer, flow.ProtocolTCP, l.listenAddr(), clientAddr, "firewall_deny", decision)
			_ = client.Close()
			return
		}
	}
	if l.picker == nil {
		_ = client.Close()
		return
	}
	selected, err := l.picker.Pick(candidate)
	if err != nil {
		util.Event(l.logger, slog.LevelWarn, "forward.tcp.upstream_selection_failed", "error", err)
		_ = client.Close()
		return
	}
	if !selected.Addr.IsValid() {
		util.Event(l.logger, slog.LevelWarn, "forward.tcp.dial_failed",
			"upstream", selected.Tag,
			"result", "failed",
		)
		_ = client.Close()
		return
	}
	upstreamIP := selected.Addr.String()
	util.Event(l.logger, slog.LevelDebug, "forward.tcp.upstream_selected",
		"upstream", selected.Tag,
		"upstream.ip", upstreamIP,
	)
	remoteAddr := net.JoinHostPort(selected.Addr.String(), util.FormatPort(l.cfg.BindPort))
	upConn, err := dialTCPWithRetry(ctx, remoteAddr, 2, 150*time.Millisecond, l.logger, selected.Tag)
	if err != nil {
		util.Event(l.logger, slog.LevelWarn, "forward.tcp.dial_failed",
			"upstream", selected.Tag,
			"upstream.ip", upstreamIP,
			"error", err,
			"result", "failed",
		)
		if feedback, ok := l.picker.(DialFeedback); ok {
			feedback.MarkDialFailure(selected, tcpDialFailureCooldown)
		}
		_ = client.Close()
		return
	}
	if feedback, ok := l.picker.(DialFeedback); ok {
		feedback.ClearDialFailure(selected)
	}

	conn := &tcpConn{
		client:       client,
		upstream:     upConn,
		upstreamTag:  selected.Tag,
		listenPort:   l.cfg.BindPort,
		timeout:      l.timeout,
		logger:       l.logger,
		observer:     l.observer,
		registry:     l.registry,
		binder:       l.binder,
		upstreamIP:   upstreamIP,
		upstreamAddr: remoteAddr,
		listenAddr:   net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort)),
		route:        l.cfg.Route,
		created:      candidate.StartedAt,
	}
	conn.start(ctx)
}

type tcpConn struct {
	client       net.Conn
	upstream     net.Conn
	upstreamTag  string
	listenPort   int
	timeout      time.Duration
	logger       util.Logger
	observer     FlowObserver
	registry     *flow.Registry
	binder       BackendBinder
	upstreamIP   string
	upstreamAddr string
	listenAddr   string
	route        string
	clientAddr   string
	clientIP     string

	id         flow.ID
	closeOnce  sync.Once
	activityCh chan struct{}
	done       chan struct{}
	created    time.Time
	lifecycle  *flow.Lifecycle
}

func (c *tcpConn) start(ctx context.Context) {
	clientAddr := c.client.RemoteAddr().String()
	c.clientAddr = clientAddr
	c.clientIP = clientIPFromAddr(clientAddr)
	if c.created.IsZero() {
		c.created = time.Now().UTC()
	} else {
		c.created = c.created.UTC()
	}
	c.activityCh = make(chan struct{}, 1)
	c.done = make(chan struct{})
	clientEndpoint, err := netip.ParseAddrPort(clientAddr)
	if err != nil {
		util.Event(c.logger, slog.LevelWarn, "forward.tcp.invalid_client_addr", "client.addr", clientAddr, "error", err)
		_ = c.client.Close()
		_ = c.upstream.Close()
		return
	}
	c.id, err = flow.NewID()
	if err != nil {
		util.Event(c.logger, slog.LevelError, "forward.tcp.flow_id_failed", "error", err)
		_ = c.client.Close()
		_ = c.upstream.Close()
		return
	}
	c.lifecycle = flow.NewLifecycle(flow.Meta{
		ID:         c.id,
		Protocol:   flow.ProtocolTCP,
		ClientAddr: clientEndpoint,
		Listener:   c.listenAddr,
		Route:      c.route,
		Upstream:   c.upstreamTag,
		StartedAt:  c.created,
	}, c.observer, c.registry, c.close)
	c.lifecycle.Open()
	if c.binder != nil {
		if tuple, bindErr := backendTuple(flow.ProtocolTCP, c.upstreamTag, c.upstream.LocalAddr(), c.upstream.RemoteAddr()); bindErr != nil {
			util.Event(c.logger, slog.LevelWarn, "forward.tcp.backend_bind_failed", "flow.id", c.id, "error", bindErr)
		} else if bindErr := c.binder.Bind(c.id, tuple); bindErr != nil {
			util.Event(c.logger, slog.LevelWarn, "forward.tcp.backend_bind_failed", "flow.id", c.id, "error", bindErr)
		}
	}
	util.Event(c.logger, slog.LevelInfo, "forward.tcp.connection_opened",
		"flow.id", c.id,
		"request.protocol", "tcp",
		"client.addr", clientAddr,
		"client.ip", c.clientIP,
		"listen.addr", c.listenAddr,
		"flow.listen_port", c.listenPort,
		"upstream", c.upstreamTag,
		"upstream.ip", c.upstreamIP,
		"upstream.addr", c.upstreamAddr,
		"result", "connected",
	)

	go c.proxy(ctx, c.upstream, c.client, true)
	go c.proxy(ctx, c.client, c.upstream, false)
	go c.idleWatcher(ctx)
}

func (c *tcpConn) proxy(ctx context.Context, dst, src net.Conn, up bool) {
	bufPtr := tcpBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer tcpBufPool.Put(bufPtr)
	for {
		_ = src.SetReadDeadline(time.Now().Add(c.timeout))
		n, err := src.Read(buf)
		if n > 0 {
			if werr := writeAll(dst, buf[:n]); werr != nil {
				c.closeWithReason("write_error")
				return
			}
			c.touch(uint64(n), up)
		}
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					c.closeWithReason("context_done")
					return
				case <-c.done:
					return
				default:
					continue
				}
			}
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

func dialTCPWithRetry(ctx context.Context, addr string, attempts int, backoff time.Duration, logger util.Logger, upstream string) (net.Conn, error) {
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
			util.Event(logger, slog.LevelDebug, "forward.tcp.dial_retry",
				"upstream", upstream,
				"attempt", i+1,
				"error", err,
			)
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
		if c.lifecycle != nil {
			c.lifecycle.Add(n, 0, 1, 0)
		}
	} else {
		if c.lifecycle != nil {
			c.lifecycle.Add(0, n, 0, 1)
		}
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
		durationMs := int64(0)
		if !c.created.IsZero() {
			durationMs = time.Since(c.created).Milliseconds()
		}
		counters := flow.Counters{}
		if c.lifecycle != nil {
			counters = c.lifecycle.Snapshot()
			c.lifecycle.Close(reason)
		}
		util.Event(c.logger, slog.LevelInfo, "forward.tcp.connection_closed",
			"flow.id", c.id,
			"request.protocol", "tcp",
			"client.addr", c.clientAddr,
			"client.ip", c.clientIP,
			"upstream", c.upstreamTag,
			"upstream.ip", c.upstreamIP,
			"upstream.addr", c.upstreamAddr,
			"flow.close_reason", reason,
			"flow.bytes_up", counters.BytesUp,
			"flow.bytes_down", counters.BytesDown,
			"flow.duration_ms", durationMs,
			"result", "closed",
		)
	})
}

func (c *tcpConn) close() {
	c.closeWithReason("upstream_unusable")
}

func clientIPFromAddr(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func (l *TCPListener) listenAddr() string {
	return net.JoinHostPort(l.cfg.BindAddr, util.FormatPort(l.cfg.BindPort))
}
