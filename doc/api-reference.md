# API reference

This reference documents programmatic interfaces for fbforward and bwprobe. For
CLI usage, see user guides ([fbforward](user-guide-fbforward.md),
[bwprobe](user-guide-bwprobe.md)).

fbnotify now has standalone product documentation under
[doc/fbnotify/](fbnotify/index.md), including its
[API reference](fbnotify/api.md) and
[user guide](fbnotify/user-guide.md).

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
| Active connections (list) | RPC `GetActiveFlows` | Client-controlled polling |
| Numeric metrics (health, RTT, probes, traffic rates) | Prometheus `/metrics` | Poll-based, summary metrics only |
| Control commands | RPC `/rpc` | `GetRouteStatus`, `SetRouteOverride`, `ClearRouteOverride`, `SetUpstream` (deprecated), `Restart`, `RunMeasurement` |
| Config and scheduler queries | RPC `/rpc` | `GetStatus`, `GetRouteStatus`, `GetMeasurementConfig`, `GetRuntimeConfig`, `GetScheduleStatus`, `GetGeoIPStatus`, `GetFirewallPolicy`, `GetFirewallStatus`, `ValidateFirewallPolicy`, `ReloadFirewallPolicy`, `GetIPLogStatus`, `QueryIPLog`, `QueryRejectionLog`, `QueryLogEvents` |
| Online rule operations | RPC `/rpc` | `CreateOnlineRule`, `ListOnlineRules`, `DeleteOnlineRule`, `ExpireOnlineRule` |
| GeoIP/IP-log operations | RPC `/rpc` | `RefreshGeoIP` (trigger re-download) |

**Key principles:**
- `GetActiveFlows` returns active-flow telemetry as a point-in-time snapshot.
- RPC methods provide only control actions and non-metric status queries
- Prometheus provides all numeric metrics (health, RTT, probes, and traffic rates)
- No data duplication across endpoints

**Endpoints:**

| Path | Method | Auth Required | Description |
|------|--------|---------------|-------------|
| `/` | GET | No | Embedded operator page |
| `/rpc` | POST | Yes | Control RPC methods |
| `/identity` | GET | Yes | Instance identity (hostname, IPs, version) |
| `/metrics` | GET | Yes | Prometheus metrics |

**Note:** All management endpoints require Bearer token authentication.

### 5.2.2 RPC methods

The `/rpc` endpoint accepts the project’s simplified JSON-over-HTTP RPC format.

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

#### GetRouteStatus

Returns the effective selection and override state for every configured route.

**Method:** `GetRouteStatus`

#### SetRouteOverride / ClearRouteOverride

Route-local operator controls for new flows. `SetRouteOverride` takes
`{"route":"web","upstream":"web-b"}`. `ClearRouteOverride` takes
`{"route":"web"}`. A route or upstream outside the configured topology is
rejected. Adaptive routes fall back within the route when an override becomes
unavailable; static routes do not.

#### SetUpstream (deprecated)

Set the legacy global selection mode. This is supported only as a compatibility
adapter when exactly one route exists; multi-route configurations must use the
route-local methods above.

**Method:** `SetUpstream`

**Parameters:**

```json
{
  "mode": "auto" | "manual",
  "tag": "upstream-tag" // Required if mode=manual
}
```

**Result:** `null` (success indicated by `ok: true`)

**Notes:**
- `tag` is required only when `mode` is `manual`.

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

- `mode`: Current runtime mode
- `active_upstream`: Manual preference, if configured; auto mode is route-local
- `tag`: Upstream identifier
- `host`: Configured hostname or IP
- `ips`: All resolved IP addresses
- `active_ip`: Currently selected IP for forwarding
- `active`: Whether this is the manual preference
- `usable`: Whether upstream is eligible for adaptive selection
- `health_state`: Unified `unknown`, `healthy`, `stale`, or `down` state

**Note:** RTT, health, probe, and traffic metrics are available via Prometheus
`/metrics`. Active connection counts are available via the `GetActiveFlows`
RPC or Prometheus metrics.

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
  "schedule": {
    "interval": {"min": "15m0s", "max": "45m0s"},
    "upstream_gap": "5s"
  },
  "protocols": {
    "tcp": {
      "enabled": true,
      "ping_count": 5,
      "timeout": {"per_sample": "10s", "per_cycle": "30s"}
    },
    "udp": {
      "enabled": true,
      "ping_count": 5,
      "timeout": {"per_sample": "10s", "per_cycle": "30s"}
    }
  }
}
```

#### GetRuntimeConfig

Retrieve complete runtime configuration (all sections).

**Method:** `GetRuntimeConfig`

**Parameters:** Empty object `{}`

**Result:** Full configuration object with the normalized forwarding topology,
`upstreams`, `dns`, `measurement`, `health`, `control`,
`logging`, `shaping`, `geoip`, `ip_log`, and `firewall` sections.

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
    "primary:tcp": "2026-01-27T10:15:30Z",
    "primary:udp": "2026-01-27T10:16:15Z"
  }
}
```

**Field descriptions:**

- `queue_length`: Number of pending measurements in queue
- `next_scheduled`: Timestamp of next scheduled measurement (null if queue empty)
- `last_measurements`: Map key `<upstream>:<protocol>` → last successful measurement timestamp

**Example:**

```bash
curl -X POST http://localhost:8080/rpc \
  -H "Authorization: Bearer token" \
  -H "Content-Type: application/json" \
  -d '{"method": "GetScheduleStatus", "params": {}}'
```

#### GetGeoIPStatus

Report configured GeoIP database files, file metadata, and active in-memory reader availability.

**Method:** `GetGeoIPStatus`

**Parameters:** Empty object `{}` or omitted `params`

**Result:**

```json
{
  "asn_db": {
    "configured": true,
    "available": true,
    "path": "/var/lib/fbforward/GeoLite2-ASN.mmdb",
    "file_mod_time": 1712505600,
    "file_size": 8421376
  },
  "country_db": {
    "configured": true,
    "available": true,
    "path": "/var/lib/fbforward/Country-without-asn.mmdb",
    "file_mod_time": 1712505600,
    "file_size": 5242880
  },
  "refresh_interval": "24h0m0s"
}
```

**Failure:**
- HTTP `503` with `{ "ok": false, "error": "geoip manager not available" }` when GeoIP is not enabled

#### RefreshGeoIP

Trigger an immediate GeoIP download/validate/swap cycle outside the normal refresh schedule.

**Method:** `RefreshGeoIP`

**Parameters:** Empty object `{}` or omitted `params`

**Result:**

```json
{
  "asn_db": {
    "configured": true,
    "attempted": true,
    "refreshed": true,
    "previous_mod_time": 1712505600,
    "current_mod_time": 1712592000,
    "error": ""
  },
  "country_db": {
    "configured": true,
    "attempted": true,
    "refreshed": false,
    "previous_mod_time": 1712505600,
    "current_mod_time": 1712505600,
    "error": "download failed"
  }
}
```

**Failure:**
- HTTP `503` with `{ "ok": false, "error": "geoip manager not available" }` when GeoIP is not enabled
- HTTP `503` with `{ "ok": false, "error": "no geoip databases configured" }` if no DB pairs are configured

#### GetFirewallPolicy

Return the currently active persistent firewall policy.

**Method:** `GetFirewallPolicy`

**Parameters:** Empty object `{}` or omitted `params`

**Result:** The normalized policy document plus `source`, SHA-256 `hash`, `generation`, and `loaded_at` metadata.

#### GetFirewallStatus

Return firewall policy loading and reload status.

**Method:** `GetFirewallStatus`

**Parameters:** Empty object `{}` or omitted `params`

**Result:** Includes `enabled`, `policy_file`, `state`, `loaded`, `version`, `hash`, `generation`, `loaded_at`, `last_reload_at`, and any `last_error`.

#### ValidateFirewallPolicy

Validate the configured policy file or candidate YAML without changing the active policy.

**Method:** `ValidateFirewallPolicy`

**Parameters:** Optional candidate content:

```json
{ "content": "version: 1\ndefault: deny\nrules: []\n" }
```

When `content` is omitted, the configured `policy_file` is validated. Invalid YAML or policy semantics return HTTP 400.

#### ReloadFirewallPolicy

Atomically load and activate the configured policy file.

**Method:** `ReloadFirewallPolicy`

**Parameters:** Empty object `{}`

The operation does not restart listeners. A failed parse, validation, or compilation leaves the previous policy active and is recorded in the request audit log.

#### CreateOnlineRule

Create a temporary runtime rule in the IP-log SQLite database.

**Method:** `CreateOnlineRule`

**Parameters:**

```json
{
  "rule_id": "block-office",
  "action": "deny",
  "matcher": {"source_cidr": "198.51.100.0/24", "protocol": "tcp", "port": 443},
  "priority": 100,
  "ttl_seconds": 3600,
  "reason": "incident",
  "ticket_ref": "INC-123"
}
```

Actions are `deny`, `rate_limit` (with `limit_bps`), and `route_override`
(with `upstream`). At least one matcher is required. TTL is limited to 24
hours, and online allow rules are rejected. The server sets `created_by` from
the authenticated control request. Duplicate IDs return HTTP `409`.

#### ListOnlineRules

List runtime rules ordered by priority, creation time, and rule ID.

**Method:** `ListOnlineRules`

**Parameters:** `{ "include_expired": false }` (optional)

By default only enabled, unexpired rules are returned. Set `include_expired`
to `true` to include expired and disabled records.

#### DeleteOnlineRule

Hard-delete one runtime rule while retaining its delete audit event.

**Method:** `DeleteOnlineRule`

**Parameters:** `{ "rule_id": "block-office" }`

#### ExpireOnlineRule

Disable a runtime rule immediately and retain its expire audit event.

**Method:** `ExpireOnlineRule`

**Parameters:** `{ "rule_id": "block-office" }`

Online-rule APIs return HTTP `503` when `ip_log.enabled` is false. Persistent
firewall reloads do not remove or replace online rules. TCP rate limits wait
before forwarding bytes; UDP excess packets are dropped and logged.

#### GetIPLogStatus

Report the current IP log database path, file size, flow/rejection counts, and overall record time bounds.

**Method:** `GetIPLogStatus`

**Parameters:** Empty object `{}` or omitted `params`

**Result:**

```json
{
  "db_path": "/var/lib/fbforward/iplog.sqlite",
  "file_size": 10485760,
  "record_count": 15231,
  "flow_record_count": 15230,
  "rejection_record_count": 1,
  "total_record_count": 15231,
  "oldest_record_at": 1710000000,
  "newest_record_at": 1712592000,
  "retention": "720h0m0s",
  "prune_interval": "1h0m0s"
}
```

**Notes:**
- `record_count` is retained as a compatibility alias for `total_record_count`
- `oldest_record_at` and `newest_record_at` span both flow-close and rejection tables

**Failure:**
- HTTP `503` with `{ "ok": false, "error": "ip log store not available" }` when IP logging is not enabled

#### QueryIPLog

Query persisted accepted flow-close records with optional filters, pagination, and sorting.

**Method:** `QueryIPLog`

**Parameters:**

```json
{
  "start_time": 1712505600,
  "end_time": 1712592000,
  "cidr": "10.0.1.0/24",
  "asn": 13335,
  "country": "US",
  "sort_by": "recorded_at" | "bytes_up" | "bytes_down" | "bytes_total" | "duration_ms",
  "sort_order": "asc" | "desc",
  "limit": 200,
  "offset": 0
}
```

**Notes:**
- `sort_by` defaults to `recorded_at`
- `sort_order` defaults to `desc`
- `cidr` still requires `start_time` or `end_time`
- sorting is deterministic: ties are broken by `id` in the same direction as `sort_order`
- `QueryIPLog` is the flow-only compatibility API. Rejection records are excluded; use `QueryRejectionLog` or `QueryLogEvents` for rejection history.

#### QueryRejectionLog

Query persisted rejection records with optional filters, pagination, and sorting.

**Method:** `QueryRejectionLog`

**Parameters:**

```json
{
  "start_time": 1712505600,
  "end_time": 1712592000,
  "cidr": "198.51.100.0/24",
  "asn": 13335,
  "country": "US",
  "reason": "firewall_deny",
  "protocol": "tcp" | "udp",
  "port": 9000,
  "matched_rule_type": "cidr",
  "matched_rule_value": "198.51.100.0/24",
  "sort_by": "recorded_at" | "ip" | "asn" | "country" | "protocol" | "port" | "reason" | "matched_rule_type" | "matched_rule_value",
  "sort_order": "asc" | "desc",
  "limit": 200,
  "offset": 0
}
```

**Result:**

```json
{
  "total": 1,
  "records": [
    {
      "id": 7,
      "ip": "198.51.100.10",
      "asn": 64500,
      "as_org": "Example Org",
      "country": "US",
      "protocol": "tcp",
      "port": 9000,
      "reason": "firewall_deny",
      "matched_rule_type": "cidr",
      "matched_rule_value": "198.51.100.0/24",
      "recorded_at": 1712592000
    }
  ]
}
```

**Notes:**
- `limit` defaults to `200` and may not exceed `1000`
- `sort_by` defaults to `recorded_at`
- `sort_order` defaults to `desc`
- `cidr` still requires `start_time` or `end_time`
- Stable rejection reasons are `firewall_deny`, `tcp_connection_limit`, `udp_per_ip_mapping_limit`, and `udp_mapping_limit`

#### QueryLogEvents

Query merged flow-close and rejection history with optional filters, pagination, and sorting. This is the API used by the `#/iplog` page.

**Method:** `QueryLogEvents`

**Parameters:**

```json
{
  "start_time": 1712505600,
  "end_time": 1712592000,
  "cidr": "198.51.100.0/24",
  "asn": 13335,
  "country": "US",
  "protocol": "tcp" | "udp",
  "port": 9000,
  "reason": "firewall_deny",
  "matched_rule_type": "cidr",
  "matched_rule_value": "198.51.100.0/24",
  "entry_type": "all" | "flow" | "rejection",
  "sort_by": "recorded_at",
  "sort_order": "asc" | "desc",
  "limit": 200,
  "offset": 0
}
```

**Result:**

```json
{
  "total": 1,
  "records": [
    {
      "entry_type": "rejection",
      "ip": "198.51.100.10",
      "asn": 64500,
      "as_org": "Example Org",
      "country": "US",
      "protocol": "tcp",
      "port": 9000,
      "recorded_at": 1712592000,
      "upstream": null,
      "bytes_up": null,
      "bytes_down": null,
      "duration_ms": null,
      "reason": "firewall_deny",
      "matched_rule_type": "cidr",
      "matched_rule_value": "198.51.100.0/24"
    }
  ]
}
```

**Notes:**
- `entry_type` defaults to `all`
- `limit` defaults to `200` and may not exceed `1000`
- `sort_by` defaults to `recorded_at`
- `sort_order` defaults to `desc`
- `cidr` still requires `start_time` or `end_time`
- When `entry_type=all`, allowed `sort_by` values are `recorded_at`, `ip`, `asn`, `country`, `protocol`, `port`, and `entry_type`
- When `entry_type=flow`, additional `sort_by` values are `upstream`, `bytes_up`, `bytes_down`, `bytes_total`, and `duration_ms`
- When `entry_type=rejection`, additional `sort_by` values are `reason`, `matched_rule_type`, and `matched_rule_value`
- Non-applicable fields are returned as `null`
- The server merges flow and rejection rows before sorting and pagination so result ordering remains stable across pages

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

### 5.2.3 Active-flow polling

The former `/status` WebSocket endpoint was removed in phase 12.1. Active
flows are returned by the authenticated `GetActiveFlows` RPC. Clients choose
the polling interval; the minimal UI polls every two seconds while its Flow
view is visible. The response is a single snapshot and does not include queue
state, measurement history, or event replay.

**Method:** `GetActiveFlows`

**Parameters:** Empty object `{}`

**Result:**

```json
{
  "captured_at": 1710000000000,
  "tcp": [],
  "udp": []
}
```

Each entry contains the flow ID, protocol, client address, listener, route,
pinned upstream, cumulative byte/segment counters, and activity timestamps.

The control page stops polling when hidden or when another section is active.
It stores the control token only in browser `sessionStorage` and sends it as
a Bearer header.

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
| `fbforward_upstream_health_state` | gauge | `upstream,state` | One-hot upstream health state |
| `fbforward_upstream_consecutive_failures` | gauge | `upstream` | Consecutive failed probe cycles |
| `fbforward_upstream_last_success_timestamp_seconds` | gauge | `upstream` | Last successful probe time |
| `fbforward_upstream_probe_total` | counter | `upstream` | Completed TCP/UDP probes |
| `fbforward_upstream_probe_failures_total` | counter | `upstream` | Failed TCP/UDP probes |
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
| `fbforward_mode` | gauge | - | Selection mode: 0=auto, 1=manual |
| `fbforward_active_upstream` | gauge | `upstream` | Active upstream: 1=active, 0=inactive |
| `fbforward_tcp_active` | gauge | - | Active TCP connections |
| `fbforward_udp_mappings_active` | gauge | - | Active UDP mappings |
| `fbforward_measurement_queue_size` | gauge | - | Pending measurements in queue |
| `fbforward_measurement_last_run_seconds` | gauge | `upstream`, `protocol` | Seconds since last measurement |
| `fbforward_memory_alloc_bytes` | gauge | - | Allocated memory (bytes) |
| `fbforward_goroutines` | gauge | - | Runtime goroutine count |
| `fbforward_uptime_seconds` | gauge | - | Process uptime (seconds) |

**IP-log and firewall metrics:**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `fbforward_iplog_events_total` | counter | - | Total IP-log events captured (accepted flows) |
| `fbforward_iplog_events_dropped_total` | counter | - | Events dropped due to full pipeline queues |
| `fbforward_iplog_writes_total` | counter | - | Total batch writes to the SQLite database |
| `fbforward_firewall_denied_total` | counter | `rule_type`, `rule_value` | Flows denied by firewall, per matching rule |
| `fbforward_iplog_batch_size` | histogram | - | Distribution of events per write batch |

**Label descriptions:**

- `rule_type`: The type of firewall rule that matched (`cidr`, `asn`, or `country`)
- `rule_value`: The value of the matching rule (e.g., `10.0.0.0/8`, `4134`, `US`)

**Histogram buckets for `fbforward_iplog_batch_size`:** 1, 5, 10, 25, 50, 100, 250, 500.

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
# Active upstream health
fbforward_upstream_health_state{upstream="primary",state="healthy"}

# Total traffic (all upstreams)
sum(rate(fbforward_bytes_up_total[5m]))

# Active flows
fbforward_tcp_active + fbforward_udp_mappings_active

# Measurement queue depth
fbforward_measurement_queue_size
```

**Dashboard example:**

```promql
# Panel: Active upstream indicator
fbforward_active_upstream

# Panel: Traffic rates (upload)
sum by (upstream) (rate(fbforward_bytes_up_total[1m]) * 8)

# Panel: RTT comparison
fbforward_upstream_rtt_ms

# Panel: Probe failures
rate(fbforward_upstream_probe_failures_total[5m])
```

#### Metric interpretation

**Health states:** `healthy`, `stale`, `unknown`, and `down` are emitted as a
one-hot metric. `down` is excluded from adaptive route selection; static routes
remain fixed and only honor dial cooldown.

**Rate metrics:** `bytes_up_per_second` and `bytes_down_per_second` use 1-second window. Protocol-specific rates (`tcp_up_rate_bps`, etc.) also use 1-second window.

---

## Cross-reference

| API | User guide | Algorithm reference |
|-----|------------|---------------------|
| bwprobe/pkg | [3.2](user-guide-bwprobe.md) | [6.2](algorithm-specifications.md#62-bandwidth-measurement-algorithm-bwprobe) |
| Control RPC | [3.1.3](user-guide-fbforward.md#313-operation) | - |
| Active-flow polling | [3.1.3](user-guide-fbforward.md#313-operation) | - |
| Prometheus | [3.1.3](user-guide-fbforward.md#313-operation) | - |
