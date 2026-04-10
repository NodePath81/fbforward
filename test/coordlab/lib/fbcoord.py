from __future__ import annotations

import json
from typing import Iterable

from . import netns
from .http import ns_http_request, ns_http_request_with_headers
from .lab import log_excerpt
from .process import ProcessManager
from .readiness import READINESS_TIMEOUT_SEC, wait_for_condition


def fbcoord_namespace_base_url(topology: netns.Topology) -> str:
    fbcoord_ip = netns.find_link(topology.links, "hub", "fbcoord").right_ip
    return f"http://{fbcoord_ip}:8787"


def extract_session_cookie(response_headers: dict[str, str]) -> str:
    header = response_headers.get("Set-Cookie") or response_headers.get("set-cookie") or ""
    return header.split(";", 1)[0].strip()


def mint_fbcoord_node_tokens(base_url: str, request_pid: int, operator_token: str, node_ids: Iterable[str]) -> dict[str, str]:
    login_status, login_headers, login_body = ns_http_request_with_headers(
        request_pid,
        f"{base_url.rstrip('/')}/api/auth/login",
        method="POST",
        headers={"Content-Type": "application/json"},
        body=json.dumps({"token": operator_token}),
    )
    if login_status != 200:
        raise RuntimeError(f"fbcoord login failed: status={login_status} body={login_body.strip()}")

    session_cookie = extract_session_cookie(login_headers)
    if not session_cookie:
        raise RuntimeError("fbcoord login did not return a session cookie")

    minted_tokens: dict[str, str] = {}
    for node_id in node_ids:
        status, body = ns_http_request(
            request_pid,
            f"{base_url.rstrip('/')}/api/node-tokens",
            method="POST",
            headers={
                "Content-Type": "application/json",
                "Cookie": session_cookie,
            },
            body=json.dumps({"node_id": node_id}),
        )
        if status != 200:
            raise RuntimeError(f"fbcoord node-token mint failed for {node_id}: status={status} body={body.strip()}")

        payload = json.loads(body)
        token = payload.get("token")
        if not isinstance(token, str) or not token:
            raise RuntimeError(f"fbcoord node-token mint returned invalid token for {node_id}: {payload!r}")
        minted_tokens[node_id] = token

    return minted_tokens


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


def apply_coordination_mode(node_url: str, control_token: str, *, skip_build: bool) -> None:
    from . import rpc

    try:
        rpc.set_mode_coordination(node_url, control_token)
    except RuntimeError as exc:
        if skip_build and "invalid mode" in str(exc).lower():
            raise RuntimeError(
                f"{exc}. The existing fbforward binary may be stale; rerun coordlab without --skip-build "
                "or rebuild with `make build`."
            ) from exc
        raise
