package coordination

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/upstream"
	"github.com/NodePath81/fbforward/internal/util"
	"github.com/gorilla/websocket"
)

type Controller struct {
	baseCtx context.Context
	cfg     config.CoordinationConfig
	client  *Client
	manager *upstream.UpstreamManager
	metrics *metrics.Metrics
	logger  util.Logger

	mu      sync.Mutex
	enabled bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
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
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	c.manager.SetCoordinationConnected(true)
	c.syncMetrics()
	defer func() {
		c.manager.SetCoordinationConnected(false)
		c.syncMetrics()
	}()

	if err := c.writeMessage(conn, HelloMessage{
		Type:   "hello",
		Pool:   c.cfg.Pool,
		NodeID: c.cfg.NodeID,
	}); err != nil {
		return err
	}
	if err := c.writePreferences(conn); err != nil {
		return err
	}

	incoming := make(chan PickMessage, 4)
	readErr := make(chan error, 1)
	go c.readLoop(conn, incoming, readErr)

	ticker := time.NewTicker(c.cfg.HeartbeatInterval.Duration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return err
		case pick := <-incoming:
			if c.metrics != nil {
				c.metrics.IncCoordinationPicksReceived()
			}
			tag := ""
			if pick.Upstream != nil {
				tag = *pick.Upstream
			}
			applied, err := c.manager.ApplyCoordinationPick(pick.Version, tag)
			c.syncMetrics()
			if err != nil {
				if c.metrics != nil {
					c.metrics.IncCoordinationPicksRejected()
				}
				util.Event(c.logger, slog.LevelWarn, "coordination.pick_rejected",
					"version", pick.Version,
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

func (c *Controller) readLoop(conn *websocket.Conn, incoming chan<- PickMessage, readErr chan<- error) {
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
			incoming <- pick
		case "error":
			var msg ErrorMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				readErr <- fmt.Errorf("decode coordination error: %w", err)
				return
			}
			readErr <- fmt.Errorf("fbcoord error %s: %s", msg.Code, msg.Message)
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
