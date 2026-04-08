from __future__ import annotations

import subprocess
import sys
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.linkstate import LinkStateController, parse_link_show
from lib.state import ShapingInfo, ShapingTargetInfo


def target_config() -> ShapingInfo:
    return ShapingInfo(
        targets={
            "node-1": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-1", device="hub-node1"),
            "node-2": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-2", device="hub-node2"),
            "upstream-1": ShapingTargetInfo(router_ns="hub-up", tag="us-1", namespace="upstream-1", device="hubup-u1"),
            "upstream-2": ShapingTargetInfo(router_ns="hub-up", tag="us-2", namespace="upstream-2", device="hubup-u2"),
        },
    )


class LinkStateControllerTest(unittest.TestCase):
    def test_disconnect_node_target_uses_hub_router_pid(self) -> None:
        controller = LinkStateController({"hub": 111, "hub-up": 222}, target_config())
        with mock.patch("lib.linkstate.netns.nsenter_run") as run:
            controller.set_connected("node-1", False)

        run.assert_called_once_with(111, ["ip", "link", "set", "dev", "hub-node1", "down"])

    def test_reconnect_upstream_target_uses_hub_up_router_pid(self) -> None:
        controller = LinkStateController({"hub": 111, "hub-up": 222}, target_config())
        with mock.patch("lib.linkstate.netns.nsenter_run") as run:
            controller.set_connected("upstream-2", True)

        run.assert_called_once_with(222, ["ip", "link", "set", "dev", "hubup-u2", "up"])

    def test_invalid_target_raises(self) -> None:
        controller = LinkStateController({"hub": 111, "hub-up": 222}, target_config())
        with self.assertRaisesRegex(ValueError, "unknown target"):
            controller.get("missing")

    def test_missing_router_pid_raises(self) -> None:
        controller = LinkStateController({"hub": 111}, target_config())
        with self.assertRaisesRegex(RuntimeError, "missing router pid"):
            controller.get("upstream-1")

    def test_parse_link_show_detects_connected(self) -> None:
        output = "5: hub-node1@if4: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP mode DEFAULT"
        self.assertTrue(parse_link_show(output))

    def test_parse_link_show_detects_disconnected(self) -> None:
        output = "5: hub-node1@if4: <BROADCAST,MULTICAST> mtu 1500 qdisc noqueue state DOWN mode DEFAULT"
        self.assertFalse(parse_link_show(output))

    def test_get_all_returns_all_targets(self) -> None:
        controller = LinkStateController({"hub": 111, "hub-up": 222}, target_config())
        outputs = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout="5: hub-node1@if4: <BROADCAST,MULTICAST,UP,LOWER_UP>\n", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="6: hub-node2@if5: <BROADCAST,MULTICAST>\n", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="7: hubup-u1@if6: <BROADCAST,MULTICAST,UP,LOWER_UP>\n", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="8: hubup-u2@if7: <BROADCAST,MULTICAST>\n", stderr=""),
        ]
        with mock.patch("lib.linkstate.netns.nsenter_run", side_effect=outputs):
            state = controller.get_all()

        self.assertTrue(state["node-1"].connected)
        self.assertFalse(state["node-2"].connected)
        self.assertTrue(state["upstream-1"].connected)
        self.assertFalse(state["upstream-2"].connected)


if __name__ == "__main__":
    unittest.main()
