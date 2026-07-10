package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
	"github.com/gorilla/websocket"
)

type runningTestEntry struct {
	Upstream  string `json:"upstream"`
	Protocol  string `json:"protocol"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

type connectionsSnapshotMessage struct {
	SchemaVersion int           `json:"schema_version"`
	Type          string        `json:"type"`
	Timestamp     int64         `json:"timestamp"`
	TCP           []StatusEntry `json:"tcp"`
	UDP           []StatusEntry `json:"udp"`
}

type queueSnapshotMessage struct {
	SchemaVersion int                 `json:"schema_version"`
	Type          string              `json:"type"`
	Timestamp     int64               `json:"timestamp"`
	Depth         int                 `json:"depth"`
	NextDueMs     *int64              `json:"next_due_ms,omitempty"`
	Running       []runningTestEntry  `json:"running"`
	Pending       []queuePendingEntry `json:"pending"`
}

type queuePendingEntry struct {
	Upstream    string `json:"upstream"`
	Protocol    string `json:"protocol"`
	ScheduledAt int64  `json:"scheduled_at"`
}

func (c *ControlServer) getQueueSnapshot(now time.Time) queueSnapshotMessage {
	c.schedulerMu.RLock()
	scheduler := c.scheduler
	c.schedulerMu.RUnlock()

	c.collectorMu.RLock()
	collector := c.collector
	c.collectorMu.RUnlock()

	snapshot := queueSnapshotMessage{
		SchemaVersion: 1,
		Type:          "queue_snapshot",
		Timestamp:     now.UnixMilli(),
		Depth:         0,
		NextDueMs:     nil,
		Running:       []runningTestEntry{},
		Pending:       []queuePendingEntry{},
	}

	if scheduler != nil {
		status := scheduler.Status()
		snapshot.Depth = status.QueueLength
		if !status.NextScheduled.IsZero() {
			delta := status.NextScheduled.Sub(now).Milliseconds()
			if delta < 0 {
				delta = 0
			}
			snapshot.NextDueMs = &delta
		}
		entries := make([]queuePendingEntry, 0, len(status.Pending))
		for _, item := range status.Pending {
			entries = append(entries, queuePendingEntry{
				Upstream:    item.Upstream,
				Protocol:    item.Protocol,
				ScheduledAt: item.ScheduledAt.UnixMilli(),
			})
		}
		snapshot.Pending = entries
	}

	if collector != nil {
		running := collector.RunningTests()
		entries := make([]runningTestEntry, 0, len(running))
		for _, test := range running {
			entries = append(entries, runningTestEntry{
				Upstream:  test.Upstream,
				Protocol:  test.Protocol,
				ElapsedMs: now.Sub(test.StartTime).Milliseconds(),
			})
		}
		snapshot.Running = entries
	}

	return snapshot
}

func (c *ControlServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	authOK := c.checkStatusAuth(r)
	reqCtx := c.newRequestCtx(r, "ws", authOK)
	sw := &statusWriter{ResponseWriter: w}
	completionErr := ""
	defer c.auditCompletion(sw, reqCtx, &completionErr, "control.ws.request_completed")
	connID := fmt.Sprintf("ws-%d", atomic.AddUint64(&c.nextWSID, 1))

	policyAttrs := append([]any{
		"request.id", reqCtx.id,
		"client.ip", reqCtx.clientIP,
		"request.method", reqCtx.method,
		"request.path", reqCtx.path,
		"http.user_agent", reqCtx.userAgent,
		"auth.identity", "unknown",
		"auth.method", reqCtx.authMethod,
		"auth.authenticated", reqCtx.authOK,
	}, c.policyAttrs()...)
	policyAttrs = append(policyAttrs, "result", "success")
	util.Event(c.logger, slog.LevelInfo, "control.ws.access_policy_decision", policyAttrs...)

	if !c.limiter.Allow(reqCtx.clientIP) {
		completionErr = "rate limit exceeded"
		util.Event(c.logger, slog.LevelWarn, "control.ws.rate_limited",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"http.status_code", http.StatusTooManyRequests,
			"result", "denied",
		)
		writeAPIError(sw, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	if !c.status.hub.CanRegister() {
		completionErr = "too many websocket clients"
		util.Event(c.logger, slog.LevelWarn, "control.ws.connection_limit_reached",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"http.status_code", http.StatusServiceUnavailable,
			"result", "denied",
		)
		writeAPIError(sw, http.StatusServiceUnavailable, "too many websocket clients")
		return
	}
	if !c.originAllowed(r) {
		completionErr = "forbidden"
		util.Event(c.logger, slog.LevelWarn, "control.ws.origin_rejected",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"request.method", reqCtx.method,
			"request.path", reqCtx.path,
			"http.user_agent", reqCtx.userAgent,
			"http.status_code", http.StatusForbidden,
			"result", "denied",
		)
		writeAPIError(sw, http.StatusForbidden, "forbidden")
		return
	}
	if !authOK {
		completionErr = "unauthorized"
		util.Event(c.logger, slog.LevelWarn, "control.ws.auth_failed",
			"request.id", reqCtx.id,
			"client.addr", reqCtx.clientAddr,
			"client.ip", reqCtx.clientIP,
			"request.method", reqCtx.method,
			"request.path", reqCtx.path,
			"http.user_agent", reqCtx.userAgent,
			"auth.identity", "unknown",
			"auth.method", reqCtx.authMethod,
			"auth.authenticated", false,
			"http.status_code", http.StatusUnauthorized,
			"result", "denied",
		)
		writeAPIError(sw, http.StatusUnauthorized, "unauthorized")
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin:  c.originAllowed,
		Subprotocols: []string{wsPrimaryProtocol},
		Error: func(w http.ResponseWriter, _ *http.Request, status int, reason error) {
			writeAPIError(w, status, reason.Error())
		},
	}
	conn, err := upgrader.Upgrade(sw, r, nil)
	if err != nil {
		completionErr = err.Error()
		util.Event(c.logger, slog.LevelWarn, "control.ws.upgrade_failed",
			"ws.conn_id", connID,
			"client.addr", reqCtx.clientAddr,
			"error", err,
		)
		return
	}
	sw.code = http.StatusSwitchingProtocols
	sw.written = true
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	client := &statusClient{send: make(chan []byte, 32), connID: connID}
	if !c.status.hub.TryRegister(client) {
		completionErr = "too many websocket clients"
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "too many websocket clients"), time.Now().Add(wsWriteWait))
		_ = conn.Close()
		return
	}
	util.Event(c.logger, slog.LevelInfo, "control.ws.connected",
		"ws.conn_id", connID,
		"client.addr", reqCtx.clientAddr,
		"client.ip", reqCtx.clientIP,
	)

	var closeOnce sync.Once
	done := make(chan struct{})
	closeConn := func() {
		closeOnce.Do(func() {
			close(done)
			_ = conn.Close()
		})
	}
	var subMu sync.Mutex
	stopTicker := func() {
		subMu.Lock()
		if client.tickerCancel != nil {
			client.tickerCancel()
			client.tickerCancel = nil
		}
		client.subscribed = false
		client.intervalMs = 0
		subMu.Unlock()
	}

	sendJSON := func(payload any) {
		select {
		case <-done:
			return
		default:
		}
		data, _ := json.Marshal(payload)
		_ = client.enqueue(data)
	}

	sendError := func(code, message string) {
		sendJSON(statusMessage{
			SchemaVersion:      1,
			Type:               "error",
			statusErrorPayload: &statusErrorPayload{Code: code, Message: message},
		})
	}

	sendSnapshots := func() {
		select {
		case <-done:
			return
		default:
		}
		now := time.Now()
		tcp, udp := c.status.Snapshot()
		sendJSON(connectionsSnapshotMessage{
			SchemaVersion: 1,
			Type:          "connections_snapshot",
			Timestamp:     now.UnixMilli(),
			TCP:           tcp,
			UDP:           udp,
		})
		sendJSON(c.getQueueSnapshot(now))
	}

	startTicker := func(intervalMs int, sendInitial bool) {
		stopTicker()
		subMu.Lock()
		client.subscribed = true
		client.intervalMs = intervalMs
		ctx, cancel := context.WithCancel(context.Background())
		client.tickerCancel = cancel
		subMu.Unlock()
		util.Event(c.logger, slog.LevelDebug, "control.ws.subscribe_updated",
			"ws.conn_id", connID,
			"interval_ms", intervalMs,
		)
		if sendInitial {
			sendSnapshots()
		}
		go func() {
			ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-done:
					return
				case <-ticker.C:
					sendSnapshots()
				}
			}
		}()
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			stopTicker()
			closeConn()
			c.status.hub.Unregister(client)
			util.Event(c.logger, slog.LevelInfo, "control.ws.disconnected",
				"ws.conn_id", connID,
				"client.addr", reqCtx.clientAddr,
			)
		})
	}

	go func() {
		defer cleanup()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				util.Event(c.logger, slog.LevelDebug, "control.ws.io_or_heartbeat_failed",
					"ws.conn_id", connID,
					"error", err,
				)
				return
			}
			var req struct {
				Type       string `json:"type"`
				IntervalMs int    `json:"interval_ms"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				util.Event(c.logger, slog.LevelDebug, "control.ws.request_invalid",
					"ws.conn_id", connID,
					"error", err,
				)
				continue
			}
			switch req.Type {
			case "subscribe":
				if req.IntervalMs != 1000 && req.IntervalMs != 2000 && req.IntervalMs != 5000 {
					util.Event(c.logger, slog.LevelDebug, "control.ws.request_invalid",
						"ws.conn_id", connID,
						"error", "invalid interval_ms",
					)
					sendError("invalid_interval", "interval_ms must be 1000, 2000, or 5000")
					continue
				}
				subMu.Lock()
				alreadySubscribed := client.subscribed
				subMu.Unlock()
				startTicker(req.IntervalMs, !alreadySubscribed)
			case "unsubscribe":
				stopTicker()
				util.Event(c.logger, slog.LevelDebug, "control.ws.unsubscribed", "ws.conn_id", connID)
			default:
				util.Event(c.logger, slog.LevelDebug, "control.ws.request_invalid",
					"ws.conn_id", connID,
					"error", "unknown request type",
				)
			}
		}
	}()

	go func() {
		defer cleanup()
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(wsWriteWait)); err != nil {
					util.Event(c.logger, slog.LevelDebug, "control.ws.io_or_heartbeat_failed",
						"ws.conn_id", connID,
						"error", err,
					)
					return
				}
			case data, ok := <-client.send:
				if !ok {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					util.Event(c.logger, slog.LevelDebug, "control.ws.io_or_heartbeat_failed",
						"ws.conn_id", connID,
						"error", err,
					)
					return
				}
			}
		}
	}()
}
