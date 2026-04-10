# fbnotify API reference

This document describes the public fbnotify HTTP surface. For deployment,
operation, and troubleshooting, see the [fbnotify user guide](user-guide.md).

---

## Overview

fbnotify exposes three classes of public endpoints:

- unauthenticated health and UI routes
- operator login and session-backed admin APIs
- authenticated event ingress through `POST /v1/events`

Current route summary:

| Route | Method | Auth | Purpose |
|-------|--------|------|---------|
| `/` | GET | none | Admin UI shell |
| `/healthz` | GET | none | Worker health check |
| `/v1/events` | POST | node token + HMAC | Accept a notification event for asynchronous delivery |
| `/api/auth/login` | POST | operator token | Create an operator session |
| `/api/auth/check` | GET | session | Validate the current operator session |
| `/api/auth/logout` | POST | session | Clear the current operator session |
| `/api/token/info` | GET | session | Return masked operator-token metadata |
| `/api/token/rotate` | POST | session + current operator token | Rotate the operator token |
| `/api/node-tokens` | GET | session | List node tokens |
| `/api/node-tokens` | POST | session | Mint a node token |
| `/api/node-tokens/:key_id` | DELETE | session | Revoke a node token |
| `/api/targets` | GET | session | List provider targets |
| `/api/targets` | POST | session | Create a provider target |
| `/api/targets/:id` | PUT | session | Update a provider target |
| `/api/targets/:id` | DELETE | session | Delete a provider target |
| `/api/routes` | GET | session | List routes |
| `/api/routes` | POST | session | Create a route |
| `/api/routes/:id` | PUT | session | Update a route |
| `/api/routes/:id` | DELETE | session | Delete a route |
| `/api/test-send` | POST | session | Deliver a test event immediately |
| `/api/capture/messages` | GET | session | List capture messages |
| `/api/capture/clear` | POST | session | Clear the capture inbox |

---

## Authentication and session model

fbnotify uses three distinct credentials:

- operator token: used only with `POST /api/auth/login`
- session cookie: used for authenticated `/api/*` routes after login
- node token: used only with `POST /v1/events`

Current session behavior:

- cookie name: `fbnotify_session`
- TTL: 24 hours
- flags: `HttpOnly`, `Path=/`, `SameSite=Strict`
- `Secure` is added when the request is over HTTPS

Current same-origin protection applies to these state-changing admin routes:

- `POST /api/auth/login`
- `POST /api/auth/logout`
- `POST /api/token/rotate`
- `POST /api/node-tokens`
- `DELETE /api/node-tokens/:key_id`
- `POST /api/targets`
- `PUT /api/targets/:id`
- `DELETE /api/targets/:id`
- `POST /api/routes`
- `PUT /api/routes/:id`
- `DELETE /api/routes/:id`
- `POST /api/test-send`
- `POST /api/capture/clear`

If an `Origin` header is present and does not match the request origin,
fbnotify returns:

```json
{
  "error": "forbidden"
}
```

with HTTP status `403`.

---

## Public endpoints

### `GET /healthz`

Unauthenticated health check.

Success response:

```text
ok
```

### `GET /`

Unauthenticated UI shell.

Current behavior:

- `/` serves `index.html` from the Worker asset bundle
- non-API paths also fall through to the asset bundle when `ASSETS` is bound
- API access still requires login

---

## Event ingress

### `POST /v1/events`

Accept a notification event for asynchronous delivery.

Authentication headers:

- `X-FBNotify-Key-Id`
- `X-FBNotify-Timestamp`
- `X-FBNotify-Signature`

Current signing input:

```text
<timestamp> + "." + <raw-json-body>
```

The signature is an HMAC-SHA256 over that exact string, using the raw node
token as the secret.

Required JSON envelope:

```json
{
  "schema_version": 1,
  "event_name": "upstream.active_changed",
  "severity": "warn",
  "timestamp": "2026-04-09T00:00:00Z",
  "source": {
    "service": "fbforward",
    "instance": "node-1"
  },
  "attributes": {
    "switch.reason": "failover_loss"
  }
}
```

Current validation rules:

- `schema_version` must be a positive integer
- `event_name` must be a non-empty string matching `[A-Za-z0-9._:-]{1,128}`
- `severity` must be `info`, `warn`, or `critical`
- `timestamp` must be a non-empty string or a finite number
- `source` must be an object containing `service` and `instance`
- `source.service` and `source.instance` must match
  `[A-Za-z0-9._:-]{1,128}`
- `attributes` must be an object
- `X-FBNotify-Timestamp` must be an integer epoch timestamp in seconds
- the timestamp skew must be within 300 seconds
- the `key_id` must exist
- the bound node-token source tuple must match `source.service` and
  `source.instance`

Success response:

```json
{
  "accepted": true,
  "target_count": 2
}
```

Current success semantics:

- status code is `202`
- delivery continues asynchronously after the response
- `target_count` may be `0` when no route matches
- acceptance does not guarantee provider success

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"..."}` for invalid envelope fields
- `401 {"error":"missing ingress authentication fields"}`
- `401 {"error":"timestamp must be epoch seconds"}`
- `401 {"error":"stale timestamp"}`
- `401 {"error":"unknown key id"}`
- `401 {"error":"source mismatch"}`
- `401 {"error":"invalid signature"}`

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

Success also sets the `fbnotify_session` cookie.

Current error cases:

- `401 {"error":"invalid token"}`
- `403 {"error":"forbidden"}`

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

Success also clears the `fbnotify_session` cookie.

Current error cases:

- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

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

Required body field:

- `current_token`: must match the current operator token

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

Success response when supplying a custom token:

```json
{
  "masked_prefix": "abcd1234...",
  "created_at": 1735689600000
}
```

Current behavior:

- replacement tokens must satisfy the current token-format checks
- the generated token is returned once
- custom replacement tokens are not echoed back
- rotating the operator token does not invalidate the current session

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
      "key_id": "L9u2v3...",
      "source_service": "fbforward",
      "source_instance": "node-1",
      "masked_prefix": "abcd1234...",
      "created_at": 1735689600000,
      "last_used_at": 1735689660000
    }
  ]
}
```

Current behavior:

- tokens are sorted by `source_service`, then `source_instance`
- the raw node token is never returned from the list endpoint

Current error cases:

- `401 {"error":"unauthorized"}`

### `POST /api/node-tokens`

Mint a node token for one source tuple.

Request body:

```json
{
  "source_service": "fbforward",
  "source_instance": "node-1"
}
```

Success response:

```json
{
  "key_id": "L9u2v3...",
  "token": "raw-node-token",
  "info": {
    "key_id": "L9u2v3...",
    "source_service": "fbforward",
    "source_instance": "node-1",
    "masked_prefix": "abcd1234...",
    "created_at": 1735689600000,
    "last_used_at": null
  }
}
```

Current behavior:

- one active token may exist for each `(source_service, source_instance)` tuple
- both identifiers must match `[A-Za-z0-9._:-]{1,128}`
- the raw `token` is returned exactly once

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"..."}` for invalid identifiers or duplicate source tuple
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

### `DELETE /api/node-tokens/:key_id`

Revoke a node token by `key_id`.

Success response:

```json
{
  "ok": true
}
```

Current error cases:

- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`
- `404 {"error":"node token not found"}`

---

## Provider-target endpoints

### Target summary shape

List, create, and update responses return a summary record:

```json
{
  "id": "tgt_abc123",
  "name": "primary webhook",
  "type": "webhook",
  "created_at": 1735689600000,
  "updated_at": 1735689600000,
  "summary": {
    "url_host": "hooks.example.com",
    "url_path": "/notify"
  }
}
```

Current summary behavior by target type:

- `webhook`: `summary.url_host` and `summary.url_path`
- `pushover`: masked `api_token`, masked `user_key`, and optional `device`
- `capture`: empty `summary`

### Target config shapes

Webhook target:

```json
{
  "name": "primary webhook",
  "type": "webhook",
  "config": {
    "url": "https://hooks.example.com/notify"
  }
}
```

Pushover target:

```json
{
  "name": "on-call",
  "type": "pushover",
  "config": {
    "api_token": "pushover-app-token",
    "user_key": "pushover-user-key",
    "device": "iphone"
  }
}
```

Capture target:

```json
{
  "name": "capture",
  "type": "capture",
  "config": {}
}
```

### `GET /api/targets`

List provider targets.

Success response:

```json
{
  "targets": [
    {
      "id": "tgt_abc123",
      "name": "capture",
      "type": "capture",
      "created_at": 1735689600000,
      "updated_at": 1735689600000,
      "summary": {}
    }
  ]
}
```

Current error cases:

- `401 {"error":"unauthorized"}`

### `POST /api/targets`

Create a provider target.

Success response uses the target summary shape.

Current validation behavior:

- `name` is required and limited to 128 characters
- `type` must be one of `webhook`, `pushover`, or `capture`
- webhook URLs must use `http` or `https`
- Pushover `api_token` and `user_key` are required
- capture targets use an empty config object

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"..."}` for invalid target data
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

### `PUT /api/targets/:id`

Update a provider target.

Request body shape:

```json
{
  "name": "new target name",
  "config": {
    "url": "https://hooks.example.com/new-path"
  }
}
```

Current update behavior:

- omitted config fields keep their existing values
- Pushover `device` may be cleared by sending `null` or an empty string
- target `type` is not changeable

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"..."}` for unknown targets or invalid config
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

### `DELETE /api/targets/:id`

Delete a provider target.

Success response:

```json
{
  "ok": true
}
```

Current error cases:

- `400 {"error":"target is still referenced by a route"}`
- `400 {"error":"target not found"}`
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

---

## Route endpoints

### Route summary shape

List, create, and update responses return a route summary:

```json
{
  "id": "route_abc123",
  "name": "default",
  "source_service": null,
  "event_name": null,
  "target_ids": ["tgt_abc123"],
  "created_at": 1735689600000,
  "updated_at": 1735689600000,
  "match_kind": "global_default"
}
```

Current `match_kind` values:

- `global_default`
- `service_default`
- `event`
- `service_event`

Current precedence:

1. `service_event`
2. `event`
3. `service_default`
4. `global_default`

### `GET /api/routes`

List routes.

Success response:

```json
{
  "routes": [
    {
      "id": "route_abc123",
      "name": "default",
      "source_service": null,
      "event_name": null,
      "target_ids": ["tgt_abc123"],
      "created_at": 1735689600000,
      "updated_at": 1735689600000,
      "match_kind": "global_default"
    }
  ]
}
```

Current error cases:

- `401 {"error":"unauthorized"}`

### `POST /api/routes`

Create a route.

Request body:

```json
{
  "name": "fbforward failover",
  "source_service": "fbforward",
  "event_name": "upstream.active_changed",
  "target_ids": ["tgt_abc123", "tgt_def456"]
}
```

Current validation behavior:

- `name` is required
- `target_ids` must be a non-empty array
- all target IDs must exist
- `source_service` and `event_name` use the same identifier rules as event
  ingress
- only one route may exist for a given match scope

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"..."}` for invalid route data
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

### `PUT /api/routes/:id`

Update a route.

Request body:

```json
{
  "name": "fbforward failover",
  "source_service": "fbforward",
  "event_name": "upstream.active_changed",
  "target_ids": ["tgt_abc123"]
}
```

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"..."}` for unknown routes, invalid targets, or duplicate
  match scopes
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

### `DELETE /api/routes/:id`

Delete a route.

Success response:

```json
{
  "ok": true
}
```

Current error cases:

- `400 {"error":"route not found"}`
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`

---

## Test and capture endpoints

### Delivery result shape

`POST /api/test-send` returns one delivery result per resolved target:

```json
{
  "ok": false,
  "status": 500,
  "target_id": "tgt_abc123",
  "target_name": "primary webhook",
  "target_type": "webhook",
  "error": "webhook responded with 500"
}
```

### `POST /api/test-send`

Validate and deliver a test event immediately.

Request body:

```json
{
  "event": {
    "schema_version": 1,
    "event_name": "demo.test",
    "severity": "info",
    "timestamp": "2026-04-09T00:00:00Z",
    "source": {
      "service": "manual",
      "instance": "dashboard"
    },
    "attributes": {
      "note": "hello from fbnotify"
    }
  },
  "target_ids": ["tgt_abc123"]
}
```

If `target_ids` is omitted or empty, fbnotify resolves targets from the current
route table.

Success response:

```json
{
  "target_count": 1,
  "results": [
    {
      "ok": true,
      "status": 200,
      "target_id": "tgt_abc123",
      "target_name": "capture",
      "target_type": "capture"
    }
  ]
}
```

Current error cases:

- `400 {"error":"invalid json"}`
- `400 {"error":"..."}` for invalid event data
- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`
- current target-resolution failures may surface as Worker `500`

### Capture message shape

`GET /api/capture/messages` returns capture records with this shape:

```json
{
  "id": "msg_abc123",
  "target_id": "tgt_abc123",
  "target_name": "capture",
  "target_type": "capture",
  "event_name": "demo.capture",
  "severity": "warn",
  "source_service": "fbforward",
  "source_instance": "node-1",
  "received_at": 1735689600000,
  "payload": "WARN demo.capture\nservice=fbforward\n..."
}
```

Current behavior:

- messages are returned newest first
- the inbox retains at most 200 messages

### `GET /api/capture/messages`

List capture messages.

Success response:

```json
{
  "messages": [
    {
      "id": "msg_abc123",
      "target_id": "tgt_abc123",
      "target_name": "capture",
      "target_type": "capture",
      "event_name": "demo.capture",
      "severity": "warn",
      "source_service": "fbforward",
      "source_instance": "node-1",
      "received_at": 1735689600000,
      "payload": "WARN demo.capture\nservice=fbforward\n..."
    }
  ]
}
```

Current error cases:

- `401 {"error":"unauthorized"}`

### `POST /api/capture/clear`

Clear the capture inbox.

Success response:

```json
{
  "ok": true
}
```

Current error cases:

- `401 {"error":"unauthorized"}`
- `403 {"error":"forbidden"}`
