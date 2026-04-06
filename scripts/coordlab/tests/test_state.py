from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.state import (
    LabState,
    LinkInfo,
    NamespaceInfo,
    ProcessInfo,
    ProxyInfo,
    ShapingInfo,
    ShapingTargetInfo,
    TokenInfo,
    TopologyInfo,
    load_state,
    save_state,
)


class StateRoundTripTest(unittest.TestCase):
    def test_save_and_load_round_trip(self) -> None:
        state = LabState(
            phase=1,
            active=True,
            created_at="2026-04-05T00:00:00+00:00",
            work_dir="/tmp/coordlab-test",
            namespaces={
                "hub": NamespaceInfo(pid=101, parent=None, role="hub"),
                "node-1": NamespaceInfo(pid=202, parent="hub", role="node"),
            },
            processes={
                "fbforward-node-1": ProcessInfo(pid=303, ns="node-1", log_path="/tmp/coordlab-test/logs/node-1.log", order=3),
            },
            proxies={
                "node-1": ProxyInfo(
                    listen_host="127.0.0.1",
                    host_port=18701,
                    target_ns="node-1",
                    target_host="127.0.0.1",
                    target_port=8080,
                ),
            },
            shaping=ShapingInfo(
                targets={
                    "node-1": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-1", device="hub-node1"),
                    "upstream-1": ShapingTargetInfo(
                        router_ns="hub-up",
                        tag="us-1",
                        namespace="upstream-1",
                        device="hubup-u1",
                    ),
                },
            ),
            tokens=TokenInfo(coord_token="coord-token", control_token="control-token"),
            topology=TopologyInfo(
                base_cidr="10.99.0.0/24",
                links=[
                    LinkInfo(
                        left_ns="hub",
                        right_ns="node-1",
                        left_if="hub-node1",
                        right_if="node1-peer",
                        subnet="10.99.0.4/30",
                        left_ip="10.99.0.5",
                        right_ip="10.99.0.6",
                    )
                ],
            ),
        )

        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "state.json"
            save_state(path, state)
            loaded = load_state(path)

        self.assertIsNotNone(loaded)
        assert loaded is not None
        self.assertEqual(state.phase, loaded.phase)
        self.assertEqual(state.active, loaded.active)
        self.assertEqual(state.work_dir, loaded.work_dir)
        self.assertEqual(state.namespaces["hub"].pid, loaded.namespaces["hub"].pid)
        self.assertEqual(state.processes["fbforward-node-1"].order, loaded.processes["fbforward-node-1"].order)
        self.assertEqual(state.proxies["node-1"].target_port, loaded.proxies["node-1"].target_port)
        self.assertEqual(state.shaping.targets["node-1"].router_ns, loaded.shaping.targets["node-1"].router_ns)
        self.assertEqual(state.shaping.targets["upstream-1"].device, loaded.shaping.targets["upstream-1"].device)
        self.assertEqual(state.tokens.coord_token, loaded.tokens.coord_token)
        self.assertEqual(state.topology.links[0].right_if, loaded.topology.links[0].right_if)


if __name__ == "__main__":
    unittest.main()
