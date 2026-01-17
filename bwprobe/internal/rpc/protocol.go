package rpc

import (
	"encoding/json"
)

// Session management
type HelloRequest struct {
	ClientVersion     string             `json:"client_version"`
	SupportedFeatures []string           `json:"supported_features"`
	Capabilities      ClientCapabilities `json:"capabilities"`
}

type ClientCapabilities struct {
	MaxBandwidthBps int64 `json:"max_bandwidth_bps"`
	MaxSampleBytes  int64 `json:"max_sample_bytes"`
}

type HelloResponse struct {
	ServerVersion       string             `json:"server_version"`
	SessionID           string             `json:"session_id"` // UUID
	SupportedFeatures   []string           `json:"supported_features"`
	Capabilities        ServerCapabilities `json:"capabilities"`
	HeartbeatIntervalMs int                `json:"heartbeat_interval_ms"`
}

type ServerCapabilities struct {
	MaxBandwidthBps    int64    `json:"max_bandwidth_bps"`
	MaxSampleBytes     int64    `json:"max_sample_bytes"`
	IntervalDurationMs int      `json:"interval_duration_ms"`
	SupportedNetworks  []string `json:"supported_networks"`
}

// Heartbeat
type HeartbeatRequest struct {
	SessionID string `json:"session_id"`
	Timestamp int64  `json:"timestamp"` // nanoseconds
}

type HeartbeatResponse struct {
	Timestamp  int64 `json:"timestamp"`   // echo client timestamp
	ServerTime int64 `json:"server_time"` // server timestamp
}

// UDP registration
type UDPRegisterRequest struct {
	SessionID       string `json:"session_id"`
	UDPPort         int    `json:"udp_port"`
	TestPacketCount int    `json:"test_packet_count"`
}

type UDPRegisterResponse struct {
	Status              string `json:"status"` // "registered" or "failed"
	ServerWillSendTo    string `json:"server_will_send_to"`
	TestPacketsReceived int    `json:"test_packets_received"`
}

// Sample operations
type SampleStartRequest struct {
	SessionID string `json:"session_id"`
	SampleID  uint32 `json:"sample_id"`
	Network   string `json:"network"` // "tcp" or "udp"
}

type SampleStartResponse struct {
	SampleID  uint32 `json:"sample_id"`
	StartedAt string `json:"started_at"` // ISO8601
	Ready     bool   `json:"ready"`
}

type SampleStartReverseRequest struct {
	SessionID           string  `json:"session_id"`
	SampleID            uint32  `json:"sample_id"`
	Network             string  `json:"network"`
	BandwidthBps        float64 `json:"bandwidth_bps"`
	ChunkSize           int64   `json:"chunk_size"`
	RTTMs               int64   `json:"rtt_ms"`
	SampleBytes         int64   `json:"sample_bytes"`
	DataConnectionReady bool    `json:"data_connection_ready"`
}

type SampleStartReverseResponse struct {
	SampleID    uint32 `json:"sample_id"`
	StartedAt   string `json:"started_at"`
	ServerReady bool   `json:"server_ready"`
}

type SampleStopRequest struct {
	SessionID string `json:"session_id"`
	SampleID  uint32 `json:"sample_id"`
}

type IntervalReport struct {
	Bytes      uint64 `json:"bytes"`
	DurationMs int64  `json:"duration_ms"`
	OOOCount   uint64 `json:"ooo_count"`
}

type SampleStopResponse struct {
	SampleID           uint32           `json:"sample_id"`
	TotalBytes         uint64           `json:"total_bytes"`
	TotalDuration      float64          `json:"total_duration"`
	Intervals          []IntervalReport `json:"intervals"`
	FirstByteTime      string           `json:"first_byte_time"`
	LastByteTime       string           `json:"last_byte_time"`
	AvgThroughputBps   float64          `json:"avg_throughput_bps,omitempty"`
	PacketsRecv        uint64           `json:"packets_recv,omitempty"`
	PacketsLost        uint64           `json:"packets_lost,omitempty"`
	TCPSendBufferBytes uint64           `json:"tcp_send_buffer_bytes,omitempty"`
	TCPRetransmits     uint64           `json:"tcp_retransmits,omitempty"`
	TCPSegmentsSent    uint64           `json:"tcp_segments_sent,omitempty"`
}

// Ping
type PingRequest struct {
	SessionID string `json:"session_id"`
	Timestamp int64  `json:"timestamp"`
}

type PingResponse struct {
	Timestamp  int64 `json:"timestamp"`
	ServerTime int64 `json:"server_time"`
}

// Server info
type ServerInfoRequest struct{}

type ServerInfoResponse struct {
	Version        string             `json:"version"`
	UptimeSeconds  int64              `json:"uptime_seconds"`
	ActiveSessions int                `json:"active_sessions"`
	Capabilities   ServerCapabilities `json:"capabilities"`
}

// Session close
type SessionCloseRequest struct {
	SessionID string `json:"session_id"`
}

type SessionCloseResponse struct {
	Status          string `json:"status"`
	SessionsCleaned int    `json:"sessions_cleaned"`
}

// JSON-RPC 2.0 envelope
type Request struct {
	JSONRPC string          `json:"jsonrpc"` // must be "2.0"
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      interface{}     `json:"id"`
}

type Error struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Error implements the error interface
func (e *Error) Error() string {
	return e.Message
}

// NewRPCError creates a new RPC error
func NewRPCError(code int, message string, data interface{}) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Data:    data,
	}
}

// Standard error codes
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603

	// Application errors
	ErrServerError         = -32000
	ErrSampleAlreadyActive = -32001
	ErrSampleNotFound      = -32002
	ErrSampleIDMismatch    = -32003
	ErrInvalidNetwork      = -32004
	ErrInvalidBandwidth    = -32005
	ErrInvalidSampleSize   = -32006
	ErrReverseNotAvailable = -32007
	ErrConnectionTimeout   = -32008
	ErrRateLimitExceeded   = -32009
	ErrInvalidSession      = -32010
	ErrSessionExpired      = -32011
	ErrUDPNotRegistered    = -32012
)
