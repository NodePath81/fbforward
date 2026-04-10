# fbnotify user guide

This guide covers fbnotify deployment, operation, and troubleshooting. For
request and response shapes, see the [fbnotify API reference](api.md).

---

## Overview

### What fbnotify does

fbnotify is a standalone Cloudflare Worker notification bridge. It accepts
authenticated notification events, resolves those events to one or more
configured provider targets, and delivers them best-effort without becoming a
full alert-policy engine.

The current implementation also includes a built-in admin UI and admin API for:

- provider-target management
- routing configuration
- operator-token rotation
- emitter node-token lifecycle management
- provider test-send workflows
- capture-inbox inspection and clearing

fbnotify is currently implemented as a standalone service. It does not yet
modify `fbforward`, `fbcoord`, or coordlab in this repository.

### Authentication model

fbnotify uses three credential classes:

- operator token: used only for `POST /api/auth/login`
- session cookie: used for authenticated `/api/*` requests after login
- node token: used only for `POST /v1/events`

Each node token is bound to one `(source.service, source.instance)` tuple. A
valid token cannot be reused for a different source tuple.

### Deployment model

fbnotify runs as a Cloudflare Worker backed by Durable Objects:

- one configuration Durable Object stores provider targets and routes
- one token Durable Object stores the operator-token verifier, the stable
  session-signing secret, and node tokens
- one capture Durable Object stores the built-in capture inbox used for test
  workflows

The admin UI is served from Worker assets at `/`.

---

## Deployment and configuration

### Prerequisites

You need:

- a Cloudflare account with Workers and Durable Objects enabled
- `wrangler`
- Node.js and `npm`

### Build and deploy

From the repository root:

```bash
npm --prefix fbnotify install
npm --prefix fbnotify run build
```

Then deploy from `fbnotify/`:

```bash
cd fbnotify
wrangler secret put FBNOTIFY_OPERATOR_TOKEN
wrangler secret put FBNOTIFY_TOKEN_PEPPER
wrangler deploy
```

The current Worker configuration in
[fbnotify/wrangler.toml](../../fbnotify/wrangler.toml) binds:

- `FBNOTIFY_CONFIG`
- `FBNOTIFY_TOKEN_STORE`
- `FBNOTIFY_CAPTURE`
- `ASSETS`

The Worker runtime in [fbnotify/src/worker.ts](../../fbnotify/src/worker.ts)
also requires two secrets:

- `FBNOTIFY_OPERATOR_TOKEN`
- `FBNOTIFY_TOKEN_PEPPER`

Do not rename those bindings or secrets unless the Worker code changes with
them.

### Bootstrap operator token

Generate the bootstrap operator token out of band:

```bash
openssl rand -hex 32
```

Use that value as the Worker secret `FBNOTIFY_OPERATOR_TOKEN`. Set a separate
Worker secret `FBNOTIFY_TOKEN_PEPPER` for slow verifier derivation.

On first use, fbnotify seeds its operator-token record from
`FBNOTIFY_OPERATOR_TOKEN`. After that:

- the token verifier is persisted in Durable Object state
- the verifier is derived with PBKDF2 plus a per-record salt and the Worker
  pepper
- replacement tokens must be at least 32 characters and pass the current
  format checks
- the full operator token is not shown again after rotation unless a new token
  is generated in the rotation response

Because the rotated operator token is stored in Durable Object state, normal
Worker restarts and redeployments do not revert it. You should still update
`FBNOTIFY_OPERATOR_TOKEN` after rotation so bootstrap state stays correct if
the token store is reprovisioned from empty storage.

### Verify the deployment

Check the health endpoint:

```bash
curl -i https://fbnotify.example.workers.dev/healthz
```

Expected response:

```text
HTTP/2 200
...

ok
```

Then open `/` in a browser and log in with the operator token.

---

## Operation and web UI

### Public routes

Current routes:

| Route | Method | Auth | Purpose |
|-------|--------|------|---------|
| `/` | GET | none | Admin UI shell |
| `/healthz` | GET | none | Worker health check |
| `/v1/events` | POST | node token + HMAC | Event ingress |
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
| `/api/test-send` | POST | session | Send a test event immediately |
| `/api/capture/messages` | GET | session | List captured messages |
| `/api/capture/clear` | POST | session | Clear the capture inbox |

### Session behavior

Operators log in with the operator token. On success, fbnotify sets a session
cookie:

- cookie name: `fbnotify_session`
- lifetime: 24 hours
- flags: `HttpOnly`, `Path=/`, `SameSite=Strict`
- `Secure` is added when the request is served over HTTPS

The current session remains valid after operator-token rotation because session
signing is stored separately from the operator-token verifier.

### UI structure

The built-in UI is a single-page admin shell with one login route and one main
dashboard route:

- `#/login`
- `#/`

The dashboard includes these sections:

- provider targets
- routing
- emitter node tokens
- operator token
- provider test send
- capture inbox

### Provider targets

Current target types:

- `webhook`: sends the event JSON to the configured URL
- `pushover`: sends a formatted message through the Pushover API
- `capture`: writes the formatted message into the built-in capture inbox

Operational notes:

- stored credentials are not returned after create or update
- webhook summaries show only host and path
- Pushover summaries show masked token prefixes
- capture targets do not require external credentials

### Routing

Each route maps a scope to one or more provider targets.

Current scopes:

- global default: `source_service = null`, `event_name = null`
- service default: `source_service = <value>`, `event_name = null`
- event-specific: `source_service = null`, `event_name = <value>`
- service-and-event specific: both fields set

Current precedence:

1. exact `source.service + event_name`
2. exact `event_name`
3. service default
4. global default

Current constraints:

- each match scope can exist only once
- `target_ids` must be a non-empty array of existing targets
- deleting a target fails while any route still references it

### Node-token management

Operators mint one active node token per `(source.service, source.instance)`
tuple.

Current behavior:

- the raw node token is returned exactly once at creation time
- later reads show only `key_id`, source tuple, masked prefix, creation time,
  and last-used time
- the source tuple must use 1 to 128 characters from
  `[A-Za-z0-9._:-]`
- creating a second token for the same source tuple fails until the original
  token is revoked

Revocation is keyed by `key_id`, not by source tuple.

### Event ingress behavior

Event emitters send JSON to `POST /v1/events` and authenticate with:

- `X-FBNotify-Key-Id`
- `X-FBNotify-Timestamp`
- `X-FBNotify-Signature`

The signature input is:

```text
timestamp.raw_separator_body := <timestamp> + "." + <raw-json-body>
```

Current ingress behavior:

- requests with invalid JSON or invalid envelope fields return `400`
- requests with unknown keys, stale timestamps, source mismatches, or bad
  signatures return `401`
- accepted requests return `202 {"accepted":true,"target_count":N}`
- provider delivery runs best-effort after acceptance
- `target_count` may be `0` if no route matches the event

Accepted ingress does not mean delivery succeeded.

### Capture inbox

The built-in capture target is the primary test oracle for the standalone
service.

Current behavior:

- captured messages are stored newest first
- the inbox retains at most 200 messages
- `POST /api/capture/clear` removes all captured messages
- capture entries store the rendered message text, not the raw provider
  request body

### Operator-token rotation

Operators can rotate the operator token in two modes:

- generate a new random token
- provide a custom replacement token

Current rotation behavior:

- the new operator token takes effect immediately for future logins
- the previous operator token is invalidated immediately
- rotation requires the current operator token even for an already-authenticated
  session
- the generated token is shown once in the response
- custom replacement tokens are not echoed back
- current operator sessions stay valid until expiry
- node tokens are unaffected

---

## Troubleshooting

### `/healthz` is healthy but no notifications are arriving

Common causes:

- no matching route exists for the event
- the route points to the wrong provider target
- the event was accepted with `target_count = 0`
- provider delivery is failing after acceptance

Check the capture inbox first. If the event reaches a capture target, routing
worked and the issue is with the external provider target.

### `POST /v1/events` returns `401`

Common causes:

- the `X-FBNotify-Key-Id` value is unknown
- the timestamp is more than 300 seconds away from server time
- the signature was computed from a different body
- the event `source.service` or `source.instance` does not match the bound node
  token

Verify the raw JSON body, the timestamp, and the source tuple before
recomputing the signature.

### `POST /v1/events` returns `400`

Common causes:

- the request body is not valid JSON
- `schema_version` is missing or not a positive integer
- `severity` is not one of `info`, `warn`, or `critical`
- `source` is missing or malformed
- `attributes` is not an object

Validate the event envelope against the current contract before retrying.

### Admin writes return `403 {"error":"forbidden"}`

fbnotify applies same-origin checks to browser-style state-changing admin
routes. If an `Origin` header is present and does not match the request origin,
fbnotify rejects the request.

This most often happens when:

- the UI is opened from a different origin than the API
- a browser extension or proxy rewrites the request origin

### Creating or updating a target returns `400`

Common causes:

- invalid or empty target name
- unsupported target type
- webhook URL is missing or not `http` or `https`
- Pushover `api_token` or `user_key` is missing

Remember that capture targets require an empty config object.

### Creating or updating a route returns `400`

Common causes:

- `target_ids` is empty
- `target_ids` references an unknown target
- a route for the same match scope already exists
- `source_service` or `event_name` contains unsupported characters

### Deleting a target returns `400`

Current target deletion fails while any route still references that target.
Delete or update the dependent routes first, then retry the target deletion.
