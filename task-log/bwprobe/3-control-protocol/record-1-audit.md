# Control Protocol Audit (Record-1)

## Scope
Reviewed `task/protocol-record-1.md` against the current codebase implementation of the JSON-RPC control protocol and its integration with the data plane.

## Findings (Bugs / Incomplete Implementations)

1) **RPC control is not wired to the data plane**
- The RPC session state is updated by `sample.start`/`sample.stop`, but TCP/UDP data handlers still record into legacy `clientState` keyed by client IP. RPC sessions never receive data, so RPC reports can be empty or incorrect.
- Evidence:
  - RPC session manager used only in `internal/rpc/server.go`.
  - Data handlers use `state.client(key)` in `internal/server/server.go` and never call `SessionState.RecordSample`/`RecordUDPPacket`.
- Impact: If client uses RPC control, throughput/loss reports will not reflect actual data traffic.
- Files: `internal/server/server.go`, `internal/rpc/server.go`, `internal/rpc/session.go`.

2) **Reverse mode RPC does not trigger server-side send path**
- `sample.start_reverse` in `internal/rpc/server.go` only sets sample state; it does not start reverse data transmission. The reverse send logic exists only in legacy control handlers (`startReverseTCP`/`startReverseUDP`) in `internal/server/server.go`.
- Impact: RPC reverse tests can start successfully but send no data.
- Files: `internal/rpc/server.go`, `internal/server/server.go`.

3) **RPC data connection binding is missing**
- Session has `RegisterDataConnection`/`RegisterReverseConnection` but these are never called, and data frames do not include `session_id`. The server cannot associate data connections with sessions.
- Impact: Even with RPC control, data cannot be safely attributed to the correct session (NAT collision risk remains).
- Files: `internal/rpc/session.go`, `internal/server/server.go`, `internal/network/sender.go`.

4) **RPC StartSample uses hard-coded network type**
- `controlClient.StartSample` always calls `StartSample(sampleID, "tcp")`, even for UDP tests.
- Impact: RPC request misrepresents the active network type. Server validation currently allows both but the request is wrong.
- File: `internal/engine/control.go`.

5) **UDP registration handshake is a stub**
- `udp.register` returns `TestPacketsReceived` equal to the requested count without sending/validating any packets.
- Impact: false-positive success; reverse UDP can still fail silently.
- File: `internal/rpc/server.go`.

6) **Heartbeat cleanup is not active**
- `SessionManager.CleanupExpiredSessions` exists but no goroutine calls it; the RPC client does not send heartbeats by default.
- Impact: session leak under client crashes or network interruptions.
- Files: `internal/rpc/session.go`, `internal/server/server.go`, `internal/engine/control.go`.

7) **Legacy control protocol is still the default fallback**
- The client auto-falls back to legacy if RPC fails; no switch to enforce RPC-only operation.
- Impact: legacy protocol is not truly deprecated and may mask RPC issues.
- File: `internal/engine/control.go`.

## Documentation Mismatches (Record vs. Code)
- `task/protocol-record-1.md` claims “complete” JSON-RPC integration, but:
  - Data plane remains legacy and IP-keyed.
  - Reverse RPC does not transmit data.
  - UDP register is a placeholder.
  - Session cleanup not running.

## Cleanup / Simplification Opportunities

1) **Remove or wire unused session fields**
- `SessionState.dataConn`, `reverseConn`, and reverse-control fields are unused. Either remove or implement their usage.
- File: `internal/rpc/session.go`.

2) **Consolidate report types**
- `intervalReport`/`SampleReport` are duplicated across `internal/engine/control.go`, `internal/server/server.go`, and `internal/rpc/protocol.go`. Consider a shared struct in `internal/protocol` or `internal/types`.

3) **Deprecation switch**
- Add a client flag to require RPC (fail fast) to encourage migration and expose RPC defects early.

4) **Reduce control protocol mixing**
- If RPC is primary, move legacy control parsing into a separate file or build tag to keep paths clean.

## Suggested Next Steps (Prioritized)

1) **Bind data plane to sessions**
- Add `session_id` to TCP/UDP data headers and register connections to session state.
- Update data handlers to call `SessionState.RecordSample`/`RecordUDPPacket`.

2) **Implement reverse RPC send path**
- Move reverse sending logic into RPC handlers or call the existing send routines through a shared interface.

3) **Implement UDP registration validation**
- Send test packets and confirm receipt before returning “registered.”

4) **Enable session cleanup**
- Start a goroutine to call `CleanupExpiredSessions` on a timer; add periodic client heartbeat (or server-side TTL).

5) **Fix `StartSample` network parameter**
- Pass the actual network type (tcp/udp) from config in `controlClient.StartSample`.

---

If you want, I can turn these into actionable tickets and start with the session-bound data frame integration.
