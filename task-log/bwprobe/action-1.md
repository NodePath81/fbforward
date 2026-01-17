## Action Log: code-review-2 fixes

Implemented the critical fixes and refactors from `task-log/code-review-2.md`:

- Made `RTTSampler.Stop()` idempotent with `sync.Once` to prevent double-stop panics in `internal/metrics/rtt_sampler.go`.
- Fixed legacy client state collision by using `addr.String()` as the key in `internal/server/server.go`.
- Added RPC call deadlines (`defaultRPCTimeout = 10s`) to prevent hangs in `internal/rpc/client.go`.
- Added per-sample deadlines and timeout caps for reverse downloads (TCP/UDP) and made inter-sample waits context-cancellable in `internal/engine/samples.go`.
- Added UDP ping cleanup (`CleanupExpiredUDPPings`) and wired it into the server cleanup loop; exported default cleanup intervals in `internal/rpc/session.go` and used them in `internal/server/server.go`.
- Replaced reverse TCP busy-wait loops with ready channels in `internal/server/server.go` and `internal/rpc/session.go`.
- Added network validation in `probe/Sampler.Connect()` to reject invalid `-network` values.
- Removed unused progress code (`internal/engine/progress.go`, `internal/progress/progress.go`) to avoid duplicate implementations.
- Extracted magic numbers into constants (session cleanup intervals in `internal/rpc/session.go`, buffer size in `internal/server/server.go`) and added `maxRPCMessageSize` in `internal/rpc/server.go`.
- Hardened reverse UDP setup by retrying the ping + `udp.register` sequence to avoid transient validation failures (`internal/transport/transport.go`, `internal/engine/control.go`, `internal/engine/reverse.go`).
