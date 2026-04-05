from __future__ import annotations

import json
from dataclasses import asdict, dataclass, field
from pathlib import Path


@dataclass(slots=True)
class NamespaceInfo:
    pid: int
    parent: str | None
    role: str


@dataclass(slots=True)
class LinkInfo:
    left_ns: str
    right_ns: str
    left_if: str
    right_if: str
    subnet: str
    left_ip: str
    right_ip: str


@dataclass(slots=True)
class ProcessInfo:
    pid: int
    ns: str
    log_path: str


@dataclass(slots=True)
class ProxyInfo:
    host_port: int
    target_ip: str
    target_port: int


@dataclass(slots=True)
class TerminalInfo:
    host_port: int
    pid: int


@dataclass(slots=True)
class TokenInfo:
    coord_token: str = ""
    control_token: str = ""


@dataclass(slots=True)
class TopologyInfo:
    base_cidr: str
    links: list[LinkInfo] = field(default_factory=list)


@dataclass(slots=True)
class LabState:
    phase: int
    active: bool
    created_at: str
    work_dir: str
    namespaces: dict[str, NamespaceInfo] = field(default_factory=dict)
    processes: dict[str, ProcessInfo] = field(default_factory=dict)
    proxies: dict[str, ProxyInfo] = field(default_factory=dict)
    terminals: dict[str, TerminalInfo] = field(default_factory=dict)
    tokens: TokenInfo = field(default_factory=TokenInfo)
    topology: TopologyInfo = field(default_factory=lambda: TopologyInfo(base_cidr=""))

    def to_dict(self) -> dict:
        return asdict(self)

    @classmethod
    def from_dict(cls, data: dict) -> "LabState":
        namespaces = {
            name: NamespaceInfo(**info)
            for name, info in data.get("namespaces", {}).items()
        }
        processes = {
            name: ProcessInfo(**info)
            for name, info in data.get("processes", {}).items()
        }
        proxies = {
            name: ProxyInfo(**info)
            for name, info in data.get("proxies", {}).items()
        }
        terminals = {
            name: TerminalInfo(**info)
            for name, info in data.get("terminals", {}).items()
        }
        tokens = TokenInfo(**data.get("tokens", {}))
        topology_raw = data.get("topology", {})
        topology = TopologyInfo(
            base_cidr=topology_raw.get("base_cidr", ""),
            links=[LinkInfo(**info) for info in topology_raw.get("links", [])],
        )
        return cls(
            phase=int(data.get("phase", 1)),
            active=bool(data.get("active", False)),
            created_at=str(data.get("created_at", "")),
            work_dir=str(data.get("work_dir", "")),
            namespaces=namespaces,
            processes=processes,
            proxies=proxies,
            terminals=terminals,
            tokens=tokens,
            topology=topology,
        )


def save_state(path: str | Path, state: LabState) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(state.to_dict(), indent=2, sort_keys=True) + "\n", encoding="utf-8")


def load_state(path: str | Path) -> LabState | None:
    target = Path(path)
    if not target.exists():
        return None
    return LabState.from_dict(json.loads(target.read_text(encoding="utf-8")))
