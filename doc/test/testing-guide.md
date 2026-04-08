# Testing guide

This guide covers the retained testing surfaces in this repository:

- automated package tests run with normal language-native tooling
- manual Linux coordination testing through `coordlab`

The legacy scenario harness has been removed. `coordlab` is now the only repository-owned test framework.

---

## 1. Overview

Use the following validation layers:

- `go test ./...` for Go package tests across `bwprobe`, `fbforward`, and `fbmeasure`
- `npm --prefix fbcoord test` and `npm --prefix fbcoord run build` for `fbcoord`
- `coordlab` for manual end-to-end coordination, topology, traffic, GeoIP, IP-log, firewall, and control-plane verification on Linux

Quick start:

```bash
go test ./...
npm --prefix fbcoord test
npm --prefix fbcoord run build

python3 -m venv .venv
.venv/bin/pip install -r test/coordlab/requirements.txt
.venv/bin/python test/coordlab/coordlab.py up --skip-build --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
# Open http://127.0.0.1:18800
.venv/bin/python test/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

---

## 2. Automated tests

### 2.1 Go packages

Go test coverage remains package-based. The main entrypoint is:

```bash
go test ./...
```

Common focused runs:

```bash
go test ./bwprobe/internal/... -v
go test ./internal/upstream ./internal/config ./internal/control ./internal/geoip ./internal/iplog/... ./internal/firewall ./internal/forwarding ./internal/app ./internal/metrics -v
```

These tests cover, among other areas:

- bwprobe measurement algorithms and pacing behavior
- upstream scoring and switching logic
- configuration validation
- control-plane RPCs
- GeoIP manager behavior
- IP-log storage and pipeline behavior
- firewall rule evaluation
- forwarding and runtime lifecycle
- Prometheus metric rendering

### 2.2 Frontend verification

The `fbforward` and `fbcoord` frontends should still be validated through their normal builds:

```bash
npm --prefix fbcoord run build
cd web && npm run build
```

Successful builds confirm that the TypeScript sources compile cleanly.

### 2.3 General test-writing guidance

- Prefer table-driven Go tests with `t.Run` subtests.
- Keep Linux-only tests explicitly guarded where required.
- Keep package tests runnable without coordlab unless the behavior is inherently manual or topology-dependent.

---

## 3. coordlab manual testing

`coordlab` is the retained manual test framework for coordinated `fbforward` deployments. It runs from the repo-root Python venv and lives under `test/coordlab/`.

Bootstrap:

```bash
python3 -m venv .venv
.venv/bin/pip install -r test/coordlab/requirements.txt
```

Main entrypoint:

```bash
.venv/bin/python test/coordlab/coordlab.py
```

Typical workflow:

```bash
.venv/bin/python test/coordlab/coordlab.py up --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py status --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
.venv/bin/python test/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

Useful operator commands:

```bash
.venv/bin/python test/coordlab/coordlab.py status --workdir /tmp/coordlab-phase5 --json
.venv/bin/python test/coordlab/coordlab.py add-client --workdir /tmp/coordlab-phase5 --client client-1=198.51.100.10
.venv/bin/python test/coordlab/coordlab.py exec --workdir /tmp/coordlab-phase5 --ns client-1 -- ip route
.venv/bin/python test/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase5 --target upstream-1 --delay-ms 200
.venv/bin/python test/coordlab/coordlab.py shaping-clear-all --workdir /tmp/coordlab-phase5
```

For the full operator reference, dashboard behavior, and feature-verification flows, see [coordlab.md](coordlab.md).

### 3.1 Host requirements

coordlab requires:

- Linux with unprivileged user namespaces enabled
- the repo-root Python venv
- `unshare`, `nsenter`, `ip`, `sysctl`, and `ping`
- `ttyd` for browser terminals
- `make` and `npm` for normal `up` builds
- `tc` when using shaping commands

### 3.2 Recommended validation commands

```bash
.venv/bin/python -m py_compile test/coordlab/coordlab.py test/coordlab/lib/*.py test/coordlab/web/*.py test/coordlab/tests/*.py
.venv/bin/python -m unittest discover -s test/coordlab/tests -p 'test_*.py'
```

---

## 4. Choosing the right test surface

- Use Go package tests for deterministic logic and component-level behavior.
- Use frontend builds and package tests for UI and TypeScript validation.
- Use coordlab when behavior depends on live coordination, network namespaces, manual traffic generation, browser inspection, or end-to-end operational flows.
