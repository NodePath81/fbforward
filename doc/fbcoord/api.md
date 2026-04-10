# fbcoord API reference

This document describes the public fbcoord HTTP surface. For deployment,
operation, and troubleshooting, see the [fbcoord user guide](user-guide.md).
For the node WebSocket message contract, selector, and lifecycle semantics, see
the [fbcoord protocol reference](protocol.md).

---

## Overview

fbcoord exposes three classes of public endpoints:

- unauthenticated health and UI routes
- operator login and session-backed admin APIs
- node participation through `GET /ws/node`

Current route summary:

| Route | Method | Auth | Purpose |
|-------|--------|------|---------|
| `/` | GET | none | Admin UI shell |
| `/healthz` | GET | none | Worker health check |
| `/ws/node` | GET upgrade | node token | Node participation endpoint |
| `/api/auth/login` | POST | operator token | Create an operator session |
| `/api/auth/check` | GET | session | Validate current operator session |
| `/api/auth/logout` | POST | session | Clear the current operator session |
| `/api/state` | GET | session | Return the global coordination state |
| `/api/token/info` | GET | session | Return masked operator-token metadata |
| `/api/token/rotate` | POST | session + current operator token | Rotate the operator token |
| `/api/node-tokens` | GET | session | List node tokens |
| `/api/node-tokens` | POST | session | Mint a node token |
| `/api/node-tokens/:node_id` | DELETE | session | Revoke a node token |

---

## Authentication and session model

fbcoord uses three distinct credentials:

- operator token: used only with `POST /api/auth/login`
- session cookie: used for authenticated `/api/*` requests after login
- node token: used only for `GET /ws/node`

Current session behavior:

- cookie name: `fbcoord_session`
- TTL: 24 hours
- flags: `HttpOnly`, `Path=/`, `SameSite=Strict`
- `Secure` is added when the request is over HTTPS

For state-changing admin routes, current browser-origin protection is:

- `POST /api/auth/login`
- `POST /api/auth/logout`
- `POST /api/token/rotate`
- `POST /api/node-tokens`
- `DELETE /api/node-tokens/:node_id`

If an `Origin` header is present and does not match the request origin, fbcoord
returns `403 {"error":"forbidden"}`.

fbcoord also applies auth rate limiting to:

- `POST /api/auth/login`
- `GET /ws/node`

When blocked, current behavior is `429 Too Many Requests` and may include
`Retry-After`.

---

## Public endpoints

### `GET /healthz`

Unauthenticated health check.

Success response:

```text
ok
```

### `GET /ws/node`

Node participation upgrade endpoint.

Authentication:

- request must send `Authorization: Bearer <node-token>`
- valid node token upgrades to WebSocket
- operator token is rejected with `401 Unauthorized`
- invalid or missing token is rejected with `401 Unauthorized`
- rate-limited clients receive `429 Too Many Requests`

Compatibility behavior:

- any `pool` query parameter is ignored
- node identity is derived from the validated token, not client-supplied
  `node_id`

After upgrade:

- the first node message must be `hello`
- the current wire contract is documented in
  [protocol.md](protocol.md#532-message-reference)

---

## Session endpoints

### `POST /api/auth/login`

Create an operator session from the operator token.

Request body:

```json
{
  "token": "operator-token"
}
```

Success response:

```json
{
  "ok": true
}
```

Success also sets the `fbcoord_session` cookie.

Current error cases:

- `400 {"error":"invalid json"}`
- `401 {"error":"invalid token"}`
- `403 {"error":"forbidden"}` for origin mismatch
- `429 {"error":"too many requests"}`

### `GET /api/auth/check`

Validate the current operator session.

Success response:

```json
{
  "ok": true
}
```

Current error cases:

- `401 {"error":"unauthorized"}`

### `POST /api/auth/logout`

Clear the current operator session.

Success response:

```json
{
  "ok": true
}
```

Success also clears the `fbcoord_session` cookie.

Current error cases:

- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

---

## Admin state endpoint

### `GET /api/state`

Return the deployment-wide coordination state and presence roster.

Success response shape:

```json
{
  "pick": {
    "version": 12,
    "upstream": "us-a"
  },
  "node_count": 2,
  "counts": {
    "online": 2,
    "offline": 0,
    "aborted": 0,
    "never_seen": 1
  },
  "nodes": [
    {
      "node_id": "node-1",
      "status": "online",
      "first_seen_at": 1735689600000,
      "last_connected_at": 1735689600000,
      "last_seen_at": 1735689660000,
      "disconnected_at": null,
      "upstreams": ["us-a", "eu-b"],
      "active_upstream": "us-a"
    }
  ]
}
```

Field semantics:

- `pick`: current visible coordinated pick
- `node_count`: count of `online` nodes only
- `counts`: current roster counts for `online`, `offline`, `aborted`, and
  `never_seen`
- `nodes`: all provisioned node IDs, sorted by `node_id`

Node entry fields:

- `node_id`: operator-chosen identity bound to the node token
- `status`: `online`, `offline`, `aborted`, or `never_seen`
- `first_seen_at`: first successful session start, or `null` for `never_seen`
- `last_connected_at`: most recent successful handshake, or `null` for
  `never_seen`
- `last_seen_at`: most recent heartbeat or session activity, or `null` for
  `never_seen`
- `disconnected_at`: last clean or abnormal disconnect time, or `null` for
  current `online` nodes and `never_seen`
- `upstreams`: last submitted preference list for `online` nodes, otherwise `[]`
- `active_upstream`: current node-reported active upstream for `online` nodes,
  otherwise `null`

Current synthesis behavior:

- `never_seen` is derived by joining provisioned node tokens with the
  persisted/live roster

Current error cases:

- `401 {"error":"unauthorized"}`

---

## Operator-token endpoints

### `GET /api/token/info`

Return current masked operator-token metadata.

Success response:

```json
{
  "masked_prefix": "abcd1234...",
  "created_at": 1735689600000
}
```

Current error cases:

- `401 {"error":"unauthorized"}`

### `POST /api/token/rotate`

Rotate the operator token.

Required body fields:

- `current_token`: must match the current operator token

Supported modes:

Generate a new random token:

```json
{
  "current_token": "current-operator-token",
  "generate": true
}
```

Set a custom replacement token:

```json
{
  "current_token": "current-operator-token",
  "token": "custom-replacement-token"
}
```

Success response when generating:

```json
{
  "masked_prefix": "abcd1234...",
  "created_at": 1735689600000,
  "token": "new-random-operator-token"
}
```

Success response when submitting a custom token:

```json
{
  "masked_prefix": "abcd1234...",
  "created_at": 1735689600000
}
```

Current validation behavior:

- custom or generated operator tokens must pass the current token-format checks
- the raw generated token is returned once
- custom replacement tokens are not echoed back
- rotating the operator token does not revoke node tokens and does not invalidate
  the current session cookie

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"..."}` for invalid replacement token format
- `401 {"error":"invalid current token"}`
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

---

## Node-token endpoints

### `GET /api/node-tokens`

List provisioned node tokens.

Success response:

```json
{
  "tokens": [
    {
      "node_id": "node-1",
      "masked_prefix": "abcd1234...",
      "created_at": 1735689600000,
      "last_used_at": 1735689660000
    }
  ]
}
```

Current behavior:

- sorted by `node_id`
- raw token values are never returned by this endpoint

Current error cases:

- `401 {"error":"unauthorized"}`

### `POST /api/node-tokens`

Mint a new node token bound to a specific `node_id`.

Request body:

```json
{
  "node_id": "node-1"
}
```

Success response:

```json
{
  "token": "raw-node-token",
  "info": {
    "node_id": "node-1",
    "masked_prefix": "abcd1234...",
    "created_at": 1735689600000,
    "last_used_at": null
  }
}
```

Current validation behavior:

- `node_id` must be non-empty
- `node_id` must be at most 128 characters
- duplicate `node_id` returns conflict
- the raw node token is returned once at creation time

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"invalid node_id"}` or another validation error
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`
- `409 {"error":"node_id already exists"}`

### `DELETE /api/node-tokens/:node_id`

Revoke an existing node token.

Success response:

```json
{
  "ok": true
}
```

Current behavior:

- revocation blocks future authentication and reconnects
- any live WebSocket for that `node_id` is closed
- the node's roster entry is removed from `/api/state`

Current error cases:

- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`
- `404 {"error":"node_id not found"}`

---

## Notes on consistency

Current implementation boundaries:

- `api.md` is authoritative for HTTP/admin endpoints and auth/session behavior
- `protocol.md` is authoritative for node WebSocket messages, session
  lifecycle, selector behavior, and roster semantics
- `user-guide.md` is authoritative for deployment, migration, operator
  workflows, and troubleshooting
