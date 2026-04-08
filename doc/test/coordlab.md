# coordlab manual test framework

This document describes `coordlab`, the Python-based manual testing environment for `fbcoord` and coordinated `fbforward` nodes.

coordlab is separate from the Go scenario harness in `test/harness/`. It is intended for interactive operator and developer testing on one Linux host, with a browser-accessible dashboard and explicit control over node-side and upstream-side degradation.

---

## 1. Purpose

coordlab provides a reusable local lab that can:

- start a local `fbcoord` instance with `wrangler dev`
- start two real `fbforward` nodes against that coordinator
- start two real `fbmeasure` upstreams
- start zero or more client namespaces for manual end-to-end traffic generation
- expose `fbcoord` and both node control planes back to the host
- expose browser terminals for client and upstream namespaces
- apply live delay and packet-loss shaping on both node-side and upstream-side links
- disconnect and reconnect individual node-side and upstream-side links
- expose a browser dashboard for lab status, coordination state, shaping, and log viewing

It is a manual environment, not a CI runner and not a scenario/assertion engine.

---

## 2. Runtime model

coordlab runs entirely from the repo-root Python venv:

```bash
python3 -m venv .venv
.venv/bin/pip install -r scripts/coordlab/requirements.txt
```

The main entrypoint is:

```bash
.venv/bin/python scripts/coordlab/coordlab.py
```

### Commands

| Command | Purpose |
|--------|---------|
| `up` | Build the lab topology, start services, start host proxies, switch nodes to `coordination`, and verify readiness |
| `down` | Stop proxies and services, tear down namespaces, and mark the lab inactive |
| `status` | Print host URLs, process state, namespace state, and artifact paths |
| `web` | Start the Flask dashboard on the host |
| `shaping-status` | Show current live shaping on all node-side and upstream-side targets |
| `shaping-set` | Apply delay and/or loss to one shaping target |
| `shaping-clear` | Remove shaping from one target |
| `shaping-clear-all` | Remove shaping from all targets |
| `link-status` | Show current live link state for all targets |
| `disconnect` | Bring one target link down |
| `reconnect` | Bring one target link back up |
| `net-up` / `net-status` / `net-down` | Phase 1 topology-only debugging commands |

### Work directory

coordlab stores runtime artifacts under a work directory, defaulting to `/tmp/coordlab`. A typical run uses a dedicated directory, for example:

```bash
.venv/bin/python scripts/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5 \
  --client client-1=198.51.100.10 \
  --client client-2=203.0.113.20
```

`up` always rebuilds `fbforward`, `fbmeasure`, and the `fbcoord` UI unless `--skip-build` is explicitly provided.
It also ensures the GeoIP MMDB cache is present under the work directory before node startup.

The work directory contains:

- `state.json` — persisted lab state
- `logs/` — one log file per managed process
- `configs/` — generated `fbforward` configs
- `mmdb/` — cached GeoIP MMDB files used by both nodes
- `data/` — SQLite parent directory for per-node IP log databases
- `fbcoord-runtime/` — isolated runtime copy used by `wrangler dev`

---

## 3. Topology and services

coordlab builds a fixed service-side topology with an optional client-side extension:

- `hub`
- `hub-up`
- `internet`
- `fbcoord`
- `node-1`
- `node-2`
- `upstream-1`
- `upstream-2`
- `client-edge` when any client is configured, including on-demand creation from the web dashboard
- one or more `client-*` namespaces when requested by CLI input

The topology is a two-hub layout joined by an `internet` transit namespace, with client namespaces attached through `client-edge` when enabled. Node-side traffic stays on `hub`, upstream-side traffic stays on `hub-up`, and client traffic enters through `client-edge` before crossing `internet` toward `hub`. Node-side degradation is applied on `hub`'s node-facing veths, and upstream-side degradation is applied on `hub-up`'s upstream-facing veths.

### Managed processes

When `up` succeeds, coordlab manages:

- `fbmeasure-upstream-1`
- `fbmeasure-upstream-2`
- `fbcoord`
- `fbforward-node-1`
- `fbforward-node-2`
- `coordlab-proxy`

Host-facing access points:

- `http://127.0.0.1:18700` — `fbcoord`
- `http://127.0.0.1:18701` — node-1 control/UI/metrics
- `http://127.0.0.1:18702` — node-2 control/UI/metrics
- `http://127.0.0.1:18800` — coordlab dashboard when `web` is running
- `http://127.0.0.1:18900+` — ttyd terminals for configured client namespaces and upstream namespaces

Each ttyd terminal launches `/bin/bash --noprofile --norc -i` with `PS1` set to `<namespace>@\w$ ` so the namespace identity is always visible in the prompt.

### Node feature defaults

coordlab now generates both node configs with the following features enabled by default:

- `geoip`
- `ip_log`
- `firewall`

The generated firewall defaults are intentionally simple and testable:

- deny CIDR `198.51.100.0/24`
- deny ASN `15169`
- deny country `AU`
- allow all other traffic by default

---

## 4. Code layout

`scripts/coordlab/` is organized by subsystem:

| Path | Responsibility |
|------|----------------|
| `coordlab.py` | CLI entrypoint and orchestration |
| `lib/netns.py` | Namespace creation, veth setup, routing, connectivity checks |
| `lib/process.py` | Background process lifecycle and logging |
| `lib/config.py` | Token generation, config rendering, `fbcoord` runtime prep |
| `lib/proxy.py` | Host-side TCP proxy daemon for namespace-local services |
| `lib/readiness.py` | HTTP/RPC readiness polling |
| `lib/rpc.py` | Node RPC helpers (`GetStatus`, `SetUpstream`) |
| `lib/shaping.py` | `tc netem` shaping backend |
| `lib/linkstate.py` | Per-target link down/up control on `hub` and `hub-up` |
| `lib/state.py` | Persistent state model and JSON serialization |
| `lib/output.py` | CLI status rendering |
| `web/app.py` | Flask app and JSON API |
| `web/templates/` | Dashboard HTML |
| `web/static/` | Dashboard JS and CSS |
| `tests/` | Python unit tests for helpers and Flask API routes |

### State model

`state.json` is the source of truth for the dashboard and CLI inspection commands. It records:

- phase number
- active/inactive state
- namespace PIDs and roles
- managed process PIDs and log paths
- host proxy mappings
- configured client namespaces and client identity IPs
- terminal endpoints for client and upstream namespaces
- shaping topology (target name, router namespace, and shaped device for both shaping axes)
- generated coordination and control tokens
- persisted per-node feature summaries for GeoIP, IP log, and firewall
- topology link metadata
- the next `/30` transport allocation index used for live client additions

Live shaping values are not stored in state; they are always read from `tc`.
Live link state is not stored in state; it is always read from `ip link show`.

---

## 5. Web dashboard

The Phase 5 dashboard is started separately:

```bash
.venv/bin/python scripts/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
```

It serves:

- `GET /` — single-page dashboard
- `GET /api/status`
- `GET /api/coordination`
- `POST /api/clients`
- `DELETE /api/clients/<name>`
- `GET /api/shaping`
- `PUT /api/shaping/<target>`
- `DELETE /api/shaping/<target>`
- `DELETE /api/shaping`
- `GET /api/link-state`
- `PUT /api/link-state/<target>`
- `GET /api/logs/<process>?lines=N`

The dashboard does not own any background state. It reads the current `state.json` on each request and talks to the live lab through the existing host proxies.

### Dashboard sections

- Lab status
- Client management
- Coordination state
- Network topology
- Traffic shaping
- Service links
- Terminal access
- Log viewer

Disconnect is modeled separately from shaping:

- shaping is a soft impairment using delay/loss
- disconnect is a hard partition using interface admin down/up
- shaping values remain visible while disconnected and are restored when the link is reconnected

After the lab has been started, the dashboard is the primary operator interface for normal manual testing. The CLI remains responsible for lifecycle commands such as `up`, `down`, and `web`; the web UI handles routine client add/remove, service access, shaping, link-state, and log viewing.

Phase 3 does not add dedicated dashboard panels for GeoIP, IP log, or firewall. Operators should use the existing node service links to reach each node's native UI and RPC surface for:

- GeoIP database status and refresh
- IP log inspection and queries
- firewall inspection and manual rule changes

---

## 6. Verification flow

Typical manual smoke:

```bash
.venv/bin/python scripts/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
# open http://127.0.0.1:18800
# add clients from the dashboard, then use the page for shaping, link-state control, service links, terminal access, and log viewing
# open the node UI / RPC through the existing service links for GeoIP, IP log, and firewall verification
.venv/bin/python scripts/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

Phase 3 feature smoke:

- confirm `mmdb/GeoLite2-ASN.mmdb` and `mmdb/Country-without-asn.mmdb` exist under the work directory after `up`
- use the node UI or `GetGeoIPStatus` RPC to confirm both MMDBs are configured and available
- generate traffic from an allowed client identity and confirm `QueryIPLog` returns geo-enriched records
- verify firewall denies for:
  - `198.51.100.10` via CIDR rule
  - `8.8.8.8` via ASN rule
  - `1.1.1.1` via country rule
- verify `203.0.113.20` is allowed by default

Developer-side validation:

```bash
.venv/bin/python -m py_compile scripts/coordlab/coordlab.py scripts/coordlab/lib/*.py scripts/coordlab/web/*.py scripts/coordlab/tests/*.py
.venv/bin/python -m unittest discover -s scripts/coordlab/tests -p 'test_*.py'
```

---

## 7. Known limitations

- coordlab is Linux-only and depends on unprivileged user namespaces being enabled.
- The service-side topology remains fixed at two nodes and two upstreams; this phase adds only the client-side extension.
- The shaping model provides two axes only: node-side on `hub` and upstream-side on `hub-up`. It does not provide a full independent `(node, upstream)` matrix.
- Disconnect controls follow the same two-axis target model and preserve any shaping already configured on that target.
- The dashboard is a local operator tool with no auth; it should remain bound to `127.0.0.1`.
- Client-side shaping is not implemented in this phase.
- There is a known product issue in `fbforward`: induced packet loss can trigger a UDP measurement crash. For Phase 5 validation, use delay-based degradation as the primary smoke path.
- Concurrent web client add/remove requests are rejected with HTTP `409`; they are not queued.
