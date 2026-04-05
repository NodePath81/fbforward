from __future__ import annotations

import sys
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.output import render_summary
from lib.state import LabState, NamespaceInfo, ProcessInfo, ProxyInfo, TokenInfo, TopologyInfo


class OutputSummaryTest(unittest.TestCase):
    def test_render_summary_includes_proxy_urls_and_commands(self) -> None:
        state = LabState(
            phase=3,
            active=True,
            created_at="2026-04-05T00:00:00+00:00",
            work_dir="/tmp/coordlab-phase3",
            namespaces={"node-1": NamespaceInfo(pid=100, parent="hub", role="node")},
            processes={
                "coordlab-proxy": ProcessInfo(pid=300, ns="host", log_path="/tmp/proxy.log", order=5),
                "fbforward-node-1": ProcessInfo(pid=301, ns="node-1", log_path="/tmp/node1.log", order=3),
            },
            proxies={
                "fbcoord": ProxyInfo(
                    listen_host="127.0.0.1",
                    host_port=18700,
                    target_ns="fbcoord",
                    target_host="127.0.0.1",
                    target_port=8787,
                ),
                "node-1": ProxyInfo(
                    listen_host="127.0.0.1",
                    host_port=18701,
                    target_ns="node-1",
                    target_host="127.0.0.1",
                    target_port=8080,
                ),
            },
            tokens=TokenInfo(coord_token="coord-token", control_token="control-token"),
            topology=TopologyInfo(base_cidr="10.99.0.0/24"),
        )

        with mock.patch("lib.output.is_alive", return_value=True):
            summary = render_summary(state, "/repo/.venv/bin/python")

        self.assertIn("http://127.0.0.1:18700", summary)
        self.assertIn("http://127.0.0.1:18701", summary)
        self.assertIn("/repo/.venv/bin/python", summary)
        self.assertIn("coordlab-proxy: alive", summary)
        self.assertIn(" web --workdir ", summary)


if __name__ == "__main__":
    unittest.main()
