# Bandwidth Probing Algorithm

## Goals
- Maximize accuracy while minimizing time and data usage.
- Use a **single TCP stream** with **server-side timing** to avoid client-side bias.
- Respect hard caps: `-max-duration` and `-max-bytes`.

## Overview
The client:
1. Measures RTT (median of `FixedRttSamples` PINGs).
2. Computes warmup + steady-state durations using a TCP model and statistical bounds.
3. Warms up the TCP connection (full-effort send).
4. Runs a full-effort timed test.
5. Requests server-side stats (bytes received, start/end timestamps, throughput).

The server:
- Tracks bytes only between `START` and `STOP` commands on a control connection.
- Reports receive-side duration and throughput.

## TCP Model (Warmup)
Warmup models slow start and loss recovery:

```
BDP = B * RTT (bytes)
CWND0 = 10 * MSS
n_ss = ceil(log2(BDP / CWND0))
T_slowstart = n_ss * RTT
loss_events = p * (BDP / MSS)
T_loss = loss_events * 2 * RTT
T_warmup = T_slowstart + T_loss
```

This ensures the test samples steady-state behavior rather than ramp-up throughput.

## Statistical Duration (Steady State)
We treat throughput as a random variable with coefficient of variation `sigma`. The
number of independent samples needed for relative error `epsilon` at confidence
level `z` is:

```
n = ceil((z * sigma / epsilon)^2)
T_steady = n * sample_interval
```

### Loss-Aware Variance
Loss increases throughput variance, so sigma is adjusted by loss:

```
sigma_eff = clamp( sigma_base * (1 + sqrt(4p)), 0.08, 0.25 )
```

This reduces test duration on clean paths while avoiding under-sampling on lossy links.

## Hard Caps: Time and Bytes
We enforce both `-max-duration` and `-max-bytes` by allocating a **budget**.

```
max_time_sec  = max_duration
max_bytes_sec = max_bytes / (B/8)
TOTAL_BUDGET  = min(max_time_sec, max_bytes_sec)
```

Warmup is capped to preserve test time:

```
warmup_cap = min( TOTAL_BUDGET * 0.25, TOTAL_BUDGET - min_sample )
T_warmup = min(T_warmup_model, warmup_cap)
```

Steady state duration is then:

```
T_steady = min(T_steady_ideal, TOTAL_BUDGET - T_warmup)
```

If caps shorten the test, we compute the **effective error bound**:

```
epsilon_eff = z * sigma_eff / sqrt(n_eff)
```

The client prints `n_eff` and `epsilon_eff` to make the tradeoff explicit.

## Control Connection Protocol
The client uses a separate TCP connection for coordination:

- `RESET` → clear counters
- `START` → begin counting receive bytes
- `STOP`  → return `STATS bytes start_ns end_ns throughput_bps`

This keeps the data channel unidirectional and avoids timing bias from client clocks.

## Accuracy vs. Efficiency
- **Accuracy** improves with larger `T_steady` and smaller `epsilon_eff`.
- **Efficiency** improves by lowering sigma on clean paths and enforcing caps.
- If you need tighter error bounds, increase `-max-duration` or `-max-bytes`.

## Defaults
- `-max-duration`: 15s
- `-max-bytes`: 200MB
- `-loss`: 0.001
- `sample_interval`: 1s

## Example
```bash
./bwprobe -mode=client -target=10.168.168.1 -bandwidth=140Mbps -loss=0.05 \
  -max-duration=15s -max-bytes=200MB
```
