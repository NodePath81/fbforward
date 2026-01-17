package rpc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const defaultRPCTimeout = 10 * time.Second

// RPCClient is a JSON-RPC client
type RPCClient struct {
	conn      net.Conn
	mu        sync.Mutex
	nextID    int
	sessionID string
	stopCh    chan struct{}
	stopOnce  sync.Once
}

// NewRPCClient creates a new RPC client and performs handshake
func NewRPCClient(target string, port int) (*RPCClient, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", target, port), 3*time.Second)
	if err != nil {
		return nil, err
	}

	// Send RPC header to indicate we want JSON-RPC protocol
	rpcHeader := []byte("RPC\x00")
	if _, err := conn.Write(rpcHeader); err != nil {
		conn.Close()
		return nil, err
	}

	client := &RPCClient{
		conn:   conn,
		nextID: 1,
	}

	// Perform session.hello handshake
	helloReq := HelloRequest{
		ClientVersion:     "1.0.0",
		SupportedFeatures: []string{"tcp", "udp", "reverse", "ping"},
		Capabilities: ClientCapabilities{
			MaxBandwidthBps: 10_000_000_000,
			MaxSampleBytes:  1_000_000_000,
		},
	}

	var helloResp HelloResponse
	if err := client.call("session.hello", helloReq, &helloResp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake failed: %w", err)
	}

	client.sessionID = helloResp.SessionID
	if helloResp.HeartbeatIntervalMs > 0 {
		client.startHeartbeat(time.Duration(helloResp.HeartbeatIntervalMs) * time.Millisecond)
	}
	return client, nil
}

// call makes a JSON-RPC call
func (c *RPCClient) call(method string, params interface{}, result interface{}) error {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	// Encode params
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return err
	}

	// Create request
	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
		ID:      id,
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return err
	}

	// Write length-prefixed request
	c.mu.Lock()
	defer c.mu.Unlock()

	deadline := time.Now().Add(defaultRPCTimeout)
	_ = c.conn.SetDeadline(deadline)
	defer func() {
		_ = c.conn.SetDeadline(time.Time{})
	}()

	if err := binary.Write(c.conn, binary.BigEndian, uint32(len(reqJSON))); err != nil {
		return err
	}
	if _, err := c.conn.Write(reqJSON); err != nil {
		return err
	}

	// Read length-prefixed response
	var length uint32
	if err := binary.Read(c.conn, binary.BigEndian, &length); err != nil {
		return err
	}

	if length == 0 || length > 10*1024*1024 {
		return errors.New("invalid response length")
	}

	respBuf := make([]byte, length)
	if _, err := io.ReadFull(c.conn, respBuf); err != nil {
		return err
	}

	// Parse response
	var resp Response
	if err := json.Unmarshal(respBuf, &resp); err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	// Decode result
	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return err
		}
	}

	return nil
}

// SessionID returns the current session ID
func (c *RPCClient) SessionID() string {
	return c.sessionID
}

// StartSample starts a forward sample
func (c *RPCClient) StartSample(sampleID uint32, network string) error {
	req := SampleStartRequest{
		SessionID: c.sessionID,
		SampleID:  sampleID,
		Network:   network,
	}

	var resp SampleStartResponse
	return c.call("sample.start", req, &resp)
}

// StartSampleReverse starts a reverse sample
func (c *RPCClient) StartSampleReverse(sampleID uint32, network string, bandwidthBps float64, chunkSize int64, rttMs int64, sampleBytes int64) error {
	req := SampleStartReverseRequest{
		SessionID:           c.sessionID,
		SampleID:            sampleID,
		Network:             network,
		BandwidthBps:        bandwidthBps,
		ChunkSize:           chunkSize,
		RTTMs:               rttMs,
		SampleBytes:         sampleBytes,
		DataConnectionReady: true,
	}

	var resp SampleStartReverseResponse
	return c.call("sample.start_reverse", req, &resp)
}

// StopSample stops a sample and returns the report
func (c *RPCClient) StopSample(sampleID uint32) (SampleStopResponse, error) {
	req := SampleStopRequest{
		SessionID: c.sessionID,
		SampleID:  sampleID,
	}

	var resp SampleStopResponse
	err := c.call("sample.stop", req, &resp)
	return resp, err
}

// Ping sends a ping for RTT measurement
func (c *RPCClient) Ping() (time.Duration, error) {
	start := time.Now()
	req := PingRequest{
		SessionID: c.sessionID,
		Timestamp: start.UnixNano(),
	}

	var resp PingResponse
	if err := c.call("ping", req, &resp); err != nil {
		return 0, err
	}

	rtt := time.Since(start)
	return rtt, nil
}

// RegisterUDP registers a UDP endpoint
func (c *RPCClient) RegisterUDP(udpPort int) error {
	req := UDPRegisterRequest{
		SessionID:       c.sessionID,
		UDPPort:         udpPort,
		TestPacketCount: 5,
	}

	var resp UDPRegisterResponse
	if err := c.call("udp.register", req, &resp); err != nil {
		return err
	}

	if resp.Status != "registered" {
		return fmt.Errorf("UDP registration failed: %s", resp.Status)
	}

	return nil
}

// Heartbeat sends a heartbeat
func (c *RPCClient) Heartbeat() error {
	req := HeartbeatRequest{
		SessionID: c.sessionID,
		Timestamp: time.Now().UnixNano(),
	}

	var resp HeartbeatResponse
	return c.call("session.heartbeat", req, &resp)
}

// Close closes the session and connection
func (c *RPCClient) Close() error {
	if c.stopCh != nil {
		c.stopOnce.Do(func() {
			close(c.stopCh)
		})
	}
	req := SessionCloseRequest{
		SessionID: c.sessionID,
	}

	var resp SessionCloseResponse
	_ = c.call("session.close", req, &resp)

	return c.conn.Close()
}

func (c *RPCClient) startHeartbeat(interval time.Duration) {
	c.stopCh = make(chan struct{})
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = c.Heartbeat()
			case <-c.stopCh:
				return
			}
		}
	}()
}
