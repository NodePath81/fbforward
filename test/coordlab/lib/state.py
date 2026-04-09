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
    order: int = 0


@dataclass(slots=True)
class ProxyInfo:
    listen_host: str
    host_port: int
    target_ns: str
    target_host: str
    target_port: int


@dataclass(slots=True)
class TerminalInfo:
    host_port: int
    pid: int


@dataclass(slots=True)
class ClientInfo:
    identity_ip: str


@dataclass(slots=True)
class GeoIPFeatureInfo:
    enabled: bool = False
    asn_db_url: str = ""
    asn_db_path: str = ""
    country_db_url: str = ""
    country_db_path: str = ""
    refresh_interval: str = ""


@dataclass(slots=True)
class IPLogFeatureInfo:
    enabled: bool = False
    db_path: str = ""
    retention: str = ""
    geo_queue_size: int = 0
    write_queue_size: int = 0
    batch_size: int = 0
    flush_interval: str = ""
    prune_interval: str = ""


@dataclass(slots=True)
class FirewallRuleInfo:
    action: str
    cidr: str | None = None
    asn: int | None = None
    country: str | None = None


@dataclass(slots=True)
class FirewallFeatureInfo:
    enabled: bool = False
    default_policy: str = ""
    rules: list[FirewallRuleInfo] = field(default_factory=list)


@dataclass(slots=True)
class NodeFeatureInfo:
    geoip: GeoIPFeatureInfo = field(default_factory=GeoIPFeatureInfo)
    ip_log: IPLogFeatureInfo = field(default_factory=IPLogFeatureInfo)
    firewall: FirewallFeatureInfo = field(default_factory=FirewallFeatureInfo)


@dataclass(slots=True)
class ShapingTargetInfo:
    router_ns: str
    tag: str
    namespace: str
    device: str


@dataclass(slots=True)
class ShapingInfo:
    targets: dict[str, ShapingTargetInfo] = field(default_factory=dict)


@dataclass(slots=True)
class TokenInfo:
    control_token: str = ""
    operator_token: str = ""
    node_tokens: dict[str, str] = field(default_factory=dict)


@dataclass(slots=True)
class TopologyInfo:
    base_cidr: str
    links: list[LinkInfo] = field(default_factory=list)
    next_subnet_index: int = 0


@dataclass(slots=True)
class LabState:
    phase: int
    active: bool
    created_at: str
    work_dir: str
    namespaces: dict[str, NamespaceInfo] = field(default_factory=dict)
    processes: dict[str, ProcessInfo] = field(default_factory=dict)
    proxies: dict[str, ProxyInfo] = field(default_factory=dict)
    clients: dict[str, ClientInfo] = field(default_factory=dict)
    terminals: dict[str, TerminalInfo] = field(default_factory=dict)
    node_features: dict[str, NodeFeatureInfo] = field(default_factory=dict)
    shaping: ShapingInfo = field(default_factory=ShapingInfo)
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
        clients = {
            name: ClientInfo(**info)
            for name, info in data.get("clients", {}).items()
        }
        terminals = {
            name: TerminalInfo(**info)
            for name, info in data.get("terminals", {}).items()
        }
        node_features = {
            name: NodeFeatureInfo(
                geoip=GeoIPFeatureInfo(**info.get("geoip", {})),
                ip_log=IPLogFeatureInfo(**info.get("ip_log", {})),
                firewall=FirewallFeatureInfo(
                    enabled=bool(info.get("firewall", {}).get("enabled", False)),
                    default_policy=str(info.get("firewall", {}).get("default_policy", "")),
                    rules=[
                        FirewallRuleInfo(**rule)
                        for rule in info.get("firewall", {}).get("rules", [])
                    ],
                ),
            )
            for name, info in data.get("node_features", {}).items()
        }
        shaping_raw = data.get("shaping", {})
        shaping = ShapingInfo(
            targets={
                name: ShapingTargetInfo(**info)
                for name, info in shaping_raw.get("targets", {}).items()
            },
        )
        tokens = TokenInfo(**data.get("tokens", {}))
        topology_raw = data.get("topology", {})
        topology = TopologyInfo(
            base_cidr=topology_raw.get("base_cidr", ""),
            links=[LinkInfo(**info) for info in topology_raw.get("links", [])],
            next_subnet_index=int(topology_raw.get("next_subnet_index", len(topology_raw.get("links", [])))),
        )
        return cls(
            phase=int(data.get("phase", 1)),
            active=bool(data.get("active", False)),
            created_at=str(data.get("created_at", "")),
            work_dir=str(data.get("work_dir", "")),
            namespaces=namespaces,
            processes=processes,
            proxies=proxies,
            clients=clients,
            terminals=terminals,
            node_features=node_features,
            shaping=shaping,
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
