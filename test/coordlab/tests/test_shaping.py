from __future__ import annotations

import subprocess
import sys
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.network_control import NetworkController
from lib.shaping import ShapingState, TrafficShaper, parse_qdisc_show
from lib.state import DesiredTargetState, LabState, LinkInfo, NamespaceInfo, ShapingInfo, ShapingTargetInfo, TopologyInfo


def shaping_config() -> ShapingInfo:
    return ShapingInfo(
        targets={
            "node-1": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-1", device="hub-node1"),
            "node-2": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-2", device="hub-node2"),
            "upstream-1": ShapingTargetInfo(router_ns="hub-up", tag="us-1", namespace="upstream-1", device="hubup-u1"),
            "upstream-2": ShapingTargetInfo(router_ns="hub-up", tag="us-2", namespace="upstream-2", device="hubup-u2"),
        },
    )


def controller_state() -> LabState:
    return LabState(
        phase=5,
        active=True,
        created_at="2026-04-10T00:00:00+00:00",
        work_dir="/tmp/coordlab-test",
        namespaces={
            "hub": NamespaceInfo(pid=111, parent=None, role="hub"),
            "internet": NamespaceInfo(pid=114, parent="hub", role="internet"),
            "hub-up": NamespaceInfo(pid=115, parent="hub", role="hub-up"),
            "node-1": NamespaceInfo(pid=112, parent="hub", role="node"),
            "fbnotify": NamespaceInfo(pid=113, parent="hub", role="fbnotify"),
        },
        shaping=ShapingInfo(
            targets={
                "node-1": ShapingTargetInfo(
                    router_ns="hub",
                    tag="",
                    namespace="node-1",
                    device="hub-node1",
                    kind="node",
                    peer_device="node1-peer",
                    shape_capable=True,
                    display_name="node-1",
                ),
                "fbnotify": ShapingTargetInfo(
                    router_ns="hub",
                    tag="",
                    namespace="fbnotify",
                    device="hub-fbnotify",
                    kind="service",
                    peer_device="fbnotify-peer",
                    shape_capable=False,
                    display_name="fbnotify",
                ),
            },
            desired={
                "node-1": DesiredTargetState(),
                "fbnotify": DesiredTargetState(),
            },
        ),
        topology=TopologyInfo(
            base_cidr="10.99.0.0/24",
            links=[
                LinkInfo("hub", "node-1", "hub-node1", "node1-peer", "10.99.0.4/30", "10.99.0.5", "10.99.0.6"),
                LinkInfo("hub", "fbnotify", "hub-fbnotify", "fbnotify-peer", "10.99.0.0/30", "10.99.0.1", "10.99.0.2"),
                LinkInfo("hub", "internet", "hub-inet", "inet-hub", "10.99.0.12/30", "10.99.0.13", "10.99.0.14"),
                LinkInfo("internet", "hub-up", "inet-hubup", "hubup-inet", "10.99.0.16/30", "10.99.0.17", "10.99.0.18"),
            ],
        ),
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

    def test_network_controller_set_shaping_on_disconnected_target_updates_desired_only(self) -> None:
        controller = NetworkController(controller_state())
        commands: list[list[str]] = []

        def fake_run(_pid: int, args: list[str]):
            commands.append(args)
            if args[:4] == ["ip", "-o", "link", "show"]:
                return subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=f"5: {args[-1]}: <BROADCAST,MULTICAST> state DOWN\n",
                    stderr="",
                )
            raise AssertionError(f"unexpected args: {args!r}")

        with mock.patch("lib.network_control.netns.nsenter_run", side_effect=fake_run):
            status = controller.set_shaping("node-1", delay_ms=120, loss_pct=3.5)

        self.assertEqual(120, status.delay_ms)
        self.assertEqual(3.5, status.loss_pct)
        self.assertFalse(status.connected)
        self.assertEqual(120, controller.state.shaping.desired["node-1"].delay_ms)
        self.assertEqual(3.5, controller.state.shaping.desired["node-1"].loss_pct)
        self.assertFalse(any(command[:3] == ["tc", "qdisc", "replace"] for command in commands))

    def test_network_controller_reconnect_reapplies_desired_shaping(self) -> None:
        controller = controller_state()
        controller.shaping.desired["node-1"] = DesiredTargetState(connected=False, delay_ms=150, loss_pct=7.5)
        live = {
            ("node-1", "node1-peer"): False,
            ("hub", "hub-node1"): False,
        }
        commands: list[list[str]] = []

        def fake_run(pid: int, args: list[str]):
            commands.append(args)
            namespace = "hub" if pid == 111 else "node-1"
            if args[:4] == ["ip", "-o", "link", "show"]:
                connected = live[(namespace, args[-1])]
                state = "UP" if connected else "DOWN"
                flags = "BROADCAST,MULTICAST,UP,LOWER_UP" if connected else "BROADCAST,MULTICAST"
                return subprocess.CompletedProcess(args=[], returncode=0, stdout=f"5: {args[-1]}: <{flags}> state {state}\n", stderr="")
            if args[:4] == ["ip", "link", "set", "dev"]:
                live[(namespace, args[4])] = args[5] == "up"
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr="")
            if args[:3] == ["ip", "route", "replace"]:
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr="")
            if args[:3] == ["tc", "qdisc", "replace"]:
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr="")
            if args[:3] == ["tc", "qdisc", "show"]:
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="qdisc noqueue 0: root refcnt 2\n", stderr="")
            raise AssertionError(f"unexpected args: {args!r}")

        with mock.patch("lib.network_control.netns.nsenter_run", side_effect=fake_run):
            status = NetworkController(controller).set_connected("node-1", True)

        self.assertTrue(status.connected)
        self.assertIn(["ip", "link", "set", "dev", "node1-peer", "up"], commands)
        self.assertIn(["ip", "link", "set", "dev", "hub-node1", "up"], commands)
        self.assertIn(["ip", "route", "replace", "default", "via", "10.99.0.5", "dev", "node1-peer"], commands)
        self.assertIn(["ip", "route", "replace", "10.99.0.4/30", "via", "10.99.0.17", "dev", "hubup-inet"], commands)
        self.assertIn(["ip", "route", "replace", "10.99.0.4/30", "via", "10.99.0.13", "dev", "inet-hub"], commands)
        self.assertIn(
            ["tc", "qdisc", "replace", "dev", "hub-node1", "root", "netem", "delay", "150ms", "loss", "7.5%"],
            commands,
        )

    def test_network_controller_rejects_non_shape_capable_target(self) -> None:
        controller = NetworkController(controller_state())

        with self.assertRaisesRegex(ValueError, "does not support shaping"):
            controller.set_shaping("fbnotify", delay_ms=10)


if __name__ == "__main__":
    unittest.main()
