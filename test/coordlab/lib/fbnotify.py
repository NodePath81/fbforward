from __future__ import annotations

import base64
import hashlib
import hmac
import json
import time

import httpx

from . import netns
from .fbcoord import extract_session_cookie
from .http import ns_http_request, ns_http_request_with_headers
from .lab import log_excerpt
from .ports import PROXY_SPECS
from .process import ProcessManager
from .readiness import READINESS_TIMEOUT_SEC, wait_for_condition
from .state import FBNotifyEmitterInfo, FBNotifyInfo, LabState

FBNOTIFY_SOURCE_INSTANCE = "fbcoord"
FBNOTIFY_TARGET_NAME = "coordlab-capture"
FBNOTIFY_ROUTE_NAME = "coordlab-default"
FBNOTIFY_NODE_TOKEN_ENVS = {
    "node-1": "FBNOTIFY_TOKEN_NODE_1",
    "node-2": "FBNOTIFY_TOKEN_NODE_2",
}


class NotificationWaitTimeout(RuntimeError):
    def __init__(self, messages: list[dict]) -> None:
        super().__init__("timed out")
        self.messages = messages


def fbnotify_namespace_base_url(topology: netns.Topology) -> str:
    fbnotify_ip = netns.find_link(topology.links, "hub", "fbnotify").right_ip
    return f"http://{fbnotify_ip}:8787"


def fbnotify_ingest_url(topology: netns.Topology) -> str:
    return f"{fbnotify_namespace_base_url(topology)}/v1/events"


def fbnotify_public_url() -> str:
    listen_host, host_port, _, _, _ = PROXY_SPECS["fbnotify"]
    return f"http://{listen_host}:{host_port}"


def build_fbnotify_ingress_headers(key_id: str, token: str, raw_body: str, *, header_timestamp: int | None = None) -> dict[str, str]:
    ts = int(time.time()) if header_timestamp is None else int(header_timestamp)
    payload = f"{ts}.{raw_body}".encode("utf-8")
    digest = hmac.new(token.encode("utf-8"), payload, hashlib.sha256).digest()
    signature = base64.urlsafe_b64encode(digest).decode("ascii").rstrip("=")
    return {
        "Content-Type": "application/json",
        "X-FBNotify-Key-Id": key_id,
        "X-FBNotify-Timestamp": str(ts),
        "X-FBNotify-Signature": signature,
    }


def emit_fbnotify_event(url: str, key_id: str, token: str, event: dict, *, request_pid: int | None = None) -> tuple[int, str]:
    raw_body = json.dumps(event)
    headers = build_fbnotify_ingress_headers(key_id, token, raw_body)
    if request_pid is not None:
        return ns_http_request(request_pid, url, method="POST", headers=headers, body=raw_body)
    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        response = client.post(url, headers=headers, content=raw_body)
        return response.status_code, response.text


def fbnotify_login_in_namespace(base_url: str, request_pid: int, operator_token: str) -> str:
    login_status, login_headers, login_body = ns_http_request_with_headers(
        request_pid,
        f"{base_url.rstrip('/')}/api/auth/login",
        method="POST",
        headers={"Content-Type": "application/json"},
        body=json.dumps({"token": operator_token}),
    )
    if login_status != 200:
        raise RuntimeError(f"fbnotify login failed: status={login_status} body={login_body.strip()}")

    session_cookie = extract_session_cookie(login_headers)
    if not session_cookie:
        raise RuntimeError("fbnotify login did not return a session cookie")
    return session_cookie


def bootstrap_fbnotify(base_url: str, request_pid: int, operator_token: str) -> dict[str, FBNotifyEmitterInfo]:
    session_cookie = fbnotify_login_in_namespace(base_url, request_pid, operator_token)
    headers = {
        "Content-Type": "application/json",
        "Cookie": session_cookie,
    }

    target_status, target_body = ns_http_request(
        request_pid,
        f"{base_url.rstrip('/')}/api/targets",
        method="POST",
        headers=headers,
        body=json.dumps({"name": FBNOTIFY_TARGET_NAME, "type": "capture", "config": {}}),
    )
    if target_status != 200:
        raise RuntimeError(f"fbnotify target bootstrap failed: status={target_status} body={target_body.strip()}")
    target_payload = json.loads(target_body)
    target_id = target_payload.get("id")
    if not isinstance(target_id, str) or not target_id:
        raise RuntimeError(f"fbnotify target bootstrap returned invalid id: {target_payload!r}")

    route_status, route_body = ns_http_request(
        request_pid,
        f"{base_url.rstrip('/')}/api/routes",
        method="POST",
        headers=headers,
        body=json.dumps(
            {
                "name": FBNOTIFY_ROUTE_NAME,
                "source_service": None,
                "event_name": None,
                "target_ids": [target_id],
            }
        ),
    )
    if route_status != 200:
        raise RuntimeError(f"fbnotify route bootstrap failed: status={route_status} body={route_body.strip()}")

    emitter_specs = {
        "node-1": ("fbforward", "node-1"),
        "node-2": ("fbforward", "node-2"),
        "fbcoord": ("fbcoord", FBNOTIFY_SOURCE_INSTANCE),
    }
    emitters: dict[str, FBNotifyEmitterInfo] = {}
    for emitter_name, (source_service, source_instance) in emitter_specs.items():
        status, body = ns_http_request(
            request_pid,
            f"{base_url.rstrip('/')}/api/node-tokens",
            method="POST",
            headers=headers,
            body=json.dumps(
                {
                    "source_service": source_service,
                    "source_instance": source_instance,
                }
            ),
        )
        if status != 200:
            raise RuntimeError(f"fbnotify node-token mint failed for {emitter_name}: status={status} body={body.strip()}")
        payload = json.loads(body)
        key_id = payload.get("key_id")
        token = payload.get("token")
        if not isinstance(key_id, str) or not key_id or not isinstance(token, str) or not token:
            raise RuntimeError(f"fbnotify node-token mint returned invalid payload for {emitter_name}: {payload!r}")
        emitters[emitter_name] = FBNotifyEmitterInfo(
            key_id=key_id,
            token=token,
            source_service=source_service,
            source_instance=source_instance,
        )

    return emitters


def verify_fbnotify_health_in_namespace(topology: netns.Topology, manager: ProcessManager) -> None:
    fbnotify_url = fbnotify_namespace_base_url(topology)
    node_pid = topology.namespaces["node-1"].pid

    def check() -> bool:
        managed = manager.get("fbnotify")
        if managed is None or not manager.is_alive("fbnotify"):
            excerpt = log_excerpt(managed.log_path) if managed is not None else "<process not started>"
            raise RuntimeError(f"fbnotify exited early\n{excerpt}")
        status, body = ns_http_request(node_pid, f"{fbnotify_url}/healthz")
        return status == 200 and body.strip() == "ok"

    wait_for_condition(READINESS_TIMEOUT_SEC, check, "fbnotify did not become healthy from node-1 namespace")


def require_fbnotify_available(state: LabState) -> FBNotifyInfo:
    if not state.fbnotify.available or not state.fbnotify.public_url:
        raise RuntimeError("fbnotify is not available for this lab run")
    return state.fbnotify


def fbnotify_host_session_cookie(info: FBNotifyInfo) -> str:
    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        response = client.post(f"{info.public_url.rstrip('/')}/api/auth/login", json={"token": info.operator_token})
    if response.status_code != 200:
        raise RuntimeError(f"fbnotify login failed: status={response.status_code} body={response.text.strip()}")
    session_cookie = response.headers.get("set-cookie", "").split(";", 1)[0].strip()
    if not session_cookie:
        raise RuntimeError("fbnotify login did not return a session cookie")
    return session_cookie


def list_ntfybox_messages(state: LabState) -> list[dict]:
    info = require_fbnotify_available(state)
    session_cookie = fbnotify_host_session_cookie(info)
    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        response = client.get(
            f"{info.public_url.rstrip('/')}/api/capture/messages",
            headers={"Cookie": session_cookie},
        )
    if response.status_code != 200:
        raise RuntimeError(f"fbnotify capture fetch failed: status={response.status_code} body={response.text.strip()}")
    payload = response.json()
    messages = payload.get("messages")
    if not isinstance(messages, list):
        raise RuntimeError(f"fbnotify capture fetch returned invalid payload: {payload!r}")
    return messages


def clear_ntfybox_messages(state: LabState) -> None:
    info = require_fbnotify_available(state)
    session_cookie = fbnotify_host_session_cookie(info)
    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        response = client.post(
            f"{info.public_url.rstrip('/')}/api/capture/clear",
            headers={"Cookie": session_cookie},
        )
    if response.status_code != 200:
        raise RuntimeError(f"fbnotify capture clear failed: status={response.status_code} body={response.text.strip()}")


def _payload_attributes(message: dict) -> dict:
    payload = message.get("payload")
    if not isinstance(payload, str):
        return {}
    try:
        decoded = json.loads(payload)
    except json.JSONDecodeError:
        return {}
    if not isinstance(decoded, dict):
        return {}
    attributes = decoded.get("attributes")
    return attributes if isinstance(attributes, dict) else {}


def _matches_ntfybox_message(
    message: dict,
    *,
    event_name: str,
    source_service: str | None,
    source_instance: str | None,
    severity: str | None,
    attr_filters: list[tuple[str, str]],
) -> bool:
    if message.get("event_name") != event_name:
        return False
    if source_service is not None and message.get("source_service") != source_service:
        return False
    if source_instance is not None and message.get("source_instance") != source_instance:
        return False
    if severity is not None and message.get("severity") != severity:
        return False
    if not attr_filters:
        return True
    attributes = _payload_attributes(message)
    return all(attributes.get(key) == value for key, value in attr_filters)


def wait_for_ntfybox_messages(
    state: LabState,
    *,
    event_name: str,
    source_service: str | None = None,
    source_instance: str | None = None,
    severity: str | None = None,
    attr_filters: list[tuple[str, str]] | None = None,
    timeout_sec: float = 30.0,
    interval_sec: float = 1.0,
) -> list[dict]:
    deadline = time.monotonic() + timeout_sec
    filters = list(attr_filters or [])
    last_messages: list[dict] = []
    while time.monotonic() < deadline:
        last_messages = list_ntfybox_messages(state)
        matches = [
            message
            for message in last_messages
            if _matches_ntfybox_message(
                message,
                event_name=event_name,
                source_service=source_service,
                source_instance=source_instance,
                severity=severity,
                attr_filters=filters,
            )
        ]
        if matches:
            return matches
        time.sleep(interval_sec)
    raise NotificationWaitTimeout(last_messages)
