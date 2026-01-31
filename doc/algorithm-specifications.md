# Algorithm specifications

This section provides formal specifications for fbforward algorithms. For operational guidance, see user guides ([fbforward](user-guide-fbforward.md), [bwprobe](user-guide-bwprobe.md)).

---

## 6.1 Upstream selection algorithm

### 6.1.1 Overview

The upstream selection algorithm determines which upstream receives new flow assignments. The algorithm guarantees [flow pinning](glossary.md#flow-pinning): once a flow is assigned to an upstream, it remains pinned until termination (TCP close or UDP idle timeout).

**Key concepts:**

- **[Primary upstream](glossary.md#primary-upstream)**: The only upstream that receives new flow assignments
- **Pinned flow**: Existing flow mapped to an upstream via flow table
- **Flow table**: Maps (protocol, srcIP, srcPort, dstIP, dstPort) → upstream tag
- **Scoring**: Quality metric combining bandwidth, RTT, jitter, and loss/retransmit measurements

**Forwarding rule:**

1. Lookup flow key in flow table
2. If found, forward to mapped upstream (pinned flow)
3. If not found, assign to primary upstream, insert mapping, forward (new flow)

**TCP lifecycle:** Create mapping on connection accept, remove on FIN/RST.

**UDP lifecycle:** Create mapping on first packet, remove after idle timeout.

**Switching impact:** Changing the primary upstream affects only new flows. Existing flows continue on their assigned upstream until completion.

### 6.1.2 Formal description

#### Fast-start mode

At startup, select a primary upstream immediately using lightweight TCP RTT probes without waiting for full bwprobe measurements.

**Initial score formula:**

$$S_{\mathrm{init},i} = \mathbf{1}_{\mathrm{reachable},i} \cdot \left( \frac{100}{1 + R_{\mathrm{probe},i} / R_0} + P_i \right)$$

**Parameters:**

| Symbol | Description | Default |
|--------|-------------|---------|
| $\mathbf{1}_{\mathrm{reachable},i}$ | 1 if probe response received, 0 otherwise | — |
| $R_{\mathrm{probe},i}$ | Probe RTT (milliseconds) | Measured |
| $R_0$ | RTT normalization constant | 50 ms |
| $P_i$ | Static priority bonus (`upstreams[].priority`) | 0 |
| $T_{\mathrm{probe}}$ | Probe timeout | 500 ms |

**Primary selection:** $i^* = \arg\max_{i} S_{\mathrm{init},i}$

**Warmup transition:** After $T_{\mathrm{warmup}}$ (default: 15s), switch to full scoring.

**Relaxed switching during warmup:** Use $\delta_{\mathrm{switch}}' = \delta_{\mathrm{switch}} / 2$, $T_{\mathrm{hold}}' = 0$.

#### Sub-scores

Each metric is normalized to $[\varepsilon, 1]$ where $\varepsilon = 0.001$.

**Bandwidth (higher is better):**

$$s_{B_{\mathrm{up}}} = \max\left(1 - \exp\left(-B_{\mathrm{up}} / B_{\mathrm{up}}^{\mathrm{ref}}\right), \varepsilon\right)$$

$$s_{B_{\mathrm{dn}}} = \max\left(1 - \exp\left(-B_{\mathrm{dn}} / B_{\mathrm{dn}}^{\mathrm{ref}}\right), \varepsilon\right)$$

Exponential normalization ensures diminishing returns: doubling bandwidth does not double the score. Prevents high-bandwidth links from dominating when all upstreams exceed reference value.

**RTT and jitter (lower is better):**

$$s_R = \max\left(\exp\left(-R / R^{\mathrm{ref}}\right), \varepsilon\right)$$

$$s_J = \max\left(\exp\left(-J / J^{\mathrm{ref}}\right), \varepsilon\right)$$

Lower RTT/jitter yields higher scores. Exponential decay penalizes high latency/jitter.

**Retransmission rate (TCP) and loss rate (UDP):**

$$s_\rho = \max\left(\exp\left(-\rho / \rho^{\mathrm{ref}}\right), \varepsilon\right)$$

$$s_L = \max\left(\exp\left(-L / L^{\mathrm{ref}}\right), \varepsilon\right)$$

Lower loss/retransmit rates yield higher scores.

**Reference parameters:**

| Parameter | Description | Default | Config path |
|-----------|-------------|---------|-------------|
| $B_{\mathrm{up}}^{\mathrm{ref}}$ | Target upload bandwidth | 10 Mbps | `scoring.reference.tcp.bandwidth.upload` |
| $B_{\mathrm{dn}}^{\mathrm{ref}}$ | Target download bandwidth | 50 Mbps | `scoring.reference.tcp.bandwidth.download` |
| $R^{\mathrm{ref}}$ | Target RTT | 50 ms | `scoring.reference.tcp.latency.rtt` |
| $J^{\mathrm{ref}}$ | Target jitter | 10 ms | `scoring.reference.tcp.latency.jitter` |
| $\rho^{\mathrm{ref}}$ | Target TCP retransmit rate | 0.01 (1%) | `scoring.reference.tcp.retransmit_rate` |
| $L^{\mathrm{ref}}$ | Target UDP loss rate | 0.01 (1%) | `scoring.reference.udp.loss_rate` |

#### Base quality score

**TCP:**

$$Q_{\mathrm{tcp}} = 100 \cdot s_{B_{\mathrm{up}}}^{w_{B_{\mathrm{up}}}} \cdot s_{B_{\mathrm{dn}}}^{w_{B_{\mathrm{dn}}}} \cdot s_R^{w_R} \cdot s_J^{w_J} \cdot s_\rho^{w_\rho}$$

**UDP:**

$$Q_{\mathrm{udp}} = 100 \cdot s_{B_{\mathrm{up}}}^{w_{B_{\mathrm{up}}}} \cdot s_{B_{\mathrm{dn}}}^{w_{B_{\mathrm{dn}}}} \cdot s_R^{w_R} \cdot s_J^{w_J} \cdot s_L^{w_L}$$

**Weights (automatically normalized to sum to 1):**

| Weight | TCP default | UDP default | Config path |
|--------|-------------|-------------|-------------|
| $w_{B_{\mathrm{up}}}$ | 0.15 | 0.10 | `scoring.weights.tcp.bandwidth_upload` |
| $w_{B_{\mathrm{dn}}}$ | 0.25 | 0.30 | `scoring.weights.tcp.bandwidth_download` |
| $w_R$ | 0.25 | 0.15 | `scoring.weights.tcp.rtt` |
| $w_J$ | 0.10 | 0.30 | `scoring.weights.tcp.jitter` |
| $w_\rho$ / $w_L$ | 0.25 | 0.15 | `scoring.weights.tcp.retransmit_rate` |

Weights reflect typical application requirements: TCP favors low retransmit rate and moderate bandwidth, UDP favors low jitter and high download bandwidth.

#### Utilization penalty

Utilization penalty prevents overloading a single upstream by reducing score when traffic approaches measured capacity.

$$M = m_{\min} + (1 - m_{\min}) \cdot \exp\left(-\left(u / u^0\right)^p\right)$$

where $u = \max(u_{\mathrm{up}}, u_{\mathrm{dn}})$ and utilization is computed per upstream:

$$u_{\mathrm{up}} = \frac{\tau_{\mathrm{up}} \cdot 8}{B_{\mathrm{up}}^{\mathrm{emp}} \cdot T_0}, \quad u_{\mathrm{dn}} = \frac{\tau_{\mathrm{dn}} \cdot 8}{B_{\mathrm{dn}}^{\mathrm{emp}} \cdot T_0}$$

**Variables:**

- $\tau_{\mathrm{up}}$, $\tau_{\mathrm{dn}}$: Actual traffic bytes (upload/download) over window $T_0$
- $B_{\mathrm{up}}^{\mathrm{emp}}$, $B_{\mathrm{dn}}^{\mathrm{emp}}$: Last measured bandwidth (bits/sec)
- $T_0$: Traffic sampling window (seconds)

**Parameters:**

| Symbol | Description | Default | Config path |
|--------|-------------|---------|-------------|
| $m_{\min}$ | Minimum score multiplier at 100% utilization | 0.3 | `scoring.utilization_penalty.min_multiplier` |
| $u^0$ | Utilization threshold | 0.7 (70%) | `scoring.utilization_penalty.threshold` |
| $p$ | Penalty exponent | 2.0 | `scoring.utilization_penalty.exponent` |
| $T_0$ | Window duration | 5s | `scoring.utilization_penalty.window_duration` |

**Behavior:**

- $u < u^0$: No penalty ($M \approx 1$)
- $u = u^0$: $M \approx 0.87$ (moderate penalty)
- $u = 1.0$: $M = m_{\min} = 0.3$ (severe penalty)

Utilization is computed on-demand from recent traffic samples, so metrics and UI reflect near-real-time link usage.

#### Bias transformation

User-specified bias adjusts scores up or down.

$$M_\beta = \exp(\kappa \cdot \beta)$$

where $\beta \in [-1, 1]$ is user preference and $\kappa = \ln 2 \approx 0.693147$.

**Parameters:**

| Symbol | Description | Default | Config path |
|--------|-------------|---------|-------------|
| $\beta$ | Bias adjustment | 0 | `upstreams[].bias` |
| $\kappa$ | Scaling constant | $\ln 2$ | `scoring.bias_transform.kappa` |

**Behavior:**

- $\beta = 0$: No adjustment ($M_\beta = 1$)
- $\beta = 1$: Maximum positive bias ($M_\beta \approx 1.5$)
- $\beta = -1$: Maximum negative bias ($M_\beta \approx 0.67$)

**Clamp:** The bias multiplier is clamped to the range $[0.67, 1.5]$ to prevent extreme score distortions. The theoretical formula $\exp(\kappa \cdot \beta)$ would produce values in $[0.5, 2.0]$, but the implementation applies a tighter clamp.

#### Final scores

**Per-protocol:**

$$S_{\mathrm{tcp}} = Q_{\mathrm{tcp}} \cdot M \cdot M_\beta$$

$$S_{\mathrm{udp}} = Q_{\mathrm{udp}} \cdot M \cdot M_\beta$$

**Overall upstream score:**

$$S_{\mathrm{overall}} = \omega_{\mathrm{tcp}} \cdot S_{\mathrm{tcp}} + \omega_{\mathrm{udp}} \cdot S_{\mathrm{udp}}$$

**Protocol blend weights:**

| Weight | Default | Config path |
|--------|---------|-------------|
| $\omega_{\mathrm{tcp}}$ | 0.5 | `scoring.weights.protocol_blend.tcp_weight` |
| $\omega_{\mathrm{udp}}$ | 0.5 | `scoring.weights.protocol_blend.udp_weight` |

Weights are automatically normalized to sum to 1.

**Final adjustment:**

$$S_{\mathrm{final}} = P \cdot S_{\mathrm{overall}}$$

where $P$ is static priority (`upstreams[].priority`, default 0).

**Score interpretation:**

- Score ≥ 80: Excellent quality
- Score 60-80: Good quality
- Score 40-60: Fair quality
- Score < 40: Poor quality
- Score 0: Upstream unusable (100% loss or consecutive dial failures)

#### Metric smoothing

Apply exponential moving average (EMA) to all measurements before scoring:

$$\tilde{X} = \alpha \cdot X_{\mathrm{new}} + (1 - \alpha) \cdot \tilde{X}_{\mathrm{prev}}$$

where $\alpha = 0.2$ (configurable via `scoring.smoothing.alpha`).

**Initialization:** First measurement initializes smoothed value ($\tilde{X} = X_{\mathrm{new}}$).

**Staleness handling:** If measurement age exceeds $T_{\mathrm{stale}}$ (default: 60 minutes, configurable via `measurement.stale_threshold`):

- Bandwidth: Use $0.5 \times \text{reference value}$
- RTT/jitter/loss: Use $2 \times \text{reference value}$

**Note:** ICMP probes monitor reachability continuously but do not contribute to quality scoring. The `measurement.fallback_to_icmp_on_stale` configuration flag only controls logging behavior (logs a warning when stale threshold is exceeded).

### 6.1.3 Parameters

All parameters are configurable via [Section 4](configuration-reference.md). This table summarizes algorithm-critical parameters.

| Parameter | Symbol | Default | Valid range | Config path |
|-----------|--------|---------|-------------|-------------|
| **Scoring** |
| EMA alpha | $\alpha$ | 0.2 | (0, 1] | `scoring.smoothing.alpha` |
| TCP ref upload BW | $B_{\mathrm{up}}^{\mathrm{ref}}$ | 10 Mbps | > 0 | `scoring.reference.tcp.bandwidth.upload` |
| TCP ref download BW | $B_{\mathrm{dn}}^{\mathrm{ref}}$ | 50 Mbps | > 0 | `scoring.reference.tcp.bandwidth.download` |
| TCP ref RTT | $R^{\mathrm{ref}}$ | 50 ms | > 0 | `scoring.reference.tcp.latency.rtt` |
| TCP ref jitter | $J^{\mathrm{ref}}$ | 10 ms | > 0 | `scoring.reference.tcp.latency.jitter` |
| TCP ref retrans rate | $\rho^{\mathrm{ref}}$ | 0.01 | (0, 1] | `scoring.reference.tcp.retransmit_rate` |
| UDP ref loss rate | $L^{\mathrm{ref}}$ | 0.01 | (0, 1] | `scoring.reference.udp.loss_rate` |
| **Utilization penalty** |
| Min multiplier | $m_{\min}$ | 0.3 | (0, 1] | `scoring.utilization_penalty.min_multiplier` |
| Threshold | $u^0$ | 0.7 | > 0 | `scoring.utilization_penalty.threshold` |
| Exponent | $p$ | 2.0 | > 0 | `scoring.utilization_penalty.exponent` |
| Window duration | $T_0$ | 5s | > 0 | `scoring.utilization_penalty.window_duration` |
| **Bias** |
| Bias kappa | $\kappa$ | $\ln 2$ | > 0 | `scoring.bias_transform.kappa` |
| **Switching** |
| Score delta threshold | $\delta_{\mathrm{switch}}$ | 5.0 | ≥ 0 | `switching.auto.score_delta_threshold` |
| Confirm duration | $T_{\mathrm{confirm}}$ | 15s | ≥ 0 | `switching.auto.confirm_duration` |
| Min hold time | $T_{\mathrm{hold}}$ | 30s | ≥ 0 | `switching.auto.min_hold_time` |
| Loss failover threshold | $L_{\mathrm{fail}}$ | 0.2 | (0, 1] | `switching.failover.loss_rate_threshold` |
| Retrans failover threshold | $\rho_{\mathrm{fail}}$ | 0.2 | (0, 1] | `switching.failover.retransmit_rate_threshold` |
| **Fast-start** |
| RTT normalization | $R_0$ | 50 ms | > 0 | (hardcoded) |
| Probe timeout | $T_{\mathrm{probe}}$ | 500 ms | > 0 | `measurement.fast_start.timeout` |
| Warmup duration | $T_{\mathrm{warmup}}$ | 15s | ≥ 0 | `measurement.fast_start.warmup_duration` |

### 6.1.4 Edge cases

#### Unusable upstreams

An upstream becomes unusable when:

- **100% loss:** All recent ICMP probes fail
- **Consecutive dial failures:** $N_{\mathrm{fail}} \geq 2$ connection failures within failure window
- **Manual override rejected:** Operator attempts to pin to upstream that fails validation

**Recovery:** Upstream becomes usable again when next ICMP probe succeeds. Dial failures expire after timeout period.

**Scoring impact:** Unusable upstreams receive score of 0 and are excluded from primary selection.

#### Fast failover

Fast failover bypasses normal switching hysteresis when primary upstream degrades severely.

**Triggers:**

$$L_{\mathrm{current}} > L_{\mathrm{fail}} \quad \text{or} \quad \rho_{\mathrm{current}} > \rho_{\mathrm{fail}}$$

**Behavior:**

1. Immediate primary switch to best available upstream
2. Skip $T_{\mathrm{confirm}}$ and $T_{\mathrm{hold}}$ checks
3. If `switching.close_flows_on_failover` is `true`, terminate all flows to failed upstream (TCP RST, UDP expire)

**Default thresholds:** $L_{\mathrm{fail}} = \rho_{\mathrm{fail}} = 0.2$ (20% loss/retransmit rate)

#### Stale measurements

Measurements become stale when age exceeds `measurement.stale_threshold` (default: 60 minutes).

**Fallback behavior:**

When measurements are stale, the scoring engine applies reference-value penalties:

- Bandwidth sub-scores computed using $0.5 \times \text{reference value}$
- RTT/jitter/loss sub-scores computed using $2 \times \text{reference value}$

This effectively degrades the upstream's quality score. The stale upstream remains selectable but will likely be outscored by upstreams with fresh measurements.

**Note:** ICMP probes monitor reachability continuously but do not contribute to quality scoring. The `measurement.fallback_to_icmp_on_stale` configuration flag only controls logging behavior (logs a warning when measurements become stale).

**Warning:** Stale scores may not reflect current link quality. Reduce `measurement.schedule.interval.max` if staleness occurs frequently.

#### Missing protocol measurements

If TCP or UDP measurements are unavailable:

- Use last valid measurement for missing protocol
- If no valid measurement exists, use reference-based defaults
- Protocol blend weight for missing protocol is effectively 0

**Example:** If UDP measurement fails but TCP succeeds:

$$S_{\mathrm{overall}} \approx S_{\mathrm{tcp}}$$

#### Manual mode override

In manual mode, operator specifies primary upstream via RPC.

**Validation:**

- Upstream must exist in configuration
- Upstream must be usable (not 100% loss, not dial-failed)

**Rejection:** RPC returns error if upstream is unusable. Primary remains unchanged.

**Switching:** Manual override bypasses all scoring and hysteresis. Primary switches immediately to specified tag.

#### Warmup mode

During warmup period ($T_{\mathrm{warmup}}$), scoring uses relaxed switching parameters:

- $\delta_{\mathrm{switch}}' = \delta_{\mathrm{switch}} / 2$ (lower score gap required)
- $T_{\mathrm{hold}}' = 0$ (no minimum hold time)
- $T_{\mathrm{confirm}}$ unchanged

**Rationale:** Fast-start RTT probes are less accurate than full bwprobe measurements. Relaxed parameters allow quick correction if initial selection is suboptimal.

**Transition:** After $T_{\mathrm{warmup}}$, switch to normal parameters. Any pending confirm state is reset.

---

## 6.2 Bandwidth measurement algorithm (bwprobe)

### 6.2.1 Overview

bwprobe measures bandwidth at a user-specified rate cap using sample-based testing with server-side timing.

**Goals:**

- Maximize accuracy while minimizing time overhead
- Use kernel pacing (`SO_MAX_PACING_RATE`) for precise rate limiting
- Avoid client-side timing bias via server-side interval aggregation
- Support both TCP (retransmit tracking) and UDP (loss tracking)

**Two-channel design:**

- **Control channel:** TCP connection for JSON-RPC commands (session setup, sample coordination)
- **Data channel:** TCP or UDP stream for actual bandwidth measurement data

**Sample-based model:**

1. Client sends `sample.start` command
2. Data transfer runs at target rate until sample size reached
3. Client sends `sample.stop` command
4. Server aggregates data into 100ms intervals
5. Server returns per-sample report with metrics

### 6.2.2 Formal description

#### Pacing and framing

**Kernel pacing:** Linux `SO_MAX_PACING_RATE` socket option enforces bandwidth cap.

**Target rate:** $B_{\mathrm{target}}$ (bits per second, configured via `-bandwidth` flag)

**Chunk size:** $C$ (bytes, configured via `-chunk-size` flag, default 1200 bytes)

**Frame format:**

| Field | Size | Description |
|-------|------|-------------|
| Magic | 4 bytes | `0xBEEFCAFE` |
| Sequence | 4 bytes | Monotonic counter |
| Timestamp | 8 bytes | Nanoseconds since epoch |
| Payload | $C - 16$ bytes | Data |

**Send rate:** Paced to achieve $B_{\mathrm{target}}$ over the sample duration.

#### Interval aggregation

Server aggregates received data into fixed 100ms intervals.

**Interval duration:** $\Delta t = 100$ ms (hardcoded in [bwprobe/internal/rpc/session.go](../bwprobe/internal/rpc/session.go))

**Per-interval metrics:**

- $n_i$: Bytes received in interval $i$
- $t_i$: Interval start time
- $\Delta t_i$: Actual interval duration (typically 100ms)

**Interval throughput:**

$$r_i = \frac{n_i \cdot 8}{\Delta t_i} \quad \text{(bits per second)}$$

#### Throughput calculation

**Trimmed mean:** Primary bandwidth metric. Drop top/bottom 10% of interval rates, average remainder.

$$\text{Trimmed mean} = \frac{1}{k} \sum_{i \in S} r_i$$

where $S$ is the set of intervals after trimming and $k = |S|$.

**Sustained peak:** Maximum average throughput over rolling 1-second window.

$$\text{Peak}_{1s} = \max_{j} \left( \frac{1}{W} \sum_{i=j}^{j+W-1} r_i \right)$$

where $W = \lceil 1.0 / \Delta t \rceil$ (number of intervals in 1 second, typically 10).

**Percentiles:** Sort interval rates, select P90/P80 values.

$$P_{90} = \text{percentile}(r, 0.90), \quad P_{80} = \text{percentile}(r, 0.80)$$

#### RTT measurement

Continuous RTT sampling during tests at configured rate (default 10 samples/sec).

**TCP RTT:** Read from `TCP_INFO` socket option, `tcpi_rtt` field (microseconds).

**Sampling:** Client samples RTT at regular intervals during data transfer.

**Statistics:**

- Mean: $\bar{R} = \frac{1}{N} \sum_{i=1}^{N} R_i$
- Min: $R_{\min} = \min_i R_i$
- Max: $R_{\max} = \max_i R_i$
- Jitter (standard deviation): $\sigma_R = \sqrt{\frac{1}{N} \sum_{i=1}^{N} (R_i - \bar{R})^2}$

#### Loss and retransmit measurement

**TCP retransmit rate (sender side):**

Read from `TCP_INFO` socket option:

- `tcpi_total_retrans`: Total retransmits
- `tcpi_segs_out`: Total segments sent

$$\text{Retrans rate} = \frac{\text{tcpi\_total\_retrans}}{\text{tcpi\_segs\_out}}$$

**UDP loss rate (receiver side):**

Server tracks received sequence numbers, detects gaps.

- $N_{\mathrm{sent}}$: Total packets sent (max sequence seen + 1)
- $N_{\mathrm{recv}}$: Total packets received
- $N_{\mathrm{lost}} = N_{\mathrm{sent}} - N_{\mathrm{recv}}$

$$\text{Loss rate} = \frac{N_{\mathrm{lost}}}{N_{\mathrm{sent}}}$$

**Out-of-order tracking:** Server reports `ooo_count` (packets received out of order) per interval. Not included in loss calculation.

#### Upload vs download (reverse mode)

**Upload test (default):**

- Client sends data to server
- Client measures RTT
- Server reports receive-side throughput
- TCP: Server reports retransmit stats (sender-side on upload)
- UDP: Server reports loss stats (receiver-side)

**Download test (`-reverse` flag):**

- Server sends data to client
- Client receives data
- Client measures RTT
- Server reports send-side throughput
- TCP: Server reports retransmit stats (sender-side on download)
- UDP: Client reports loss stats (receiver-side)

**Reverse mode differences:**

- Server establishes data channel back to client
- Control channel flow reversed for sample coordination
- RTT measurement direction unchanged (client always measures)
- Loss/retransmit side changes based on sender/receiver role

### 6.2.3 Parameters

| Parameter | Type | Default | Valid range | CLI flag |
|-----------|------|---------|-------------|----------|
| **Test configuration** |
| Target bandwidth | bits/sec | *required* | > 0 | `-bandwidth` |
| Sample size | bytes | *required* | > 0 | `-sample-bytes` |
| Sample count | int | 10 | > 0 | `-samples` |
| Wait between samples | duration | 0 | ≥ 0 | `-wait` |
| Max test duration | duration | unlimited | ≥ 0 | `-max-duration` |
| **Timing and pacing** |
| Chunk size | bytes | 1200 | > 0 | `-chunk-size` |
| RTT sample rate | samples/sec | 10 | > 0 | `-rtt-rate` |
| Per-sample timeout | duration | 10s | > 0 | (config only) |
| Per-cycle timeout | duration | 30s | > 0 | (config only) |
| **Server-side** |
| Interval duration | duration | 100ms | — | (hardcoded) |
| Receive wait | duration | 100ms (fbmeasure), 500ms (bwprobe) | ≥ 0 | `-recv-wait` |

**Chunk size selection:**

- Small chunks (< 1200 bytes): Finer pacing granularity, higher syscall overhead
- Large chunks (> 64 KB): Lower overhead, coarser pacing, potential buffering
- Default 1200 bytes avoids IP fragmentation on typical MTUs (1500 bytes)
- For high-bandwidth tests (> 500 Mbps), use larger chunks (e.g., 64 KB)

**Receive wait:**

Server continues receiving data after `sample.stop` for configured duration. Accommodates in-flight packets. Increase for high-latency or high-BDP links.

### 6.2.4 Edge cases

#### Timeout handling

**Per-sample timeout:** If a single sample does not complete within `timeout.per_sample`, the sample is aborted and the test continues with remaining samples.

**Per-cycle timeout:** If the entire test cycle does not complete within `timeout.per_cycle`, the test terminates early and reports partial results.

**Partial results:** `SamplesCompleted < SamplesPlanned` indicates timeout. Throughput calculations use only completed samples.

#### Partial sample handling

If sample stops early (due to timeout or error):

- Server reports actual bytes received
- Interval aggregation includes partial last interval
- Throughput calculated over actual duration, not target duration

#### Loss and retransmit measurement quirks

**TCP retransmits:**

- `TCP_INFO` is cumulative (total retransmits since connection start)
- Client computes delta from previous sample to get per-sample retransmits
- Initial sample has no baseline, reports 0 retransmits

**UDP loss:**

- Loss rate depends on server successfully receiving all sequence numbers
- Out-of-order packets are not considered lost
- Gaps in sequence numbers indicate loss
- Large reordering windows can underestimate loss

#### Reverse mode differences

**TCP download:**

- Server sends data, tracks retransmits
- Client receives, measures RTT
- Server reports send-side `tcpi_total_retrans` and `tcpi_segs_out`

**UDP download:**

- Server sends data
- Client receives, detects sequence gaps
- Client reports loss rate (not included in standard output, requires custom integration)

**Data channel establishment:**

In reverse mode, server must connect back to client. Requires:

- Client binds ephemeral port for data channel
- Client communicates port to server via control channel
- Server initiates data connection to client

Firewalls blocking inbound connections to client will cause reverse mode tests to fail.

---

## 6.3 bwprobe RPC protocol

### 6.3.1 Overview

The bwprobe control protocol uses JSON-RPC 2.0 over TCP for session management and sample coordination.

**Transport:** TCP connection (default port 9876)

**Framing:** Newline-delimited JSON messages (`\n`)

**Protocol version:** JSON-RPC 2.0 (specified in `jsonrpc: "2.0"` field)

**Session lifecycle:**

1. Client connects to control port
2. Client sends `session.hello` request
3. Server responds with session ID and capabilities
4. Client runs samples via `sample.start` / `sample.stop`
5. Client sends `session.goodbye` (optional)
6. Client closes connection

### 6.3.2 Formal description

#### Protocol envelope

**JSON-RPC 2.0 request:**

```json
{
  "jsonrpc": "2.0",
  "method": "method.name",
  "params": {...},
  "id": 1
}
```

**JSON-RPC 2.0 response:**

```json
{
  "jsonrpc": "2.0",
  "result": {...},
  "id": 1
}
```

**Error response:**

```json
{
  "jsonrpc": "2.0",
  "error": {
    "code": -32000,
    "message": "error message",
    "data": {...}
  },
  "id": 1
}
```

**Message ID:** Client-provided integer or string. Server echoes in response. Used for request/response matching.

#### Session methods

**session.hello:**

Establish session and exchange capabilities.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "session.hello",
  "params": {
    "client_version": "bwprobe/0.1.0",
    "supported_features": [],
    "capabilities": {
      "max_bandwidth_bps": 10000000000,
      "max_sample_bytes": 1000000000
    }
  },
  "id": 1
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "server_version": "fbmeasure/0.1.0",
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "supported_features": [],
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

**session.goodbye:**

Close session (optional, connection close suffices).

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "session.goodbye",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000"
  },
  "id": 2
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "status": "closed",
    "sessions_cleaned": 1
  },
  "id": 2
}
```

#### Sample methods

**sample.start:**

Begin data collection for upload test.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "sample.start",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "sample_id": 1,
    "network": "tcp"
  },
  "id": 3
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "sample_id": 1,
    "started_at": "2026-01-26T12:34:56.789Z",
    "ready": true
  },
  "id": 3
}
```

**sample.start_reverse:**

Begin data collection for download test.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "sample.start_reverse",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "sample_id": 1,
    "network": "tcp",
    "bandwidth_bps": 50000000,
    "chunk_size": 1200,
    "rtt_ms": 25,
    "sample_bytes": 5000000,
    "data_connection_ready": true
  },
  "id": 3
}
```

**Response:** Same as `sample.start`

**sample.stop:**

Stop data collection and retrieve results.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "sample.stop",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "sample_id": 1
  },
  "id": 4
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "sample_id": 1,
    "total_bytes": 5000000,
    "total_duration": 0.8234,
    "intervals": [
      {"bytes": 500000, "duration_ms": 100, "ooo_count": 0},
      {"bytes": 500000, "duration_ms": 100, "ooo_count": 0},
      ...
    ],
    "first_byte_time": "2026-01-26T12:34:56.789Z",
    "last_byte_time": "2026-01-26T12:34:57.612Z",
    "avg_throughput_bps": 48562000,
    "tcp_send_buffer_bytes": 524288,
    "tcp_retransmits": 3,
    "tcp_segments_sent": 4167
  },
  "id": 4
}
```

**UDP-specific fields:**

```json
{
  "packets_recv": 4265,
  "packets_lost": 2
}
```

### 6.3.3 Parameters

| Parameter | Type | Description | Notes |
|-----------|------|-------------|-------|
| **Session** |
| `session_id` | string | UUID identifying session | Assigned by server |
| `client_version` | string | Client version string | Format: `bwprobe/X.Y.Z` |
| `server_version` | string | Server version string | Format: `fbmeasure/X.Y.Z` |
| `heartbeat_interval_ms` | int | Heartbeat interval (milliseconds) | Default: 30000 (30s) |
| **Sample** |
| `sample_id` | uint32 | Sample identifier | Monotonic, assigned by client |
| `network` | string | Protocol | `"tcp"` or `"udp"` |
| `bandwidth_bps` | float64 | Target bandwidth (bits/sec) | Reverse mode only |
| `chunk_size` | int64 | Chunk size (bytes) | Reverse mode only |
| `rtt_ms` | int64 | Estimated RTT (milliseconds) | Reverse mode only |
| `sample_bytes` | int64 | Payload bytes per sample | Reverse mode only |
| **Results** |
| `total_bytes` | uint64 | Total received bytes | — |
| `total_duration` | float64 | Sample duration (seconds) | — |
| `intervals` | array | Per-interval reports | 100ms intervals |
| `avg_throughput_bps` | float64 | Average throughput (bits/sec) | — |
| `tcp_send_buffer_bytes` | uint64 | TCP send buffer size | TCP only |
| `tcp_retransmits` | uint64 | Total retransmits | TCP only |
| `tcp_segments_sent` | uint64 | Total segments sent | TCP only |
| `packets_recv` | uint64 | Packets received | UDP only |
| `packets_lost` | uint64 | Packets lost | UDP only |

### 6.3.4 Edge cases

#### Error codes

Standard JSON-RPC 2.0 error codes:

| Code | Name | Description |
|------|------|-------------|
| -32700 | Parse error | Invalid JSON |
| -32600 | Invalid request | Malformed JSON-RPC request |
| -32601 | Method not found | Method does not exist |
| -32602 | Invalid params | Invalid method parameters |
| -32603 | Internal error | Server error |

Application-specific error codes (defined in [bwprobe/internal/rpc/protocol.go](../bwprobe/internal/rpc/protocol.go)):

| Code | Name | Description |
|------|------|-------------|
| -32000 | Server error | Generic server error |
| -32001 | Sample already active | `sample.start` called while sample active |
| -32002 | Sample not found | `sample.stop` called for unknown sample |
| -32003 | Sample ID mismatch | Response `sample_id` does not match request |
| -32010 | Invalid session | Session ID not found |
| -32011 | Session expired | Session timed out |

#### Legacy protocol fallback

bwprobe supports legacy text-based protocol for compatibility with older fbmeasure servers.

**Detection:** If JSON-RPC handshake fails, client falls back to legacy protocol.

**Legacy commands:**

- `RESET` → Clear counters
- `START` → Begin counting bytes
- `STOP` → Return `STATS bytes start_ns end_ns throughput_bps`

**Recommendation:** Use JSON-RPC protocol. Legacy protocol lacks session management, capabilities negotiation, and interval reporting.

#### Connection recovery

If control connection drops during test:

- Client should abort test and close data connection
- Server cleans up session after timeout (default: 60s)
- Client must reconnect and establish new session

No automatic reconnection or session resumption is supported.

#### Session expiry

Server expires idle sessions after timeout period (default: 60s since last activity).

**Heartbeat:** Client sends `heartbeat` method at interval specified in `session.hello` response to keep session alive.

**Heartbeat request:**

```json
{
  "jsonrpc": "2.0",
  "method": "heartbeat",
  "params": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "timestamp": 1706270096123456789
  },
  "id": 5
}
```

**Heartbeat response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "timestamp": 1706270096123456789,
    "server_time": 1706270096234567890
  },
  "id": 5
}
```

---

## Cross-reference

| Algorithm | Implementation | Configuration | User guide |
|-----------|---------------|---------------|------------|
| Upstream selection | [internal/upstream/upstream.go](../internal/upstream/upstream.go) | [4.7](configuration-reference.md#47-scoring-section), [4.8](configuration-reference.md#48-switching-section) | [3.1.1](user-guide-fbforward.md#311-overview) |
| Bandwidth measurement | [bwprobe/internal/engine/](../bwprobe/internal/engine/) | [4.6](configuration-reference.md#46-measurement-section) | [3.2](user-guide-bwprobe.md) |
| RPC protocol | [bwprobe/internal/rpc/](../bwprobe/internal/rpc/) | — | [3.2.1](user-guide-bwprobe.md#321-overview) |
