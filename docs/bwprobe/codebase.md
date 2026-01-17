# Codebase Guide

This document summarizes the bwprobe codebase layout, core modules, and
key integration points for maintenance.

## Top-level layout

- `bwprobe/cmd/bwprobe/main.go`: CLI entry point and output formatting.
- `bwprobe/pkg/`: Public Go API for embedding bwprobe in other tools (package `probe`).
- `bwprobe/internal/`: Core implementation packages.
- `docs/bwprobe/`: Documentation and design notes.
- `task-log/bwprobe/`: Historical plans and audits.

## Public API (`bwprobe/pkg/`)

- `probe.Config`, `probe.Run`, `probe.RunWithProgress`: end-to-end tests.
- `probe.MeasureRTT`, `probe.RTTMeasurer`: RTT-only or continuous RTT sampling.
- `probe.Sampler`: low-level, per-sample control (upload only).
- `probe.Results`: stable result contract for callers.

## Core packages

- `bwprobe/internal/engine/`
  - Orchestrates test flow and sampling loops.
  - Computes throughput metrics (trimmed mean, p90/p80, sustained peak).
  - Implements upload and download paths with a shared sample loop.
- `bwprobe/internal/rpc/`
  - JSON-RPC 2.0 control protocol (client/server).
  - Session manager aggregates per-interval stats and sample reports.
  - Provides session IDs for tying control and data channels together.
- `bwprobe/internal/server/`
  - Listens for control connections and data streams.
  - Routes RPC requests, registers UDP clients, and receives TCP/UDP data.
- `bwprobe/internal/transport/`
  - Reverse-mode helpers (dial/listen data sockets).
  - Shared sender/receiver loops for server-driven transfers.
- `bwprobe/internal/network/`
  - TCP/UDP sender implementations and framing logic.
  - TCP pacing (`SO_MAX_PACING_RATE`) and UDP leaky-bucket limiter.
  - Chunk sizing helpers and BDP-based TCP send buffer sizing.
- `bwprobe/internal/metrics/`
  - RTT sampler and TCP/UDP ping helpers.
  - TCP_INFO reader for retransmit/segment stats.
  - UDP loss tracking helpers.
- `bwprobe/internal/protocol/`
  - TCP/UDP frame headers and constants used on the data plane.
- `bwprobe/internal/progress/`
  - CLI progress bar utilities.
- `bwprobe/internal/util/`
  - Parsing and formatting helpers (bandwidth, bytes).

## Data flow summary

1. `bwprobe/cmd/bwprobe/main.go` builds a `probe.Config` and calls `probe.RunWithProgress`.
2. `probe.Run` converts to `internal/engine.Config` and starts RTT sampling.
3. `internal/engine.Run` selects upload or download and runs `runSampleSeries`.
4. For each sample, the control channel issues `StartSample`/`StopSample`.
5. The server session manager aggregates 100ms intervals and returns a report.
6. The engine computes metrics from the report and returns `probe.Results`.

## Reverse (download) mode

- The client still drives the test and control channel.
- The data flow is reversed: the server sends data to a client-side receiver.
- Reverse data sockets are handled in `bwprobe/internal/transport/`.
- TCP retransmits and send-buffer values are reported by the server.

## Protocol and framing

- Control: JSON-RPC methods live in `bwprobe/internal/rpc/protocol.go`.
- TCP data frames: 8-byte header (`sample_id`, `payload_len`) + payload.
- UDP data frames: type + `sample_id` + sequence number (session-aware header
  when JSON-RPC sessions are used).
- Interval duration is fixed at 100ms in `bwprobe/internal/rpc/session.go`.

## Where to change things

- Add/adjust control methods: `bwprobe/internal/rpc/*` and `bwprobe/internal/engine/control.go`.
- Change throughput math: `bwprobe/internal/engine/samples.go`.
- Update pacing/chunk logic: `bwprobe/internal/network/sender.go`.
- Extend server metrics: `bwprobe/internal/rpc/session.go` and `bwprobe/internal/metrics/*`.
- Update CLI output: `bwprobe/cmd/bwprobe/main.go`.

## Tests

- `bwprobe/internal/network/sender_test.go` covers sender behavior and framing.
