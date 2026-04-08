from __future__ import annotations

import shutil
import textwrap
from pathlib import Path
from secrets import token_hex

from . import netns
from .state import FirewallFeatureInfo, FirewallRuleInfo, GeoIPFeatureInfo, IPLogFeatureInfo, NodeFeatureInfo, TokenInfo

FBCOORD_RUNTIME_DIR = "fbcoord-runtime"
FBCOORD_SOURCE_DIR = Path(__file__).resolve().parents[3] / "fbcoord"
MMDB_DIRNAME = "mmdb"
DATA_DIRNAME = "data"
GEOIP_ASN_DB_FILENAME = "GeoLite2-ASN.mmdb"
GEOIP_COUNTRY_DB_FILENAME = "Country-without-asn.mmdb"
GEOIP_ASN_DB_URL = "https://raw.githubusercontent.com/Loyalsoldier/geoip/release/GeoLite2-ASN.mmdb"
GEOIP_COUNTRY_DB_URL = "https://raw.githubusercontent.com/Loyalsoldier/geoip/release/Country-without-asn.mmdb"
GEOIP_REFRESH_INTERVAL = "24h"
IP_LOG_RETENTION = "24h"
IP_LOG_GEO_QUEUE_SIZE = 128
IP_LOG_WRITE_QUEUE_SIZE = 128
IP_LOG_BATCH_SIZE = 10
IP_LOG_FLUSH_INTERVAL = "2s"
IP_LOG_PRUNE_INTERVAL = "1h"
FIREWALL_DEFAULT_POLICY = "allow"
FIREWALL_RULES = [
    FirewallRuleInfo(action="deny", cidr="198.51.100.0/24"),
    FirewallRuleInfo(action="deny", asn=15169),
    FirewallRuleInfo(action="deny", country="AU"),
]


def mmdb_dir_for(work_dir: str | Path) -> Path:
    return Path(work_dir) / MMDB_DIRNAME


def data_dir_for(work_dir: str | Path) -> Path:
    return Path(work_dir) / DATA_DIRNAME


def build_node_feature_info(node_name: str, work_dir: str | Path) -> NodeFeatureInfo:
    work_path = Path(work_dir)
    data_dir = data_dir_for(work_path)
    data_dir.mkdir(parents=True, exist_ok=True)
    mmdb_dir = mmdb_dir_for(work_path)
    return NodeFeatureInfo(
        geoip=GeoIPFeatureInfo(
            enabled=True,
            asn_db_url=GEOIP_ASN_DB_URL,
            asn_db_path=str(mmdb_dir / GEOIP_ASN_DB_FILENAME),
            country_db_url=GEOIP_COUNTRY_DB_URL,
            country_db_path=str(mmdb_dir / GEOIP_COUNTRY_DB_FILENAME),
            refresh_interval=GEOIP_REFRESH_INTERVAL,
        ),
        ip_log=IPLogFeatureInfo(
            enabled=True,
            db_path=str(data_dir / f"{node_name}-iplog.sqlite"),
            retention=IP_LOG_RETENTION,
            geo_queue_size=IP_LOG_GEO_QUEUE_SIZE,
            write_queue_size=IP_LOG_WRITE_QUEUE_SIZE,
            batch_size=IP_LOG_BATCH_SIZE,
            flush_interval=IP_LOG_FLUSH_INTERVAL,
            prune_interval=IP_LOG_PRUNE_INTERVAL,
        ),
        firewall=FirewallFeatureInfo(
            enabled=True,
            default_policy=FIREWALL_DEFAULT_POLICY,
            rules=[
                FirewallRuleInfo(action=rule.action, cidr=rule.cidr, asn=rule.asn, country=rule.country)
                for rule in FIREWALL_RULES
            ],
        ),
    )


def render_node_feature_config(features: NodeFeatureInfo) -> str:
    firewall_lines = ["firewall:", f"  enabled: {str(features.firewall.enabled).lower()}", f"  default: {features.firewall.default_policy}", "  rules:"]
    for rule in features.firewall.rules:
        firewall_lines.append(f"    - action: {rule.action}")
        if rule.cidr:
            firewall_lines.append(f"      cidr: {rule.cidr}")
        elif rule.asn is not None:
            firewall_lines.append(f"      asn: {rule.asn}")
        elif rule.country:
            firewall_lines.append(f"      country: {rule.country}")

    return "\n".join(
        [
            "geoip:",
            f"  enabled: {str(features.geoip.enabled).lower()}",
            f'  asn_db_url: "{features.geoip.asn_db_url}"',
            f'  asn_db_path: "{features.geoip.asn_db_path}"',
            f'  country_db_url: "{features.geoip.country_db_url}"',
            f'  country_db_path: "{features.geoip.country_db_path}"',
            f"  refresh_interval: {features.geoip.refresh_interval}",
            "",
            "ip_log:",
            f"  enabled: {str(features.ip_log.enabled).lower()}",
            f'  db_path: "{features.ip_log.db_path}"',
            f"  retention: {features.ip_log.retention}",
            f"  geo_queue_size: {features.ip_log.geo_queue_size}",
            f"  write_queue_size: {features.ip_log.write_queue_size}",
            f"  batch_size: {features.ip_log.batch_size}",
            f"  flush_interval: {features.ip_log.flush_interval}",
            f"  prune_interval: {features.ip_log.prune_interval}",
            "",
            *firewall_lines,
        ]
    )


def generate_tokens() -> TokenInfo:
    return TokenInfo(coord_token=token_hex(32), control_token=token_hex(32))


def generate_fbforward_config(
    node_name: str,
    topology: netns.Topology,
    tokens: TokenInfo,
    work_dir: str | Path,
) -> Path:
    work_path = Path(work_dir)
    configs_dir = work_path / "configs"
    configs_dir.mkdir(parents=True, exist_ok=True)
    features = build_node_feature_info(node_name, work_path)

    coord_ip = netns.find_link(topology.links, "hub", "fbcoord").right_ip
    us1_ip = netns.find_link(topology.links, "hub-up", "upstream-1").right_ip
    us2_ip = netns.find_link(topology.links, "hub-up", "upstream-2").right_ip

    rendered = (
        textwrap.dedent(
            f"""\
            hostname: {node_name}

            forwarding:
              listeners:
                - bind_addr: 0.0.0.0
                  bind_port: 9000
                  protocol: tcp

            upstreams:
              - tag: us-1
                destination:
                  host: {us1_ip}
                measurement:
                  host: {us1_ip}
                  port: 9876
              - tag: us-2
                destination:
                  host: {us2_ip}
                measurement:
                  host: {us2_ip}
                  port: 9876

            reachability:
              probe_interval: 1s
              window_size: 5
              startup_delay: 5s

            measurement:
              startup_delay: 5s
              stale_threshold: 30s
              schedule:
                interval:
                  min: 5s
                  max: 5s
                upstream_gap: 1s

            control:
              bind_addr: 127.0.0.1
              bind_port: 8080
              auth_token: "{tokens.control_token}"
              webui:
                enabled: true
              metrics:
                enabled: true

            coordination:
              endpoint: http://{coord_ip}:8787
              pool: lab
              node_id: {node_name}
              token: "{tokens.coord_token}"
              heartbeat_interval: 10s
            """
        )
        + "\n"
        + render_node_feature_config(features)
        + "\n\n"
        + textwrap.dedent(
            """\
            logging:
              level: info
              format: text
            """
        )
    )

    target = configs_dir / f"{node_name}.yaml"
    target.write_text(rendered, encoding="utf-8")
    return target


def prepare_fbcoord_runtime(work_dir: str | Path, coord_token: str) -> Path:
    work_path = Path(work_dir)
    runtime_dir = work_path / FBCOORD_RUNTIME_DIR
    if runtime_dir.exists():
        shutil.rmtree(runtime_dir)

    shutil.copytree(
        FBCOORD_SOURCE_DIR,
        runtime_dir,
        symlinks=True,
        ignore=shutil.ignore_patterns(".git", ".wrangler", "node_modules", "__pycache__"),
    )

    node_modules_src = FBCOORD_SOURCE_DIR / "node_modules"
    if node_modules_src.exists():
        (runtime_dir / "node_modules").symlink_to(node_modules_src)

    (runtime_dir / ".dev.vars").write_text(f"FBCOORD_TOKEN={coord_token}\n", encoding="utf-8")
    return runtime_dir
