# fbforward

A lightweight userspace port-forwarder that can automatically choose the best upstream based on network quality (RTT, jitter, and packet loss), with manual override and fallback.

## Overview

`fbforward` forwards TCP and UDP traffic to one of several upstreams. It continuously probes each upstream, scores their quality, and selects the best one for new connections. Existing connections stay pinned to their current upstream until they go idle.

## Features

- Userspace port-forwarding for both TCP and UDP
- Multiple upstreams with automatic selection and fallback
- Quality scoring based on RTT, jitter, and loss
- Manual upstream selection
- Monitoring/metrics endpoint and a Web UI for status and control

## How it works

### Startup and upstream discovery

On startup, the program waits a short period to discover available upstreams. If multiple upstreams are valid, one is chosen at random as the initial target.

### Probing and scoring

Each upstream is probed with ICMP to measure average RTT, jitter, and loss rate. Metrics are smoothed over time using an exponential moving average:

```
metric = a * new_metric + (1 - a) * old_metric
```

Scores are derived from three subscores (RTT, jitter, loss) and their weights:

```
score = 100 * (s_rtt ^ w_rtt) * (s_jit ^ w_jit) * (s_los ^ w_los)
subscore = e^(- metric / metric_ref)
```

Where:
- `a` is the smoothing factor (0–1).
- `metric_ref` is the reference value for an ideal network condition.
- Higher scores indicate better upstream quality.

Upstream selection can be automatic (based on score) or manual.

### Forwarding behavior and failover

- New TCP connections are forwarded to the current upstream.
- For UDP, each new source `ip:port` tuple is treated like a connection and forwarded to the current upstream.
- When the upstream changes, only new connections (or new UDP sources) use the new upstream; existing connections remain on the previous one.
- Idle connections are removed after a timeout so they can be re-established on the new upstream.

## Monitoring and control

A monitoring/metrics endpoint and Web UI are provided to inspect upstream metrics, forwarded traffic, and logs, and to manually select the active upstream.
