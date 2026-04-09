# coordlab manual test framework

This document describes `coordlab`, the retained Python-based manual testing environment for `fbcoord` and coordinated `fbforward` nodes.

coordlab is intended for interactive operator and developer testing on one Linux host, with a browser-accessible dashboard and explicit control over node-side and upstream-side degradation.

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
.venv/bin/pip install -r test/coordlab/requirements.txt
```

The main entrypoint is:

```bash
.venv/bin/python test/coordlab/coordlab.py
```

### Host prerequisites

coordlab is intended for one Linux development host and expects the same baseline environment as the implementation:

- repo-root Python venv created from `test/coordlab/requirements.txt`
- unprivileged user namespaces enabled
- `unshare`, `nsenter`, `ip`, `sysctl`, and `ping` for namespace setup
- `ttyd` for browser terminals
- `make` for default `up` builds
- `npm` plus `fbcoord/node_modules` for the `fbcoord` UI build
- `tc` only when using shaping commands

If `up` is run without `--skip-build`, coordlab rebuilds `fbforward`, `fbmeasure`, and the `fbcoord` UI before lab startup. If `--skip-build` is used, the existing binaries and built `fbcoord` assets must already exist.

### CLI conventions

All operator-facing commands use the same form:

```bash
.venv/bin/python test/coordlab/coordlab.py <subcommand> [options]
```

Common behavior:

- `--workdir` defaults to `/tmp/coordlab` for every user-facing subcommand
- the same `--workdir` should be reused across `up`, `status`, `web`, and `down`
- runtime errors print `error: ...` to stderr and exit non-zero
- `status` and `net-status` inspect saved state; they do not start or repair the lab
- `down` and `net-down` are safe to run when no state file exists; they return success after printing a notice
- `proxy-daemon` is an internal helper started by `up`; it is not part of the operator workflow

### Command matrix

| Command | Purpose | Key options |
|--------|---------|-------------|
| `up` | Build the full Phase 5 lab, start services, proxies, and terminals, switch both nodes to `coordination`, and verify readiness | `--workdir`, `--skip-build`, repeatable `--client NAME=IP` |
| `down` | Stop Phase 5 services and tear down namespaces | `--workdir` |
| `status` | Print the saved Phase 5 state, including live/dead process and namespace status, node features, and artifact paths | `--workdir`, `--json` |
| `web` | Start the Flask dashboard for an existing workdir | `--workdir`, `--host`, `--port` |
| `add-client` | Add one client namespace to a running Phase 5 lab | `--workdir`, `--client NAME=IP`, `--json` |
| `remove-client` | Remove one client namespace from a running Phase 5 lab | `--workdir`, `--name`, `--json` |
| `exec` | Run one command inside a saved namespace using `nsenter` | `--workdir`, `--ns`, `--json`, `-- COMMAND ...` |
| `shaping-status` | Show current live shaping for all targets | `--workdir` |
| `shaping-set` | Apply delay and/or loss to one target | `--workdir`, `--target`, `--delay-ms`, `--loss-pct` |
| `shaping-clear` | Remove shaping from one target | `--workdir`, `--target` |
| `shaping-clear-all` | Remove shaping from every target | `--workdir` |
| `link-status` | Show current live connected/disconnected state for all targets | `--workdir` |
| `disconnect` | Bring one node-side or upstream-side link down | `--workdir`, `--target` |
| `reconnect` | Bring one node-side or upstream-side link back up | `--workdir`, `--target` |
| `net-up` | Build the namespace topology only, without starting services, proxies, or ttyd | `--workdir`, repeatable `--client NAME=IP` |
| `net-status` | Print the saved Phase 1 topology-only state | `--workdir`, `--json` |
| `net-down` | Tear down the Phase 1 topology-only lab | `--workdir` |

### Client specification rules

Both `up` and `net-up` accept repeatable `--client NAME=IP` arguments. Each client spec is validated before topology creation:

- `NAME` must start with `client-`
- `IP` must be a valid IPv4 address
- duplicate client names are rejected
- duplicate client identity IPs are rejected
- client identity IPs must not overlap the transport `base_cidr`

Example:

```bash
.venv/bin/python test/coordlab/coordlab.py up \
  --workdir /tmp/coordlab-phase5 \
  --client client-1=198.51.100.10 \
  --client client-2=203.0.113.20
```

### Command reference

#### `up`

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py up [--workdir PATH] [--skip-build] [--client NAME=IP ...]
```

What `up` does:

- validates all requested client specs
- checks the fixed proxy ports (`18700`-`18702`) and the ttyd port set starting at `18900`
- rebuilds binaries and `fbcoord` UI unless `--skip-build` is set
- downloads missing MMDB files into `mmdb/`
- creates the namespace topology
- generates node configs into `configs/`
- starts `fbmeasure`, `fbcoord`, `fbforward`, the host proxy daemon, and ttyd terminals
- switches both nodes to coordination mode and verifies readiness
- writes the final state to `state.json`

Important notes:

- `up` fails if an earlier lab in the same `--workdir` still has live namespaces or processes
- `up` starts ttyd terminals for all configured clients plus `upstream-1` and `upstream-2`
- ttyd ports are deterministic at startup: sorted clients first, then sorted upstream namespaces
- GeoIP MMDB downloads are cached per file; existing files are reused

Typical usage:

```bash
.venv/bin/python test/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5 --skip-build
```

#### `down`

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py down [--workdir PATH]
```

`down` stops the proxy daemon, ttyd, `fbcoord`, `fbforward`, `fbmeasure`, and finally the namespace tree. It then marks the saved state inactive.

#### `status`

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py status [--workdir PATH] [--json]
```

`status` renders the saved Phase 5 view of the lab:

- host-facing service URLs
- process and namespace liveness
- proxy mappings
- configured clients and terminal URLs
- persisted node feature summaries
- artifact directories and command reminders

This command is useful both while the lab is active and after teardown, because it shows the saved state along with current liveness.

With `--json`, `status` emits the same derived payload shape used by `GET /api/status`. This JSON is intended for scripts and agents, not as a raw dump of `state.json`.

#### `add-client`

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py add-client [--workdir PATH] --client NAME=IP [--json]
```

`add-client` uses the same validation and runtime path as the web dashboard:

- it requires an active Phase 5 lab
- it rejects bad client specs, duplicate names, duplicate identity IPs, and identity IPs that overlap the transport CIDR
- it acquires the same workdir-scoped mutation lock used by the web API
- it creates `client-edge` on demand if the lab was started without clients

By default it prints the updated human-readable status. With `--json`, it emits the updated derived status payload.

Example:

```bash
.venv/bin/python test/coordlab/coordlab.py add-client \
  --workdir /tmp/coordlab-phase5 \
  --client client-3=203.0.113.30
```

#### `remove-client`

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py remove-client [--workdir PATH] --name NAME [--json]
```

`remove-client` removes one live client namespace, stops its ttyd terminal, and updates the saved state. It also uses the shared workdir mutation lock, so concurrent CLI or web client mutations fail fast instead of waiting.

By default it prints the updated human-readable status. With `--json`, it emits the updated derived status payload.

Example:

```bash
.venv/bin/python test/coordlab/coordlab.py remove-client --workdir /tmp/coordlab-phase5 --name client-3
```

#### `web`

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py web [--workdir PATH] [--host HOST] [--port PORT]
```

Default host/port are `127.0.0.1:18800`. `web` does not start the lab by itself; it serves the dashboard for the state and live services already associated with the selected `--workdir`.

Examples:

```bash
.venv/bin/python test/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5 --host 127.0.0.1 --port 18880
```

#### `exec`

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py exec [--workdir PATH] --ns NAMESPACE [--json] -- COMMAND [ARGS...]
```

`exec` runs one command inside a saved namespace using `nsenter --preserve-credentials --keep-caps -t PID -U -n -- ...`.

Default behavior:

- stdin, stdout, and stderr are passed through directly
- the child exit code becomes the CLI exit code
- this is the preferred mode for interactive or streaming commands

`--json` behavior:

- captures stdout and stderr
- prints a structured result with `namespace`, `pid`, `command`, `exit_code`, `stdout`, and `stderr`
- still returns the child exit code

Examples:

```bash
.venv/bin/python test/coordlab/coordlab.py exec --workdir /tmp/coordlab-phase5 --ns client-1 -- ping -c 1 10.0.0.2
.venv/bin/python test/coordlab/coordlab.py exec --workdir /tmp/coordlab-phase5 --ns node-1 --json -- ss -ltnp
```

#### Shaping commands

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py shaping-status [--workdir PATH]
.venv/bin/python test/coordlab/coordlab.py shaping-set [--workdir PATH] --target TARGET [--delay-ms N] [--loss-pct P]
.venv/bin/python test/coordlab/coordlab.py shaping-clear [--workdir PATH] --target TARGET
.venv/bin/python test/coordlab/coordlab.py shaping-clear-all [--workdir PATH]
```

Allowed targets are:

- `node-1`
- `node-2`
- `upstream-1`
- `upstream-2`

Behavior:

- `shaping-status` reads the live `tc` state
- `shaping-set` can apply delay only, loss only, or both in one command
- `shaping-clear` removes shaping from one target
- `shaping-clear-all` clears every target in the shaping model

Examples:

```bash
.venv/bin/python test/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase5 --target node-1 --delay-ms 200
.venv/bin/python test/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase5 --target upstream-2 --loss-pct 30
.venv/bin/python test/coordlab/coordlab.py shaping-clear --workdir /tmp/coordlab-phase5 --target upstream-1
```

#### Link-state commands

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py link-status [--workdir PATH]
.venv/bin/python test/coordlab/coordlab.py disconnect [--workdir PATH] --target TARGET
.venv/bin/python test/coordlab/coordlab.py reconnect [--workdir PATH] --target TARGET
```

The same four targets are supported. These commands operate on interface admin state, not `tc`, so they model hard partitions rather than soft impairments.

Examples:

```bash
.venv/bin/python test/coordlab/coordlab.py disconnect --workdir /tmp/coordlab-phase5 --target node-1
.venv/bin/python test/coordlab/coordlab.py reconnect --workdir /tmp/coordlab-phase5 --target upstream-1
```

#### Topology-only commands

Usage:

```bash
.venv/bin/python test/coordlab/coordlab.py net-up [--workdir PATH] [--client NAME=IP ...]
.venv/bin/python test/coordlab/coordlab.py net-status [--workdir PATH] [--json]
.venv/bin/python test/coordlab/coordlab.py net-down [--workdir PATH]
```

`net-up` creates only the namespace topology and routing. It does not:

- build binaries
- download MMDB files
- start `fbcoord`
- start `fbforward`
- start `fbmeasure`
- start host proxies
- start ttyd terminals

Use these commands when debugging namespace wiring, addresses, routing, or connectivity in isolation from the service processes.

With `--json`, `net-status` emits the same derived status contract family as `status --json`, but without live service links or process-backed runtime expectations.

### Work directory

coordlab stores runtime artifacts under a work directory, defaulting to `/tmp/coordlab`. A typical run uses a dedicated directory, for example:

```bash
.venv/bin/python test/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5 \
  --client client-1=198.51.100.10 \
  --client client-2=203.0.113.20
```

`up` always rebuilds `fbforward`, `fbmeasure`, and the `fbcoord` UI unless `--skip-build` is explicitly provided.
It also ensures the GeoIP MMDB cache is present under the work directory before node startup.

The work directory contains:

- `state.json` — persisted lab state
- `logs/` — one log file per managed process
- `configs/` — generated YAML node configs such as `configs/node-1.yaml` and `configs/node-2.yaml`
- `mmdb/` — cached GeoIP MMDB files used by both nodes
- `data/` — SQLite parent directory for per-node IP log databases
- `fbcoord-runtime/` — isolated runtime copy used by `wrangler dev`

### Common CLI workflows

Minimal full lab:

```bash
.venv/bin/python test/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py status --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

Topology-only debugging:

```bash
.venv/bin/python test/coordlab/coordlab.py net-up --workdir /tmp/coordlab-net --client client-1=203.0.113.20
.venv/bin/python test/coordlab/coordlab.py net-status --workdir /tmp/coordlab-net
.venv/bin/python test/coordlab/coordlab.py net-down --workdir /tmp/coordlab-net
```

Operational inspection from CLI after startup:

```bash
.venv/bin/python test/coordlab/coordlab.py status --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py status --workdir /tmp/coordlab-phase5 --json
.venv/bin/python test/coordlab/coordlab.py shaping-status --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py link-status --workdir /tmp/coordlab-phase5
```

Live client changes from CLI:

```bash
.venv/bin/python test/coordlab/coordlab.py add-client --workdir /tmp/coordlab-phase5 --client client-3=203.0.113.30
.venv/bin/python test/coordlab/coordlab.py remove-client --workdir /tmp/coordlab-phase5 --name client-3
```

Run a one-off command in a saved namespace:

```bash
.venv/bin/python test/coordlab/coordlab.py exec --workdir /tmp/coordlab-phase5 --ns client-1 -- ip route
```

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
The dashboard labels each Web Shell entry as `NS - PID`, for example `client-1 - 12345`.

Inside each node namespace, the generated forwarding listener binds `0.0.0.0:9000`. coordlab does not expose that forwarding port directly on the host; it is intended to be reached from client namespaces or via `coordlab exec`.

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

`test/coordlab/` is organized by subsystem:

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
.venv/bin/python test/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
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

The web UI and the CLI now share the same derived status model. `GET /api/status`, `coordlab.py status --json`, and `coordlab.py net-status --json` are intended to stay aligned so operators, scripts, and agents can consume one stable shape.

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
- firewall inspection and manual rule changes when supported by the node itself

RPC requests to the nodes require Bearer authentication. The control token comes from coordlab-generated state and config:

- `status` shows the saved control token summary in the human-readable output
- `state.json` persists the token values directly
- the generated YAML config files under `configs/` also contain the configured token

Example RPC call:

```bash
TOKEN="$(jq -r '.tokens.control' /tmp/coordlab-phase5/state.json)"
curl -s \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  http://127.0.0.1:18701/rpc \
  -d '{"jsonrpc":"2.0","id":1,"method":"GetGeoIPStatus","params":{}}'
```

---

## 6. Verification flow

Typical manual smoke:

```bash
.venv/bin/python test/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
# open http://127.0.0.1:18800
# add clients from the dashboard, then use the page for shaping, link-state control, service links, terminal access, and log viewing
# open the node UI / RPC through the existing service links for GeoIP, IP log, and firewall verification
.venv/bin/python test/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

Phase 3 feature smoke:

- confirm `mmdb/GeoLite2-ASN.mmdb` and `mmdb/Country-without-asn.mmdb` exist under the work directory after `up`
- use the node UI or `GetGeoIPStatus` RPC to confirm both MMDBs are configured and available
- generate traffic from an allowed client identity and confirm `QueryIPLog` returns geo-enriched flow-close records
- verify firewall denies for:
  - `198.51.100.10` via CIDR rule
  - `8.8.8.8` via ASN rule
  - `1.1.1.1` via country rule
- confirm blocked attempts appear in `QueryRejectionLog` or `QueryLogEvents`, while `QueryIPLog` remains flow-only
- verify `203.0.113.20` is allowed by default

Concrete CLI-based firewall verification:

```bash
.venv/bin/python test/coordlab/coordlab.py exec \
  --workdir /tmp/coordlab-phase5 \
  --ns upstream-1 -- \
  python3 -m http.server 9000 --bind 0.0.0.0

.venv/bin/python test/coordlab/coordlab.py exec \
  --workdir /tmp/coordlab-phase5 \
  --ns client-allow -- \
  curl -sS --max-time 5 http://10.0.0.2:9000/

.venv/bin/python test/coordlab/coordlab.py exec \
  --workdir /tmp/coordlab-phase5 \
  --ns client-deny-cidr -- \
  curl -sS --max-time 5 http://10.0.0.2:9000/
```

Interpretation:

- the allowed client should reach the forwarding listener inside the node namespace
- the denied client should fail
- firewall deny confirmation should come from node logs, Prometheus metrics, and `QueryRejectionLog` or `QueryLogEvents`, plus the generated config and persisted node feature summary
- do not assume there is a dedicated firewall inspection RPC unless the node implementation explicitly provides one

Developer-side validation:

```bash
.venv/bin/python -m py_compile test/coordlab/coordlab.py test/coordlab/lib/*.py test/coordlab/web/*.py test/coordlab/tests/*.py
.venv/bin/python -m unittest discover -s test/coordlab/tests -p 'test_*.py'
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
