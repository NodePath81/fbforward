#!/usr/bin/env python3
from __future__ import annotations

import argparse
import importlib.util
import ipaddress
import json
import shutil
import socket
import subprocess
import sys
import textwrap
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable

from lib import config as coordconfig
from lib import netns
from lib.output import render_summary
from lib.process import ProcessManager, is_alive, terminate_pid, terminate_process_group
from lib.proxy import run_proxy_daemon
from lib.state import (
    ClientInfo,
    LabState,
    LinkInfo,
    NamespaceInfo,
    ProcessInfo,
    ProxyInfo,
    ShapingInfo,
    ShapingTargetInfo,
    TerminalInfo,
    TokenInfo,
    TopologyInfo,
    load_state,
    save_state,
)

REPO_ROOT = Path(__file__).resolve().parents[2]
COORDLAB_SCRIPT = Path(__file__).resolve()
DEFAULT_WORKDIR = Path("/tmp/coordlab")
STATE_FILENAME = "state.json"
FBFORWARD_BIN = REPO_ROOT / "build/bin/fbforward"
FBMEASURE_BIN = REPO_ROOT / "build/bin/fbmeasure"
FBCOORD_BUILD_SENTINEL = REPO_ROOT / "fbcoord/ui/dist/index.html"
VENV_PYTHON = REPO_ROOT / ".venv/bin/python"
REQUIREMENTS_FILE = REPO_ROOT / "scripts/coordlab/requirements.txt"
CONFIGS_DIRNAME = "configs"
LOGS_DIRNAME = "logs"
RUNTIME_DIRNAME = coordconfig.FBCOORD_RUNTIME_DIR
POLL_INTERVAL_SEC = 0.5
READINESS_TIMEOUT_SEC = 30.0
PROXY_PROCESS_NAME = "coordlab-proxy"
TTYD_BASE_PORT = 18900
PROXY_SPECS = {
    "fbcoord": ("127.0.0.1", 18700, "fbcoord", "127.0.0.1", 8787),
    "node-1": ("127.0.0.1", 18701, "node-1", "127.0.0.1", 8080),
    "node-2": ("127.0.0.1", 18702, "node-2", "127.0.0.1", 8080),
}

HTTP_HELPER = textwrap.dedent(
    """\
    import json
    import sys
    import urllib.error
    import urllib.request

    url = sys.argv[1]
    method = sys.argv[2]
    headers = json.loads(sys.argv[3])
    body = sys.argv[4].encode("utf-8") if len(sys.argv) > 4 and sys.argv[4] else None
    req = urllib.request.Request(url, data=body, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            print(resp.status)
            print(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        print(exc.code)
        print(exc.read().decode("utf-8"))
    """
)


def state_path_for(workdir: Path) -> Path:
    return workdir / STATE_FILENAME


def configs_dir_for(workdir: Path) -> Path:
    return workdir / CONFIGS_DIRNAME


def logs_dir_for(workdir: Path) -> Path:
    return workdir / LOGS_DIRNAME


def runtime_dir_for(workdir: Path) -> Path:
    return workdir / RUNTIME_DIRNAME


def require_runtime_environment() -> None:
    actual = Path(sys.executable).resolve()
    if actual != VENV_PYTHON.resolve():
        raise RuntimeError(
            "coordlab must be run with the repo venv interpreter.\n"
            f"expected: {VENV_PYTHON}\n"
            f"actual:   {actual}\n"
            "bootstrap:\n"
            "  python3 -m venv .venv\n"
            "  .venv/bin/pip install -r scripts/coordlab/requirements.txt"
        )


def require_flask_environment() -> None:
    if importlib.util.find_spec("flask") is None:
        raise RuntimeError(
            "coordlab web requires flask in the repo venv.\n"
            "bootstrap:\n"
            "  python3 -m venv .venv\n"
            "  .venv/bin/pip install -r scripts/coordlab/requirements.txt"
        )
    if importlib.util.find_spec("httpx") is None:
        raise RuntimeError(
            "coordlab requires httpx in the repo venv.\n"
            "bootstrap:\n"
            "  python3 -m venv .venv\n"
            "  .venv/bin/pip install -r scripts/coordlab/requirements.txt"
        )


def build_state(
    workdir: Path,
    topology: netns.Topology,
    phase: int,
    *,
    active: bool,
    processes: dict[str, ProcessInfo] | None = None,
    proxies: dict[str, ProxyInfo] | None = None,
    clients: dict[str, ClientInfo] | None = None,
    terminals: dict[str, TerminalInfo] | None = None,
    shaping: ShapingInfo | None = None,
    tokens: TokenInfo | None = None,
) -> LabState:
    namespaces = {
        name: NamespaceInfo(pid=ns.pid, parent=ns.parent, role=ns.role)
        for name, ns in topology.namespaces.items()
    }
    client_infos = clients or {
        name: ClientInfo(identity_ip=identity_ip)
        for name, identity_ip in sorted(topology.clients.items())
    }
    links = [
        LinkInfo(
            left_ns=link.left_ns,
            right_ns=link.right_ns,
            left_if=link.left_if,
            right_if=link.right_if,
            subnet=link.subnet,
            left_ip=link.left_ip,
            right_ip=link.right_ip,
        )
        for link in topology.links
    ]
    return LabState(
        phase=phase,
        active=active,
        created_at=datetime.now(timezone.utc).isoformat(),
        work_dir=str(workdir),
        namespaces=namespaces,
        processes=processes or {},
        proxies=proxies or {},
        clients=client_infos,
        terminals=terminals or {},
        shaping=shaping or build_shaping_info(topology),
        tokens=tokens or TokenInfo(),
        topology=TopologyInfo(base_cidr=topology.base_cidr, links=links),
    )


def build_shaping_info(topology: netns.Topology) -> ShapingInfo:
    return ShapingInfo(
        targets={
            "node-1": ShapingTargetInfo(
                router_ns="hub",
                tag="",
                namespace="node-1",
                device=netns.find_link(topology.links, "hub", "node-1").left_if,
            ),
            "node-2": ShapingTargetInfo(
                router_ns="hub",
                tag="",
                namespace="node-2",
                device=netns.find_link(topology.links, "hub", "node-2").left_if,
            ),
            "upstream-1": ShapingTargetInfo(
                router_ns="hub-up",
                tag="us-1",
                namespace="upstream-1",
                device=netns.find_link(topology.links, "hub-up", "upstream-1").left_if,
            ),
            "upstream-2": ShapingTargetInfo(
                router_ns="hub-up",
                tag="us-2",
                namespace="upstream-2",
                device=netns.find_link(topology.links, "hub-up", "upstream-2").left_if,
            ),
        },
    )


def namespace_shutdown_order(namespaces: dict[str, NamespaceInfo]) -> list[tuple[str, NamespaceInfo]]:
    def depth(name: str) -> int:
        level = 0
        current = namespaces[name]
        while current.parent:
            level += 1
            current = namespaces[current.parent]
        return level

    return sorted(namespaces.items(), key=lambda item: (depth(item[0]), item[0]), reverse=True)


def process_shutdown_order(processes: dict[str, ProcessInfo]) -> list[tuple[str, ProcessInfo]]:
    return sorted(processes.items(), key=lambda item: (item[1].order, item[0]), reverse=True)


def print_basic_status(state: LabState) -> None:
    workdir = Path(state.work_dir)
    print(f"coordlab phase={state.phase} active={state.active}")
    print(f"work_dir={state.work_dir}")
    print("namespaces:")
    for name, info in sorted(state.namespaces.items()):
        parent = info.parent or "-"
        status = "alive" if is_alive(info.pid) else "dead"
        print(f"  {name}: pid={info.pid} parent={parent} role={info.role} status={status}")
    if state.processes:
        print("processes:")
        for name, info in sorted(state.processes.items(), key=lambda item: (item[1].order, item[0])):
            status = "alive" if is_alive(info.pid) else "dead"
            print(f"  {name}: pid={info.pid} ns={info.ns} order={info.order} status={status} log={info.log_path}")
    print("links:")
    for link in state.topology.links:
        print(
            f"  {link.left_ns}:{link.left_if} {link.left_ip} <-> "
            f"{link.right_ns}:{link.right_if} {link.right_ip} subnet={link.subnet}"
        )
    if state.proxies:
        print("proxies:")
        for name, proxy in sorted(state.proxies.items()):
            print(
                f"  {name}: {proxy.listen_host}:{proxy.host_port} -> "
                f"{proxy.target_ns}:{proxy.target_host}:{proxy.target_port}"
            )
    if state.clients:
        print("clients:")
        for name, info in sorted(state.clients.items()):
            print(f"  {name}: identity_ip={info.identity_ip}")
    if state.terminals:
        print("terminals:")
        for name, info in sorted(state.terminals.items()):
            status = "alive" if is_alive(info.pid) else "dead"
            print(f"  {name}: http://127.0.0.1:{info.host_port} pid={info.pid} status={status}")
    if state.tokens.coord_token or state.tokens.control_token:
        print("tokens:")
        if state.tokens.coord_token:
            print(f"  coord_token={state.tokens.coord_token}")
        if state.tokens.control_token:
            print(f"  control_token={state.tokens.control_token}")
    print("artifacts:")
    print(f"  configs={configs_dir_for(workdir)}")
    print(f"  logs={logs_dir_for(workdir)}")
    print(f"  fbcoord_runtime={runtime_dir_for(workdir)}")
    print(f"  state={state_path_for(workdir)}")


def require_tools(tools: Iterable[str]) -> None:
    missing = [tool for tool in tools if shutil.which(tool) is None]
    if missing:
        raise RuntimeError(f"missing required tools: {', '.join(missing)}")


def parse_client_specs(raw_specs: list[str], *, base_cidr: str = netns.DEFAULT_BASE_CIDR) -> dict[str, str]:
    if not raw_specs:
        return {}
    base_network = ipaddress.ip_network(base_cidr)
    parsed: dict[str, str] = {}
    seen_ips: set[str] = set()
    for raw in raw_specs:
        name, separator, raw_ip = raw.partition("=")
        if separator != "=" or not name or not raw_ip:
            raise RuntimeError(f"invalid client spec {raw!r}; expected NAME=IP")
        if not name.startswith("client-"):
            raise RuntimeError(f"invalid client name {name!r}; expected prefix 'client-'")
        if name in parsed:
            raise RuntimeError(f"duplicate client name: {name}")
        try:
            ip = ipaddress.ip_address(raw_ip)
        except ValueError as exc:
            raise RuntimeError(f"invalid client IP {raw_ip!r} for {name}") from exc
        if not isinstance(ip, ipaddress.IPv4Address):
            raise RuntimeError(f"client IP must be IPv4: {raw_ip}")
        if raw_ip in seen_ips:
            raise RuntimeError(f"duplicate client IP: {raw_ip}")
        if ip in base_network:
            raise RuntimeError(f"client IP {raw_ip} overlaps transport base CIDR {base_cidr}")
        parsed[name] = raw_ip
        seen_ips.add(raw_ip)
    return parsed


def allocate_ttyd_ports(
    client_names: Iterable[str],
    upstream_names: Iterable[str] = ("upstream-1", "upstream-2"),
) -> dict[str, int]:
    ports: dict[str, int] = {}
    port = TTYD_BASE_PORT
    for name in [*sorted(client_names), *sorted(upstream_names)]:
        ports[name] = port
        port += 1
    return ports


def build_ttyd_command(*, ns_pid: int, port: int) -> list[str]:
    return [
        "ttyd",
        "--interface",
        "127.0.0.1",
        "--port",
        str(port),
        "--writable",
        "nsenter",
        "--preserve-credentials",
        "--keep-caps",
        "-t",
        str(ns_pid),
        "-U",
        "-n",
        "--",
        "/bin/bash",
        "-l",
    ]


def run_host(args: list[str], *, cwd: str | Path | None = None, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(args, cwd=cwd, env=env, check=True, capture_output=True, text=True)
    except subprocess.CalledProcessError as exc:
        details = []
        if exc.stdout.strip():
            details.append(f"stdout={exc.stdout.strip()}")
        if exc.stderr.strip():
            details.append(f"stderr={exc.stderr.strip()}")
        suffix = f" ({'; '.join(details)})" if details else ""
        raise RuntimeError(f"command failed: {' '.join(args)}{suffix}") from exc


def existing_lab_is_alive(state: LabState) -> list[str]:
    alive: list[str] = []
    alive.extend(f"ns:{name}" for name, info in state.namespaces.items() if is_alive(info.pid))
    alive.extend(f"proc:{name}" for name, info in state.processes.items() if is_alive(info.pid))
    return sorted(alive)


def ensure_fbforward_binaries(skip_build: bool) -> None:
    missing = [str(path) for path in (FBFORWARD_BIN, FBMEASURE_BIN) if not path.exists()]
    if not missing:
        return
    if skip_build:
        raise RuntimeError(f"missing required binaries with --skip-build: {', '.join(missing)}")
    require_tools(["make"])
    run_host(["make", "build"], cwd=REPO_ROOT)


def ensure_fbcoord_assets(skip_build: bool) -> None:
    if not (coordconfig.FBCOORD_SOURCE_DIR / "node_modules").exists():
        raise RuntimeError("fbcoord/node_modules is missing; run `npm --prefix fbcoord install` before coordlab up")
    if skip_build:
        if not FBCOORD_BUILD_SENTINEL.exists():
            raise RuntimeError(f"missing fbcoord build output with --skip-build: {FBCOORD_BUILD_SENTINEL}")
        return
    require_tools(["npm"])
    run_host(["npm", "--prefix", "fbcoord", "run", "build"], cwd=REPO_ROOT)


def wrangler_command() -> list[str]:
    if shutil.which("wrangler"):
        run_host(["wrangler", "--version"], cwd=REPO_ROOT)
        return ["wrangler", "dev"]

    node = shutil.which("node")
    candidates = sorted(
        Path.home().glob(".npm/_npx/*/node_modules/wrangler/wrangler-dist/cli.js"),
        key=lambda path: path.stat().st_mtime,
        reverse=True,
    )
    if node is not None and candidates:
        return [node, str(candidates[0]), "dev"]

    if shutil.which("npx"):
        run_host(["npx", "--yes", "wrangler", "--version"], cwd=REPO_ROOT)
        node = shutil.which("node")
        if node is None:
            raise RuntimeError("node is required for the npx-based wrangler fallback")
        candidates = sorted(
            Path.home().glob(".npm/_npx/*/node_modules/wrangler/wrangler-dist/cli.js"),
            key=lambda path: path.stat().st_mtime,
            reverse=True,
        )
        if not candidates:
            raise RuntimeError("unable to locate cached wrangler CLI after npx warmup")
        return [node, str(candidates[0]), "dev"]
    raise RuntimeError("wrangler is not available and npx is missing")


def validate_fbforward_config(config_path: Path) -> None:
    run_host([str(FBFORWARD_BIN), "check", "--config", str(config_path)])


def log_excerpt(log_path: str, *, lines: int = 20) -> str:
    path = Path(log_path)
    if not path.exists():
        return "<log file not found>"
    content = path.read_text(encoding="utf-8", errors="replace").splitlines()
    if not content:
        return "<log file empty>"
    return "\n".join(content[-lines:])


def ns_http_request(pid: int, url: str, *, method: str = "GET", headers: dict[str, str] | None = None, body: str = "") -> tuple[int, str]:
    result = netns.nsenter_run(
        pid,
        [
            str(VENV_PYTHON),
            "-c",
            HTTP_HELPER,
            url,
            method,
            json.dumps(headers or {}),
            body,
        ],
    )
    lines = result.stdout.splitlines()
    if not lines:
        raise RuntimeError(f"no HTTP response returned for {url}")
    status = int(lines[0].strip())
    body_text = "\n".join(lines[1:])
    return status, body_text


def wait_for_condition(timeout_sec: float, poll_fn, failure_message: str) -> None:
    deadline = time.monotonic() + timeout_sec
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            if poll_fn():
                return
        except Exception as exc:
            last_error = exc
        time.sleep(POLL_INTERVAL_SEC)
    if last_error is not None:
        raise RuntimeError(f"{failure_message}: {last_error}") from last_error
    raise RuntimeError(failure_message)


def verify_fbcoord_health_in_namespace(topology: netns.Topology, manager: ProcessManager) -> None:
    fbcoord_ip = netns.find_link(topology.links, "hub", "fbcoord").right_ip
    node_pid = topology.namespaces["node-1"].pid

    def check() -> bool:
        managed = manager.get("fbcoord")
        if managed is None or not manager.is_alive("fbcoord"):
            excerpt = log_excerpt(managed.log_path) if managed is not None else "<process not started>"
            raise RuntimeError(f"fbcoord exited early\n{excerpt}")
        status, body = ns_http_request(node_pid, f"http://{fbcoord_ip}:8787/healthz")
        return status == 200 and body.strip() == "ok"

    wait_for_condition(READINESS_TIMEOUT_SEC, check, "fbcoord did not become healthy from node-1 namespace")


def verify_fbforward_rpc_in_namespace(topology: netns.Topology, manager: ProcessManager, node_name: str, control_token: str) -> None:
    node_pid = topology.namespaces[node_name].pid
    process_name = f"fbforward-{node_name}"

    def check() -> bool:
        managed = manager.get(process_name)
        if managed is None or not manager.is_alive(process_name):
            excerpt = log_excerpt(managed.log_path) if managed is not None else "<process not started>"
            raise RuntimeError(f"{process_name} exited early\n{excerpt}")
        status, body = ns_http_request(
            node_pid,
            "http://127.0.0.1:8080/rpc",
            method="POST",
            headers={
                "Authorization": f"Bearer {control_token}",
                "Content-Type": "application/json",
            },
            body=json.dumps({"method": "GetStatus", "params": {}}),
        )
        if status != 200:
            return False
        payload = json.loads(body)
        return bool(payload.get("ok"))

    wait_for_condition(READINESS_TIMEOUT_SEC, check, f"{process_name} RPC did not become ready")


def assert_host_ports_available(extra_bindings: Iterable[tuple[str, str, int]] | None = None) -> None:
    busy: list[str] = []
    bindings = [(name, listen_host, host_port) for name, (listen_host, host_port, _, _, _) in PROXY_SPECS.items()]
    bindings.extend(list(extra_bindings or ()))
    for name, listen_host, host_port in bindings:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                sock.bind((listen_host, host_port))
            except OSError:
                busy.append(f"{name}:{listen_host}:{host_port}")
    if busy:
        raise RuntimeError(f"coordlab proxy ports are already in use: {', '.join(busy)}")


def apply_coordination_mode(node_url: str, control_token: str, *, skip_build: bool) -> None:
    from lib import rpc

    try:
        rpc.set_mode_coordination(node_url, control_token)
    except RuntimeError as exc:
        if skip_build and "invalid mode" in str(exc).lower():
            raise RuntimeError(
                f"{exc}. The existing fbforward binary may be stale; rerun coordlab without --skip-build "
                "or rebuild with `make build`."
            ) from exc
        raise


def build_proxy_infos() -> dict[str, ProxyInfo]:
    return {
        name: ProxyInfo(
            listen_host=listen_host,
            host_port=host_port,
            target_ns=target_ns,
            target_host=target_host,
            target_port=target_port,
        )
        for name, (listen_host, host_port, target_ns, target_host, target_port) in PROXY_SPECS.items()
    }


def start_ttyd_terminals(
    manager: ProcessManager,
    topology: netns.Topology,
    ttyd_ports: dict[str, int],
) -> dict[str, TerminalInfo]:
    terminals: dict[str, TerminalInfo] = {}
    for namespace_name, port in sorted(ttyd_ports.items(), key=lambda item: item[1]):
        managed = manager.start_host(
            build_ttyd_command(ns_pid=topology.namespaces[namespace_name].pid, port=port),
            f"ttyd-{namespace_name}",
        )
        terminals[namespace_name] = TerminalInfo(host_port=port, pid=managed.pid)
    return terminals


def load_active_state(workdir: Path) -> LabState:
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        raise RuntimeError(f"no coordlab state found at {state_path}")
    if not state.active:
        raise RuntimeError(f"coordlab state is not active: {state_path}")
    return state


def build_shaper_from_state(state: LabState):
    from lib.shaping import TrafficShaper

    require_tools(["tc"])
    router_pids = resolve_target_router_pids(state, kind="shaping")
    return TrafficShaper(router_pids, state.shaping)


def build_link_state_controller_from_state(state: LabState):
    from lib.linkstate import LinkStateController

    require_tools(["ip"])
    router_pids = resolve_target_router_pids(state, kind="link-state")
    return LinkStateController(router_pids, state.shaping)


def resolve_target_router_pids(state: LabState, *, kind: str) -> dict[str, int]:
    if not state.shaping.targets:
        raise RuntimeError("coordlab state does not contain target topology; rerun `coordlab.py up`")
    router_pids: dict[str, int] = {}
    for target_name, target in sorted(state.shaping.targets.items()):
        router_info = state.namespaces.get(target.router_ns)
        if router_info is None:
            raise RuntimeError(
                f"coordlab state references unknown {kind} router namespace: {target.router_ns} for {target_name}"
            )
        if not is_alive(router_info.pid):
            raise RuntimeError(f"{kind} router namespace is not alive: {target.router_ns} pid={router_info.pid}")
        router_pids[target.router_ns] = router_info.pid
    return router_pids


def format_shaping_state(states: dict[str, object | None]) -> str:
    lines: list[str] = []
    for target_name, shaping_state in states.items():
        if shaping_state is None:
            lines.append(f"{target_name}: none")
            continue
        delay_ms = getattr(shaping_state, "delay_ms")
        loss_pct = getattr(shaping_state, "loss_pct")
        lines.append(f"{target_name}: delay={delay_ms}ms loss={loss_pct:g}%")
    return "\n".join(lines)


def format_link_state(states: dict[str, object]) -> str:
    lines: list[str] = []
    for target_name, link_state in states.items():
        connected = getattr(link_state, "connected", False)
        lines.append(f"{target_name}: {'connected' if connected else 'disconnected'}")
    return "\n".join(lines)


def cmd_net_up(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    workdir.mkdir(parents=True, exist_ok=True)
    state_path = state_path_for(workdir)
    existing = load_state(state_path)
    if existing is not None:
        alive = existing_lab_is_alive(existing)
        if alive:
            raise RuntimeError(
                f"existing coordlab state is still active in {workdir}: alive entries={', '.join(alive)}"
            )

    require_tools(["unshare", "nsenter", "ip", "sysctl", "ping"])
    client_specs = parse_client_specs(args.client)

    topology = netns.build_topology(str(workdir), client_specs=client_specs)
    try:
        netns.verify_connectivity(topology)
    except Exception:
        netns.destroy_topology(topology)
        raise

    state = build_state(workdir, topology, phase=1, active=True)
    save_state(state_path, state)
    print_basic_status(state)
    return 0


def cmd_net_down(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        print(f"no coordlab state found at {state_path}")
        return 0

    for _, info in namespace_shutdown_order(state.namespaces):
        terminate_pid(info.pid, timeout_sec=5)

    state.active = False
    save_state(state_path, state)
    print(f"coordlab topology stopped for {workdir}")
    return 0


def cmd_net_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        print(f"no coordlab state found at {state_path}")
        return 1
    print_basic_status(state)
    return 0


def cmd_up(args: argparse.Namespace) -> int:
    from lib import readiness

    workdir = Path(args.workdir).expanduser().resolve()
    workdir.mkdir(parents=True, exist_ok=True)
    state_path = state_path_for(workdir)
    existing = load_state(state_path)
    if existing is not None:
        alive = existing_lab_is_alive(existing)
        if alive:
            raise RuntimeError(
                f"existing coordlab state is still active in {workdir}: alive entries={', '.join(alive)}"
            )

    client_specs = parse_client_specs(args.client)
    ttyd_ports = allocate_ttyd_ports(client_specs.keys())
    require_tools(["unshare", "nsenter", "ip", "sysctl", "ping", str(VENV_PYTHON), "ttyd"])
    assert_host_ports_available(
        extra_bindings=[(f"ttyd-{name}", "127.0.0.1", port) for name, port in sorted(ttyd_ports.items())]
    )
    ensure_fbforward_binaries(args.skip_build)
    ensure_fbcoord_assets(args.skip_build)

    tokens = coordconfig.generate_tokens()
    topology = netns.build_topology(str(workdir), client_specs=client_specs)
    manager = ProcessManager(logs_dir_for(workdir))

    try:
        netns.verify_connectivity(topology)
        runtime_dir = coordconfig.prepare_fbcoord_runtime(workdir, tokens.coord_token)

        config_paths = {
            node: coordconfig.generate_fbforward_config(node, topology, tokens, workdir)
            for node in ("node-1", "node-2")
        }
        for config_path in config_paths.values():
            validate_fbforward_config(config_path)

        manager.start(
            topology.namespaces["upstream-1"].pid,
            "upstream-1",
            [str(FBMEASURE_BIN), "--port", "9876"],
            "fbmeasure-upstream-1",
        )
        manager.start(
            topology.namespaces["upstream-2"].pid,
            "upstream-2",
            [str(FBMEASURE_BIN), "--port", "9876"],
            "fbmeasure-upstream-2",
        )
        manager.start(
            topology.namespaces["fbcoord"].pid,
            "fbcoord",
            [*wrangler_command(), "--ip", "0.0.0.0", "--port", "8787"],
            "fbcoord",
            cwd=str(runtime_dir),
            env={"FBCOORD_TOKEN": tokens.coord_token},
        )
        manager.start(
            topology.namespaces["node-1"].pid,
            "node-1",
            [str(FBFORWARD_BIN), "run", "--config", str(config_paths["node-1"])],
            "fbforward-node-1",
        )
        manager.start(
            topology.namespaces["node-2"].pid,
            "node-2",
            [str(FBFORWARD_BIN), "run", "--config", str(config_paths["node-2"])],
            "fbforward-node-2",
        )

        verify_fbcoord_health_in_namespace(topology, manager)
        verify_fbforward_rpc_in_namespace(topology, manager, "node-1", tokens.control_token)
        verify_fbforward_rpc_in_namespace(topology, manager, "node-2", tokens.control_token)

        proxies = build_proxy_infos()
        state = build_state(
            workdir,
            topology,
            phase=5,
            active=True,
            processes=manager.infos(),
            proxies=proxies,
            tokens=tokens,
        )
        save_state(state_path, state)

        manager.start_host(
            [str(VENV_PYTHON), str(COORDLAB_SCRIPT), "proxy-daemon", "--state", str(state_path)],
            PROXY_PROCESS_NAME,
            cwd=str(REPO_ROOT),
        )

        state = build_state(
            workdir,
            topology,
            phase=5,
            active=True,
            processes=manager.infos(),
            proxies=proxies,
            tokens=tokens,
        )
        save_state(state_path, state)

        fbcoord_url = f"http://{proxies['fbcoord'].listen_host}:{proxies['fbcoord'].host_port}"
        node1_url = f"http://{proxies['node-1'].listen_host}:{proxies['node-1'].host_port}"
        node2_url = f"http://{proxies['node-2'].listen_host}:{proxies['node-2'].host_port}"

        readiness.wait_http_ok(f"{fbcoord_url}/healthz")
        readiness.wait_for_status(node1_url, tokens.control_token, predicate=lambda status: True)
        readiness.wait_for_status(node2_url, tokens.control_token, predicate=lambda status: True)

        apply_coordination_mode(node1_url, tokens.control_token, skip_build=args.skip_build)
        apply_coordination_mode(node2_url, tokens.control_token, skip_build=args.skip_build)

        def coordination_connected(status: dict) -> bool:
            coordination = status.get("coordination") or {}
            return status.get("mode") == "coordination" and bool(coordination.get("connected"))

        readiness.wait_for_status(node1_url, tokens.control_token, predicate=coordination_connected)
        readiness.wait_for_status(node2_url, tokens.control_token, predicate=coordination_connected)
        readiness.verify_fbcoord_api(fbcoord_url, tokens.coord_token, expected_pool="lab")

        terminals = start_ttyd_terminals(manager, topology, ttyd_ports)
        state = build_state(
            workdir,
            topology,
            phase=5,
            active=True,
            processes=manager.infos(),
            proxies=proxies,
            terminals=terminals,
            tokens=tokens,
        )
        save_state(state_path, state)
        print(render_summary(state, str(VENV_PYTHON)))
        return 0
    except Exception:
        manager.stop_all()
        netns.destroy_topology(topology)
        raise


def cmd_down(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        print(f"no coordlab state found at {state_path}")
        return 0

    proxy_info = state.processes.get(PROXY_PROCESS_NAME)
    if proxy_info is not None:
        terminate_process_group(proxy_info.pid, timeout_sec=5)
    for name, info in process_shutdown_order(state.processes):
        if name == PROXY_PROCESS_NAME:
            continue
        terminate_process_group(info.pid, timeout_sec=5)
    for _, info in namespace_shutdown_order(state.namespaces):
        terminate_pid(info.pid, timeout_sec=5)

    state.active = False
    save_state(state_path, state)
    print(f"coordlab services stopped for {workdir}")
    return 0


def cmd_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        print(f"no coordlab state found at {state_path}")
        return 1
    print(render_summary(state, str(VENV_PYTHON)))
    return 0


def cmd_shaping_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    shaper = build_shaper_from_state(state)
    print(format_shaping_state(shaper.get_all()))
    return 0


def cmd_shaping_set(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    shaper = build_shaper_from_state(state)
    shaper.set(args.target, delay_ms=args.delay_ms, loss_pct=args.loss_pct)
    print(format_shaping_state(shaper.get_all()))
    return 0


def cmd_shaping_clear(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    shaper = build_shaper_from_state(state)
    shaper.clear(args.target)
    print(format_shaping_state(shaper.get_all()))
    return 0


def cmd_shaping_clear_all(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    shaper = build_shaper_from_state(state)
    shaper.clear_all()
    print(format_shaping_state(shaper.get_all()))
    return 0


def cmd_link_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    controller = build_link_state_controller_from_state(state)
    print(format_link_state(controller.get_all()))
    return 0


def cmd_disconnect(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    controller = build_link_state_controller_from_state(state)
    controller.set_connected(args.target, False)
    print(format_link_state(controller.get_all()))
    return 0


def cmd_reconnect(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    controller = build_link_state_controller_from_state(state)
    controller.set_connected(args.target, True)
    print(format_link_state(controller.get_all()))
    return 0


def cmd_web(args: argparse.Namespace) -> int:
    require_flask_environment()
    from web.app import create_app

    workdir = Path(args.workdir).expanduser().resolve()
    app = create_app(workdir)
    app.run(host=args.host, port=args.port, debug=False, use_reloader=False)
    return 0


def cmd_proxy_daemon(args: argparse.Namespace) -> int:
    run_proxy_daemon(args.state)
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="coordlab.py")
    subparsers = parser.add_subparsers(dest="command", required=True)

    for name, handler, help_text in (
        ("net-up", cmd_net_up, "create the Phase 1 namespace topology"),
        ("net-down", cmd_net_down, "destroy the Phase 1 namespace topology"),
        ("net-status", cmd_net_status, "show the Phase 1 namespace topology state"),
    ):
        sub = subparsers.add_parser(name, help=help_text)
        sub.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
        if name == "net-up":
            sub.add_argument("--client", action="append", default=[], metavar="NAME=IP")
        sub.set_defaults(handler=handler)

    for name, handler, help_text in (
        ("up", cmd_up, "start the Phase 5 coordlab services and host proxies"),
        ("down", cmd_down, "stop the Phase 5 coordlab services and topology"),
        ("status", cmd_status, "show the Phase 5 coordlab state"),
    ):
        sub = subparsers.add_parser(name, help=help_text)
        sub.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
        if name == "up":
            sub.add_argument("--skip-build", action="store_true")
            sub.add_argument("--client", action="append", default=[], metavar="NAME=IP")
        sub.set_defaults(handler=handler)

    web = subparsers.add_parser("web", help="start the Phase 5 coordlab dashboard")
    web.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    web.add_argument("--host", default="127.0.0.1")
    web.add_argument("--port", type=int, default=18800)
    web.set_defaults(handler=cmd_web)

    shaping_status = subparsers.add_parser(
        "shaping-status",
        help="show current node-side and upstream-side shaping state",
    )
    shaping_status.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_status.set_defaults(handler=cmd_shaping_status)

    shaping_set = subparsers.add_parser("shaping-set", help="apply delay/loss shaping to a node or upstream target")
    shaping_set.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_set.add_argument("--target", required=True, choices=["node-1", "node-2", "upstream-1", "upstream-2"])
    shaping_set.add_argument("--delay-ms", type=int, default=0)
    shaping_set.add_argument("--loss-pct", type=float, default=0.0)
    shaping_set.set_defaults(handler=cmd_shaping_set)

    shaping_clear = subparsers.add_parser("shaping-clear", help="clear shaping on one node or upstream target")
    shaping_clear.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_clear.add_argument("--target", required=True, choices=["node-1", "node-2", "upstream-1", "upstream-2"])
    shaping_clear.set_defaults(handler=cmd_shaping_clear)

    shaping_clear_all = subparsers.add_parser(
        "shaping-clear-all",
        help="clear shaping on all node and upstream targets",
    )
    shaping_clear_all.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_clear_all.set_defaults(handler=cmd_shaping_clear_all)

    link_status = subparsers.add_parser("link-status", help="show current live link state for all targets")
    link_status.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    link_status.set_defaults(handler=cmd_link_status)

    disconnect = subparsers.add_parser("disconnect", help="disconnect one node or upstream target")
    disconnect.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    disconnect.add_argument("--target", required=True, choices=["node-1", "node-2", "upstream-1", "upstream-2"])
    disconnect.set_defaults(handler=cmd_disconnect)

    reconnect = subparsers.add_parser("reconnect", help="reconnect one node or upstream target")
    reconnect.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    reconnect.add_argument("--target", required=True, choices=["node-1", "node-2", "upstream-1", "upstream-2"])
    reconnect.set_defaults(handler=cmd_reconnect)

    hidden = subparsers.add_parser("proxy-daemon", help=argparse.SUPPRESS)
    hidden.add_argument("--state", required=True)
    hidden.set_defaults(handler=cmd_proxy_daemon)

    return parser


def main(argv: list[str] | None = None) -> int:
    require_runtime_environment()
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.handler(args)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RuntimeError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1)
