from __future__ import annotations

import argparse

from . import clients, exec_, infra, lifecycle, links, net, ntfybox, shaping


def register_subcommands(parser: argparse.ArgumentParser) -> None:
    subparsers = parser.add_subparsers(dest="command", required=True)
    for module in (net, lifecycle, clients, exec_, infra, shaping, links, ntfybox):
        module.register_parser(subparsers)
