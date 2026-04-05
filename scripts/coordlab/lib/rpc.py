from __future__ import annotations

import httpx


def rpc_call(base_url: str, token: str, method: str, params: dict | None = None) -> dict:
    response = httpx.post(
        f"{base_url.rstrip('/')}/rpc",
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
        },
        json={
            "method": method,
            "params": params or {},
        },
        timeout=5.0,
    )
    if response.status_code != 200:
        raise RuntimeError(f"{method} failed with status={response.status_code}: {response.text.strip()}")
    payload = response.json()
    if not payload.get("ok"):
        raise RuntimeError(f"{method} returned ok=false: {payload}")
    return payload


def get_status(base_url: str, token: str) -> dict:
    payload = rpc_call(base_url, token, "GetStatus", {})
    result = payload.get("result")
    if not isinstance(result, dict):
        raise RuntimeError(f"GetStatus returned invalid result: {payload}")
    return result


def set_mode_coordination(base_url: str, token: str) -> None:
    rpc_call(base_url, token, "SetUpstream", {"mode": "coordination"})
