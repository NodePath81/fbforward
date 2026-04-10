from __future__ import annotations

import argparse
from pathlib import Path

from lib.clients import run_locked_add_client, run_locked_remove_client
from lib.env import parse_client_specs
from lib.output import emit_status_result, exception_message, print_json
from lib.paths import DEFAULT_WORKDIR


def register_parser(subparsers) -> None:
    add_client_cmd = subparsers.add_parser("add-client", help="add one client namespace to a running Phase 5 lab")
    add_client_cmd.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    add_client_cmd.add_argument("--client", required=True, metavar="NAME=IP")
    add_client_cmd.add_argument("--json", action="store_true")
    add_client_cmd.set_defaults(handler=cmd_add_client)

    remove_client_cmd = subparsers.add_parser("remove-client", help="remove one client namespace from a running Phase 5 lab")
    remove_client_cmd.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    remove_client_cmd.add_argument("--name", required=True)
    remove_client_cmd.add_argument("--json", action="store_true")
    remove_client_cmd.set_defaults(handler=cmd_remove_client)


def cmd_add_client(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    try:
        client_specs = parse_client_specs([args.client])
        name, identity_ip = next(iter(client_specs.items()))
        updated = run_locked_add_client(workdir, name, identity_ip)
    except (RuntimeError, KeyError) as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise RuntimeError(exception_message(exc)) from exc
    emit_status_result(workdir, updated, json_output=args.json)
    return 0


def cmd_remove_client(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    try:
        updated = run_locked_remove_client(workdir, args.name)
    except (RuntimeError, KeyError) as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise RuntimeError(exception_message(exc)) from exc
    emit_status_result(workdir, updated, json_output=args.json)
    return 0
