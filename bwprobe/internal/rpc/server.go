package rpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	serverVersion       = "1.0.0"
	maxBandwidthBps     = 10_000_000_000 // 10 Gbps
	maxSampleBytes      = 1_000_000_000  // 1 GB
	heartbeatIntervalMs = 30000          // 30 seconds
	maxRPCMessageSize   = 10 * 1024 * 1024
)

// RPCServer handles JSON-RPC requests
type RPCServer struct {
	sessionMgr *SessionManager
	udpConn    *net.UDPConn
	startTime  time.Time
}

// NewRPCServer creates a new RPC server
func NewRPCServer(sessionMgr *SessionManager, udpConn *net.UDPConn) *RPCServer {
	return &RPCServer{
		sessionMgr: sessionMgr,
		udpConn:    udpConn,
		startTime:  time.Now(),
	}
}

// Handle processes JSON-RPC requests on a connection
func (s *RPCServer) Handle(ctx context.Context, conn net.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read length-prefixed message
		var length uint32
		if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		if length == 0 || length > maxRPCMessageSize {
			return errors.New("invalid message length")
		}

		// Read JSON-RPC message
		msgBuf := make([]byte, length)
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			return err
		}

		// Process request and get response
		respBuf, err := s.processRequest(msgBuf, conn.RemoteAddr())
		if err != nil {
			return err
		}

		// Write length-prefixed response
		respLength := uint32(len(respBuf))
		if err := binary.Write(conn, binary.BigEndian, respLength); err != nil {
			return err
		}
		if _, err := conn.Write(respBuf); err != nil {
			return err
		}
	}
}

// processRequest handles a single JSON-RPC request
func (s *RPCServer) processRequest(msgBuf []byte, clientAddr net.Addr) ([]byte, error) {
	var req Request
	if err := json.Unmarshal(msgBuf, &req); err != nil {
		return s.errorResponse(nil, ErrParseError, "Parse error", nil)
	}

	if req.JSONRPC != "2.0" {
		return s.errorResponse(req.ID, ErrInvalidRequest, "Invalid Request", nil)
	}

	// Dispatch to method handler
	result, rpcErr := s.dispatch(req.Method, req.Params, clientAddr)
	if rpcErr != nil {
		return s.errorResponse(req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
	}

	// Encode result
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return s.errorResponse(req.ID, ErrInternalError, "Internal error", nil)
	}

	resp := Response{
		JSONRPC: "2.0",
		Result:  resultJSON,
		ID:      req.ID,
	}

	return json.Marshal(resp)
}

// dispatch routes requests to appropriate handlers
func (s *RPCServer) dispatch(method string, params json.RawMessage, clientAddr net.Addr) (interface{}, *Error) {
	switch method {
	case "session.hello":
		return s.handleHello(params, clientAddr)
	case "session.heartbeat":
		return s.handleHeartbeat(params)
	case "session.close":
		return s.handleSessionClose(params)
	case "sample.start":
		return s.handleSampleStart(params)
	case "sample.start_reverse":
		return s.handleSampleStartReverse(params)
	case "sample.stop":
		return s.handleSampleStop(params)
	case "ping":
		return s.handlePing(params)
	case "server.info":
		return s.handleServerInfo(params)
	case "udp.register":
		return s.handleUDPRegister(params, clientAddr)
	default:
		return nil, NewRPCError(ErrMethodNotFound, "Method not found", nil)
	}
}

// handleHello creates a new session
func (s *RPCServer) handleHello(params json.RawMessage, clientAddr net.Addr) (interface{}, *Error) {
	var req HelloRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewRPCError(ErrInvalidParams, "Invalid params", nil)
	}

	// Create new session
	session := s.sessionMgr.CreateSession(clientAddr, req.Capabilities)

	return HelloResponse{
		ServerVersion:     serverVersion,
		SessionID:         session.sessionID,
		SupportedFeatures: []string{"tcp", "udp", "reverse", "ping"},
		Capabilities: ServerCapabilities{
			MaxBandwidthBps:    maxBandwidthBps,
			MaxSampleBytes:     maxSampleBytes,
			IntervalDurationMs: 100,
			SupportedNetworks:  []string{"tcp", "udp"},
		},
		HeartbeatIntervalMs: heartbeatIntervalMs,
	}, nil
}

// handleHeartbeat updates session liveness
func (s *RPCServer) handleHeartbeat(params json.RawMessage) (interface{}, *Error) {
	var req HeartbeatRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewRPCError(ErrInvalidParams, "Invalid params", nil)
	}

	if err := s.sessionMgr.UpdateHeartbeat(req.SessionID); err != nil {
		if rpcErr, ok := err.(*Error); ok {
			return nil, rpcErr
		}
		return nil, NewRPCError(ErrInvalidSession, "Invalid session", nil)
	}

	return HeartbeatResponse{
		Timestamp:  req.Timestamp,
		ServerTime: time.Now().UnixNano(),
	}, nil
}

// handleSessionClose closes a session
func (s *RPCServer) handleSessionClose(params json.RawMessage) (interface{}, *Error) {
	var req SessionCloseRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewRPCError(ErrInvalidParams, "Invalid params", nil)
	}

	if err := s.sessionMgr.DeleteSession(req.SessionID); err != nil {
		if rpcErr, ok := err.(*Error); ok {
			return nil, rpcErr
		}
		return nil, NewRPCError(ErrInvalidSession, "Invalid session", nil)
	}

	return SessionCloseResponse{
		Status:          "closed",
		SessionsCleaned: 1,
	}, nil
}

// handleSampleStart starts a forward sample
func (s *RPCServer) handleSampleStart(params json.RawMessage) (interface{}, *Error) {
	var req SampleStartRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewRPCError(ErrInvalidParams, "Invalid params", nil)
	}

	session, ok := s.sessionMgr.GetSession(req.SessionID)
	if !ok {
		return nil, NewRPCError(ErrInvalidSession, "Session not found", nil)
	}

	if session.IsActive() {
		return nil, NewRPCError(ErrSampleAlreadyActive, "Sample already active", nil)
	}

	if req.Network != "tcp" && req.Network != "udp" {
		return nil, NewRPCError(ErrInvalidNetwork, "Invalid network type", nil)
	}

	session.StartSample(req.SampleID)

	return SampleStartResponse{
		SampleID:  req.SampleID,
		StartedAt: time.Now().Format(time.RFC3339Nano),
		Ready:     true,
	}, nil
}

// handleSampleStartReverse starts a reverse sample
func (s *RPCServer) handleSampleStartReverse(params json.RawMessage) (interface{}, *Error) {
	var req SampleStartReverseRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewRPCError(ErrInvalidParams, "Invalid params", nil)
	}

	session, ok := s.sessionMgr.GetSession(req.SessionID)
	if !ok {
		return nil, NewRPCError(ErrInvalidSession, "Session not found", nil)
	}

	if session.IsActive() {
		return nil, NewRPCError(ErrSampleAlreadyActive, "Sample already active", nil)
	}

	if req.Network != "tcp" && req.Network != "udp" {
		return nil, NewRPCError(ErrInvalidNetwork, "Invalid network type", nil)
	}

	// Validate parameters
	if req.BandwidthBps <= 0 || req.BandwidthBps > float64(maxBandwidthBps) {
		return nil, NewRPCError(ErrInvalidBandwidth, "Invalid bandwidth", nil)
	}

	if req.SampleBytes <= 0 || req.SampleBytes > maxSampleBytes {
		return nil, NewRPCError(ErrInvalidSampleSize, "Invalid sample size", nil)
	}

	// For UDP reverse, verify UDP endpoint is registered
	if req.Network == "udp" {
		if _, registered := session.GetUDPEndpoint(); !registered {
			return nil, NewRPCError(ErrUDPNotRegistered, "UDP endpoint not registered", nil)
		}
	}

	// For TCP reverse, verify reverse connection is ready
	if req.Network == "tcp" && !req.DataConnectionReady {
		return nil, NewRPCError(ErrReverseNotAvailable, "Data connection not ready", nil)
	}

	cfg := ReverseConfig{
		BandwidthBps: req.BandwidthBps,
		ChunkSize:    req.ChunkSize,
		RTT:          time.Duration(req.RTTMs) * time.Millisecond,
		SampleBytes:  req.SampleBytes,
	}
	if req.Network == "udp" {
		if err := session.StartReverseUDP(req.SampleID, s.udpConn, cfg); err != nil {
			return nil, err
		}
	} else {
		if err := session.StartReverseTCP(req.SampleID, cfg); err != nil {
			return nil, err
		}
	}

	return SampleStartReverseResponse{
		SampleID:    req.SampleID,
		StartedAt:   time.Now().Format(time.RFC3339Nano),
		ServerReady: true,
	}, nil
}

// handleSampleStop stops a sample and returns report
func (s *RPCServer) handleSampleStop(params json.RawMessage) (interface{}, *Error) {
	var req SampleStopRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewRPCError(ErrInvalidParams, "Invalid params", nil)
	}

	session, ok := s.sessionMgr.GetSession(req.SessionID)
	if !ok {
		return nil, NewRPCError(ErrInvalidSession, "Session not found", nil)
	}

	var (
		report SampleStopResponse
		err    error
	)
	if session.IsReverseActive() {
		report, err = session.StopReverse(req.SampleID)
	} else {
		if s.sessionMgr.recvWait > 0 && session.ShouldWait() {
			time.Sleep(s.sessionMgr.recvWait)
		}
		report, err = session.StopSample(req.SampleID)
	}
	if err != nil {
		if rpcErr, ok := err.(*Error); ok {
			return nil, rpcErr
		}
		return nil, NewRPCError(ErrInternalError, "Failed to stop sample", nil)
	}

	return report, nil
}

// handlePing responds to ping for RTT measurement
func (s *RPCServer) handlePing(params json.RawMessage) (interface{}, *Error) {
	var req PingRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewRPCError(ErrInvalidParams, "Invalid params", nil)
	}

	// Verify session exists (optional - could also allow ping without session)
	if req.SessionID != "" {
		if _, ok := s.sessionMgr.GetSession(req.SessionID); !ok {
			return nil, NewRPCError(ErrInvalidSession, "Session not found", nil)
		}
	}

	return PingResponse{
		Timestamp:  req.Timestamp,
		ServerTime: time.Now().UnixNano(),
	}, nil
}

// handleServerInfo returns server status
func (s *RPCServer) handleServerInfo(params json.RawMessage) (interface{}, *Error) {
	return ServerInfoResponse{
		Version:        serverVersion,
		UptimeSeconds:  int64(time.Since(s.startTime).Seconds()),
		ActiveSessions: s.sessionMgr.ActiveSessionCount(),
		Capabilities: ServerCapabilities{
			MaxBandwidthBps:    maxBandwidthBps,
			MaxSampleBytes:     maxSampleBytes,
			IntervalDurationMs: 100,
			SupportedNetworks:  []string{"tcp", "udp"},
		},
	}, nil
}

// handleUDPRegister registers and validates UDP endpoint
func (s *RPCServer) handleUDPRegister(params json.RawMessage, clientAddr net.Addr) (interface{}, *Error) {
	var req UDPRegisterRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewRPCError(ErrInvalidParams, "Invalid params", nil)
	}

	session, ok := s.sessionMgr.GetSession(req.SessionID)
	if !ok {
		return nil, NewRPCError(ErrInvalidSession, "Session not found", nil)
	}

	// Extract client IP from control connection
	host, _, err := net.SplitHostPort(clientAddr.String())
	if err != nil {
		return nil, NewRPCError(ErrServerError, "Failed to parse client address", nil)
	}

	// Construct UDP address
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, req.UDPPort))
	if err != nil {
		return nil, NewRPCError(ErrServerError, "Failed to resolve UDP address", nil)
	}

	if !s.sessionMgr.RecentUDPPing(udpAddr, 15*time.Second) {
		return nil, NewRPCError(ErrUDPNotRegistered, "UDP validation failed", nil)
	}

	// Register the UDP endpoint
	session.RegisterUDPEndpoint(udpAddr)

	return UDPRegisterResponse{
		Status:              "registered",
		ServerWillSendTo:    udpAddr.String(),
		TestPacketsReceived: 1,
	}, nil
}

// errorResponse creates an error response
func (s *RPCServer) errorResponse(id interface{}, code int, message string, data interface{}) ([]byte, error) {
	resp := Response{
		JSONRPC: "2.0",
		Error: &Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}
	return json.Marshal(resp)
}
