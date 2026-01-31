# Glossary

This glossary defines domain terminology used throughout the fbforward documentation. Terms are organized by category and cross-referenced to their defining sections.

---

## Architecture terms

### Control plane
The subsystem that handles management operations: HTTP API, WebSocket status streaming, RPC methods, and Prometheus metrics endpoint. Runs on the configurable `control.bind_addr:control.bind_port`. See [Section 1.2](outline.md#12-architecture-overview), [Section 5.2](outline.md#52-control-plane-api).

### Data plane
The subsystem that handles actual traffic forwarding: TCP listeners, UDP listeners, and per-flow proxying to upstreams. Configured via `forwarding.listeners`. See [Section 1.2](outline.md#12-architecture-overview), [Section 4.2](outline.md#42-forwarding-section).

### Flow
A single TCP connection or UDP 5-tuple mapping. The term "flow" is used generically throughout the documentation to refer to either protocol. When protocol-specific behavior is relevant, the documentation uses "TCP connection" or "UDP mapping" explicitly. Each flow is pinned to an upstream at creation time. See [Section 6.1.1](outline.md#611-overview).

### Flow pinning
The guarantee that once a flow is assigned to an upstream, it continues to use that upstream until termination (TCP FIN/RST) or expiry (UDP idle timeout), even if the primary upstream changes. See [Section 6.1.1](outline.md#611-overview).

### Flow table
Internal data structure mapping flow keys to upstream assignments. Key format: `(proto, srcIP, srcPort, dstIP, dstPort)`. See [Section 6.1.1](outline.md#611-overview).

### Forwarder
A component that accepts client connections and proxies traffic to upstreams. fbforward implements TCP and UDP forwarders. See [Section 3.1.1](outline.md#311-overview).

### Listener
A bind address and port where fbforward accepts client connections. Each listener has a protocol (tcp or udp) and optional per-listener shaping. See [Section 4.2](outline.md#42-forwarding-section).

### Measurement plane
The subsystem that probes upstream quality using bwprobe tests and ICMP reachability checks. Results feed into the scoring algorithm. See [Section 1.2](outline.md#12-architecture-overview), [Section 4.6](outline.md#46-measurement-section).

### Primary upstream
The only upstream that receives new flow assignments. Selected by the scoring algorithm in auto mode or operator choice in manual mode. See [Section 6.1.1](outline.md#611-overview).

### Proxy
See *Forwarder*.

### Runtime
The component that wires all subsystems together and manages their lifecycle. Created by Supervisor on startup or reload. See [Section 1.3](outline.md#13-component-relationships).

### Supervisor
Top-level component that owns Runtime and handles restart/reload lifecycle. See [Section 1.3](outline.md#13-component-relationships).

### Upstream
A destination server that fbforward forwards traffic to. Each upstream has a tag, destination address, measurement endpoint, and optional tuning parameters. See [Section 4.3](outline.md#43-upstreams-section).

---

## Measurement terms

### Bandwidth cap
The target sending rate for bwprobe tests. Configured via `measurement.protocols.*.target_bandwidth`. Uses SO_MAX_PACING_RATE on Linux. See [Section 4.6](outline.md#46-measurement-section), [Section 6.2.3](outline.md#623-parameters).

### Chunk size
The size of individual data frames sent during bwprobe tests. Affects pacing granularity. Default: 1200 bytes. See [Section 6.2.3](outline.md#623-parameters).

### EMA (exponential moving average)
Smoothing technique applied to measurement metrics. New value = alpha * measurement + (1 - alpha) * old value. Configured via `scoring.smoothing.alpha`. See [Section 6.1.2](outline.md#612-formal-description).

### Interval
A 100ms time bucket used for aggregating bwprobe sample data. Metrics are computed per-interval then combined. See [Section 6.2.2](outline.md#622-formal-description).

### Jitter
Variation in round-trip time between consecutive measurements. Lower is better. Measured in milliseconds. See [Section 6.1.2](outline.md#612-formal-description).

### Loss rate
Fraction of UDP packets lost during measurement. Computed as (sent - received) / sent. Lower is better. See [Section 6.1.2](outline.md#612-formal-description).

### Pacing rate
The rate at which data is sent, controlled by Linux kernel pacing. Set via SO_MAX_PACING_RATE socket option. See [Section 6.2.1](outline.md#621-overview).

### Retransmit rate
Fraction of TCP segments that required retransmission during measurement. Derived from TCP_INFO. Lower is better. See [Section 6.1.2](outline.md#612-formal-description).

### RTT (round-trip time)
Time for a packet to travel from sender to receiver and back. Measured via control channel or dedicated probes. Lower is better. See [Section 6.1.2](outline.md#612-formal-description).

### Sample
A single measurement run within a bwprobe test. Each sample transfers a fixed number of bytes at the target rate. Multiple samples may be run per test cycle. See [Section 6.2.1](outline.md#621-overview).

### Sample size
Number of payload bytes transferred per sample. Configured via `measurement.protocols.*.sample_size`. See [Section 6.2.3](outline.md#623-parameters).

### Sustained peak
Maximum average throughput over a rolling 1-second window during a sample. See [Section 6.2.2](outline.md#622-formal-description).

### Trimmed mean
Average throughput after dropping the top and bottom 10% of interval rates. More robust to outliers than simple mean. See [Section 6.2.2](outline.md#622-formal-description).

---

## Scoring terms

### Base quality score
The weighted combination of sub-scores before applying utilization penalty and bias. See [Section 6.1.2](outline.md#612-formal-description).

### Bias
Static adjustment to an upstream's score. Positive bias increases preference; negative decreases. Configured per-upstream. See [Section 4.3](outline.md#43-upstreams-section), [Section 6.1.2](outline.md#612-formal-description).

### Bias transform
Function that converts raw bias value to a score multiplier. Uses exponential form with configurable kappa. See [Section 6.1.2](outline.md#612-formal-description).

### Fast failover
Immediate switch to another upstream when the current primary experiences high loss/retransmit rates or consecutive dial failures. See [Section 6.1.4](outline.md#614-edge-cases).

### Fast-start mode
Initial operation mode using lightweight RTT probes for immediate primary selection. Transitions to full scoring after warmup period. See [Section 6.1.4](outline.md#614-edge-cases).

### Final score
The upstream's computed quality score after all adjustments: base score * utilization multiplier * bias multiplier + priority. See [Section 6.1.2](outline.md#612-formal-description).

### Hold time
Minimum duration before switching away from current primary. Prevents oscillation. Configured via `switching.auto.min_hold_time`. See [Section 4.8](outline.md#48-switching-section).

### Priority
Static integer bonus added to an upstream's final score. Higher priority increases preference. Configured per-upstream. See [Section 4.3](outline.md#43-upstreams-section).

### Protocol blend
Weighted combination of TCP and UDP sub-scores. Configured via `scoring.weights.protocol_blend`. See [Section 6.1.2](outline.md#612-formal-description).

### Reference value
Target/ideal value for a metric. Used to normalize sub-scores. Configured in `scoring.reference`. See [Section 6.1.3](outline.md#613-parameters).

### Score delta threshold
Minimum score difference required to trigger a switch. Prevents switching on noise. Configured via `switching.auto.score_delta_threshold`. See [Section 4.8](outline.md#48-switching-section).

### Sub-score
Normalized score (0-1) for a single metric (bandwidth, RTT, jitter, loss, retransmit). See [Section 6.1.2](outline.md#612-formal-description).

### Unusable upstream
An upstream marked as not available for selection due to 100% loss, consecutive dial failures, or other fatal conditions. Automatically recovers when probes succeed. See [Section 6.1.4](outline.md#614-edge-cases).

### Utilization penalty
Score reduction applied when actual traffic exceeds configured link capacity. Prevents overloading busy upstreams. See [Section 6.1.2](outline.md#612-formal-description).

### Weight
Relative importance of a metric in the scoring formula. All weights for a protocol should sum to 1.0. See [Section 6.1.3](outline.md#613-parameters).

---

## Configuration terms

### Bandwidth format
String format for specifying bandwidth values: number followed by unit suffix. Valid suffixes: `k` (Kbps), `m` (Mbps), `g` (Gbps). Example: `10m` = 10 Mbps. See [Section 4.1](outline.md#41-configuration-file-format).

### Default value
The value used when a configuration field is not specified. Defaults are documented per-field in Section 4. See [Section 4.1](outline.md#41-configuration-file-format).

### Duration format
String format for specifying time durations: number followed by unit suffix. Valid suffixes: `s` (seconds), `m` (minutes), `h` (hours). Example: `30s` = 30 seconds. See [Section 4.1](outline.md#41-configuration-file-format).

### Field
A single configuration option within a section. Has a name, type, default value, and validation rules. See [Section 4.1](outline.md#41-configuration-file-format).

### Host
Hostname or IP address of a destination. May be resolved via DNS. See [Section 4.3](outline.md#43-upstreams-section).

### Port
TCP or UDP port number (1-65535). See [Section 4.2](outline.md#42-forwarding-section), [Section 4.3](outline.md#43-upstreams-section).

### Section
Top-level key in the configuration file (forwarding, upstreams, measurement, etc.). See [Section 4.1](outline.md#41-configuration-file-format).

### Tag
Unique identifier for an upstream. Used in logs, metrics, and manual selection. See [Section 4.3](outline.md#43-upstreams-section).

---

## Protocol terms

### Control channel
TCP connection used for bwprobe session management and sample coordination. Carries JSON-RPC messages. See [Section 6.3.1](outline.md#631-overview).

### Data channel
TCP or UDP connection used for actual bandwidth measurement data transfer. See [Section 6.2.1](outline.md#621-overview).

### JSON-RPC
Protocol used for bwprobe control communication and fbforward control plane RPC. Version 2.0 with length-prefixed framing. See [Section 6.3.1](outline.md#631-overview), [Section 5.2.2](outline.md#522-rpc-methods).

### Reverse mode
bwprobe mode where the server sends data to the client (download test) instead of client sending to server (upload). See [Section 3.2.1](outline.md#321-overview).

### WebSocket
Protocol used for real-time status streaming from fbforward control plane. Authenticated via Bearer token in subprotocol. See [Section 5.2.3](outline.md#523-websocket-status-stream).

---

## Shaping terms

### Aggregate limit
Maximum total bandwidth for all traffic through fbforward in each direction. Configured via `shaping.aggregate_limit`. See [Section 4.10](outline.md#410-shaping-section).

### Download limit
Maximum bandwidth for traffic received from an upstream (upstream → fbforward). Configured per-upstream or per-listener. See [Section 4.10](outline.md#410-shaping-section).

### IFB device
Intermediate Functional Block device used by Linux tc for ingress shaping. Configured via `shaping.ifb_device`. See [Section 4.10](outline.md#410-shaping-section).

### Ingress shaping
Rate limiting of incoming traffic (from upstreams to fbforward). Implemented via IFB redirect on Linux. See [Section 4.10](outline.md#410-shaping-section).

### tc (traffic control)
Linux kernel subsystem for traffic shaping. fbforward uses tc via netlink for rate limiting. Requires CAP_NET_ADMIN. See [Section 4.10](outline.md#410-shaping-section).

### Upload limit
Maximum bandwidth for traffic sent to an upstream (fbforward → upstream). Configured per-upstream or per-listener. See [Section 4.10](outline.md#410-shaping-section).
