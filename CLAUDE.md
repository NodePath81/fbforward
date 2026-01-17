# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Repository Overview

This repository contains two independent Linux-only networking tools:

1. **bwprobe** - Network quality measurement tool that tests at a user-specified bandwidth cap
2. **fbforward** - TCP/UDP port forwarder with ICMP-based upstream quality selection

Both projects are production-grade Go implementations focused on network
quality measurement and management.

## Build and Test Commands

### Monorepo

```bash
# Build both binaries
make build

# Build one binary
make build-fbforward
make build-bwprobe

# Run tests
go test ./...
```

### bwprobe

```bash
# Build
make build-bwprobe
# or directly:
go build -o build/bin/bwprobe ./bwprobe/cmd/bwprobe

# Run tests
go test ./...

# Run specific test
go test ./bwprobe/internal/network -run TestSenderFraming
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

# Run with capabilities (required for ICMP probing)
sudo setcap cap_net_raw+ep ./build/bin/fbforward
./build/bin/fbforward --config config.yaml
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
  - Control methods: `bwprobe/internal/rpc/protocol.go`

- **Data channel**: TCP or UDP stream with per-sample framing
  - Frame headers: `bwprobe/internal/protocol/types.go`

### Sample-Based Testing Model

Each test run executes a fixed number of samples:

1. Client sends `SAMPLE_START` (or `SAMPLE_START_REVERSE`) via control channel
2. Data transfer runs at target rate until `-sample-bytes` payload bytes sent
3. Client sends `SAMPLE_STOP`
4. Server aggregates data into 100ms intervals (fixed in `bwprobe/internal/rpc/session.go`)
5. Server returns per-sample report with interval stats and metrics

### Throughput Calculation

- **Trimmed mean**: Drop top/bottom 10% of interval rates (the achieved bandwidth)
- **P90/P80**: Percentiles of interval rates
- **Sustained peak**: Max average rate over rolling 1-second window

Logic: `bwprobe/internal/engine/samples.go`

### Upload vs Download (Reverse Mode)

- **Upload** (default): Client sends data to server
- **Download** (`-reverse`): Server sends data to client, but client still drives control
  - TCP retransmit stats are reported by the server (it is the sender)
  - Requires separate reverse data connection handling in `bwprobe/internal/transport/`

### Package Responsibilities

- `bwprobe/cmd/bwprobe/main.go`: CLI entry point, flag parsing, output formatting
- `bwprobe/pkg/`: Public Go API for embedding (import path `github.com/NodePath81/fbforward/bwprobe/pkg`)
- `bwprobe/internal/engine/`: Test orchestration, sample loop, metric computation
- `bwprobe/internal/rpc/`: JSON-RPC client/server, session management
- `bwprobe/internal/server/`: Control listener, data stream routing
- `bwprobe/internal/transport/`: Reverse-mode connection handling
- `bwprobe/internal/network/`: TCP/UDP senders with pacing
- `bwprobe/internal/metrics/`: RTT sampler, TCP_INFO reader, UDP loss tracking
- `bwprobe/internal/protocol/`: Data frame headers and constants

## fbforward Architecture

### Three-Plane Design

fbforward runs as a single process with three main planes:

1. **Data plane**: TCP/UDP listeners forward traffic to selected upstream with per-connection/mapping pinning
2. **Control plane**: HTTP server providing RPC, metrics, WebSocket status stream, embedded web UI
3. **Health/selection plane**: ICMP probes, scoring, and upstream switching logic
4. **Shaping plane** (optional): Linux tc-based ingress/egress shaping via netlink

### Startup Flow

1. `cmd/fbforward/main.go` loads config, creates logger, validates Linux, starts Supervisor
2. `Supervisor` loads config and constructs a `Runtime`
3. `Runtime` resolves upstreams, creates `UpstreamManager`, `Metrics`, `StatusStore`, `ControlServer`, starts probes and listeners
4. On shutdown/restart, Runtime stops listeners and closes active flows

### Scoring and Upstream Selection

ICMP probing is used for upstream quality assessment:

- Each upstream accumulates fixed-size window of probe results
- Window metrics: loss (clamped `[0,1]`), avg RTT, jitter (mean absolute difference)
- Metrics smoothed using EMA: `metric = alpha*new + (1-alpha)*old`
- Score formula: `100 * (s_rtt ^ w_rtt) * (s_jit ^ w_jit) * (s_los ^ w_los)`
- Upstream unusable on 100% loss; recovers automatically

**Auto mode** switching:
- Requires confirmation windows, score delta threshold, minimum hold time
- Fast failover on high loss windows or consecutive dial failures

**Manual mode**:
- Operator-selected tag must be usable; otherwise rejected

### Key Components

- `internal/app/supervisor.go`: Owns Runtime, handles restart lifecycle
- `internal/app/runtime.go`: Wires all runtime components, manages goroutines
- `internal/probe/probe.go`: ICMP probing, windowing, jitter, score updates
- `internal/upstream/upstream.go`: State, EMA metrics, scoring, switching logic
- `internal/forwarding/forward_tcp.go`: TCP listener, per-connection proxying, idle handling
- `internal/forwarding/forward_udp.go`: UDP listener, per-mapping sockets, idle handling
- `internal/control/control.go`: HTTP API, auth, WebSocket status stream
- `internal/metrics/metrics.go`: Prometheus metric aggregation
- `web/handler.go`: Embedded UI handler

### Control Plane

- `GET /metrics`: Prometheus metrics (Bearer token required)
- `POST /rpc`: JSON RPC methods: `SetUpstream`, `Restart`, `GetStatus`, `ListUpstreams` (token required)
- `GET /status`: WebSocket stream (token required; browser UI uses WebSocket subprotocol)
- `GET /`: Embedded SPA UI

Auth uses Bearer tokens with constant-time comparison. WebSocket auth supports
token embedded in subprotocol list.

## Key Integration Points

### bwprobe

When modifying behavior across control/data boundary:

1. **Add new control method**: Update `bwprobe/internal/rpc/protocol.go`,
   `bwprobe/internal/rpc/server.go`, `bwprobe/internal/rpc/client.go`,
   `bwprobe/internal/engine/control.go`

2. **Change throughput calculation**: Update `bwprobe/internal/engine/samples.go`
   and ensure interval duration in `bwprobe/internal/rpc/session.go` matches

3. **Modify pacing or framing**: Update `bwprobe/internal/network/sender.go` and
   verify chunk size logic aligns with headers in `bwprobe/internal/protocol/types.go`

4. **Extend server metrics**: Update session manager in `bwprobe/internal/rpc/session.go`
   and metric collectors in `bwprobe/internal/metrics/`

### fbforward

When extending functionality:

1. **Switching behavior**: Adjust `SwitchingConfig` and `UpstreamManager` logic in
   `internal/upstream/upstream.go`

2. **Control API**: Add new RPC methods in `internal/control/control.go`

3. **Observability**: Extend `Metrics` or `StatusStore` for new telemetry

4. **Data plane**: Add new protocol listeners following TCP/UDP patterns in
   `internal/forwarding/`

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
