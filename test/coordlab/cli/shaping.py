from __future__ import annotations

import argparse
from pathlib import Path

from lib.lab import build_shaper_from_state, format_shaping_state, load_active_state
from lib.paths import DEFAULT_WORKDIR

TARGET_CHOICES = ["node-1", "node-2", "upstream-1", "upstream-2"]


def register_parser(subparsers) -> None:
    shaping_status = subparsers.add_parser(
        "shaping-status",
        help="show current node-side and upstream-side shaping state",
    )
    shaping_status.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_status.set_defaults(handler=cmd_shaping_status)

    shaping_set = subparsers.add_parser("shaping-set", help="apply delay/loss shaping to a node or upstream target")
    shaping_set.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_set.add_argument("--target", required=True, choices=TARGET_CHOICES)
    shaping_set.add_argument("--delay-ms", type=int, default=0)
    shaping_set.add_argument("--loss-pct", type=float, default=0.0)
    shaping_set.set_defaults(handler=cmd_shaping_set)

    shaping_clear = subparsers.add_parser("shaping-clear", help="clear shaping on one node or upstream target")
    shaping_clear.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    shaping_clear.add_argument("--target", required=True, choices=TARGET_CHOICES)
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
    shaper = build_shaper_from_state(state)
    print(format_shaping_state(shaper.get_all()))
    return 0


def cmd_shaping_set(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    shaper = build_shaper_from_state(state)
    shaper.set(args.target, delay_ms=args.delay_ms, loss_pct=args.loss_pct)
    print(format_shaping_state(shaper.get_all()))
    return 0


def cmd_shaping_clear(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    shaper = build_shaper_from_state(state)
    shaper.clear(args.target)
    print(format_shaping_state(shaper.get_all()))
    return 0


def cmd_shaping_clear_all(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state = load_active_state(workdir)
    shaper = build_shaper_from_state(state)
    shaper.clear_all()
    print(format_shaping_state(shaper.get_all()))
    return 0
