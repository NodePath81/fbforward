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
    kind: str = ""
    peer_device: str = ""
    shape_capable: bool = True
    display_name: str = ""


@dataclass(slots=True)
class DesiredTargetState:
    connected: bool = True
    delay_ms: int = 0
    loss_pct: float = 0.0


@dataclass(slots=True)
class ShapingInfo:
    targets: dict[str, ShapingTargetInfo] = field(default_factory=dict)
    desired: dict[str, DesiredTargetState] = field(default_factory=dict)


@dataclass(slots=True)
class TokenInfo:
    control_token: str = ""
    operator_token: str = ""
    node_tokens: dict[str, str] = field(default_factory=dict)


@dataclass(slots=True)
class FBNotifyEmitterInfo:
    key_id: str = ""
    token: str = ""
    source_service: str = ""
    source_instance: str = ""


@dataclass(slots=True)
class FBCoordNotifyConfigInfo:
    verified: bool = False
    configured: bool = False
    source: str = "none"
    endpoint: str = ""
    key_id: str = ""
    source_instance: str = ""
    masked_prefix: str = ""
    updated_at: int | None = None
    error: str = ""


@dataclass(slots=True)
class FBNotifyInfo:
    available: bool = False
    error: str = ""
    public_url: str = ""
    internal_base_url: str = ""
    internal_ingest_url: str = ""
    operator_token: str = ""
    emitters: dict[str, FBNotifyEmitterInfo] = field(default_factory=dict)
    fbcoord_notify: FBCoordNotifyConfigInfo = field(default_factory=FBCoordNotifyConfigInfo)


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
    fbnotify: FBNotifyInfo = field(default_factory=FBNotifyInfo)
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
            desired={
                name: DesiredTargetState(**info)
                for name, info in shaping_raw.get("desired", {}).items()
            },
        )
        tokens = TokenInfo(**data.get("tokens", {}))
        fbnotify_raw = data.get("fbnotify", {})
        fbnotify = FBNotifyInfo(
            available=bool(fbnotify_raw.get("available", False)),
            error=str(fbnotify_raw.get("error", "")),
            public_url=str(fbnotify_raw.get("public_url", "")),
            internal_base_url=str(fbnotify_raw.get("internal_base_url", "")),
            internal_ingest_url=str(fbnotify_raw.get("internal_ingest_url", "")),
            operator_token=str(fbnotify_raw.get("operator_token", "")),
            emitters={
                name: FBNotifyEmitterInfo(**info)
                for name, info in fbnotify_raw.get("emitters", {}).items()
            },
            fbcoord_notify=FBCoordNotifyConfigInfo(**fbnotify_raw.get("fbcoord_notify", {})),
        )
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
            fbnotify=fbnotify,
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
