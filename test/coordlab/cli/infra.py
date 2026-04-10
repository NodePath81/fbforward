from __future__ import annotations

import argparse
from pathlib import Path

from lib.env import require_flask_environment
from lib.paths import DEFAULT_WORKDIR
from lib.proxy import run_proxy_daemon


def register_parser(subparsers) -> None:
    web = subparsers.add_parser("web", help="start the Phase 5 coordlab dashboard")
    web.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
    web.add_argument("--host", default="127.0.0.1")
    web.add_argument("--port", type=int, default=18800)
    web.set_defaults(handler=cmd_web)

    hidden = subparsers.add_parser("proxy-daemon", help=argparse.SUPPRESS)
    hidden.add_argument("--state", required=True)
    hidden.set_defaults(handler=cmd_proxy_daemon)


def cmd_web(args: argparse.Namespace) -> int:
    require_flask_environment()
    from web.app import create_app

    workdir = Path(args.workdir).expanduser().resolve()
    app = create_app(workdir)
    app.run(host=args.host, port=args.port, debug=False, use_reloader=False)
    return 0


def cmd_proxy_daemon(args: argparse.Namespace) -> int:
    run_proxy_daemon(args.state)
    return 0
