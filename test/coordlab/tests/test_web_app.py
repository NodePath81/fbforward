from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.netns import default_links
from lib.state import (
    ClientInfo,
    FBNotifyEmitterInfo,
    FBNotifyInfo,
    FirewallFeatureInfo,
    GeoIPFeatureInfo,
    IPLogFeatureInfo,
    LabState,
    LinkInfo,
    NodeFeatureInfo,
    NamespaceInfo,
    ProcessInfo,
    ProxyInfo,
    ShapingInfo,
    TerminalInfo,
    TokenInfo,
    TopologyInfo,
    save_state,
)
from lib.locking import acquire_client_mutation_lock
from web.app import create_app


class FakeNetworkController:
    def __init__(self):
        self.shaping_states = {
            "node-1": SimpleNamespace(
                target="node-1",
                display_name="node-1",
                kind="node",
                router_ns="hub",
                namespace="node-1",
                device="hub-node1",
                delay_ms=0,
                loss_pct=0.0,
                connected=True,
            ),
            "node-2": SimpleNamespace(
                target="node-2",
                display_name="node-2",
                kind="node",
                router_ns="hub",
                namespace="node-2",
                device="hub-node2",
                delay_ms=0,
                loss_pct=0.0,
                connected=True,
            ),
            "upstream-1": SimpleNamespace(
                target="upstream-1",
                display_name="upstream-1",
                kind="upstream",
                router_ns="hub-up",
                namespace="upstream-1",
                device="hubup-u1",
                delay_ms=0,
                loss_pct=0.0,
                connected=True,
            ),
            "upstream-2": SimpleNamespace(
                target="upstream-2",
                display_name="upstream-2",
                kind="upstream",
                router_ns="hub-up",
                namespace="upstream-2",
                device="hubup-u2",
                delay_ms=0,
                loss_pct=0.0,
                connected=True,
            ),
        }
        self.link_states = {
            "fbcoord": SimpleNamespace(
                target="fbcoord",
                display_name="fbcoord",
                kind="service",
                namespace="fbcoord",
                router_ns="hub",
                device="hub-fbcoord",
                peer_device="fbcoord-peer",
                shape_capable=False,
                connected=True,
            ),
            "fbnotify": SimpleNamespace(
                target="fbnotify",
                display_name="fbnotify",
                kind="service",
                namespace="fbnotify",
                router_ns="hub",
                device="hub-fbnotify",
                peer_device="fbnotify-peer",
                shape_capable=False,
                connected=False,
            ),
            "node-1": SimpleNamespace(
                target="node-1",
                display_name="node-1",
                kind="node",
                namespace="node-1",
                router_ns="hub",
                device="hub-node1",
                peer_device="node1-peer",
                shape_capable=True,
                connected=True,
            ),
            "node-2": SimpleNamespace(
                target="node-2",
                display_name="node-2",
                kind="node",
                namespace="node-2",
                router_ns="hub",
                device="hub-node2",
                peer_device="node2-peer",
                shape_capable=True,
                connected=True,
            ),
            "upstream-1": SimpleNamespace(
                target="upstream-1",
                display_name="upstream-1",
                kind="upstream",
                namespace="upstream-1",
                router_ns="hub-up",
                device="hubup-u1",
                peer_device="upstream1-peer",
                shape_capable=True,
                connected=True,
            ),
            "upstream-2": SimpleNamespace(
                target="upstream-2",
                display_name="upstream-2",
                kind="upstream",
                namespace="upstream-2",
                router_ns="hub-up",
                device="hubup-u2",
                peer_device="upstream2-peer",
                shape_capable=True,
                connected=True,
            ),
            "client-1": SimpleNamespace(
                target="client-1",
                display_name="client-1",
                kind="client",
                namespace="client-1",
                router_ns="client-edge",
                device="cedge-c1",
                peer_device="c1-peer",
                shape_capable=False,
                connected=True,
            ),
        }

    def get_shaping_all(self):
        return self.shaping_states

    def get_links(self):
        return self.link_states


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
            "fbcoord": NamespaceInfo(pid=97, parent="hub", role="fbcoord"),
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
        shaping=ShapingInfo(),
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
        topology=TopologyInfo(
            base_cidr="10.99.0.0/24",
            links=[
                LinkInfo(
                    left_ns=link.left_ns,
                    right_ns=link.right_ns,
                    left_if=link.left_if,
                    right_if=link.right_if,
                    subnet=link.subnet,
                    left_ip=link.left_ip,
                    right_ip=link.right_ip,
                )
                for link in default_links(client_names=["client-1"])
            ],
            next_subnet_index=10,
        ),
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
        self.assertIn("fbnotify", {entry["target"] for entry in payload["shaping_targets"]})
        self.assertIn("client-1", {entry["target"] for entry in payload["shaping_targets"]})

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

    def test_shaping_routes_reuse_controller_and_return_shape_capable_targets(self) -> None:
        self.write_state(sample_state(self.workdir))
        fake = FakeNetworkController()
        with (
            mock.patch("web.app.build_network_controller_from_state", return_value=fake),
            mock.patch("web.app.run_locked_set_shaping", return_value=sample_state(self.workdir)) as run_locked_set_shaping,
            mock.patch("web.app.run_locked_clear_shaping", return_value=sample_state(self.workdir)) as run_locked_clear_shaping,
            mock.patch("web.app.run_locked_clear_all_shaping", return_value=sample_state(self.workdir)) as run_locked_clear_all_shaping,
        ):
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
        run_locked_set_shaping.assert_called_once_with(self.workdir.resolve(), "node-1", 200, 0.0)
        run_locked_clear_shaping.assert_called_once_with(self.workdir.resolve(), "upstream-1")
        run_locked_clear_all_shaping.assert_called_once_with(self.workdir.resolve())

    def test_link_state_routes_return_current_state_and_apply_changes(self) -> None:
        self.write_state(sample_state(self.workdir))
        fake = FakeNetworkController()
        with (
            mock.patch("web.app.build_network_controller_from_state", return_value=fake),
            mock.patch("web.app.run_locked_set_connected", return_value=sample_state(self.workdir)) as run_locked_set_connected,
        ):
            get_response = self.client.get("/api/link-state")
            put_response = self.client.put(
                "/api/link-state/fbnotify",
                data=json.dumps({"connected": False}),
                content_type="application/json",
            )

        self.assertEqual(200, get_response.status_code)
        self.assertEqual(200, put_response.status_code)
        payload = get_response.get_json()
        self.assertCountEqual(
            ["fbcoord", "fbnotify", "node-1", "node-2", "upstream-1", "upstream-2", "client-1"],
            [entry["target"] for entry in payload["targets"]],
        )
        fbnotify = next(entry for entry in payload["targets"] if entry["target"] == "fbnotify")
        client = next(entry for entry in payload["targets"] if entry["target"] == "client-1")
        self.assertFalse(fbnotify["shape_capable"])
        self.assertFalse(client["shape_capable"])
        run_locked_set_connected.assert_called_once_with(self.workdir.resolve(), "fbnotify", False)

    def test_link_state_route_rejects_bad_target_and_inactive_state(self) -> None:
        self.write_state(sample_state(self.workdir))
        with mock.patch("web.app.run_locked_set_connected", side_effect=ValueError("unknown target 'node-9'")):
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

    def test_shaping_route_rejects_non_shape_capable_target(self) -> None:
        self.write_state(sample_state(self.workdir))
        with mock.patch(
            "web.app.run_locked_set_shaping",
            side_effect=ValueError("target 'fbnotify' does not support shaping"),
        ):
            response = self.client.put(
                "/api/shaping/fbnotify",
                data=json.dumps({"delay_ms": 100, "loss_pct": 0}),
                content_type="application/json",
            )

        self.assertEqual(400, response.status_code)
        self.assertIn("does not support shaping", response.get_json()["error"])

    def test_index_and_static_assets_include_fbnotify_and_client_topology_hooks(self) -> None:
        index = self.client.get("/")
        script = self.client.get("/static/app.js")
        styles = self.client.get("/static/styles.css")

        self.assertEqual(200, index.status_code)
        self.assertEqual(200, script.status_code)
        self.assertEqual(200, styles.status_code)
        self.assertIn("topology-target-fbnotify", index.get_data(as_text=True))
        self.assertIn("topology-clients", index.get_data(as_text=True))
        self.assertIn("FIXED_TARGET_ORDER", script.get_data(as_text=True))
        self.assertIn("client-edge", script.get_data(as_text=True))
        self.assertIn("shape-card-offline", styles.get_data(as_text=True))
        index.close()
        script.close()
        styles.close()

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
