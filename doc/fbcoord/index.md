# fbcoord documentation

This directory is the canonical home for current fbcoord documentation.

fbcoord is the optional coordination service for multi-node fbforward
deployments. The current implementation uses:

- one global coordination state per deployment
- split credentials: operator token for UI/admin APIs, node tokens for
  `GET /ws/node`
- a persisted presence roster with `online`, `offline`, `aborted`, and
  `never_seen` states

Use these documents by purpose:

- [user-guide.md](user-guide.md): deployment, migration, operation, UI, and
  troubleshooting
- [api.md](api.md): HTTP/admin API reference and auth/session model
- [protocol.md](protocol.md): node WebSocket contract, selector, and state
  semantics
- [../notification-events.md](../notification-events.md): notification events
  emitted toward `fbnotify`

Legacy top-level docs such as
[../user-guide-fbcoord.md](../user-guide-fbcoord.md) and
[../fbcoord-protocol.md](../fbcoord-protocol.md) remain as short redirect
stubs so existing links keep working.
