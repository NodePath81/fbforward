from __future__ import annotations

import argparse
import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from coordlab import cmd_add_client, cmd_exec, cmd_net_status, cmd_remove_client, cmd_status
from lib.locking import acquire_client_mutation_lock
from lib.state import (
    ClientInfo,
    FirewallFeatureInfo,
    GeoIPFeatureInfo,
    IPLogFeatureInfo,
    LabState,
    NamespaceInfo,
    NodeFeatureInfo,
    ProcessInfo,
    ProxyInfo,
    ShapingInfo,
    ShapingTargetInfo,
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
            "node-1": NamespaceInfo(pid=101, parent="hub", role="node"),
            "client-1": NamespaceInfo(pid=106, parent="client-edge", role="client"),
        },
        processes={
            "fbcoord": ProcessInfo(pid=200, ns="fbcoord", log_path=str(workdir / "fbcoord.log"), order=1),
            "ttyd-client-1": ProcessInfo(pid=301, ns="host", log_path=str(workdir / "ttyd-client-1.log"), order=2),
        },
        proxies={
            "fbcoord": ProxyInfo("127.0.0.1", 18700, "fbcoord", "127.0.0.1", 8787),
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
        shaping=ShapingInfo(
            targets={
                "node-1": ShapingTargetInfo(router_ns="hub", tag="", namespace="node-1", device="hub-node1"),
            },
        ),
        tokens=TokenInfo(coord_token="coord-token", control_token="control-token"),
        topology=TopologyInfo(base_cidr="10.99.0.0/24", next_subnet_index=10),
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
        with mock.patch("coordlab.is_alive", return_value=True):
            exit_code, output = self.capture_stdout(cmd_status, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertTrue(payload["active"])
        self.assertEqual("http://127.0.0.1:18700", payload["service_links"]["fbcoord"])
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
        with mock.patch("coordlab.run_locked_add_client", return_value=updated):
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
        with mock.patch("coordlab.run_locked_remove_client", return_value=updated):
            exit_code, output = self.capture_stdout(cmd_remove_client, args)

        self.assertEqual(0, exit_code)
        payload = json.loads(output)
        self.assertNotIn("client-1", payload["clients"])

    def test_exec_json_runs_nsenter_command_and_returns_exit_code(self) -> None:
        self.write_state(sample_state(self.workdir))
        args = argparse.Namespace(workdir=str(self.workdir), ns="client-1", json=True, command=["--", "echo", "ok"])
        completed = mock.Mock(returncode=7, stdout="out", stderr="err")
        with (
            mock.patch("coordlab.subprocess.run", return_value=completed) as run,
            mock.patch("coordlab.is_alive", return_value=True),
        ):
            exit_code, output = self.capture_stdout(cmd_exec, args)

        self.assertEqual(7, exit_code)
        payload = json.loads(output)
        self.assertEqual("client-1", payload["namespace"])
        self.assertEqual(106, payload["pid"])
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


if __name__ == "__main__":
    unittest.main()
