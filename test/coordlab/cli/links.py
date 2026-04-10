from __future__ import annotations

import argparse
from pathlib import Path

from lib.lab import build_link_state_controller_from_state, format_link_state, load_active_state
from lib.paths import DEFAULT_WORKDIR

TARGET_CHOICES = ["node-1", "node-2", "upstream-1", "upstream-2"]


def register_parser(subparsers) -> None:
    link_status = subparsers.add_parser("link-status", help="show current live link state for all targets")
    link_status.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    link_status.set_defaults(handler=cmd_link_status)

    disconnect = subparsers.add_parser("disconnect", help="disconnect one node or upstream target")
    disconnect.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    disconnect.add_argument("--target", required=True, choices=TARGET_CHOICES)
    disconnect.set_defaults(handler=cmd_disconnect)

    reconnect = subparsers.add_parser("reconnect", help="reconnect one node or upstream target")
    reconnect.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    reconnect.add_argument("--target", required=True, choices=TARGET_CHOICES)
    reconnect.set_defaults(handler=cmd_reconnect)


def cmd_link_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    controller = build_link_state_controller_from_state(state)
    print(format_link_state(controller.get_all()))
    return 0


def cmd_disconnect(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    controller = build_link_state_controller_from_state(state)
    controller.set_connected(args.target, False)
    print(format_link_state(controller.get_all()))
    return 0


def cmd_reconnect(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    controller = build_link_state_controller_from_state(state)
    controller.set_connected(args.target, True)
    print(format_link_state(controller.get_all()))
    return 0
