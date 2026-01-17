# Communication Pattern Refinement Plan (Revised)

## Current Communication (Observed in Code)

### Connection Types
1) **TCP Ping (PING/PONG)**
- RTT measurement using a short TCP connection with header `PING` → `PONG`.

2) **TCP Control (CTRL)**
- Line protocol: `SAMPLE_START <id>` or `SAMPLE_START <id> REVERSE <bw> <chunk> <rtt_ms> <bytes> <udp_port>`.
- `SAMPLE_STOP <id>` returns a JSON report as a single line (or `ERR ...`).

3) **TCP Data (DATA)**
- Frames: 8-byte header (sample_id, payload_len) + payload.
- Forward tests: client → server.

4) **TCP Reverse (RECV)**
- Same framing as DATA, but server → client.

5) **UDP Data + Ping**
- UDP data packets with header: type(1B) + sample_id(4B) + seq(8B) + payload.
- UDP ping/pong uses type 2/3.

### Data Flow
- **Forward (upload)**: control channel starts sample → data channel streams frames → control channel stop returns JSON report.
- **Reverse (download)**: client opens `RECV` conn → control channel start reverse → server writes frames → control stop returns JSON report.
- **UDP reverse**: client opens UDP listener, sends ping, control starts reverse with UDP port; server sends UDP to client.

### Current Weaknesses
- No explicit **session identity**; server keys state by client IP only (NAT collision risk).
- **Mixed protocol styles**: text commands, JSON replies, binary data; no versioning or capability negotiation.
- **Reverse race** between control and data channel.
- No request/response correlation; no structured error codes.
- UDP reverse relies on ad-hoc ping and host parsing; no explicit registration ACK.

## Revised Target Protocol (Incorporates `protocol-refinement.md`)

### Guiding Principles
- **RPC-style control channel** with explicit request/response IDs.
- **Versioned protocol** with feature negotiation.
- **Session-bound data channels** using a `session_id`.
- **Binary data plane unchanged** for efficiency.

### Control Channel (JSON-RPC 2.0 over TCP)
- **Transport**: single TCP connection.
- **Framing**: `uint32 length` + JSON-RPC payload.
- **Header**: use 4-byte `RPC\0` preamble to distinguish from legacy `CTRL`.

#### Core RPC Methods
- `session.hello` → negotiate version, return `session_id`, server capabilities.
- `sample.start` → start forward sample (per-sample state).
- `sample.start_reverse` → start reverse sample (explicit parameters for server pacing).
- `sample.stop` → returns sample report (JSON payload).
- `ping` → RTT measurement via RPC (optional replacement for PING/PONG).
- `server.info` → capabilities/limits (optional).

#### Error Handling
- Standard JSON-RPC errors + app-specific codes (e.g., `REVERSE_NOT_READY`, `INVALID_SAMPLE`, `PROTO_MISMATCH`).
- Error responses include code, message, optional data.

### Data Channels (Session-Bound)
Keep existing frame layouts but **add `session_id`**:
- **TCP data frame**: `session_id | sample_id | payload_len | payload`.
- **UDP data packet**: `session_id | sample_id | seq | payload`.

This binds data to control state, prevents collisions across clients and NAT.

### Reverse Flow Improvements
- **Explicit reverse readiness** via RPC `sample.start_reverse` response.
- **Client-opened data connection** (recommended) to avoid inbound firewall issues.
- **UDP registration**: client calls `udp.register` with local port; server responds `udp.register_ack` with confirmation before sending data.

### RTT Measurement
- Prefer RPC `ping` (timestamp echo) to avoid separate TCP connections.
- Keep legacy PING/PONG as fallback while migrating.

## Migration Plan

### Phase 1: Dual Stack
- Server supports both `CTRL` and `RPC\0`.
- Client tries RPC first; falls back to legacy text protocol.
- Data frames remain compatible (optionally accept both with/without session_id during transition).

### Phase 2: Session Binding
- Add `session_id` to data headers.
- Server accepts both legacy frames and session-bound frames.

### Phase 3: Deprecate Legacy
- Log warnings for `CTRL` usage.
- Remove legacy after a transition window.

## Implementation Steps (Refined)
1) Define RPC schema (`internal/rpc` package): request/response structs, error codes.
2) Add RPC server handler (length-prefixed JSON-RPC).
3) Add RPC client with request IDs, timeout handling.
4) Implement session management and binding in server state.
5) Add UDP registration handshake (explicit ACK).
6) Update data plane headers to include session_id (optional during phase 1).
7) Add integration tests for forward/reverse TCP/UDP with both protocols.

## Testing Plan
- Unit tests: parser/encoder, error handling, RPC method validation.
- Integration: forward TCP/UDP, reverse TCP/UDP, concurrency, session collision.
- Backward compatibility tests: legacy client ↔ new server and vice versa.

## Summary
This revision aligns the plan with the alternative proposal by adopting JSON-RPC 2.0 with length-prefixed framing, structured error codes, and explicit session binding—while preserving efficient binary data channels. The protocol becomes easier to evolve, safer under NAT/concurrent use, and more robust for reverse-mode tests.
