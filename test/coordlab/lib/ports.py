from __future__ import annotations

import socket
from typing import Iterable

from .state import ProxyInfo

PROXY_PROCESS_NAME = "coordlab-proxy"
TTYD_BASE_PORT = 18900
PROXY_SPECS = {
    "fbcoord": ("127.0.0.1", 18700, "fbcoord", "127.0.0.1", 8787),
    "node-1": ("127.0.0.1", 18701, "node-1", "127.0.0.1", 8080),
    "node-2": ("127.0.0.1", 18702, "node-2", "127.0.0.1", 8080),
    "fbnotify": ("127.0.0.1", 18703, "fbnotify", "127.0.0.1", 8787),
}


def fixed_proxy_bindings() -> list[tuple[str, str, int]]:
    return [
        (name, listen_host, host_port)
        for name, (listen_host, host_port, _, _, _) in PROXY_SPECS.items()
    ]


def assert_bindings_available(
    bindings: Iterable[tuple[str, str, int]],
    *,
    error_prefix: str = "coordlab host ports are already in use",
) -> None:
    busy: list[str] = []
    for name, listen_host, host_port in bindings:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                sock.bind((listen_host, host_port))
            except OSError:
                busy.append(f"{name}:{listen_host}:{host_port}")
    if busy:
        raise RuntimeError(f"{error_prefix}: {', '.join(busy)}")


def assert_host_ports_available(extra_bindings: Iterable[tuple[str, str, int]] | None = None) -> None:
    assert_bindings_available(
        [*fixed_proxy_bindings(), *list(extra_bindings or ())],
        error_prefix="coordlab proxy ports are already in use",
    )


def build_proxy_infos(*, include_fbnotify: bool) -> dict[str, ProxyInfo]:
    return {
        name: ProxyInfo(
            listen_host=listen_host,
            host_port=host_port,
            target_ns=target_ns,
            target_host=target_host,
            target_port=target_port,
        )
        for name, (listen_host, host_port, target_ns, target_host, target_port) in PROXY_SPECS.items()
        if include_fbnotify or name != "fbnotify"
    }
