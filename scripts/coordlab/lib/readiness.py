from __future__ import annotations

import time
from typing import Callable

import httpx

from . import rpc

POLL_INTERVAL_SEC = 0.5
TIMEOUT_SEC = 30.0


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


def verify_fbcoord_api(base_url: str, coord_token: str, *, expected_pool: str) -> list[dict]:
    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        login = client.post(f"{base_url.rstrip('/')}/api/auth/login", json={"token": coord_token})
        if login.status_code != 200:
            raise RuntimeError(f"fbcoord login failed: status={login.status_code} body={login.text.strip()}")
        session_cookie = login.headers.get("set-cookie", "").split(";", 1)[0].strip()
        if not session_cookie:
            raise RuntimeError("fbcoord login did not return a session cookie")
        pools = client.get(
            f"{base_url.rstrip('/')}/api/pools",
            headers={"Cookie": session_cookie},
        )
        if pools.status_code != 200:
            raise RuntimeError(f"fbcoord pools fetch failed: status={pools.status_code} body={pools.text.strip()}")
        payload = pools.json()
        entries = payload.get("pools", [])
        if not any(pool.get("name") == expected_pool for pool in entries):
            raise RuntimeError(f"expected pool {expected_pool!r} not found in fbcoord pools response")
        return entries
