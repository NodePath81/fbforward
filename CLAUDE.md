# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Repository Overview

This repository contains two independent Linux-only networking tools plus a
measurement server binary:

1. **bwprobe** - Network quality measurement tool that tests at a user-specified bandwidth cap
2. **fbforward** - TCP/UDP port forwarder with bwprobe-based upstream selection
3. **fbmeasure** - Measurement server used by fbforward for TCP/UDP link metrics

Both projects are production-grade Go implementations focused on network
quality measurement and management.

## Build and Test Commands

### Monorepo

```bash
# Build all binaries
make build

# Build one binary
make build-fbforward
make build-bwprobe
make build-fbmeasure

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

# Build web UI separately
cd web
npm install
npm run build

# Development mode for web UI (hot reload with Vite)
cd web
npm install  # First time only
npm run dev  # Opens dev server on http://localhost:5173
# Note: UI is TypeScript-based, built with Vite, and embedded via web/handler.go

# Run with capabilities (required for ICMP probing)
sudo setcap cap_net_raw+ep ./build/bin/fbforward
./build/bin/fbforward --config config.yaml

# Build measurement server (required for fbforward operation)
make build-fbmeasure
go build -o build/bin/fbmeasure ./bwprobe/cmd/fbmeasure
```

### fbmeasure

```bash
# Build
make build-fbmeasure

# Run on upstream hosts
./build/bin/fbmeasure --port 9876

# Test connectivity
./build/bin/bwprobe -server <upstream-host>:9876 -bandwidth 10m
```

## Platform Requirements

Both projects require:
- Linux only - uses SO_MAX_PACING_RATE, TCP_INFO, raw ICMP sockets
- Go 1.25.5+
- CAP_NET_RAW capability for ICMP operations
- CAP_NET_ADMIN capability for fbforward traffic shaping (optional)

## bwprobe Architecture

### Two-Channel Design

bwprobe uses separate control and data channels:

- **Control channel**: JSON-RPC 2.0 over TCP for session setup and sample coordination
  - Supports fallback to legacy text protocol for compatibility
  - Control methods: [bwprobe/internal/rpc/protocol.go](bwprobe/internal/rpc/protocol.go)

- **Data channel**: TCP or UDP stream with per-sample framing
  - Frame headers: [bwprobe/internal/protocol/types.go](bwprobe/internal/protocol/types.go)

### Sample-Based Testing Model

Each test run executes a fixed number of samples:

1. Client sends `SAMPLE_START` (or `SAMPLE_START_REVERSE`) via control channel
2. Data transfer runs at target rate until `-sample-bytes` payload bytes sent
3. Client sends `SAMPLE_STOP`
4. Server aggregates data into 100ms intervals (fixed in [bwprobe/internal/rpc/session.go](bwprobe/internal/rpc/session.go))
5. Server returns per-sample report with interval stats and metrics

### Throughput Calculation

- **Trimmed mean**: Drop top/bottom 10% of interval rates (the achieved bandwidth)
- **P90/P80**: Percentiles of interval rates
- **Sustained peak**: Max average rate over rolling 1-second window

Logic: [bwprobe/internal/engine/samples.go](bwprobe/internal/engine/samples.go)

### Upload vs Download (Reverse Mode)

- **Upload** (default): Client sends data to server
- **Download** (`-reverse`): Server sends data to client, but client still drives control
  - TCP retransmit stats are reported by the server (it is the sender)
  - Requires separate reverse data connection handling in [bwprobe/internal/transport/](bwprobe/internal/transport/)

### Package Responsibilities

- [bwprobe/cmd/main.go](bwprobe/cmd/main.go): CLI entry point, flag parsing, output formatting
- [bwprobe/pkg/](bwprobe/pkg/): Public Go API for embedding (import path `github.com/NodePath81/fbforward/bwprobe/pkg`)
- [bwprobe/internal/engine/](bwprobe/internal/engine/): Test orchestration, sample loop, metric computation
- [bwprobe/internal/rpc/](bwprobe/internal/rpc/): JSON-RPC client/server, session management
- [bwprobe/internal/server/](bwprobe/internal/server/): Control listener, data stream routing
- [bwprobe/internal/transport/](bwprobe/internal/transport/): Reverse-mode connection handling
- [bwprobe/internal/network/](bwprobe/internal/network/): TCP/UDP senders with pacing
- [bwprobe/internal/metrics/](bwprobe/internal/metrics/): RTT sampler, TCP_INFO reader, UDP loss tracking
- [bwprobe/internal/protocol/](bwprobe/internal/protocol/): Data frame headers and constants

## fbforward Architecture

### Three-Plane Design

fbforward runs as a single process with three main planes:

1. **Data plane**: TCP/UDP listeners forward traffic to selected upstream with per-connection/mapping pinning
2. **Control plane**: HTTP server providing RPC, metrics, WebSocket status stream, embedded web UI
3. **Health/selection plane**: ICMP reachability, bwprobe measurements, scoring, and switching logic
4. **Shaping plane** (optional): Linux tc-based ingress/egress shaping via netlink

### Startup Flow

1. [cmd/fbforward/main.go](cmd/fbforward/main.go) loads config, creates logger, validates Linux, starts Supervisor
2. `Supervisor` loads config and constructs a `Runtime`
3. `Runtime` resolves upstreams, creates `UpstreamManager`, `Metrics`, `StatusStore`, `ControlServer`, starts probes and listeners
4. On shutdown/restart, Runtime stops listeners and closes active flows

### Measurement Server Requirement

**CRITICAL**: fbforward requires `fbmeasure` running on each upstream host:

- fbmeasure provides TCP/UDP measurement endpoints for bwprobe tests
- Default port: 9876 (configurable via `measure_port` in upstream config)
- Deploy with: `make build-fbmeasure` then `./build/bin/fbmeasure --port 9876`
- Without fbmeasure, fbforward falls back to ICMP-only reachability (degraded mode)

### Flow Pinning Model

**Key concept**: Once a flow (TCP connection or UDP 5-tuple mapping) is assigned to an upstream, it stays pinned to that upstream until termination/expiry, even if the primary upstream changes.

- **Primary upstream**: The only upstream that receives **new** flow assignments
- **Pinned flows**: Existing flows continue to their originally assigned upstream
- **TCP lifecycle**: Create mapping on accept, remove on FIN/RST
- **UDP lifecycle**: Create mapping on first packet, remove after idle timeout
- **Admission rule**: New flows always go to current primary; switching only affects future flows

This ensures in-flight connections are not disrupted during upstream switches.

### Scoring and Upstream Selection

Upstream quality is based on bwprobe TCP/UDP measurements, with detailed algorithm in [docs/algorithm.md](docs/algorithm.md):

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

### Key Components

- [internal/app/supervisor.go](internal/app/supervisor.go): Owns Runtime, handles restart lifecycle
- [internal/app/runtime.go](internal/app/runtime.go): Wires all runtime components, manages goroutines
- [internal/probe/probe.go](internal/probe/probe.go): ICMP probing for reachability (no scoring)
- [internal/measure/collector.go](internal/measure/collector.go): bwprobe measurement loop and scoring inputs
- [internal/measure/fast_start.go](internal/measure/fast_start.go): fast-start TCP RTT selection
- [internal/upstream/upstream.go](internal/upstream/upstream.go): State, EMA metrics, scoring, switching logic
- [internal/forwarding/forward_tcp.go](internal/forwarding/forward_tcp.go): TCP listener, per-connection proxying, idle handling, flow pinning
- [internal/forwarding/forward_udp.go](internal/forwarding/forward_udp.go): UDP listener, per-mapping sockets, idle handling, flow pinning
- [internal/control/control.go](internal/control/control.go): HTTP API, auth, WebSocket status stream
- [internal/metrics/metrics.go](internal/metrics/metrics.go): Prometheus metric aggregation
- [web/handler.go](web/handler.go): Embedded UI handler

### Control Plane

- `GET /metrics`: Prometheus metrics (Bearer token required)
- `POST /rpc`: JSON RPC methods: `SetUpstream`, `Restart`, `GetStatus`, `ListUpstreams`, `GetMeasurementConfig` (token required)
- `GET /status`: WebSocket stream (token required; browser UI uses WebSocket subprotocol)
- `GET /`: Embedded SPA UI (Vite-built TypeScript)

Auth uses Bearer tokens with constant-time comparison. WebSocket auth supports
token embedded in subprotocol list.

## Key Integration Points

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
   [internal/upstream/upstream.go](internal/upstream/upstream.go). See [docs/algorithm.md](docs/algorithm.md) for scoring details.

2. **Control API**: Add new RPC methods in [internal/control/control.go](internal/control/control.go)

3. **Observability**: Extend `Metrics` or `StatusStore` for new telemetry

4. **Data plane**: Add new protocol listeners following TCP/UDP patterns in
   [internal/forwarding/](internal/forwarding/), ensuring flow pinning semantics

5. **Scoring algorithm**: Modify normalization or weights in [internal/upstream/upstream.go](internal/upstream/upstream.go),
   update config schema in [internal/config/config.go](internal/config/config.go)

## Implementation Principles

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

## Documentation Structure

### docs/ Directory

The [docs/](docs/) directory contains structured project documentation:

- **[outline.md](docs/outline.md)**: Complete documentation outline with 8 major sections covering the entire project
- **[glossary.md](docs/glossary.md)**: Domain terminology definitions organized by category
- **[diagrams.md](docs/diagrams.md)**: Inventory of 15 diagrams with Mermaid templates
- **[style-guide.md](docs/style-guide.md)**: Writing conventions for all documentation

Legacy documentation has been archived to [docs/archive/2025-01-26-legacy/](docs/archive/2025-01-26-legacy/).

### Using the Style Guide

When writing or updating documentation, follow [docs/style-guide.md](docs/style-guide.md):

- **Tone**: Technical and precise, neutral, active voice
- **Structure**: Sentence case headings, max 4 levels, numbered sections for reference docs
- **Terminology**: Use terms consistently from [glossary.md](docs/glossary.md), define on first use
- **Code blocks**: Always specify language, include inline comments
- **Cross-references**: Use relative links, include section numbers

The style guide provides examples for each documentation type (user guides, configuration reference, algorithm specifications).

### Documentation Sections

The documentation follows this structure (see [outline.md](docs/outline.md)):

1. **Project overview** (1.1-1.3): Purpose, architecture, component relationships
2. **Getting started** (2.1-2.3): Prerequisites, installation, quick start
3. **User guides** (3.1-3.3): fbforward, bwprobe, fbmeasure operation
4. **Configuration reference** (4.1-4.10): Complete config schema documentation
5. **API reference** (5.1-5.2): bwprobe public API, control plane API
6. **Algorithm specifications** (6.1-6.3): Upstream selection, bandwidth measurement, RPC protocol
7. **Developer guide** (7.1-7.3): Architecture deep dive, extension points, contributing
8. **Appendices** (8.1-8.2): Glossary, diagram index

## Notes on Other Documentation Files

- **AGENT.md**: Contains outdated information about ICMP-based scoring (pre-bwprobe migration). Refer to this CLAUDE.md for current architecture.
- Legacy documentation (algorithm.md, configuration.md, codebase.md) has been archived to [docs/archive/2025-01-26-legacy/](docs/archive/2025-01-26-legacy/)
