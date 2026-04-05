from __future__ import annotations

import re
from dataclasses import dataclass
from subprocess import CompletedProcess

from . import netns
from .state import ShapingInfo, ShapingTargetInfo

_DELAY_RE = re.compile(r"delay\s+([0-9.]+)(us|ms|s)")
_LOSS_RE = re.compile(r"loss\s+([0-9.]+)%")
_MISSING_QDISC_ERRORS = (
    "Cannot delete qdisc with handle of zero",
    "No such file or directory",
    "No such qdisc",
)


@dataclass(slots=True)
class ShapingState:
    delay_ms: int
    loss_pct: float


@dataclass(slots=True)
class ShapingTarget:
    upstream: str
    tag: str
    namespace: str
    device: str


class TrafficShaper:
    def __init__(self, router_pid: int, config: ShapingInfo) -> None:
        self.router_pid = router_pid
        self.config = config

    def set(self, upstream: str, delay_ms: int = 0, loss_pct: float = 0) -> None:
        target = self._target(upstream)
        validate_shaping_values(delay_ms, loss_pct)
        if delay_ms == 0 and loss_pct == 0:
            self.clear(upstream)
            return

        args = ["tc", "qdisc", "replace", "dev", target.device, "root", "netem"]
        if delay_ms > 0:
            args.extend(["delay", f"{delay_ms}ms"])
        if loss_pct > 0:
            args.extend(["loss", f"{loss_pct:g}%"])
        self._run(args)

    def clear(self, upstream: str) -> None:
        target = self._target(upstream)
        try:
            self._run(["tc", "qdisc", "del", "dev", target.device, "root"])
        except RuntimeError as exc:
            if is_missing_qdisc_error(str(exc)):
                return
            raise

    def clear_all(self) -> None:
        for upstream in sorted(self.config.targets):
            self.clear(upstream)

    def get(self, upstream: str) -> ShapingState | None:
        target = self._target(upstream)
        result = self._run(["tc", "qdisc", "show", "dev", target.device])
        return parse_qdisc_show(result.stdout)

    def get_all(self) -> dict[str, ShapingState | None]:
        return {
            upstream: self.get(upstream)
            for upstream in sorted(self.config.targets)
        }

    def _target(self, upstream: str) -> ShapingTarget:
        target = self.config.targets.get(upstream)
        if target is None:
            raise ValueError(f"unknown upstream {upstream!r}")
        return ShapingTarget(
            upstream=upstream,
            tag=target.tag,
            namespace=target.namespace,
            device=target.device,
        )

    def _run(self, args: list[str]) -> CompletedProcess[str]:
        return netns.nsenter_run(self.router_pid, args)


def validate_shaping_values(delay_ms: int, loss_pct: float) -> None:
    if delay_ms < 0:
        raise ValueError("delay_ms must be >= 0")
    if loss_pct < 0 or loss_pct > 100:
        raise ValueError("loss_pct must be between 0 and 100")


def is_missing_qdisc_error(message: str) -> bool:
    return any(fragment in message for fragment in _MISSING_QDISC_ERRORS)


def parse_qdisc_show(output: str) -> ShapingState | None:
    for line in output.splitlines():
        if "qdisc netem" not in line:
            continue
        delay_ms = 0
        loss_pct = 0.0
        delay_match = _DELAY_RE.search(line)
        if delay_match is not None:
            delay_ms = _duration_to_ms(delay_match.group(1), delay_match.group(2))
        loss_match = _LOSS_RE.search(line)
        if loss_match is not None:
            loss_pct = float(loss_match.group(1))
        if delay_ms == 0 and loss_pct == 0:
            return None
        return ShapingState(delay_ms=delay_ms, loss_pct=loss_pct)
    return None


def _duration_to_ms(value: str, unit: str) -> int:
    amount = float(value)
    if unit == "s":
        amount *= 1000
    elif unit == "us":
        amount /= 1000
    return int(round(amount))
