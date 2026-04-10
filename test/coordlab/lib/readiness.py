from __future__ import annotations

import time
from typing import Callable

import httpx

from . import rpc

POLL_INTERVAL_SEC = 0.5
TIMEOUT_SEC = 30.0
READINESS_TIMEOUT_SEC = TIMEOUT_SEC


def wait_http_ok(url: str, *, timeout_sec: float = TIMEOUT_SEC, interval_sec: float = POLL_INTERVAL_SEC) -> httpx.Response:
    deadline = time.monotonic() + timeout_sec
    last_error: Exception | None = None
    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        while time.monotonic() < deadline:
            try:
                response = client.get(url)
                if response.status_code == 200:
                    return response
                last_error = RuntimeError(f"unexpected status {response.status_code} for {url}")
            except httpx.HTTPError as exc:
                last_error = exc
            time.sleep(interval_sec)
    raise RuntimeError(f"{url} did not become ready within {timeout_sec}s: {last_error}")


def wait_for_status(
    base_url: str,
    token: str,
    *,
    predicate: Callable[[dict], bool],
    timeout_sec: float = TIMEOUT_SEC,
    interval_sec: float = POLL_INTERVAL_SEC,
) -> dict:
    deadline = time.monotonic() + timeout_sec
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            status = rpc.get_status(base_url, token)
            if predicate(status):
                return status
            last_error = RuntimeError(f"predicate not satisfied for {base_url}")
        except Exception as exc:
            last_error = exc
        time.sleep(interval_sec)
    raise RuntimeError(f"{base_url} did not reach expected status within {timeout_sec}s: {last_error}")


def verify_fbcoord_api(base_url: str, operator_token: str, *, expected_node_ids: tuple[str, ...] | list[str]) -> dict:
    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        login = client.post(f"{base_url.rstrip('/')}/api/auth/login", json={"token": operator_token})
        if login.status_code != 200:
            raise RuntimeError(f"fbcoord login failed: status={login.status_code} body={login.text.strip()}")
        session_cookie = login.headers.get("set-cookie", "").split(";", 1)[0].strip()
        if not session_cookie:
            raise RuntimeError("fbcoord login did not return a session cookie")
        state = client.get(
            f"{base_url.rstrip('/')}/api/state",
            headers={"Cookie": session_cookie},
        )
        if state.status_code != 200:
            raise RuntimeError(f"fbcoord state fetch failed: status={state.status_code} body={state.text.strip()}")
        payload = state.json()
        nodes = payload.get("nodes", [])
        observed_node_ids = {
            node.get("node_id")
            for node in nodes
            if isinstance(node, dict)
        }
        missing = [node_id for node_id in expected_node_ids if node_id not in observed_node_ids]
        if missing:
            raise RuntimeError(f"expected node_ids {missing!r} not found in fbcoord state response")
        return payload


def verify_fbnotify_api(base_url: str, operator_token: str) -> dict:
    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        login = client.post(f"{base_url.rstrip('/')}/api/auth/login", json={"token": operator_token})
        if login.status_code != 200:
            raise RuntimeError(f"fbnotify login failed: status={login.status_code} body={login.text.strip()}")
        session_cookie = login.headers.get("set-cookie", "").split(";", 1)[0].strip()
        if not session_cookie:
            raise RuntimeError("fbnotify login did not return a session cookie")
        capture = client.get(
            f"{base_url.rstrip('/')}/api/capture/messages",
            headers={"Cookie": session_cookie},
        )
        if capture.status_code != 200:
            raise RuntimeError(f"fbnotify capture fetch failed: status={capture.status_code} body={capture.text.strip()}")
        return capture.json()


def wait_for_condition(timeout_sec: float, poll_fn, failure_message: str, *, interval_sec: float = POLL_INTERVAL_SEC) -> None:
    deadline = time.monotonic() + timeout_sec
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            if poll_fn():
                return
        except Exception as exc:
            last_error = exc
        time.sleep(interval_sec)
    if last_error is not None:
        raise RuntimeError(f"{failure_message}: {last_error}") from last_error
    raise RuntimeError(failure_message)
