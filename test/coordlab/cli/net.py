from __future__ import annotations

import argparse
from pathlib import Path

from lib import netns
from lib.env import parse_client_specs, require_tools
from lib.lab import build_state, existing_lab_is_alive, namespace_shutdown_order
from lib.output import print_basic_status, print_json, status_payload
from lib.paths import DEFAULT_WORKDIR, state_path_for
from lib.process import terminate_pid
from lib.state import load_state, save_state


def register_parser(subparsers) -> None:
    for name, handler, help_text in (
        ("net-up", cmd_net_up, "create the Phase 1 namespace topology"),
        ("net-down", cmd_net_down, "destroy the Phase 1 namespace topology"),
        ("net-status", cmd_net_status, "show the Phase 1 namespace topology state"),
    ):
        sub = subparsers.add_parser(name, help=help_text)
        sub.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
        if name == "net-up":
            sub.add_argument("--client", action="append", default=[], metavar="NAME=IP")
            sub.add_argument("--skip-connectivity-check", action="store_true")
        if name == "net-status":
            sub.add_argument("--json", action="store_true")
        sub.set_defaults(handler=handler)


def cmd_net_up(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    skip_connectivity_check = bool(getattr(args, "skip_connectivity_check", False))
    workdir.mkdir(parents=True, exist_ok=True)
    state_path = state_path_for(workdir)
    existing = load_state(state_path)
    if existing is not None:
        alive = existing_lab_is_alive(existing)
        if alive:
            raise RuntimeError(
                f"existing coordlab state is still active in {workdir}: alive entries={', '.join(alive)}"
            )

    require_tools(["unshare", "nsenter", "ip", "sysctl", "ping"])
    client_specs = parse_client_specs(args.client)

    topology = netns.build_topology(str(workdir), client_specs=client_specs)
    try:
        if not skip_connectivity_check:
            netns.verify_connectivity(topology)
    except Exception:
        netns.destroy_topology(topology)
        raise

    state = build_state(workdir, topology, phase=1, active=True)
    save_state(state_path, state)
    if skip_connectivity_check:
        print("coordlab note: skipping connectivity preflight")
    print_basic_status(state)
    return 0


def cmd_net_down(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        print(f"no coordlab state found at {state_path}")
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
        if args.json:
            print_json(status_payload(None, workdir))
            return 1
        print(f"no coordlab state found at {state_path}")
        return 1
    if args.json:
        print_json(status_payload(state, workdir))
        return 0
    print_basic_status(state)
    return 0
