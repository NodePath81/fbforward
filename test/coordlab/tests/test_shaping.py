from __future__ import annotations

import subprocess
import sys
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.shaping import ShapingState, TrafficShaper, parse_qdisc_show
from lib.state import ShapingInfo, ShapingTargetInfo


def shaping_config() -> ShapingInfo:
    return ShapingInfo(
        targets={
            "node-1": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-1", device="hub-node1"),
            "node-2": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-2", device="hub-node2"),
            "upstream-1": ShapingTargetInfo(router_ns="hub-up", tag="us-1", namespace="upstream-1", device="hubup-u1"),
            "upstream-2": ShapingTargetInfo(router_ns="hub-up", tag="us-2", namespace="upstream-2", device="hubup-u2"),
        },
    )


class TrafficShaperTest(unittest.TestCase):
    def test_node_target_uses_hub_router_pid(self) -> None:
        shaper = TrafficShaper({"hub": 111, "hub-up": 222}, shaping_config())
        with mock.patch("lib.shaping.netns.nsenter_run") as run:
            shaper.set("node-1", delay_ms=200)

        run.assert_called_once_with(
            111,
            ["tc", "qdisc", "replace", "dev", "hub-node1", "root", "netem", "delay", "200ms"],
        )

    def test_upstream_target_uses_hub_up_router_pid(self) -> None:
        shaper = TrafficShaper({"hub": 111, "hub-up": 222}, shaping_config())
        with mock.patch("lib.shaping.netns.nsenter_run") as run:
            shaper.set("upstream-2", loss_pct=30.0)

        run.assert_called_once_with(
            222,
            ["tc", "qdisc", "replace", "dev", "hubup-u2", "root", "netem", "loss", "30%"],
        )

    def test_set_delay_and_loss_uses_combined_replace(self) -> None:
        shaper = TrafficShaper({"hub": 111, "hub-up": 222}, shaping_config())
        with mock.patch("lib.shaping.netns.nsenter_run") as run:
            shaper.set("upstream-1", delay_ms=150, loss_pct=7.5)

        run.assert_called_once_with(
            222,
            ["tc", "qdisc", "replace", "dev", "hubup-u1", "root", "netem", "delay", "150ms", "loss", "7.5%"],
        )

    def test_clear_missing_qdisc_is_noop(self) -> None:
        shaper = TrafficShaper({"hub": 111, "hub-up": 222}, shaping_config())
        with mock.patch("lib.shaping.netns.nsenter_run", side_effect=RuntimeError("Cannot delete qdisc with handle of zero")):
            shaper.clear("node-2")

    def test_invalid_values_raise(self) -> None:
        shaper = TrafficShaper({"hub": 111, "hub-up": 222}, shaping_config())
        with self.assertRaisesRegex(ValueError, "unknown target"):
            shaper.set("missing", delay_ms=1)
        with self.assertRaisesRegex(ValueError, "delay_ms must be >= 0"):
            shaper.set("node-1", delay_ms=-1)
        with self.assertRaisesRegex(ValueError, "loss_pct must be between 0 and 100"):
            shaper.set("upstream-1", loss_pct=120.0)

    def test_parse_qdisc_show_returns_shaping_state(self) -> None:
        parsed = parse_qdisc_show("qdisc netem 8001: root refcnt 2 limit 1000 delay 200.0ms loss 30%")
        self.assertEqual(ShapingState(delay_ms=200, loss_pct=30.0), parsed)

    def test_parse_qdisc_show_returns_none_without_netem(self) -> None:
        self.assertIsNone(parse_qdisc_show("qdisc noqueue 0: root refcnt 2"))

    def test_get_all_returns_all_targets(self) -> None:
        shaper = TrafficShaper({"hub": 111, "hub-up": 222}, shaping_config())
        outputs = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout="qdisc netem 8001: root delay 125.0ms\n", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="qdisc noqueue 0: root refcnt 2\n", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="qdisc netem 8001: root loss 10%\n", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="qdisc noqueue 0: root refcnt 2\n", stderr=""),
        ]
        with mock.patch("lib.shaping.netns.nsenter_run", side_effect=outputs):
            state = shaper.get_all()

        self.assertEqual(125, state["node-1"].delay_ms)
        self.assertIsNone(state["node-2"])
        self.assertEqual(10.0, state["upstream-1"].loss_pct)
        self.assertIsNone(state["upstream-2"])


if __name__ == "__main__":
    unittest.main()
