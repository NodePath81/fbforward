# Configuration reference

This reference documents all configuration options for fbforward. For operational guidance, see [Section 3.1](user-guide-fbforward.md).

---

## 4.1 Configuration file format

### YAML structure

fbforward uses YAML for configuration. The top-level structure contains 11 main sections:

```yaml
hostname: fbforward-01           # Optional identifier
forwarding: {...}                 # Listeners and flow management
upstreams: [...]                  # Upstream list
dns: {...}                        # DNS resolution
reachability: {...}               # ICMP probing
measurement: {...}                # fbmeasure probe settings
scoring: {...}                    # Quality scoring algorithm
switching: {...}                  # Upstream switching behavior
control: {...}                    # Control plane (HTTP API, web UI)
coordination: {...}               # Optional fbcoord participation
shaping: {...}                    # Linux tc traffic shaping
```

### Duration format

Duration fields accept time.Duration strings or numeric values (interpreted as seconds):

| Format | Example | Meaning |
|--------|---------|---------|
| String | `"30s"` | 30 seconds |
| String | `"5m"` | 5 minutes |
| String | `"2h"` | 2 hours |
| Number | `60` | 60 seconds |
| Number | `1.5` | 1.5 seconds |

Valid units: `ms` (milliseconds), `s` (seconds), `m` (minutes), `h` (hours).

### Bandwidth format

Bandwidth fields accept SI unit strings (bits per second):

| Example | Meaning |
|---------|---------|
| `"10m"` | 10 Mbps (10,000,000 bps) |
| `"1g"` | 1 Gbps (1,000,000,000 bps) |
| `"500k"` | 500 Kbps (500,000 bps) |
| `"100m"` | 100 Mbps |

Valid suffixes: `k` (kilo), `m` (mega), `g` (giga). Case-insensitive.

Non-zero bare numbers are rejected for bandwidth fields. Only `0` or `0.0` may be unitless.

### Byte size format

Byte size fields accept SI unit strings:

| Example | Meaning |
|---------|---------|
| `"1200"` | 1200 bytes |
| `"500kb"` | 500 kilobytes (500,000 bytes) |
| `"5mb"` | 5 megabytes (5,000,000 bytes) |

Valid suffixes: `b` (bytes), `kb` (kilobytes), `mb` (megabytes), `gb` (gigabytes). Case-insensitive.

### Default value handling

Omitted optional fields use built-in defaults. Explicitly set fields override defaults, even when set to zero or empty string (where semantically valid).

Default values are documented in each section below.

### Loading and validation

fbforward loads configuration on startup and validates all fields:

```bash
./fbforward --config /etc/fbforward/config.yaml
```

Validation errors stop startup and report the invalid field path. Use the `check` subcommand to validate without starting:

```bash
./fbforward check /etc/fbforward/config.yaml
```

Removed measurement/scoring keys from older configs are rejected explicitly. The error lists each removed key path found in the input file.

### Environment variables

fbforward does not support environment variable overrides. All configuration must be in the YAML file.

---

## 4.2 forwarding section

The `forwarding` section configures listeners and flow management.

### listeners

List of listener definitions. Each listener binds to an address and accepts client connections. fbforward forwards traffic to the selected [primary upstream](glossary.md#primary-upstream).

**Type:** Array of objects

**Required:** Yes (at least one listener)

**Maximum:** 45 listeners

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bind_addr` | string | `0.0.0.0` | Address to bind |
| `bind_port` | int | *required* | Port to bind (1-65535) |
| `protocol` | string | `tcp` | Protocol: `tcp` or `udp` |
| `shaping` | object | *none* | Per-listener shaping (requires `shaping.enabled`) |

**Example:**

```yaml
forwarding:
  listeners:
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: tcp
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: udp
```

**Validation:**
- `bind_port` must be in range 1-65535
- `protocol` must be `tcp` or `udp` (case-insensitive, normalized to lowercase)
- Duplicate `(bind_addr, bind_port, protocol)` tuples are rejected
- If `shaping` is set, `shaping.enabled` must be `true`

### limits

Connection and mapping limits.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_tcp_connections` | int | `50` | Maximum concurrent TCP connections |
| `max_udp_mappings` | int | `500` | Maximum concurrent UDP 5-tuple mappings |

**Example:**

```yaml
forwarding:
  limits:
    max_tcp_connections: 100
    max_udp_mappings: 1000
```

When limits are reached, new flows are rejected until existing flows close or expire.

**Validation:**
- Both fields must be greater than zero

### idle_timeout

Idle timeout for TCP connections and UDP mappings.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tcp` | duration | `60s` | TCP idle timeout |
| `udp` | duration | `30s` | UDP mapping expiry |

**Example:**

```yaml
forwarding:
  idle_timeout:
    tcp: 2m
    udp: 1m
```

TCP idle timeout measures time since last data transfer in either direction. UDP idle timeout measures time since last packet from client. See [Section 3.1.1](user-guide-fbforward.md#311-overview) for lifecycle details.

**Validation:**
- Both fields must be greater than zero

### Per-listener shaping

Optional bandwidth limits per listener. Requires `shaping.enabled: true`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `upload_limit` | string | *none* | Upload bandwidth cap (client → upstream) |
| `download_limit` | string | *none* | Download bandwidth cap (upstream → client) |

**Example:**

```yaml
forwarding:
  listeners:
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: tcp
      shaping:
        upload_limit: 50m
        download_limit: 200m
```

At least one of `upload_limit` or `download_limit` must be specified. See [Section 4.11](configuration-reference.md#411-shaping-section) for shaping architecture.

---

## 4.3 upstreams section

The `upstreams` section defines the list of available forwarding destinations. Each upstream has a [tag](glossary.md#upstream), destination address, measurement endpoint, and optional tuning parameters.

**Type:** Array of objects

**Required:** Yes (at least one upstream)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tag` | string | *required* | Unique identifier for this upstream |
| `destination` | object | *required* | Forwarding destination (see below) |
| `measurement` | object | *optional* | Measurement endpoint (see below) |
| `priority` | float64 | `0` | Static priority adjustment (≥ 0) |
| `bias` | float64 | `0` | Additive bias adjustment ([-1, 1]) |
| `shaping` | object | *none* | Per-upstream shaping (requires `shaping.enabled`) |

### destination

Forwarding destination. Traffic is forwarded to `host` using the same port as the listener that accepted the connection.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `host` | string | *required* | Hostname or IP address |

**Port behavior:** There is no per-upstream port configuration. The destination port is always the same as the listener's `bind_port`. For example, if a client connects to `fbforward:9000`, fbforward forwards to `upstream:9000`.

**Example:**

```yaml
upstreams:
  - tag: primary
    destination:
      host: 203.0.113.10
  - tag: backup
    destination:
      host: upstream.example.com
```

The `host` field accepts:
- IPv4 addresses: `203.0.113.10`
- IPv6 addresses: `2001:db8::1`
- Hostnames: `upstream.example.com` (resolved via DNS)

**Validation:**
- `host` must not be empty after trimming whitespace

### measurement

Measurement endpoint for fbmeasure targeted probes. Defaults to
`destination.host` on port 9876 (fbmeasure default).

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `host` | string | `destination.host` | Measurement endpoint host |
| `port` | int | `9876` | fbmeasure port |

**Example:**

```yaml
upstreams:
  - tag: primary
    destination:
      host: 203.0.113.10
    measurement:
      host: 203.0.113.10
      port: 9876
```

Typically `measurement.host` matches `destination.host`. Separate measurement hosts are useful when:
- Upstream uses NAT and measurement server has different address
- Measurement server runs on separate host for load distribution

**Validation:**
- `port` must be in range 1-65535

**Deployment requirement:** fbmeasure must be running on `measurement.host:port`. Without fbmeasure, fbforward operates in degraded mode using ICMP-only reachability. See [Section 3.3](user-guide-fbmeasure.md).

### priority

Static priority bonus used by fast-start preselection. It is not applied in steady-state scoring.

**Type:** float64

**Default:** `0`

**Range:** ≥ 0

**Example:**

```yaml
upstreams:
  - tag: primary
    destination:
      host: 203.0.113.10
    priority: 100  # Strongly preferred
  - tag: backup
    destination:
      host: 203.0.113.20
    priority: 50   # Lower priority
```

Fast-start score uses `100 / (1 + RTT / 50) + priority`. Steady-state scoring excludes priority. See [Section 6.1.2](algorithm-specifications.md#612-formal-description).

### bias

Additive bias adjustment. Positive bias increases score, negative bias decreases score.

**Type:** float64

**Default:** `0`

**Range:** [-1, 1]

**Example:**

```yaml
upstreams:
  - tag: primary
    destination:
      host: 203.0.113.10
    bias: 0.1   # Slight boost
  - tag: backup
    destination:
      host: 203.0.113.20
    bias: -0.05 # Slight penalty
```

Bias is transformed via exponential function and applied to quality score. See `scoring.bias_transform.kappa` for bias scaling. See [Section 6.1.2](algorithm-specifications.md#612-formal-description) for details.

### Per-upstream shaping

Optional bandwidth limits per upstream. Requires `shaping.enabled: true`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `upload_limit` | string | *none* | Upload bandwidth cap (fbforward → upstream) |
| `download_limit` | string | *none* | Download bandwidth cap (upstream → fbforward) |

**Example:**

```yaml
upstreams:
  - tag: primary
    destination:
      host: 203.0.113.10
    shaping:
      upload_limit: 100m
      download_limit: 500m
```

At least one of `upload_limit` or `download_limit` must be specified. Shaping applies to all traffic to/from the upstream's resolved IP addresses. See [Section 4.11](configuration-reference.md#411-shaping-section) for shaping architecture.

**Validation:**
- `tag` must be unique across all upstreams
- `tag` must not be empty after trimming whitespace
- `destination.host` must not be empty
- `priority` must be ≥ 0
- `bias` must be in range [-1, 1]
- If `shaping` is set, `shaping.enabled` must be `true`

---

## 4.4 dns section

The `dns` section configures DNS resolution for upstream hostnames.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `servers` | array of strings | *system DNS* | Custom DNS server list |
| `strategy` | string | *both A and AAAA* | Address selection strategy |

### servers

Custom DNS servers. Each entry is `ip` or `ip:port` (default port 53).

**Type:** Array of strings

**Default:** Empty (uses system DNS from `/etc/resolv.conf`)

**Example:**

```yaml
dns:
  servers:
    - 1.1.1.1
    - 8.8.8.8:53
    - 2606:4700:4700::1111
```

fbforward uses these servers for all upstream hostname resolution. System DNS is not consulted when `servers` is non-empty.

### strategy

Address selection strategy when upstream hostname resolves to multiple addresses.

**Type:** string

**Default:** *both A and AAAA* (IPv4 and IPv6)

**Options:**
- `ipv4_only`: Use only A records (IPv4 addresses)
- `prefer_ipv6`: Prefer AAAA records (IPv6), fall back to A if no AAAA

**Example:**

```yaml
dns:
  strategy: ipv4_only
```

When `strategy` is omitted, both A and AAAA results are used. fbforward selects one address from the resolved set for forwarding.

**Validation:**
- `strategy` must be `ipv4_only` or `prefer_ipv6` (case-insensitive)

---

## 4.5 reachability section

The `reachability` section configures ICMP echo (ping) probing for upstream reachability. ICMP probes run continuously and determine whether upstreams are usable. ICMP probes do not affect quality scoring.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `probe_interval` | duration | `1s` | Time between ICMP probes |
| `window_size` | int | `5` | Number of recent probes tracked |
| `startup_delay` | duration | `probe_interval × window_size` | Delay before starting probes |

### probe_interval

Time between ICMP echo requests to each upstream.

**Type:** duration

**Default:** `1s`

**Example:**

```yaml
reachability:
  probe_interval: 2s
```

fbforward sends one ICMP echo request to each upstream every `probe_interval`. Shorter intervals provide faster failure detection but increase network overhead.

**Validation:**
- Must be greater than or equal to `100ms`

### window_size

Number of recent probe results tracked per upstream. Reachability is computed as success rate over the window.

**Type:** int

**Default:** `5`

**Example:**

```yaml
reachability:
  window_size: 10
```

An upstream is considered unreachable if all probes in the window fail (100% loss). Larger windows smooth out transient packet loss but slow down failure detection.

**Validation:**
- Must be greater than zero

### startup_delay

Delay before starting ICMP probes. Allows upstreams time to initialize.

**Type:** duration

**Default:** `probe_interval × window_size`

**Example:**

```yaml
reachability:
  probe_interval: 1s
  window_size: 5
  startup_delay: 10s  # Override computed default of 5s
```

Default value ensures at least one full window of probes before reachability affects upstream selection.

**Validation:**
- Must be greater than or equal to zero

**Requirement:** fbforward requires `CAP_NET_RAW` capability for ICMP sockets. See [Section 2.2](getting-started.md#22-installation).

---

## 4.6 measurement section

The `measurement` section configures fbmeasure-based quality measurements.
These measurements feed scoring (RTT, jitter, retransmit/loss).

### Startup and staleness

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `startup_delay` | duration | `10s` | Delay before first measurement loop scheduling |
| `stale_threshold` | duration | `60m` | Age after which protocol measurements are treated as stale |
| `fallback_to_icmp_on_stale` | bool | `true` | Controls stale-warning logging only |

**Example:**

```yaml
measurement:
  startup_delay: 30s
  stale_threshold: 2h
  fallback_to_icmp_on_stale: false
```

When measurements are stale, scoring substitutes degraded reference values for the stale protocol (RTT/jitter/retransmit/loss). ICMP remains reachability-only and does not contribute numeric quality scores.

**Validation:**
- `startup_delay` must be ≥ 0
- `stale_threshold` must be > 0

### schedule

Measurement scheduling controls when tests run.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `interval` | object | *see below* | Randomized measurement interval range |
| `upstream_gap` | duration | `5s` | Gap between measurement jobs |

#### interval

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `min` | duration | `15m` | Minimum interval between scheduled measurements |
| `max` | duration | `45m` | Maximum interval between scheduled measurements |

**Example:**

```yaml
measurement:
  schedule:
    interval:
      min: 10m
      max: 30m
```

fbforward schedules measurements randomly between `min` and `max` to avoid synchronized bursts across instances.

**Validation:**
- `min` must be > 0
- `max` must be > 0
- `max` must be ≥ `min`

#### upstream_gap

Time gap between measurement jobs.

**Type:** duration

**Default:** `5s`

**Example:**

```yaml
measurement:
  schedule:
    upstream_gap: 10s
```

**Validation:**
- Must be ≥ 0

### fast_start

Fast-start uses TCP connect RTT probes to `upstreams[].measurement.host:port` for startup preselection, then transitions to normal scoring after warmup.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable fast-start preselection |
| `timeout` | duration | `500ms` | Per-probe TCP connect timeout |
| `warmup_duration` | duration | `15s` | Warmup duration with relaxed switching |

**Example:**

```yaml
measurement:
  fast_start:
    enabled: true
    timeout: 1s
    warmup_duration: 30s
```

When `enabled` is `false`, startup skips preselection and proceeds directly with normal runtime startup (listeners still start; no blocking on first full measurement).

**Validation:**
- `timeout` must be > 0
- `warmup_duration` must be ≥ 0

### security

Transport security settings for fbforward's TCP connections to fbmeasure. This
applies to the TCP control channel and TCP retransmission data connection. UDP
probe traffic remains datagram-based and is authenticated per test by the
fbmeasure protocol.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mode` | string | `off` | Security mode: `off`, `tls`, or `mtls` |
| `ca_file` | string | empty | Optional CA bundle used to verify the fbmeasure server certificate |
| `server_name` | string | empty | Optional TLS server name override for certificate verification |
| `client_cert_file` | string | empty | Client certificate for mutual TLS |
| `client_key_file` | string | empty | Client private key for mutual TLS |

**Example:**

```yaml
measurement:
  security:
    mode: tls
    ca_file: /etc/fbforward/measurement-ca.pem
    server_name: fbmeasure.internal.example.com
```

If `server_name` is unset and `upstreams[].measurement.host` is a hostname,
fbforward uses that hostname for TLS verification automatically. When the
measurement host is configured as an IP address, set `server_name` explicitly if
the certificate does not contain the IP as a SAN.

**Validation:**
- `mode` must be `off`, `tls`, or `mtls`
- `client_cert_file` and `client_key_file` must be set together
- `mode: mtls` requires both `client_cert_file` and `client_key_file`

### protocols

Protocol-specific measurement parameters for TCP and UDP probe cycles.

#### tcp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable TCP measurements |
| `ping_count` | int | `5` | Number of TCP RTT pings per cycle |
| `retransmit_bytes` | string | `"500kb"` | Payload sent during the TCP retransmission test |
| `timeout` | object | *see below* | Timeout configuration |

**timeout:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `per_sample` | duration | `10s` | Timeout for each probe stage |
| `per_cycle` | duration | `30s` | Timeout for the entire TCP cycle |

**Example:**

```yaml
measurement:
  protocols:
    tcp:
      enabled: true
      ping_count: 5
      retransmit_bytes: 1mb
      timeout:
        per_sample: 15s
        per_cycle: 60s
```

#### udp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable UDP measurements |
| `ping_count` | int | `5` | Number of UDP RTT pings per cycle |
| `loss_packets` | int | `64` | Number of UDP datagrams sent for the loss test |
| `packet_size` | string | `"1200"` | UDP datagram size in bytes |
| `timeout` | object | *see tcp* | Timeout configuration |

**Example:**

```yaml
measurement:
  protocols:
    udp:
      enabled: true
      ping_count: 5
      loss_packets: 64
      packet_size: 1200
      timeout:
        per_sample: 10s
        per_cycle: 30s
```

**Validation:**
- At least one of TCP or UDP must be enabled
- `ping_count` must be > 0
- `retransmit_bytes` (TCP) must be > 0
- `loss_packets` (UDP) must be > 0
- `packet_size` (UDP) must be > 0
- `timeout.per_sample` must be > 0
- `timeout.per_cycle` must be > 0

The legacy bwprobe-oriented fields `alternate`, `chunk_size`, `sample_size`,
and `sample_count` are rejected during configuration load.

---

## 4.7 scoring section

The `scoring` section configures upstream quality scoring. Steady-state scoring uses RTT, jitter, and protocol-specific quality-loss signals (TCP retransmit rate, UDP loss rate), plus protocol blend and bias transform.

### smoothing

Exponential moving average (EMA) smoothing for metric updates.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `alpha` | float64 | `0.2` | EMA smoothing factor |

**Validation:**
- Must be in range (0, 1]

### reference

Reference values for score normalization.

#### tcp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `latency.rtt` | float64 | `50` | Reference RTT (milliseconds) |
| `latency.jitter` | float64 | `10` | Reference jitter (milliseconds) |
| `retransmit_rate` | float64 | `0.01` | Reference TCP retransmit rate |

#### udp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `latency.rtt` | float64 | `50` | Reference RTT (milliseconds) |
| `latency.jitter` | float64 | `10` | Reference jitter (milliseconds) |
| `loss_rate` | float64 | `0.01` | Reference UDP packet loss rate |

**Validation:**
- Latency values must be > 0
- `retransmit_rate` (TCP) must be in range (0, 1]
- `loss_rate` (UDP) must be in range (0, 1]

### weights

Weights are normalized automatically.

#### tcp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rtt` | float64 | `0.25` | RTT weight |
| `jitter` | float64 | `0.10` | Jitter weight |
| `retransmit_rate` | float64 | `0.25` | Retransmit rate weight |

#### udp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rtt` | float64 | `0.15` | RTT weight |
| `jitter` | float64 | `0.30` | Jitter weight |
| `loss_rate` | float64 | `0.15` | Loss rate weight |

#### protocol_blend

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tcp_weight` | float64 | `0.5` | TCP score contribution |
| `udp_weight` | float64 | `0.5` | UDP score contribution |

**Validation:**
- All weights must be ≥ 0
- Each weight group must have sum > 0 (then normalized)

### bias_transform

Bias transformation scales `upstreams[].bias`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `kappa` | float64 | `0.693147` | Exponential scaling constant |

**Validation:**
- Must be > 0

### Removed measurement/scoring keys

The following keys are no longer supported and fail config loading with explicit path errors:

- `measurement.schedule.headroom.*`
- `measurement.protocols.tcp.target_bandwidth.*`
- `measurement.protocols.udp.target_bandwidth.*`
- `scoring.reference.tcp.bandwidth.*`
- `scoring.reference.udp.bandwidth.*`
- `scoring.weights.tcp.bandwidth_upload`
- `scoring.weights.tcp.bandwidth_download`
- `scoring.weights.udp.bandwidth_upload`
- `scoring.weights.udp.bandwidth_download`
- `scoring.utilization_penalty.*`

---

## 4.8 switching section

The `switching` section configures upstream switching behavior in auto mode and fast failover triggers.

### auto

Auto mode switching parameters. Applies when upstream selection is automatic (not manually pinned).

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `confirm_duration` | duration | `15s` | Time new leader must sustain advantage |
| `score_delta_threshold` | float64 | `5.0` | Minimum score advantage to switch |
| `min_hold_time` | duration | `30s` | Minimum time on primary before switching |

**Example:**

```yaml
switching:
  auto:
    confirm_duration: 30s
    score_delta_threshold: 10.0
    min_hold_time: 1m
```

To switch from current primary to a new upstream:
1. New upstream must have score advantage ≥ `score_delta_threshold`
2. Advantage must be sustained for `confirm_duration`
3. Current primary must have been active for ≥ `min_hold_time`

This prevents flapping when upstreams have similar scores.

**Validation:**
- `confirm_duration` must be ≥ 0
- `min_hold_time` must be ≥ 0

### failover

Fast failover thresholds trigger immediate switching when current primary degrades.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `loss_rate_threshold` | float64 | `0.2` | UDP loss rate threshold (20%) |
| `retransmit_rate_threshold` | float64 | `0.2` | TCP retransmit rate threshold (20%) |

**Example:**

```yaml
switching:
  failover:
    loss_rate_threshold: 0.3
    retransmit_rate_threshold: 0.3
```

If recent measurements show loss or retransmit rates exceeding thresholds, fbforward immediately switches to next-best upstream without waiting for `confirm_duration`.

**Validation:**
- Both thresholds must be in range (0, 1]

### close_flows_on_failover

Close existing flows when fast failover occurs.

**Type:** bool

**Default:** `false`

**Example:**

```yaml
switching:
  close_flows_on_failover: true
```

When `false` (default), existing flows remain pinned to their current upstream even during failover. Only new flows go to the new primary.

When `true`, fbforward closes all flows to the failed upstream during fast failover. TCP connections receive RST, UDP mappings expire immediately. This forces clients to reconnect to the new primary.

---

## 4.9 control section

The `control` section configures the HTTP control plane: RPC API, web UI, WebSocket status stream, and Prometheus metrics.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bind_addr` | string | `127.0.0.1` | HTTP server bind address |
| `bind_port` | int | `8080` | HTTP server port |
| `auth_token` | string | *required* | Bearer token for authentication |
| `webui` | object | *see below* | Web UI configuration |
| `metrics` | object | *see below* | Prometheus metrics configuration |

**Example:**

```yaml
control:
  bind_addr: 0.0.0.0
  bind_port: 8080
  auth_token: "replace-with-a-long-random-token"
  webui:
    enabled: true
  metrics:
    enabled: true
```

**Validation:**
- `auth_token` must not be empty
- `auth_token` must not use the placeholder value `change-me`
- `auth_token` must be at least 16 characters long
- `bind_port` must be in range 1-65535

### Endpoints

The control plane exposes the following HTTP endpoints:

| Path | Method | Description |
|------|--------|-------------|
| `/` | GET | Web UI (embedded SPA) |
| `/auth` | GET | Token authentication page |
| `/rpc` | POST | JSON-RPC methods |
| `/metrics` | GET | Prometheus metrics |
| `/status` | GET | WebSocket status stream |
| `/identity` | GET | Instance identity document |

All endpoints except `/` and `/auth` require Bearer token authentication:

```bash
curl -H "Authorization: Bearer replace-with-a-long-random-token" http://localhost:8080/metrics
```

WebSocket authentication uses subprotocol for browser compatibility:

```javascript
const token = 'replace-with-a-long-random-token';
const encoded = btoa(token)
  .replace(/\+/g, '-')
  .replace(/\//g, '_')
  .replace(/=+$/g, '');

new WebSocket('ws://localhost:8080/status', ['fbforward', `fbforward-token.${encoded}`]);
```

Browser WebSocket requests must be same-origin. fbforward rejects upgrades whose
`Origin` host does not match the request host.

See [Section 5.2](api-reference.md#52-control-plane-api) for API details.

When coordination is configured, the local Web UI can also switch the runtime
into `coordination` mode and display live coordination status using the same
local control plane.

### webui

Web UI enable/disable.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable web UI |

**Example:**

```yaml
control:
  webui:
    enabled: false
```

When `false`, `GET /` returns 404. RPC and metrics remain accessible.

### metrics

Prometheus metrics enable/disable.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable Prometheus metrics |

**Example:**

```yaml
control:
  metrics:
    enabled: false
```

When `false`, `GET /metrics` returns 404.

---

## 4.10 coordination section

The `coordination` section enables optional participation in an external
`fbcoord` service. When configured, operators can switch the runtime into
`coordination` mode using the existing local control plane.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `endpoint` | string | *required when section is used* | Base URL for the `fbcoord` service |
| `pool` | string | *required when section is used* | Coordination pool name |
| `node_id` | string | *required when section is used* | Stable node identifier submitted to `fbcoord` |
| `token` | string | *required when section is used* | Bearer token used to authenticate with `fbcoord` |
| `heartbeat_interval` | duration | `10s` | Heartbeat and full preference submission interval |

**Example:**

```yaml
coordination:
  endpoint: https://fbcoord.example.workers.dev
  pool: default
  node_id: fbforward-01
  token: "replace-with-a-separate-long-random-token"
  heartbeat_interval: 10s
```

**Behavior:**
- The section is optional.
- If any coordination field is set, all of `endpoint`, `pool`, `node_id`, and `token` must be set.
- fbforward connects to `fbcoord` only while runtime mode is `coordination`, using the node participation endpoint `/ws/node`.
- The local node submits its sorted upstream preference list in best-first order.
- If `fbcoord` returns no upstream, disconnects, or returns a locally unusable upstream, fbforward stays in coordination mode and falls back to local auto-selection behavior.

**Validation:**
- `heartbeat_interval` must be > 0 when the section is used
- `token` must not be empty
- `token` must not use the placeholder value `change-me`
- `token` must be at least 16 characters long

---

## 4.11 shaping section

The `shaping` section configures Linux tc (traffic control) bandwidth shaping via netlink. Shaping is optional and requires `CAP_NET_ADMIN` capability.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable traffic shaping |
| `interface` | string | *required if enabled* | Network interface to shape |
| `ifb_device` | string | `ifb0` | IFB device for ingress shaping |
| `aggregate_limit` | string | `1g` | Aggregate bandwidth cap (bits/sec) |

**Example:**

```yaml
shaping:
  enabled: true
  interface: eth0
  ifb_device: ifb0
  aggregate_limit: 1g
```

**Validation:**
- When `enabled` is `true`:
  - `interface` must not be empty
  - `ifb_device` must not be empty
  - `aggregate_limit` must be valid bandwidth string if non-empty

### Architecture

fbforward uses Linux tc with HTB (Hierarchical Token Bucket) qdisc:

- **Egress (upload):** HTB qdisc on `interface` shapes outbound traffic
- **Ingress (download):** Traffic is redirected to `ifb_device` and shaped there

Shaping can be configured at three levels:

1. **Global aggregate:** `shaping.aggregate_limit` caps total bandwidth in each direction
2. **Per-listener:** `forwarding.listeners[].shaping` caps bandwidth for a listener
3. **Per-upstream:** `upstreams[].shaping` caps bandwidth to/from an upstream IP

### Per-listener vs per-upstream shaping

| Scope | Upload direction | Download direction | Use case |
|-------|------------------|--------------------| ---------|
| Listener | Client → fbforward → upstream | Upstream → fbforward → client | Limit bandwidth for specific client-facing port |
| Upstream | fbforward → specific upstream | Specific upstream → fbforward | Limit bandwidth per upstream link |

Listener shaping limits aggregate traffic on a listener port. Upstream shaping limits traffic to/from specific upstream hosts.

Both can be used simultaneously. Packets are subject to all applicable limits.

### IFB device setup

IFB (Intermediate Functional Block) device is required for ingress shaping. fbforward creates and configures `ifb_device` automatically at startup.

If manual setup is needed:

```bash
sudo modprobe ifb
sudo ip link add ifb0 type ifb
sudo ip link set ifb0 up
```

Verify:

```bash
ip link show ifb0
```

### Capability requirement

Shaping requires `CAP_NET_ADMIN` capability. Set via systemd `AmbientCapabilities` or `setcap`:

```bash
sudo setcap cap_net_admin+ep ./fbforward
```

See [Section 2.2](getting-started.md#22-installation).

### Disabling shaping

To disable shaping, set `enabled: false` and remove per-listener and per-upstream shaping blocks:

```yaml
shaping:
  enabled: false
```

fbforward will not create tc qdiscs or require `CAP_NET_ADMIN`.

---

## Cross-reference

| Configuration section | Algorithm reference | User guide |
|-----------------------|---------------------|------------|
| `forwarding` | - | [3.1.1](user-guide-fbforward.md#311-overview) |
| `upstreams` | [6.1.1](algorithm-specifications.md#611-overview) | [3.1.2](user-guide-fbforward.md#312-configuration) |
| `dns` | - | [3.1.2](user-guide-fbforward.md#312-configuration) |
| `reachability` | - | [3.1.2](user-guide-fbforward.md#312-configuration) |
| `measurement` | - | [3.1.2](user-guide-fbforward.md#312-configuration) |
| `scoring` | [6.1.2](algorithm-specifications.md#612-formal-description) | [3.1.2](user-guide-fbforward.md#312-configuration) |
| `switching` | [6.1.4](algorithm-specifications.md#614-edge-cases) | [3.1.1](user-guide-fbforward.md#311-overview) |
| `control` | - | [3.1.3](user-guide-fbforward.md#313-operation), [5.2](api-reference.md#52-control-plane-api) |
| `coordination` | - | [3.1.1](user-guide-fbforward.md#311-overview), [5.2](api-reference.md#52-control-plane-api) |
| `shaping` | - | [3.1.2](user-guide-fbforward.md#312-configuration) |
