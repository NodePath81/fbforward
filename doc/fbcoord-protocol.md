# fbcoord protocol reference

This document describes the coordination contract between fbforward nodes and fbcoord. For deployment, UI usage, and troubleshooting, see [fbcoord user guide](user-guide-fbcoord.md).

---

## 5.3.1 Transport and authentication

### Transport

Nodes connect to:

```text
GET /ws/node?pool=<pool>
```

fbcoord is designed for deployment behind Cloudflare Workers, so the expected external transport is WebSocket over HTTPS.

### Authentication

The WebSocket upgrade request must include:

```text
Authorization: Bearer <shared-token>
```

Current behavior before upgrade:

- valid token: request is routed to the pool Durable Object
- invalid or missing token: `401 Unauthorized`
- source IP currently rate-limited: `429 Too Many Requests` with `Retry-After`

The pool name is required both:

- in the query string (`?pool=<pool>`)
- in the first `hello` message

If those values do not match, fbcoord sends `error { code: "invalid_pool" }` and closes the socket.

### First-message requirement

After upgrade, the first node message must be `hello`. Any other message before `hello` produces:

```json
{ "type": "error", "code": "missing_hello", "message": "hello must be sent first" }
```

### Liveness expectation

The current server implementation evicts stale nodes after 30 seconds without heartbeat. That matches three 10-second heartbeat intervals, which is the default fbforward coordination heartbeat.

---

## 5.3.2 Message reference

### `hello`

Sent once per connection, first message only.

```json
{
  "type": "hello",
  "pool": "default",
  "node_id": "edge-sfo-01"
}
```

Fields:

- `pool`: pool name, must match the query parameter
- `node_id`: stable per-node identifier within the pool

Behavior:

- if another connection with the same `node_id` is already active in that pool, the old connection is closed and replaced
- fbcoord immediately sends the current `pick` snapshot after successful `hello`

### `preferences`

Sent after `hello`, then again whenever the node's ranked preference list changes.

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

- the submitted order is significant and feeds the aggregate-rank selector directly
- an empty `upstreams` list is an explicit signal that the pool should have no coordinated pick while that node remains active

### `heartbeat`

Sent periodically to keep the node active.

```json
{
  "type": "heartbeat"
}
```

Heartbeats update `last_seen` and prevent stale eviction.

### `pick`

Broadcast by fbcoord whenever the pool's visible coordinated pick changes, and also sent immediately after successful `hello`.

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

- `version`: monotonic pool pick version
- `upstream`: selected upstream tag, or `null` when there is no coordinated pick

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
- `invalid_pool`
- `missing_hello`

Connection behavior:

- `invalid_json`: socket stays open
- `missing_hello`: socket stays open
- `invalid_pool`: socket is closed with code `1008`

### Session lifecycle

Normal lifecycle:

1. Client opens `GET /ws/node?pool=<pool>` with Bearer token.
2. Server upgrades the request.
3. Client sends `hello`.
4. Server sends the current `pick`.
5. Client sends `preferences`.
6. Server recomputes and broadcasts `pick` if the visible pool result changes.
7. Client continues sending `heartbeat`.
8. On disconnect or stale eviction, the node is removed from the pool state.

If a node reconnects with the same `node_id`, the old socket is closed with code `1012` and reason `replaced`.

---

## 5.3.3 Selection algorithm

fbcoord selects one coordinated upstream per pool from the submitted preference lists of all active nodes in that pool.

Current algorithm:

1. If there are no active nodes, return `null`.
2. If any active node submitted an empty list, return `null`.
3. Compute the intersection of all submitted upstream lists.
4. If the intersection is empty, return `null`.
5. For each shared upstream, compute aggregate rank as the sum of its zero-based index in each node's list.
6. Choose the shared upstream with the lowest aggregate rank.
7. If multiple upstreams tie on aggregate rank, choose the lexicographically smallest tag.

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

The selector is deterministic. The same set of submitted lists produces the same pick.

---

## 5.3.4 Pool state and lifecycle

### Pool state model

Each pool tracks:

- active nodes keyed by `node_id`
- each node's last submitted `upstreams`
- each node's `active_upstream`
- `last_seen`
- `connected_at`
- current visible `pick { version, upstream }`

The admin UI reads this state through internal Worker-to-Durable-Object queries exposed as `GET /state`.

### Pool registration

Pool visibility is lifecycle-driven:

- when node count transitions from `0 -> 1`, the pool is registered as active
- when node count transitions from `1 -> 0`, the pool is deregistered

That registry state drives the dashboard pool list.

### Stale eviction

If a node does not refresh `last_seen` within 30 seconds, it is evicted from pool state. Eviction removes:

- the node snapshot
- its live connection
- its contribution to the selector

If eviction changes the visible pick, fbcoord broadcasts a new `pick`.

### Version semantics

Pool pick versions start at:

```json
{ "version": 0, "upstream": null }
```

The version increments only when the visible `upstream` value changes. It does not increment when:

- node membership changes but the selected upstream stays the same
- a node resubmits the same preference list
- a new node joins and the visible pick remains unchanged

It does increment when the visible pick changes in either direction:

- `null -> "tag"`
- `"tag-a" -> "tag-b"`
- `"tag" -> null`

### Reconnect and redeploy expectations

Reconnect semantics:

- same `node_id` within a pool replaces the older live connection
- the replacement connection receives the current pick immediately after `hello`

Redeploy semantics:

- Worker redeploys drop live WebSocket sessions
- nodes are expected to reconnect and resend `hello` plus `preferences`
- pool state repopulates from those reconnects

### Admin API state shape

Current admin HTTP state surfaces mirror this pool model:

- `GET /api/pools` returns pool summaries with `name`, `node_count`, and `pick`
- `GET /api/pools/:pool` returns `pool`, `pick`, `node_count`, and per-node detail with:
  - `node_id`
  - `upstreams`
  - `active_upstream`
  - `last_seen`
  - `connected_at`

These admin routes are for operator visibility. They are separate from the node coordination WebSocket protocol itself.
