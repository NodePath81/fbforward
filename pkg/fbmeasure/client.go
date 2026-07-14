package fbmeasure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	defaultTimeout = 2 * time.Second
	minTimeout     = 100 * time.Millisecond
	maxTimeout     = 10 * time.Second
	sampleCount    = 3
)

type ClientConfig struct {
	Address string
	Timeout time.Duration
}

type Client struct {
	address string
	timeout time.Duration

	mu     sync.Mutex
	closed bool
}

func NewClient(config ClientConfig) (*Client, error) {
	if config.Address == "" {
		return nil, errors.New("fbmeasure address must not be empty")
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.Timeout < minTimeout || config.Timeout > maxTimeout {
		return nil, fmt.Errorf("fbmeasure timeout must be between %s and %s", minTimeout, maxTimeout)
	}
	return &Client{address: config.Address, timeout: config.Timeout}, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func (c *Client) isClosed() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Client) operationContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if c == nil || c.isClosed() {
		return nil, nil, errors.New("fbmeasure client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	operationCtx, cancel := context.WithTimeout(ctx, c.timeout)
	return operationCtx, cancel, nil
}

func (c *Client) ProbeTCP(ctx context.Context) (Result, error) {
	result := Result{Protocol: ProtocolTCP, ObservedAt: time.Now().UTC()}
	opCtx, cancel, err := c.operationContext(ctx)
	if err != nil {
		return result, err
	}
	defer cancel()

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(opCtx, "tcp", c.address)
	if err != nil {
		return result, err
	}
	defer conn.Close()

	var minRTT time.Duration
	var lastErr error
	for sequence := uint64(0); sequence < sampleCount; sequence++ {
		probe, frameErr := newProbeFrame(sequence)
		if frameErr != nil {
			return result, frameErr
		}
		if deadline, ok := opCtx.Deadline(); ok {
			if err := conn.SetDeadline(deadline); err != nil {
				return result, err
			}
		}
		started := time.Now()
		if err := writeFull(conn, probe[:]); err != nil {
			lastErr = err
			break
		}
		var response [frameSize]byte
		if _, err := io.ReadFull(conn, response[:]); err != nil {
			lastErr = err
			break
		}
		decoded, err := parseFrame(response[:])
		if err != nil {
			lastErr = err
			break
		}
		if decoded != probe {
			lastErr = errors.New("fbmeasure TCP echo mismatch")
			break
		}
		rtt := time.Since(started)
		if minRTT == 0 || rtt < minRTT {
			minRTT = rtt
		}
	}
	if minRTT > 0 {
		result.Reachable = true
		result.RTT = minRTT
		result.ObservedAt = time.Now().UTC()
		return result, nil
	}
	if lastErr == nil {
		lastErr = opCtx.Err()
	}
	return result, lastErr
}

func (c *Client) ProbeUDP(ctx context.Context) (Result, error) {
	result := Result{Protocol: ProtocolUDP, ObservedAt: time.Now().UTC()}
	opCtx, cancel, err := c.operationContext(ctx)
	if err != nil {
		return result, err
	}
	defer cancel()

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(opCtx, "udp", c.address)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if deadline, ok := opCtx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return result, err
		}
	}

	var minRTT time.Duration
	var lastErr error
	for sequence := uint64(0); sequence < sampleCount; sequence++ {
		probe, frameErr := newProbeFrame(sequence)
		if frameErr != nil {
			return result, frameErr
		}
		started := time.Now()
		if err := writeFull(conn, probe[:]); err != nil {
			lastErr = err
			break
		}
		for {
			var response [64 * 1024]byte
			n, err := conn.Read(response[:])
			if err != nil {
				lastErr = err
				break
			}
			decoded, err := parseFrame(response[:n])
			if err != nil || decoded != probe {
				if err != nil {
					lastErr = err
				} else {
					lastErr = errors.New("fbmeasure UDP echo mismatch")
				}
				continue
			}
			rtt := time.Since(started)
			if minRTT == 0 || rtt < minRTT {
				minRTT = rtt
			}
			break
		}
		if opCtx.Err() != nil {
			break
		}
	}
	if minRTT > 0 {
		result.Reachable = true
		result.RTT = minRTT
		result.ObservedAt = time.Now().UTC()
		return result, nil
	}
	if lastErr == nil {
		lastErr = opCtx.Err()
	}
	return result, lastErr
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
