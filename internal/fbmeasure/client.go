package fbmeasure

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"sync"
	"time"
)

type RTTStats struct {
	Min     time.Duration
	Mean    time.Duration
	Max     time.Duration
	Jitter  time.Duration
	Samples int
}

type RetransResult struct {
	BytesSent    uint64
	Retransmits  uint64
	SegmentsSent uint64
	RTT          time.Duration
	RTTVar       time.Duration
}

func (r RetransResult) Rate() float64 {
	if r.SegmentsSent == 0 {
		return 0
	}
	return float64(r.Retransmits) / float64(r.SegmentsSent)
}

type LossResult struct {
	PacketsSent uint64
	PacketsRecv uint64
	PacketsLost uint64
	OutOfOrder  uint64
	LossRate    float64
}

type Client struct {
	conn      net.Conn
	dialAddr  string
	tlsConfig *tls.Config
	mu        sync.Mutex
	nextID    uint64
}

func Dial(ctx context.Context, addr string, security ClientSecurityConfig) (*Client, error) {
	tlsConfig, err := security.TLSConfig(addr)
	if err != nil {
		return nil, err
	}
	conn, err := dialTCP(ctx, addr, tlsConfig)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:      conn,
		dialAddr:  addr,
		tlsConfig: tlsConfig,
		nextID:    1,
	}, nil
}

func dialTCP(ctx context.Context, addr string, tlsConfig *tls.Config) (net.Conn, error) {
	dialer := &net.Dialer{}
	if tlsConfig == nil {
		return dialer.DialContext(ctx, "tcp", addr)
	}
	tlsDialer := &tls.Dialer{
		NetDialer: dialer,
		Config:    tlsConfig.Clone(),
	}
	return tlsDialer.DialContext(ctx, "tcp", addr)
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) nextRequestID() uint64 {
	id := c.nextID
	c.nextID++
	return id
}

func (c *Client) dialAuxTCP(ctx context.Context) (net.Conn, error) {
	return dialTCP(ctx, c.dialAddr, c.tlsConfig)
}

func (c *Client) withLockedCall(ctx context.Context, op string, payload any, sideEffect func() error, decode func(jsonPayload []byte) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("client closed")
	}

	reqID := c.nextRequestID()
	reqPayload, err := marshalPayload(payload)
	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(deadline); err != nil {
			return err
		}
		defer func() {
			_ = c.conn.SetDeadline(time.Time{})
		}()
	}

	if err := writeControlMessage(c.conn, controlRequest{
		ID:      reqID,
		Op:      op,
		Payload: reqPayload,
	}); err != nil {
		return err
	}

	if sideEffect != nil {
		if err := sideEffect(); err != nil {
			_ = c.conn.Close()
			c.conn = nil
			return err
		}
	}

	var resp controlResponse
	if err := readControlMessage(c.conn, &resp); err != nil {
		return err
	}
	if resp.ID != reqID || resp.Op != op {
		return fmt.Errorf("unexpected control response id=%d op=%q", resp.ID, resp.Op)
	}
	if !resp.OK {
		if resp.Error == "" {
			return fmt.Errorf("operation %s failed", op)
		}
		return fmt.Errorf("%s: %s", op, resp.Error)
	}
	if decode != nil {
		return decode(resp.Payload)
	}
	return nil
}

type rttAccumulator struct {
	min     time.Duration
	max     time.Duration
	mean    float64
	m2      float64
	samples int
}

func (a *rttAccumulator) Add(sample time.Duration) {
	value := float64(sample)
	if a.samples == 0 {
		a.min = sample
		a.max = sample
		a.mean = value
		a.samples = 1
		return
	}
	if sample < a.min {
		a.min = sample
	}
	if sample > a.max {
		a.max = sample
	}
	a.samples++
	delta := value - a.mean
	a.mean += delta / float64(a.samples)
	a.m2 += delta * (value - a.mean)
}

func (a *rttAccumulator) Stats() RTTStats {
	if a.samples == 0 {
		return RTTStats{}
	}
	stats := RTTStats{
		Min:     a.min,
		Max:     a.max,
		Mean:    time.Duration(a.mean),
		Samples: a.samples,
	}
	if a.samples > 1 {
		variance := a.m2 / float64(a.samples-1)
		stats.Jitter = time.Duration(math.Sqrt(variance))
	}
	return stats
}

func timeoutMillis(ctx context.Context, fallback time.Duration) int {
	if deadline, ok := ctx.Deadline(); ok {
		d := time.Until(deadline)
		if d <= 0 {
			return 1
		}
		return int(d.Milliseconds())
	}
	return int(fallback.Milliseconds())
}
