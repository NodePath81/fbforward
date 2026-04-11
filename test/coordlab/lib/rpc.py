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


def set_upstream(base_url: str, token: str, mode: str, *, tag: str | None = None) -> dict:
    params = {"mode": mode}
    if tag is not None:
        params["tag"] = tag
    return rpc_call(base_url, token, "SetUpstream", params)


def set_mode_coordination(base_url: str, token: str) -> None:
    set_upstream(base_url, token, "coordination")


def fetch_metrics(base_url: str) -> str:
    response = httpx.get(
        f"{base_url.rstrip('/')}/metrics",
        timeout=5.0,
        follow_redirects=True,
    )
    if response.status_code != 200:
        raise RuntimeError(f"metrics fetch failed with status={response.status_code}: {response.text.strip()}")
    return response.text
