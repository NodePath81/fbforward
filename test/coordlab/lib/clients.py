from __future__ import annotations

from pathlib import Path

from . import netns
from .env import validate_client_spec
from .lab import load_active_state, save_current_state, sync_state_topology, topology_from_state
from .locking import acquire_client_mutation_lock, acquire_network_mutation_lock
from .ports import assert_bindings_available
from .process import terminate_process_group
from .state import LabState, ProcessInfo, TerminalInfo
from .terminal import (
    allocate_live_ttyd_port,
    next_process_order,
    start_terminal_process,
    ttyd_process_name,
)


def ensure_client_edge(state: LabState) -> LabState:
    topology = topology_from_state(state)
    if "client-edge" in topology.namespaces:
        return state
    namespace, link, next_subnet_index = netns.create_client_edge(topology)
    topology.namespaces.setdefault("client-edge", namespace)
    if not any(existing.left_ns == "internet" and existing.right_ns == "client-edge" for existing in topology.links):
        topology.links.append(link)
    topology.next_subnet_index = max(topology.next_subnet_index, next_subnet_index)
    sync_state_topology(state, topology)
    save_current_state(state)
    return state


def add_client(state: LabState, name: str, identity_ip: str, *, skip_connectivity_check: bool = False) -> LabState:
    if not state.active:
        raise RuntimeError("coordlab state is not active")
    normalized_ip = validate_client_spec(
        name,
        identity_ip,
        base_cidr=state.topology.base_cidr,
        existing_names=state.clients,
        existing_ips=(info.identity_ip for info in state.clients.values()),
    )
    workdir = Path(state.work_dir)
    host_port = allocate_live_ttyd_port(state.terminals)
    assert_bindings_available(
        [(ttyd_process_name(name), "127.0.0.1", host_port)],
        error_prefix="coordlab ttyd ports are already in use",
    )

    topology = topology_from_state(state)
    created_client_edge = "client-edge" not in topology.namespaces
    terminal_pid: int | None = None
    try:
        if created_client_edge:
            edge_namespace, edge_link, edge_cursor = netns.create_client_edge(topology)
            topology.namespaces.setdefault("client-edge", edge_namespace)
            if not any(existing.left_ns == "internet" and existing.right_ns == "client-edge" for existing in topology.links):
                topology.links.append(edge_link)
            topology.next_subnet_index = max(topology.next_subnet_index, edge_cursor)
        namespace, client_link, next_subnet_index = netns.create_client_namespace(topology, name, normalized_ip)
        topology.namespaces.setdefault(name, namespace)
        if not any(existing.left_ns == "client-edge" and existing.right_ns == name for existing in topology.links):
            topology.links.append(client_link)
        topology.clients.setdefault(name, normalized_ip)
        topology.next_subnet_index = max(topology.next_subnet_index, next_subnet_index)
        if not skip_connectivity_check:
            netns.verify_connectivity(topology)
        terminal_pid, log_path = start_terminal_process(workdir, name, namespace.pid, host_port)
    except Exception:
        if terminal_pid is not None:
            terminate_process_group(terminal_pid, timeout_sec=5)
        try:
            if name in topology.namespaces:
                netns.remove_client_namespace(topology, name)
        except Exception:
            pass
        if created_client_edge:
            try:
                netns.remove_client_edge(topology)
            except Exception:
                pass
        raise

    sync_state_topology(state, topology)
    state.terminals[name] = TerminalInfo(host_port=host_port, pid=terminal_pid)
    state.processes[ttyd_process_name(name)] = ProcessInfo(
        pid=terminal_pid,
        ns="host",
        log_path=log_path,
        order=next_process_order(state.processes),
    )
    save_current_state(state)
    return state


def remove_client(state: LabState, name: str) -> LabState:
    if name not in state.clients:
        raise KeyError(f"unknown client namespace: {name}")
    topology = topology_from_state(state)
    terminal = state.terminals.get(name)
    if terminal is not None:
        terminate_process_group(terminal.pid, timeout_sec=5)
    netns.remove_client_namespace(topology, name)
    topology.namespaces.pop(name, None)
    topology.clients.pop(name, None)
    topology.links = [
        link
        for link in topology.links
        if not (link.left_ns == "client-edge" and link.right_ns == name)
    ]
    sync_state_topology(state, topology)
    state.terminals.pop(name, None)
    state.processes.pop(ttyd_process_name(name), None)
    save_current_state(state)
    return state


def run_locked_add_client(
    workdir: Path,
    name: str,
    identity_ip: str,
    *,
    skip_connectivity_check: bool = False,
) -> LabState:
    with acquire_client_mutation_lock(workdir):
        with acquire_network_mutation_lock(workdir):
            state = load_active_state(workdir)
            return add_client(state, name, identity_ip, skip_connectivity_check=skip_connectivity_check)


def run_locked_remove_client(workdir: Path, name: str) -> LabState:
    with acquire_client_mutation_lock(workdir):
        with acquire_network_mutation_lock(workdir):
            state = load_active_state(workdir)
            if name not in state.clients:
                raise KeyError(f"unknown client namespace: {name}")
            return remove_client(state, name)
