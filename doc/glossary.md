# Glossary

This glossary defines domain terminology used throughout the fbforward documentation. Terms are organized by category and cross-referenced to their defining sections.

---

## Architecture terms

### Control plane
The subsystem that handles management operations: HTTP API, WebSocket status streaming, RPC methods, and Prometheus metrics endpoint. Runs on the configurable `control.bind_addr:control.bind_port`. See [Section 1.2](project-overview.md), [Section 5.2](project-overview.md).

### Data plane
The subsystem that handles actual traffic forwarding: TCP listeners, UDP listeners, and per-flow proxying to upstreams. Configured via `forwarding.listeners`. See [Section 1.2](project-overview.md), [Section 4.2](project-overview.md).

### Flow
A single TCP connection or UDP 5-tuple mapping. The term "flow" is used generically throughout the documentation to refer to either protocol. When protocol-specific behavior is relevant, the documentation uses "TCP connection" or "UDP mapping" explicitly. Each flow is pinned to an upstream at creation time. See [Section 6.1.1](project-overview.md).

### Flow pinning
The guarantee that once a flow is assigned to an upstream, it continues to use that upstream until termination (TCP FIN/RST) or expiry (UDP idle timeout), even if the primary upstream changes. See [Section 6.1.1](project-overview.md).

### Flow table
Internal data structure mapping flow keys to upstream assignments. Key format: `(proto, srcIP, srcPort, dstIP, dstPort)`. See [Section 6.1.1](project-overview.md).

### Forwarder
A component that accepts client connections and proxies traffic to upstreams. fbforward implements TCP and UDP forwarders. See [Section 3.1.1](project-overview.md).

### Listener
A bind address and port where fbforward accepts client connections. Each listener has a protocol (tcp or udp) and optional per-listener shaping. See [Section 4.2](project-overview.md).

### Measurement plane
The subsystem that probes adaptive-route upstreams with fbmeasure TCP/UDP RTT
probes and maintains a unified health state. It does not calculate a score.

### Primary upstream
Legacy control-plane term for the currently selected upstream. Adaptive routes
select independently by health, RTT, priority, and configuration order.

### Proxy
See *Forwarder*.

### Runtime
The component that wires all subsystems together and manages their lifecycle. Created by Supervisor on startup or reload. See [Section 1.3](project-overview.md).

### Supervisor
Top-level component that owns Runtime and handles restart/reload lifecycle. See [Section 1.3](project-overview.md).

### Upstream
A destination server that fbforward forwards traffic to. Each upstream has a tag, destination address, measurement endpoint, and optional tuning parameters. See [Section 4.3](project-overview.md).

---

## Measurement terms

### Bandwidth cap
The target sending rate for bwprobe tests. Configured via `measurement.protocols.*.target_bandwidth`. Uses SO_MAX_PACING_RATE on Linux. See [Section 4.6](project-overview.md), [Section 6.2.3](project-overview.md).

### Chunk size
The size of individual data frames sent during bwprobe tests. Affects pacing granularity. Default: 1200 bytes. See [Section 6.2.3](project-overview.md).

### EMA (exponential moving average)
Smoothing applied to successful RTT observations. New value = alpha * RTT +
(1 - alpha) * previous RTT. Configured via `health.rtt_ewma_alpha`.

### Interval
A 100ms time bucket used for aggregating bwprobe sample data. Metrics are computed per-interval then combined. See [Section 6.2.2](project-overview.md).

### Jitter
Variation in round-trip time between consecutive measurements. Lower is better. Measured in milliseconds. See [Section 6.1.2](project-overview.md).

### Loss rate
Fraction of UDP packets lost during measurement. Computed as (sent - received) / sent. Lower is better. See [Section 6.1.2](project-overview.md).

### Pacing rate
The rate at which data is sent, controlled by Linux kernel pacing. Set via SO_MAX_PACING_RATE socket option. See [Section 6.2.1](project-overview.md).

### Retransmit rate
Fraction of TCP segments that required retransmission during measurement. Derived from TCP_INFO. Lower is better. See [Section 6.1.2](project-overview.md).

### RTT (round-trip time)
Time for a packet to travel from sender to receiver and back. Measured via control channel or dedicated probes. Lower is better. See [Section 6.1.2](project-overview.md).

### Sample
A single measurement run within a bwprobe test. Each sample transfers a fixed number of bytes at the target rate. Multiple samples may be run per test cycle. See [Section 6.2.1](project-overview.md).

### Sample size
Number of payload bytes transferred per sample. Configured via `measurement.protocols.*.sample_size`. See [Section 6.2.3](project-overview.md).

### Sustained peak
Maximum average throughput over a rolling 1-second window during a sample. See [Section 6.2.2](project-overview.md).

### Trimmed mean
Average throughput after dropping the top and bottom 10% of interval rates. More robust to outliers than simple mean. See [Section 6.2.2](project-overview.md).

---

## Health and selection terms

### Health state
The current state of an upstream: `unknown`, `healthy`, `stale`, or `down`.
Adaptive selection excludes `down` candidates.

### HealthSnapshot
The in-memory state containing health state, RTT EWMA, last attempt/success
timestamps, and consecutive success/failure counters.

### Priority
Static ordering value used after health and RTT for adaptive route selection.

### Route-local selection
Selection restricted to the upstream list of one route. A static route uses its
single configured upstream; an adaptive route ranks only its own candidates.

### Unusable upstream
An upstream excluded from adaptive selection because it is `down` or in dial
cooldown. Static routes remain fixed and only honor dial cooldown.

---

## Configuration terms

### Bandwidth format
String format for specifying bandwidth values: number followed by unit suffix. Valid suffixes: `k` (Kbps), `m` (Mbps), `g` (Gbps). Example: `10m` = 10 Mbps. See [Section 4.1](project-overview.md).

### Default value
The value used when a configuration field is not specified. Defaults are documented per-field in Section 4. See [Section 4.1](project-overview.md).

### Duration format
String format for specifying time durations: number followed by unit suffix. Valid suffixes: `s` (seconds), `m` (minutes), `h` (hours). Example: `30s` = 30 seconds. See [Section 4.1](project-overview.md).

### Field
A single configuration option within a section. Has a name, type, default value, and validation rules. See [Section 4.1](project-overview.md).

### Host
Hostname or IP address of a destination. May be resolved via DNS. See [Section 4.3](project-overview.md).

### Port
TCP or UDP port number (1-65535). See [Section 4.2](project-overview.md), [Section 4.3](project-overview.md).

### Section
Top-level key in the configuration file (forwarding, upstreams, measurement, etc.). See [Section 4.1](project-overview.md).

### Tag
Unique identifier for an upstream. Used in logs, metrics, and manual selection. See [Section 4.3](project-overview.md).

---

---

## Protocol terms

### Control channel
TCP connection used for bwprobe session management and sample control. Carries JSON-RPC messages. See [Section 6.3.1](project-overview.md).

### Data channel
TCP or UDP connection used for actual bandwidth measurement data transfer. See [Section 6.2.1](project-overview.md).

### JSON-RPC
Protocol used for bwprobe control communication and fbforward control plane RPC. Version 2.0 with length-prefixed framing. See [Section 6.3.1](project-overview.md), [Section 5.2.2](project-overview.md).

### Reverse mode
bwprobe mode where the server sends data to the client (download test) instead of client sending to server (upload). See [Section 3.2.1](project-overview.md).

### WebSocket
Protocol used for real-time status streaming from fbforward control plane. Authenticated via Bearer token in subprotocol. See [Section 5.2.3](project-overview.md).

---

## Shaping terms

### Aggregate limit
Maximum total bandwidth for all traffic through fbforward in each direction. Configured via `shaping.aggregate_limit`. See [Section 4.10](project-overview.md).

### Download limit
Maximum bandwidth for traffic received from an upstream (upstream → fbforward). Configured per-upstream or per-listener. See [Section 4.10](project-overview.md).

### IFB device
Intermediate Functional Block device used by Linux tc for ingress shaping. Configured via `shaping.ifb_device`. See [Section 4.10](project-overview.md).

### Ingress shaping
Rate limiting of incoming traffic (from upstreams to fbforward). Implemented via IFB redirect on Linux. See [Section 4.10](project-overview.md).

### tc (traffic control)
Linux kernel subsystem for traffic shaping. fbforward uses tc via netlink for rate limiting. Requires CAP_NET_ADMIN. See [Section 4.10](project-overview.md).

### Upload limit
Maximum bandwidth for traffic sent to an upstream (fbforward → upstream). Configured per-upstream or per-listener. See [Section 4.10](project-overview.md).
