from __future__ import annotations

import argparse
from pathlib import Path

from lib.fbnotify import (
    NotificationWaitTimeout,
    clear_ntfybox_messages,
    list_ntfybox_messages,
    wait_for_ntfybox_messages,
)
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

    notify_wait = subparsers.add_parser("notify-wait", help="wait for a captured fbnotify message")
    notify_wait.add_argument("--event", required=True)
    notify_wait.add_argument("--source-service")
    notify_wait.add_argument("--source-instance")
    notify_wait.add_argument("--severity")
    notify_wait.add_argument("--attr", action="append", default=[], metavar="KEY=VALUE")
    notify_wait.add_argument("--timeout", type=float, default=30.0)
    notify_wait.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    notify_wait.add_argument("--json", action="store_true")
    notify_wait.set_defaults(handler=cmd_notify_wait)


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


def _parse_attr_filters(raw_filters: list[str]) -> list[tuple[str, str]]:
    parsed: list[tuple[str, str]] = []
    for raw in raw_filters:
        if "=" not in raw:
            raise RuntimeError(f"invalid attr filter {raw!r}; expected KEY=VALUE")
        key, value = raw.split("=", 1)
        if not key:
            raise RuntimeError(f"invalid attr filter {raw!r}; key must not be empty")
        parsed.append((key, value))
    return parsed


def cmd_notify_wait(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    try:
        state = load_active_state(workdir)
        attr_filters = _parse_attr_filters(args.attr)
        matches = wait_for_ntfybox_messages(
            state,
            event_name=args.event,
            source_service=args.source_service,
            source_instance=args.source_instance,
            severity=args.severity,
            attr_filters=attr_filters,
            timeout_sec=args.timeout,
        )
    except NotificationWaitTimeout as exc:
        payload = {"ok": False, "error": "timed out", "messages": exc.messages}
        if args.json:
            print_json(payload)
            return 1
        raise RuntimeError(f"timed out waiting for {args.event}; last inbox size={len(exc.messages)}") from exc
    except RuntimeError as exc:
        if args.json:
            print_json({"error": exception_message(exc)})
            return 1
        raise RuntimeError(exception_message(exc)) from exc

    payload = {"ok": True, "count": len(matches), "messages": matches}
    if args.json:
        print_json(payload)
        return 0
    print(f"matched notifications: {len(matches)}")
    for message in matches:
        event_name = message.get("event_name", "unknown")
        severity = message.get("severity", "unknown")
        source = f"{message.get('source_service', '?')}/{message.get('source_instance', '?')}"
        received_at = message.get("received_at", "?")
        print(f"- {severity} {event_name} {source} received_at={received_at}")
    return 0
