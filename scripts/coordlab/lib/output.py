from __future__ import annotations

from pathlib import Path

from .process import is_alive
from .state import LabState


def proxy_url(state: LabState, name: str) -> str | None:
    proxy = state.proxies.get(name)
    if proxy is None:
        return None
    return f"http://{proxy.listen_host}:{proxy.host_port}"


def terminal_url(host_port: int) -> str:
    return f"http://127.0.0.1:{host_port}"


def render_summary(state: LabState, python_executable: str) -> str:
    lines: list[str] = []
    workdir = Path(state.work_dir)
    lines.append("=== coordlab ===")
    lines.append("")

    fbcoord_url = proxy_url(state, "fbcoord")
    node1_url = proxy_url(state, "node-1")
    node2_url = proxy_url(state, "node-2")
    if fbcoord_url:
        lines.append(f"  fbcoord: {fbcoord_url}")
    if node1_url:
        lines.append(f"  node-1:  {node1_url}  (UI /, RPC /rpc, metrics /metrics)")
    if node2_url:
        lines.append(f"  node-2:  {node2_url}  (UI /, RPC /rpc, metrics /metrics)")
    lines.append("")

    lines.append("  process status:")
    for name, info in sorted(state.processes.items(), key=lambda item: (item[1].order, item[0])):
        status = "alive" if is_alive(info.pid) else "dead"
        lines.append(f"    {name}: {status} pid={info.pid} ns={info.ns}")
    lines.append("")

    lines.append("  namespace status:")
    for name, info in sorted(state.namespaces.items()):
        status = "alive" if is_alive(info.pid) else "dead"
        lines.append(f"    {name}: {status} pid={info.pid}")
    lines.append("")

    if state.proxies:
        lines.append("  proxies:")
        for name, proxy in sorted(state.proxies.items()):
            lines.append(
                f"    {name}: {proxy.listen_host}:{proxy.host_port} -> "
                f"{proxy.target_ns}:{proxy.target_host}:{proxy.target_port}"
            )
        lines.append("")

    if state.clients:
        lines.append("  clients:")
        for name, info in sorted(state.clients.items()):
            lines.append(f"    {name}: {info.identity_ip}")
        lines.append("")

    if state.terminals:
        lines.append("  terminals:")
        for name, info in sorted(state.terminals.items()):
            status = "alive" if is_alive(info.pid) else "dead"
            lines.append(f"    {name}: {terminal_url(info.host_port)} ({status})")
        lines.append("")

    if state.tokens.coord_token or state.tokens.control_token:
        lines.append("  tokens:")
        if state.tokens.coord_token:
            lines.append(f"    coordination: {state.tokens.coord_token}")
        if state.tokens.control_token:
            lines.append(f"    control:      {state.tokens.control_token}")
        lines.append("")

    lines.append("  artifacts:")
    lines.append(f"    logs:     {workdir / 'logs'}")
    lines.append(f"    configs:  {workdir / 'configs'}")
    lines.append(f"    state:    {workdir / 'state.json'}")
    lines.append("")

    script = Path(__file__).resolve().parents[1] / "coordlab.py"
    lines.append("  commands:")
    lines.append(f"    {python_executable} {script} status --workdir {workdir}")
    lines.append(f"    {python_executable} {script} web --workdir {workdir}")
    lines.append(f"    {python_executable} {script} down --workdir {workdir}")
    return "\n".join(lines)
