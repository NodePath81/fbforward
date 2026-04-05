#!/usr/bin/env python3
from __future__ import annotations

import argparse
import sys
from dataclasses import asdict
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable

from lib import netns
from lib.process import is_alive, terminate_pid
from lib.state import LabState, LinkInfo, NamespaceInfo, TopologyInfo, load_state, save_state

DEFAULT_WORKDIR = Path("/tmp/coordlab")
STATE_FILENAME = "state.json"


def state_path_for(workdir: Path) -> Path:
    return workdir / STATE_FILENAME


def build_phase1_state(workdir: Path, topology: netns.Topology) -> LabState:
    namespaces = {
        name: NamespaceInfo(pid=ns.pid, parent=ns.parent, role=ns.role)
        for name, ns in topology.namespaces.items()
    }
    links = [
        LinkInfo(
            left_ns=link.left_ns,
            right_ns=link.right_ns,
            left_if=link.left_if,
            right_if=link.right_if,
            subnet=link.subnet,
            left_ip=link.left_ip,
            right_ip=link.right_ip,
        )
        for link in topology.links
    ]
    return LabState(
        phase=1,
        active=True,
        created_at=datetime.now(timezone.utc).isoformat(),
        work_dir=str(workdir),
        namespaces=namespaces,
        topology=TopologyInfo(base_cidr=topology.base_cidr, links=links),
    )


def namespace_shutdown_order(namespaces: dict[str, NamespaceInfo]) -> list[tuple[str, NamespaceInfo]]:
    def depth(name: str) -> int:
        level = 0
        current = namespaces[name]
        while current.parent:
            level += 1
            current = namespaces[current.parent]
        return level

    return sorted(namespaces.items(), key=lambda item: (depth(item[0]), item[0]), reverse=True)


def print_status(state: LabState) -> None:
    print(f"coordlab phase={state.phase} active={state.active}")
    print(f"work_dir={state.work_dir}")
    print("namespaces:")
    for name, info in sorted(state.namespaces.items()):
        alive = is_alive(info.pid)
        parent = info.parent or "-"
        status = "alive" if alive else "dead"
        print(f"  {name}: pid={info.pid} parent={parent} role={info.role} status={status}")
    print("links:")
    for link in state.topology.links:
        print(
            f"  {link.left_ns}:{link.left_if} {link.left_ip} <-> "
            f"{link.right_ns}:{link.right_if} {link.right_ip} subnet={link.subnet}"
        )


def require_tools(tools: Iterable[str]) -> None:
    missing = [tool for tool in tools if netns.which(tool) is None]
    if missing:
        raise RuntimeError(f"missing required tools: {', '.join(missing)}")


def cmd_net_up(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    workdir.mkdir(parents=True, exist_ok=True)
    state_path = state_path_for(workdir)
    existing = load_state(state_path)
    if existing is not None:
        alive = [name for name, info in existing.namespaces.items() if is_alive(info.pid)]
        if alive:
            raise RuntimeError(
                f"existing Phase 1 state is still active in {workdir}: alive namespaces={', '.join(sorted(alive))}"
            )

    require_tools(["unshare", "nsenter", "ip", "sysctl", "ping"])

    topology = netns.build_topology(str(workdir))
    try:
        netns.verify_connectivity(topology)
    except Exception:
        netns.destroy_topology(topology)
        raise

    state = build_phase1_state(workdir, topology)
    save_state(state_path, state)
    print_status(state)
    return 0


def cmd_net_down(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        print(f"no Phase 1 state found at {state_path}")
        return 0

    for _, info in namespace_shutdown_order(state.namespaces):
        terminate_pid(info.pid, timeout_sec=5)

    state.active = False
    save_state(state_path, state)
    print(f"coordlab topology stopped for {workdir}")
    return 0


def cmd_net_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        print(f"no Phase 1 state found at {state_path}")
        return 1
    print_status(state)
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="coordlab.py")
    subparsers = parser.add_subparsers(dest="command", required=True)

    for name, handler, help_text in (
        ("net-up", cmd_net_up, "create the Phase 1 namespace topology"),
        ("net-down", cmd_net_down, "destroy the Phase 1 namespace topology"),
        ("net-status", cmd_net_status, "show the Phase 1 namespace topology state"),
    ):
        sub = subparsers.add_parser(name, help=help_text)
        sub.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
        sub.set_defaults(handler=handler)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.handler(args)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RuntimeError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1)
