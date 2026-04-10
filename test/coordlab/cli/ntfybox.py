from __future__ import annotations

import argparse
from pathlib import Path

from lib.fbnotify import clear_ntfybox_messages, list_ntfybox_messages
from lib.lab import load_active_state
from lib.output import exception_message, print_json
from lib.paths import DEFAULT_WORKDIR


def register_parser(subparsers) -> None:
    ntfybox_list = subparsers.add_parser("ntfybox-list", help="list captured fbnotify messages")
    ntfybox_list.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    ntfybox_list.add_argument("--json", action="store_true")
    ntfybox_list.set_defaults(handler=cmd_ntfybox_list)

    ntfybox_clear = subparsers.add_parser("ntfybox-clear", help="clear captured fbnotify messages")
    ntfybox_clear.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    ntfybox_clear.add_argument("--json", action="store_true")
    ntfybox_clear.set_defaults(handler=cmd_ntfybox_clear)


def cmd_ntfybox_list(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    try:
        state = load_active_state(workdir)
        messages = list_ntfybox_messages(state)
    except RuntimeError as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise RuntimeError(exception_message(exc)) from exc

    payload = {
        "ok": True,
        "count": len(messages),
        "messages": messages,
    }
    if args.json:
        print_json(payload)
        return 0
    print(f"ntfybox messages: {len(messages)}")
    for message in messages:
        event_name = message.get("event_name", "unknown")
        severity = message.get("severity", "unknown")
        source = f"{message.get('source_service', '?')}/{message.get('source_instance', '?')}"
        received_at = message.get("received_at", "?")
        print(f"- {severity} {event_name} {source} received_at={received_at}")
    return 0


def cmd_ntfybox_clear(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    try:
        state = load_active_state(workdir)
        clear_ntfybox_messages(state)
    except RuntimeError as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise RuntimeError(exception_message(exc)) from exc

    payload = {"ok": True}
    if args.json:
        print_json(payload)
        return 0
    print("ntfybox cleared")
    return 0
