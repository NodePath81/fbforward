from __future__ import annotations

import argparse
import json
from pathlib import Path

from lib import rpc
from lib.lab import load_active_state
from lib.output import exception_message, print_json
from lib.paths import DEFAULT_WORKDIR

NODE_NAMES = ("node-1", "node-2")


def register_parser(subparsers) -> None:
    node_cmd = subparsers.add_parser("node", help="node control and status commands")
    node_subparsers = node_cmd.add_subparsers(dest="node_command", required=True)

    rpc_cmd = node_subparsers.add_parser("rpc", help="call a node RPC method")
    rpc_cmd.add_argument("node", choices=NODE_NAMES)
    rpc_cmd.add_argument("method")
    rpc_cmd.add_argument("params_json", nargs="?")
    rpc_cmd.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    rpc_cmd.add_argument("--json", action="store_true")
    rpc_cmd.set_defaults(handler=cmd_node_rpc)

    switch_cmd = node_subparsers.add_parser("switch", help="switch the node upstream mode")
    switch_cmd.add_argument("node", choices=NODE_NAMES)
    switch_cmd.add_argument("mode", choices=("auto", "coordination", "manual"))
    switch_cmd.add_argument("tag", nargs="?")
    switch_cmd.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    switch_cmd.add_argument("--json", action="store_true")
    switch_cmd.set_defaults(handler=cmd_node_switch)

    status_cmd = node_subparsers.add_parser("status", help="fetch raw node Prometheus metrics")
    status_cmd.add_argument("node", choices=NODE_NAMES)
    status_cmd.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    status_cmd.add_argument("--json", action="store_true")
    status_cmd.set_defaults(handler=cmd_node_status)


def _node_base_url(workdir: Path, node: str) -> tuple[str, str]:
    state = load_active_state(workdir)
    proxy = state.proxies.get(node)
    if proxy is None:
        raise RuntimeError(f"coordlab state does not expose a proxy for {node}")
    base_url = f"http://{proxy.listen_host}:{proxy.host_port}"
    return base_url, state.tokens.control_token


def _parse_params(raw: str | None) -> dict:
    if raw is None:
        return {}
    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"invalid params_json: {exc.msg}") from exc
    if not isinstance(parsed, dict):
        raise RuntimeError("params_json must decode to a JSON object")
    return parsed


def cmd_node_rpc(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    try:
        base_url, token = _node_base_url(workdir, args.node)
        payload = rpc.rpc_call(base_url, token, args.method, _parse_params(args.params_json))
    except RuntimeError as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise RuntimeError(exception_message(exc)) from exc

    print_json(payload)
    return 0


def cmd_node_switch(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    tag = args.tag
    if args.mode == "manual" and not tag:
        message = "manual mode requires a tag"
        if args.json:
            print_json({"error": message})
            return 1
        raise RuntimeError(message)
    if args.mode != "manual" and tag is not None:
        message = f"{args.mode} mode does not accept a tag"
        if args.json:
            print_json({"error": message})
            return 1
        raise RuntimeError(message)

    try:
        base_url, token = _node_base_url(workdir, args.node)
        payload = rpc.set_upstream(base_url, token, args.mode, tag=tag)
    except RuntimeError as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise RuntimeError(exception_message(exc)) from exc

    print_json(payload)
    return 0


def cmd_node_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    try:
        base_url, _ = _node_base_url(workdir, args.node)
        metrics = rpc.fetch_metrics(base_url)
    except RuntimeError as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise RuntimeError(exception_message(exc)) from exc

    if args.json:
        print_json({"node": args.node, "metrics": metrics})
        return 0
    print(metrics, end="" if metrics.endswith("\n") else "\n")
    return 0
