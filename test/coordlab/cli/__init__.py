from __future__ import annotations

import argparse

from . import clients, exec_, infra, lifecycle, links, net, node, ntfybox, shaping


def register_subcommands(parser: argparse.ArgumentParser) -> None:
    subparsers = parser.add_subparsers(dest="command", required=True)
    for module in (net, lifecycle, clients, exec_, infra, shaping, links, node, ntfybox):
        module.register_parser(subparsers)
