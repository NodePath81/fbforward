# fbcoord user guide

This guide covers fbcoord deployment, operation, and troubleshooting. For
request/response shapes, see the [fbcoord API reference](api.md). For the node
wire contract and selector details, see the
[fbcoord protocol reference](protocol.md).

---

## 3.4.1 Overview

### What fbcoord does

fbcoord is an optional coordination service for multi-node fbforward
deployments. Each fbforward node connects to fbcoord, submits its local
upstream preference list in best-first order, and receives one coordinated
upstream pick shared by every node connected to that fbcoord deployment.

fbcoord does not replace the local fbforward control plane. Operators still use
each fbforward node's own RPC API and Web UI for mode changes, local status,
and manual overrides. fbcoord only provides shared pick coordination across
nodes.

### Authentication model

fbcoord uses two credential classes:

- operator token: used only for Web UI login and admin API access
- node tokens: used only for `GET /ws/node`

Each node token is bound to one operator-chosen `node_id`. That `node_id` is
derived server-side from the validated token. The node does not self-report its
identity.

### Deployment model

fbcoord runs as a Cloudflare Worker backed by Durable Objects:

- one global coordination-state Durable Object stores live node state and the
  current coordinated pick
- one token Durable Object stores the operator token record, node-token
  records, and the session signing secret
- one auth-guard Durable Object family enforces authoritative auth-rate limits
  and active bans
- one registry Durable Object binding remains in Wrangler config for migration
  stability, but it is not used by the current runtime

### Coordination behavior

At a high level:

1. A node opens `GET /ws/node` with `Authorization: Bearer <node-token>`.
2. The node sends `hello` and waits for `ready`.
3. The node sends `preferences`, then periodic `heartbeat` messages.
4. fbcoord computes one coordinated pick across all online nodes and
   broadcasts `pick` updates.
5. To exit coordination cleanly, the node sends `bye` and waits for
   `closing`.
6. If there is no shared candidate, fbcoord returns `upstream: null`.

### What the Web UI covers

The built-in admin UI provides:

- a single dashboard showing the current coordinated pick, node count, status
  counts, and per-node detail
- operator token info and rotation
- node-token mint, list, and revoke actions

The UI does not override picks, evict nodes, or push configuration.

### Notification integration

fbcoord can optionally emit outbound notification events to `fbnotify` when
the `FBNOTIFY_*` Worker bindings are configured. The current emitted event set
is documented in the [notification event reference](../notification-events.md).

---

## 3.4.2 Deployment and configuration

### Prerequisites

You need:

- a Cloudflare account with Workers and Durable Objects enabled
- `wrangler`
- Node.js and `npm`

### Build and deploy

From the repository root:

```bash
npm --prefix fbcoord install
npm --prefix fbcoord run build
wrangler secret put FBCOORD_TOKEN
wrangler secret put FBCOORD_TOKEN_PEPPER
wrangler deploy
```

The current Worker configuration in
[fbcoord/wrangler.toml](../../fbcoord/wrangler.toml) binds:

- `FBCOORD_POOL`
- `FBCOORD_REGISTRY`
- `FBCOORD_TOKEN_STORE`
- `FBCOORD_AUTH_GUARD`
- `FBCOORD_AUTH_KV`
- `ASSETS`

Do not rename those bindings unless the Worker code changes with them.

### Generate the initial operator token

Generate the bootstrap operator token out of band:

```bash
openssl rand -hex 32
```

Use that value in two places:

1. Set it as the Worker secret `FBCOORD_TOKEN`
2. Set a distinct Worker secret `FBCOORD_TOKEN_PEPPER` for slow token
   verification

On first use, fbcoord seeds its operator-token record from `FBCOORD_TOKEN`.
After that:

- the operator token is persisted in a Durable Object
- the verifier is derived with PBKDF2 plus a per-record salt and a Worker-side
  pepper
- generated or custom replacement tokens must be at least 32 characters
- the full current operator token is never displayed again after rotation

Because the rotated token is stored in Durable Object state, normal Worker
restarts and redeployments do not revert to the old secret value. You should
still update `FBCOORD_TOKEN` after rotation so bootstrap state stays correct if
the token store is ever reprovisioned from empty storage.

### Node-token provisioning and migration

Node tokens are not seeded from `FBCOORD_TOKEN`. Operators create them after
deployment through the Web UI or admin API.

Hard-cutover migration behavior:

- existing operator login with `FBCOORD_TOKEN` continues to work after upgrade
- existing fbforward nodes that still use `FBCOORD_TOKEN` as
  `coordination.token` lose coordination connectivity after upgrade
- those nodes remain disconnected from fbcoord until the operator mints a
  per-node token and updates each node's configuration

Recommended migration workflow:

1. Deploy the upgraded Worker.
2. Log in with the existing operator token.
3. Create one node token per fbforward node.
4. Update each fbforward node to use the new token.
5. Remove any legacy `coordination.pool` and `coordination.node_id` settings.
   Those fields are ignored with a warning if left behind.

Example fbforward config snippet:

```yaml
coordination:
  endpoint: https://fbcoord.example.workers.dev
  token: "paste-the-node-token-here"
  heartbeat_interval: 10s
```

See [fbforward user guide](../user-guide-fbforward.md) and
[configuration reference](../configuration-reference.md#410-coordination-section)
for node-side coordination settings.

### Verify the deployment

Check the health endpoint:

```bash
curl -i https://fbcoord.example.workers.dev/healthz
```

Expected response:

```text
HTTP/2 200
...

ok
```

Once fbforward nodes connect with valid node tokens, the dashboard should show
the current coordinated pick and connected nodes.

---

## 3.4.3 Operation and web UI

### Public routes

Current routes:

| Route | Method | Auth | Purpose |
|-------|--------|------|---------|
| `/` | GET | none | Admin UI shell; API access still requires login |
| `/healthz` | GET | none | Worker health check |
| `/ws/node` | GET upgrade | node token | Node coordination endpoint |
| `/api/auth/login` | POST | operator token | Create an operator session |
| `/api/auth/check` | GET | session | Validate current operator session |
| `/api/auth/logout` | POST | session | Clear the current operator session |
| `/api/state` | GET | session | Return the current global coordination state |
| `/api/token/info` | GET | session | Return masked operator-token metadata |
| `/api/token/rotate` | POST | session + current operator token | Rotate the operator token |
| `/api/node-tokens` | GET | session | List node tokens |
| `/api/node-tokens` | POST | session | Mint a node token |
| `/api/node-tokens/:node_id` | DELETE | session | Revoke a node token |

For request and response details, see the [fbcoord API reference](api.md).

The UI is served from `/` and uses hash routes:

- `#/login`
- `#/`
- `#/token`

### Session behavior

Operators log in with the operator token. On success, fbcoord sets a session
cookie:

- cookie name: `fbcoord_session`
- lifetime: 24 hours
- flags: `HttpOnly`, `Secure`, `SameSite=Strict`

The current session remains valid after operator-token rotation because session
signing is stored separately from the operator token record.

### Dashboard

The dashboard shows one deployment-wide state view:

- current coordinated pick or `no consensus`
- pick version
- status counts for `online`, `offline`, `aborted`, and `never_seen`
- per-node detail with `node_id`, status, submitted upstream list,
  `active_upstream`, `last_seen_at`, `disconnected_at`, and `first_seen_at`

The UI is poll-based. It does not subscribe to a live stream.

### Node-token management

Operators manage node tokens from the token page:

- create a token by supplying `node_id`
- view `node_id`, masked prefix, creation time, and last-used time
- revoke a token

Important behavior:

- a raw node token is shown exactly once when created
- fbcoord does not provide a dedicated rotate operation for node tokens
- replacing a node token is done by revoking the old token and minting a new
  one for the same `node_id`
- revoking a node token blocks future authentication and reconnects, closes any
  already-established WebSocket for that node, and removes the node from the
  roster entirely

### Operator-token rotation

Operators can rotate the operator token in the UI in two ways:

- generate a new random token
- submit a custom replacement token

Rotation behavior:

- the new operator token takes effect immediately
- the previous operator token is invalidated immediately
- rotation requires the current operator token as confirmation, even for an
  already-authenticated operator session
- the generated token is shown once and must be recorded then
- the UI only shows a masked prefix afterward
- node tokens remain valid and unchanged

Recommended operator-token rotation workflow:

1. Rotate the token in the UI.
2. Record the generated token, or record the custom replacement.
3. Update the Worker secret so bootstrap state matches:

```bash
wrangler secret put FBCOORD_TOKEN
```

### Rate limiting

fbcoord applies a fail2ban-style rate limiter to both:

- `POST /api/auth/login`
- `GET /ws/node`

Current defaults:

- threshold: 3 failed attempts
- window: 10 minutes
- initial block duration: 15 minutes

Implementation notes:

- auth rate limiting is enforced by a Durable Object (`FBCOORD_AUTH_GUARD`),
  not isolate-local Worker memory
- Cloudflare KV (`FBCOORD_AUTH_KV`) is used as a fast replicated ban cache and
  manual denylist layer
- login and node-auth failures are tracked separately

---

## 3.4.4 Troubleshooting

### `/healthz` is healthy but the dashboard is empty

Common causes:

- no fbforward nodes are connected
- nodes were not migrated and are still trying to use `FBCOORD_TOKEN` as
  `coordination.token`
- node tokens were revoked or copied incorrectly

### A node gets `401 Unauthorized` from `/ws/node`

Common causes:

- the node is using the operator token instead of a node token
- the node token was revoked
- the node token was issued for a different node and the wrong secret was
  copied into config

### Operator login fails

Common causes:

- the operator token was rotated and the old token is being used
- `FBCOORD_TOKEN` was changed in secrets, but the current operator token in the
  Durable Object was not updated through `/api/token/rotate`

### Clients are receiving `429 Too Many Requests`

The auth guard has temporarily or permanently blocked the current client key.
This can happen independently for:

- operator login attempts
- node-auth attempts

Repeatedly misconfigured nodes can rate-limit themselves until the cooldown
expires.

### There is no coordinated pick

fbcoord returns `upstream: null` when:

- there are no active nodes
- any active node reports an empty preference list
- the connected nodes have no shared upstream

For the exact selector rules, see
[fbcoord protocol reference](protocol.md#533-selection-algorithm).
