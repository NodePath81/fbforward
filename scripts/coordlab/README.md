# coordlab

Coordlab is the manual coordination lab for `fbcoord` and multi-node `fbforward`.

## Python environment

Coordlab must be run from the repo-root venv:

```bash
python3 -m venv .venv
.venv/bin/pip install -r scripts/coordlab/requirements.txt
```

## Phase 4 usage

```bash
.venv/bin/python scripts/coordlab/coordlab.py up --skip-build --workdir /tmp/coordlab-phase4
.venv/bin/python scripts/coordlab/coordlab.py status --workdir /tmp/coordlab-phase4
.venv/bin/python scripts/coordlab/coordlab.py down --workdir /tmp/coordlab-phase4
```

Host proxy ports:

- `127.0.0.1:18700` -> `fbcoord`
- `127.0.0.1:18701` -> `node-1`
- `127.0.0.1:18702` -> `node-2`

Phase 1 network-only commands are still available:

```bash
.venv/bin/python scripts/coordlab/coordlab.py net-up
.venv/bin/python scripts/coordlab/coordlab.py net-status
.venv/bin/python scripts/coordlab/coordlab.py net-down
```

Traffic shaping commands:

```bash
.venv/bin/python scripts/coordlab/coordlab.py shaping-status --workdir /tmp/coordlab-phase4
.venv/bin/python scripts/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase4 --upstream upstream-1 --delay-ms 200
.venv/bin/python scripts/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase4 --upstream upstream-2 --loss-pct 30
.venv/bin/python scripts/coordlab/coordlab.py shaping-clear --workdir /tmp/coordlab-phase4 --upstream upstream-1
.venv/bin/python scripts/coordlab/coordlab.py shaping-clear-all --workdir /tmp/coordlab-phase4
```
