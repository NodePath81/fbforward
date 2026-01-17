# BWProbe Protocol Refinement Plan (Revised)

## Executive Summary

This document analyzes the current client-server communication pattern in bwprobe and proposes a cleaner, more robust RPC-style protocol with explicit session management. The current implementation uses a mix of text-based commands and binary data channels with **critical architectural issues**: no session binding between control and data connections, NAT collision risks, and race conditions in reverse mode.

**Key Improvements**:
1. **Session-bound architecture**: Explicit session IDs binding all connections
2. **JSON-RPC 2.0 control channel**: Structured, versioned, extensible protocol
3. **UDP registration handshake**: Eliminates IP parsing and race conditions
4. **Heartbeat mechanism**: Detects half-open connections
5. **Enhanced data frames**: Session ID in all data channel frames

## Current Communication Pattern Analysis

### Connection Types

The current implementation uses **5 distinct connection types**:

#### 1. TCP Ping Connection (PING)
- **Purpose**: RTT measurement
- **Protocol**: 4-byte header "PING" → 4-byte response "PONG"
- **Lifecycle**: Single request-response, then close

#### 2. TCP Control Connection (CTRL)
- **Purpose**: Sample coordination and metrics retrieval
- **Protocol**: Text-based line protocol
- **Format**: `COMMAND ARGS\n` → `RESPONSE\n`
- **Commands**:
  - `SAMPLE_START <sample_id>` → `OK`
  - `SAMPLE_START <sample_id> REVERSE <bw> <chunk> <rtt> <bytes> <udp_port>` → `OK`
  - `SAMPLE_STOP <sample_id>` → `{JSON report}` or `ERR message`
- **Lifecycle**: Persistent connection, multiple commands

#### 3. TCP Data Connection (DATA)
- **Purpose**: Forward bandwidth test (client → server)
- **Protocol**: Binary frames
- **Format**: Each frame = 8-byte header + payload
  - Bytes 0-3: `sample_id` (uint32, big-endian)
  - Bytes 4-7: `payload_len` (uint32, big-endian)
  - Bytes 8+: payload data
- **Lifecycle**: Persistent, streams multiple frames

#### 4. TCP Reverse Connection (RECV)
- **Purpose**: Reverse bandwidth test (server → client)
- **Protocol**: Binary frames (same format as DATA)
- **Format**: Same 8-byte header + payload
- **Lifecycle**: Persistent, server writes frames

#### 5. UDP Data Connection
- **Purpose**: UDP bandwidth testing + RTT
- **Protocol**: Binary packets with sequence numbers
- **Packet Types**:
  - Type 1 (DATA): 13-byte header + payload
    - Byte 0: type (0x01)
    - Bytes 1-4: sample_id (uint32)
    - Bytes 5-12: sequence (uint64)
    - Bytes 13+: payload
  - Type 2 (PING): 1 + 8 bytes (timestamp)
  - Type 3 (PONG): 1 + 8 bytes (echo timestamp)
- **Lifecycle**: Stateless, connectionless

### Data Flow Analysis

#### Forward Test Flow (Client → Server)
```
1. Client → Server: TCP connection with "CTRL" header
2. Client → Server: "SAMPLE_START 1\n"
3. Server → Client: "OK\n"
4. Client → Server: New TCP connection with "DATA" header
5. Client → Server: Binary frames (sample_id=1, data...)
6. ... (multiple frames)
7. Client → Server: "SAMPLE_STOP 1\n" (on control channel)
8. Server → Client: JSON report (on control channel)
```

#### Reverse Test Flow (Server → Client)
```
1. Client → Server: TCP connection with "CTRL" header
2. Client → Server: New TCP connection with "RECV" header
3. Client → Server: "SAMPLE_START 1 REVERSE <params>\n" (control)
4. Server → Client: "OK\n"
5. Server → Client: Binary frames (sample_id=1, data...) on RECV conn
6. ... (multiple frames)
7. Client → Server: "SAMPLE_STOP 1\n" (control)
8. Server → Client: JSON report (control)
```

### Current Protocol Strengths

1. **Simple to debug**: Text-based control protocol is human-readable
2. **Low overhead**: Binary data frames are efficient
3. **Separation of concerns**: Control and data on separate channels
4. **Works reliably**: Proven functional implementation

### Current Protocol Weaknesses

#### 1. **No Session Identity (CRITICAL)**

**Problem**: Server identifies clients by IP address only.

From `server.go:441-447`:
```go
func clientKey(addr net.Addr) string {
    host, _, err := net.SplitHostPort(addr.String())
    if err != nil {
        return addr.String()
    }
    return host  // Just the IP, no session identifier!
}
```

**Risks**:
- **NAT collision**: Multiple clients behind same NAT share one IP
- **Connection mixup**: Data connection from client A might be attributed to client B
- **Security**: No authentication, anyone from same IP can interfere with test
- **Debugging**: Cannot distinguish concurrent tests from same IP

**Example Failure Scenario**:
```
Client A (192.168.1.10 → NAT → 203.0.113.5) starts test
Client B (192.168.1.20 → NAT → 203.0.113.5) starts test
Server sees both as key="203.0.113.5" → state collision!
```

#### 2. **Loosely Coupled Channels (CRITICAL)**

**Problem**: Control and data connections have no explicit binding.

- Control connection uses `clientKey("203.0.113.5")`
- Data connection uses `clientKey("203.0.113.5")`
- **Assumption**: They belong to the same client (may not be true!)

**Race Condition Example** (Reverse Mode):
```go
// client/reverse.go - approximate flow
1. Client opens RECV connection (writes "RECV" header)
2. Client sends SAMPLE_START REVERSE on control
3. Server receives SAMPLE_START, looks for reverseTCP
   → RACE: reverseTCP might not be set yet!
4. Server may timeout or use wrong connection
```

From `server.go:288-291`:
```go
func (c *clientState) waitReverseTCP(timeout time.Duration) *net.TCPConn {
    // Polls for reverseTCP to appear - fragile!
    for time.Now().Before(deadline) {
        // ...
```

#### 3. **UDP Reverse Mode Fragility**

**Problem**: UDP reverse relies on IP parsing and "best-effort" ping.

From `server.go:578-582`:
```go
udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", key, cfg.udpPort))
// Uses clientKey (just IP) + port from client
// No handshake, no confirmation
```

**Issues**:
- Client must provide port number in control message
- Server sends to IP:port without verifying client is listening
- No registration ACK - client might miss first packets
- Firewall/NAT issues not detected

#### 4. Mixed Protocol Types
- Text-based control (SAMPLE_START/STOP)
- Binary data frames (TCP/UDP)
- JSON responses (metrics)
- No unified message format

#### 5. Poor Extensibility
- Adding new commands requires text parsing changes
- No versioning mechanism
- No capability negotiation
- Hard to add optional parameters

#### 6. Error Handling Issues
- Errors returned as text strings ("ERR message")
- No structured error codes
- Client must parse error messages
- No error context or details

#### 7. No Heartbeat/Keepalive
- Cannot detect half-open connections
- Timeouts are ad-hoc and scattered
- No liveness detection for long-running tests

#### 8. Type Safety Issues
- Arguments passed as strings, parsed at runtime
- No compile-time type checking
- Easy to make mistakes with argument order

Example from `control.go:42`:
```go
line := fmt.Sprintf("SAMPLE_START %d REVERSE %d %d %d %d %d",
    sampleID, bandwidth, cfg.ChunkSize, rttMs, cfg.SampleBytes, cfg.UDPPort)
// Args must be in exact order, no type safety
```

## Proposed Protocol Refinement

### Design Principles

1. **Session-first architecture**: Explicit session IDs for all operations
2. **RPC-style messaging**: Structured request/response pairs
3. **Channel binding**: All connections bound to session
4. **Versioning**: Protocol version negotiation
5. **Error handling**: Structured error codes and details
6. **Heartbeat**: Liveness detection on control channel
7. **Registration handshakes**: Explicit endpoint registration for UDP
8. **Backward compatible**: Phased migration path

### Recommended Architecture: Session-Bound JSON-RPC

#### Connection Model

```
┌─────────────────────────────────────────────────────────────┐
│                          CLIENT                             │
│                     (Session ID: abc-123)                   │
└─────────────────────────────────────────────────────────────┘
     │                      │                      │
     │ Control (JSON-RPC)  │ Data (Binary)       │ UDP (Binary)
     │ + Heartbeat         │ + Session ID        │ + Session ID
     ▼                      ▼                      ▼
┌─────────────────────────────────────────────────────────────┐
│                          SERVER                             │
│  ┌─────────────┐   ┌─────────────┐   ┌──────────────┐     │
│  │ RPC Handler │   │ TCP Handler │   │ UDP Handler  │     │
│  │ (by session)│   │ (by session)│   │ (by session) │     │
│  └─────────────┘   └─────────────┘   └──────────────┘     │
│         └──────────────┬───────────────────┘               │
│                   Session State                             │
│              (indexed by session_id)                        │
└─────────────────────────────────────────────────────────────┘
```

#### Session Lifecycle

```
1. Client connects → Control channel established
2. Client sends session.hello → Server assigns session_id
3. Client opens data connection with session_id in header
4. All subsequent operations use session_id
5. Heartbeat maintains session liveness
6. Client closes control → Session terminated, all connections cleaned up
```

### Protocol Architecture

#### Message Framing

Each JSON-RPC message is framed with a length prefix:

```
┌────────────┬──────────────────────────┐
│ Length     │ JSON-RPC Message         │
│ (4 bytes)  │ (variable)               │
│ uint32 BE  │ UTF-8 JSON               │
└────────────┴──────────────────────────┘
```

This enables:
- Easy message boundary detection
- Efficient buffering
- Support for large messages
- Streaming multiple messages

#### Enhanced Data Channel Formats

**NEW: TCP Data Frame (with session binding)**
```
┌────────────┬────────────┬────────────┬──────────────┐
│ Session ID │ Sample ID  │ Payload Len│ Payload      │
│ 16 bytes   │ 4 bytes    │ 4 bytes    │ variable     │
│ UUID       │ uint32 BE  │ uint32 BE  │              │
└────────────┴────────────┴────────────┴──────────────┘
```

**NEW: UDP Data Packet (with session binding)**
```
┌────────────┬──────┬────────────┬──────────┬──────────────┐
│ Session ID │ Type │ Sample ID  │ Sequence │ Payload      │
│ 16 bytes   │ 1B   │ 4 bytes    │ 8 bytes  │ variable     │
│ UUID       │      │ uint32 BE  │ uint64 BE│              │
└────────────┴──────┴────────────┴──────────┴──────────────┘
```

**Benefits**:
- Server can validate session_id and reject stray packets
- No NAT collision - each client has unique session
- Clear binding between control and data
- Can track multiple concurrent sessions from same IP

### JSON-RPC Control Protocol

#### Error Codes

Standard JSON-RPC errors plus custom application errors:

| Code   | Message                   | Meaning                                        |
|--------|---------------------------|------------------------------------------------|
| -32700 | Parse error               | Invalid JSON received                          |
| -32600 | Invalid Request           | JSON not valid JSON-RPC request                |
| -32601 | Method not found          | Method does not exist                          |
| -32602 | Invalid params            | Invalid method parameters                      |
| -32603 | Internal error            | Internal JSON-RPC error                        |
| -32000 | Server error              | Generic server error                           |
| -32001 | Sample already active     | Cannot start, sample already running           |
| -32002 | Sample not found          | Sample ID not found or not active              |
| -32003 | Sample ID mismatch        | Wrong sample ID in request                     |
| -32004 | Invalid network           | Network type not supported (must be tcp/udp)   |
| -32005 | Invalid bandwidth         | Bandwidth out of range                         |
| -32006 | Invalid sample size       | Sample bytes out of range                      |
| -32007 | Reverse not available     | Reverse connection not established             |
| -32008 | Connection timeout        | Connection or operation timed out              |
| -32009 | Rate limit exceeded       | Too many requests                              |
| -32010 | Invalid session           | Session ID not found or expired                |
| -32011 | Session expired           | Session timed out due to inactivity            |
| -32012 | UDP not registered        | UDP endpoint not registered                    |

#### Method 1: session.hello (Handshake)

Establishes session and negotiates capabilities.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "session.hello",
  "params": {
    "client_version": "1.0.0",
    "supported_features": ["tcp", "udp", "reverse", "ping"],
    "capabilities": {
      "max_bandwidth_bps": 10000000000,
      "max_sample_bytes": 1000000000
    }
  },
  "id": 1
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "server_version": "1.0.0",
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "supported_features": ["tcp", "udp", "reverse", "ping"],
    "capabilities": {
      "max_bandwidth_bps": 10000000000,
      "max_sample_bytes": 1000000000,
      "interval_duration_ms": 100
    },
    "heartbeat_interval_ms": 30000
  },
  "id": 1
}
```

**Server Behavior**:
- Generate unique UUID for session_id
- Create session state (indexed by session_id, not IP!)
- Store client capabilities
- Start heartbeat timer

#### Method 2: session.heartbeat (Keepalive)

Maintains session liveness and detects half-open connections.

**Request** (from client):
```json
{
  "jsonrpc": "2.0",
  "method": "session.heartbeat",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "timestamp": 1705412096789
  },
  "id": 2
}
```

**Response** (from server):
```json
{
  "jsonrpc": "2.0",
  "result": {
    "timestamp": 1705412096789,
    "server_time": 1705412096790
  },
  "id": 2
}
```

**Behavior**:
- Client sends every `heartbeat_interval_ms / 2` (e.g., every 15s if interval is 30s)
- Server updates last-seen timestamp
- Server terminates session if no heartbeat for `heartbeat_interval_ms * 2`
- Can also measure RTT via timestamp difference

#### Method 3: udp.register (UDP Endpoint Registration)

**CRITICAL FOR UDP**: Explicitly registers client UDP endpoint before test.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "udp.register",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "udp_port": 54321,
    "test_packet_count": 5
  },
  "id": 3
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "status": "registered",
    "server_will_send_to": "203.0.113.5:54321",
    "test_packets_received": 5
  },
  "id": 3
}
```

**Behavior**:
1. Client opens UDP socket on specified port
2. Client sends `udp.register` with port number
3. Server extracts client IP from control connection
4. Server sends N test packets to client_ip:udp_port
5. Client counts received test packets
6. Client responds with count
7. Server confirms registration if count > 0

**Benefits**:
- Validates UDP path before test starts
- Detects firewall/NAT issues early
- No race conditions - explicit handshake
- Can measure packet loss even during registration

#### Method 4: sample.start (Start Forward Sample)

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "sample.start",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "sample_id": 1,
    "network": "tcp"
  },
  "id": 4
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "sample_id": 1,
    "started_at": "2026-01-16T12:34:56.789Z",
    "ready": true
  },
  "id": 4
}
```

#### Method 5: sample.start_reverse (Start Reverse Sample)

**Enhanced flow to eliminate race conditions.**

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "sample.start_reverse",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "sample_id": 2,
    "network": "tcp",
    "bandwidth_bps": 100000000,
    "chunk_size": 65536,
    "rtt_ms": 50,
    "sample_bytes": 20000000,
    "data_connection_ready": true
  },
  "id": 5
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "sample_id": 2,
    "started_at": "2026-01-16T12:34:57.123Z",
    "server_ready": true
  },
  "id": 5
}
```

**New Flow (eliminates race)**:
```
1. Client opens reverse data connection (TCP or UDP)
2. Client sends DATA/RECV header + session_id
3. Server validates session_id and registers connection
4. Client sends sample.start_reverse with data_connection_ready=true
5. Server verifies data connection is registered
6. Server responds server_ready=true
7. Server begins sending data
8. No race condition!
```

For **UDP reverse**:
```
1. Client calls udp.register first (establishes UDP path)
2. Client sends sample.start_reverse with network="udp"
3. Server knows UDP endpoint from registration
4. Server sends data to registered endpoint
```

#### Method 6: sample.stop (Stop Sample)

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "sample.stop",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "sample_id": 1
  },
  "id": 6
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "sample_id": 1,
    "total_bytes": 10485760,
    "total_duration": 1.234,
    "intervals": [
      {"bytes": 1048576, "duration_ms": 100, "ooo_count": 0},
      {"bytes": 1048576, "duration_ms": 100, "ooo_count": 0}
    ],
    "first_byte_time": "2026-01-16T12:34:56.800Z",
    "last_byte_time": "2026-01-16T12:34:58.034Z",
    "avg_throughput_bps": 67952640.2,
    "packets_recv": 0,
    "packets_lost": 0
  },
  "id": 6
}
```

#### Method 7: ping (RTT via RPC)

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "ping",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "timestamp": 1705412096789123456
  },
  "id": 7
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "timestamp": 1705412096789123456,
    "server_time": 1705412096790234567
  },
  "id": 7
}
```

#### Method 8: server.info (Server Status)

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "server.info",
  "params": {},
  "id": 8
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "version": "1.0.0",
    "uptime_seconds": 86400,
    "active_sessions": 3,
    "capabilities": {
      "max_bandwidth_bps": 10000000000,
      "supported_networks": ["tcp", "udp"]
    }
  },
  "id": 8
}
```

#### Method 9: session.close (Graceful Shutdown)

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "session.close",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000"
  },
  "id": 9
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "status": "closed",
    "sessions_cleaned": 1
  },
  "id": 9
}
```

**Server behavior**:
- Close all data connections for this session
- Clean up session state
- Release resources

### Implementation Plan

#### Phase 1: Protocol Structures

Create `internal/rpc/protocol.go`:

```go
package rpc

import (
    "encoding/json"
    "time"
)

// Session management
type HelloRequest struct {
    ClientVersion     string            `json:"client_version"`
    SupportedFeatures []string          `json:"supported_features"`
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
    MaxBandwidthBps     int64    `json:"max_bandwidth_bps"`
    MaxSampleBytes      int64    `json:"max_sample_bytes"`
    IntervalDurationMs  int      `json:"interval_duration_ms"`
    SupportedNetworks   []string `json:"supported_networks"`
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
    Status               string `json:"status"` // "registered" or "failed"
    ServerWillSendTo     string `json:"server_will_send_to"`
    TestPacketsReceived  int    `json:"test_packets_received"`
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
    SessionID            string  `json:"session_id"`
    SampleID             uint32  `json:"sample_id"`
    Network              string  `json:"network"`
    BandwidthBps         float64 `json:"bandwidth_bps"`
    ChunkSize            int64   `json:"chunk_size"`
    RTTMs                int64   `json:"rtt_ms"`
    SampleBytes          int64   `json:"sample_bytes"`
    DataConnectionReady  bool    `json:"data_connection_ready"`
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
    SampleID         uint32           `json:"sample_id"`
    TotalBytes       uint64           `json:"total_bytes"`
    TotalDuration    float64          `json:"total_duration"`
    Intervals        []IntervalReport `json:"intervals"`
    FirstByteTime    string           `json:"first_byte_time"`
    LastByteTime     string           `json:"last_byte_time"`
    AvgThroughputBps float64          `json:"avg_throughput_bps,omitempty"`
    PacketsRecv      uint64           `json:"packets_recv,omitempty"`
    PacketsLost      uint64           `json:"packets_lost,omitempty"`
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

// Custom error constructor
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
    ErrServerError          = -32000
    ErrSampleAlreadyActive  = -32001
    ErrSampleNotFound       = -32002
    ErrSampleIDMismatch     = -32003
    ErrInvalidNetwork       = -32004
    ErrInvalidBandwidth     = -32005
    ErrInvalidSampleSize    = -32006
    ErrReverseNotAvailable  = -32007
    ErrConnectionTimeout    = -32008
    ErrRateLimitExceeded    = -32009
    ErrInvalidSession       = -32010
    ErrSessionExpired       = -32011
    ErrUDPNotRegistered     = -32012
)
```

#### Phase 2: Server Session Management

Create `internal/rpc/session.go`:

```go
package rpc

import (
    "net"
    "sync"
    "time"

    "github.com/google/uuid"
)

// SessionState manages state for a single client session
type SessionState struct {
    mu                sync.Mutex
    sessionID         string
    clientAddr        net.Addr
    created           time.Time
    lastHeartbeat     time.Time
    capabilities      ClientCapabilities

    // Sample state (existing clientState fields)
    intervalDur       time.Duration
    active            bool
    sampleID          uint32
    startTime         time.Time
    firstByte         time.Time
    lastByte          time.Time
    totalBytes        uint64
    intervals         []intervalBucket
    baseSeq           uint64
    maxSeq            uint64
    hasSeq            bool
    packetsRecv       uint64

    // Connection references
    dataConn          net.Conn      // TCP data connection
    reverseConn       *net.TCPConn  // TCP reverse connection
    udpAddr           *net.UDPAddr  // Registered UDP endpoint
    udpRegistered     bool

    // Reverse mode
    reverseActive     bool
    reverseStopCh     chan struct{}
    reverseDoneCh     chan struct{}
}

// SessionManager manages all active sessions
type SessionManager struct {
    mu       sync.RWMutex
    sessions map[string]*SessionState  // indexed by session_id
    interval time.Duration
    recvWait time.Duration
}

func NewSessionManager(recvWait time.Duration) *SessionManager {
    return &SessionManager{
        sessions: make(map[string]*SessionState),
        interval: 100 * time.Millisecond,
        recvWait: recvWait,
    }
}

// CreateSession creates a new session with unique ID
func (sm *SessionManager) CreateSession(clientAddr net.Addr, caps ClientCapabilities) *SessionState {
    sm.mu.Lock()
    defer sm.mu.Unlock()

    sessionID := uuid.New().String()
    session := &SessionState{
        sessionID:    sessionID,
        clientAddr:   clientAddr,
        created:      time.Now(),
        lastHeartbeat: time.Now(),
        capabilities: caps,
        intervalDur:  sm.interval,
    }

    sm.sessions[sessionID] = session
    return session
}

// GetSession retrieves session by ID
func (sm *SessionManager) GetSession(sessionID string) (*SessionState, bool) {
    sm.mu.RLock()
    defer sm.mu.RUnlock()
    session, ok := sm.sessions[sessionID]
    return session, ok
}

// UpdateHeartbeat updates last heartbeat time
func (sm *SessionManager) UpdateHeartbeat(sessionID string) error {
    sm.mu.RLock()
    session, ok := sm.sessions[sessionID]
    sm.mu.RUnlock()

    if !ok {
        return NewRPCError(ErrInvalidSession, "Session not found", nil)
    }

    session.mu.Lock()
    session.lastHeartbeat = time.Now()
    session.mu.Unlock()
    return nil
}

// CleanupExpiredSessions removes sessions with no recent heartbeat
func (sm *SessionManager) CleanupExpiredSessions(timeout time.Duration) int {
    sm.mu.Lock()
    defer sm.mu.Unlock()

    now := time.Now()
    count := 0

    for sessionID, session := range sm.sessions {
        session.mu.Lock()
        expired := now.Sub(session.lastHeartbeat) > timeout
        session.mu.Unlock()

        if expired {
            // Close all connections
            if session.dataConn != nil {
                session.dataConn.Close()
            }
            if session.reverseConn != nil {
                session.reverseConn.Close()
            }
            delete(sm.sessions, sessionID)
            count++
        }
    }

    return count
}

// RegisterDataConnection associates a data connection with session
func (s *SessionState) RegisterDataConnection(conn net.Conn) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.dataConn = conn
}

// RegisterUDPEndpoint stores validated UDP endpoint
func (s *SessionState) RegisterUDPEndpoint(addr *net.UDPAddr) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.udpAddr = addr
    s.udpRegistered = true
}

// GetUDPEndpoint retrieves registered UDP endpoint
func (s *SessionState) GetUDPEndpoint() (*net.UDPAddr, bool) {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.udpAddr, s.udpRegistered
}
```

#### Phase 3: Data Channel Protocol

Update data channel frame formats:

**TCP Data Connection Handshake**:
```go
// After TCP connection established:
1. Write 4-byte header: "DATA" or "RECV"
2. Write 16-byte session ID (binary UUID)
3. Server validates session ID
4. Begin frame exchange
```

**Enhanced Frame Format** (`internal/protocol/frames.go`):
```go
package protocol

import (
    "encoding/binary"
    "io"
    "net"

    "github.com/google/uuid"
)

const (
    SessionIDSize       = 16  // UUID binary size
    TCPFrameHeaderSize  = SessionIDSize + 4 + 4  // session + sample + len
    UDPFrameHeaderSize  = SessionIDSize + 1 + 4 + 8  // session + type + sample + seq
)

// WriteTCPFrame writes a session-bound TCP frame
func WriteTCPFrame(conn net.Conn, sessionID uuid.UUID, sampleID uint32, payload []byte) error {
    header := make([]byte, TCPFrameHeaderSize)

    // Session ID (16 bytes)
    copy(header[0:16], sessionID[:])

    // Sample ID (4 bytes)
    binary.BigEndian.PutUint32(header[16:20], sampleID)

    // Payload length (4 bytes)
    binary.BigEndian.PutUint32(header[20:24], uint32(len(payload)))

    // Write header + payload
    if _, err := conn.Write(header); err != nil {
        return err
    }
    if _, err := conn.Write(payload); err != nil {
        return err
    }
    return nil
}

// ReadTCPFrame reads and validates a session-bound TCP frame
func ReadTCPFrame(conn net.Conn, expectedSessionID uuid.UUID) (uint32, []byte, error) {
    header := make([]byte, TCPFrameHeaderSize)
    if _, err := io.ReadFull(conn, header); err != nil {
        return 0, nil, err
    }

    // Validate session ID
    sessionID, _ := uuid.FromBytes(header[0:16])
    if sessionID != expectedSessionID {
        return 0, nil, ErrInvalidSession
    }

    sampleID := binary.BigEndian.Uint32(header[16:20])
    payloadLen := binary.BigEndian.Uint32(header[20:24])

    payload := make([]byte, payloadLen)
    if _, err := io.ReadFull(conn, payload); err != nil {
        return 0, nil, err
    }

    return sampleID, payload, nil
}

// WriteUDPPacket writes a session-bound UDP packet
func WriteUDPPacket(conn *net.UDPConn, addr *net.UDPAddr, sessionID uuid.UUID,
                    msgType byte, sampleID uint32, seq uint64, payload []byte) error {
    header := make([]byte, UDPFrameHeaderSize)

    copy(header[0:16], sessionID[:])
    header[16] = msgType
    binary.BigEndian.PutUint32(header[17:21], sampleID)
    binary.BigEndian.PutUint64(header[21:29], seq)

    packet := append(header, payload...)
    _, err := conn.WriteToUDP(packet, addr)
    return err
}
```

### Migration Strategy

#### Phase 1: Dual-Stack Implementation (Backward Compatible)

**Server**:
```go
func handleTCP(conn net.Conn, sm *SessionManager) {
    defer conn.Close()

    header := make([]byte, 4)
    if _, err := io.ReadFull(conn, header); err != nil {
        return
    }

    switch string(header) {
    case "RPC\x00":
        // NEW: JSON-RPC with session management
        rpcServer := NewRPCServer(sm)
        _ = rpcServer.Handle(context.Background(), conn)

    case "CTRL":
        // LEGACY: Old text protocol (keyed by IP)
        handleControlLegacy(conn, legacyState, clientKey(conn.RemoteAddr()))

    case "DATA":
        // Try to read session ID (new protocol)
        sessionID := make([]byte, 16)
        n, _ := conn.Read(sessionID)
        if n == 16 {
            // NEW: Session-bound data
            handleTCPDataWithSession(conn, sm, sessionID)
        } else {
            // LEGACY: IP-based data
            handleTCPDataLegacy(conn, legacyState, clientKey(conn.RemoteAddr()))
        }

    case "RECV":
        // Similar dual handling for reverse

    case "PING":
        _, _ = conn.Write([]byte("PONG"))
    }
}
```

**Client Auto-Detection**:
```go
func NewControlClient(target string, port int) (*ControlClient, error) {
    // Try new protocol first
    client, err := tryJSONRPCConnect(target, port)
    if err == nil {
        return client, nil
    }

    // Fall back to legacy
    log.Printf("New protocol unavailable, using legacy")
    return newLegacyControlClient(target, port)
}
```

#### Phase 2: Deprecation (3-6 months)

- Server logs warning for legacy protocol usage
- Client defaults to new protocol
- Documentation updated
- Monitoring added for legacy usage

#### Phase 3: Legacy Removal (6-12 months)

- Remove legacy handlers
- Remove fallback from client
- Clean up code
- Major version bump

## Benefits Summary

### 1. NAT-Safe Architecture
- **Session ID** instead of IP-based keying
- Multiple clients behind same NAT work correctly
- No collision risk

### 2. Explicit Channel Binding
- All connections bound to session ID
- No ambiguity about which data belongs to which test
- Server validates session on every frame

### 3. Race-Free Reverse Mode
- Data connection registered before sample starts
- Server confirms readiness explicitly
- No polling or timeouts

### 4. Robust UDP Support
- Explicit registration handshake
- Test packets validate path before test
- Early detection of firewall/NAT issues

### 5. Liveness Detection
- Heartbeat mechanism detects half-open connections
- Automatic session cleanup
- Clear timeout behavior

### 6. Structured Protocol
- JSON-RPC 2.0 standard
- Type-safe request/response
- Comprehensive error codes
- Version negotiation

### 7. Observability
- Session IDs for tracing
- Request IDs for correlation
- Timestamps for latency analysis
- Clear state machine

### 8. Backward Compatible
- Phased migration
- Both protocols supported
- Graceful fallback
- No flag day

## Performance Considerations

### Overhead Analysis

| Component | Current | Proposed | Overhead |
|-----------|---------|----------|----------|
| Control messages | ~50B text | ~200B JSON | 150B (negligible) |
| Control frequency | ~10-20/sec | ~10-20/sec | None |
| TCP data frame header | 8B | 24B (+16B session) | +16B per frame |
| UDP packet header | 13B | 29B (+16B session) | +16B per packet |
| Heartbeat | None | 1 msg/15s | ~0.067/sec |

### TCP Frame Overhead Impact

With 64KB chunks:
- Old: 8 / 65536 = 0.012% overhead
- New: 24 / 65536 = 0.037% overhead
- **Added overhead**: 0.025% (negligible)

### UDP Packet Overhead Impact

With 1400-byte packets:
- Old: 13 / 1400 = 0.93% overhead
- New: 29 / 1400 = 2.07% overhead
- **Added overhead**: 1.14% (acceptable for safety)

**Conclusion**: Session binding adds ~16 bytes per frame/packet, which is negligible compared to the benefits of session safety and correct NAT handling.

## Testing Strategy

### Unit Tests
- Session manager operations
- RPC method handlers
- Frame encoding/decoding with session IDs
- Error conditions
- Parameter validation

### Integration Tests
- **Session isolation**: Multiple concurrent sessions from same IP
- **NAT scenario**: Simulate multiple clients behind NAT
- **Race conditions**: Reverse mode with fast start/stop
- **UDP registration**: Firewall simulation, packet loss
- **Heartbeat**: Session timeout and cleanup
- **Backward compat**: Legacy + new protocol interop

### Specific Test Cases

**Test: NAT Collision Protection**
```
1. Client A (session_id=aaa) connects from 203.0.113.5
2. Client B (session_id=bbb) connects from 203.0.113.5
3. Both start samples simultaneously
4. Verify: No state collision, independent results
```

**Test: UDP Registration**
```
1. Client opens UDP port but firewall blocks it
2. Client calls udp.register
3. Server sends test packets
4. Client receives 0 packets
5. Server returns error: UDP path failed
6. Test aborts before wasting time
```

**Test: Reverse Race Elimination**
```
1. Client opens reverse connection
2. Client immediately calls sample.start_reverse
3. Server validates connection is ready
4. Server starts sending
5. No race, no timeout
```

## Documentation Requirements

### 1. Protocol Specification
- Complete JSON-RPC method reference
- Session lifecycle documentation
- Frame format specifications
- Error code reference
- Sequence diagrams for all flows

### 2. Migration Guide
- How to upgrade server (dual-stack)
- How to upgrade client (auto-detect)
- Backward compatibility timeline
- Testing checklist

### 3. API Reference
- Go client library docs
- Code examples for each method
- Best practices

## Implementation Checklist

- [ ] Define protocol structures (`internal/rpc/protocol.go`)
- [ ] Implement session manager (`internal/rpc/session.go`)
- [ ] Implement RPC server (`internal/rpc/server.go`)
- [ ] Implement RPC client (`internal/rpc/client.go`)
- [ ] Update data frame formats with session ID
- [ ] Implement UDP registration handshake
- [ ] Implement heartbeat mechanism
- [ ] Add dual-stack support to server
- [ ] Add auto-detection to client
- [ ] Write unit tests
- [ ] Write integration tests
- [ ] Update documentation
- [ ] Performance benchmarks
- [ ] Migration guide

## Conclusion

This refined protocol addresses critical architectural issues in the current implementation:

**Critical Fixes**:
- ✅ Session binding eliminates NAT collision risk
- ✅ UDP registration handshake eliminates race conditions
- ✅ Heartbeat detects half-open connections
- ✅ Explicit readiness eliminates reverse mode races

**Additional Benefits**:
- ✅ JSON-RPC provides structure and extensibility
- ✅ Version negotiation enables evolution
- ✅ Backward compatibility ensures smooth migration
- ✅ Standards-based approach improves interoperability

**Recommendation**: Implement session-bound JSON-RPC protocol in parallel with existing protocol, with phased migration over 2-3 releases. The session binding is the most critical improvement and should be prioritized.
