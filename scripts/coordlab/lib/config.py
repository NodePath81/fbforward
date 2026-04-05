from __future__ import annotations

import shutil
import textwrap
from pathlib import Path
from secrets import token_hex

from . import netns
from .state import TokenInfo

FBCOORD_RUNTIME_DIR = "fbcoord-runtime"
FBCOORD_SOURCE_DIR = Path(__file__).resolve().parents[3] / "fbcoord"


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

    coord_ip = netns.find_link(topology.links, "hub", "fbcoord").right_ip
    us1_ip = netns.find_link(topology.links, "hub-up", "upstream-1").right_ip
    us2_ip = netns.find_link(topology.links, "hub-up", "upstream-2").right_ip

    rendered = textwrap.dedent(
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

        logging:
          level: info
          format: text
        """
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
