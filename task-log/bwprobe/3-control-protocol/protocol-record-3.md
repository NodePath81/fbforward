## Protocol Record 3 - Implementation Notes

### Summary of Fixes
- **Legacy TCP framing**: Added non-destructive session-id detection (`Peek`) so legacy TCP data frames are not corrupted by the RPC session-id handshake.
- **UDP forward (RPC)**: Added `UDPTypeDataSession` and session-aware UDP sender/receiver. UDP data is now recorded into the correct RPC session instead of legacy IP state.
- **Reverse RPC data path**: Implemented server-side reverse send loops for TCP/UDP (`StartReverseTCP/StartReverseUDP`) and wired `sample.start_reverse` to start them.
- **Reverse metrics exchange**: Sample reports now include TCP send buffer and TCP retransmit counters from the server. Client aggregates these for reverse mode results.
- **Role vs. direction**: CLI output now separates **Role** (client) from **Traffic direction** (upload/download).
- **Session liveness**: Server updates `lastHeartbeat` on sample activity; RPC client now sends periodic heartbeats. Added lightweight UDP registration validation using a recent UDP ping.

### Protocol/Behavior Changes
- **TCP data/reverse session-id handshake** remains **2-byte length + session-id** but is now detected safely (no header corruption for legacy).
- **UDP forward data**:
  - New `UDPTypeDataSession` with layout:
    - `type (1)` + `sid_len (1)` + `session_id` + `sample_id (4)` + `seq (8)` + payload
  - Legacy `UDPTypeData` remains unchanged for non-RPC clients.
- **UDP registration**: `udp.register` now validates a recent UDP ping from the client’s announced UDP port; registration is done once per control connection in reverse mode.
- **Reverse TCP**: `RECV` connection can now carry the session-id header (length-prefixed) so the server binds the reverse data channel to the RPC session.

### Notes
- Reverse mode sample reports now populate:
  - `tcp_send_buffer_bytes`
  - `tcp_retransmits`
  - `tcp_segments_sent`
- Reverse results no longer show `N/A` for TCP send buffer or retransmits.

### Follow-up Fixes
- **Reverse TCP retrans stats**: Added a TCP_INFO fallback to derive segment and retransmit counts from `Bytes_sent` / `Bytes_retrans` when segment counters report 0. This prevents zeroed stats on some kernels.
- **Reverse UDP stuck**: Server now sends a `UDPTypeDone` marker (sample_id) after completing each reverse UDP sample. Client stops on this marker and uses a time-based deadline fallback to avoid hanging when packets are lost.
- **Reverse TCP zero stats**: Kept reverse samples marked active until `sample.stop` completes; stop handler now always goes through reverse stop path so TCP stats are captured before clearing state.
