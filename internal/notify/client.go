package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
)

const (
	defaultQueueSize = 256
	defaultTimeout   = 3 * time.Second
	maxAttempts      = 3
	retryDelay       = 100 * time.Millisecond
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

type Event struct {
	Event      string         `json:"event"`
	OccurredAt string         `json:"occurred_at"`
	Instance   string         `json:"instance"`
	Attributes map[string]any `json:"attributes"`
}

type Emitter interface {
	Emit(eventName string, severity Severity, attributes map[string]any) bool
}

type Telemetry interface {
	IncWebhookDelivery(result string)
	IncWebhookDropped()
}

type Config struct {
	Endpoint       string
	BearerToken    string
	SourceInstance string
	QueueSize      int
	Timeout        time.Duration
	Now            func() time.Time
	HTTPClient     *http.Client
	Logger         util.Logger
	Telemetry      Telemetry
}

type Client struct {
	endpoint       string
	bearerToken    string
	sourceInstance string
	timeout        time.Duration
	now            func() time.Time
	httpClient     *http.Client
	logger         util.Logger
	telemetry      Telemetry

	mu     sync.Mutex
	closed bool
	queue  chan Event
	wg     sync.WaitGroup
}

func NewClient(cfg Config) (*Client, error) {
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid notify endpoint: %w", err)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid notify endpoint %q", cfg.Endpoint)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid notify endpoint scheme %q", parsed.Scheme)
	}

	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}

	c := &Client{
		endpoint:       parsed.String(),
		bearerToken:    cfg.BearerToken,
		sourceInstance: cfg.SourceInstance,
		timeout:        timeout,
		now:            nowFn,
		httpClient:     client,
		logger:         cfg.Logger,
		telemetry:      cfg.Telemetry,
		queue:          make(chan Event, queueSize),
	}
	c.wg.Add(1)
	go c.run()
	return c, nil
}

func (c *Client) Emit(eventName string, severity Severity, attributes map[string]any) bool {
	now := c.now().UTC()
	event := Event{
		Event:      eventName,
		OccurredAt: now.Format(time.RFC3339Nano),
		Instance:   c.sourceInstance,
		Attributes: cloneAttributes(attributes),
	}
	util.Event(c.logger, slog.LevelInfo, "notify.triggered",
		"notify.event", event.Event,
		"notify.severity", severity,
		"source.instance", event.Instance,
	)
	submitted := c.Submit(event)
	if submitted {
		util.Event(c.logger, slog.LevelInfo, "notify.enqueued",
			"notify.event", event.Event,
			"notify.severity", severity,
		)
		return true
	}
	util.Event(c.logger, slog.LevelWarn, "notify.enqueue_failed",
		"notify.event", event.Event,
		"notify.severity", severity,
	)
	return false
}

func (c *Client) Submit(event Event) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.queue <- event:
		return true
	default:
		if c.telemetry != nil {
			c.telemetry.IncWebhookDropped()
		}
		util.Event(c.logger, slog.LevelWarn, "notify.queue_full", "queue.capacity", cap(c.queue), "result", "dropped")
		return false
	}
}

func (c *Client) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.queue)
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.wg.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) run() {
	defer c.wg.Done()
	for event := range c.queue {
		c.send(event)
	}
}

func (c *Client) send(event Event) {
	rawBody, err := json.Marshal(event)
	if err != nil {
		util.Event(c.logger, slog.LevelWarn, "notify.marshal_failed", "event.name", event.Event, "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	util.Event(c.logger, slog.LevelInfo, "notify.delivery_started",
		"notify.event", event.Event,
		"notify.endpoint", c.endpoint,
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(rawBody))
		if err != nil {
			util.Event(c.logger, slog.LevelWarn, "notify.request_build_failed", "event.name", event.Event, "error", err)
			if c.telemetry != nil {
				c.telemetry.IncWebhookDelivery("failed")
			}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if c.bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.bearerToken)
		}

		resp, err := c.httpClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if c.telemetry != nil {
					c.telemetry.IncWebhookDelivery("success")
				}
				util.Event(c.logger, slog.LevelInfo, "notify.delivered", "notify.event", event.Event, "http.status_code", resp.StatusCode, "attempt", attempt)
				return
			}
			if resp.StatusCode < 500 {
				if c.telemetry != nil {
					c.telemetry.IncWebhookDelivery("failed")
				}
				util.Event(c.logger, slog.LevelWarn, "notify.delivery_failed", "event.name", event.Event, "http.status_code", resp.StatusCode, "attempt", attempt)
				return
			}
			util.Event(c.logger, slog.LevelWarn, "notify.delivery_retry", "event.name", event.Event, "http.status_code", resp.StatusCode, "attempt", attempt)
		} else {
			util.Event(c.logger, slog.LevelWarn, "notify.delivery_retry", "event.name", event.Event, "error", err, "attempt", attempt)
		}
		if attempt < maxAttempts {
			timer := time.NewTimer(retryDelay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				if c.telemetry != nil {
					c.telemetry.IncWebhookDelivery("failed")
				}
				return
			}
		}
	}
	if c.telemetry != nil {
		c.telemetry.IncWebhookDelivery("failed")
	}
	util.Event(c.logger, slog.LevelWarn, "notify.delivery_failed", "event.name", event.Event, "attempts", maxAttempts)
}

func cloneAttributes(attributes map[string]any) map[string]any {
	if len(attributes) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}
