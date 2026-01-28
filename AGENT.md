# Agent Guide

Primary entry document for LLM agents working with this codebase.

## Repository overview

This repository contains two independent Linux-only networking tools plus a measurement server binary:

1. **bwprobe** - Network quality measurement tool that tests at a user-specified bandwidth cap
2. **fbforward** - TCP/UDP port forwarder with bwprobe-based upstream selection
3. **fbmeasure** - Measurement server used by fbforward for TCP/UDP link metrics

Both projects are production-grade Go implementations focused on network quality measurement and management.

**fbforward summary**: Linux-only Go userspace NAT-style TCP/UDP forwarder. It measures upstreams using bwprobe (bandwidth, RTT, jitter, loss/retrans), scores them, and forwards new flows to the best upstream. ICMP probing provides reachability monitoring only. It exposes a token-protected control plane with RPC, Prometheus metrics, WebSocket status streaming with subscription model, and an embedded SPA UI.

## Platform requirements

Both projects require:
- Linux only - uses SO_MAX_PACING_RATE, TCP_INFO, raw ICMP sockets
- Go 1.25.5+
- CAP_NET_RAW capability for ICMP operations
- CAP_NET_ADMIN capability for fbforward traffic shaping (optional)

## Documentation structure

The [docs/](docs/) directory contains structured project documentation:

- [docs/outline.md](docs/outline.md): Complete documentation outline with 8 major sections
- [docs/user-guide-fbforward.md](docs/user-guide-fbforward.md): fbforward operation guide
- [docs/user-guide-bwprobe.md](docs/user-guide-bwprobe.md): bwprobe operation guide
- [docs/configuration-reference.md](docs/configuration-reference.md): Complete config schema
- [docs/api-reference.md](docs/api-reference.md): bwprobe API and control plane API
- [docs/algorithm-specifications.md](docs/algorithm-specifications.md): Upstream selection, bandwidth measurement, RPC protocol
- [docs/glossary.md](docs/glossary.md): Domain terminology definitions
- [docs/diagrams.md](docs/diagrams.md): Diagram inventory with Mermaid templates
- [docs/style-guide.md](docs/style-guide.md): Writing conventions for documentation

Legacy documentation has been archived to [docs/archive/2025-01-26-legacy/](docs/archive/2025-01-26-legacy/).

## bwprobe architecture

### Two-channel design

bwprobe uses separate control and data channels:

- **Control channel**: JSON-RPC 2.0 over TCP for session setup and sample coordination
  - Supports fallback to legacy text protocol for compatibility
  - Control methods: [bwprobe/internal/rpc/protocol.go](bwprobe/internal/rpc/protocol.go)

- **Data channel**: TCP or UDP stream with per-sample framing
  - Frame headers: [bwprobe/internal/protocol/types.go](bwprobe/internal/protocol/types.go)

### Sample-based testing model

Each test run executes a fixed number of samples:

1. Client sends `SAMPLE_START` (or `SAMPLE_START_REVERSE`) via control channel
2. Data transfer runs at target rate until `-sample-bytes` payload bytes sent
3. Client sends `SAMPLE_STOP`
4. Server aggregates data into 100ms intervals (fixed in [bwprobe/internal/rpc/session.go](bwprobe/internal/rpc/session.go))
5. Server returns per-sample report with interval stats and metrics

### Throughput calculation

- **Trimmed mean**: Drop top/bottom 10% of interval rates (the achieved bandwidth)
- **P90/P80**: Percentiles of interval rates
- **Sustained peak**: Max average rate over rolling 1-second window

Logic: [bwprobe/internal/engine/samples.go](bwprobe/internal/engine/samples.go)

### Upload vs download (reverse mode)

- **Upload** (default): Client sends data to server
- **Download** (`-reverse`): Server sends data to client, but client still drives control
  - TCP retransmit stats are reported by the server (it is the sender)
  - Requires separate reverse data connection handling in [bwprobe/internal/transport/](bwprobe/internal/transport/)

### Package responsibilities

- [bwprobe/cmd/main.go](bwprobe/cmd/main.go): CLI entry point, flag parsing, output formatting
- [bwprobe/pkg/](bwprobe/pkg/): Public Go API for embedding (import path `github.com/NodePath81/fbforward/bwprobe/pkg`)
- [bwprobe/internal/engine/](bwprobe/internal/engine/): Test orchestration, sample loop, metric computation
- [bwprobe/internal/rpc/](bwprobe/internal/rpc/): JSON-RPC client/server, session management
- [bwprobe/internal/server/](bwprobe/internal/server/): Control listener, data stream routing
- [bwprobe/internal/transport/](bwprobe/internal/transport/): Reverse-mode connection handling
- [bwprobe/internal/network/](bwprobe/internal/network/): TCP/UDP senders with pacing
- [bwprobe/internal/metrics/](bwprobe/internal/metrics/): RTT sampler, TCP_INFO reader, UDP loss tracking
- [bwprobe/internal/protocol/](bwprobe/internal/protocol/): Data frame headers and constants

## fbforward architecture

### Three-plane design

fbforward runs as a single process with three main planes:

1. **Data plane**: TCP/UDP listeners forward traffic to selected upstream with per-connection/mapping pinning
2. **Control plane**: HTTP server providing RPC, metrics, WebSocket status stream, embedded web UI
3. **Health/selection plane**: ICMP reachability, bwprobe measurements, scoring, and switching logic
4. **Shaping plane** (optional): Linux tc-based ingress/egress shaping via netlink

### Startup flow

1. [cmd/fbforward/main.go](cmd/fbforward/main.go) loads config, creates logger, validates Linux, starts Supervisor
2. `Supervisor` loads config and constructs a `Runtime`
3. `Runtime` resolves upstreams, creates `UpstreamManager`, `Metrics`, `StatusStore`, `ControlServer`, starts probes and listeners
4. On shutdown/restart, Runtime stops listeners and closes active flows

### Measurement server requirement

**CRITICAL**: fbforward requires `fbmeasure` running on each upstream host:

- fbmeasure provides TCP/UDP measurement endpoints for bwprobe tests
- Default port: 9876 (configurable via `measure_port` in upstream config)
- Deploy with: `make build-fbmeasure` then `./build/bin/fbmeasure --port 9876`
- Without fbmeasure, fbforward falls back to ICMP-only reachability (degraded mode)

### Flow pinning model

**Key concept**: Once a flow (TCP connection or UDP 5-tuple mapping) is assigned to an upstream, it stays pinned to that upstream until termination/expiry, even if the primary upstream changes.

- **Primary upstream**: The only upstream that receives **new** flow assignments
- **Pinned flows**: Existing flows continue to their originally assigned upstream
- **TCP lifecycle**: Create mapping on accept, remove on FIN/RST
- **UDP lifecycle**: Create mapping on first packet, remove after idle timeout
- **Admission rule**: New flows always go to current primary; switching only affects future flows

This ensures in-flight connections are not disrupted during upstream switches.

### Scoring and upstream selection

Upstream quality is based on bwprobe TCP/UDP measurements, with detailed algorithm in [docs/algorithm-specifications.md](docs/algorithm-specifications.md):

- Each upstream runs periodic bwprobe upload/download tests over TCP/UDP
- Metrics (bandwidth, RTT/jitter, loss/retrans) are smoothed with EMA
- Score blends TCP/UDP sub-scores using exponential normalization with configurable weights
- Protocol weight blends TCP and UDP scores (configurable, default 0.5 each)
- Utilization penalty applied when actual traffic exceeds configured link capacity
- Utilization telemetry is computed on-demand from recent traffic samples using the last measured bandwidth baseline
- Static priority and bias adjustments per upstream
- **ICMP probing is reachability-only** and does not affect scores (migration from legacy ICMP scoring)

**Fast-start mode**: At startup, uses lightweight ICMP RTT probes for immediate primary selection, then transitions to full bwprobe scoring after warmup period.

**Auto mode** switching:
- Requires confirmation duration (time-based), score delta threshold, minimum hold time
- Fast failover on high loss/retrans windows or consecutive dial failures
- Unusable upstreams (100% loss, dial failures) automatically recover when probes succeed

**Manual mode**:
- Operator-selected tag must be usable; otherwise rejected

## Key paths

**Binaries:**
- `cmd/fbforward/main.go`: fbforward entrypoint and OS guard
- `bwprobe/cmd/main.go`: bwprobe CLI tool
- `bwprobe/cmd/fbmeasure/main.go`: fbmeasure server (required on upstream hosts)

**Core runtime:**
- `internal/app/supervisor.go`: runtime lifecycle (restart, config reload)
- `internal/app/runtime.go`: component wiring, DNS refresh loop, listener/probe startup
- `internal/upstream/upstream.go`: scoring, selection, switching logic, EMA metrics, dial-failure cooldown
- `internal/measure/collector.go`: bwprobe measurement loop and metric collection
- `internal/measure/fast_start.go`: fast-start TCP RTT selection
- `internal/probe/probe.go`: ICMP reachability probing (no scoring)

**Data plane:**
- `internal/forwarding/forward_tcp.go`: TCP forwarding, flow pinning
- `internal/forwarding/forward_udp.go`: UDP forwarding, mapping lifecycle

**Control plane:**
- `internal/control/control.go`: RPC, WebSocket subscription protocol, metrics auth
- `internal/control/status.go`: WebSocket hub, status store, message broadcast
- `internal/metrics/metrics.go`: Prometheus metric aggregation

**Web UI:**
- `web/handler.go`: embedded UI asset handler
- `web/src/main.ts`: UI application logic
- `web/index.html`: SPA template

**Config and docs:**
- `internal/config/config.go`: YAML config schema, defaults, validation
- `docs/`: structured documentation (outline, user guides, configuration reference, API reference, algorithm specs)
- `CLAUDE.md`: comprehensive codebase guide for Claude Code

## Build and run

### Monorepo build

```bash
# Build all binaries
make build

# Build one binary
make build-fbforward          # fbforward with web UI
make build-bwprobe            # bwprobe only
make build-fbmeasure          # fbmeasure only

# Run all tests
make test
# or directly:
go test ./...

# Run tests for specific package
go test ./internal/upstream -v
go test ./bwprobe/internal/network -run TestSenderFraming

# Clean build artifacts and web UI
make clean
```

### bwprobe

```bash
# Build
make build-bwprobe
# or directly:
go build -o build/bin/bwprobe ./bwprobe/cmd

# Run with specific flags
./build/bin/bwprobe -server localhost:9876 -bandwidth 10m -samples 5
```

### fbforward

```bash
# Build from repo root (builds UI + Go binary)
make build-fbforward

# Build only Go binary (uses existing web/dist)
go build -o build/bin/fbforward ./cmd/fbforward

# Run with capabilities (required for ICMP probing)
sudo setcap cap_net_raw+ep ./build/bin/fbforward
./build/bin/fbforward --config config.yaml

# With traffic shaping support (optional)
sudo setcap cap_net_raw,cap_net_admin+ep ./build/bin/fbforward
```

### fbmeasure

```bash
# Build
make build-fbmeasure

# CRITICAL: fbforward requires fbmeasure running on each upstream host
./build/bin/fbmeasure --port 9876

# Test connectivity
./build/bin/bwprobe -server <upstream-host>:9876 -bandwidth 10m
```

## Control plane

**Authentication:**
- RPC and metrics require `Authorization: Bearer <token>`
- WebSocket `/status` uses subprotocol authentication:
  - `fbforward`
  - `fbforward-token.<base64url(token)>`

**Data source responsibilities:**
- **WebSocket** (`/status`): connection/queue telemetry via subscription (1s/2s/5s intervals), test history events, session events
  - Client sends `{"type": "subscribe", "interval_ms": 2000}` to start receiving periodic snapshots
  - Separate message types: `connections_snapshot`, `queue_snapshot`, `test_history_event`, `add`, `update`, `remove`, `error`
  - All messages include `schema_version: 1`
- **RPC** (`/rpc`): control commands (`SetUpstream`, `Restart`, `RunMeasurement`), config queries (`GetStatus`, `GetMeasurementConfig`, `GetRuntimeConfig`, `GetScheduleStatus`)
  - `GetStatus` returns only non-metric fields (tag, host, IPs, active, usable, reachable)
  - No numeric metrics (bandwidth, RTT, scores) in RPC responses
- **Prometheus** (`/metrics`): all numeric metrics (bandwidth, RTT, jitter, loss/retrans rates, scores, utilization, queue depth, active connections)

**Key principle:** Single source of truth per data type, no duplication across endpoints.

**Details:** See [docs/api-reference.md](docs/api-reference.md) section 5.2

## UI assets

**Structure:**
- Source: `web/src/` (TypeScript)
- Built output: `web/dist/` (embedded via `web/handler.go`)
- Build tool: Vite

**Development workflow:**
```bash
cd web
npm install        # First time only
npm run dev        # Dev server with hot reload on http://localhost:5173
npm run build      # Production build to web/dist/
```

**Production build:**
```bash
make build-fbforward  # Builds web UI and Go binary together
```

The built assets in `web/dist/` are embedded into the Go binary via `web/handler.go`.

## Key integration points

### bwprobe

When modifying behavior across control/data boundary:

1. **Add new control method**: Update [bwprobe/internal/rpc/protocol.go](bwprobe/internal/rpc/protocol.go),
   [bwprobe/internal/rpc/server.go](bwprobe/internal/rpc/server.go), [bwprobe/internal/rpc/client.go](bwprobe/internal/rpc/client.go),
   [bwprobe/internal/engine/control.go](bwprobe/internal/engine/control.go)

2. **Change throughput calculation**: Update [bwprobe/internal/engine/samples.go](bwprobe/internal/engine/samples.go)
   and ensure interval duration in [bwprobe/internal/rpc/session.go](bwprobe/internal/rpc/session.go) matches

3. **Modify pacing or framing**: Update [bwprobe/internal/network/sender.go](bwprobe/internal/network/sender.go) and
   verify chunk size logic aligns with headers in [bwprobe/internal/protocol/types.go](bwprobe/internal/protocol/types.go)

4. **Extend server metrics**: Update session manager in [bwprobe/internal/rpc/session.go](bwprobe/internal/rpc/session.go)
   and metric collectors in [bwprobe/internal/metrics/](bwprobe/internal/metrics/)

### fbforward

When extending functionality:

1. **Switching behavior**: Adjust `SwitchingConfig` and `UpstreamManager` logic in
   [internal/upstream/upstream.go](internal/upstream/upstream.go). See [docs/algorithm-specifications.md](docs/algorithm-specifications.md) for scoring details.

2. **Control API**: Add new RPC methods in [internal/control/control.go](internal/control/control.go)

3. **Observability**: Extend `Metrics` or `StatusStore` for new telemetry

4. **Data plane**: Add new protocol listeners following TCP/UDP patterns in
   [internal/forwarding/](internal/forwarding/), ensuring flow pinning semantics

5. **Scoring algorithm**: Modify normalization or weights in [internal/upstream/upstream.go](internal/upstream/upstream.go),
   update config schema in [internal/config/config.go](internal/config/config.go)

## Implementation principles

### bwprobe

- **KISS**: Single process, simple control flow, explicit flags
- **Repeatable**: Fixed number of samples with explicit pacing
- **Low overhead**: Pacing limiter and large write chunks to avoid client bottlenecks
- **Observable**: Continuous RTT sampling and clear progress reporting

### fbforward

- **NAT-style forwarding**: Clients connect to fbforward; upstream sees fbforward as source
- **Flow pinning**: TCP/UDP flows pinned to selected upstream until idle/expired
- **Fast failover**: Immediate switch on high loss windows or dial failures
- **Auto recovery**: Unusable upstreams recover automatically when probes succeed
- **Measurement-driven**: ICMP for reachability only; bwprobe measurements drive all scoring

## Config hints

- Listener ports must be `1..65535`.
- Duplicate `addr:port:protocol` listeners are rejected.
- Hostname upstreams are re-resolved every `30s` (see `internal/app/runtime.go`).

## Testing

No automated tests yet. Prefer `go test ./...` and a quick manual run with a local config.
