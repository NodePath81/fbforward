from __future__ import annotations

import subprocess
from datetime import datetime, timezone
from pathlib import Path

from . import config as coordconfig
from . import netns
from .env import require_tools
from .paths import state_path_for
from .process import is_alive
from .state import (
    ClientInfo,
    DesiredTargetState,
    FBNotifyInfo,
    LabState,
    LinkInfo,
    NamespaceInfo,
    NodeFeatureInfo,
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
    node_features: dict[str, NodeFeatureInfo] | None = None,
    shaping: ShapingInfo | None = None,
    tokens: TokenInfo | None = None,
    fbnotify: FBNotifyInfo | None = None,
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
        node_features=node_features or {},
        shaping=build_shaping_info(topology, existing=shaping),
        tokens=tokens or TokenInfo(),
        fbnotify=fbnotify or FBNotifyInfo(),
        topology=TopologyInfo(
            base_cidr=topology.base_cidr,
            links=links,
            next_subnet_index=topology.next_subnet_index or len(links),
        ),
    )


def _merge_desired_target_state(existing: ShapingInfo | None, target_name: str) -> DesiredTargetState:
    if existing is None:
        return DesiredTargetState()
    desired = existing.desired.get(target_name)
    if desired is None:
        return DesiredTargetState()
    return DesiredTargetState(
        connected=desired.connected,
        delay_ms=desired.delay_ms,
        loss_pct=desired.loss_pct,
    )


def _target_from_link(
    topology: netns.Topology,
    *,
    target_name: str,
    router_ns: str,
    namespace: str,
    tag: str,
    kind: str,
    shape_capable: bool,
    display_name: str,
) -> ShapingTargetInfo:
    link = netns.find_link(topology.links, router_ns, namespace)
    return ShapingTargetInfo(
        router_ns=router_ns,
        tag=tag,
        namespace=namespace,
        device=link.left_if,
        kind=kind,
        peer_device=link.right_if,
        shape_capable=shape_capable,
        display_name=display_name or target_name,
    )


def _maybe_target_from_link(
    topology: netns.Topology,
    *,
    target_name: str,
    router_ns: str,
    namespace: str,
    tag: str,
    kind: str,
    shape_capable: bool,
    display_name: str,
) -> tuple[str, ShapingTargetInfo] | None:
    if router_ns not in topology.namespaces or namespace not in topology.namespaces:
        return None
    try:
        target = _target_from_link(
            topology,
            target_name=target_name,
            router_ns=router_ns,
            namespace=namespace,
            tag=tag,
            kind=kind,
            shape_capable=shape_capable,
            display_name=display_name,
        )
    except ValueError:
        return None
    return target_name, target


def build_shaping_info(topology: netns.Topology, *, existing: ShapingInfo | None = None) -> ShapingInfo:
    targets: dict[str, ShapingTargetInfo] = {}
    fixed_targets = (
        ("fbcoord", "hub", "fbcoord", "", "service", False, "fbcoord"),
        ("fbnotify", "hub", "fbnotify", "", "service", False, "fbnotify"),
        ("node-1", "hub", "node-1", "", "node", True, "node-1"),
        ("node-2", "hub", "node-2", "", "node", True, "node-2"),
        ("upstream-1", "hub-up", "upstream-1", "us-1", "upstream", True, "upstream-1"),
        ("upstream-2", "hub-up", "upstream-2", "us-2", "upstream", True, "upstream-2"),
    )
    for target_name, router_ns, namespace, tag, kind, shape_capable, display_name in fixed_targets:
        maybe_target = _maybe_target_from_link(
            topology,
            target_name=target_name,
            router_ns=router_ns,
            namespace=namespace,
            tag=tag,
            kind=kind,
            shape_capable=shape_capable,
            display_name=display_name,
        )
        if maybe_target is None:
            continue
        name, target = maybe_target
        targets[name] = target
    for client_name in sorted(topology.clients):
        maybe_target = _maybe_target_from_link(
            topology,
            target_name=client_name,
            router_ns="client-edge",
            namespace=client_name,
            tag="",
            kind="client",
            shape_capable=False,
            display_name=client_name,
        )
        if maybe_target is None:
            continue
        name, target = maybe_target
        targets[name] = target
    return ShapingInfo(
        targets=targets,
        desired={
            target_name: _merge_desired_target_state(existing, target_name)
            for target_name in targets
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


def build_node_feature_summary(workdir: Path) -> dict[str, NodeFeatureInfo]:
    return {
        node_name: coordconfig.build_node_feature_info(node_name, workdir)
        for node_name in ("node-1", "node-2")
    }


def validate_fbforward_config(config_path: Path, env: dict[str, str] | None = None) -> None:
    from .build import FBFORWARD_BIN

    run_host([str(FBFORWARD_BIN), "check", "--config", str(config_path)], env=env)


def log_excerpt(log_path: str, *, lines: int = 20) -> str:
    path = Path(log_path)
    if not path.exists():
        return "<log file not found>"
    content = path.read_text(encoding="utf-8", errors="replace").splitlines()
    if not content:
        return "<log file empty>"
    return "\n".join(content[-lines:])


def topology_from_state(state: LabState) -> netns.Topology:
    return netns.Topology(
        work_dir=state.work_dir,
        namespaces={
            name: netns.Namespace(name=name, pid=info.pid, parent=info.parent, role=info.role)
            for name, info in state.namespaces.items()
        },
        links=[
            netns.Link(
                left_ns=link.left_ns,
                right_ns=link.right_ns,
                left_if=link.left_if,
                right_if=link.right_if,
                subnet=link.subnet,
                left_ip=link.left_ip,
                right_ip=link.right_ip,
            )
            for link in state.topology.links
        ],
        base_cidr=state.topology.base_cidr,
        clients={name: info.identity_ip for name, info in state.clients.items()},
        next_subnet_index=state.topology.next_subnet_index or len(state.topology.links),
    )


def sync_state_topology(state: LabState, topology: netns.Topology) -> None:
    state.namespaces = {
        name: NamespaceInfo(pid=ns.pid, parent=ns.parent, role=ns.role)
        for name, ns in topology.namespaces.items()
    }
    state.clients = {
        name: ClientInfo(identity_ip=identity_ip)
        for name, identity_ip in sorted(topology.clients.items())
    }
    state.topology = TopologyInfo(
        base_cidr=topology.base_cidr,
        links=[
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
        ],
        next_subnet_index=topology.next_subnet_index,
    )
    state.shaping = build_shaping_info(topology, existing=state.shaping)


def save_current_state(state: LabState) -> None:
    save_state(state_path_for(Path(state.work_dir)), state)


def load_active_state(workdir: Path) -> LabState:
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        raise RuntimeError(f"no coordlab state found at {state_path}")
    if not state.active:
        raise RuntimeError(f"coordlab state is not active: {state_path}")
    return normalize_state_topology(state)


def normalize_state_topology(state: LabState) -> LabState:
    if not state.topology.links:
        state.shaping = ShapingInfo(
            targets=state.shaping.targets,
            desired={
                target_name: _merge_desired_target_state(state.shaping, target_name)
                for target_name in state.shaping.targets
            },
        )
        return state
    topology = topology_from_state(state)
    state.shaping = build_shaping_info(topology, existing=state.shaping)
    return state


def build_shaper_from_state(state: LabState):
    from .shaping import TrafficShaper

    require_tools(["tc"])
    router_pids = resolve_target_router_pids(state, kind="shaping")
    shaping = ShapingInfo(
        targets={
            name: target
            for name, target in state.shaping.targets.items()
            if target.shape_capable
        },
        desired={
            name: desired
            for name, desired in state.shaping.desired.items()
            if name in state.shaping.targets and state.shaping.targets[name].shape_capable
        },
    )
    return TrafficShaper(router_pids, shaping)


def build_link_state_controller_from_state(state: LabState):
    from .linkstate import LinkStateController

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
