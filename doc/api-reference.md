# API reference

This reference documents programmatic interfaces for fbforward and bwprobe. For CLI usage, see user guides ([fbforward](user-guide-fbforward.md), [bwprobe](user-guide-bwprobe.md)).

---

## 5.1 bwprobe public API

### 5.1.1 Overview

The bwprobe public API is provided as the `github.com/NodePath81/fbforward/bwprobe/pkg` package. This Go library enables embedding bandwidth measurements in custom applications.

**Import path:**

```go
import probe "github.com/NodePath81/fbforward/bwprobe/pkg"
```

**Use cases:**
- Custom network monitoring tools
- Integration with external orchestration systems
- Programmatic network quality testing
- Measurement data collection for analytics

**Relationship to CLI:** The bwprobe CLI binary ([bwprobe/cmd](../bwprobe/cmd/main.go)) is a thin wrapper around this API.

### 5.1.2 Types

#### Config

Test configuration struct.

```go
type Config struct {
    Target       string        // Host or IP of server (required)
    Port         int           // Control/data port (default: 9876)
    Network      string        // Protocol: "tcp" or "udp" (default: "tcp")
    BandwidthBps int64         // Target bandwidth cap in bps (required)
    Reverse      bool          // Download test (default: false)
    Samples      int           // Number of samples (default: 10)
    SampleBytes  int64         // Payload bytes per sample (required)
    Wait         time.Duration // Pause between samples (default: 0)
    MaxDuration  time.Duration // Max test duration (default: unlimited)
    RTTRate      int           // RTT samples per second (default: 10)
    ChunkSize    int64         // Chunk size including headers (default: 1200)
}
```

**Validation:** `Target`, `BandwidthBps`, and `SampleBytes` are required. Other fields use defaults if zero.

**Example:**

```go
cfg := probe.Config{
    Target:       "upstream.example.com",
    Port:         9876,
    Network:      "tcp",
    BandwidthBps: 50_000_000, // 50 Mbps
    SampleBytes:  5_000_000,  // 5 MB
    Samples:      10,
}
```

#### Results

Complete test results struct.

```go
type Results struct {
    Throughput       Throughput    // Bandwidth measurements
    RTT              RTTStats      // Round-trip time stats
    Loss             LossStats     // Retransmit or packet-loss stats
    TestDuration     time.Duration // Wall-clock duration
    BytesSent        int64         // Payload bytes sent (upload)
    BytesReceived    int64         // Payload bytes received (download)
    SamplesPlanned   int           // Requested number of samples
    SamplesCompleted int           // Number of completed samples
    Network          string        // "tcp" or "udp"
    TCPSendBufferBytes uint64      // TCP send buffer size (if available)
}
```

**Example:**

```go
results, err := probe.Run(ctx, cfg)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Achieved bandwidth: %.2f Mbps\n", results.Throughput.AchievedBps/1e6)
fmt.Printf("Mean RTT: %v\n", results.RTT.Mean)
```

#### Throughput

Bandwidth measurements in bits per second.

```go
type Throughput struct {
    TargetBps      int64   // Configured target rate
    AchievedBps    float64 // Reported bandwidth (trimmed mean)
    Utilization    float64 // AchievedBps / TargetBps
    TrimmedMeanBps float64 // Trimmed mean of interval rates
    Peak1sBps      float64 // Sustained peak over 1s rolling window
    P90Bps         float64 // 90th percentile of interval rates
    P80Bps         float64 // 80th percentile of interval rates
}
```

**Trimmed mean:** Average throughput after dropping top/bottom 10% of interval rates. This is the primary bandwidth metric (`AchievedBps`).

**Sustained peak:** Maximum average throughput over any 1-second window. Indicates burst capacity.

**Percentiles:** P90/P80 values show throughput distribution.

#### RTTStats

Round-trip time measurements.

```go
type RTTStats struct {
    Min     time.Duration // Minimum RTT sample
    Mean    time.Duration // Average RTT
    Max     time.Duration // Maximum RTT sample
    Jitter  time.Duration // Standard deviation of RTT samples
    Samples int           // Number of RTT samples collected
}
```

**RTT sampling:** Continuous during tests at configured `RTTRate` (default 10 samples/sec).

**Jitter:** Standard deviation measures latency stability. Lower jitter indicates more stable latency.

#### LossStats

Loss or retransmit statistics.

```go
type LossStats struct {
    Protocol     string  // "tcp" or "udp"
    LossRate     float64 // retransmits/segments (TCP) or packets_lost/packets_sent (UDP)

    // TCP fields (sender side)
    Retransmits  uint64  // Number of TCP retransmits
    SegmentsSent uint64  // Total TCP segments sent

    // UDP fields (server side)
    PacketsLost  uint64  // Number of UDP packets lost
    PacketsRecv  uint64  // Number of UDP packets received
    PacketsSent  uint64  // Number of UDP packets sent
}
```

**TCP loss rate:** Derived from `TCP_INFO` socket statistics on sender side.

**UDP loss rate:** Computed from sequence number gaps on receiver side.

### 5.1.3 Functions

#### Run

Execute a complete network quality test.

```go
func Run(ctx context.Context, cfg Config) (*Results, error)
```

**Parameters:**
- `ctx`: Context for cancellation and timeout
- `cfg`: Test configuration

**Returns:**
- `*Results`: Test results (nil on error)
- `error`: Error details (nil on success)

**Errors:**
- Connection failures (server unreachable, port closed)
- Timeout (test exceeds `cfg.MaxDuration`)
- Protocol errors (incompatible server version)
- Invalid configuration (zero target bandwidth, zero sample size)

**Example:**

```go
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()

cfg := probe.Config{
    Target:       "203.0.113.10",
    BandwidthBps: 100_000_000, // 100 Mbps
    SampleBytes:  10_000_000,  // 10 MB
}

results, err := probe.Run(ctx, cfg)
if err != nil {
    log.Fatalf("test failed: %v", err)
}

fmt.Printf("Throughput: %.2f Mbps (%.1f%% utilization)\n",
    results.Throughput.AchievedBps/1e6,
    results.Throughput.Utilization*100)
```

#### RunWithProgress

Execute a test with progress callback.

```go
func RunWithProgress(ctx context.Context, cfg Config, progress ProgressFunc) (*Results, error)
```

**Parameters:**
- `ctx`: Context for cancellation and timeout
- `cfg`: Test configuration
- `progress`: Callback function (may be nil)

**Returns:** Same as `Run`.

**ProgressFunc signature:**

```go
type ProgressFunc func(phase string, percentComplete float64, status string)
```

**Progress callback parameters:**
- `phase`: Current sample (e.g., "sample 3/10")
- `percentComplete`: Sample progress [0.0, 1.0]
- `status`: Human-readable status (e.g., "120 Mbps | 15.0 MB")

**Example:**

```go
progress := func(phase string, pct float64, status string) {
    fmt.Printf("\r[%s] %.0f%% %s", phase, pct*100, status)
}

results, err := probe.RunWithProgress(ctx, cfg, progress)
if err != nil {
    log.Fatal(err)
}
fmt.Println() // Clear progress line
```

#### MeasureRTT

Measure round-trip time only (no bandwidth test).

```go
func MeasureRTT(ctx context.Context, cfg RTTConfig) (*RTTStats, error)
```

**Parameters:**
- `ctx`: Context for cancellation and timeout
- `cfg`: RTT measurement configuration

**RTTConfig type:**

```go
type RTTConfig struct {
    Target  string        // Host or IP to measure (required)
    Port    int           // Port to probe (default: 9876)
    Network string        // Protocol: "tcp" or "udp" (default: "tcp")
    Samples int           // Number of RTT samples (default: 10)
    Rate    int           // Sampling rate per second (default: 10)
    Timeout time.Duration // Per-ping timeout (default: 1s)
}
```

**Returns:**
- `*RTTStats`: RTT statistics (nil on error)
- `error`: Error details (nil on success)

**Example:**

```go
rttCfg := probe.RTTConfig{
    Target:  "upstream.example.com",
    Samples: 20,
    Rate:    10,
}

stats, err := probe.MeasureRTT(ctx, rttCfg)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("RTT: mean=%v min=%v max=%v jitter=%v\n",
    stats.Mean, stats.Min, stats.Max, stats.Jitter)
```

### 5.1.4 Examples

#### Basic TCP upload test

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    probe "github.com/NodePath81/fbforward/bwprobe/pkg"
)

func main() {
    cfg := probe.Config{
        Target:       "upstream.example.com",
        BandwidthBps: 50_000_000, // 50 Mbps
        SampleBytes:  5_000_000,  // 5 MB
        Samples:      5,
    }

    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    results, err := probe.Run(ctx, cfg)
    if err != nil {
        log.Fatalf("test failed: %v", err)
    }

    fmt.Printf("Throughput: %.2f Mbps (%.1f%% utilization)\n",
        results.Throughput.AchievedBps/1e6,
        results.Throughput.Utilization*100)
    fmt.Printf("RTT: mean=%v jitter=%v\n", results.RTT.Mean, results.RTT.Jitter)
    fmt.Printf("Loss: %.4f%%\n", results.Loss.LossRate*100)
}
```

#### TCP download test

```go
cfg := probe.Config{
    Target:       "upstream.example.com",
    BandwidthBps: 200_000_000, // 200 Mbps
    SampleBytes:  10_000_000,  // 10 MB
    Reverse:      true,         // Download test
}

results, err := probe.Run(ctx, cfg)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Download speed: %.2f Mbps\n", results.Throughput.AchievedBps/1e6)
```

#### UDP test with progress

```go
cfg := probe.Config{
    Target:       "upstream.example.com",
    Network:      "udp",
    BandwidthBps: 50_000_000, // 50 Mbps
    SampleBytes:  5_000_000,  // 5 MB
    Samples:      10,
}

progress := func(phase string, pct float64, status string) {
    if pct >= 1.0 {
        fmt.Printf("\r[%s] Complete                    \n", phase)
    } else {
        fmt.Printf("\r[%s] %.0f%% %s", phase, pct*100, status)
    }
}

results, err := probe.RunWithProgress(ctx, cfg, progress)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("UDP loss rate: %.4f%%\n", results.Loss.LossRate*100)
```

#### Periodic monitoring loop

```go
ticker := time.NewTicker(5 * time.Minute)
defer ticker.Stop()

cfg := probe.Config{
    Target:       "upstream.example.com",
    BandwidthBps: 50_000_000,
    SampleBytes:  1_000_000, // Smaller samples for quick tests
    Samples:      3,
}

for {
    select {
    case <-ticker.C:
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        results, err := probe.Run(ctx, cfg)
        cancel()

        if err != nil {
            log.Printf("measurement failed: %v", err)
            continue
        }

        log.Printf("bandwidth=%.2f Mbps rtt=%v loss=%.4f%%",
            results.Throughput.AchievedBps/1e6,
            results.RTT.Mean,
            results.Loss.LossRate*100)
    }
}
```

---

## 5.2 Control plane API

### 5.2.1 Overview

The fbforward control plane exposes HTTP endpoints for runtime management, monitoring, and observability.

**Base URL:** `http://{control.bind_addr}:{control.bind_port}`

**Default:** `http://127.0.0.1:8080`

**Authentication:** Bearer token (configured via `control.auth_token`).

**Data source responsibilities:**

The control plane follows a single-source-of-truth architecture:

| Data Type | Source | Notes |
|-----------|--------|-------|
| Active connections (list) | WebSocket `connections_snapshot` | Periodic, subscription-controlled |
| Queue status (list) | WebSocket `queue_snapshot` | Periodic, subscription-controlled |
| Numeric metrics (bandwidth, RTT, scores) | Prometheus `/metrics` | Poll-based, summary metrics only |
| Test history (events) | WebSocket `test_history_event` | Event-driven, broadcast immediately |
| Session history (events) | WebSocket `add`/`update`/`remove` | Event-driven, broadcast immediately |
| Control commands | RPC `/rpc` | `SetUpstream`, `Restart`, `RunMeasurement` |
| Config queries | RPC `/rpc` | `GetStatus`, `GetMeasurementConfig`, `GetRuntimeConfig` |

**Key principles:**
- WebSocket delivers connection/queue telemetry via subscription (no polling)
- RPC methods provide only control actions and non-metric status queries
- Prometheus provides all numeric metrics (bandwidth, RTT, jitter, scores, utilization)
- No data duplication across endpoints

**Endpoints:**

| Path | Method | Auth Required | Description |
|------|--------|---------------|-------------|
| `/` | GET | No | Web UI (embedded SPA) |
| `/auth` | GET | No | Authentication page for token input |
| `/rpc` | POST | Yes | JSON-RPC methods |
| `/status` | GET | Yes | WebSocket status stream |
| `/identity` | GET | Yes | Instance identity (hostname, IPs, version) |
| `/metrics` | GET | Yes | Prometheus metrics |

**Note:** Only `/` and `/auth` are publicly accessible. All other endpoints require Bearer token authentication.

### 5.2.2 RPC methods

The `/rpc` endpoint accepts JSON-RPC 2.0 requests.

#### Authentication

Include Bearer token in `Authorization` header:

```bash
curl -X POST http://localhost:8080/rpc \
  -H "Authorization: Bearer your-auth-token" \
  -H "Content-Type: application/json" \
  -d '{"method": "GetStatus", "params": {}}'
```

#### Request format

```json
{
  "method": "MethodName",
  "params": {...}
}
```

#### Response format

```json
{
  "ok": true,
  "result": {...}
}
```

Error response:

```json
{
  "ok": false,
  "error": "error message"
}
```

#### Rate limiting

RPC requests are rate-limited to prevent abuse:

- **Limit:** 5 requests per second per client IP
- **Burst:** Up to 10 requests in burst
- **Window:** 5-minute rolling window

When rate limit is exceeded, the server returns HTTP 429 (Too Many Requests). Clients should implement exponential backoff when encountering rate limit errors.

#### SetUpstream

Set upstream selection mode.

**Method:** `SetUpstream`

**Parameters:**

```json
{
  "mode": "auto" | "manual",
  "tag": "upstream-tag" // Required if mode=manual
}
```

**Result:** `null` (success indicated by `ok: true`)

**Example (switch to auto mode):**

```bash
curl -X POST http://localhost:8080/rpc \
  -H "Authorization: Bearer token" \
  -H "Content-Type: application/json" \
  -d '{"method": "SetUpstream", "params": {"mode": "auto"}}'
```

**Example (pin to specific upstream):**

```bash
curl -X POST http://localhost:8080/rpc \
  -H "Authorization: Bearer token" \
  -H "Content-Type: application/json" \
  -d '{"method": "SetUpstream", "params": {"mode": "manual", "tag": "primary"}}'
```

#### Restart

Trigger config reload and restart runtime.

**Method:** `Restart`

**Parameters:** Empty object `{}`

**Result:** `null`

**Example:**

```bash
curl -X POST http://localhost:8080/rpc \
  -H "Authorization: Bearer token" \
  -H "Content-Type: application/json" \
  -d '{"method": "Restart", "params": {}}'
```

**Behavior:** fbforward reloads configuration, stops existing runtime, creates new runtime, and restarts listeners and probes. Active flows are terminated.

#### GetStatus

Retrieve current runtime status.

**Method:** `GetStatus`

**Parameters:** Empty object `{}`

**Result:**

```json
{
  "mode": "auto" | "manual",
  "active_upstream": "tag",
  "upstreams": [
    {
      "tag": "primary",
      "host": "upstream1.example.com",
      "ips": ["203.0.113.10", "203.0.113.11"],
      "active_ip": "203.0.113.10",
      "active": true,
      "usable": true,
      "reachable": true
    }
  ]
}
```

**Field descriptions:**

- `tag`: Upstream identifier
- `host`: Configured hostname or IP
- `ips`: All resolved IP addresses
- `active_ip`: Currently selected IP for forwarding
- `active`: Whether this is the primary upstream (receives new flows)
- `usable`: Whether upstream is eligible for selection (not failed/unreachable)
- `reachable`: ICMP probe reachability status

**Note:** Numeric metrics (bandwidth, RTT, jitter, scores, utilization) are available exclusively via Prometheus `/metrics` endpoint. Active connection counts are available via WebSocket `connections_snapshot` or Prometheus metrics.

**Example:**

```bash
curl -X POST http://localhost:8080/rpc \
  -H "Authorization: Bearer token" \
  -H "Content-Type: application/json" \
  -d '{"method": "GetStatus", "params": {}}'
```

#### ListUpstreams

List all upstreams with detailed stats (same as `GetStatus` upstreams field).

**Method:** `ListUpstreams`

**Parameters:** Empty object `{}`

**Result:** Array of upstream objects (same format as `GetStatus.upstreams`)

#### GetMeasurementConfig

Retrieve current measurement configuration.

**Method:** `GetMeasurementConfig`

**Parameters:** Empty object `{}`

**Result:**

```json
{
  "startup_delay": "10s",
  "stale_threshold": "1h0m0s",
  "fallback_to_icmp_on_stale": true,
  "schedule": {
    "interval": {"min": "15m0s", "max": "45m0s"},
    "upstream_gap": "5s",
    "headroom": {
      "max_link_utilization": 0.7,
      "required_free_bandwidth": "0"
    }
  },
  "fast_start": {
    "enabled": true,
    "timeout": "500ms",
    "warmup_duration": "15s"
  },
  "protocols": {
    "tcp": {
      "enabled": true,
      "alternate": true,
      "target_bandwidth": {"upload": "10m", "download": "50m"},
      "chunk_size": "1200",
      "sample_size": "500kb",
      "sample_count": 1,
      "timeout": {"per_sample": "10s", "per_cycle": "30s"}
    },
    "udp": {...}
  }
}
```

#### GetRuntimeConfig

Retrieve complete runtime configuration (all sections).

**Method:** `GetRuntimeConfig`

**Parameters:** Empty object `{}`

**Result:** Full configuration object with all sections (`forwarding`, `upstreams`, `dns`, `reachability`, `measurement`, `scoring`, `switching`, `control`, `shaping`).

#### GetScheduleStatus

Retrieve measurement scheduler status.

**Method:** `GetScheduleStatus`

**Parameters:** Empty object `{}`

**Result:**

```json
{
  "queue_length": 2,
  "next_scheduled": "2026-01-27T10:20:00Z",
  "last_measurements": {
    "primary": "2026-01-27T10:15:30Z",
    "backup": "2026-01-27T10:16:15Z"
  },
  "skipped_total": 5
}
```

**Field descriptions:**

- `queue_length`: Number of pending measurements in queue
- `next_scheduled`: Timestamp of next scheduled measurement (null if queue empty)
- `last_measurements`: Map of upstream tag → last successful measurement timestamp
- `skipped_total`: Cumulative count of measurements skipped due to insufficient headroom

**Example:**

```bash
curl -X POST http://localhost:8080/rpc \
  -H "Authorization: Bearer token" \
  -H "Content-Type: application/json" \
  -d '{"method": "GetScheduleStatus", "params": {}}'
```

#### RunMeasurement

Trigger manual measurement for specific upstream and protocol.

**Method:** `RunMeasurement`

**Parameters:**

```json
{
  "tag": "primary",
  "protocol": "tcp" | "udp"
}
```

**Result:** `null` (measurement runs asynchronously)

**Example:**

```bash
curl -X POST http://localhost:8080/rpc \
  -H "Authorization: Bearer token" \
  -H "Content-Type: application/json" \
  -d '{"method": "RunMeasurement", "params": {"tag": "primary", "protocol": "tcp"}}'
```

### 5.2.3 WebSocket status stream

The `/status` endpoint provides real-time status updates via WebSocket.

#### Authentication

Bearer token via `Authorization` header or WebSocket subprotocol.

**Browser usage (subprotocol):**

```javascript
const token = 'your-auth-token';
const encoded = btoa(token).replace(/=/g, '');
const ws = new WebSocket('ws://localhost:8080/status', [`fbforward-token.${encoded}`]);
```

**CLI usage (header):**

```bash
websocat -H "Authorization: Bearer token" ws://localhost:8080/status
```

#### Subscription protocol

The WebSocket uses a subscription model where clients control telemetry intervals.

**Client sends subscribe:**

```json
{
  "type": "subscribe",
  "interval_ms": 2000
}
```

**Allowed intervals:** 1000, 2000, or 5000 milliseconds

**Server response:**
- On success: Starts per-client ticker and sends initial `connections_snapshot` and `queue_snapshot`
- On error: Sends error message with `invalid_interval` code

**Update subscription interval:**

Send a new `subscribe` message with different `interval_ms`. The server will cancel the old ticker and start a new one with the updated interval.

**Unsubscribe:**

```json
{
  "type": "unsubscribe"
}
```

Server stops sending periodic snapshots and cleans up ticker resources.

**Delivery model:**
- `connections_snapshot` and `queue_snapshot`: Periodic, controlled by subscription interval
- `add`, `update`, `remove`, `test_history_event`: Event-driven, broadcast immediately regardless of subscription state

#### Message format

The WebSocket stream sends JSON messages for flow (TCP connection/UDP mapping), queue status, and measurement history events. **Upstream metrics are not streamed**; use Prometheus `/metrics` endpoint for numeric metrics (bandwidth, RTT, jitter, scores, utilization).

**All messages include schema version:**

```json
{
  "schema_version": 1,
  "type": "...",
  ...
}
```

**Message types:**

**1. Connections snapshot (server → client, periodic):**

```json
{
  "schema_version": 1,
  "type": "connections_snapshot",
  "timestamp": 1706354410000,
  "tcp": [
    {
      "kind": "tcp",
      "id": "tcp-1706354400123456789-1",
      "client_addr": "10.0.0.5:54321",
      "port": 8000,
      "upstream": "primary",
      "bytes_up": 12345,
      "bytes_down": 67890,
      "segments_up": 45,
      "segments_down": 78,
      "last_activity": 1706354410000,
      "age": 15
    }
  ],
  "udp": [
    {
      "kind": "udp",
      "id": "udp-1706354405000000000-2",
      "client_addr": "10.0.0.6:12345",
      "port": 8000,
      "upstream": "backup",
      "bytes_up": 8192,
      "bytes_down": 4096,
      "segments_up": 10,
      "segments_down": 5,
      "last_activity": 1706354412000,
      "age": 7
    }
  ]
}
```

**2. Queue snapshot (server → client, periodic):**

```json
{
  "schema_version": 1,
  "type": "queue_snapshot",
  "timestamp": 1706354410000,
  "depth": 3,
  "skipped": 12,
  "next_due_ms": 5000,
  "running": [
    {
      "upstream": "primary",
      "protocol": "tcp",
      "direction": "upload",
      "elapsed_ms": 1234
    }
  ],
  "pending": [
    {
      "upstream": "backup",
      "protocol": "tcp",
      "direction": "download",
      "scheduled_at": 1706354415000
    }
  ]
}
```

**3. Add (server → client, event-driven when new flow starts):**

```json
{
  "schema_version": 1,
  "type": "add",
  "entry": {
    "kind": "tcp",
    "id": "tcp-1706354420000000000-3",
    "client_addr": "10.0.0.7:33445",
    "port": 8000,
    "upstream": "primary",
    "bytes_up": 0,
    "bytes_down": 0,
    "segments_up": 0,
    "segments_down": 0,
    "last_activity": 1706354420000,
    "age": 0
  }
}
```

**4. Update (server → client, event-driven updates for active flows):**

```json
{
  "schema_version": 1,
  "type": "update",
  "entry": {
    "kind": "tcp",
    "id": "tcp-1706354400123456789-1",
    "client_addr": "10.0.0.5:54321",
    "port": 8000,
    "upstream": "primary",
    "bytes_up": 98765,
    "bytes_down": 123456,
    "segments_up": 234,
    "segments_down": 456,
    "last_activity": 1706354425000,
    "age": 30
  }
}
```

**5. Remove (server → client, event-driven when flow terminates):**

```json
{
  "schema_version": 1,
  "type": "remove",
  "id": "tcp-1706354400123456789-1",
  "kind": "tcp"
}
```

**6. Test history event (server → client, event-driven after measurement finishes):**

```json
{
  "schema_version": 1,
  "type": "test_history_event",
  "upstream": "primary",
  "protocol": "tcp",
  "direction": "upload",
  "timestamp": 1706354430000,
  "duration_ms": 2534,
  "success": true,
  "bandwidth_up_bps": 48500000,
  "bandwidth_down_bps": 0,
  "rtt_ms": 25.4,
  "jitter_ms": 2.1,
  "loss_rate": 0.0,
  "retrans_rate": 0.0012,
  "error": ""
}
```

**7. Error (server → client when subscription validation fails):**

```json
{
  "schema_version": 1,
  "type": "error",
  "code": "invalid_interval",
  "message": "interval_ms must be 1000, 2000, or 5000"
}
```

**Field descriptions (connections_snapshot and queue_snapshot):**

- `schema_version`: Message schema version (currently 1)
- `timestamp`: Unix milliseconds when snapshot was generated
- `depth`: Number of pending measurements in queue
- `skipped`: Cumulative count of skipped measurements
- `next_due_ms`: Milliseconds until next scheduled measurement (null if queue empty)
- `running`: Array of currently executing measurements
- `pending`: Array of queued measurements awaiting execution

**Field descriptions (StatusEntry - used in add/update/remove):**

- `kind`: Protocol type (`tcp` or `udp`)
- `id`: Unique flow identifier
- `client_addr`: Client IP:port
- `port`: Listener port
- `upstream`: Pinned upstream tag
- `bytes_up`, `bytes_down`: Cumulative bytes transferred
- `segments_up`, `segments_down`: Cumulative packets/segments
- `last_activity`: Unix milliseconds of last I/O
- `age`: Seconds since flow creation

**Field descriptions (test_history_event):**

- `schema_version`: Message schema version (currently 1)
- `upstream`: Upstream tag
- `protocol`: `tcp` or `udp`
- `direction`: `upload` or `download`
- `timestamp`: Unix milliseconds when test started
- `duration_ms`: Test duration
- `success`: Whether test completed successfully
- `bandwidth_up_bps`, `bandwidth_down_bps`: Measured bandwidth (0 if not applicable to direction)
- `rtt_ms`, `jitter_ms`: RTT statistics
- `loss_rate`, `retrans_rate`: Loss/retransmit rates (protocol-dependent)
- `error`: Error message if `success: false` (empty string on success)

#### Connection lifecycle

1. Client connects with Bearer token authentication (header or subprotocol)
2. Server accepts upgrade and selects subprotocol `fbforward`
3. Client sends `subscribe` message with desired `interval_ms` (1000, 2000, or 5000)
4. Server validates interval and starts per-client ticker
5. Server sends initial `connections_snapshot` and `queue_snapshot` immediately
6. Server sends periodic snapshots at configured interval (while subscribed)
7. Server pushes `add`/`update`/`remove` messages for flow events (event-driven, independent of subscription)
8. Server pushes `test_history_event` messages for measurement completions (event-driven, independent of subscription)
9. Server sends ping every 30 seconds
10. Client must respond with pong within 60 seconds
11. Client can send new `subscribe` message to change interval
12. Client can send `unsubscribe` message to stop periodic snapshots
13. On disconnect, server cancels ticker and cleans up resources

**Example (JavaScript):**

```javascript
const token = 'your-auth-token';
const encoded = btoa(token).replace(/=/g, '');
const ws = new WebSocket('ws://localhost:8080/status', [`fbforward-token.${encoded}`]);

ws.onopen = () => {
  console.log('Connected');
  // Subscribe with 2-second interval
  ws.send(JSON.stringify({type: 'subscribe', interval_ms: 2000}));
};

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);

  switch (msg.type) {
    case 'connections_snapshot':
      console.log('Connection snapshot:', msg.tcp.length, 'TCP,', msg.udp.length, 'UDP');
      break;
    case 'queue_snapshot':
      console.log('Queue depth:', msg.depth, 'Running:', msg.running.length);
      break;
    case 'add':
      console.log('New flow:', msg.entry.id);
      break;
    case 'update':
      console.log('Flow update:', msg.entry.id);
      break;
    case 'remove':
      console.log('Flow closed:', msg.id);
      break;
    case 'test_history_event':
      console.log('Test completed:', msg.upstream, msg.protocol, msg.direction, msg.success);
      break;
    case 'error':
      console.error('WebSocket error:', msg.code, msg.message);
      break;
  }
};

ws.onerror = (error) => {
  console.error('WebSocket connection error:', error);
};

ws.onclose = () => {
  console.log('Disconnected');
};

// Change interval after connection
function changeInterval(newIntervalMs) {
  ws.send(JSON.stringify({type: 'subscribe', interval_ms: newIntervalMs}));
}

// Unsubscribe from periodic snapshots
function unsubscribe() {
  ws.send(JSON.stringify({type: 'unsubscribe'}));
}
```

### 5.2.4 Prometheus metrics

The `/metrics` endpoint exposes metrics in Prometheus text format.

#### Authentication

Requires Bearer token:

```bash
curl -H "Authorization: Bearer token" http://localhost:8080/metrics
```

#### Metric catalog

**Upstream quality metrics (per upstream):**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `fbforward_upstream_rtt_ms` | gauge | `upstream` | Mean RTT (milliseconds) |
| `fbforward_upstream_jitter_ms` | gauge | `upstream` | RTT jitter (standard deviation) |
| `fbforward_upstream_bandwidth_up_bps` | gauge | `upstream` | Upload bandwidth (bits/sec) |
| `fbforward_upstream_bandwidth_down_bps` | gauge | `upstream` | Download bandwidth (bits/sec) |
| `fbforward_upstream_bandwidth_tcp_up_bps` | gauge | `upstream` | TCP upload bandwidth |
| `fbforward_upstream_bandwidth_tcp_down_bps` | gauge | `upstream` | TCP download bandwidth |
| `fbforward_upstream_bandwidth_udp_up_bps` | gauge | `upstream` | UDP upload bandwidth |
| `fbforward_upstream_bandwidth_udp_down_bps` | gauge | `upstream` | UDP download bandwidth |
| `fbforward_upstream_retrans_rate` | gauge | `upstream` | TCP retransmit rate [0, 1] |
| `fbforward_upstream_loss_rate` | gauge | `upstream` | UDP loss rate [0, 1] |
| `fbforward_upstream_loss` | gauge | `upstream` | Generic loss metric |
| `fbforward_upstream_score_tcp` | gauge | `upstream` | TCP quality score |
| `fbforward_upstream_score_udp` | gauge | `upstream` | UDP quality score |
| `fbforward_upstream_score_overall` | gauge | `upstream` | Blended quality score |
| `fbforward_upstream_score` | gauge | `upstream` | Final score (after adjustments) |
| `fbforward_upstream_utilization` | gauge | `upstream` | Link utilization [0, 1] |
| `fbforward_upstream_utilization_up` | gauge | `upstream` | Upload utilization |
| `fbforward_upstream_utilization_down` | gauge | `upstream` | Download utilization |
| `fbforward_upstream_reachable` | gauge | `upstream` | Reachable (1) or not (0) |
| `fbforward_upstream_unusable` | gauge | `upstream` | Unusable (1) or usable (0) |

**Upstream traffic metrics (per upstream):**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `fbforward_bytes_up_total` | counter | `upstream` | Total uploaded bytes |
| `fbforward_bytes_down_total` | counter | `upstream` | Total downloaded bytes |
| `fbforward_bytes_up_per_second` | gauge | `upstream` | Upload rate (bytes/sec, 1s window) |
| `fbforward_bytes_down_per_second` | gauge | `upstream` | Download rate (bytes/sec, 1s window) |
| `fbforward_upstream_tcp_up_rate_bps` | gauge | `upstream` | TCP upload rate (bits/sec) |
| `fbforward_upstream_tcp_down_rate_bps` | gauge | `upstream` | TCP download rate (bits/sec) |
| `fbforward_upstream_udp_up_rate_bps` | gauge | `upstream` | UDP upload rate (bits/sec) |
| `fbforward_upstream_udp_down_rate_bps` | gauge | `upstream` | UDP download rate (bits/sec) |

**Global metrics:**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `fbforward_mode` | gauge | - | Selection mode: 1=manual, 0=auto |
| `fbforward_active_upstream` | gauge | `upstream` | Active upstream: 1=active, 0=inactive |
| `fbforward_tcp_active` | gauge | - | Active TCP connections |
| `fbforward_udp_mappings_active` | gauge | - | Active UDP mappings |
| `fbforward_measurement_queue_size` | gauge | - | Pending measurements in queue |
| `fbforward_measurement_skipped_total` | counter | - | Total skipped measurements |
| `fbforward_measurement_last_run_seconds` | gauge | `upstream`, `protocol`, `direction` | Seconds since last measurement |
| `fbforward_memory_alloc_bytes` | gauge | - | Allocated memory (bytes) |
| `fbforward_uptime_seconds` | gauge | - | Process uptime (seconds) |

#### Scrape configuration

**Prometheus scrape config:**

```yaml
scrape_configs:
  - job_name: 'fbforward'
    static_configs:
      - targets: ['localhost:8080']
    bearer_token: 'your-auth-token'
    metrics_path: '/metrics'
    scrape_interval: 15s
```

**Example queries:**

```promql
# Active upstream score
fbforward_upstream_score{upstream="primary"}

# Total traffic (all upstreams)
sum(rate(fbforward_bytes_up_total[5m]))

# Upload bandwidth per upstream
fbforward_upstream_bandwidth_up_bps

# Active flows
fbforward_tcp_active + fbforward_udp_mappings_active

# Upstream utilization
fbforward_upstream_utilization > 0.7

# Measurement queue depth
fbforward_measurement_queue_size
```

**Dashboard example:**

```promql
# Panel: Upstream scores
fbforward_upstream_score

# Panel: Active upstream indicator
fbforward_active_upstream

# Panel: Traffic rates (upload)
sum by (upstream) (rate(fbforward_bytes_up_total[1m]) * 8)

# Panel: RTT comparison
fbforward_upstream_rtt_ms

# Panel: Loss rates
fbforward_upstream_retrans_rate{} or fbforward_upstream_loss_rate{}
```

#### Metric interpretation

**Score metrics:** Higher is better. Range [0, 100+]. Scores above 100 indicate priority boost from `upstreams[].priority` or positive `bias`.

**Utilization metrics:** Range [0, 1]. Values above threshold (default 0.7) trigger utilization penalty in scoring.

**Reachable vs unusable:**
- `reachable=1`: ICMP probes succeed
- `unusable=1`: Upstream cannot be selected (100% loss, consecutive dial failures)
- An upstream can be reachable but unusable (e.g., high loss rate)

**Rate metrics:** `bytes_up_per_second` and `bytes_down_per_second` use 1-second window. Protocol-specific rates (`tcp_up_rate_bps`, etc.) also use 1-second window.

---

## Cross-reference

| API | User guide | Algorithm reference |
|-----|------------|---------------------|
| bwprobe/pkg | [3.2](user-guide-bwprobe.md) | [6.2](algorithm-specifications.md#62-bandwidth-measurement-algorithm-bwprobe) |
| Control RPC | [3.1.3](user-guide-fbforward.md#313-operation) | - |
| WebSocket | [3.1.3](user-guide-fbforward.md#313-operation) | - |
| Prometheus | [3.1.3](user-guide-fbforward.md#313-operation) | - |
