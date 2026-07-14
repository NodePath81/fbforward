// Package flowcontextclient provides a small backend-side client for the
// fbforward Flow Context HTTP API.
package flowcontextclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var (
	ErrUnknownInstance = errors.New("connection source is not a configured fbforward instance")
	ErrFlowNotFound    = errors.New("flow was not found on the selected fbforward")
	ErrUnauthorized    = errors.New("flow context authentication failed")
	ErrForbidden       = errors.New("flow context access denied")
	ErrFlowNotActive   = errors.New("flow context flow is not active")
	ErrRateLimited     = errors.New("flow context rate limited")
	ErrUnavailable     = errors.New("flow context service unavailable")
	ErrInvalidRequest  = errors.New("invalid flow context request")
	ErrInvalidResponse = errors.New("invalid flow context response")
)

const (
	resolvePath = "/flow-context/resolve"
	rpcPath     = "/flow-context/rpc"
	maxBodySize = 1 << 20
)

// HTTPDoer is the only transport seam needed by the client. http.Client
// satisfies it, while httptest clients can be supplied by unit tests.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	Endpoint   string
	Token      string
	BackendKey string
	Timeout    time.Duration
	// ResolveWait is the server-side wait for a tuple that has not been bound
	// yet. Zero uses the default of 100ms.
	ResolveWait time.Duration
	HTTPClient  HTTPDoer
}

type Client struct {
	endpoint    string
	token       string
	backendKey  string
	timeout     time.Duration
	resolveWait int
	httpClient  HTTPDoer
}

type Flow struct {
	ID         string
	Protocol   string
	ClientAddr netip.AddrPort
	Listener   string
	Route      string
	Upstream   string
	State      string
}

type Tuple struct {
	Protocol   string
	BackendKey string
	LocalAddr  netip.AddrPort
	RemoteAddr netip.AddrPort
}

type Tag struct {
	Namespace string
	Key       string
	Value     string
	TTL       time.Duration
}

type resolveRequest struct {
	Protocol   string `json:"protocol"`
	BackendKey string `json:"backend_key"`
	LocalAddr  string `json:"local_addr"`
	RemoteAddr string `json:"remote_addr"`
	WaitMS     int    `json:"wait_ms,omitempty"`
}

type resolveEnvelope struct {
	OK    bool      `json:"ok"`
	Flow  *flowWire `json:"flow,omitempty"`
	Error string    `json:"error,omitempty"`
}

type flowWire struct {
	ID         string `json:"flow_id"`
	Protocol   string `json:"protocol"`
	ClientAddr string `json:"client_addr"`
	Listener   string `json:"listener"`
	Route      string `json:"route"`
	Upstream   string `json:"upstream"`
	State      string `json:"state"`
}

type rpcRequest struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcEnvelope struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// New creates a client for one fbforward instance. The returned client is
// immutable and may be shared by concurrent callers.
func New(options Options) (*Client, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(options.Endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid endpoint", ErrInvalidRequest)
	}
	if strings.TrimSpace(options.Token) == "" || strings.TrimSpace(options.BackendKey) == "" {
		return nil, fmt.Errorf("%w: token and backend key are required", ErrInvalidRequest)
	}
	if options.Timeout <= 0 {
		options.Timeout = 500 * time.Millisecond
	}
	if options.ResolveWait == 0 {
		options.ResolveWait = 100 * time.Millisecond
	}
	if options.ResolveWait < 0 || options.ResolveWait > 5*time.Second {
		return nil, fmt.Errorf("%w: resolve wait must be between 0 and 5s", ErrInvalidRequest)
	}
	return &Client{
		endpoint:    endpoint,
		token:       options.Token,
		backendKey:  options.BackendKey,
		timeout:     options.Timeout,
		resolveWait: int(options.ResolveWait / time.Millisecond),
		httpClient:  chooseHTTPClient(options.HTTPClient),
	}, nil
}

func chooseHTTPClient(client HTTPDoer) HTTPDoer {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

// ResolveTuple resolves a tuple already expressed from fbforward's socket
// perspective.
func (c *Client) ResolveTuple(ctx context.Context, tuple Tuple) (Flow, error) {
	if c == nil || !tuple.LocalAddr.IsValid() || !tuple.RemoteAddr.IsValid() || strings.TrimSpace(tuple.BackendKey) == "" {
		return Flow{}, ErrInvalidRequest
	}
	if tuple.Protocol != "tcp" && tuple.Protocol != "udp" {
		return Flow{}, ErrInvalidRequest
	}
	return c.resolve(ctx, resolveRequest{
		Protocol: tuple.Protocol, BackendKey: tuple.BackendKey,
		LocalAddr: tuple.LocalAddr.String(), RemoteAddr: tuple.RemoteAddr.String(),
		WaitMS: c.resolveWait,
	})
}

func (c *Client) resolve(ctx context.Context, payload resolveRequest) (Flow, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Flow{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	response, err := c.do(ctx, http.MethodPost, resolvePath, body)
	if err != nil {
		return Flow{}, err
	}
	var envelope resolveEnvelope
	if err := decodeLimited(response, &envelope); err != nil {
		return Flow{}, decodeError(err)
	}
	if response.StatusCode != http.StatusOK {
		return Flow{}, responseError(response.StatusCode, envelope.Error)
	}
	if !envelope.OK || envelope.Flow == nil {
		return Flow{}, fmt.Errorf("%w: missing flow", ErrInvalidResponse)
	}
	return convertFlow(*envelope.Flow)
}

func convertFlow(value flowWire) (Flow, error) {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.Protocol) == "" {
		return Flow{}, fmt.Errorf("%w: missing flow identity", ErrInvalidResponse)
	}
	clientAddr, err := netip.ParseAddrPort(value.ClientAddr)
	if err != nil {
		return Flow{}, fmt.Errorf("%w: invalid client address", ErrInvalidResponse)
	}
	return Flow{ID: value.ID, Protocol: value.Protocol, ClientAddr: clientAddr, Listener: value.Listener, Route: value.Route, Upstream: value.Upstream, State: value.State}, nil
}

func (c *Client) SetFlowTag(ctx context.Context, flowID string, tag Tag) error {
	return c.setTag(ctx, "SetFlowTag", flowID, tag)
}

func (c *Client) SetClientTag(ctx context.Context, flowID string, tag Tag) error {
	return c.setTag(ctx, "SetClientTag", flowID, tag)
}

func (c *Client) UnsetFlowTag(ctx context.Context, flowID, namespace, key string) error {
	return c.setTag(ctx, "UnsetFlowTag", flowID, Tag{Namespace: namespace, Key: key})
}

// UnsetClientTag removes a client tag from the selected fbforward instance.
func (c *Client) UnsetClientTag(ctx context.Context, flowID, namespace, key string) error {
	return c.setTag(ctx, "UnsetClientTag", flowID, Tag{Namespace: namespace, Key: key})
}

func (c *Client) SetFlowLimit(ctx context.Context, flowID string, rateBPS uint64) error {
	if c == nil || strings.TrimSpace(flowID) == "" || rateBPS == 0 {
		return ErrInvalidRequest
	}
	return c.callRPC(ctx, "SetFlowLimit", map[string]any{"flow_id": flowID, "rate_bps": rateBPS})
}

func (c *Client) ClearFlowLimit(ctx context.Context, flowID string) error {
	if c == nil || strings.TrimSpace(flowID) == "" {
		return ErrInvalidRequest
	}
	return c.callRPC(ctx, "ClearFlowLimit", map[string]any{"flow_id": flowID})
}

func (c *Client) BlockFlow(ctx context.Context, flowID, reason string) error {
	if c == nil || strings.TrimSpace(flowID) == "" || len(reason) > 256 || strings.ContainsAny(reason, "\r\n") {
		return ErrInvalidRequest
	}
	return c.callRPC(ctx, "BlockFlow", map[string]any{"flow_id": flowID, "reason": reason})
}

func (c *Client) callRPC(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(rpcRequest{Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	response, err := c.do(ctx, http.MethodPost, rpcPath, body)
	if err != nil {
		return err
	}
	var envelope rpcEnvelope
	if err := decodeLimited(response, &envelope); err != nil {
		return decodeError(err)
	}
	if response.StatusCode != http.StatusOK {
		return responseError(response.StatusCode, envelope.Error)
	}
	if !envelope.OK {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, envelope.Error)
	}
	return nil
}

func (c *Client) setTag(ctx context.Context, method, flowID string, tag Tag) error {
	if c == nil || strings.TrimSpace(flowID) == "" || strings.TrimSpace(tag.Namespace) == "" || strings.TrimSpace(tag.Key) == "" || tag.TTL < 0 || tag.TTL%time.Second != 0 {
		return ErrInvalidRequest
	}
	params := map[string]any{"flow_id": flowID, "namespace": tag.Namespace, "key": tag.Key}
	if tag.Value != "" {
		params["value"] = tag.Value
	}
	if tag.TTL > 0 {
		params["ttl_seconds"] = int64(tag.TTL / time.Second)
	}
	body, err := json.Marshal(rpcRequest{Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	response, err := c.do(ctx, http.MethodPost, rpcPath, body)
	if err != nil {
		return err
	}
	var envelope rpcEnvelope
	if err := decodeLimited(response, &envelope); err != nil {
		return decodeError(err)
	}
	if response.StatusCode != http.StatusOK {
		return responseError(response.StatusCode, envelope.Error)
	}
	if !envelope.OK {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, envelope.Error)
	}
	return nil
}

type clientResponse struct {
	StatusCode int
	Body       io.ReadCloser
	cancel     context.CancelFunc
}

func (r *clientResponse) close() error {
	if r == nil {
		return nil
	}
	var err error
	if r.Body != nil {
		err = r.Body.Close()
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return err
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*clientResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	request, err := http.NewRequestWithContext(requestCtx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if response == nil || response.Body == nil {
		cancel()
		return nil, fmt.Errorf("%w: empty HTTP response", ErrInvalidResponse)
	}
	return &clientResponse{StatusCode: response.StatusCode, Body: response.Body, cancel: cancel}, nil
}

func decodeLimited(response *clientResponse, target any) error {
	defer response.close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxBodySize))
	return decoder.Decode(target)
}

func decodeError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return fmt.Errorf("%w: %v", ErrInvalidResponse, err)
}

func responseError(status int, message string) error {
	base := ErrUnavailable
	switch status {
	case http.StatusNotFound:
		base = ErrFlowNotFound
	case http.StatusUnauthorized:
		base = ErrUnauthorized
	case http.StatusForbidden:
		base = ErrForbidden
	case http.StatusConflict:
		base = ErrFlowNotActive
	case http.StatusGone:
		base = ErrFlowNotFound
	case http.StatusTooManyRequests:
		base = ErrRateLimited
	case http.StatusBadRequest:
		base = ErrInvalidRequest
	}
	if message == "" {
		return base
	}
	return fmt.Errorf("%w: %s", base, message)
}
