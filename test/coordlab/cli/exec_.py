from __future__ import annotations

import argparse
import subprocess
from pathlib import Path

from lib import netns
from lib.lab import load_active_state
from lib.output import exception_message, print_json, strip_remainder_command
from lib.paths import DEFAULT_WORKDIR


def register_parser(subparsers) -> None:
    exec_cmd = subparsers.add_parser("exec", help="run a command inside one saved namespace")
    exec_cmd.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    exec_cmd.add_argument("--ns", required=True)
    exec_cmd.add_argument("--json", action="store_true")
    exec_cmd.add_argument("command", nargs=argparse.REMAINDER)
    exec_cmd.set_defaults(handler=cmd_exec)


def cmd_exec(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    command = strip_remainder_command(list(args.command))
    if not command:
        message = "exec requires a command after --"
        if args.json:
            print_json({"error": message})
            return 1
        raise RuntimeError(message)

    try:
        state = load_active_state(workdir)
        namespace = state.namespaces.get(args.ns)
        if namespace is None:
            raise RuntimeError(f"unknown namespace: {args.ns}")
        nsenter_command = netns.nsenter_command(namespace.pid, command)
    except RuntimeError as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise

    try:
        if args.json:
            result = subprocess.run(nsenter_command, capture_output=True, text=True)
            print_json(
                {
                    "namespace": args.ns,
                    "pid": namespace.pid,
                    "command": command,
                    "exit_code": result.returncode,
                    "stdout": result.stdout,
                    "stderr": result.stderr,
                }
            )
            return result.returncode
        result = subprocess.run(nsenter_command)
        return int(result.returncode)
    except OSError as exc:
        if args.json:
            print_json({"error": str(exc), "namespace": args.ns, "pid": namespace.pid, "command": command})
            return 1
        raise RuntimeError(str(exc)) from exc
