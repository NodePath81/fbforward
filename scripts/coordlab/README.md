# coordlab

Coordlab is the manual coordination lab for `fbcoord` and multi-node `fbforward`.

## Python environment

Coordlab must be run from the repo-root venv:

```bash
python3 -m venv .venv
.venv/bin/pip install -r scripts/coordlab/requirements.txt
```

## Phase 5 usage

```bash
.venv/bin/python scripts/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5 \
  --client client-1=198.51.100.10 \
  --client client-2=203.0.113.20
.venv/bin/python scripts/coordlab/coordlab.py status --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

`up` rebuilds the Go binaries and the `fbcoord` UI by default. Use `--skip-build` only when you explicitly want to reuse existing build artifacts.
It also downloads the GeoIP MMDB cache into the work directory when files are missing.

Host proxy ports:

- `127.0.0.1:18700` -> `fbcoord`
- `127.0.0.1:18701` -> `node-1`
- `127.0.0.1:18702` -> `node-2`

Dashboard:

- `127.0.0.1:18800` -> coordlab web control UI

Terminal ports:

- `127.0.0.1:18900+` -> ttyd browser terminals for configured clients and upstream namespaces
- terminals open with `/bin/bash --noprofile --norc -i` and prompt as `<namespace>@<cwd>$`

After `up` and `web`, normal manual testing is expected to happen primarily from the web page. The CLI remains responsible for lifecycle commands, while the dashboard provides the routine test-session controls, including live client add/remove.

Generated node configs now enable:

- `geoip`
- `ip_log`
- `firewall`

Phase 3 keeps GeoIP, IP log, and firewall inspection in the native node UI / RPC surface reached through the existing service links. The coordlab dashboard still focuses on lab operations rather than feature-specific controls.

Workdir artifacts now include:

- `mmdb/GeoLite2-ASN.mmdb`
- `mmdb/Country-without-asn.mmdb`
- `data/node-1-iplog.sqlite`
- `data/node-2-iplog.sqlite`

Suggested manual firewall checks:

- `198.51.100.10` should be denied by CIDR
- `8.8.8.8` should be denied by ASN `15169`
- `1.1.1.1` should be denied by country `AU`
- `203.0.113.20` should be allowed by default

Phase 1 network-only commands are still available:

```bash
.venv/bin/python scripts/coordlab/coordlab.py net-up
.venv/bin/python scripts/coordlab/coordlab.py net-status
.venv/bin/python scripts/coordlab/coordlab.py net-down
```

Traffic shaping commands:

```bash
.venv/bin/python scripts/coordlab/coordlab.py shaping-status --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase5 --target node-1 --delay-ms 200
.venv/bin/python scripts/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase5 --target upstream-2 --loss-pct 30
.venv/bin/python scripts/coordlab/coordlab.py shaping-clear --workdir /tmp/coordlab-phase5 --target upstream-1
.venv/bin/python scripts/coordlab/coordlab.py shaping-clear-all --workdir /tmp/coordlab-phase5
```

Link-state commands:

```bash
.venv/bin/python scripts/coordlab/coordlab.py link-status --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py disconnect --workdir /tmp/coordlab-phase5 --target node-1
.venv/bin/python scripts/coordlab/coordlab.py reconnect --workdir /tmp/coordlab-phase5 --target upstream-1
```

The web dashboard mirrors the same controls:

- lab and process status
- live client add/remove with automatic `client-edge` creation on first web-added client
- client namespace status and client identity IPs
- live coordination state from `fbcoord` and both nodes
- node-side and upstream-side delay/loss controls and presets
- per-target disconnect/reconnect controls inside the same cards
- direct links to the `fbcoord` admin UI and both node UIs
- direct terminal links for configured clients and upstream namespaces
- on-demand log tailing for any tracked process

Control model:

- `node-1` / `node-2` targets run on `hub` and affect that node broadly, including coordination traffic
- `upstream-1` / `upstream-2` targets run on `hub-up` and affect both nodes only when they use that upstream
- shaping is a soft impairment using delay/loss
- disconnect is a hard partition using `ip link set ... down/up`
- effective path impairment is `node-side(node) + upstream-side(upstream)`
- this is still a two-axis model, not full per-node/per-upstream matrix shaping
- reconnect preserves any existing shaping on that target
