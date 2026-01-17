# Control Protocol Audit (Record-2)

## Scope
Reviewed the current codebase against `task/protocol-record-2.md` and verified whether issues from `task/record-1-audit.md` are resolved. Also checked robustness and correctness of the control/data plane integration.

## Status of Record-1 Issues

1) **RPC control not wired to data plane**
- **Status: Partially fixed**
- **What’s fixed**: TCP forward now binds the data connection to an RPC session via the post-`DATA` session-id handshake; `SessionState.RecordSample` is used for TCP data.
- **What’s missing**: UDP forward still records into legacy `clientState` only (no RPC session binding), so RPC UDP reports remain incorrect.
- **Files**: `internal/server/server.go`, `internal/network/sender.go`.

2) **Reverse RPC does not trigger server send**
- **Status: Not fixed**
- `sample.start_reverse` still only sets state in `internal/rpc/server.go`; it does not start the server-side send loops (TCP/UDP). Reverse send logic remains in legacy handlers only.
- **Files**: `internal/rpc/server.go`, `internal/server/server.go`.

3) **RPC data connection binding missing**
- **Status: Partially fixed**
- TCP forward uses a session-id handshake and calls `SessionState.RegisterDataConnection`.
- Reverse connections and UDP data are still not bound to RPC sessions.
- **Files**: `internal/rpc/session.go`, `internal/server/server.go`.

4) **StartSample hard-coded network type**
- **Status: Fixed**
- `StartSample(sampleID, network)` is now passed through from config in `internal/engine/samples.go` and `probe/sampler.go`.

5) **UDP registration handshake stub**
- **Status: Not fixed**
- `udp.register` still returns success without any validation or test packet exchange.
- **File**: `internal/rpc/server.go`.

6) **Heartbeat cleanup not active**
- **Status: Fixed (server side)**
- Cleanup goroutine exists in `internal/server/server.go`.
- **Gap**: RPC client does not send periodic heartbeats; long tests (>60s) risk session cleanup mid-run.

7) **Legacy fallback still default**
- **Status: Unchanged (intentional)**
- Client auto-fallback remains. No RPC-only option to force the new protocol.

## New/Additional Findings (Robustness & Correctness)

A) **Legacy TCP clients are likely broken by the session-id read**
- `handleTCPData` always attempts to read 2 bytes for a session length. Legacy clients do not send this, so the first two bytes of the TCP frame header are consumed and lost.
- This corrupts the next frame header (sample_id/payload_len misaligned) and will break legacy tests on a dual-stack server.
- **Severity: High** (backward compatibility is effectively broken)
- **File**: `internal/server/server.go`.

B) **Session-id read can corrupt data on partial reads**
- If the 2-byte read times out after consuming 1 byte, the stream is still misaligned but the code proceeds as if nothing happened.
- **Severity: Medium**
- **File**: `internal/server/server.go`.

C) **RPC UDP forward still not wired**
- Control plane is RPC, but UDP data is recorded into legacy IP-keyed state; reports do not reflect the RPC session.
- **Severity: Medium**
- **Files**: `internal/server/server.go`, `internal/network/sender.go`.

D) **Session cleanup can terminate active tests**
- `CleanupExpiredSessions` uses last heartbeat only; long tests with no heartbeats can close connections mid-test.
- **Severity: Medium**
- **Files**: `internal/rpc/session.go`, `internal/server/server.go`.

E) **Reverse RPC still non-functional**
- Even after `sample.start_reverse`, no data is sent because RPC handlers do not invoke server send loops.
- **Severity: High**
- **File**: `internal/rpc/server.go`.

## Correctness vs `protocol-record-2.md`

- The document claims backward compatibility with legacy clients; however, the current TCP session-id read likely breaks legacy DATA framing.
- TCP forward RPC integration exists, but only for TCP; UDP is still legacy-bound.
- Reverse mode remains unimplemented for RPC.

## Cleanup / Simplification Opportunities

1) **Fix session-id handshake without breaking legacy**
- Use a distinct header or marker after `DATA` (e.g., `SID\0`) to indicate the presence of a session-id block.
- Or, read the first frame header using `Peek` and only consume session-id when the marker exists.

2) **Move legacy handling behind a build tag or separate file**
- Keeps RPC path clean and reduces mixed-protocol complexity.

3) **Consolidate duplicate report structs**
- `intervalReport`/`SampleReport` exist in multiple packages.

## Recommended Next Steps

1) **Fix legacy TCP framing regression** (highest priority)
- Ensure session-id handshake does not consume header bytes when absent.

2) **Bind UDP forward data to sessions**
- Add session-id for UDP data, or register UDP connections to session state.

3) **Implement reverse RPC data path**
- Start server send loops from `sample.start_reverse`.

4) **Add client heartbeats**
- Periodic `session.heartbeat` from `rpc.RPCClient`.

---

If you want, I can implement the session-id handshake fix first (it unblocks backward compatibility) and then proceed with UDP session binding.
