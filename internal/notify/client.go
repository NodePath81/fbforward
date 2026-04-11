package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
)

const (
	defaultQueueSize = 256
	defaultTimeout   = 3 * time.Second
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

type Event struct {
	SchemaVersion int            `json:"schema_version"`
	EventName     string         `json:"event_name"`
	Severity      Severity       `json:"severity"`
	Timestamp     string         `json:"timestamp"`
	Source        EventSource    `json:"source"`
	Attributes    map[string]any `json:"attributes"`
}

type EventSource struct {
	Service  string `json:"service"`
	Instance string `json:"instance"`
}

type Emitter interface {
	Emit(eventName string, severity Severity, attributes map[string]any) bool
}

type Config struct {
	Endpoint       string
	KeyID          string
	Token          string
	SourceService  string
	SourceInstance string
	QueueSize      int
	Timeout        time.Duration
	Now            func() time.Time
	HTTPClient     *http.Client
	Logger         util.Logger
}

type Client struct {
	endpoint       string
	keyID          string
	token          string
	sourceService  string
	sourceInstance string
	timeout        time.Duration
	now            func() time.Time
	httpClient     *http.Client
	logger         util.Logger

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
		keyID:          cfg.KeyID,
		token:          cfg.Token,
		sourceService:  cfg.SourceService,
		sourceInstance: cfg.SourceInstance,
		timeout:        timeout,
		now:            nowFn,
		httpClient:     client,
		logger:         cfg.Logger,
		queue:          make(chan Event, queueSize),
	}
	c.wg.Add(1)
	go c.run()
	return c, nil
}

func (c *Client) Emit(eventName string, severity Severity, attributes map[string]any) bool {
	now := c.now().UTC()
	event := Event{
		SchemaVersion: 1,
		EventName:     eventName,
		Severity:      severity,
		Timestamp:     now.Format(time.RFC3339Nano),
		Source: EventSource{
			Service:  c.sourceService,
			Instance: c.sourceInstance,
		},
		Attributes: cloneAttributes(attributes),
	}
	util.Event(c.logger, slog.LevelInfo, "notify.triggered",
		"notify.event_name", event.EventName,
		"notify.severity", event.Severity,
		"source.service", event.Source.Service,
		"source.instance", event.Source.Instance,
	)
	submitted := c.Submit(event)
	if submitted {
		util.Event(c.logger, slog.LevelInfo, "notify.enqueued",
			"notify.event_name", event.EventName,
			"notify.severity", event.Severity,
		)
		return true
	}
	util.Event(c.logger, slog.LevelWarn, "notify.enqueue_failed",
		"notify.event_name", event.EventName,
		"notify.severity", event.Severity,
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
		util.Event(c.logger, slog.LevelWarn, "notify.marshal_failed", "event.name", event.EventName, "error", err)
		return
	}

	headerTimestamp := strconv.FormatInt(c.now().Unix(), 10)
	signature := sign(headerTimestamp+"."+string(rawBody), c.token)

	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(rawBody))
	if err != nil {
		util.Event(c.logger, slog.LevelWarn, "notify.request_build_failed", "event.name", event.EventName, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-FBNotify-Key-Id", c.keyID)
	req.Header.Set("X-FBNotify-Timestamp", headerTimestamp)
	req.Header.Set("X-FBNotify-Signature", signature)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	req = req.WithContext(ctx)
	util.Event(c.logger, slog.LevelInfo, "notify.delivery_started",
		"notify.event_name", event.EventName,
		"notify.endpoint", c.endpoint,
	)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		util.Event(c.logger, slog.LevelWarn, "notify.delivery_failed", "event.name", event.EventName, "error", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		util.Event(c.logger, slog.LevelWarn, "notify.delivery_failed", "event.name", event.EventName, "http.status_code", resp.StatusCode)
		return
	}
	util.Event(c.logger, slog.LevelInfo, "notify.delivered", "notify.event_name", event.EventName, "http.status_code", resp.StatusCode)
}

func sign(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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
