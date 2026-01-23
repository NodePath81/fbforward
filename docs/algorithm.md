# Upstream Selection Algorithm: Implementation Specification (Primary Admission + Flow Pinning)

This specification selects a **primary upstream** for **new flows** while guaranteeing **flow pinning**: once a flow is assigned to an upstream, it continues to use that upstream until the flow ends (TCP close) or expires (UDP idle timeout), even if the primary upstream changes.

---

## Deprecation Notice (ICMP Scoring)

ICMP probing is retained **for reachability only**. It no longer contributes to scoring or switching decisions. All selection scoring is based on bwprobe-derived TCP/UDP link metrics. ICMP fallback is used only when measurement data is unavailable.

---

## 0. Flow Pinning and Admission Model

### 0.1 Definitions

* **Primary upstream**: the only upstream that receives **new** flow assignments.
* **Pinned flow**: a flow that has already been assigned to an upstream; its traffic continues to be forwarded to the same upstream until termination/expiry.
* **Flow key**: `(proto, srcIP, srcPort, dstIP, dstPort)` (TCP) and the same 5-tuple for UDP.

### 0.2 Flow Table

Maintain a flow table:

* `flow_table[flow_key] = upstream_id`

Forwarding rule:

1. If `flow_key` exists in `flow_table`, forward to the mapped upstream.
2. Otherwise (new flow), assign to the **current primary upstream**, insert mapping, then forward.

### 0.3 Lifecycle

* **TCP**: create mapping at connection creation; remove on FIN/RST or connection teardown.
* **UDP**: create mapping on first packet; remove after `T_udp_idle` without packets.

### 0.4 Utilization Counters

Traffic counters `τ_up` / `τ_dn` used for utilization must be **per upstream**, derived from actual forwarded bytes over the last window `T0`, including traffic from pinned flows.

---

## 1. Fast Start (Initial Selection)

At startup, select a primary upstream immediately using lightweight probes without waiting for full measurements.

**Initial Score Formula:**

$$S_{\mathrm{init},i} = \mathbf{1}*{\mathrm{reachable},i} \cdot \left( \frac{100}{1 + R*{\mathrm{probe},i} / R_0} + P_i \right)$$

**Parameters:**

| Symbol                              | Description                               | Default  |
| ----------------------------------- | ----------------------------------------- | -------- |
| $\mathbf{1}_{\mathrm{reachable},i}$ | 1 if probe response received, 0 otherwise | —        |
| $R_{\mathrm{probe},i}$              | Probe RTT (ms)                            | Measured |
| $R_0$                               | RTT normalization constant                | 50 ms    |
| $P_i$                               | Static priority bonus                     | 0        |
| $T_{\mathrm{probe}}$                | Probe timeout                             | 500 ms   |

**Primary Selection:** $i^* = \arg\max_{i} S_{\mathrm{init},i}$

**Transition to Full Scoring:** After $T_{\mathrm{warmup}}$ (default: 15s), switch to full score.
During warmup, use relaxed switching: $\delta_{\mathrm{switch}}' = \delta_{\mathrm{switch}} / 2$, $T_{\mathrm{hold}}' = 0$.

---

## 2. Full Upstream Scoring

### 2.1 Sub-Scores

Each metric is normalized to $[\varepsilon, 1]$ where $\varepsilon = 0.001$.

**Bandwidth (higher is better):**

$$s_{B_{\mathrm{up}}} = \max\left(1 - \exp\left(-B_{\mathrm{up}} / B_{\mathrm{up}}^{\mathrm{ref}}\right), \varepsilon\right)$$
$$s_{B_{\mathrm{dn}}} = \max\left(1 - \exp\left(-B_{\mathrm{dn}} / B_{\mathrm{dn}}^{\mathrm{ref}}\right), \varepsilon\right)$$

**RTT and Jitter (lower is better):**

$$s_R = \max\left(\exp\left(-R / R^{\mathrm{ref}}\right), \varepsilon\right)$$
$$s_J = \max\left(\exp\left(-J / J^{\mathrm{ref}}\right), \varepsilon\right)$$

**Retransmission Rate (TCP) and Loss Rate (UDP):**

$$s_\rho = \max\left(\exp\left(-\rho / \rho^{\mathrm{ref}}\right), \varepsilon\right)$$
$$s_L = \max\left(\exp\left(-L / L^{\mathrm{ref}}\right), \varepsilon\right)$$

**Reference Parameters:**

| Parameter                        | Description                    | Default |
| -------------------------------- | ------------------------------ | ------- |
| $B_{\mathrm{up}}^{\mathrm{ref}}$ | Target uplink bandwidth        | 10 Mbps |
| $B_{\mathrm{dn}}^{\mathrm{ref}}$ | Target downlink bandwidth      | 50 Mbps |
| $R^{\mathrm{ref}}$               | Target RTT                     | 50 ms   |
| $J^{\mathrm{ref}}$               | Target jitter                  | 10 ms   |
| $\rho^{\mathrm{ref}}$            | Target TCP retransmission rate | 0.01    |
| $L^{\mathrm{ref}}$               | Target UDP loss rate           | 0.01    |

### 2.2 Base Quality Score

**TCP:**

$$Q_{\mathrm{tcp}} = 100 \cdot s_{B_{\mathrm{up}}}^{w_{B_{\mathrm{up}}}} \cdot s_{B_{\mathrm{dn}}}^{w_{B_{\mathrm{dn}}}} \cdot s_R^{w_R} \cdot s_J^{w_J} \cdot s_\rho^{w_\rho}$$

**UDP:**

$$Q_{\mathrm{udp}} = 100 \cdot s_{B_{\mathrm{up}}}^{w_{B_{\mathrm{up}}}} \cdot s_{B_{\mathrm{dn}}}^{w_{B_{\mathrm{dn}}}} \cdot s_R^{w_R} \cdot s_J^{w_J} \cdot s_L^{w_L}$$

**Weights (must sum to 1):**

| Weight                | TCP  | UDP  |
| --------------------- | ---- | ---- |
| $w_{B_{\mathrm{up}}}$ | 0.15 | 0.10 |
| $w_{B_{\mathrm{dn}}}$ | 0.25 | 0.30 |
| $w_R$                 | 0.25 | 0.15 |
| $w_J$                 | 0.10 | 0.30 |
| $w_\rho$ / $w_L$      | 0.25 | 0.15 |

### 2.3 Utilization Penalty (Soft Constraint)

Utilization is treated as a soft constraint by applying a multiplicative penalty.

$$M = m_{\min} + (1 - m_{\min}) \cdot \exp\left(-\left(u / u^0\right)^p\right)$$

where $u = \max(u_{\mathrm{up}}, u_{\mathrm{dn}})$ and utilization is computed **per upstream**:

$$u_{\mathrm{up}} = \frac{\tau_{\mathrm{up}} \cdot 8}{B_{\mathrm{up}}^{\mathrm{emp}} \cdot 10^6 \cdot T_0}, \quad u_{\mathrm{dn}} = \frac{\tau_{\mathrm{dn}} \cdot 8}{B_{\mathrm{dn}}^{\mathrm{emp}} \cdot 10^6 \cdot T_0}$$

**Parameters:** $m_{\min} = 0.3$, $u^0 = 0.7$, $p = 2$, $T_0 = 2$s.

Utilization telemetry is computed on-demand from the latest traffic window using the last measured bandwidth baseline, so UI/metrics reflect near-real-time usage at the polling cadence.

### 2.4 User Bias

$$M_\beta = \mathrm{clamp}\left(\exp(\kappa \cdot \beta), 0.67, 1.5\right)$$

where $\beta \in [-1, 1]$ is user preference and $\kappa = \ln 2$.

### 2.5 Final Scores

**Per-protocol:**

$$S_{\mathrm{tcp}} = \mathrm{clamp}(Q_{\mathrm{tcp}} \cdot M \cdot M_\beta, 0, 100)$$
$$S_{\mathrm{udp}} = \mathrm{clamp}(Q_{\mathrm{udp}} \cdot M \cdot M_\beta, 0, 100)$$

**Overall upstream score:**

$$S_{\mathrm{overall}} = \omega_{\mathrm{tcp}} \cdot S_{\mathrm{tcp}} + \omega_{\mathrm{udp}} \cdot S_{\mathrm{udp}}$$

where $\omega_{\mathrm{tcp}} = \omega_{\mathrm{udp}} = 0.5$.

---

## 3. Primary Upstream Switching (New-Flow Admission Only)

### 3.1 Key Invariant

**Switching the primary affects only new flow assignments.**

* Existing flows remain pinned to their previously assigned upstream via `flow_table`.
* After a primary switch, flows already mapped to the old primary continue to use it until they end/expire.

### 3.2 Decision Timing

The score of the primary upstream (and candidates) is computed periodically. Once scores are computed for the cycle, a decision is made (including hysteresis checks). Any required calculations/state updates (confirm timers, last-switch timestamps, primary pointer update) are performed **after** score computation.

### 3.3 State Machine (Primary Admission)

```
Legend:
- PRIMARY(i): upstream i is the primary for NEW flows
- Note: existing flows remain pinned to their assigned upstream regardless of PRIMARY()

┌──────────────────────────────────────────────────────────────┐
│                           PRIMARY(i)                         │
│                 (new flows -> i; old flows pinned)           │
└───────────────┬───────────────────────────────┬──────────────┘
                │                               │
      Score gap detected (candidate j)          Failure detected on i
      (S_j - S_i > δ_switch)                   (L_i > L_fail or ρ_i > ρ_fail)
                │                               │
                ▼                               ▼
┌──────────────────────────────────┐   ┌──────────────────────────────────┐
│            CONFIRMING(j)         │   │      IMMEDIATE PRIMARY SWITCH     │
│   (require sustained gap for     │   │ (bypass confirm/hold for NEW      │
│    T_confirm; candidate fixed)   │   │  flows; existing flows pinned)    │
└───────────────┬──────────────────┘   └──────────────────┬───────────────┘
                │                                         │
   Gap sustained + hold satisfied                          │
                │                                         │
                ▼                                         ▼
┌──────────────────────────────────────────────────────────────┐
│                           PRIMARY(j)                         │
│                 (new flows -> j; old flows pinned)           │
└──────────────────────────────────────────────────────────────┘
```

### 3.4 Switching Conditions

Define:

* `current` = current primary upstream index.
* `best` = upstream with maximum score among eligible candidates.

**Normal switch (all must be true):**

$$S_{\mathrm{overall},\mathrm{best}} - S_{\mathrm{overall},\mathrm{current}} > \delta_{\mathrm{switch}}$$

* Gap sustained for $T_{\mathrm{confirm}}$ with a **fixed candidate** `best`.

$$t - t_{\mathrm{last_switch}} > T_{\mathrm{hold}}$$

**Failure-triggered switch (bypass hysteresis for NEW flows):**

$$L_{\mathrm{current}} > L_{\mathrm{fail}} \ \text{or} \ \rho_{\mathrm{current}} > \rho_{\mathrm{fail}}$$

Action:

* Update the primary pointer to the best available upstream that satisfies basic eligibility (reachable, not failed).
* Existing flows pinned to the old primary continue until completion.

### 3.5 Switching Parameters

| Parameter                  | Description                                             | Default |
| -------------------------- | ------------------------------------------------------- | ------- |
| $\delta_{\mathrm{switch}}$ | Minimum score advantage to consider primary switch      | 5       |
| $T_{\mathrm{confirm}}$     | Duration score gap must persist (candidate fixed)       | 15s     |
| $T_{\mathrm{hold}}$        | Minimum time between primary switches                   | 30s     |
| $L_{\mathrm{fail}}$        | Loss rate triggering immediate primary switch           | 0.2     |
| $\rho_{\mathrm{fail}}$     | Retransmission rate triggering immediate primary switch | 0.2     |

### 3.6 Candidate Eligibility (Recommended)

To avoid switching to unknown/stale or unreachable upstreams:

* Require recent reachability (probe success within a short window) for candidates.
* If metric age exceeds $T_{\mathrm{stale}}$ (120s), apply fallback values as specified, and optionally cap the resulting score for selection until refreshed.

---

## 4. Metric Smoothing

Apply EMA to all measurements before scoring:

$$\tilde{X} = \alpha \cdot X_{\mathrm{new}} + (1 - \alpha) \cdot \tilde{X}_{\mathrm{prev}}$$

where $\alpha = 0.2$.

**Staleness handling:** If metric age exceeds $T_{\mathrm{stale}}$ (120s), use fallback values: $0.5 \times \text{reference}$ for bandwidth, $2 \times \text{reference}$ for RTT/jitter/loss.

---

## 5. Implementation Checklist

### 5.1 State per upstream

* Smoothed metrics (bandwidth up/down, RTT, jitter, loss/retrans)
* Last measurement timestamp(s)
* Traffic counters for utilization: `τ_up`, `τ_dn` computed over `T0` per upstream
* Current scores: `S_tcp`, `S_udp`, `S_overall`

### 5.2 Global state

* `primary_upstream_index`
* `t_last_switch`
* Confirm state: `confirm_candidate`, `t_confirm_start`
* Warmup flag and timing

### 5.3 Flow state

* `flow_table` mapping flow keys to upstream ids
* TCP teardown detection and UDP idle expiry (`T_udp_idle`)

### 5.4 Update cycle (every $T_0$)

1. Collect measurements per upstream.
2. Apply EMA smoothing.
3. Compute sub-scores → penalties → final scores (all upstreams).
4. Evaluate switching conditions using the computed scores.
5. If switching is triggered, update `primary_upstream_index`, `t_last_switch`, and confirm state.
6. Forwarding uses `flow_table` for existing flows; only new flows use the current primary.
