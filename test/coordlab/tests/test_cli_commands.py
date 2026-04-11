from __future__ import annotations

import argparse
import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from cli.clients import cmd_add_client, cmd_remove_client
from cli.exec_ import cmd_exec
from cli.links import cmd_disconnect, cmd_reconnect
from cli.lifecycle import cmd_status
from cli.net import cmd_net_status
from cli.node import cmd_node_rpc, cmd_node_status, cmd_node_switch
from cli.ntfybox import cmd_notify_wait, cmd_ntfybox_clear, cmd_ntfybox_list
from lib.fbnotify import NotificationWaitTimeout
from lib.locking import acquire_client_mutation_lock
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
    NamespaceInfo,
    NodeFeatureInfo,
    ProcessInfo,
    ProxyInfo,
    ShapingInfo,
    TerminalInfo,
    TokenInfo,
    TopologyInfo,
    save_state,
)


def sample_state(workdir: Path) -> LabState:
    return LabState(
        phase=5,
        active=True,
        created_at="2026-04-08T00:00:00+00:00",
        work_dir=str(workdir),
        namespaces={
            "hub": NamespaceInfo(pid=99, parent=None, role="hub"),
            "hub-up": NamespaceInfo(pid=100, parent="hub", role="hub-up"),
            "internet": NamespaceInfo(pid=101, parent="hub", role="internet"),
            "fbcoord": NamespaceInfo(pid=102, parent="hub", role="fbcoord"),
            "fbnotify": NamespaceInfo(pid=103, parent="hub", role="fbnotify"),
            "node-1": NamespaceInfo(pid=104, parent="hub", role="node"),
            "node-2": NamespaceInfo(pid=105, parent="hub", role="node"),
            "upstream-1": NamespaceInfo(pid=106, parent="hub-up", role="upstream"),
            "upstream-2": NamespaceInfo(pid=107, parent="hub-up", role="upstream"),
            "client-edge": NamespaceInfo(pid=108, parent="hub", role="client-edge"),
            "client-1": NamespaceInfo(pid=109, parent="client-edge", role="client"),
        },
        processes={
            "fbcoord": ProcessInfo(pid=200, ns="fbcoord", log_path=str(workdir / "fbcoord.log"), order=1),
            "ttyd-client-1": ProcessInfo(pid=301, ns="host", log_path=str(workdir / "ttyd-client-1.log"), order=2),
        },
        proxies={
            "fbcoord": ProxyInfo("127.0.0.1", 18700, "fbcoord", "127.0.0.1", 8787),
            "fbnotify": ProxyInfo("127.0.0.1", 18703, "fbnotify", "127.0.0.1", 8787),
            "node-1": ProxyInfo("127.0.0.1", 18701, "node-1", "127.0.0.1", 8080),
        },
        clients={"client-1": ClientInfo(identity_ip="198.51.100.10")},
        terminals={"client-1": TerminalInfo(host_port=18900, pid=301)},
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
            node_tokens={"node-1": "node-1-token"},
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
                )
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


class CliCommandsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.workdir = Path(self.tempdir.name)

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def write_state(self, state: LabState) -> None:
        save_state(self.workdir / "state.json", state)

    def capture_stdout(self, fn, *args):
        with io.StringIO() as buffer, redirect_stdout(buffer):
            exit_code = fn(*args)
            return exit_code, buffer.getvalue()

    def test_status_json_returns_derived_payload(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), json=True)
        with mock.patch("lib.output.is_alive", return_value=True):
            exit_code, output = self.capture_stdout(cmd_status, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertTrue(payload["active"])
        self.assertEqual("http://127.0.0.1:18700", payload["service_links"]["fbcoord"])
        self.assertEqual("http://127.0.0.1:18703", payload["service_links"]["fbnotify"])
        self.assertTrue(payload["fbnotify"]["available"])
        self.assertEqual("client-1 - 301", payload["terminals"]["client-1"]["label"])
        self.assertTrue(payload["node_features"]["node-1"]["geoip"]["enabled"])

    def test_net_status_json_returns_missing_payload(self) -> None:
        args = argparse.Namespace(workdir=str(self.workdir), json=True)
        exit_code, output = self.capture_stdout(cmd_net_status, args)

        self.assertEqual(1, exit_code)
        payload = json.loads(output)
        self.assertFalse(payload["active"])
        self.assertIn("error", payload)

    def test_add_client_json_returns_updated_payload(self) -> None:
        updated = sample_state(self.workdir)
        updated.clients["client-2"] = ClientInfo(identity_ip="203.0.113.20")
        args = argparse.Namespace(workdir=str(self.workdir), client="client-2=203.0.113.20", json=True)
        with mock.patch("cli.clients.run_locked_add_client", return_value=updated):
            exit_code, output = self.capture_stdout(cmd_add_client, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertEqual("203.0.113.20", payload["clients"]["client-2"]["identity_ip"])

    def test_add_client_json_reports_busy_lock_error(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), client="client-2=203.0.113.20", json=True)
        with acquire_client_mutation_lock(self.workdir):
            exit_code, output = self.capture_stdout(cmd_add_client, args)

        self.assertEqual(1, exit_code)
        payload = json.loads(output)
        self.assertEqual("client mutation already in progress", payload["error"])

    def test_remove_client_json_returns_updated_payload(self) -> None:
        updated = sample_state(self.workdir)
        updated.clients.pop("client-1")
        args = argparse.Namespace(workdir=str(self.workdir), name="client-1", json=True)
        with mock.patch("cli.clients.run_locked_remove_client", return_value=updated):
            exit_code, output = self.capture_stdout(cmd_remove_client, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertNotIn("client-1", payload["clients"])

    def test_exec_json_runs_nsenter_command_and_returns_exit_code(self) -> None:
        state = sample_state(self.workdir)
        self.write_state(state)
        args = argparse.Namespace(workdir=str(self.workdir), ns="client-1", json=True, command=["--", "echo", "ok"])
        completed = mock.Mock(returncode=7, stdout="out", stderr="err")
        with mock.patch("cli.exec_.subprocess.run", return_value=completed) as run:
            exit_code, output = self.capture_stdout(cmd_exec, args)

        self.assertEqual(7, exit_code)
        payload = json.loads(output)
        self.assertEqual("client-1", payload["namespace"])
        self.assertEqual(state.namespaces["client-1"].pid, payload["pid"])
        self.assertEqual(["echo", "ok"], payload["command"])
        self.assertEqual("out", payload["stdout"])
        self.assertEqual("err", payload["stderr"])
        run.assert_called_once()
        self.assertIn("nsenter", run.call_args.args[0][0])

    def test_exec_json_requires_command(self) -> None:
        args = argparse.Namespace(workdir=str(self.workdir), ns="client-1", json=True, command=[])
        exit_code, output = self.capture_stdout(cmd_exec, args)

        self.assertEqual(1, exit_code)
        payload = json.loads(output)
        self.assertEqual("exec requires a command after --", payload["error"])

    def test_ntfybox_list_json_returns_messages(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), json=True)
        with mock.patch(
            "cli.ntfybox.list_ntfybox_messages",
            return_value=[{"event_name": "demo.test", "severity": "info"}],
        ):
            exit_code, output = self.capture_stdout(cmd_ntfybox_list, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertTrue(payload["ok"])
        self.assertEqual(1, payload["count"])
        self.assertEqual("demo.test", payload["messages"][0]["event_name"])

    def test_ntfybox_clear_json_returns_ok(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), json=True)
        with mock.patch("cli.ntfybox.clear_ntfybox_messages") as clear:
            exit_code, output = self.capture_stdout(cmd_ntfybox_clear, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertEqual({"ok": True}, payload)
        clear.assert_called_once()

    def test_ntfybox_commands_report_unavailable(self) -> None:
        state = sample_state(self.workdir)
        state.fbnotify.available = False
        self.write_state(state)
        args = argparse.Namespace(workdir=str(self.workdir), json=True)
        exit_code, output = self.capture_stdout(cmd_ntfybox_list, args)

        self.assertEqual(1, exit_code)
        payload = json.loads(output)
        self.assertEqual("fbnotify is not available for this lab run", payload["error"])

    def test_node_rpc_json_returns_rpc_payload(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(
            workdir=str(self.workdir),
            node="node-1",
            method="GetStatus",
            params_json='{"detail":true}',
            json=True,
        )
        with mock.patch("cli.node.rpc.rpc_call", return_value={"ok": True, "result": {"mode": "auto"}}) as rpc_call:
            exit_code, output = self.capture_stdout(cmd_node_rpc, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertTrue(payload["ok"])
        rpc_call.assert_called_once_with("http://127.0.0.1:18701", "control-token", "GetStatus", {"detail": True})

    def test_node_rpc_json_rejects_non_object_params(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(
            workdir=str(self.workdir),
            node="node-1",
            method="GetStatus",
            params_json='["not","an","object"]',
            json=True,
        )
        exit_code, output = self.capture_stdout(cmd_node_rpc, args)

        self.assertEqual(1, exit_code)
        payload = json.loads(output)
        self.assertEqual("params_json must decode to a JSON object", payload["error"])

    def test_node_switch_json_wraps_set_upstream(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), node="node-1", mode="manual", tag="us-2", json=True)
        with mock.patch("cli.node.rpc.set_upstream", return_value={"ok": True}) as set_upstream:
            exit_code, output = self.capture_stdout(cmd_node_switch, args)

        self.assertEqual(0, exit_code)
        self.assertEqual({"ok": True}, json.loads(output))
        set_upstream.assert_called_once_with("http://127.0.0.1:18701", "control-token", "manual", tag="us-2")

    def test_node_status_json_returns_metrics(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), node="node-1", json=True)
        with mock.patch("cli.node.rpc.fetch_metrics", return_value="demo_metric 1\n") as fetch_metrics:
            exit_code, output = self.capture_stdout(cmd_node_status, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertEqual("node-1", payload["node"])
        self.assertEqual("demo_metric 1\n", payload["metrics"])
        fetch_metrics.assert_called_once_with("http://127.0.0.1:18701")

    def test_node_commands_report_missing_proxy(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), node="node-2", method="GetStatus", params_json=None, json=True)
        exit_code, output = self.capture_stdout(cmd_node_rpc, args)

        self.assertEqual(1, exit_code)
        payload = json.loads(output)
        self.assertEqual("coordlab state does not expose a proxy for node-2", payload["error"])

    def test_notify_wait_json_returns_matches(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(
            workdir=str(self.workdir),
            event="demo.test",
            source_service="fbforward",
            source_instance=None,
            severity=None,
            attr=["notification.state=active"],
            timeout=30.0,
            json=True,
        )
        matches = [{"event_name": "demo.test", "severity": "info"}]
        with mock.patch("cli.ntfybox.wait_for_ntfybox_messages", return_value=matches) as wait_for_ntfybox_messages:
            exit_code, output = self.capture_stdout(cmd_notify_wait, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertEqual({"ok": True, "count": 1, "messages": matches}, payload)
        wait_for_ntfybox_messages.assert_called_once()
        _, kwargs = wait_for_ntfybox_messages.call_args
        self.assertEqual("demo.test", kwargs["event_name"])
        self.assertEqual([("notification.state", "active")], kwargs["attr_filters"])

    def test_notify_wait_json_returns_timeout_payload(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(
            workdir=str(self.workdir),
            event="demo.test",
            source_service=None,
            source_instance=None,
            severity=None,
            attr=[],
            timeout=5.0,
            json=True,
        )
        with mock.patch(
            "cli.ntfybox.wait_for_ntfybox_messages",
            side_effect=NotificationWaitTimeout([{"event_name": "demo.test"}]),
        ):
            exit_code, output = self.capture_stdout(cmd_notify_wait, args)

        self.assertEqual(1, exit_code)
        payload = json.loads(output)
        self.assertFalse(payload["ok"])
        self.assertEqual("timed out", payload["error"])
        self.assertEqual([{"event_name": "demo.test"}], payload["messages"])

    def test_notify_wait_json_rejects_invalid_attr_filter(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(
            workdir=str(self.workdir),
            event="demo.test",
            source_service=None,
            source_instance=None,
            severity=None,
            attr=["broken-filter"],
            timeout=5.0,
            json=True,
        )
        exit_code, output = self.capture_stdout(cmd_notify_wait, args)

        self.assertEqual(1, exit_code)
        payload = json.loads(output)
        self.assertIn("invalid attr filter", payload["error"])

    def test_disconnect_allows_service_target(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), target="fbnotify")
        fake_controller = mock.Mock()
        fake_controller.get_links.return_value = {"fbnotify": SimpleNamespace(connected=False)}
        with (
            mock.patch("cli.links.run_locked_set_connected", return_value=sample_state(self.workdir)) as run_locked_set_connected,
            mock.patch("cli.links.build_network_controller_from_state", return_value=fake_controller),
        ):
            exit_code, output = self.capture_stdout(cmd_disconnect, args)

        self.assertEqual(0, exit_code)
        self.assertIn("fbnotify: disconnected", output)
        run_locked_set_connected.assert_called_once_with(self.workdir.resolve(), "fbnotify", False)

    def test_reconnect_allows_client_target(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), target="client-1")
        fake_controller = mock.Mock()
        fake_controller.get_links.return_value = {"client-1": SimpleNamespace(connected=True)}
        with (
            mock.patch("cli.links.run_locked_set_connected", return_value=sample_state(self.workdir)) as run_locked_set_connected,
            mock.patch("cli.links.build_network_controller_from_state", return_value=fake_controller),
        ):
            exit_code, output = self.capture_stdout(cmd_reconnect, args)

        self.assertEqual(0, exit_code)
        self.assertIn("client-1: connected", output)
        run_locked_set_connected.assert_called_once_with(self.workdir.resolve(), "client-1", True)


if __name__ == "__main__":
    unittest.main()
