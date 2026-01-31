# bwprobe Algorithm Specification

This document specifies the core algorithms, protocols, and state machines in bwprobe. All descriptions match the actual implementation as of the latest code review (2026-01-28).

## Table of Contents

1. [Overview](#overview)
2. [Measurement Model](#measurement-model)
3. [Throughput Algorithms](#throughput-algorithms)
4. [RTT Sampling Algorithm](#rtt-sampling-algorithm)
5. [Loss Tracking Algorithms](#loss-tracking-algorithms)
6. [Rate Limiting Algorithm](#rate-limiting-algorithm)
7. [Protocol State Machines](#protocol-state-machines)
8. [Frame Formats](#frame-formats)
9. [Edge Cases and Deviations](#edge-cases-and-deviations)

---

## Overview

**bwprobe** is a network quality measurement tool that uses **sample-based testing** to measure bandwidth, RTT, jitter, and loss at a user-specified rate cap. It implements a client-server architecture with separate control and data channels.

### Key Design Principles

1. **Sample-based measurement**: Fixed number of samples, each transferring a fixed number of bytes
2. **Two-channel architecture**: Control channel (prefers JSON-RPC 2.0 with `RPC\x00` header and length-prefixed messages, falls back to legacy `CTRL` line protocol) + binary data channel
3. **Explicit pacing**: Linux `SO_MAX_PACING_RATE` for TCP, leaky-bucket limiter for UDP
4. **Interval-based reporting**: Server aggregates data into 100ms intervals
5. **Session binding**: RPC mode ties connections to a session ID; legacy mode keys by client address

### Components

- **Control channel**: JSON-RPC 2.0 over TCP for session management and sample coordination. Clients attempt RPC first; if the server rejects it, they fall back to the legacy `CTRL` protocol.
- **Data channel**: TCP or UDP stream with framed payloads
- **RTT sampler**: Continuous RTT measurement via separate TCP/UDP ping exchanges to the server port
- **Metrics collectors**: TCP_INFO reader (Linux-only) and UDP sequence tracker

---

## Measurement Model

### Sample-Based Testing

Each test run consists of **N samples**, where each sample **targets S bytes** at a target rate **R bps**.

**Note**: Actual transferred bytes may slightly exceed S (senders always write full-size chunks regardless of remaining bytes) or be lower if the sample terminates early (timeout, EOF, or MaxDuration reached).

**Inputs** (from [bwprobe/internal/engine/samples.go:43](../internal/engine/samples.go#L43)):
- `cfg.Samples` (int): Number of samples to run
- `cfg.SampleBytes` (int64): Bytes to transfer per sample
- `cfg.BandwidthBps` (float64): Target bandwidth in bits/sec
- `cfg.Network` (string): "tcp" or "udp"
- `cfg.Direction` (Direction): Upload or Download
- `cfg.Wait` (time.Duration): Inter-sample wait time
- `cfg.MaxDuration` (time.Duration): Overall timeout

**Sample Loop** ([bwprobe/internal/engine/samples.go:54-179](../internal/engine/samples.go#L54-L179)):

```
for sample := 0; sample < cfg.Samples; sample++ {
    1. Check context cancellation and MaxDuration
    2. Assign sampleID = sample + 1
    3. Send SAMPLE_START (or SAMPLE_START_REVERSE) via control
    4. Transfer cfg.SampleBytes via data channel
       - Per-transfer loop with timeout/cancellation checks
       - Progress callbacks every ~1% of sample
    5. Send SAMPLE_STOP via control
    6. Server returns SampleReport with interval stats
    7. If not last sample and cfg.Wait > 0, wait (context-aware)
}
```

**Per-Sample Timeout** ([bwprobe/internal/engine/samples.go:76-104](../internal/engine/samples.go#L76-L104)):

For **download (reverse) mode only**, a deadline is computed:

```go
expectedDur := SampleBytes * 8 / BandwidthBps  // in seconds
if expectedDur <= 0:
    expectedDur = 10s

if Network == "udp":
    deadline = now + expectedDur + 2*reverseReadTimeout  // reverseReadTimeout = 2s
else:  // TCP
    deadline = now + expectedDur + 5s
```

If deadline is exceeded or more than 10 **total (non-resetting)** timeout errors occur, the sample fails.

**Upload mode** has no explicit per-sample deadline; relies on write deadlines and MaxDuration.

---

## Throughput Algorithms

The server aggregates received bytes into **100ms intervals** ([bwprobe/internal/rpc/session.go:16](../internal/rpc/session.go#L16)):

```go
const defaultIntervalDuration = 100 * time.Millisecond
```

Each interval reports:
- `Bytes` (uint64): Bytes received in this interval
- `DurationMs` (int64): Actual interval duration in milliseconds
- `OOOCount` (uint64): Out-of-order packets (UDP only)

The client computes four throughput metrics from the interval data.

### 1. Trimmed Mean

**Purpose**: Robust average that discards outliers

**Algorithm** ([bwprobe/internal/engine/samples.go:274-295](../internal/engine/samples.go#L274-L295)):

```
trimFraction = 0.1  // Drop top/bottom 10%

1. Compute per-interval rates:
   throughputs[i] = intervals[i].Bytes * 8 / (intervals[i].DurationMs / 1000.0)

2. Sort throughputs ascending

3. Trim both ends:
   cut = floor(len(throughputs) * trimFraction)
   start = cut
   end = len(throughputs) - cut

4. If start >= end, return mean(throughputs)  // fallback

5. Return sum(throughputs[start:end]) / (end - start)
```

**Output**: Trimmed mean throughput in bits/sec

### 2. Percentile (P90, P80)

**Purpose**: High-percentile rates indicating achievable bandwidth

**Algorithm** ([bwprobe/internal/engine/samples.go:297-315](../internal/engine/samples.go#L297-L315)):

```
percentile(values, pct):
    if len(values) == 0: return 0
    if pct <= 0: return values[0]
    if pct >= 1: return values[len(values)-1]

    idx = ceil(len(values) * pct) - 1
    idx = clamp(idx, 0, len(values)-1)
    return values[idx]
```

**Example**: For 10 intervals sorted by rate, P90 = `values[ceil(10*0.9)-1] = values[8]`

### 3. Peak 1-Second Rate

**Purpose**: Maximum sustained bandwidth over a 1-second rolling window

**Algorithm** ([bwprobe/internal/engine/samples.go:317-346](../internal/engine/samples.go#L317-L346)):

```
Input:
  bytes[i] = cumulative bytes at end of interval i
  times[i] = cumulative time (seconds) at end of interval i
  window = 1.0 second

peak = 0
start = 0
for i in 0..len(times)-1:
    // Advance start until window size is valid
    while start+1 <= i and (times[i] - times[start+1]) >= window:
        start++

    dt = times[i] - times[start]
    if dt < window or dt <= 0:
        continue  // Window too small

    db = bytes[i] - bytes[start]
    if db < 0: continue

    rate = db * 8 / dt  // bits/sec
    if rate > peak:
        peak = rate

return peak
```

**Deviations**:
- Requires at least 1 second of cumulative data to produce a valid peak
- If sample duration < 1s, the peak metric may be 0 or fallback to overall average

### 4. Fallback: Overall Average

If no intervals are present (server error or empty sample), all metrics fallback to:

```
avg = TotalBytes * 8 / TotalDuration
```

([bwprobe/internal/engine/samples.go:196-209](../internal/engine/samples.go#L196-L209))

---

## RTT Sampling Algorithm

RTT is measured **continuously during the test run** via separate TCP/UDP ping exchanges to the server port (not over the data channel). Sampling starts before the first sample and continues until the run completes.

### Welford's Online Algorithm

**Purpose**: Compute mean and variance in a single pass without storing all samples

**Implementation** ([bwprobe/internal/metrics/rtt_sampler.go:106-123](../internal/metrics/rtt_sampler.go#L106-L123)):

```
State:
  count: int = 0
  mean: float64 = 0  // in microseconds
  m2: float64 = 0    // sum of squared deviations
  min: Duration = 0
  max: Duration = 0

addSample(rtt Duration):
    if count == 0 or rtt < min:
        min = rtt
    if count == 0 or rtt > max:
        max = rtt

    value = rtt.Microseconds()
    count++
    delta = value - mean
    mean += delta / count
    delta2 = value - mean
    m2 += delta * delta2
```

**Variance and Standard Deviation**:

```
variance = m2 / (count - 1)    // for count > 1
stddev = sqrt(variance)
```

([bwprobe/internal/metrics/rtt_sampler.go:83-104](../internal/metrics/rtt_sampler.go#L83-L104))

**Outputs**:
- `Mean`: mean RTT (Duration)
- `StdDev`: standard deviation (jitter, Duration)
- `Min`, `Max`: observed extrema
- `Samples`: count

### Sampling Rate

RTT samples are collected at a fixed rate (default **10 samples/sec** if `cfg.RTTRate <= 0`):

```go
if cfg.RTTRate <= 0:
    cfg.RTTRate = 10

interval = 1 second / RTTRate
ticker = NewTicker(interval)
```

([bwprobe/internal/metrics/rtt_sampler.go:50-71](../internal/metrics/rtt_sampler.go#L50-L71), [bwprobe/internal/engine/runner.go:39-41](../internal/engine/runner.go#L39-L41))

**Note**: The sampler skips failed pings or non-positive RTT values.

**Idempotent Stop**:

The sampler uses `sync.Once` to ensure `Stop()` can be called multiple times safely ([bwprobe/internal/metrics/rtt_sampler.go:23,75-80](../internal/metrics/rtt_sampler.go#L23,L75-L80)):

```go
stopOnce sync.Once

func (s *RTTSampler) Stop() {
    s.stopOnce.Do(func() {
        close(s.stopCh)
    })
    <-s.doneCh  // Blocks until sampler goroutine exits
}
```

**Note**: `Stop()` blocks until the sampler goroutine exits and assumes `Start()` was called.

---

## Loss Tracking Algorithms

### TCP: Retransmit Tracking

**Data Source**: Linux `TCP_INFO` via `getsockopt()`

**Implementation** ([bwprobe/internal/metrics/tcp.go:29-72](../internal/metrics/tcp.go#L29-L72)):

```go
info = getsockopt(fd, IPPROTO_TCP, TCP_INFO)

// Prefer Data_segs_out, fallback to Segs_out or estimate from bytes
segmentsSent = info.Data_segs_out
if segmentsSent == 0:
    segmentsSent = info.Segs_out
if segmentsSent == 0 and info.Bytes_sent > 0 and info.Snd_mss > 0:
    segmentsSent = (info.Bytes_sent + info.Snd_mss - 1) / info.Snd_mss

// Prefer Total_retrans, fallback to estimate from retransmitted bytes
retransmits = info.Total_retrans
if retransmits == 0 and info.Bytes_retrans > 0 and info.Snd_mss > 0:
    retransmits = (info.Bytes_retrans + info.Snd_mss - 1) / info.Snd_mss
```

**Outputs**:
- `Retransmits` (uint64): Total retransmitted segments
- `SegmentsSent` (uint64): Total segments sent
- `RTT` (Duration): Kernel's smoothed RTT (microseconds)
- `RTTVar` (Duration): Kernel's RTT variance

**Loss Rate**: Computed by caller as `Retransmits / SegmentsSent`

### UDP: Sequence Gap Estimation

**Algorithm** ([bwprobe/internal/metrics/udp.go:15-50](../internal/metrics/udp.go#L15-L50)):

```
State:
  initialized: bool = false
  baseSeq: uint64
  maxSeq: uint64
  packetsRecv: uint64
  bytesRecv: uint64

Add(seq, bytes):
    if not initialized:
        baseSeq = seq
        maxSeq = seq
        initialized = true
        packetsRecv = 1
        bytesRecv = bytes
        return

    if seq > maxSeq:
        maxSeq = seq

    packetsRecv++
    bytesRecv += bytes

Stats():
    total = maxSeq - baseSeq + 1
    lost = max(0, total - packetsRecv)
    return (packetsRecv, lost, bytesRecv)
```

**Key Properties**:
- **Loss = gap-based estimate**: `(maxSeq - baseSeq + 1) - packetsRecv`
- Assumes monotonically increasing sequence numbers starting from `baseSeq`
- Does **not** track duplicates or reordering separately (duplicates increase `packetsRecv`, lowering apparent loss)

**Deviations**:
- Reordering or duplicates can cause **underestimation** of true loss
- No sequence bitmap (memory optimization for high-rate tests)
- First packet sets `baseSeq`; if first packet is lost, loss count is unaffected

---

## Rate Limiting Algorithm

### Leaky Bucket Limiter

Used for **UDP pacing** (TCP uses `SO_MAX_PACING_RATE` instead).

**Implementation** ([bwprobe/internal/network/ratelimit.go:8-41](../internal/network/ratelimit.go#L8-L41)):

```
State:
  rate: float64     // bytes/sec
  next: time.Time   // next allowed send time
  mu: sync.Mutex

Wait(n int):
    if rate <= 0 or n <= 0:
        return

    lock(mu)
    now = time.Now()
    if next < now:
        next = now

    wait = next - now
    next = next + Duration(n / rate * 1e9 nanoseconds)
    unlock(mu)

    if wait > 0:
        sleep(wait)
```

**Properties**:
- **Constant drain rate**: Permits exactly `rate` bytes/sec on average
- **No burst allowance**: First call after idle starts immediately (`next = now`)
- **Serialized**: Mutex ensures thread-safe rate tracking

**Example**:
```
rate = 10 MB/s = 10,485,760 bytes/sec
n = 1400 bytes (one UDP packet)
delay = 1400 / 10485760 * 1e9 = 133,514 ns ≈ 133 μs
```

---

## Protocol State Machines

### Session Lifecycle

**States**: `NONE` → `HELLO_SENT` → `ACTIVE` → `CLOSED`

```
Client                          Server
  |                               |
  |--- TCP connect (CTRL) ------->|
  |<-- accept ---------------------|
  |                               |
  |--- session.hello ------------->|  (allocate session_id)
  |<-- HelloResponse (session_id)--|
  |                               |
  [ACTIVE state, heartbeat starts] |
  |                               |
  |--- heartbeat ----------------->|  (periodic, updates lastHeartbeat)
  |<-- HeartbeatResponse ----------|
  |                               |
  |--- session.close ------------->|  (or TCP close)
  |<-- SessionCloseResponse -------|
  |                               |
  [CLOSED, connections cleaned up] |
```

**Session Management** ([bwprobe/internal/rpc/session.go:84-194](../internal/rpc/session.go#L84-L194)):

- **Session creation**: `CreateSession()` assigns UUID, sets `created` and `lastHeartbeat` to now
- **Heartbeat interval**: Server suggests interval in `HelloResponse.heartbeat_interval_ms` (30 seconds)
- **Session timeout**: Sessions with no heartbeat for >60s are cleaned up by periodic goroutine
- **Cleanup**: Every 30s, server runs `CleanupExpiredSessions(60s)` and `CleanupExpiredUDPPings(15m)`

**Note**: The JSON-RPC session lifecycle applies only after the `RPC\x00` header; `CTRL` connections follow legacy commands and have no JSON-RPC session.

**Constants** ([bwprobe/internal/rpc/session.go:15-26](../internal/rpc/session.go#L15-L26)):
```go
defaultIntervalDuration = 100ms
sessionCleanupInterval  = 30s
sessionTimeout          = 60s
udpPingMaxAge           = 15min
```

### Sample Workflow: Upload (Forward) Mode

```
Client                              Server (Session State)
  |                                   |
  |--- sample.start (sid, net) ------>| active=true, sampleID=sid
  |<-- SampleStartResponse (Ready)---|  startTime=now
  |                                   |
  |=== New TCP "DATA" connection ====>| associate with session_id
  |  (header: "DATA" + session_id)   | dataConn set
  |                                   |
  |--- binary frame (sid, len, data)->| record bytes in current interval
  |--- binary frame ----------------->| if interval >= 100ms, rotate
  |...                                |
  |                                   |
  |--- sample.stop (sid) ------------->| [may wait recvWait if data seen]
  |                                   | active=false, compute report
  |<-- SampleStopResponse (intervals)-| intervals[], metrics
  |                                   |
```

**Note**: RPC method names are `sample.start`, `sample.start_reverse`, `sample.stop`. Legacy protocol uses text commands `SAMPLE_START`, `SAMPLE_STOP`.

**Receive Wait**: On non-reverse `sample.stop`, server may optionally sleep `recvWait` (configurable) if it has already seen sample data, to let trailing data arrive ([bwprobe/internal/rpc/server.go:317-319](../internal/rpc/server.go#L317-L319)).

**Frame Format** (TCP, [bwprobe/internal/network/sender.go:108-125](../internal/network/sender.go#L108-L125)):
```
[0:4]   sample_id (uint32, big-endian)
[4:8]   payload_len (uint32, big-endian)
[8:8+payload_len]   payload data
```

**Interval Bucketing** ([bwprobe/internal/rpc/session.go:28-32,44-72](../internal/rpc/session.go)):

Server maintains `intervals []intervalBucket` where each bucket tracks `{bytes, ooo}` for a 100ms window. On `SAMPLE_STOP`, server returns:

```json
{
  "sample_id": 1,
  "total_bytes": 12345,
  "total_duration": 1.234,
  "intervals": [
    {"bytes": 1024, "duration_ms": 100, "ooo_count": 0},
    {"bytes": 2048, "duration_ms": 100, "ooo_count": 0},
    ...
  ]
}
```

**Note**: `tcp_retransmits` and `tcp_segments_sent` are **only** populated for **reverse TCP samples** (see reverse mode below), where the server reads TCP_INFO from its sending connection. These fields are omitted or zero for upload samples.

### Sample Workflow: Download (Reverse) Mode

```
Client                              Server
  |                                   |
  |=== New TCP "RECV" connection ====>| reverseTCP set
  |  (header: "RECV" + session_id)   |
  |                                   |
  |--- sample.start_reverse --------->| active=true, reverseActive=true
  |    (sid, net, bw, chunk, rtt,    | spawn reverse sender goroutine
  |     sample_bytes,                |
  |     data_connection_ready)       |
  |<-- SampleStartReverseResponse ---|
  |                                   |
  |                                   |---> Write frames to reverseTCP
  |<-- binary frames (sid, data) -----|    at paced rate
  |<-- ...                            |
  |                                   |
  |--- sample.stop (sid) ------------->| stop reverse sender, wait for done
  |<-- SampleStopResponse (intervals)-| report includes server-side TCP_INFO
  |                                   |
```

**Legacy Protocol**: Legacy reverse start uses `SAMPLE_START <id> REVERSE <bandwidth_bps> <chunk_bytes> <rtt_ms> <sample_bytes> <udp_port>` (includes `udp_port` parameter; no `data_connection_ready` flag).

**UDP Reverse Completion**: For reverse UDP samples, the server transmits `UDPTypeDone` (type=4) with the sample_id **3 times** after sending the sample bytes. The client receiver treats this frame as EOF to cleanly terminate the sample.

**Reverse Sender** ([bwprobe/internal/transport/reverse_sender.go](../internal/transport/reverse_sender.go)):
- Uses same pacing as upload mode (TCP: `SO_MAX_PACING_RATE`, UDP: leaky bucket)
- Writes to `reverseTCP` or UDP socket
- Stops on `SAMPLE_STOP` or context cancel

**Connection Ready Signal** ([bwprobe/internal/rpc/session.go:67,376-406](../internal/rpc/session.go)):

To avoid race conditions, server waits for `reverseConnReady` channel before starting reverse sender:

```go
reverseConnReady chan struct{}

setReverseTCPConn(conn):
    reverseTCP = conn
    if reverseConnReady != nil:
        close(reverseConnReady)  // signal ready
        reverseConnReady = nil

waitReverseTCP(timeout):
    if reverseTCP != nil: return reverseTCP
    ready = make(chan struct{})
    reverseConnReady = ready
    select:
        case <-ready: return reverseTCP
        case <-time.After(timeout): return nil
```

### UDP Registration

Required for **UDP reverse mode** to establish client's UDP endpoint.

```
Client                              Server
  |                                   |
  |==> UDP PING (client → server) ===>| RecordUDPPing(addr, now)
  |<== UDP PONG (server → client) ===| (echo timestamp)
  |   (repeat 3 times)                |
  |                                   |
  |--- udp.register ------------------>| (session_id, udp_port,
  |                                   |  test_packet_count)
  |                                   | Validate: RecentUDPPing(addr, 15s)?
  |<-- UDPRegisterResponse -----------| {status: "registered",
  |                                   |  test_packets_received: 1}
  |                                   | session.udpAddr = addr
  |                                   | session.udpRegistered = true
```

**Implementation** ([bwprobe/internal/transport/transport.go:64-82](../internal/transport/transport.go#L64-L82), [bwprobe/internal/rpc/server.go:368-403](../internal/rpc/server.go#L368-L403)):

1. **Client sends** UDP ping packets to server (typically **5** attempts, sent in `SendUDPHello`) to "prove" its address
2. Server receives UDP pings and calls `RecordUDPPing(addr)`, storing `map[addr.String()]time.Time`
3. Server echoes each ping as PONG
4. Client calls `udp.register` RPC method with `test_packet_count: 5`
5. Server validates that a ping was seen within **15 seconds** using `RecentUDPPing(addr, 15s)`
6. If validation succeeds, server registers the UDP endpoint and returns `{status: "registered", test_packets_received: 1}`

**Note**: The server does **not** send test pings to the client. The `test_packet_count` parameter is currently ignored by the server implementation, and `test_packets_received` is fixed to 1 if validation succeeds.

**Cleanup**: `CleanupExpiredUDPPings(15min)` removes stale entries to prevent memory leak

---

## Frame Formats

### TCP Control Channel (JSON-RPC 2.0)

**Connection Header**:
```
"RPC\x00" (4 bytes)
```

**Length Prefix**: Each JSON-RPC request/response is prefixed with a 4-byte big-endian length (uint32) indicating the JSON message size.

([bwprobe/internal/rpc/client.go:34,94-99](../internal/rpc/client.go#L34,L94-L99), [bwprobe/internal/rpc/server.go:47-63](../internal/rpc/server.go#L47-L63))

**Message Format**:
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
      "interval_duration_ms": 100,
      "supported_networks": ["tcp", "udp"]
    },
    "heartbeat_interval_ms": 30000
  },
  "id": 1
}
```

See [bwprobe/internal/rpc/protocol.go](../internal/rpc/protocol.go) for all RPC methods.

### TCP Data Channel (Upload)

**Connection Header** (RPC mode):
```
"DATA" (4 bytes)
uint16 session_id_len (big-endian)
session_id (UTF-8 string, session_id_len bytes)
```

**Connection Header** (legacy mode):
```
"DATA" (4 bytes)
```

**Note**: The session ID block is **optional (RPC only)**. Legacy mode sends only the 4-byte `DATA` header with no session ID.

**Frame** ([bwprobe/internal/protocol/types.go:3-9](../internal/protocol/types.go#L3-L9), [bwprobe/internal/network/sender.go:119-123](../internal/network/sender.go#L119-L123)):
```
[0:4]   sample_id (uint32, big-endian)
[4:8]   payload_len (uint32, big-endian)
[8:...]   payload (variable length)
```

**TCP Pacing** ([bwprobe/internal/network/sender.go:164-184](../internal/network/sender.go#L164-L184)):
```go
raw.Control(func(fd uintptr) {
    setsockopt(fd, SOL_SOCKET, SO_MAX_PACING_RATE, uint64(bandwidthBps/8))
})
```

**Write Buffer Sizing** ([bwprobe/internal/network/sender.go:143-152](../internal/network/sender.go#L143-L152)):
```go
if rtt > 0 and bandwidthBps > 0:
    bdp = ceil(bandwidthBps/8 * rtt.Seconds())
    buffer = bdp
else:
    buffer = payloadSize + TCPFrameHeaderSize
```

### TCP Reverse Channel (Download)

**Connection Header** (RPC mode):
```
"RECV" (4 bytes)
uint16 session_id_len (big-endian)
session_id (UTF-8 string)
```

**Connection Header** (legacy mode):
```
"RECV" (4 bytes)
```

**Note**: The session ID block is **optional (RPC only)**. Legacy mode sends only the 4-byte `RECV` header with no session ID.

**Frame**: Same format as upload (sample_id, payload_len, payload)

**Flow**: Server writes, client reads. Server applies pacing.

### UDP Data Channel

**Packet Format (without session)** ([bwprobe/internal/protocol/types.go:13-24](../internal/protocol/types.go#L13-L24)):
```
[0]       type (uint8) = 1 (UDPTypeData)
[1:5]     sample_id (uint32, big-endian)
[5:13]    sequence (uint64, big-endian)
[13:...]  payload
```

**Packet Format (with session)** ([bwprobe/internal/network/sender.go:243-249](../internal/network/sender.go#L243-L249)):
```
[0]         type (uint8) = 6 (UDPTypeDataSession)
[1]         session_id_len (uint8)
[2:2+len]   session_id (UTF-8)
[2+len:2+len+4]    sample_id (uint32)
[2+len+4:2+len+12] sequence (uint64)
[2+len+12:...]     payload
```

**Max Packet Size**: 64 KB ([bwprobe/internal/protocol/types.go:24](../internal/protocol/types.go#L24))

**Note**: UDP chunk size is automatically capped to 64 KiB (not rejected) in `UDPPayloadSizeWithHeader` ([bwprobe/internal/network/sender.go:318-331](../internal/network/sender.go#L318-L331)).

**UDP Pacing**: Uses leaky bucket limiter (see [Rate Limiting Algorithm](#rate-limiting-algorithm))

### UDP Ping/Pong

**PING**:
```
[0]      type = 2 (UDPTypePing)
[1:9]    timestamp (int64, nanoseconds, big-endian)
```

**PONG**:
```
[0]      type = 3 (UDPTypePong)
[1:9]    timestamp (echo from PING)
```

---

## Edge Cases and Deviations

### 1. Empty Intervals

**Issue**: If server receives no data during a sample (network outage, zero-rate test), `intervals[]` is empty.

**Handling** ([bwprobe/internal/engine/samples.go:196-209](../internal/engine/samples.go#L196-L209)):
```go
if len(intervals) == 0:
    fallback = report.AvgThroughput
    if fallback <= 0 and report.TotalDuration > 0:
        fallback = report.TotalBytes * 8 / report.TotalDuration
    return sampleMetrics{peak1s: fallback, trimmed: fallback, p90: fallback, p80: fallback}
```

All metrics return the same fallback value (total average or zero).

### 2. Peak Window < 1 Second

**Issue**: If sample duration < 1 second, rolling window cannot find a valid 1s interval.

**Behavior** ([bwprobe/internal/engine/samples.go:333-335](../internal/engine/samples.go#L333-L335)):
```go
if dt < windowSec or dt <= 0:
    continue  // skip this window
```

If **no** window meets the 1s threshold, `peak = 0`, then fallback is used ([bwprobe/internal/engine/samples.go:240-242](../internal/engine/samples.go#L240-L242)):
```go
if peak <= 0 and report.TotalDuration > 0:
    peak = report.TotalBytes * 8 / report.TotalDuration
```

### 3. UDP Loss Underestimation

**Issue**: Duplicates increase `packetsRecv`, lowering loss estimate.

**Current Behavior**: Loss = `(maxSeq - baseSeq + 1) - packetsRecv`

**Example**:
- Sent: seq 0..9 (10 packets)
- Received: 0,1,2,2,3,4,5,6,7,8 (9 unique, 1 duplicate)
- packetsRecv = 10, maxSeq = 8, baseSeq = 0
- total = 8 - 0 + 1 = 9
- lost = max(0, 9 - 10) = 0

**Deviation**: True loss is 1 (seq 9 lost), but reported loss is 0 due to duplicate.

**Code Reference**: [bwprobe/internal/metrics/udp.go:38-49](../internal/metrics/udp.go#L38-L49)

### 4. Reverse TCP Timeout Retry

**Issue**: In reverse mode, if client's TCP read times out repeatedly, sample can fail even if data is arriving slowly.

**Protection** ([bwprobe/internal/engine/samples.go:92-116](../internal/engine/samples.go#L92-L116)):
```go
timeoutCount := 0
maxTimeouts := 10

if net.Error.Timeout():
    timeoutCount++
    if timeoutCount >= maxTimeouts:
        return error("too many timeouts (10)")
    continue  // retry
```

After 10 **total (non-resetting)** timeout errors, sample aborts. The timeout counter is never reset after successful reads, so this counts cumulative timeouts across the entire sample. Non-timeout errors (EOF, connection reset) abort immediately.

### 5. Context Cancellation During Inter-Sample Wait

**Issue**: If context is cancelled during `cfg.Wait` sleep, test should terminate immediately.

**Fix** ([bwprobe/internal/engine/samples.go:168-178](../internal/engine/samples.go#L168-L178)):
```go
if sample < cfg.Samples-1 && cfg.Wait > 0 {
    if ctx != nil {
        select {
        case <-ctx.Done():
            return result, ctx.Err()
        case <-time.After(cfg.Wait):
        }
    } else {
        time.Sleep(cfg.Wait)
    }
}
```

**Deviation**: If `ctx == nil`, wait is not cancellable (falls back to `time.Sleep`).

### 6. TCP Retransmit Estimation

**Issue**: If `TCP_INFO.Total_retrans` is unavailable (older kernels), fallback uses bytes:

```go
if retransmits == 0 and info.Bytes_retrans > 0 and info.Snd_mss > 0:
    retransmits = (info.Bytes_retrans + info.Snd_mss - 1) / info.Snd_mss
```

**Deviation**: This is an **estimate**. If MSS is wrong or retransmitted bytes include headers, count may be inaccurate.

**Code**: [bwprobe/internal/metrics/tcp.go:59-63](../internal/metrics/tcp.go#L59-L63)

### 7. Session ID Collision

**Issue**: UUIDs are generated with `uuid.New()` (v4, random). Collision probability is negligible but non-zero.

**Mitigation**: None. Server overwrites session if duplicate ID is generated (extremely rare).

**Code**: [bwprobe/internal/rpc/session.go:99](../internal/rpc/session.go#L99)

### 8. UDP Registration Race

**Issue**: Client sends UDP pings before calling `udp.register`, but server may not have processed them yet.

**Protection** ([bwprobe/internal/transport/transport.go:64-82](../internal/transport/transport.go#L64-L82)):

Client sends UDP pings (5 attempts with 50ms delay) before calling `udp.register`. Server validates within a 15-second window:

```go
// Client sends pings
SendUDPHello(conn, target, port)  // 5 attempts

// Then registers
resp = udp.register(session_id, port, test_packet_count=5)
if resp.status == "registered":
    // Success
```

**Deviation**: If server validation fails (no recent ping seen), registration returns an error and UDP reverse tests cannot proceed.

### 9. All Intervals Have Zero Duration

**Issue**: If all intervals exist but have `DurationMs <= 0`, throughput calculation fails.

**Handling** ([bwprobe/internal/engine/samples.go:217-231](../internal/engine/samples.go#L217-L231)):

```go
for _, interval := range intervals {
    if interval.DurationMs <= 0 {
        continue  // skip
    }
    // ... compute throughput
}
if len(throughputs) == 0:
    return sampleMetrics{}  // all zeros
```

**Deviation**: Throughput metrics return **0** (no fallback), even if intervals array is non-empty but all durations are non-positive.

---

## References

### Key Implementation Files

- **Throughput**: [bwprobe/internal/engine/samples.go](../internal/engine/samples.go)
- **RTT Sampler**: [bwprobe/internal/metrics/rtt_sampler.go](../internal/metrics/rtt_sampler.go)
- **TCP Metrics**: [bwprobe/internal/metrics/tcp.go](../internal/metrics/tcp.go)
- **UDP Loss**: [bwprobe/internal/metrics/udp.go](../internal/metrics/udp.go)
- **Rate Limiter**: [bwprobe/internal/network/ratelimit.go](../internal/network/ratelimit.go)
- **RPC Protocol**: [bwprobe/internal/rpc/protocol.go](../internal/rpc/protocol.go)
- **Session Manager**: [bwprobe/internal/rpc/session.go](../internal/rpc/session.go)
- **TCP Sender**: [bwprobe/internal/network/sender.go](../internal/network/sender.go)
- **Frame Types**: [bwprobe/internal/protocol/types.go](../internal/protocol/types.go)

### Task Logs

- **Code Review 1**: [task-log/bwprobe/code-review-1.md](../../task-log/bwprobe/code-review-1.md)
- **Code Review 2**: [task-log/bwprobe/code-review-2.md](../../task-log/bwprobe/code-review-2.md)
- **Action Log**: [task-log/bwprobe/action-1.md](../../task-log/bwprobe/action-1.md)
- **Protocol Refinement**: [task-log/bwprobe/3-control-protocol/protocol-refinement.md](../../task-log/bwprobe/3-control-protocol/protocol-refinement.md)

---

**Document Version**: 1.0
**Last Updated**: 2026-01-28
**Implementation Version**: Based on code as of commit 717beac
