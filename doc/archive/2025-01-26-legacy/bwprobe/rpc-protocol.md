# bwprobe RPC Protocol

This document describes the JSON-RPC control protocol used by bwprobe for
session management and sample coordination.

## Transport and framing

- Transport: TCP control connection to the bwprobe server port.
- Protocol selection: client sends the 4-byte header `RPC\x00`. The server then
  switches the connection to JSON-RPC; if the server does not recognize this
  header, the client falls back to the legacy text protocol.
- Framing: every JSON-RPC message is length-prefixed with a 4-byte, big-endian
  unsigned integer. The length is the number of bytes of JSON that follow.
- Max message size: 10 MB (server rejects larger payloads).

## JSON-RPC envelope

Requests and responses follow JSON-RPC 2.0:

```json
{
  "jsonrpc": "2.0",
  "method": "sample.start",
  "params": { ... },
  "id": 1
}
```

Responses contain either `result` or `error`:

```json
{
  "jsonrpc": "2.0",
  "result": { ... },
  "id": 1
}
```

```json
{
  "jsonrpc": "2.0",
  "error": { "code": -32002, "message": "Sample not found" },
  "id": 1
}
```

## Session lifecycle

1. `session.hello` must be called first. The server returns a `session_id` and
   heartbeat interval.
2. The client sends periodic `session.heartbeat` calls.
3. When the client exits, it calls `session.close`.

If heartbeats stop, the server may expire the session (currently ~60s).

## Methods

### session.hello

Request:

```json
{
  "client_version": "1.0.0",
  "supported_features": ["tcp", "udp", "reverse", "ping"],
  "capabilities": {
    "max_bandwidth_bps": 10000000000,
    "max_sample_bytes": 1000000000
  }
}
```

Response:

```json
{
  "server_version": "1.0.0",
  "session_id": "uuid",
  "supported_features": ["tcp", "udp", "reverse", "ping"],
  "capabilities": {
    "max_bandwidth_bps": 10000000000,
    "max_sample_bytes": 1000000000,
    "interval_duration_ms": 100,
    "supported_networks": ["tcp", "udp"]
  },
  "heartbeat_interval_ms": 30000
}
```

### session.heartbeat

Request:

```json
{
  "session_id": "uuid",
  "timestamp": 1700000000000000000
}
```

Response:

```json
{
  "timestamp": 1700000000000000000,
  "server_time": 1700000000123456789
}
```

### session.close

Request:

```json
{
  "session_id": "uuid"
}
```

Response:

```json
{
  "status": "closed",
  "sessions_cleaned": 1
}
```

### server.info

Request: empty params.

Response:

```json
{
  "version": "1.0.0",
  "uptime_seconds": 1234,
  "active_sessions": 2,
  "capabilities": {
    "max_bandwidth_bps": 10000000000,
    "max_sample_bytes": 1000000000,
    "interval_duration_ms": 100,
    "supported_networks": ["tcp", "udp"]
  }
}
```

### ping

Request:

```json
{
  "session_id": "uuid",
  "timestamp": 1700000000000000000
}
```

Response:

```json
{
  "timestamp": 1700000000000000000,
  "server_time": 1700000000123456789
}
```

### udp.register

Registers the client UDP endpoint for reverse UDP tests. The client must send
a UDP ping (`UDPTypePing`) to the server shortly before calling this method so
the server can validate the endpoint.

Request:

```json
{
  "session_id": "uuid",
  "udp_port": 50001,
  "test_packet_count": 5
}
```

Response:

```json
{
  "status": "registered",
  "server_will_send_to": "1.2.3.4:50001",
  "test_packets_received": 1
}
```

### sample.start

Starts a forward (upload) sample.

Request:

```json
{
  "session_id": "uuid",
  "sample_id": 1,
  "network": "tcp"
}
```

Response:

```json
{
  "sample_id": 1,
  "started_at": "2026-01-14T07:35:53.431255822Z",
  "ready": true
}
```

### sample.start_reverse

Starts a reverse (download) sample. For TCP reverse, the client must open the
reverse data connection first (see Sequencing below). For UDP reverse, the
client must complete `udp.register`.

Request:

```json
{
  "session_id": "uuid",
  "sample_id": 1,
  "network": "tcp",
  "bandwidth_bps": 140000000,
  "chunk_size": 65536,
  "rtt_ms": 120,
  "sample_bytes": 20000000,
  "data_connection_ready": true
}
```

Response:

```json
{
  "sample_id": 1,
  "started_at": "2026-01-14T07:35:53.431255822Z",
  "server_ready": true
}
```

### sample.stop

Stops the sample and returns the report. The server may wait for `recv-wait`
to capture in-flight bytes before producing the report (forward mode only).

Request:

```json
{
  "session_id": "uuid",
  "sample_id": 1
}
```

Response (selected fields):

```json
{
  "sample_id": 1,
  "total_bytes": 20000000,
  "total_duration": 1.23,
  "intervals": [
    { "bytes": 1600000, "duration_ms": 100, "ooo_count": 0 }
  ],
  "first_byte_time": "2026-01-14T07:35:53.431255822Z",
  "last_byte_time": "2026-01-14T07:35:54.661255822Z",
  "avg_throughput_bps": 130081300.0,
  "packets_recv": 12345,
  "packets_lost": 12,
  "tcp_send_buffer_bytes": 3145728,
  "tcp_retransmits": 42,
  "tcp_segments_sent": 123456
}
```

Notes:

- `intervals` are in receive-time order (100ms buckets).
- TCP stats are reported by the sender (server in reverse mode).
- UDP loss is derived from sequence gaps reported by the server.

## Sequencing

### Forward (upload) test

1. Open control TCP connection and send `RPC\x00`.
2. Call `session.hello`.
3. For each sample:
   - `sample.start`
   - Send data on the data connection (TCP "DATA" stream or UDP packets).
   - `sample.stop` and read the report.
4. `session.close`.

### Reverse (download) TCP

1. Open control TCP connection and call `session.hello`.
2. Open a reverse TCP data connection and send the `RECV` header plus
   session ID.
3. For each sample:
   - `sample.start_reverse`
   - Receive data on the reverse connection.
   - `sample.stop` and read the report.
4. `session.close`.

### Reverse (download) UDP

1. Open control TCP connection and call `session.hello`.
2. Bind a UDP socket locally and send a UDP ping to the server (UDPTypePing).
3. Call `udp.register` with the bound UDP port.
4. For each sample:
   - `sample.start_reverse`
   - Receive UDP data until `sample.stop`.
5. `session.close`.

## Error handling

Standard JSON-RPC errors:

- `-32700` Parse error
- `-32600` Invalid Request
- `-32601` Method not found
- `-32602` Invalid params
- `-32603` Internal error

Application-specific errors:

- `-32000` Server error
- `-32001` Sample already active
- `-32002` Sample not found
- `-32003` Sample ID mismatch
- `-32004` Invalid network
- `-32005` Invalid bandwidth
- `-32006` Invalid sample size
- `-32007` Reverse not available
- `-32008` Connection timeout
- `-32009` Rate limit exceeded
- `-32010` Invalid session
- `-32011` Session expired
- `-32012` UDP not registered

## Data-plane framing (summary)

The control protocol coordinates samples, but data is sent over separate TCP
or UDP channels:

- TCP data: "DATA" header, then framed chunks: 8-byte header
  (`sample_id`, `payload_len`) + payload.
- TCP reverse: "RECV" header + session ID, then framed chunks as above.
- UDP data: `UDPTypeData`/`UDPTypeDataSession` + `sample_id` + `seq`.
- UDP reverse completion is signaled by `UDPTypeDone`.

See `internal/protocol/types.go` for exact header sizes and constants.
