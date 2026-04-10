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


def terminal_label(namespace_name: str, pid: int) -> str:
    return f"{namespace_name} - {pid}"


def proxy_dict(state: LabState) -> dict[str, dict]:
    return {
        name: {
            "listen_host": proxy.listen_host,
            "host_port": proxy.host_port,
            "target_ns": proxy.target_ns,
            "target_host": proxy.target_host,
            "target_port": proxy.target_port,
        }
        for name, proxy in sorted(state.proxies.items())
    }


def service_links(state: LabState) -> dict[str, str]:
    links: dict[str, str] = {}
    for name in ("fbcoord", "fbnotify", "node-1", "node-2"):
        url = proxy_url(state, name)
        if url:
            links[name] = url
    return links


def client_dict(state: LabState) -> dict[str, dict]:
    return {
        name: {
            "identity_ip": info.identity_ip,
        }
        for name, info in sorted(state.clients.items())
    }


def terminal_dict(state: LabState) -> dict[str, dict]:
    return {
        name: {
            "host_port": info.host_port,
            "pid": info.pid,
            "alive": is_alive(info.pid),
            "url": terminal_url(info.host_port),
            "label": terminal_label(name, info.pid),
        }
        for name, info in sorted(state.terminals.items())
    }


def node_feature_dict(state: LabState) -> dict[str, dict]:
    return {
        name: {
            "geoip": {
                "enabled": info.geoip.enabled,
                "asn_db_url": info.geoip.asn_db_url,
                "asn_db_path": info.geoip.asn_db_path,
                "country_db_url": info.geoip.country_db_url,
                "country_db_path": info.geoip.country_db_path,
                "refresh_interval": info.geoip.refresh_interval,
            },
            "ip_log": {
                "enabled": info.ip_log.enabled,
                "db_path": info.ip_log.db_path,
                "retention": info.ip_log.retention,
                "geo_queue_size": info.ip_log.geo_queue_size,
                "write_queue_size": info.ip_log.write_queue_size,
                "batch_size": info.ip_log.batch_size,
                "flush_interval": info.ip_log.flush_interval,
                "prune_interval": info.ip_log.prune_interval,
            },
            "firewall": {
                "enabled": info.firewall.enabled,
                "default_policy": info.firewall.default_policy,
                "rules": [
                    {
                        "action": rule.action,
                        "cidr": rule.cidr,
                        "asn": rule.asn,
                        "country": rule.country,
                    }
                    for rule in info.firewall.rules
                ],
            },
        }
        for name, info in sorted(state.node_features.items())
    }


def inactive_status_payload(workdir: Path, error: str) -> dict:
    return {
        "active": False,
        "error": error,
        "phase": None,
        "work_dir": str(workdir),
        "state_path": str(workdir / "state.json"),
        "namespaces": [],
        "processes": [],
        "proxies": {},
        "clients": {},
        "terminals": {},
        "node_features": {},
        "service_links": {},
        "fbnotify": {
            "available": False,
            "error": "",
            "public_url": "",
            "internal_base_url": "",
            "internal_ingest_url": "",
        },
        "shaping_targets": [],
        "topology_links": [],
    }


def status_payload(state: LabState | None, workdir: Path) -> dict:
    if state is None:
        return inactive_status_payload(workdir, f"no coordlab state found at {workdir / 'state.json'}")
    if not state.active:
        return {
            **inactive_status_payload(workdir, f"coordlab state is not active: {workdir / 'state.json'}"),
            "phase": state.phase,
        }

    return {
        "active": True,
        "phase": state.phase,
        "work_dir": state.work_dir,
        "state_path": str(workdir / "state.json"),
        "namespaces": [
            {
                "name": name,
                "pid": info.pid,
                "parent": info.parent,
                "role": info.role,
                "alive": is_alive(info.pid),
            }
            for name, info in sorted(state.namespaces.items())
        ],
        "processes": [
            {
                "name": name,
                "pid": info.pid,
                "ns": info.ns,
                "alive": is_alive(info.pid),
                "log_path": info.log_path,
                "order": info.order,
            }
            for name, info in sorted(state.processes.items(), key=lambda item: (item[1].order, item[0]))
        ],
        "proxies": proxy_dict(state),
        "clients": client_dict(state),
        "terminals": terminal_dict(state),
        "node_features": node_feature_dict(state),
        "service_links": service_links(state),
        "fbnotify": {
            "available": state.fbnotify.available,
            "error": state.fbnotify.error,
            "public_url": state.fbnotify.public_url,
            "internal_base_url": state.fbnotify.internal_base_url,
            "internal_ingest_url": state.fbnotify.internal_ingest_url,
        },
        "shaping_targets": [
            {
                "target": target_name,
                "router_ns": target.router_ns,
                "tag": target.tag,
                "namespace": target.namespace,
                "device": target.device,
            }
            for target_name, target in sorted(state.shaping.targets.items())
        ],
        "topology_links": [
            {
                "left_ns": link.left_ns,
                "right_ns": link.right_ns,
                "left_if": link.left_if,
                "right_if": link.right_if,
                "left_ip": link.left_ip,
                "right_ip": link.right_ip,
                "subnet": link.subnet,
            }
            for link in state.topology.links
        ],
    }


def render_summary(state: LabState, python_executable: str) -> str:
    lines: list[str] = []
    workdir = Path(state.work_dir)
    lines.append("=== coordlab ===")
    lines.append("")

    fbcoord_url = proxy_url(state, "fbcoord")
    fbnotify_url = proxy_url(state, "fbnotify")
    node1_url = proxy_url(state, "node-1")
    node2_url = proxy_url(state, "node-2")
    if fbcoord_url:
        lines.append(f"  fbcoord: {fbcoord_url}")
    if fbnotify_url:
        lines.append(f"  fbnotify: {fbnotify_url}")
    if node1_url:
        lines.append(f"  node-1:  {node1_url}  (UI /, RPC /rpc, metrics /metrics)")
    if node2_url:
        lines.append(f"  node-2:  {node2_url}  (UI /, RPC /rpc, metrics /metrics)")
    lines.append("")

    if state.fbnotify.public_url or state.fbnotify.error:
        status = "available" if state.fbnotify.available else "degraded"
        lines.append("  fbnotify:")
        lines.append(f"    status: {status}")
        if state.fbnotify.public_url:
            lines.append(f"    public_url: {state.fbnotify.public_url}")
        if state.fbnotify.internal_base_url:
            lines.append(f"    internal_url: {state.fbnotify.internal_base_url}")
        if state.fbnotify.error:
            lines.append(f"    error: {state.fbnotify.error}")
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

    if state.node_features:
        lines.append("  node features:")
        for name, features in sorted(state.node_features.items()):
            geoip_status = "enabled" if features.geoip.enabled else "disabled"
            ip_log_status = "enabled" if features.ip_log.enabled else "disabled"
            firewall_status = "enabled" if features.firewall.enabled else "disabled"
            lines.append(f"    {name}:")
            lines.append(
                f"      geoip: {geoip_status} "
                f"asn_db={features.geoip.asn_db_path} country_db={features.geoip.country_db_path}"
            )
            lines.append(f"      ip_log: {ip_log_status} db={features.ip_log.db_path}")
            lines.append(f"      firewall: {firewall_status} default={features.firewall.default_policy}")
        lines.append("")

    if state.tokens.control_token or state.tokens.operator_token or state.tokens.node_tokens:
        lines.append("  tokens:")
        if state.tokens.control_token:
            lines.append(f"    control:      {state.tokens.control_token}")
        if state.tokens.operator_token:
            lines.append(f"    operator:     {state.tokens.operator_token}")
        for node_id, token in sorted(state.tokens.node_tokens.items()):
            lines.append(f"    node[{node_id}]: {token}")
        lines.append("")

    lines.append("  artifacts:")
    lines.append(f"    data:     {workdir / 'data'}")
    lines.append(f"    logs:     {workdir / 'logs'}")
    lines.append(f"    mmdb:     {workdir / 'mmdb'}")
    lines.append(f"    configs:  {workdir / 'configs'}")
    lines.append(f"    state:    {workdir / 'state.json'}")
    lines.append("")

    script = Path(__file__).resolve().parents[1] / "coordlab.py"
    lines.append("  commands:")
    lines.append(f"    {python_executable} {script} status --workdir {workdir}")
    lines.append(f"    {python_executable} {script} web --workdir {workdir}")
    lines.append(f"    {python_executable} {script} down --workdir {workdir}")
    return "\n".join(lines)
