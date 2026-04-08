from __future__ import annotations

import re
from dataclasses import dataclass
from subprocess import CompletedProcess

from . import netns
from .state import ShapingInfo

_FLAGS_RE = re.compile(r"<([^>]+)>")


@dataclass(slots=True)
class LinkState:
    target: str
    router_ns: str
    namespace: str
    device: str
    connected: bool


@dataclass(slots=True)
class LinkTarget:
    target: str
    router_ns: str
    namespace: str
    device: str


class LinkStateController:
    def __init__(self, router_pids: dict[str, int], config: ShapingInfo) -> None:
        self.router_pids = router_pids
        self.config = config

    def get(self, target_name: str) -> LinkState:
        target = self._target(target_name)
        result = self._run(target, ["ip", "-o", "link", "show", "dev", target.device])
        return LinkState(
            target=target.target,
            router_ns=target.router_ns,
            namespace=target.namespace,
            device=target.device,
            connected=parse_link_show(result.stdout),
        )

    def get_all(self) -> dict[str, LinkState]:
        return {
            target_name: self.get(target_name)
            for target_name in sorted(self.config.targets)
        }

    def set_connected(self, target_name: str, connected: bool) -> None:
        target = self._target(target_name)
        state = "up" if connected else "down"
        self._run(target, ["ip", "link", "set", "dev", target.device, state])

    def _target(self, target_name: str) -> LinkTarget:
        target = self.config.targets.get(target_name)
        if target is None:
            raise ValueError(f"unknown target {target_name!r}")
        if target.router_ns not in self.router_pids:
            raise RuntimeError(f"missing router pid for link-state namespace {target.router_ns!r}")
        return LinkTarget(
            target=target_name,
            router_ns=target.router_ns,
            namespace=target.namespace,
            device=target.device,
        )

    def _run(self, target: LinkTarget, args: list[str]) -> CompletedProcess[str]:
        return netns.nsenter_run(self.router_pids[target.router_ns], args)


def parse_link_show(output: str) -> bool:
    for line in output.splitlines():
        match = _FLAGS_RE.search(line)
        if match is None:
            continue
        flags = {flag.strip() for flag in match.group(1).split(",")}
        return "UP" in flags
    raise ValueError(f"unable to parse link state from output: {output!r}")
