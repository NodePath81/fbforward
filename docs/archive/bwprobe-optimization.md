# BWProbe Algorithm & Optimization Summary

## Overview
BWProbe now runs a **single full-effort TCP stream** with **server-side timing** to improve accuracy. The client measures RTT, computes the minimum test duration using statistical confidence bounds adjusted by loss, warms up the connection, and performs a timed send. The server reports the receive-side start/end time and average throughput.

This design improves accuracy (receive-side timing, single stream), while reducing total time and data via **loss-aware statistics** and **hard caps** on duration and bytes.

## Measurement Flow
1. **RTT Measurement**: Client sends PINGs and uses the median RTT.
2. **Duration Calculation**:
   - Warmup time uses TCP slow-start and loss recovery.
   - Steady-state time uses sample-count bounds:
     ```
     n = ceil((z * sigma / epsilon)^2)
     T_steady = n * sample_interval
     ```
   - `sigma` is adjusted by loss (`p`) to reduce test time on clean paths.
3. **Warmup**: Full-effort send for the computed warmup time.
4. **Measurement**: Full-effort send for the computed test duration.
5. **Server Report**: Server returns bytes received and receive-side start/end times.

## Statistical Optimization
- **Loss-aware variance**: `sigma` increases with loss, but is bounded to avoid runaway durations.
- **Effective confidence**: If caps shorten the test, the client reports the effective CI width.

## Hard Caps (new)
Two caps ensure small tests without giving up accuracy reporting:
- **Max test duration**: default `15s`
- **Max bytes sent**: default `200MB`

If caps shorten the test, the client prints the **effective sample count** and **estimated relative CI** so you know the accuracy trade-off.

## CLI Options
Client options:
- `-bandwidth` (required): bandwidth estimate, e.g. `100Mbps`
- `-loss`: packet loss probability (default `0.001`)
- `-max-duration`: cap test duration (default `15s`)
- `-max-bytes`: cap bytes sent (default `200MB`)
- `-rtt`: manual RTT (optional; 0 = auto)
- `-no-progress`: disable progress bar

Server option:
- `-verbose`: log per-connection data summaries

## Example
```bash
# Server
./bwprobe -mode=server

# Client
./bwprobe -mode=client -target=10.168.168.1 -bandwidth=140Mbps -loss=0.05 \
  -max-duration=15s -max-bytes=200MB
```

## Notes
- The test always uses a **single stream** to avoid overestimating bandwidth.
- The server is the source of truth for timing and throughput.
- If you need longer tests, raise `-max-duration` and/or `-max-bytes`.
