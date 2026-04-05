# fbcoord user guide

This guide covers fbcoord deployment, operation, and troubleshooting.

---

## 3.4.1 Overview

### What fbcoord does

fbcoord is an optional coordination service for multi-node fbforward deployments. Each fbforward node connects to fbcoord, submits its local upstream preference list in best-first order, and receives a coordinated upstream pick for its pool.

fbcoord does not replace the local fbforward control plane. Operators still use each fbforward node's own RPC API and Web UI for mode changes, local status, and manual overrides. fbcoord only provides shared pick coordination across nodes.

### Deployment model

fbcoord runs as a Cloudflare Worker backed by Durable Objects:

- one pool Durable Object stores live node state and the current coordinated pick for that pool
- one registry Durable Object tracks which pools are active
- one token Durable Object persists the active shared token hash, masked token metadata, and the session signing secret
- the Worker serves both the node coordination endpoint and the operator Web UI

### Coordination behavior

At a high level:

1. A node opens `GET /ws/node?pool=<pool>` with `Authorization: Bearer <token>`.
2. The node sends `hello`, then `preferences`, then periodic `heartbeat` messages.
3. fbcoord computes one coordinated pick per pool and broadcasts `pick` updates to all connected nodes in that pool.
4. If there is no shared candidate, fbcoord returns `upstream: null`.

For the wire contract and selector details, see [fbcoord protocol reference](fbcoord-protocol.md).

### What the Web UI covers

The built-in admin UI is read-only for coordination state:

- pool list with node counts, coordinated pick, and version
- per-pool node detail with submitted upstream lists, active upstream, last seen time, and connection age
- shared token rotation

The UI does not override picks, evict nodes, or push configuration.

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
wrangler deploy
```

The current Worker configuration in [fbcoord/wrangler.toml](../fbcoord/wrangler.toml) binds:

- `FBCOORD_POOL`
- `FBCOORD_REGISTRY`
- `FBCOORD_TOKEN_STORE`
- `ASSETS`

Do not rename those bindings unless the Worker code changes with them.

### Generate the initial shared token

Generate the bootstrap token out of band:

```bash
openssl rand -hex 32
```

Use that value in two places:

1. Set it as the Worker secret `FBCOORD_TOKEN`
2. Configure the same value in each fbforward node's `coordination.token`

Example fbforward config snippet:

```yaml
coordination:
  endpoint: https://fbcoord.example.workers.dev
  pool: default
  node_id: fbforward-01
  token: "paste-the-same-shared-token-here"
  heartbeat_interval: 10s
```

See [fbforward user guide](user-guide-fbforward.md) and [configuration reference](configuration-reference.md#410-coordination-section) for node-side coordination settings.

### How token bootstrap works

On first use, fbcoord seeds its active token from the deploy-time Worker secret `FBCOORD_TOKEN`. After that:

- the active token is persisted as a hash in a Durable Object
- generated or custom replacement tokens must be at least 32 characters
- the full current token is never displayed back to operators after rotation

Because the rotated token is stored in Durable Object state, normal Worker restarts and redeployments do not revert to the old secret value. You should still update `FBCOORD_TOKEN` after rotation so bootstrap state stays correct if the token store is ever reprovisioned from empty storage.

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

Once fbforward nodes connect, the dashboard should show active pools and coordinated picks. If the UI is empty but `/healthz` returns `200`, the Worker is running but no nodes are currently connected.

---

## 3.4.3 Operation and web UI

### Public routes

Current routes:

| Route | Method | Auth | Purpose |
|-------|--------|------|---------|
| `/` | GET | none | Admin UI shell; API access still requires login |
| `/healthz` | GET | none | Worker health check |
| `/ws/node?pool=<pool>` | GET upgrade | Bearer token | Node coordination endpoint |
| `/api/auth/login` | POST | shared token | Create an operator session |
| `/api/auth/check` | GET | session | Validate current operator session |
| `/api/pools` | GET | session | List active pools |
| `/api/pools/:pool` | GET | session | Fetch one pool's node detail |
| `/api/token/info` | GET | session | Return masked token metadata |
| `/api/token/rotate` | POST | session | Rotate the shared token |

The UI is served from `/` and uses hash routes:

- `#/login`
- `#/`
- `#/pool/<name>`
- `#/token`

### Session behavior

Operators log in with the shared coordination token. On success, fbcoord sets a session cookie:

- cookie name: `fbcoord_session`
- lifetime: 24 hours
- flags: `HttpOnly`, `Secure`, `SameSite=Strict`

The current session remains valid after token rotation because session signing is stored separately from the active node token.

### Dashboard and node detail

The dashboard shows one card per active pool:

- pool name
- connected node count
- current coordinated pick or `no consensus`
- pick version

Selecting a pool opens node detail, which shows:

- node ID
- submitted upstream list in best-first order
- active upstream
- last seen time
- connection age

Visual indicators:

- the coordinated pick is highlighted inside each node's submitted upstream list
- a node whose active upstream differs from the coordinated pick is highlighted as diverged, which usually means fallback or in-progress convergence

The UI is poll-based. It does not subscribe to a live stream. Current polling options are `2s`, `5s`, and `15s`, with `5s` as the default.

### Pool lifecycle and redeploy behavior

Pools are dynamic:

- a pool appears when its first node connects
- a pool disappears when its last node disconnects or is evicted as stale

The current stale-node timeout is 30 seconds. That corresponds to three 10-second heartbeat intervals, which is the default fbforward heartbeat cadence.

When fbcoord is redeployed:

- all live WebSocket sessions drop
- connected nodes must reconnect
- pools repopulate as nodes reconnect and resend `hello` and `preferences`

### Token rotation

Operators can rotate the shared token in the UI in two ways:

- generate a new random token
- submit a custom replacement token

Rotation behavior:

- the new token takes effect immediately
- the previous token is invalidated immediately
- the generated token is shown once and must be copied then
- the UI only shows a masked prefix for the current token afterward
- currently connected nodes keep their existing WebSocket session, but any reconnect using the old token will fail

Recommended rotation workflow:

1. Rotate the token in the UI.
2. Copy the generated token, or record the custom replacement.
3. Update every fbforward node's `coordination.token`.
4. Update the Worker secret so bootstrap state matches:

```bash
wrangler secret put FBCOORD_TOKEN
```

5. Roll or restart nodes as needed so reconnects use the new token.

### Rate limiting

fbcoord applies a fail2ban-style in-memory rate limiter to both:

- `POST /api/auth/login`
- `GET /ws/node`

Current defaults:

- threshold: 3 failed attempts
- window: 10 minutes
- block duration: 15 minutes

Blocked clients receive `429 Too Many Requests` with a `Retry-After` header. A successful login or a successful node authentication resets the failure counter for that source IP.

Because the limiter also applies to `/ws/node`, a repeatedly misconfigured node can block its own source IP until the cooldown expires.

---

## 3.4.4 Troubleshooting

### `/healthz` is healthy but no pools appear

The Worker is running, but no nodes are currently connected. Check:

- the fbforward `coordination.endpoint`
- the node `coordination.token`
- whether the node is actually in runtime mode `coordination`

### Node connections get `401 Unauthorized`

The shared token does not match the active fbcoord token. Common causes:

- node config still uses the old token after UI rotation
- `Authorization` header missing or malformed
- wrong Worker deployment URL

### Node or login requests get `429 Too Many Requests`

The source IP is currently blocked by the fail2ban limiter. Wait for the 15-minute cooldown, then retry with the correct token. If many nodes share one egress IP, repeated bad credentials from one node can affect the others.

### Pools disappear unexpectedly

The pool registry is driven by live node presence. A pool disappears when:

- the last node disconnects
- the last node becomes stale and is evicted after 30 seconds without heartbeat
- the Worker is redeployed and nodes have not reconnected yet

### The dashboard shows `no consensus`

Current selector behavior returns no coordinated pick when:

- there are no active nodes in the pool
- any active node submits an empty upstream list
- the submitted lists have no common upstream

For the exact selector rules, see [fbcoord protocol reference](fbcoord-protocol.md#533-selection-algorithm).

### A node shows a different active upstream than the coordinated pick

That usually means one of:

- the node is currently using local fallback because the coordinated pick is locally unusable
- the node has not yet converged to the latest coordinated pick
- the pool pick changed recently and the UI poll has caught an intermediate state

Cross-check the node's local status in the fbforward Web UI or RPC API if you need node-local fallback details.
