from __future__ import annotations

import subprocess
from pathlib import Path
from typing import Iterable

from .paths import REPO_ROOT, logs_dir_for
from .ports import TTYD_BASE_PORT
from .process import ProcessManager
from .state import ProcessInfo, TerminalInfo


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


def allocate_live_ttyd_port(terminals: dict[str, TerminalInfo]) -> int:
    used = {info.host_port for info in terminals.values()}
    port = TTYD_BASE_PORT
    while port in used:
        port += 1
    return port


def build_ttyd_command(*, ns_pid: int, port: int, namespace_name: str) -> list[str]:
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
        "env",
        f"PS1={namespace_name}@\\w$ ",
        "/bin/bash",
        "--noprofile",
        "--norc",
        "-i",
    ]


def ttyd_process_name(namespace_name: str) -> str:
    return f"ttyd-{namespace_name}"


def next_process_order(processes: dict[str, ProcessInfo]) -> int:
    return max((info.order for info in processes.values()), default=-1) + 1


def start_terminal_process(workdir: Path, namespace_name: str, ns_pid: int, host_port: int) -> tuple[int, str]:
    logs_dir = logs_dir_for(workdir)
    logs_dir.mkdir(parents=True, exist_ok=True)
    log_path = logs_dir / f"{ttyd_process_name(namespace_name)}.log"
    log_handle = log_path.open("wb")
    try:
        process = subprocess.Popen(
            build_ttyd_command(ns_pid=ns_pid, port=host_port, namespace_name=namespace_name),
            cwd=str(REPO_ROOT),
            stdout=log_handle,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
    finally:
        log_handle.close()
    return process.pid, str(log_path)


def start_ttyd_terminals(
    manager: ProcessManager,
    topology,
    ttyd_ports: dict[str, int],
) -> dict[str, TerminalInfo]:
    terminals: dict[str, TerminalInfo] = {}
    for namespace_name, port in sorted(ttyd_ports.items(), key=lambda item: item[1]):
        managed = manager.start_host(
            build_ttyd_command(
                ns_pid=topology.namespaces[namespace_name].pid,
                port=port,
                namespace_name=namespace_name,
            ),
            ttyd_process_name(namespace_name),
        )
        terminals[namespace_name] = TerminalInfo(host_port=port, pid=managed.pid)
    return terminals
