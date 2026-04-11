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
from lib.network_control import NetworkController
from lib.state import DesiredTargetState, LabState, LinkInfo, NamespaceInfo, ShapingInfo, ShapingTargetInfo, TopologyInfo


def target_config() -> ShapingInfo:
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
            "internet": NamespaceInfo(pid=116, parent="hub", role="internet"),
            "hub-up": NamespaceInfo(pid=117, parent="hub", role="hub-up"),
            "fbnotify": NamespaceInfo(pid=112, parent="hub", role="fbnotify"),
            "node-1": NamespaceInfo(pid=113, parent="hub", role="node"),
            "client-edge": NamespaceInfo(pid=114, parent="hub", role="client-edge"),
            "client-1": NamespaceInfo(pid=115, parent="client-edge", role="client"),
            "upstream-1": NamespaceInfo(pid=118, parent="hub-up", role="upstream"),
        },
        shaping=ShapingInfo(
            targets={
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
                "client-1": ShapingTargetInfo(
                    router_ns="client-edge",
                    tag="",
                    namespace="client-1",
                    device="cedge-c1",
                    kind="client",
                    peer_device="c1-peer",
                    shape_capable=False,
                    display_name="client-1",
                ),
            },
            desired={
                "fbnotify": DesiredTargetState(),
                "node-1": DesiredTargetState(),
                "client-1": DesiredTargetState(),
            },
        ),
        topology=TopologyInfo(
            base_cidr="10.99.0.0/24",
            links=[
                LinkInfo("hub", "fbnotify", "hub-fbnotify", "fbnotify-peer", "10.99.0.0/30", "10.99.0.1", "10.99.0.2"),
                LinkInfo("hub", "node-1", "hub-node1", "node1-peer", "10.99.0.4/30", "10.99.0.5", "10.99.0.6"),
                LinkInfo("hub", "internet", "hub-inet", "inet-hub", "10.99.0.12/30", "10.99.0.13", "10.99.0.14"),
                LinkInfo("internet", "hub-up", "inet-hubup", "hubup-inet", "10.99.0.16/30", "10.99.0.17", "10.99.0.18"),
                LinkInfo("hub-up", "upstream-1", "hubup-u1", "upstream1-peer", "10.99.0.20/30", "10.99.0.21", "10.99.0.22"),
                LinkInfo("internet", "client-edge", "inet-cedge", "cedge-inet", "10.99.0.28/30", "10.99.0.29", "10.99.0.30"),
                LinkInfo("client-edge", "client-1", "cedge-c1", "c1-peer", "10.99.0.32/30", "10.99.0.33", "10.99.0.34"),
            ],
        ),
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

    def test_network_controller_target_names_include_service_and_client_targets(self) -> None:
        controller = NetworkController(controller_state())

        self.assertEqual(["client-1", "fbnotify", "node-1"], controller.target_names())
        self.assertEqual(["node-1"], controller.target_names(shape_capable=True))

    def test_network_controller_disconnect_touches_router_and_peer_interfaces(self) -> None:
        controller = NetworkController(controller_state())
        live = {
            ("hub", "hub-fbnotify"): True,
            ("fbnotify", "fbnotify-peer"): True,
        }
        commands: list[tuple[int, list[str]]] = []

        def fake_run(pid: int, args: list[str]):
            commands.append((pid, args))
            if args[:4] == ["ip", "-o", "link", "show"]:
                key = ("hub", args[-1]) if pid == 111 else ("fbnotify", args[-1])
                connected = live.get(key, False)
                state = "UP" if connected else "DOWN"
                flags = "BROADCAST,MULTICAST,UP,LOWER_UP" if connected else "BROADCAST,MULTICAST"
                return subprocess.CompletedProcess(args=[], returncode=0, stdout=f"5: {args[-1]}: <{flags}> state {state}\n", stderr="")
            if args[:4] == ["ip", "link", "set", "dev"]:
                namespace = "hub" if pid == 111 else "fbnotify"
                live[(namespace, args[4])] = args[5] == "up"
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr="")
            raise AssertionError(f"unexpected args: {args!r}")

        with mock.patch("lib.network_control.netns.nsenter_run", side_effect=fake_run):
            status = controller.set_connected("fbnotify", False)

        self.assertFalse(status.connected)
        self.assertFalse(live[("hub", "hub-fbnotify")])
        self.assertFalse(live[("fbnotify", "fbnotify-peer")])
        self.assertIn((111, ["ip", "link", "set", "dev", "hub-fbnotify", "down"]), commands)
        self.assertIn((112, ["ip", "link", "set", "dev", "fbnotify-peer", "down"]), commands)

    def test_network_controller_repeated_disconnect_is_idempotent(self) -> None:
        controller = NetworkController(controller_state())
        live = {
            ("hub", "hub-fbnotify"): False,
            ("fbnotify", "fbnotify-peer"): False,
        }
        commands: list[list[str]] = []

        def fake_run(pid: int, args: list[str]):
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
            status = controller.set_connected("fbnotify", False)

        self.assertFalse(status.connected)
        self.assertEqual(4, len(commands))
        self.assertTrue(all(command[:4] == ["ip", "-o", "link", "show"] for command in commands))

    def test_network_controller_reconnect_replays_upstream_routes(self) -> None:
        state = controller_state()
        state.shaping.targets["upstream-1"] = ShapingTargetInfo(
            router_ns="hub-up",
            tag="us-1",
            namespace="upstream-1",
            device="hubup-u1",
            kind="upstream",
            peer_device="upstream1-peer",
            shape_capable=True,
            display_name="upstream-1",
        )
        state.shaping.desired["upstream-1"] = DesiredTargetState(connected=False)
        controller = NetworkController(state)
        live = {
            ("hub-up", "hubup-u1"): False,
            ("upstream-1", "upstream1-peer"): False,
        }
        commands: list[tuple[int, list[str]]] = []

        def fake_run(pid: int, args: list[str]):
            commands.append((pid, args))
            namespace = {117: "hub-up", 118: "upstream-1"}.get(pid)
            if args[:4] == ["ip", "-o", "link", "show"]:
                connected = live[(namespace, args[-1])]
                state_name = "UP" if connected else "DOWN"
                flags = "BROADCAST,MULTICAST,UP,LOWER_UP" if connected else "BROADCAST,MULTICAST"
                return subprocess.CompletedProcess(args=[], returncode=0, stdout=f"5: {args[-1]}: <{flags}> state {state_name}\n", stderr="")
            if args[:4] == ["ip", "link", "set", "dev"]:
                live[(namespace, args[4])] = args[5] == "up"
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr="")
            if args[:3] == ["ip", "route", "replace"]:
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr="")
            if args[:3] == ["tc", "qdisc", "del"]:
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr="")
            if args[:3] == ["tc", "qdisc", "show"]:
                return subprocess.CompletedProcess(args=[], returncode=0, stdout="qdisc noqueue 0: root refcnt 2\n", stderr="")
            raise AssertionError(f"unexpected args: {args!r}")

        with mock.patch("lib.network_control.netns.nsenter_run", side_effect=fake_run):
            status = controller.set_connected("upstream-1", True)

        self.assertTrue(status.connected)
        self.assertIn((118, ["ip", "route", "replace", "default", "via", "10.99.0.21", "dev", "upstream1-peer"]), commands)
        self.assertIn((111, ["ip", "route", "replace", "10.99.0.20/30", "via", "10.99.0.14", "dev", "hub-inet"]), commands)
        self.assertIn((116, ["ip", "route", "replace", "10.99.0.20/30", "via", "10.99.0.18", "dev", "inet-hubup"]), commands)


if __name__ == "__main__":
    unittest.main()
