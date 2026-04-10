# fbcoord protocol reference

This document describes the coordination contract between fbforward nodes and
fbcoord. For deployment, UI usage, and troubleshooting, see
[fbcoord user guide](user-guide-fbcoord.md).

---

## 5.3.1 Transport and authentication

### Transport

Nodes connect to:

```text
GET /ws/node
```

fbcoord is designed for deployment behind Cloudflare Workers, so the expected
external transport is WebSocket over HTTPS.

Any `pool` query parameter sent for backward compatibility is ignored. fbcoord
maintains one global coordination state per deployment.

### Authentication

The WebSocket upgrade request must include:

```text
Authorization: Bearer <node-token>
```

Current behavior:

- valid node token: request is upgraded and routed into the global coordination
  state
- operator token: `401 Unauthorized`
- invalid or missing token: `401 Unauthorized`
- source IP currently rate-limited: `429 Too Many Requests` with `Retry-After`

The authenticated `node_id` is derived server-side from the validated node
token. The Worker passes that trusted identity to the Durable Object over an
internal header; the Durable Object does not trust a client-supplied `node_id`.

### First-message requirement

After upgrade, the first node message must be `hello`. Any other message
produces:

```json
{
  "type": "error",
  "code": "missing_hello",
  "message": "hello must be sent first"
}
```

and the socket is closed with code `1008`.

### Liveness expectation

The current server implementation evicts stale nodes after 30 seconds without a
heartbeat. That matches three 10-second heartbeat intervals, which is the
default fbforward coordination heartbeat.

---

## 5.3.2 Message reference

### `hello`

Sent once per connection, first message only.

```json
{
  "type": "hello"
}
```

Compatibility notes:

- legacy `pool` and `node_id` fields are accepted if present
- those legacy fields are ignored

Behavior:

- if another connection with the same authenticated `node_id` is already
  active, the old connection is closed and replaced
- on successful handshake, fbcoord sends `ready` first, then the current
  `pick`

### `ready`

Sent by fbcoord after it accepts `hello`.

```json
{
  "type": "ready",
  "node_id": "node-1"
}
```

Fields:

- `node_id`: the server-authenticated node identity derived from the node token

### `preferences`

Sent after `hello`, then again whenever the node's ranked preference list
changes.

```json
{
  "type": "preferences",
  "upstreams": ["us-a", "us-b", "eu-c"],
  "active_upstream": "us-a"
}
```

Fields:

- `upstreams`: node-local preference list, ordered best first
- `active_upstream`: optional current active upstream on the node, or `null`

Notes:

- the submitted order is significant and feeds the aggregate-rank selector
  directly
- an empty `upstreams` list is an explicit signal that the global coordination
  state should have no coordinated pick while that node remains active

### `heartbeat`

Sent periodically to keep the node active.

```json
{
  "type": "heartbeat"
}
```

Heartbeats update `last_seen_at` and prevent stale eviction.

### `bye`

Sent by the node to exit coordination cleanly.

```json
{
  "type": "bye"
}
```

If fbcoord accepts `bye`, the node transitions to `offline`, receives
`closing`, and is removed from the active selector.

### `closing`

Sent by fbcoord after it accepts `bye`.

```json
{
  "type": "closing"
}
```

After `closing`, either peer may close the WebSocket. If the socket stays open,
fbcoord force-closes it after a short timeout.

### `pick`

Broadcast by fbcoord whenever the visible coordinated pick changes, and also
sent immediately after successful `hello`.

```json
{
  "type": "pick",
  "version": 12,
  "upstream": "us-a"
}
```

No-consensus example:

```json
{
  "type": "pick",
  "version": 13,
  "upstream": null
}
```

Fields:

- `version`: monotonic pick version for the deployment-wide coordination state
- `upstream`: selected upstream tag, or `null` when there is no coordinated
  pick

### `error`

Sent for protocol-level problems on an established socket.

```json
{
  "type": "error",
  "code": "invalid_json",
  "message": "Invalid JSON payload"
}
```

Current error codes:

- `invalid_json`
- `invalid_message`
- `missing_hello`
- `rate_limited`
- `reconnect_throttled`

Connection behavior:

- `invalid_json`: socket is closed with code `1008`
- `invalid_message`: socket is closed with code `1008`
- `missing_hello`: socket is closed with code `1008`
- `rate_limited`: socket is closed with code `1008`
- `reconnect_throttled`: socket is closed with code `1013`

### Session lifecycle

Normal lifecycle:

1. Client opens `GET /ws/node` with Bearer node token.
2. Worker validates the node token, derives the bound `node_id`, and upgrades
   the request.
3. Client sends `hello`.
4. Server registers the connection under the authenticated `node_id` and sends
   `ready`.
5. Server sends the current `pick`.
6. Client sends `preferences`.
7. Server recomputes and broadcasts `pick` if the visible result changes.
8. Client continues sending `heartbeat`.
9. To exit cleanly, client sends `bye` and waits for `closing`.
10. On disconnect or stale eviction without accepted teardown, the node moves to
    `aborted`.

If a node reconnects with the same token or another token bound to the same
`node_id`, the old socket is closed with code `1012` and reason `replaced`.

---

## 5.3.3 Selection algorithm

fbcoord selects one coordinated upstream for the deployment-wide coordination
state from the submitted preference lists of all active nodes.

Current algorithm:

1. If there are no active nodes, return `null`.
2. If any active node submitted an empty list, return `null`.
3. Compute the intersection of all submitted upstream lists.
4. If the intersection is empty, return `null`.
5. For each shared upstream, compute aggregate rank as the sum of its zero-based
   index in each node's list.
6. Choose the shared upstream with the lowest aggregate rank.
7. If multiple upstreams tie on aggregate rank, choose the lexicographically
   smallest tag.

Worked example:

```text
node-1: [b, a, c]
node-2: [a, b, c]
node-3: [b, c, a]
```

Shared candidates:

- `a`: rank 1 + 0 + 2 = 3
- `b`: rank 0 + 1 + 0 = 1
- `c`: rank 2 + 2 + 1 = 5

Result:

```text
pick = b
```

No-consensus cases:

- no active nodes
- any node submits `[]`
- nodes submit disjoint sets

The selector is deterministic. The same set of submitted lists produces the
same pick.

---

## 5.3.4 Coordination state and lifecycle

### Coordination state model

The global coordination state tracks:

- active selector inputs for currently `online` nodes
- a persisted node roster keyed by `node_id`
- per-node status: `online`, `offline`, or `aborted`
- per-node timestamps:
  - `first_seen_at`
  - `last_connected_at`
  - `last_seen_at`
  - `disconnected_at`
- current visible `pick { version, upstream }`

The admin UI reads this state through `GET /api/state`.

### Stale eviction

If a node does not refresh `last_seen_at` within 30 seconds, it is removed from
the active selector and its roster status becomes `aborted`. Eviction removes:

- its live connection
- its contribution to the selector

If eviction changes the visible pick, fbcoord broadcasts a new `pick`.

### Version semantics

Pick versions start at:

```json
{ "version": 0, "upstream": null }
```

The version increments only when the visible `upstream` value changes. It does
not increment when:

- node membership changes but the selected upstream stays the same
- a node resubmits the same preference list
- a new node joins and the visible pick remains unchanged

It does increment when the visible pick changes in either direction:

- `null -> "tag"`
- `"tag-a" -> "tag-b"`
- `"tag" -> null`

### Reconnect and redeploy expectations

Reconnect semantics:

- same `node_id` replaces the older live connection
- the replacement connection receives `ready`, then the current pick
- the public roster remains `online`; superseded sockets do not create a
  transient `aborted` state

Redeploy semantics:

- Worker redeploys drop live WebSocket sessions
- nodes are expected to reconnect and resend `hello` plus `preferences`
- persisted roster history survives Durable Object restart, and any previously
  persisted `online` entries are normalized to `aborted` on reload
- node tokens remain valid across redeploys unless they were explicitly revoked

### Admin API state shape

Current admin HTTP state surface:

- `GET /api/state` returns `pick`, `node_count`, `counts`, and per-node detail
  for all provisioned node IDs with:
  - `node_id`
  - `status`
  - `first_seen_at`
  - `last_connected_at`
  - `last_seen_at`
  - `disconnected_at`
  - `upstreams`
  - `active_upstream`

`counts` includes:

- `online`
- `offline`
- `aborted`
- `never_seen`

`never_seen` is synthesized by joining provisioned node tokens with the
persisted/live roster. It means a node token exists, but no session has ever
completed `hello`.

Credential management is separate from the node WebSocket protocol:

- `GET /api/token/info`
- `POST /api/token/rotate`
- `GET /api/node-tokens`
- `POST /api/node-tokens`
- `DELETE /api/node-tokens/:node_id`

Revoking a node token removes the corresponding roster entry and closes any
live session for that `node_id`.
