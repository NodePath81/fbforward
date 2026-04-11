from __future__ import annotations

from dataclasses import dataclass

from . import netns
from .lab import load_active_state, save_current_state
from .linkstate import parse_link_show
from .locking import acquire_network_mutation_lock
from .shaping import ShapingState, is_missing_qdisc_error, parse_qdisc_show, validate_shaping_values
from .state import DesiredTargetState, LabState, ShapingTargetInfo


@dataclass(slots=True)
class LinkStatus:
    target: str
    display_name: str
    kind: str
    namespace: str
    router_ns: str
    device: str
    peer_device: str
    shape_capable: bool
    connected: bool


@dataclass(slots=True)
class ShapingStatus:
    target: str
    display_name: str
    kind: str
    namespace: str
    router_ns: str
    device: str
    delay_ms: int
    loss_pct: float
    connected: bool


def _default_desired() -> DesiredTargetState:
    return DesiredTargetState()


class NetworkController:
    def __init__(self, state: LabState) -> None:
        self.state = state

    def target_names(self, *, shape_capable: bool | None = None) -> list[str]:
        names = sorted(self.state.shaping.targets)
        if shape_capable is None:
            return names
        return [name for name in names if self._target(name).shape_capable == shape_capable]

    def get_link(self, target_name: str) -> LinkStatus:
        target = self._target(target_name)
        router_connected = self._interface_connected(target.router_ns, target.device)
        peer_connected = self._interface_connected(target.namespace, target.peer_device)
        return LinkStatus(
            target=target_name,
            display_name=target.display_name or target_name,
            kind=target.kind,
            namespace=target.namespace,
            router_ns=target.router_ns,
            device=target.device,
            peer_device=target.peer_device,
            shape_capable=target.shape_capable,
            connected=router_connected and peer_connected,
        )

    def get_links(self) -> dict[str, LinkStatus]:
        return {
            target_name: self.get_link(target_name)
            for target_name in self.target_names()
        }

    def get_live_link_state(self, target_name: str) -> LinkStatus:
        return self.get_link(target_name)

    def get_shaping(self, target_name: str) -> ShapingStatus:
        target = self._target(target_name)
        if not target.shape_capable:
            raise ValueError(f"target {target_name!r} does not support shaping")
        desired = self._desired(target_name)
        return ShapingStatus(
            target=target_name,
            display_name=target.display_name or target_name,
            kind=target.kind,
            namespace=target.namespace,
            router_ns=target.router_ns,
            device=target.device,
            delay_ms=desired.delay_ms,
            loss_pct=desired.loss_pct,
            connected=self.get_link(target_name).connected,
        )

    def get_shaping_all(self) -> dict[str, ShapingStatus]:
        return {
            target_name: self.get_shaping(target_name)
            for target_name in self.target_names(shape_capable=True)
        }

    def set_connected(self, target_name: str, connected: bool) -> LinkStatus:
        target = self._target(target_name)
        desired = self._desired(target_name)
        desired.connected = connected
        if connected:
            self._set_interface_connected(target.namespace, target.peer_device, True)
            self._set_interface_connected(target.router_ns, target.device, True)
            self.reconcile(target_name)
        else:
            self._set_interface_connected(target.router_ns, target.device, False)
            self._set_interface_connected(target.namespace, target.peer_device, False)
        return self.get_link(target_name)

    def set_shaping(self, target_name: str, delay_ms: int = 0, loss_pct: float = 0.0) -> ShapingStatus:
        target = self._target(target_name)
        if not target.shape_capable:
            raise ValueError(f"target {target_name!r} does not support shaping")
        validate_shaping_values(delay_ms, loss_pct)
        desired = self._desired(target_name)
        desired.delay_ms = delay_ms
        desired.loss_pct = loss_pct
        if self.get_link(target_name).connected:
            self._reconcile_shaping(target_name)
        return self.get_shaping(target_name)

    def clear_shaping(self, target_name: str) -> ShapingStatus:
        return self.set_shaping(target_name, 0, 0.0)

    def clear_all_shaping(self) -> dict[str, ShapingStatus]:
        for target_name in self.target_names(shape_capable=True):
            self.clear_shaping(target_name)
        return self.get_shaping_all()

    def reconcile(self, target_name: str) -> None:
        target = self._target(target_name)
        if not self.get_link(target_name).connected:
            return
        if target.shape_capable:
            self._reconcile_shaping(target_name)

    def _reconcile_shaping(self, target_name: str) -> None:
        target = self._target(target_name)
        desired = self._desired(target_name)
        if desired.delay_ms <= 0 and desired.loss_pct <= 0:
            self._clear_live_qdisc(target.router_ns, target.device)
            return
        args = ["tc", "qdisc", "replace", "dev", target.device, "root", "netem"]
        if desired.delay_ms > 0:
            args.extend(["delay", f"{desired.delay_ms}ms"])
        if desired.loss_pct > 0:
            args.extend(["loss", f"{desired.loss_pct:g}%"])
        self._run(target.router_ns, args)

    def _clear_live_qdisc(self, namespace_name: str, device: str) -> None:
        try:
            self._run(namespace_name, ["tc", "qdisc", "del", "dev", device, "root"])
        except RuntimeError as exc:
            if is_missing_qdisc_error(str(exc)):
                return
            raise

    def get_live_qdisc(self, target_name: str) -> ShapingState | None:
        target = self._target(target_name)
        if not target.shape_capable:
            raise ValueError(f"target {target_name!r} does not support shaping")
        result = self._run(target.router_ns, ["tc", "qdisc", "show", "dev", target.device])
        return parse_qdisc_show(result.stdout)

    def _desired(self, target_name: str) -> DesiredTargetState:
        desired = self.state.shaping.desired.get(target_name)
        if desired is None:
            desired = _default_desired()
            self.state.shaping.desired[target_name] = desired
        return desired

    def _target(self, target_name: str) -> ShapingTargetInfo:
        target = self.state.shaping.targets.get(target_name)
        if target is None:
            raise ValueError(f"unknown target {target_name!r}")
        return target

    def _interface_connected(self, namespace_name: str, device: str) -> bool:
        result = self._run(namespace_name, ["ip", "-o", "link", "show", "dev", device])
        return parse_link_show(result.stdout)

    def _set_interface_connected(self, namespace_name: str, device: str, connected: bool) -> None:
        if self._interface_connected(namespace_name, device) == connected:
            return
        self._run(namespace_name, ["ip", "link", "set", "dev", device, "up" if connected else "down"])

    def _run(self, namespace_name: str, args: list[str]):
        info = self.state.namespaces.get(namespace_name)
        if info is None:
            raise RuntimeError(f"missing namespace metadata for {namespace_name!r}")
        return netns.nsenter_run(info.pid, args)


def build_network_controller_from_state(state: LabState) -> NetworkController:
    return NetworkController(state)


def run_locked_set_connected(workdir, target_name: str, connected: bool) -> LabState:
    with acquire_network_mutation_lock(workdir):
        state = load_active_state(workdir)
        controller = build_network_controller_from_state(state)
        controller.set_connected(target_name, connected)
        save_current_state(state)
        return state


def run_locked_set_shaping(workdir, target_name: str, delay_ms: int, loss_pct: float) -> LabState:
    with acquire_network_mutation_lock(workdir):
        state = load_active_state(workdir)
        controller = build_network_controller_from_state(state)
        controller.set_shaping(target_name, delay_ms, loss_pct)
        save_current_state(state)
        return state


def run_locked_clear_shaping(workdir, target_name: str) -> LabState:
    with acquire_network_mutation_lock(workdir):
        state = load_active_state(workdir)
        controller = build_network_controller_from_state(state)
        controller.clear_shaping(target_name)
        save_current_state(state)
        return state


def run_locked_clear_all_shaping(workdir) -> LabState:
    with acquire_network_mutation_lock(workdir):
        state = load_active_state(workdir)
        controller = build_network_controller_from_state(state)
        controller.clear_all_shaping()
        save_current_state(state)
        return state
