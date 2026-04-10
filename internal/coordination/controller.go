package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/gorilla/websocket"
)

const (
	handshakeTimeout        = 10 * time.Second
	gracefulTeardownTimeout = 2 * time.Second
)

type incomingEvent struct {
	pick    *PickMessage
	closing bool
}

type Controller struct {
	baseCtx context.Context
	cfg     config.CoordinationConfig
	client  *Client
	manager *upstream.UpstreamManager
	metrics *metrics.Metrics
	logger  util.Logger

	mu                 sync.Mutex
	enabled            bool
	cancel             context.CancelFunc
	connectionCallback func(bool)
	wg                 sync.WaitGroup
}

func NewController(
	baseCtx context.Context,
	cfg config.CoordinationConfig,
	manager *upstream.UpstreamManager,
	metrics *metrics.Metrics,
	logger util.Logger,
) *Controller {
	return &Controller{
		baseCtx: baseCtx,
		cfg:     cfg,
		client:  NewClient(cfg),
		manager: manager,
		metrics: metrics,
		logger:  logger,
	}
}

func (c *Controller) Configured() bool {
	return c != nil && c.cfg.IsConfigured()
}

func (c *Controller) Enable() {
	if c == nil || !c.Configured() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enabled {
		return
	}
	ctx, cancel := context.WithCancel(c.baseCtx)
	c.enabled = true
	c.cancel = cancel
	c.wg.Add(1)
	go c.run(ctx)
}

func (c *Controller) Disable() {
	if c == nil {
		return
	}
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.enabled = false
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Controller) Close() {
	c.Disable()
	c.wg.Wait()
}

func (c *Controller) SetConnectionCallback(callback func(bool)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectionCallback = callback
}

func (c *Controller) run(ctx context.Context) {
	defer c.wg.Done()

	backoff := time.Second
	for {
		err := c.runSession(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			util.Event(c.logger, slog.LevelWarn, "coordination.session_ended", "error", err)
		}
		if c.metrics != nil {
			c.metrics.IncCoordinationReconnects()
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 10*time.Second {
			backoff *= 2
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
		}
	}
}

func (c *Controller) runSession(ctx context.Context) error {
	conn, _, err := c.client.DialNode(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	handshakeActive := atomic.Bool{}
	handshakeActive.Store(true)
	handshakeWatcherDone := make(chan struct{})
	stopHandshakeWatcher := make(chan struct{})
	var stopHandshakeOnce sync.Once
	stopHandshake := func() {
		handshakeActive.Store(false)
		stopHandshakeOnce.Do(func() {
			close(stopHandshakeWatcher)
			<-handshakeWatcherDone
		})
	}
	go func() {
		defer close(handshakeWatcherDone)
		select {
		case <-ctx.Done():
			if handshakeActive.Load() {
				_ = conn.Close()
			}
		case <-stopHandshakeWatcher:
		}
	}()
	defer stopHandshake()

	if err := c.writeMessage(conn, HelloMessage{Type: "hello"}); err != nil {
		return err
	}
	if err := c.waitForReady(ctx, conn); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	stopHandshake()

	c.manager.SetCoordinationConnected(true)
	c.callConnectionCallback(true)
	c.syncMetrics()
	defer func() {
		c.manager.SetCoordinationConnected(false)
		c.callConnectionCallback(false)
		c.syncMetrics()
	}()

	incoming := make(chan incomingEvent, 4)
	readErr := make(chan error, 1)
	go c.readLoop(conn, incoming, readErr)

	if err := c.writePreferences(conn); err != nil {
		return err
	}

	ticker := time.NewTicker(c.cfg.HeartbeatInterval.Duration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.gracefulTeardown(conn, incoming, readErr)
			return nil
		case err := <-readErr:
			return err
		case event := <-incoming:
			if event.closing {
				return nil
			}
			if event.pick == nil {
				continue
			}
			if c.metrics != nil {
				c.metrics.IncCoordinationPicksReceived()
			}
			tag := ""
			if event.pick.Upstream != nil {
				tag = *event.pick.Upstream
			}
			applied, err := c.manager.ApplyCoordinationPick(event.pick.Version, tag)
			c.syncMetrics()
			if err != nil {
				if c.metrics != nil {
					c.metrics.IncCoordinationPicksRejected()
				}
				util.Event(c.logger, slog.LevelWarn, "coordination.pick_rejected",
					"version", event.pick.Version,
					"upstream", tag,
					"error", err,
				)
				continue
			}
			if applied && c.metrics != nil {
				c.metrics.IncCoordinationPicksApplied()
			}
		case <-ticker.C:
			if err := c.writeMessage(conn, HeartbeatMessage{Type: "heartbeat"}); err != nil {
				return err
			}
			if err := c.writePreferences(conn); err != nil {
				return err
			}
		}
	}
}

func (c *Controller) waitForReady(ctx context.Context, conn *websocket.Conn) error {
	if err := conn.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return err
	}
	defer conn.SetReadDeadline(time.Time{})

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("decode coordination message: %w", err)
		}

		switch envelope.Type {
		case "ready":
			var ready ReadyMessage
			if err := json.Unmarshal(data, &ready); err != nil {
				return fmt.Errorf("decode coordination ready: %w", err)
			}
			if ready.NodeID == "" {
				return errors.New("coordination ready missing node_id")
			}
			return nil
		case "error":
			var msg ErrorMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				return fmt.Errorf("decode coordination error: %w", err)
			}
			return fmt.Errorf("fbcoord error %s: %s", msg.Code, msg.Message)
		case "closing":
			return errors.New("fbcoord closed the session during handshake")
		case "pick":
			return errors.New("fbcoord sent pick before ready")
		default:
			return fmt.Errorf("unexpected coordination message type %q during handshake", envelope.Type)
		}
	}
}

func (c *Controller) callConnectionCallback(connected bool) {
	c.mu.Lock()
	callback := c.connectionCallback
	c.mu.Unlock()
	if callback != nil {
		callback(connected)
	}
}

func (c *Controller) gracefulTeardown(
	conn *websocket.Conn,
	incoming <-chan incomingEvent,
	readErr <-chan error,
) {
	if err := c.writeMessage(conn, ByeMessage{Type: "bye"}); err != nil {
		_ = conn.Close()
		return
	}

	timer := time.NewTimer(gracefulTeardownTimeout)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			_ = conn.Close()
			return
		case event := <-incoming:
			if event.closing {
				_ = conn.Close()
				return
			}
		case <-readErr:
			return
		}
	}
}

func (c *Controller) writePreferences(conn *websocket.Conn) error {
	return c.writeMessage(conn, PreferencesMessage{
		Type:           "preferences",
		Upstreams:      c.manager.RankedTags(),
		ActiveUpstream: c.manager.ActiveTag(),
	})
}

func (c *Controller) writeMessage(conn *websocket.Conn, message interface{}) error {
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return conn.WriteJSON(message)
}

func (c *Controller) readLoop(conn *websocket.Conn, incoming chan<- incomingEvent, readErr chan<- error) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			readErr <- err
			return
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			readErr <- fmt.Errorf("decode coordination message: %w", err)
			return
		}

		switch envelope.Type {
		case "pick":
			var pick PickMessage
			if err := json.Unmarshal(data, &pick); err != nil {
				readErr <- fmt.Errorf("decode coordination pick: %w", err)
				return
			}
			incoming <- incomingEvent{pick: &pick}
		case "closing":
			var closing ClosingMessage
			if err := json.Unmarshal(data, &closing); err != nil {
				readErr <- fmt.Errorf("decode coordination closing: %w", err)
				return
			}
			incoming <- incomingEvent{closing: true}
			return
		case "error":
			var msg ErrorMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				readErr <- fmt.Errorf("decode coordination error: %w", err)
				return
			}
			readErr <- fmt.Errorf("fbcoord error %s: %s", msg.Code, msg.Message)
			return
		default:
			readErr <- fmt.Errorf("unexpected coordination message type %q", envelope.Type)
			return
		}
	}
}

func (c *Controller) syncMetrics() {
	if c.metrics == nil {
		return
	}
	c.metrics.SetCoordinationState(c.manager.CoordinationState())
}
