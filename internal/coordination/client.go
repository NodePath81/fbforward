package coordination

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/gorilla/websocket"
)

type Client struct {
	cfg    config.CoordinationConfig
	dialer *websocket.Dialer
}

func NewClient(cfg config.CoordinationConfig) *Client {
	return &Client{
		cfg: cfg,
		dialer: &websocket.Dialer{
			EnableCompression: true,
		},
	}
}

func (c *Client) DialNode(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	wsURL, err := buildNodeURL(c.cfg.Endpoint, c.cfg.Pool)
	if err != nil {
		return nil, nil, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.cfg.Token)
	return c.dialer.DialContext(ctx, wsURL, header)
}

func buildNodeURL(endpoint, pool string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("invalid coordination endpoint: %w", err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid coordination endpoint: missing host")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("invalid coordination endpoint scheme %q", parsed.Scheme)
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	if basePath == "" {
		parsed.Path = "/ws/node"
	} else {
		parsed.Path = basePath + "/ws/node"
	}
	query := parsed.Query()
	query.Set("pool", pool)
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String(), nil
}
