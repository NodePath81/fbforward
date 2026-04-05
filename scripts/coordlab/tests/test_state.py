from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.state import LabState, LinkInfo, NamespaceInfo, TopologyInfo, load_state, save_state


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
        self.assertEqual(state.topology.links[0].right_if, loaded.topology.links[0].right_if)


if __name__ == "__main__":
    unittest.main()
