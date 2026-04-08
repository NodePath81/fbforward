from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.state import (
    ClientInfo,
    LabState,
    NamespaceInfo,
    ProcessInfo,
    ProxyInfo,
    ShapingInfo,
    ShapingTargetInfo,
    TerminalInfo,
    TokenInfo,
    TopologyInfo,
    save_state,
)
from web.app import create_app


class FakeShaper:
    def __init__(self):
        self.calls: list[tuple] = []
        self.states = {
            "node-1": {"delay_ms": 0, "loss_pct": 0.0},
            "node-2": {"delay_ms": 0, "loss_pct": 0.0},
            "upstream-1": {"delay_ms": 0, "loss_pct": 0.0},
            "upstream-2": {"delay_ms": 0, "loss_pct": 0.0},
        }

    def get_all(self):
        result = {}
        for name, state in self.states.items():
            if state["delay_ms"] == 0 and state["loss_pct"] == 0:
                result[name] = None
            else:
                result[name] = type("ShapingState", (), state)()
        return result

    def set(self, target: str, delay_ms: int = 0, loss_pct: float = 0):
        self.calls.append(("set", target, delay_ms, loss_pct))
        self.states[target] = {"delay_ms": delay_ms, "loss_pct": loss_pct}

    def clear(self, target: str):
        self.calls.append(("clear", target))
        self.states[target] = {"delay_ms": 0, "loss_pct": 0.0}

    def clear_all(self):
        self.calls.append(("clear_all",))
        for upstream in self.states:
            self.states[upstream] = {"delay_ms": 0, "loss_pct": 0.0}


class FakeLinkStateController:
    def __init__(self):
        self.calls: list[tuple] = []
        self.states = {
            "node-1": {"target": "node-1", "router_ns": "hub", "namespace": "node-1", "device": "hub-node1", "connected": True},
            "node-2": {"target": "node-2", "router_ns": "hub", "namespace": "node-2", "device": "hub-node2", "connected": True},
            "upstream-1": {"target": "upstream-1", "router_ns": "hub-up", "namespace": "upstream-1", "device": "hubup-u1", "connected": True},
            "upstream-2": {"target": "upstream-2", "router_ns": "hub-up", "namespace": "upstream-2", "device": "hubup-u2", "connected": True},
        }

    def get_all(self):
        result = {}
        for name, state in self.states.items():
            result[name] = type("LinkState", (), state)()
        return result

    def set_connected(self, target: str, connected: bool):
        if target not in self.states:
            raise ValueError(f"unknown target {target!r}")
        self.calls.append(("set_connected", target, connected))
        self.states[target]["connected"] = connected


def sample_state(workdir: Path) -> LabState:
    return LabState(
        phase=5,
        active=True,
        created_at="2026-04-05T00:00:00+00:00",
        work_dir=str(workdir),
        namespaces={
            "hub": NamespaceInfo(pid=99, parent=None, role="hub"),
            "hub-up": NamespaceInfo(pid=100, parent="hub", role="hub-up"),
            "internet": NamespaceInfo(pid=109, parent="hub", role="internet"),
            "node-1": NamespaceInfo(pid=101, parent="hub", role="node"),
            "node-2": NamespaceInfo(pid=102, parent="hub", role="node"),
            "upstream-1": NamespaceInfo(pid=103, parent="hub-up", role="upstream"),
            "upstream-2": NamespaceInfo(pid=104, parent="hub-up", role="upstream"),
            "client-edge": NamespaceInfo(pid=105, parent="hub", role="client-edge"),
            "client-1": NamespaceInfo(pid=106, parent="client-edge", role="client"),
        },
        processes={
            "fbcoord": ProcessInfo(pid=200, ns="fbcoord", log_path=str(workdir / "fbcoord.log"), order=1),
            "fbforward-node-1": ProcessInfo(pid=201, ns="node-1", log_path=str(workdir / "node1.log"), order=2),
            "fbforward-node-2": ProcessInfo(pid=202, ns="node-2", log_path=str(workdir / "node2.log"), order=3),
        },
        proxies={
            "fbcoord": ProxyInfo("127.0.0.1", 18700, "fbcoord", "127.0.0.1", 8787),
            "node-1": ProxyInfo("127.0.0.1", 18701, "node-1", "127.0.0.1", 8080),
                "node-2": ProxyInfo("127.0.0.1", 18702, "node-2", "127.0.0.1", 8080),
        },
        clients={
            "client-1": ClientInfo(identity_ip="198.51.100.10"),
        },
        terminals={
            "client-1": TerminalInfo(host_port=18900, pid=301),
            "upstream-1": TerminalInfo(host_port=18901, pid=302),
            "upstream-2": TerminalInfo(host_port=18902, pid=303),
        },
        shaping=ShapingInfo(
            targets={
                "node-1": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-1", device="hub-node1"),
                "node-2": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-2", device="hub-node2"),
                "upstream-1": ShapingTargetInfo(
                    router_ns="hub-up",
                    tag="us-1",
                    namespace="upstream-1",
                    device="hubup-u1",
                ),
                "upstream-2": ShapingTargetInfo(
                    router_ns="hub-up",
                    tag="us-2",
                    namespace="upstream-2",
                    device="hubup-u2",
                ),
            },
        ),
        tokens=TokenInfo(coord_token="coord-token", control_token="control-token"),
        topology=TopologyInfo(base_cidr="10.99.0.0/24"),
    )


class WebAppTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.workdir = Path(self.tempdir.name)
        self.app = create_app(self.workdir)
        self.client = self.app.test_client()

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def write_state(self, state: LabState) -> None:
        save_state(self.workdir / "state.json", state)

    def test_status_returns_inactive_payload_without_state(self) -> None:
        response = self.client.get("/api/status")
        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertFalse(payload["active"])
        self.assertIn("error", payload)
        self.assertEqual({}, payload["clients"])
        self.assertEqual({}, payload["terminals"])

    def test_status_returns_active_summary_without_tokens(self) -> None:
        self.write_state(sample_state(self.workdir))
        with mock.patch("web.app.is_alive", return_value=True):
            response = self.client.get("/api/status")
        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertTrue(payload["active"])
        self.assertNotIn("tokens", payload)
        self.assertIn("fbcoord", payload["service_links"])
        self.assertNotIn("client-1", payload["service_links"])
        self.assertEqual("198.51.100.10", payload["clients"]["client-1"]["identity_ip"])
        self.assertEqual("http://127.0.0.1:18900", payload["terminals"]["client-1"]["url"])
        self.assertTrue(payload["terminals"]["upstream-1"]["alive"])
        self.assertEqual("node-1", payload["shaping_targets"][0]["target"])

    def test_coordination_returns_partial_errors(self) -> None:
        self.write_state(sample_state(self.workdir))
        with (
            mock.patch("web.app.is_alive", return_value=True),
            mock.patch("web.app.fetch_fbcoord_pool", return_value={"pool": "lab", "pick": {"version": 2, "upstream": "us-2"}, "node_count": 2, "nodes": []}),
            mock.patch("web.app.fetch_node_status", side_effect=[{"mode": "coordination"}, RuntimeError("node-2 unavailable")]),
        ):
            response = self.client.get("/api/coordination")
        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertEqual("lab", payload["fbcoord"]["pool"])
        self.assertEqual("coordination", payload["nodes"]["node-1"]["mode"])
        self.assertIsNone(payload["nodes"]["node-2"])
        self.assertIn("node-2", payload["errors"])

    def test_coordination_reports_dead_node_process_without_fetching_status(self) -> None:
        self.write_state(sample_state(self.workdir))

        def fake_is_alive(pid: int) -> bool:
            if pid == 201:
                return False
            return True

        with (
            mock.patch("web.app.is_alive", side_effect=fake_is_alive),
            mock.patch("web.app.fetch_fbcoord_pool", return_value={"pool": "lab", "pick": {"version": 2, "upstream": "us-2"}, "node_count": 1, "nodes": []}),
            mock.patch("web.app.fetch_node_status", return_value={"mode": "coordination"}) as fetch_node_status,
        ):
            response = self.client.get("/api/coordination")

        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertIsNone(payload["nodes"]["node-1"])
        self.assertEqual("process exited; see log", payload["errors"]["node-1"])
        fetch_node_status.assert_called_once_with(mock.ANY, "node-2")

    def test_coordination_maps_missing_pool_after_node_disconnect(self) -> None:
        self.write_state(sample_state(self.workdir))

        def fake_is_alive(pid: int) -> bool:
            return pid == 200

        with (
            mock.patch("web.app.is_alive", side_effect=fake_is_alive),
            mock.patch("web.app.fetch_fbcoord_pool", side_effect=RuntimeError('fbcoord pool fetch failed: status=404 body={"error":"pool not found"}')),
            mock.patch("web.app.fetch_node_status") as fetch_node_status,
        ):
            response = self.client.get("/api/coordination")

        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertEqual("pool disappeared after node disconnect", payload["errors"]["fbcoord"])
        self.assertEqual("process exited; see log", payload["errors"]["node-1"])
        self.assertEqual("process exited; see log", payload["errors"]["node-2"])
        fetch_node_status.assert_not_called()

    def test_shaping_routes_reuse_shaper_and_return_current_state(self) -> None:
        self.write_state(sample_state(self.workdir))
        fake = FakeShaper()
        with mock.patch("web.app.build_shaper_from_state", return_value=fake):
            get_response = self.client.get("/api/shaping")
            put_response = self.client.put(
                "/api/shaping/node-1",
                data=json.dumps({"delay_ms": 200, "loss_pct": 0}),
                content_type="application/json",
            )
            delete_response = self.client.delete("/api/shaping/upstream-1")
            clear_all_response = self.client.delete("/api/shaping")

        self.assertEqual(200, get_response.status_code)
        self.assertEqual(200, put_response.status_code)
        self.assertEqual(200, delete_response.status_code)
        self.assertEqual(200, clear_all_response.status_code)
        payload = get_response.get_json()
        self.assertEqual(["node-1", "node-2", "upstream-1", "upstream-2"], [entry["target"] for entry in payload["targets"]])
        self.assertEqual(("set", "node-1", 200, 0.0), fake.calls[0])
        self.assertEqual(("clear", "upstream-1"), fake.calls[1])
        self.assertEqual(("clear_all",), fake.calls[2])

    def test_link_state_routes_return_current_state_and_apply_changes(self) -> None:
        self.write_state(sample_state(self.workdir))
        fake = FakeLinkStateController()
        with mock.patch("web.app.build_link_state_controller_from_state", return_value=fake):
            get_response = self.client.get("/api/link-state")
            put_response = self.client.put(
                "/api/link-state/node-1",
                data=json.dumps({"connected": False}),
                content_type="application/json",
            )

        self.assertEqual(200, get_response.status_code)
        self.assertEqual(200, put_response.status_code)
        payload = get_response.get_json()
        self.assertEqual(["node-1", "node-2", "upstream-1", "upstream-2"], [entry["target"] for entry in payload["targets"]])
        self.assertEqual(("set_connected", "node-1", False), fake.calls[0])

    def test_link_state_route_rejects_bad_target_and_inactive_state(self) -> None:
        self.write_state(sample_state(self.workdir))
        fake = FakeLinkStateController()
        with mock.patch("web.app.build_link_state_controller_from_state", return_value=fake):
            bad = self.client.put(
                "/api/link-state/node-9",
                data=json.dumps({"connected": False}),
                content_type="application/json",
            )
        self.assertEqual(400, bad.status_code)

        inactive = sample_state(self.workdir)
        inactive.active = False
        self.write_state(inactive)
        response = self.client.get("/api/link-state")
        self.assertEqual(409, response.status_code)

    def test_logs_route_clamps_lines_and_returns_404_for_unknown_process(self) -> None:
        state = sample_state(self.workdir)
        log_path = Path(state.processes["fbforward-node-1"].log_path)
        log_path.write_text("\n".join(f"line {index}" for index in range(600)) + "\n", encoding="utf-8")
        self.write_state(state)

        ok = self.client.get("/api/logs/fbforward-node-1?lines=999")
        self.assertEqual(200, ok.status_code)
        payload = ok.get_json()
        self.assertEqual(500, payload["lines"])
        self.assertEqual(500, len(payload["text"].splitlines()))

        missing = self.client.get("/api/logs/missing")
        self.assertEqual(404, missing.status_code)


if __name__ == "__main__":
    unittest.main()
