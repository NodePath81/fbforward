# fbforward Go Codebase Guide

This document summarizes the Go codebase architecture, structure, and the key
components that implement forwarding, probing, control APIs, and metrics.

## Architecture Overview

fbforward runs as a single process with three main planes:

- Data plane: TCP/UDP listeners that forward traffic to the currently selected
  upstream, with per-connection or per-mapping pinning.
- Control plane: HTTP server providing RPC, metrics, WebSocket status stream,
  and the embedded web UI.
- Health/selection plane: ICMP probes, scoring, and upstream switching logic.
- Optional shaping plane: Linux tc-based ingress/egress shaping via netlink.

High-level startup flow:

1. `cmd/fbforward/main.go` loads config path, creates a logger, validates Linux, and starts
   the Supervisor.
2. `Supervisor` loads config and constructs a `Runtime`.
3. `Runtime` resolves upstreams, creates the `UpstreamManager`, `Metrics`,
   `StatusStore`, and `ControlServer`, starts probes, DNS refresh, and listeners.
4. On shutdown or restart, the Runtime stops listeners and closes active flows.

## Code Structure (by file)

- `cmd/fbforward/main.go`: CLI entry, Linux guard, signal handling.
- `internal/app/supervisor.go`: Owns the current Runtime and handles restart lifecycle.
- `internal/app/runtime.go`: Wires together all runtime components and manages goroutines.
- `internal/config/config.go`: YAML config parsing, defaults, and validation.
- `internal/config/bandwidth.go`: bandwidth/size parsing helpers.
- `internal/resolver/resolver.go`: DNS resolution (custom servers or system), plus refresh.
- `internal/probe/probe.go`: ICMP probe loop, windowing, jitter, and score updates.
- `internal/upstream/upstream.go`: Upstream state, EMA metrics, scoring, and switching logic.
- `internal/forwarding/forward_tcp.go`: TCP listener, per-connection proxying, idle handling.
- `internal/forwarding/forward_udp.go`: UDP listener, per-mapping sockets, idle handling.
- `internal/control/control.go`: HTTP API, auth, WebSocket status stream, rate limiting.
- `internal/control/status.go`: Active connection/mapping tracking and WebSocket broadcasting.
- `internal/metrics/metrics.go`: Prometheus metric aggregation and rendering.
- `internal/shaping/shaping_linux.go`, `internal/shaping/shaping_stub.go`: tc shaping helpers and netlink integration.
- `web/handler.go`: Embedded UI handler (serves `web/dist`).
- `internal/util/logger.go`: slog-based logger setup.
- `internal/util/util.go`: small helpers (port formatting, net join).

## Key Structs and Components

### Runtime and Supervisor

- `Supervisor` owns the current `Runtime` instance and handles restart by
  shutting down the current runtime and rebuilding from config.
- `Runtime` is the root of the live system. It holds:
  - `Resolver` for DNS.
  - `UpstreamManager` for scoring and selection.
  - `Metrics` and `StatusStore` for observability.
  - `ControlServer` for RPC/metrics/status/UI.
  - `TrafficShaper` for optional tc-based shaping.
  - TCP/UDP listeners and probe goroutines.

### Upstreams, Scoring, and Switching

- `Upstream` holds static config (`Tag`, `Host`, resolved `IPs`) plus
  live stats and dial-failure tracking.
- `UpstreamManager` owns:
  - `mode`: auto or manual.
  - `activeTag`: current upstream tag.
  - switch tracking: pending confirmation, hold time, thresholds.

Scoring and switching:

- `ProbeLoop` feeds `WindowMetrics` into `UpdateWindow`.
- `applyEMA` smooths RTT/jitter/loss once initialization happens.
- `computeScore` uses weighted exponential subscores for RTT/jitter/loss.
- Auto mode switching respects:
  - `switching.confirm_windows` (consecutive windows).
  - `switching.switch_threshold` (minimum score gap).
  - `switching.min_hold_seconds` (avoid rapid flapping).
  - `switching.failure_loss_threshold` (fast failover on heavy loss).
- Manual mode pins to the selected upstream; unusable or dial-failed upstreams
  reject selection.

### TCP Forwarding

- `TCPListener` listens on configured address/port, limits concurrency with a
  semaphore, and spawns per-connection handlers.
- `tcpConn` manages:
  - bidirectional proxying with buffered copy loops,
  - idle timeout based on activity in either direction,
  - per-connection pinning to the selected upstream.
- Dial behavior uses `dialTCPWithRetry` with timeout and small backoff. Dial
  failures set a short cooldown to avoid rapid retries.
- Metrics and status updates are emitted on data transfer.

### UDP Forwarding

- `UDPListener` reads packets, fans out processing via a worker pool, and
  enforces a per-mapping limit with a semaphore.
- Each client address maps to a `udpMapping` with its own upstream socket.
- Mappings are pinned to the upstream selected at creation time.
- `udpMapping` handles upstream reads, client writes, idle expiration, and
  cleanup of mapping state.

### Probing and Health

- `ProbeLoop` uses raw ICMP sockets (IPv4/IPv6) and emits a probe every
  `probe.interval`.
- `probeWindow` aggregates `window_size` samples to compute loss, RTT average,
  and jitter (mean absolute RTT difference).
- 100% loss marks an upstream unusable; recovery happens automatically when
  a later window is not 100% loss.

### Metrics and Status

- `Metrics` stores per-upstream gauges, active counts, and byte counters.
  - Byte totals are atomic; per-second rates are derived every second.
  - `/metrics` exposes Prometheus text format.
- `StatusStore` tracks active TCP and UDP entries for WebSocket streaming.
  - Emits add/update/remove events to `StatusHub`.
  - `StatusHub` broadcasts to registered WebSocket clients.

### Control Plane

- `ControlServer` serves:
  - `/metrics`: Prometheus metrics (token protected).
  - `/rpc`: JSON RPC-style control methods (token protected + rate-limited).
  - `/status`: WebSocket status stream (token auth).
  - `/`: embedded web UI.
- Auth uses Bearer tokens with constant-time comparison. WebSocket auth also
  supports a token embedded in the subprotocol list.
- A simple per-IP token bucket is used for RPC rate limiting.

### DNS Resolution

- `Resolver` uses system DNS by default or custom servers when configured.
- `Runtime` refreshes hostname-based upstream IPs on a fixed interval and
  updates the active IP if necessary.

### Traffic Shaping (Optional)

- `TrafficShaper` applies HTB + fq_codel shaping per-port using netlink.
- Listener `ingress`/`egress` settings are converted into per-port classes.
- Egress shaping is applied on the device; ingress shaping uses IFB redirect.
- Enabling shaping resets root/ingress qdiscs for the configured device/IFB.

## Concurrency Model

- Goroutines:
  - One probe goroutine per upstream.
  - One goroutine per TCP connection (two proxy loops + idle watcher).
  - One goroutine per UDP mapping (read loop + idle watcher).
  - Worker pool for UDP packet handling.
  - Control server and WebSocket read/write loops.
  - Metrics per-second updater.
- Locks:
  - `UpstreamManager` uses a RW mutex for selection and updates.
  - `StatusStore` uses a mutex for entry tracking and closures.
  - `Metrics` uses a mutex for gauges and counts, and atomics for counters.

## Extension Points

- Switching behavior: adjust `SwitchingConfig` and `UpstreamManager` logic.
- Observability: extend `Metrics` or `StatusStore` for new telemetry.
- Control API: add new RPC methods in `internal/control/control.go`.
- Data plane: add new protocol listeners following TCP/UDP patterns.
