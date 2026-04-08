# Agent Guide

Primary entry document for LLM agents working with this codebase.

## Repository overview

This repository contains three production codebases plus a local manual test framework:

1. **bwprobe** - Network quality measurement tool that tests at a user-specified bandwidth cap
2. **fbforward** - TCP/UDP port forwarder with fbmeasure-based upstream selection
3. **fbmeasure** - Measurement server used by fbforward for TCP/UDP link metrics
4. **fbcoord** - Cloudflare Worker coordination service for multi-node fbforward deployments
5. **coordlab** - Python-based local lab for manual fbcoord/fbforward testing

The runtime mix is:
- Go binaries for `bwprobe`, `fbforward`, and `fbmeasure`
- TypeScript/Cloudflare Workers code for `fbcoord`
- Python tooling for `scripts/coordlab`

**fbforward summary**: Linux-only Go userspace NAT-style TCP/UDP forwarder. It measures upstreams using fbmeasure targeted probes (RTT, jitter, TCP retransmission rate, UDP loss rate), scores them, and forwards new flows to the best upstream. ICMP probing provides reachability monitoring only. It exposes a token-protected control plane with RPC, Prometheus metrics, WebSocket status streaming with subscription model, and an embedded SPA UI.

**fbcoord summary**: Cloudflare Worker backed by Durable Objects. It coordinates a shared upstream pick across multiple fbforward nodes in the same pool, serves an operator UI/API, enforces auth bans through an auth-guard DO plus Cloudflare KV, and persists the shared coordination token in a token DO.

## Platform requirements

Go data-plane components require:
- Linux only - uses SO_MAX_PACING_RATE, TCP_INFO, raw ICMP sockets
- Go 1.25.5+
- CAP_NET_RAW capability for ICMP operations
- CAP_NET_ADMIN capability for fbforward traffic shaping (optional)

Additional tooling:
- `fbcoord`: Node.js/npm, `wrangler`, Cloudflare Workers/Durable Objects/KV
- `coordlab`: Linux host with unprivileged user namespaces enabled, Python venv at `.venv/`

## Documentation structure

The [doc/](doc/) directory contains structured project documentation:

- [doc/outline.md](doc/outline.md): Complete documentation outline with 8 major sections
- [doc/user-guide-fbforward.md](doc/user-guide-fbforward.md): fbforward operation guide
- [doc/user-guide-fbcoord.md](doc/user-guide-fbcoord.md): fbcoord deployment, operation, and security guidance
- [doc/user-guide-bwprobe.md](doc/user-guide-bwprobe.md): bwprobe operation guide
- [doc/configuration-reference.md](doc/configuration-reference.md): Complete config schema
- [doc/api-reference.md](doc/api-reference.md): bwprobe API and control plane API
- [doc/algorithm-specifications.md](doc/algorithm-specifications.md): Upstream selection, bandwidth measurement, RPC protocol
- [doc/glossary.md](doc/glossary.md): Domain terminology definitions
- [doc/diagrams.md](doc/diagrams.md): Diagram inventory with Mermaid templates
- [doc/style-guide.md](doc/style-guide.md): Writing conventions for documentation
- [doc/logging-guidelines.md](doc/logging-guidelines.md): Structured logging requirements, event naming, OTel alignment, privacy/redaction, and review checks
- [doc/test/testing-guide.md](doc/test/testing-guide.md): Manual and automated test workflows
- [doc/test/coordlab.md](doc/test/coordlab.md): coordlab architecture and operator guide

Legacy documentation has been archived to [doc/archive/2025-01-26-legacy/](doc/archive/2025-01-26-legacy/).

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

## fbcoord architecture

### Worker + Durable Object design

fbcoord is a Cloudflare Worker with four Durable Object bindings:

- `PoolDurableObject`: live node connections, preferences, and coordinated picks per pool
- `RegistryDurableObject`: active pool registry
- `TokenDurableObject`: shared coordination token record and session signing secret
- `AuthGuardDurableObject`: authoritative auth failure counters and active bans

Cloudflare KV (`FBCOORD_AUTH_KV`) is used as a fast replicated ban cache and manual denylist layer, but the auth-guard DO remains the source of truth for short-window enforcement.

### Security model

- Operator login and node auth both use the shared coordination token, but rate limiting is split by scope: `login` vs `node-auth`
- Token verification uses PBKDF2-HMAC-SHA256 with per-record salt plus a Worker-side pepper secret
- Token rotation requires both an authenticated operator session and `current_token`
- Mutating admin endpoints enforce `Origin` when present
- Pool and node WebSocket traffic is validated at runtime and rate-limited per connection

### Public routes

- `/healthz`: worker health check
- `/ws/node?pool=<pool>`: node coordination socket
- `/api/auth/login`, `/api/auth/check`, `/api/auth/logout`
- `/api/pools`, `/api/pools/:pool`
- `/api/token/info`, `/api/token/rotate`

The `fbcoord` admin UI is a separate TypeScript app under `fbcoord/ui/`.

## fbforward architecture

### Three-plane design

fbforward runs as a single process with three main planes:

1. **Data plane**: TCP/UDP listeners forward traffic to selected upstream with per-connection/mapping pinning
2. **Control plane**: HTTP server providing RPC, metrics, WebSocket status stream, embedded web UI
3. **Health/selection plane**: ICMP reachability, fbmeasure measurements, scoring, and switching logic
4. **Shaping plane** (optional): Linux tc-based ingress/egress shaping via netlink

### Startup flow

1. [cmd/fbforward/main.go](cmd/fbforward/main.go) loads config, creates logger, validates Linux, starts Supervisor
2. `Supervisor` loads config and constructs a `Runtime`
3. `Runtime` resolves upstreams, creates `UpstreamManager`, `Metrics`, `StatusStore`, `ControlServer`, starts probes and listeners
4. On shutdown/restart, Runtime stops listeners and closes active flows

### Measurement server requirement

**CRITICAL**: fbforward requires `fbmeasure` running on each upstream host:

- fbmeasure provides TCP/UDP measurement endpoints for targeted probes
- Default port: 9876 (configurable via `upstreams[].measurement.port`)
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

Upstream quality is based on fbmeasure TCP/UDP measurements, with detailed algorithm in [doc/algorithm-specifications.md](doc/algorithm-specifications.md):

- Each upstream runs periodic TCP and UDP probe cycles
- Metrics (RTT, jitter, loss/retrans) are smoothed with EMA
- Score blends TCP/UDP sub-scores using exponential normalization with configurable weights
- Protocol weight blends TCP and UDP scores (configurable, default 0.5 each)
- Static priority and bias adjustments per upstream
- **ICMP probing is reachability-only** and does not affect scores (migration from legacy ICMP scoring)

**Fast-start mode**: At startup, uses lightweight ICMP RTT probes for immediate primary selection, then transitions to full fbmeasure scoring after warmup period.

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
- `cmd/fbmeasure/main.go`: fbmeasure server (required on upstream hosts)

**Core runtime:**
- `internal/app/supervisor.go`: runtime lifecycle (restart, config reload)
- `internal/app/runtime.go`: component wiring, DNS refresh loop, listener/probe startup
- `internal/upstream/upstream.go`: scoring, selection, switching logic, EMA metrics, dial-failure cooldown
- `internal/measure/collector.go`: fbmeasure measurement loop and metric collection
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
- `web/src/auth.ts`: browser-side token entry and persistence
- `web/index.html`: SPA template

**fbcoord:**
- `fbcoord/src/worker.ts`: Worker entrypoint, routing, admin API, auth guard integration
- `fbcoord/src/durable-objects/pool.ts`: node socket lifecycle and coordinated pick logic
- `fbcoord/src/durable-objects/token.ts`: shared token persistence and verification
- `fbcoord/src/durable-objects/auth-guard.ts`: auth failure counters, bans, KV cache integration
- `fbcoord/ui/`: admin UI source
- `fbcoord/wrangler.toml`: Worker bindings, migrations, KV namespace config

**coordlab:**
- `scripts/coordlab/coordlab.py`: CLI entrypoint and orchestration
- `scripts/coordlab/lib/`: topology, process, shaping, link-state, proxy, readiness, and state helpers
- `scripts/coordlab/web/`: Flask dashboard and API for manual lab control

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

### fbcoord

```bash
cd fbcoord
npm install
npm run build
npx wrangler deploy
```

Deployment notes:
- `fbcoord/wrangler.toml` uses `new_sqlite_classes` migrations for free-plan Durable Objects
- `FBCOORD_AUTH_KV` must point at a real Cloudflare KV namespace ID
- required secrets include `FBCOORD_TOKEN` and `FBCOORD_TOKEN_PEPPER`

### fbmeasure

```bash
# Build
make build-fbmeasure

# CRITICAL: fbforward requires fbmeasure running on each upstream host
./build/bin/fbmeasure --port 9876

# Test connectivity
nc -zv <upstream-host> 9876
```

### coordlab

```bash
python3 -m venv .venv
.venv/bin/pip install -r scripts/coordlab/requirements.txt
.venv/bin/python scripts/coordlab/coordlab.py up --skip-build --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
```

## Control plane

**Authentication:**
- RPC and metrics require `Authorization: Bearer <token>`
- WebSocket `/status` uses subprotocol authentication:
  - `fbforward`
  - `fbforward-token.<base64url(token)>`
- The `fbforward` browser UI stores the raw control token in browser `localStorage` as `fbforward_token`
- `Logout` clears that browser-side token; there is no cookie-backed server session in `fbforward`

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

**Details:** See [doc/api-reference.md](doc/api-reference.md) section 5.2

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
   [internal/upstream/upstream.go](internal/upstream/upstream.go). See [doc/algorithm-specifications.md](doc/algorithm-specifications.md) for scoring details.

2. **Control API**: Add new RPC methods in [internal/control/control.go](internal/control/control.go)

3. **Observability**: Extend `Metrics` or `StatusStore` for new telemetry

4. **Data plane**: Add new protocol listeners following TCP/UDP patterns in
   [internal/forwarding/](internal/forwarding/), ensuring flow pinning semantics

5. **Scoring algorithm**: Modify normalization or weights in [internal/upstream/upstream.go](internal/upstream/upstream.go),
   update config schema in [internal/config/config.go](internal/config/config.go)

### fbcoord

When extending functionality:

1. **Admin/API auth**: Update route handling in `fbcoord/src/worker.ts` and keep auth-guard + KV behavior aligned
2. **Pool coordination behavior**: Change node socket or pick logic in `fbcoord/src/durable-objects/pool.ts`
3. **Token/session behavior**: Update `fbcoord/src/durable-objects/token.ts` and related admin UI flows together
4. **Deploy config**: Keep `fbcoord/wrangler.toml` bindings, KV IDs, and DO migrations consistent with Worker code

### coordlab

When extending local manual-test tooling:

1. **Topology/process lifecycle**: Change `scripts/coordlab/coordlab.py` and `scripts/coordlab/lib/netns.py` / `process.py`
2. **Lab controls**: Keep CLI, Flask API, and dashboard behavior aligned across `lib/`, `web/app.py`, and `web/static/`
3. **State model**: Treat `state.json` as the dashboard/CLI contract and update `scripts/coordlab/lib/state.py` deliberately

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
- **Measurement-driven**: ICMP for reachability only; fbmeasure measurements drive all scoring

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

Automated test coverage exists across Go, `fbcoord`, and `coordlab`:

- Go code: `go test ./...`
- `fbcoord`: `npm --prefix fbcoord test`
- `coordlab`: `.venv/bin/python -m unittest discover -s scripts/coordlab/tests -p 'test_*.py'`

Common validation commands:

```bash
go test ./...
npm --prefix fbcoord test
npm --prefix fbcoord run build
.venv/bin/python -m py_compile scripts/coordlab/coordlab.py scripts/coordlab/lib/*.py scripts/coordlab/web/*.py scripts/coordlab/tests/*.py
.venv/bin/python -m unittest discover -s scripts/coordlab/tests -p 'test_*.py'
```

For manual coordination testing, use `coordlab` instead of assuming only ad-hoc local configs exist.
