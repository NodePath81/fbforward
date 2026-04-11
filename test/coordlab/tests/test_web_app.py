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
    FBNotifyEmitterInfo,
    FBNotifyInfo,
    FirewallFeatureInfo,
    GeoIPFeatureInfo,
    IPLogFeatureInfo,
    LabState,
    NodeFeatureInfo,
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
from lib.locking import acquire_client_mutation_lock
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
            "fbnotify": NamespaceInfo(pid=98, parent="hub", role="fbnotify"),
            "node-1": NamespaceInfo(pid=101, parent="hub", role="node"),
            "node-2": NamespaceInfo(pid=102, parent="hub", role="node"),
            "upstream-1": NamespaceInfo(pid=103, parent="hub-up", role="upstream"),
            "upstream-2": NamespaceInfo(pid=104, parent="hub-up", role="upstream"),
            "client-edge": NamespaceInfo(pid=105, parent="hub", role="client-edge"),
            "client-1": NamespaceInfo(pid=106, parent="client-edge", role="client"),
        },
        processes={
            "fbnotify": ProcessInfo(pid=199, ns="fbnotify", log_path=str(workdir / "fbnotify.log"), order=0),
            "fbcoord": ProcessInfo(pid=200, ns="fbcoord", log_path=str(workdir / "fbcoord.log"), order=1),
            "fbforward-node-1": ProcessInfo(pid=201, ns="node-1", log_path=str(workdir / "node1.log"), order=2),
            "fbforward-node-2": ProcessInfo(pid=202, ns="node-2", log_path=str(workdir / "node2.log"), order=3),
            "ttyd-client-1": ProcessInfo(pid=301, ns="host", log_path=str(workdir / "ttyd-client-1.log"), order=4),
            "ttyd-upstream-1": ProcessInfo(pid=302, ns="host", log_path=str(workdir / "ttyd-upstream-1.log"), order=5),
            "ttyd-upstream-2": ProcessInfo(pid=303, ns="host", log_path=str(workdir / "ttyd-upstream-2.log"), order=6),
        },
        proxies={
            "fbcoord": ProxyInfo("127.0.0.1", 18700, "fbcoord", "127.0.0.1", 8787),
            "fbnotify": ProxyInfo("127.0.0.1", 18703, "fbnotify", "127.0.0.1", 8787),
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
        node_features={
            "node-1": NodeFeatureInfo(
                geoip=GeoIPFeatureInfo(
                    enabled=True,
                    asn_db_url="https://example.test/asn.mmdb",
                    asn_db_path=str(workdir / "mmdb" / "GeoLite2-ASN.mmdb"),
                    country_db_url="https://example.test/country.mmdb",
                    country_db_path=str(workdir / "mmdb" / "Country-without-asn.mmdb"),
                    refresh_interval="24h",
                ),
                ip_log=IPLogFeatureInfo(
                    enabled=True,
                    db_path=str(workdir / "data" / "node-1-iplog.sqlite"),
                    retention="24h",
                    geo_queue_size=128,
                    write_queue_size=128,
                    batch_size=10,
                    flush_interval="2s",
                    prune_interval="1h",
                ),
                firewall=FirewallFeatureInfo(enabled=True, default_policy="allow"),
            ),
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
        tokens=TokenInfo(
            control_token="control-token",
            operator_token="operator-token",
            node_tokens={"node-1": "node-1-token", "node-2": "node-2-token"},
        ),
        fbnotify=FBNotifyInfo(
            available=True,
            error="",
            public_url="http://127.0.0.1:18703",
            internal_base_url="http://10.99.0.30:8787",
            internal_ingest_url="http://10.99.0.30:8787/v1/events",
            operator_token="fbnotify-operator-token",
            emitters={
                "node-1": FBNotifyEmitterInfo(
                    key_id="notify-key-1",
                    token="fbnotify-secret-node-1",
                    source_service="fbforward",
                    source_instance="node-1",
                ),
            },
        ),
        topology=TopologyInfo(base_cidr="10.99.0.0/24", next_subnet_index=10),
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
        self.assertEqual({}, payload["node_features"])

    def test_status_returns_active_summary_without_tokens(self) -> None:
        self.write_state(sample_state(self.workdir))
        with (
            mock.patch("web.app.is_alive", return_value=True),
            mock.patch("lib.output.is_alive", return_value=True),
        ):
            response = self.client.get("/api/status")
        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertTrue(payload["active"])
        self.assertNotIn("tokens", payload)
        self.assertIn("fbcoord", payload["service_links"])
        self.assertIn("fbnotify", payload["service_links"])
        self.assertTrue(payload["fbnotify"]["available"])
        self.assertNotIn("client-1", payload["service_links"])
        self.assertEqual("198.51.100.10", payload["clients"]["client-1"]["identity_ip"])
        self.assertEqual("http://127.0.0.1:18900", payload["terminals"]["client-1"]["url"])
        self.assertEqual("client-1 - 301", payload["terminals"]["client-1"]["label"])

    def test_status_hides_fbnotify_service_link_when_proxy_is_missing(self) -> None:
        state = sample_state(self.workdir)
        state.proxies.pop("fbnotify")
        state.fbnotify.available = False
        state.fbnotify.error = "public proxy failed"
        self.write_state(state)
        with (
            mock.patch("web.app.is_alive", return_value=True),
            mock.patch("lib.output.is_alive", return_value=True),
        ):
            response = self.client.get("/api/status")

        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertNotIn("fbnotify", payload["service_links"])
        self.assertFalse(payload["fbnotify"]["available"])
        self.assertEqual("public proxy failed", payload["fbnotify"]["error"])
        self.assertTrue(payload["terminals"]["upstream-1"]["alive"])
        self.assertTrue(payload["node_features"]["node-1"]["geoip"]["enabled"])
        self.assertEqual("node-1", payload["shaping_targets"][0]["target"])

    def test_coordination_returns_partial_errors(self) -> None:
        self.write_state(sample_state(self.workdir))
        with (
            mock.patch("web.app.is_alive", return_value=True),
            mock.patch(
                "web.app.fetch_fbcoord_state",
                return_value={
                    "pick": {"version": 2, "upstream": "us-2"},
                    "node_count": 2,
                    "nodes": [{"node_id": "node-1"}, {"node_id": "node-2"}],
                },
            ),
            mock.patch("web.app.fetch_node_status", side_effect=[{"mode": "coordination"}, RuntimeError("node-2 unavailable")]),
        ):
            response = self.client.get("/api/coordination")
        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertEqual(2, payload["fbcoord"]["node_count"])
        self.assertEqual("node-1", payload["fbcoord"]["nodes"][0]["node_id"])
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
            mock.patch(
                "web.app.fetch_fbcoord_state",
                return_value={"pick": {"version": 2, "upstream": "us-2"}, "node_count": 1, "nodes": [{"node_id": "node-2"}]},
            ),
            mock.patch("web.app.fetch_node_status", return_value={"mode": "coordination"}) as fetch_node_status,
        ):
            response = self.client.get("/api/coordination")

        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertIsNone(payload["nodes"]["node-1"])
        self.assertEqual("process exited; see log", payload["errors"]["node-1"])
        fetch_node_status.assert_called_once_with(mock.ANY, "node-2")

    def test_coordination_surfaces_fbcoord_state_fetch_errors(self) -> None:
        self.write_state(sample_state(self.workdir))

        def fake_is_alive(pid: int) -> bool:
            return pid == 200

        with (
            mock.patch("web.app.is_alive", side_effect=fake_is_alive),
            mock.patch("web.app.fetch_fbcoord_state", side_effect=RuntimeError('fbcoord state fetch failed: status=500 body={"error":"boom"}')),
            mock.patch("web.app.fetch_node_status") as fetch_node_status,
        ):
            response = self.client.get("/api/coordination")

        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertEqual('fbcoord state fetch failed: status=500 body={"error":"boom"}', payload["errors"]["fbcoord"])
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

    def test_ntfybox_routes_proxy_capture_data(self) -> None:
        self.write_state(sample_state(self.workdir))
        with mock.patch("web.app.list_ntfybox_messages", return_value=[{"event_name": "demo.test"}]):
            response = self.client.get("/api/ntfybox")

        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertEqual("demo.test", payload["messages"][0]["event_name"])

    def test_ntfybox_clear_route_returns_ok(self) -> None:
        self.write_state(sample_state(self.workdir))
        with mock.patch("web.app.clear_ntfybox_messages") as clear:
            response = self.client.delete("/api/ntfybox")

        self.assertEqual(200, response.status_code)
        self.assertEqual({"ok": True}, response.get_json())
        clear.assert_called_once()

    def test_ntfybox_routes_report_unavailable(self) -> None:
        state = sample_state(self.workdir)
        state.fbnotify.available = False
        self.write_state(state)

        response = self.client.get("/api/ntfybox")

        self.assertEqual(409, response.status_code)
        self.assertEqual("fbnotify is not available for this lab run", response.get_json()["error"])

    def test_add_client_route_returns_updated_status_payload(self) -> None:
        updated = sample_state(self.workdir)
        updated.clients["client-2"] = ClientInfo(identity_ip="203.0.113.20")
        with (
            mock.patch("web.app.is_alive", return_value=True),
            mock.patch("web.app.run_locked_add_client", return_value=updated) as add_client,
        ):
            self.write_state(sample_state(self.workdir))
            response = self.client.post(
                "/api/clients",
                data=json.dumps({"name": "client-2", "identity_ip": "203.0.113.20"}),
                content_type="application/json",
            )

        self.assertEqual(200, response.status_code)
        payload = response.get_json()
        self.assertEqual("203.0.113.20", payload["clients"]["client-2"]["identity_ip"])
        add_client.assert_called_once()

    def test_add_client_route_rejects_invalid_body(self) -> None:
        self.write_state(sample_state(self.workdir))
        response = self.client.post(
            "/api/clients",
            data=json.dumps({"name": "bad", "identity_ip": "not-an-ip"}),
            content_type="application/json",
        )
        self.assertEqual(400, response.status_code)

    def test_delete_client_route_returns_404_for_unknown_client(self) -> None:
        self.write_state(sample_state(self.workdir))
        response = self.client.delete("/api/clients/client-9")
        self.assertEqual(404, response.status_code)

    def test_client_mutation_routes_return_409_when_busy(self) -> None:
        self.write_state(sample_state(self.workdir))
        with acquire_client_mutation_lock(self.workdir):
            response = self.client.post(
                "/api/clients",
                data=json.dumps({"name": "client-2", "identity_ip": "203.0.113.20"}),
                content_type="application/json",
            )
        self.assertEqual(409, response.status_code)

    def test_add_client_route_port_conflict_mentions_only_ttyd_binding(self) -> None:
        self.write_state(sample_state(self.workdir))
        with mock.patch(
            "web.app.run_locked_add_client",
            side_effect=RuntimeError("coordlab ttyd ports are already in use: ttyd-client-2:127.0.0.1:18900"),
        ):
            response = self.client.post(
                "/api/clients",
                data=json.dumps({"name": "client-2", "identity_ip": "203.0.113.20"}),
                content_type="application/json",
            )

        self.assertEqual(409, response.status_code)
        payload = response.get_json()
        self.assertIn("ttyd-client-2:127.0.0.1:18900", payload["error"])
        self.assertNotIn("fbcoord", payload["error"])

    def test_terminal_links_renderer_uses_label_when_present(self) -> None:
        script = (ROOT / "web" / "static" / "app.js").read_text(encoding="utf-8")
        self.assertIn("${info.label || name}", script)


if __name__ == "__main__":
    unittest.main()
