#!/usr/bin/env python3
from __future__ import annotations

import argparse
import sys

from cli import register_subcommands
from lib.env import require_runtime_environment


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="coordlab.py")
    register_subcommands(parser)
    return parser


def main(argv: list[str] | None = None) -> int:
    require_runtime_environment()
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.handler(args)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RuntimeError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1)
