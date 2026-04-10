from __future__ import annotations

import importlib.util
import ipaddress
import shutil
import sys
from pathlib import Path
from typing import Iterable

from . import netns
from .paths import REQUIREMENTS_FILE, VENV_PYTHON


def _bootstrap_lines() -> str:
    return (
        "bootstrap:\n"
        "  python3 -m venv .venv\n"
        "  .venv/bin/pip install -r test/coordlab/requirements.txt"
    )


def require_runtime_environment() -> None:
    actual = Path(sys.executable).resolve()
    if actual != VENV_PYTHON.resolve():
        raise RuntimeError(
            "coordlab must be run with the repo venv interpreter.\n"
            f"expected: {VENV_PYTHON}\n"
            f"actual:   {actual}\n"
            f"{_bootstrap_lines()}"
        )


def require_flask_environment() -> None:
    if importlib.util.find_spec("flask") is None:
        raise RuntimeError(
            "coordlab web requires flask in the repo venv.\n"
            f"{_bootstrap_lines()}"
        )
    if importlib.util.find_spec("httpx") is None:
        raise RuntimeError(
            "coordlab requires httpx in the repo venv.\n"
            f"{_bootstrap_lines()}"
        )


def require_tools(tools: Iterable[str]) -> None:
    missing = [tool for tool in tools if shutil.which(tool) is None]
    if missing:
        raise RuntimeError(f"missing required tools: {', '.join(missing)}")


def validate_client_spec(
    name: str,
    raw_ip: str,
    *,
    base_cidr: str = netns.DEFAULT_BASE_CIDR,
    existing_names: Iterable[str] = (),
    existing_ips: Iterable[str] = (),
) -> str:
    base_network = ipaddress.ip_network(base_cidr)
    if not name.startswith("client-"):
        raise ValueError(f"invalid client name {name!r}; expected prefix 'client-'")
    if name in set(existing_names):
        raise ValueError(f"duplicate client name: {name}")
    try:
        ip = ipaddress.ip_address(raw_ip)
    except ValueError as exc:
        raise ValueError(f"invalid client IP {raw_ip!r} for {name}") from exc
    if not isinstance(ip, ipaddress.IPv4Address):
        raise ValueError(f"client IP must be IPv4: {raw_ip}")
    if str(ip) in set(existing_ips):
        raise ValueError(f"duplicate client IP: {ip}")
    if ip in base_network:
        raise ValueError(f"client IP {ip} overlaps transport base CIDR {base_cidr}")
    return str(ip)


def parse_client_specs(raw_specs: list[str], *, base_cidr: str = netns.DEFAULT_BASE_CIDR) -> dict[str, str]:
    if not raw_specs:
        return {}
    parsed: dict[str, str] = {}
    seen_ips: set[str] = set()
    for raw in raw_specs:
        name, separator, raw_ip = raw.partition("=")
        if separator != "=" or not name or not raw_ip:
            raise RuntimeError(f"invalid client spec {raw!r}; expected NAME=IP")
        try:
            parsed[name] = validate_client_spec(
                name,
                raw_ip,
                base_cidr=base_cidr,
                existing_names=parsed,
                existing_ips=seen_ips,
            )
        except ValueError as exc:
            raise RuntimeError(str(exc)) from exc
        seen_ips.add(parsed[name])
    return parsed
