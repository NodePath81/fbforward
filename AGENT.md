# Agent Guide

Primary entry document for LLM agents working with this codebase.

## Repository overview

This repository contains three production codebases:

1. **bwprobe** - Network quality measurement tool that tests at a user-specified bandwidth cap
2. **fbforward** - TCP/UDP port forwarder with fbmeasure-based upstream selection
3. **fbmeasure** - Measurement server used by fbforward for TCP/UDP link metrics

The runtime mix is:
- Go binaries for `bwprobe`, `fbforward`, and `fbmeasure`

**fbforward summary**: Linux-only Go userspace NAT-style TCP/UDP forwarder. It
uses fbmeasure TCP/UDP RTT probes to maintain one unified health state and
selects new flows locally by route, health, RTT, priority, and configuration
order. Optional subsystems include GeoIP database management, persisted IP
connection logging (SQLite), and CIDR/ASN/country firewalling. It exposes a
token-protected control plane with RPC, Prometheus metrics, and WebSocket
status streaming.

## Platform requirements

Go data-plane components require:
- Linux only - uses SO_MAX_PACING_RATE and TCP_INFO
- Go 1.25.5+
- CAP_NET_ADMIN capability for fbforward traffic shaping (optional)
- C toolchain (gcc) for `fbforward` builds because IP-log support currently links the CGO-based `github.com/mattn/go-sqlite3` driver

## Documentation structure

The [doc/](doc/) directory contains structured project documentation:

- [doc/user-guide-fbforward.md](doc/user-guide-fbforward.md): fbforward operation guide
- [doc/user-guide-bwprobe.md](doc/user-guide-bwprobe.md): bwprobe operation guide
- [doc/configuration-reference.md](doc/configuration-reference.md): Complete config schema
- [doc/api-reference.md](doc/api-reference.md): bwprobe API and fbforward control plane API
- [doc/algorithm-specifications.md](doc/algorithm-specifications.md): Upstream selection, bandwidth measurement, RPC protocol
- [doc/glossary.md](doc/glossary.md): Domain terminology definitions
- [doc/diagrams.md](doc/diagrams.md): Diagram inventory with Mermaid templates
- [doc/style-guide.md](doc/style-guide.md): Writing conventions for documentation
- [doc/logging-guidelines.md](doc/logging-guidelines.md): Structured logging requirements, event naming, OTel alignment, privacy/redaction, and review checks
- [doc/test/testing-guide.md](doc/test/testing-guide.md): Manual and automated test workflows

Legacy documentation has been archived to [doc/archive/2025-01-26-legacy/](doc/archive/2025-01-26-legacy/).

## bwprobe architecture

### Two-channel design

bwprobe uses separate control and data channels:

- **Control channel**: JSON-RPC 2.0 over TCP for session setup and sample control
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
2. **Control plane**: HTTP server providing RPC, metrics, and WebSocket status stream
3. **Health/selection plane**: fbmeasure measurements and route-local health/RTT selection
4. **Shaping plane** (optional): Linux tc-based ingress/egress shaping via netlink

### Startup flow

1. [cmd/fbforward/main.go](cmd/fbforward/main.go) loads config, creates logger, validates Linux, starts Supervisor
2. `Supervisor` loads config and constructs a `Runtime`
3. `Runtime` resolves upstreams, creates `UpstreamManager`, `Metrics`, `StatusStore`, `ControlServer`, starts probes and listeners
4. On shutdown/restart, Runtime stops listeners and closes active flows

### Measurement server requirement

fbforward can use `fbmeasure` on each adaptive-route upstream:

- fbmeasure provides TCP/UDP measurement endpoints for targeted probes
- Default port: 9876 (configurable via `upstreams[].measurement.port`)
- Deploy with: `make build-fbmeasure` then `./build/bin/fbmeasure --port 9876`
- Without fbmeasure, adaptive routes expose unknown/stale health and continue using local selection rules.

### Flow pinning model

**Key concept**: Once a flow (TCP connection or UDP 5-tuple mapping) is assigned to an upstream, it stays pinned to that upstream until termination/expiry, even if the primary upstream changes.

- **Primary upstream**: The only upstream that receives **new** flow assignments
- **Pinned flows**: Existing flows continue to their originally assigned upstream
- **TCP lifecycle**: Create mapping on accept, remove on FIN/RST
- **UDP lifecycle**: Create mapping on first packet, remove after idle timeout
- **Admission rule**: New flows always go to current primary; switching only affects future flows

This ensures in-flight connections are not disrupted during upstream switches.

### Health and upstream selection

Adaptive routes filter down/cooldown upstreams, prefer healthy candidates, then compare unified RTT,
priority, current selection, and configuration order. Static routes always use their sole configured
upstream. Existing flows remain pinned when health changes.

**Auto mode** switching:
- Adaptive routes prefer healthy upstreams, then lower RTT, priority, current selection, and configuration order
- Dial failures use a short cooldown; successful probes restore health

**Manual mode**:
- Operator-selected tag must be usable; otherwise rejected

## Key paths

**Binaries:**
- `cmd/fbforward/main.go`: fbforward entrypoint and OS guard
- `bwprobe/cmd/main.go`: bwprobe CLI tool
- `cmd/fbmeasure/main.go`: fbmeasure server (required on upstream hosts)

**Core runtime:**
- `internal/app/supervisor.go`: runtime lifecycle (restart, config reload)
- `internal/app/runtime.go`: component wiring, DNS refresh loop, listener/probe startup
- `internal/upstream/upstream.go`: health state, route-local selection, and dial-failure cooldown
- `internal/measure/collector.go`: fbmeasure measurement loop and metric collection
- `internal/fbmeasure/`: fbmeasure probe client

**Data plane:**
- `internal/forwarding/forward_tcp.go`: TCP forwarding, flow pinning
- `internal/forwarding/forward_udp.go`: UDP forwarding, mapping lifecycle

**GeoIP / IP-log / Firewall:**
- `internal/geoip/manager.go`: MMDB database download, hot-reload, ASN/country lookup
- `internal/iplog/store.go`: SQLite schema, write batching, retention pruning
- `internal/iplog/pipeline.go`: async enrichment and write pipeline
- `internal/firewall/engine.go`: CIDR/ASN/country rule evaluation, fail-open on missing GeoIP

**Control plane:**
- `internal/control/control.go`: RPC (including `GetGeoIPStatus`, `RefreshGeoIP`, `GetIPLogStatus`, `QueryIPLog`), WebSocket subscription protocol, metrics auth
- `internal/control/status.go`: WebSocket hub, status store, message broadcast
- `internal/metrics/metrics.go`: Prometheus metric aggregation (includes IP-log and firewall metrics)

**Config and docs:**
- `internal/config/config.go`: YAML config schema, defaults, validation
- `doc/`: structured documentation (outline, user guides, configuration reference, API reference, algorithm specs)
- `CLAUDE.md`: comprehensive codebase guide for Claude Code

## Build and run

### Monorepo build

```bash
# Build all binaries
make build

# Build one binary
make build-fbforward          # fbforward API-only binary
make build-bwprobe            # bwprobe only
make build-fbmeasure          # fbmeasure only

# Run all tests
make test
# or directly:
go test ./...

# Run tests for specific package
go test ./internal/upstream -v
go test ./bwprobe/internal/network -run TestSenderFraming

# Clean build artifacts
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
# Build from repo root
make build-fbforward

# Build only the Go binary
go build -o build/bin/fbforward ./cmd/fbforward

# With traffic shaping support (optional)
sudo setcap cap_net_admin+ep ./build/bin/fbforward
./build/bin/fbforward --config config.yaml
```

Note:
- `fbforward` builds currently require a working C toolchain because the IP-log SQLite driver is compiled in even when `ip_log.enabled` is `false` at runtime.

### fbmeasure

```bash
# Build
make build-fbmeasure

# CRITICAL: fbforward requires fbmeasure running on each upstream host
./build/bin/fbmeasure --port 9876

# Test connectivity
nc -zv <upstream-host> 9876
```

## Control plane

**Authentication:**
- RPC and metrics require `Authorization: Bearer <token>`
- WebSocket `/status` uses subprotocol authentication:
  - `fbforward`
  - `fbforward-token.<base64url(token)>`
- The control plane is API-only; clients must provide the bearer token directly.

**Data source responsibilities:**
- **WebSocket** (`/status`): connection/queue telemetry via subscription (1s/2s/5s intervals), test history events, session events
  - Client sends `{"type": "subscribe", "interval_ms": 2000}` to start receiving periodic snapshots
  - Separate message types: `connections_snapshot`, `queue_snapshot`, `test_history_event`, `add`, `update`, `remove`, `error`
  - All messages include `schema_version: 1`
- **RPC** (`/rpc`): control commands (`SetUpstream`, `Restart`, `RunMeasurement`, `RefreshGeoIP`), config queries (`GetStatus`, `GetMeasurementConfig`, `GetRuntimeConfig`, `GetScheduleStatus`, `GetGeoIPStatus`, `GetIPLogStatus`, `QueryIPLog`)
  - `GetStatus` returns control-plane state and health fields
  - Numeric telemetry is exposed through Prometheus
- **Prometheus** (`/metrics`): RTT, health, probe counters, queue depth, and active connections

**Key principle:** Single source of truth per data type, no duplication across endpoints.

**Details:** See [doc/api-reference.md](doc/api-reference.md) section 5.2

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

1. **Selection behavior**: Adjust health and route-local selection in
   [internal/upstream/](internal/upstream/), and update the active algorithm documentation.

2. **Control API**: Add new RPC methods in [internal/control/control.go](internal/control/control.go)

3. **Observability**: Extend `Metrics` or `StatusStore` for new telemetry

4. **Data plane**: Add new protocol listeners following TCP/UDP patterns in
   [internal/forwarding/](internal/forwarding/), ensuring flow pinning semantics


## Implementation principles

### bwprobe

- **KISS**: Single process, simple control flow, explicit flags
- **Repeatable**: Fixed number of samples with explicit pacing
- **Low overhead**: Pacing limiter and large write chunks to avoid client bottlenecks
- **Observable**: Continuous RTT sampling and clear progress reporting

### fbforward

- **NAT-style forwarding**: Clients connect to fbforward; upstream sees fbforward as source
- **Flow pinning**: TCP/UDP flows pinned to selected upstream until idle/expired
- **Fast failover**: Immediate switch on health failure or dial failure
- **Auto recovery**: Unusable upstreams recover automatically when probes succeed
- **Measurement-driven**: fbmeasure measurements provide unified health and RTT

## Logging requirements for agents (mandatory)

When adding or modifying code, agents must follow [doc/logging-guidelines.md](doc/logging-guidelines.md).

Required behavior:
- Use structured logging for operational events. Do not add unstructured/free-form operational logs.
- Preserve canonical event naming and key conventions, aligned to OTel-style schema used by this repo.
- Include required correlation fields where context exists (`request.id`, `ws.conn_id`, `flow.id`, `measure.cycle_id`).
- Apply privacy/redaction rules: never log tokens/secrets/raw sensitive payloads.
- Ensure control-plane and flow-mapping audit coverage where applicable.

Acceptance checks for agent changes:
- No unstructured logs introduced.
- Required correlation fields present for new/changed log events where applicable.
- Audit-sensitive paths include access outcome and policy/auth decision logs where applicable.

## Config hints

- Listener ports must be `1..65535`.
- Duplicate `addr:port:protocol` listeners are rejected.
- Hostname upstreams are re-resolved every `30s` (see `internal/app/runtime.go`).

## Testing

Automated test coverage is provided by the Go packages:

- Go code: `go test ./...`

Common validation commands:

```bash
go test ./...
```
