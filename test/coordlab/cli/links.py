from __future__ import annotations

import argparse
from pathlib import Path

from lib.lab import format_link_state, load_active_state
from lib.network_control import build_network_controller_from_state, run_locked_set_connected
from lib.paths import DEFAULT_WORKDIR


def register_parser(subparsers) -> None:
    link_status = subparsers.add_parser("link-status", help="show current live link state for all targets")
    link_status.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    link_status.set_defaults(handler=cmd_link_status)

    disconnect = subparsers.add_parser("disconnect", help="disconnect one controllable target")
    disconnect.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    disconnect.add_argument("--target", required=True)
    disconnect.set_defaults(handler=cmd_disconnect)

    reconnect = subparsers.add_parser("reconnect", help="reconnect one controllable target")
    reconnect.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    reconnect.add_argument("--target", required=True)
    reconnect.set_defaults(handler=cmd_reconnect)


def cmd_link_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    controller = build_network_controller_from_state(state)
    print(format_link_state(controller.get_links()))
    return 0


def cmd_disconnect(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = run_locked_set_connected(workdir, args.target, False)
    controller = build_network_controller_from_state(state)
    print(format_link_state(controller.get_links()))
    return 0


def cmd_reconnect(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = run_locked_set_connected(workdir, args.target, True)
    controller = build_network_controller_from_state(state)
    print(format_link_state(controller.get_links()))
    return 0
