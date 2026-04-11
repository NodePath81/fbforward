from __future__ import annotations

import argparse
from pathlib import Path

from lib.lab import format_shaping_state, load_active_state
from lib.network_control import (
    build_network_controller_from_state,
    run_locked_clear_all_shaping,
    run_locked_clear_shaping,
    run_locked_set_shaping,
)
from lib.paths import DEFAULT_WORKDIR


def register_parser(subparsers) -> None:
    shaping_status = subparsers.add_parser(
        "shaping-status",
        help="show current shaping state for all shape-capable targets",
    )
    shaping_status.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_status.set_defaults(handler=cmd_shaping_status)

    shaping_set = subparsers.add_parser("shaping-set", help="apply delay/loss shaping to a shape-capable target")
    shaping_set.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_set.add_argument("--target", required=True)
    shaping_set.add_argument("--delay-ms", type=int, default=0)
    shaping_set.add_argument("--loss-pct", type=float, default=0.0)
    shaping_set.set_defaults(handler=cmd_shaping_set)

    shaping_clear = subparsers.add_parser("shaping-clear", help="clear shaping on one shape-capable target")
    shaping_clear.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_clear.add_argument("--target", required=True)
    shaping_clear.set_defaults(handler=cmd_shaping_clear)

    shaping_clear_all = subparsers.add_parser(
        "shaping-clear-all",
        help="clear shaping on all node and upstream targets",
    )
    shaping_clear_all.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_clear_all.set_defaults(handler=cmd_shaping_clear_all)


def cmd_shaping_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    controller = build_network_controller_from_state(state)
    print(format_shaping_state(controller.get_shaping_all()))
    return 0


def cmd_shaping_set(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = run_locked_set_shaping(workdir, args.target, args.delay_ms, args.loss_pct)
    controller = build_network_controller_from_state(state)
    print(format_shaping_state(controller.get_shaping_all()))
    return 0


def cmd_shaping_clear(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = run_locked_clear_shaping(workdir, args.target)
    controller = build_network_controller_from_state(state)
    print(format_shaping_state(controller.get_shaping_all()))
    return 0


def cmd_shaping_clear_all(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = run_locked_clear_all_shaping(workdir)
    controller = build_network_controller_from_state(state)
    print(format_shaping_state(controller.get_shaping_all()))
    return 0
