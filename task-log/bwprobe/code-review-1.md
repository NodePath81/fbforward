# Go Project Code Review Report
## Executive Summary
- The repo implements a client/server network quality probe with sample-based transfers, RTT sampling, and loss metrics; the design is modular and documented.
- Code structure is clear overall, with separate CLI, public API, and internal engine/network/server layers.
- Primary risks are build failure from a panic on double Stop in RTT sampling, and hangs in reverse TCP download or RPC calls; legacy client state also collides across clients sharing an IP.
- Tests and CI are thin, so regressions in protocol handling or timing behavior are likely to slip through.

## Project Overview (what it does, main components)
- CLI entrypoint `cmd/bwprobe/main.go` parses flags, prints configuration, and runs client probes or starts the server.
- Public API in `probe/` wraps `internal/engine` for end-to-end runs, RTT-only sampling, and per-sample control.
- `internal/engine` drives sample loops and aggregates metrics; it talks to `internal/rpc` for control and `internal/network` or `internal/transport` for data transfer.
- Server side `internal/server/server.go` listens on TCP/UDP, supports JSON-RPC sessions plus a legacy text protocol, and reports per-sample intervals.
- Architectural boundary is blurred by two parallel control/data stacks (RPC sessions vs legacy client state) that duplicate state tracking and metrics logic.

## Strengths
- Clear package separation between API, engine, transport, and server layers.
- Sampling and throughput calculations are documented and implemented consistently.
- TCP pacing and BDP-based buffer sizing are practical choices for stable measurements.
- RTT sampling and loss metrics are integrated without heavy dependencies.

## Findings (Prioritized)
### P0
- None found.

### P1
- Location: `internal/metrics/rtt_sampler.go` (RTTSampler.Stop) and `probe/rtt.go` (RTTMeasurer.Start); Impact: process can panic on double Stop when a caller stops manually and the context cancel path also stops; Evidence: Stop closes `stopCh` without guard and Start spawns a goroutine that calls Stop on ctx cancel; Suggested fix: make Stop idempotent using `sync.Once` or a non-blocking close pattern.
- Location: `internal/server/server.go` (clientKey, serverState.client, runUDP); Impact: concurrent legacy clients behind the same IP can mix state and corrupt metrics; Evidence: client key is host-only and ignores port, so multiple clients share the same `clientState`; Suggested fix: key by full remote address or refactor legacy state to be per-connection; consider deprecating the legacy protocol.
- Location: `internal/engine/samples.go` (runSampleSeries) and `internal/transport/receiver.go` (TCPReceiver.Receive); Impact: reverse TCP downloads can hang indefinitely if the server stalls, especially with default MaxDuration=0; Evidence: timeout errors are retried forever and there is no per-sample deadline in the TCP download path; Suggested fix: add per-sample timeouts based on bandwidth and sample size, and stop on repeated timeouts or ctx cancel.
- Location: `internal/rpc/client.go` (call); Impact: a stalled server or network can block the client forever, freezing tests and heartbeats; Evidence: there are no read/write deadlines or context timeouts around RPC calls; Suggested fix: set per-call deadlines on the connection and propagate context or timeouts to call sites.

### P2
- Location: `probe/sampler.go` (Connect); Impact: invalid `Network` values silently fall back to UDP, which can produce incorrect protocol behavior; Evidence: any non-"tcp" string uses the UDP path without validation; Suggested fix: validate network values and return `ErrInvalidNetwork`.
- Location: `internal/engine/progress.go`, `internal/progress/progress.go`, `cmd/bwprobe/main.go`; Impact: duplicate and unused progress implementations add maintenance overhead and confusion; Evidence: `NewProgressTracker` and `progress.Bar` are unused while CLI defines its own progress UI; Suggested fix: remove unused code or unify to one progress implementation.
- Location: `internal/metrics/udp.go` and `internal/rpc/session.go` (RecordUDPPacket); Impact: UDP loss can be underreported when duplicates or reordering occur; Evidence: loss is computed from max-base+1 minus packetsRecv, and duplicates increase packetsRecv; Suggested fix: track seen sequences in a window or compute loss on unique sequence numbers.
- Location: `internal/engine/samples.go` (inter-sample wait); Impact: cancellation can be delayed by the wait duration; Evidence: `time.Sleep(cfg.Wait)` ignores ctx.Done; Suggested fix: replace with a timer and select on ctx.Done.
- Location: `internal/rpc/session.go` (udpPing map); Impact: memory can grow without bound on long-lived servers with many client addresses; Evidence: the map is never pruned; Suggested fix: add TTL cleanup alongside the existing session cleanup ticker.

## Suggested Refactor/Cleanup Plan
- Phase 1 (S): Make RTT sampler Stop idempotent; add RPC call deadlines; add per-sample timeout for reverse TCP downloads; validate network in `probe.Sampler`; make inter-sample waits context-aware.
- Phase 2 (M): Reduce protocol duplication by isolating or deprecating the legacy path; unify progress reporting; remove unused helpers and duplicate `writeFull` implementations; improve UDP loss tracking with focused unit tests.
- Phase 3 (L): Add integration tests for TCP/UDP and reverse mode over loopback; add basic benchmarks for senders; implement CI with lint, test, and race checks; document supported Go versions and Linux requirements in CI.

## Tooling & CI Recommendations
- Enforce formatting via `gofmt` and `goimports` in CI.
- Add `golangci-lint` with `staticcheck`, `govet`, `errcheck`, `unused`, and `revive` enabled.
- Run `go test ./...` and `go test -race ./...` in CI; add a small integration test suite for client/server loopback.
- Run `govulncheck` on modules; fail CI on high severity findings.
- Add a `go mod tidy` check and pin a supported Go version in CI.

## Appendix: Notable Files/Modules
- `cmd/bwprobe/main.go` CLI entrypoint and output formatting.
- `probe/` public API: config, results, RTT, sampler.
- `internal/engine/` sampling loop, metrics aggregation, progress handling.
- `internal/server/server.go` server logic and legacy protocol handling.
- `internal/rpc/` JSON-RPC protocol and session management.
- `internal/network/` and `internal/transport/` data plane send/receive paths.
