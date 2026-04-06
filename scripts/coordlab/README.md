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
.venv/bin/python scripts/coordlab/coordlab.py up --skip-build --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py status --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

Host proxy ports:

- `127.0.0.1:18700` -> `fbcoord`
- `127.0.0.1:18701` -> `node-1`
- `127.0.0.1:18702` -> `node-2`

Dashboard:

- `127.0.0.1:18800` -> coordlab web control UI

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

The web dashboard mirrors the same controls:

- lab and process status
- live coordination state from `fbcoord` and both nodes
- node-side and upstream-side delay/loss controls and presets
- direct links to the `fbcoord` admin UI and both node UIs
- on-demand log tailing for any tracked process

Shaping model:

- `node-1` / `node-2` targets run on `hub` and affect that node broadly, including coordination traffic
- `upstream-1` / `upstream-2` targets run on `hub-up` and affect both nodes only when they use that upstream
- effective path impairment is `node-side(node) + upstream-side(upstream)`
- this is still a two-axis model, not full per-node/per-upstream matrix shaping
