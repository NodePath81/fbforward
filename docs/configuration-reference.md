# Configuration reference

This reference documents all configuration options for fbforward. For operational guidance, see [Section 3.1](user-guide-fbforward.md).

---

## 4.1 Configuration file format

### YAML structure

fbforward uses YAML for configuration. The top-level structure contains 10 main sections:

```yaml
hostname: fbforward-01           # Optional identifier
forwarding: {...}                 # Listeners and flow management
upstreams: [...]                  # Upstream list
dns: {...}                        # DNS resolution
reachability: {...}               # ICMP probing
measurement: {...}                # bwprobe measurement settings
scoring: {...}                    # Quality scoring algorithm
switching: {...}                  # Upstream switching behavior
control: {...}                    # Control plane (HTTP API, web UI)
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

At least one of `upload_limit` or `download_limit` must be specified. See [Section 4.10](configuration-reference.md#410-shaping-section) for shaping architecture.

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

Measurement endpoint for bwprobe tests. Defaults to `destination.host` on port 9876 (fbmeasure default).

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

Static priority adjustment. Higher priority upstreams are preferred.

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

Priority is multiplied by the computed quality score before upstream selection. See [Section 6.1.2](algorithm-specifications.md#612-formal-description) for scoring algorithm.

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

At least one of `upload_limit` or `download_limit` must be specified. Shaping applies to all traffic to/from the upstream's resolved IP addresses. See [Section 4.10](configuration-reference.md#410-shaping-section).

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
- Must be greater than zero

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

The `measurement` section configures bwprobe-based link quality measurement. Measurements drive upstream quality scoring.

### Startup and staleness

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `startup_delay` | duration | `10s` | Delay before first measurement |
| `stale_threshold` | duration | `60m` | Age at which measurement is stale |
| `fallback_to_icmp_on_stale` | bool | `true` | Log warning when measurements become stale |

**Example:**

```yaml
measurement:
  startup_delay: 30s
  stale_threshold: 2h
  fallback_to_icmp_on_stale: false
```

`startup_delay` allows listeners and probes to stabilize before measurements begin.

`stale_threshold` defines when a measurement becomes stale (age exceeds this duration since last successful test). Stale measurements trigger penalty scoring.

When measurements are stale, the scoring engine applies reference-value penalties: bandwidth sub-scores are computed using configured reference values rather than measured values, effectively degrading the upstream's quality score. The stale upstream remains selectable but will likely be outscored by upstreams with fresh measurements.

**Note:** ICMP probes monitor reachability continuously but do not contribute to quality scoring. The `fallback_to_icmp_on_stale` flag currently only controls logging behavior (logs "ICMP fallback" warning when stale threshold is exceeded).

**Validation:**
- `startup_delay` must be ≥ 0
- `stale_threshold` must be > 0

### schedule

Measurement scheduling controls when tests run and how to avoid saturating the link.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `interval` | object | *see below* | Time between measurements |
| `upstream_gap` | duration | `5s` | Gap between upstream tests in a cycle |
| `headroom` | object | *see below* | Link utilization gating |

#### interval

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `min` | duration | `15m` | Minimum interval between measurements |
| `max` | duration | `45m` | Maximum interval between measurements |

**Example:**

```yaml
measurement:
  schedule:
    interval:
      min: 10m
      max: 30m
```

fbforward schedules measurements randomly between `min` and `max` to avoid synchronized bursts across multiple instances. Each upstream is measured once per cycle.

**Validation:**
- `min` must be > 0
- `max` must be > 0
- `max` must be ≥ `min`

#### upstream_gap

Time gap between consecutive upstream measurements within a cycle.

**Type:** duration

**Default:** `5s`

**Example:**

```yaml
measurement:
  schedule:
    upstream_gap: 10s
```

Prevents simultaneous measurements to multiple upstreams, which could saturate the link.

**Validation:**
- Must be ≥ 0

#### headroom

Link utilization gating prevents measurements when link is heavily loaded.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_link_utilization` | float64 | `0.7` | Maximum utilization before skipping measurement |
| `required_free_bandwidth` | string | `"0"` | Minimum free bandwidth required (bits/sec) |

**Example:**

```yaml
measurement:
  schedule:
    headroom:
      max_link_utilization: 0.8
      required_free_bandwidth: 10m
```

Before running a measurement, fbforward checks current link utilization (computed from recent traffic samples). If utilization exceeds `max_link_utilization` or free bandwidth is below `required_free_bandwidth`, the measurement is skipped.

**Validation:**
- `max_link_utilization` must be in range (0, 1]
- `required_free_bandwidth` must be valid bandwidth string

### fast_start

Fast-start mode uses lightweight ICMP RTT probes for immediate upstream selection at startup, then transitions to full bwprobe measurements after warmup period.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable fast-start mode |
| `timeout` | duration | `500ms` | RTT probe timeout |
| `warmup_duration` | duration | `15s` | Time before transition to bwprobe |

**Example:**

```yaml
measurement:
  fast_start:
    enabled: true
    timeout: 1s
    warmup_duration: 30s
```

When `enabled` is `true`, fbforward:
1. Sends TCP SYN probes to measure RTT
2. Selects primary upstream based on RTT
3. Accepts connections immediately
4. Transitions to bwprobe scoring after `warmup_duration`

When `enabled` is `false`, fbforward waits for first full bwprobe measurement before accepting connections.

**Validation:**
- `timeout` must be > 0
- `warmup_duration` must be ≥ 0

### protocols

Protocol-specific measurement parameters for TCP and UDP tests.

#### tcp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable TCP measurements |
| `alternate` | bool | `true` | Alternate upload/download direction |
| `target_bandwidth` | object | *see below* | Target bandwidth for tests |
| `chunk_size` | string | `"1200"` | Chunk size including headers (bytes) |
| `sample_size` | string | `"500kb"` | Payload bytes per sample |
| `sample_count` | int | `1` | Number of samples per test |
| `timeout` | object | *see below* | Timeout configuration |

**target_bandwidth:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `upload` | string | `"10m"` | Upload test bandwidth cap |
| `download` | string | `"50m"` | Download test bandwidth cap |

**timeout:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `per_sample` | duration | `10s` | Timeout per sample |
| `per_cycle` | duration | `30s` | Timeout for entire test cycle |

**Example:**

```yaml
measurement:
  protocols:
    tcp:
      enabled: true
      alternate: true
      target_bandwidth:
        upload: 20m
        download: 100m
      chunk_size: 1200
      sample_size: 1mb
      sample_count: 3
      timeout:
        per_sample: 15s
        per_cycle: 60s
```

When `alternate` is `true`, fbforward alternates between upload and download tests across measurement cycles. When `false`, both upload and download tests run in each cycle.

Set `target_bandwidth` to match expected link capacity or slightly below. Exceeding actual capacity causes congestion and inaccurate results.

**Validation:**
- `target_bandwidth.upload` must be > 0
- `target_bandwidth.download` must be > 0
- `chunk_size` must be > 0
- `sample_size` must be > 0
- `sample_count` must be > 0
- `timeout.per_sample` must be > 0
- `timeout.per_cycle` must be > 0

#### udp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable UDP measurements |
| `target_bandwidth` | object | *see tcp* | Target bandwidth for tests |
| `chunk_size` | string | `"1200"` | Chunk size including headers (bytes) |
| `sample_size` | string | `"500kb"` | Payload bytes per sample |
| `sample_count` | int | `1` | Number of samples per test |
| `timeout` | object | *see tcp* | Timeout configuration |

**Example:**

```yaml
measurement:
  protocols:
    udp:
      enabled: true
      target_bandwidth:
        upload: 10m
        download: 50m
      chunk_size: 1200
      sample_size: 500kb
      sample_count: 1
      timeout:
        per_sample: 10s
        per_cycle: 30s
```

UDP measurements track packet loss. Higher `sample_count` improves loss rate accuracy.

**Validation:** Same as TCP (except `alternate` field does not exist for UDP)

**Protocol requirement:** At least one of TCP or UDP must be enabled.

---

## 4.7 scoring section

The `scoring` section configures the upstream quality scoring algorithm. Scores determine primary upstream selection. See [Section 6.1](algorithm-specifications.md#61-upstream-selection-algorithm) for algorithm details.

### smoothing

Exponential moving average (EMA) smoothing for metric updates.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `alpha` | float64 | `0.2` | EMA smoothing factor |

**Example:**

```yaml
scoring:
  smoothing:
    alpha: 0.3
```

Higher `alpha` values (closer to 1.0) give more weight to recent measurements. Lower values provide more smoothing. Formula: `smoothed = alpha * new + (1 - alpha) * old`.

**Validation:**
- Must be in range (0, 1]

### reference

Reference values for score normalization. Each metric is normalized relative to its reference value.

#### tcp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bandwidth.upload` | string | `"10m"` | Reference upload bandwidth |
| `bandwidth.download` | string | `"50m"` | Reference download bandwidth |
| `latency.rtt` | float64 | `50` | Reference RTT (milliseconds) |
| `latency.jitter` | float64 | `10` | Reference jitter (milliseconds) |
| `retransmit_rate` | float64 | `0.01` | Reference retransmit rate (1%) |

**Example:**

```yaml
scoring:
  reference:
    tcp:
      bandwidth:
        upload: 20m
        download: 100m
      latency:
        rtt: 30
        jitter: 5
      retransmit_rate: 0.005
```

Reference values define the "target" quality. Upstreams meeting or exceeding reference values receive high sub-scores. See [Section 6.1.2](algorithm-specifications.md#612-formal-description) for normalization formulas.

**Validation:**
- Bandwidth fields must be > 0
- Latency fields must be > 0
- `retransmit_rate` must be in range (0, 1]

#### udp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bandwidth.upload` | string | `"10m"` | Reference upload bandwidth |
| `bandwidth.download` | string | `"50m"` | Reference download bandwidth |
| `latency.rtt` | float64 | `50` | Reference RTT (milliseconds) |
| `latency.jitter` | float64 | `10` | Reference jitter (milliseconds) |
| `loss_rate` | float64 | `0.01` | Reference loss rate (1%) |

**Example:**

```yaml
scoring:
  reference:
    udp:
      bandwidth:
        upload: 10m
        download: 50m
      latency:
        rtt: 50
        jitter: 10
      loss_rate: 0.01
```

**Validation:**
- Bandwidth fields must be > 0
- Latency fields must be > 0
- `loss_rate` must be in range (0, 1]

### weights

Weights determine how much each metric contributes to the final score.

#### tcp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bandwidth_upload` | float64 | `0.15` | Upload bandwidth weight |
| `bandwidth_download` | float64 | `0.25` | Download bandwidth weight |
| `rtt` | float64 | `0.25` | RTT weight |
| `jitter` | float64 | `0.10` | Jitter weight |
| `retransmit_rate` | float64 | `0.25` | Retransmit rate weight |

**Example:**

```yaml
scoring:
  weights:
    tcp:
      bandwidth_upload: 0.20
      bandwidth_download: 0.30
      rtt: 0.25
      jitter: 0.05
      retransmit_rate: 0.20
```

Weights are automatically normalized to sum to 1.0. Increase weights for metrics that matter most for your application.

**Validation:**
- All weights must be ≥ 0
- Sum of weights must be > 0 (normalized automatically)

#### udp

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bandwidth_upload` | float64 | `0.10` | Upload bandwidth weight |
| `bandwidth_download` | float64 | `0.30` | Download bandwidth weight |
| `rtt` | float64 | `0.15` | RTT weight |
| `jitter` | float64 | `0.30` | Jitter weight |
| `loss_rate` | float64 | `0.15` | Loss rate weight |

**Example:**

```yaml
scoring:
  weights:
    udp:
      bandwidth_upload: 0.10
      bandwidth_download: 0.25
      rtt: 0.20
      jitter: 0.25
      loss_rate: 0.20
```

**Validation:** Same as TCP weights

#### protocol_blend

Blend TCP and UDP sub-scores into final score.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tcp_weight` | float64 | `0.5` | TCP sub-score weight |
| `udp_weight` | float64 | `0.5` | UDP sub-score weight |

**Example:**

```yaml
scoring:
  weights:
    protocol_blend:
      tcp_weight: 0.6
      udp_weight: 0.4
```

Weights are automatically normalized to sum to 1.0. Increase `tcp_weight` for TCP-heavy workloads, increase `udp_weight` for UDP-heavy workloads.

**Validation:**
- Both weights must be ≥ 0
- Sum of weights must be > 0 (normalized automatically)

### utilization_penalty

Utilization penalty reduces score when upstream carries heavy traffic. Prevents overloading a single upstream.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable utilization penalty |
| `window_duration` | duration | `5s` | Traffic sampling window |
| `update_interval` | duration | `1s` | Utilization recomputation interval |
| `threshold` | float64 | `0.7` | Utilization threshold (70%) |
| `min_multiplier` | float64 | `0.3` | Minimum score multiplier at 100% util |
| `exponent` | float64 | `2.0` | Penalty curve exponent |

**Example:**

```yaml
scoring:
  utilization_penalty:
    enabled: true
    window_duration: 10s
    update_interval: 2s
    threshold: 0.8
    min_multiplier: 0.5
    exponent: 1.5
```

When `enabled` is `true`, fbforward computes recent traffic utilization as a fraction of measured link capacity. Above `threshold`, score is multiplied by a penalty factor between 1.0 (at threshold) and `min_multiplier` (at 100% utilization). Penalty curve uses exponential function with `exponent`.

**Validation:**
- `window_duration` must be > 0
- `update_interval` must be > 0
- `threshold` must be > 0
- `min_multiplier` must be in range (0, 1]
- `exponent` must be > 0

### bias_transform

Bias transformation scales the `upstreams[].bias` field.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `kappa` | float64 | `0.693147` | Bias scaling constant (ln(2)) |

**Example:**

```yaml
scoring:
  bias_transform:
    kappa: 1.0
```

Bias is transformed via exponential: `multiplier = exp(bias / kappa)`. Default `kappa = ln(2)` means `bias = 1.0` doubles the score, `bias = -1.0` halves it.

**Validation:**
- Must be > 0

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
  auth_token: "secret-token-change-me"
  webui:
    enabled: true
  metrics:
    enabled: true
```

**Validation:**
- `auth_token` must not be empty
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

All endpoints except `/` and `/auth` require Bearer token authentication:

```bash
curl -H "Authorization: Bearer secret-token-change-me" http://localhost:8080/metrics
```

WebSocket authentication uses subprotocol for browser compatibility:

```javascript
new WebSocket('ws://localhost:8080/status', ['bearer', 'secret-token-change-me'])
```

See [Section 5.2](api-reference.md#52-control-plane-api) for API details.

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

## 4.10 shaping section

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
| `measurement` | [6.2](algorithm-specifications.md#62-bandwidth-measurement-algorithm-bwprobe) | [3.1.2](user-guide-fbforward.md#312-configuration) |
| `scoring` | [6.1.2](algorithm-specifications.md#612-formal-description) | [3.1.2](user-guide-fbforward.md#312-configuration) |
| `switching` | [6.1.4](algorithm-specifications.md#614-edge-cases) | [3.1.1](user-guide-fbforward.md#311-overview) |
| `control` | - | [3.1.3](user-guide-fbforward.md#313-operation), [5.2](api-reference.md#52-control-plane-api) |
| `shaping` | - | [3.1.2](user-guide-fbforward.md#312-configuration) |
