# coordlab manual test framework

This document describes `coordlab`, the Python-based manual testing environment for `fbcoord` and coordinated `fbforward` nodes.

coordlab is separate from the Go scenario harness in `test/harness/`. It is intended for interactive operator and developer testing on one Linux host, with a browser-accessible dashboard and explicit control over node-side and upstream-side degradation.

---

## 1. Purpose

coordlab provides a reusable local lab that can:

- start a local `fbcoord` instance with `wrangler dev`
- start two real `fbforward` nodes against that coordinator
- start two real `fbmeasure` upstreams
- expose `fbcoord` and both node control planes back to the host
- apply live delay and packet-loss shaping on both node-side and upstream-side links
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
| `net-up` / `net-status` / `net-down` | Phase 1 topology-only debugging commands |

### Work directory

coordlab stores runtime artifacts under a work directory, defaulting to `/tmp/coordlab`. A typical run uses a dedicated directory, for example:

```bash
.venv/bin/python scripts/coordlab/coordlab.py up --skip-build --workdir /tmp/coordlab-phase5
```

The work directory contains:

- `state.json` — persisted lab state
- `logs/` — one log file per managed process
- `configs/` — generated `fbforward` configs
- `fbcoord-runtime/` — isolated runtime copy used by `wrangler dev`

---

## 3. Topology and services

coordlab builds a fixed namespace topology:

- `hub`
- `hub-up`
- `internet`
- `fbcoord`
- `node-1`
- `node-2`
- `upstream-1`
- `upstream-2`

The topology is a two-hub layout joined by an `internet` transit namespace. Node-side traffic stays on `hub`, upstream-side traffic stays on `hub-up`, node-side degradation is applied on `hub`'s node-facing veths, and upstream-side degradation is applied on `hub-up`'s upstream-facing veths.

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
- shaping topology (target name, router namespace, and shaped device for both shaping axes)
- generated coordination and control tokens
- topology link metadata

Live shaping values are not stored in state; they are always read from `tc`.

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
- `GET /api/shaping`
- `PUT /api/shaping/<target>`
- `DELETE /api/shaping/<target>`
- `DELETE /api/shaping`
- `GET /api/logs/<process>?lines=N`

The dashboard does not own any background state. It reads the current `state.json` on each request and talks to the live lab through the existing host proxies.

### Dashboard sections

- Lab status
- Coordination state
- Network topology
- Traffic shaping
- Service links
- Log viewer

Terminal links are intentionally out of scope for Phase 5 and remain part of the planned Phase 6 ttyd work.

---

## 6. Verification flow

Typical manual smoke:

```bash
.venv/bin/python scripts/coordlab/coordlab.py up --skip-build --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
# open http://127.0.0.1:18800
.venv/bin/python scripts/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase5 --target node-1 --delay-ms 200
.venv/bin/python scripts/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase5 --target upstream-1 --delay-ms 200
.venv/bin/python scripts/coordlab/coordlab.py shaping-clear-all --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

Developer-side validation:

```bash
.venv/bin/python -m py_compile scripts/coordlab/coordlab.py scripts/coordlab/lib/*.py scripts/coordlab/web/*.py scripts/coordlab/tests/*.py
.venv/bin/python -m unittest discover -s scripts/coordlab/tests -p 'test_*.py'
```

---

## 7. Known limitations

- coordlab is Linux-only and depends on unprivileged user namespaces being enabled.
- The topology is fixed at two nodes and two upstreams.
- The shaping model provides two axes only: node-side on `hub` and upstream-side on `hub-up`. It does not provide a full independent `(node, upstream)` matrix.
- The dashboard is a local operator tool with no auth; it should remain bound to `127.0.0.1`.
- Terminal integration is not implemented yet.
- There is a known product issue in `fbforward`: induced packet loss can trigger a UDP measurement crash. For Phase 5 validation, use delay-based degradation as the primary smoke path.
