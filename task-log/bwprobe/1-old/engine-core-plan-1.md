# Engine Core Modularity Review

## Findings (by severity)
- **High**: Core sample execution is duplicated for forward vs reverse paths (`internal/engine/samples.go`, `internal/engine/reverse.go`). Reverse uses bespoke loops and raw net reads instead of the `network.Sender` abstraction, so progress/reporting/timeouts can drift between directions.
- **High**: Direction and role are still coupled at the engine boundary via `Config.Reverse` and separate entry points (`internal/engine/runner.go`, `internal/engine/types.go`). This prevents a single, direction‑agnostic test pipeline.
- **Medium**: Control/data-plane semantics are scattered and implicit: control is in `internal/engine/control.go`, TCP data header/session handshake in `internal/network/sender.go`, reverse handshake in `internal/engine/reverse.go`, and server dispatch in `internal/server/server.go`. There is no single module enforcing the full RPC data‑binding contract.
- **Medium**: Server reverse send logic is duplicated between legacy and RPC (`internal/server/server.go`, `internal/rpc/session.go`). Both maintain near‑identical loops with pacing/stop/report logic, increasing divergence risk.
- **Low**: Result aggregation and metrics computation are repeated across forward/reverse for TCP/UDP (`internal/engine/runner.go`, `internal/engine/reverse.go`), despite using the same report structure.

## What’s Working Well
- `runSamples` + `network.Sender` provides a clean core path for forward tests.
- `sampleMetricsFromReport` is reused for both directions, which is a good shared boundary.
- RPC control fallback is centralized in `internal/engine/control.go`, reducing protocol branching in callers.

## Improvement Plan
1) **Introduce direction as a first‑class concept**
   - Add a `Direction` enum (Upload/Download) in `internal/engine/types.go` and map `Config.Reverse` → `Direction` at the CLI layer. This removes implicit role coupling in the engine.

2) **Define a unified SampleExecutor interface**
   - Example shape:
     - `RunSample(ctx, sampleID, cfg, progress) (bytesTransferred int64, report SampleReport, err error)`
   - Implement **UploadExecutor** (wraps `network.Sender`) and **DownloadExecutor** (wraps a `network.Receiver` or `DataChannel` abstraction) to share logic.

3) **Unify sample loop logic**
   - Replace `runSamples` and `runSamplesReverse*` with a single `runSampleSeries` that calls a `SampleExecutor`, handles start/stop, progress, waits, and termination consistently.

4) **Create a transport/data‑plane package**
   - Consolidate TCP/UDP data channel setup and session binding into a single package (e.g., `internal/transport`).
   - Move TCP `DATA`/`RECV` handshakes, UDP session header handling, and reverse UDP “done” markers into this package so both engine and server use the same semantics.

5) **Deduplicate server reverse send logic**
   - Extract the common TCP/UDP reverse send loops into a shared helper (e.g., `internal/server/reverse.go`) and call it from both legacy `clientState` and RPC `SessionState`.

6) **Strengthen RPC semantics**
   - Add an explicit RPC method for data binding (e.g., `data.attach`) or extend `sample.start`/`sample.start_reverse` to validate data‑channel readiness without relying on implicit preambles.
   - Document the data‑plane session binding in a single protocol file and ensure both legacy and RPC paths reference it.

7) **Tests to guard parity**
   - Add integration tests for upload/download (TCP/UDP) that verify:
     - identical sampling loop behavior,
     - consistent progress updates,
     - correct report contents (including reverse TCP metrics),
     - UDP reverse done/timeout behavior.

## Expected Outcome
- One shared engine core for both directions, with transport-specific details isolated.
- Cleaner RPC semantics and easier debugging for control/data-plane mismatches.
- Reduced risk of forward vs reverse behavior drifting over time.
