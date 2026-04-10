from __future__ import annotations

import argparse
import io
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from unittest import mock

from cli.net import cmd_net_up
from lib.build import ensure_fbforward_binaries, ensure_fbnotify_assets, ensure_geoip_mmdbs
from lib.env import parse_client_specs
from lib.fbcoord import mint_fbcoord_node_tokens
from lib.fbnotify import (
    FBNOTIFY_NODE_TOKEN_ENVS,
    bootstrap_fbnotify,
    build_fbnotify_ingress_headers,
)
from lib.output import print_basic_status
from lib.ports import TTYD_BASE_PORT, assert_host_ports_available, fixed_proxy_bindings
from lib.terminal import (
    allocate_live_ttyd_port,
    allocate_ttyd_ports,
    build_ttyd_command,
)
from lib.state import FirewallFeatureInfo, GeoIPFeatureInfo, IPLogFeatureInfo, LabState, NamespaceInfo, NodeFeatureInfo, TerminalInfo


class FakeHTTPResponse(io.BytesIO):
    def __init__(self, payload: bytes, *, status: int = 200) -> None:
        super().__init__(payload)
        self.status = status

    def __enter__(self) -> "FakeHTTPResponse":
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        self.close()
        return False


class CoordlabHelpersTest(unittest.TestCase):
    def test_parse_client_specs_accepts_multiple_clients(self) -> None:
        parsed = parse_client_specs(["client-2=203.0.113.20", "client-1=198.51.100.10"])
        self.assertEqual(
            {
                "client-2": "203.0.113.20",
                "client-1": "198.51.100.10",
            },
            parsed,
        )

    def test_parse_client_specs_rejects_invalid_cases(self) -> None:
        cases = [
            ["client-1"],
            ["node-1=198.51.100.10"],
            ["client-1=not-an-ip"],
            ["client-1=198.51.100.10", "client-1=203.0.113.20"],
            ["client-1=198.51.100.10", "client-2=198.51.100.10"],
            ["client-1=10.99.0.10"],
        ]
        for raw in cases:
            with self.assertRaises(RuntimeError, msg=f"expected failure for {raw!r}"):
                parse_client_specs(raw)

    def test_allocate_ttyd_ports_sorts_clients_then_upstreams(self) -> None:
        ports = allocate_ttyd_ports(["client-2", "client-1"], ["upstream-2", "upstream-1"])
        self.assertEqual(TTYD_BASE_PORT, ports["client-1"])
        self.assertEqual(TTYD_BASE_PORT + 1, ports["client-2"])
        self.assertEqual(TTYD_BASE_PORT + 2, ports["upstream-1"])
        self.assertEqual(TTYD_BASE_PORT + 3, ports["upstream-2"])

    def test_build_ttyd_command_wraps_nsenter_shell(self) -> None:
        command = build_ttyd_command(ns_pid=4242, port=TTYD_BASE_PORT, namespace_name="client-9")
        self.assertEqual("ttyd", command[0])
        self.assertIn("--port", command)
        self.assertIn(str(TTYD_BASE_PORT), command)
        self.assertIn("nsenter", command)
        self.assertIn("4242", command)
        self.assertIn("env", command)
        self.assertIn(r"PS1=client-9@\w$ ", command)
        self.assertEqual(["/bin/bash", "--noprofile", "--norc", "-i"], command[-4:])

    def test_allocate_live_ttyd_port_uses_lowest_free_port(self) -> None:
        port = allocate_live_ttyd_port(
            {
                "client-1": TerminalInfo(host_port=TTYD_BASE_PORT, pid=1),
                "upstream-1": TerminalInfo(host_port=TTYD_BASE_PORT + 2, pid=2),
            }
        )
        self.assertEqual(TTYD_BASE_PORT + 1, port)

    def test_ensure_fbforward_binaries_always_builds_without_skip(self) -> None:
        with (
            mock.patch("lib.build.require_tools") as require_tools,
            mock.patch("lib.build.run_host") as run_host,
            mock.patch("pathlib.Path.exists", return_value=True),
        ):
            ensure_fbforward_binaries(skip_build=False)

        require_tools.assert_called_once_with(["make"])
        run_host.assert_called_once()

    def test_ensure_fbnotify_assets_builds_without_skip(self) -> None:
        with (
            mock.patch("lib.build.require_tools") as require_tools,
            mock.patch("lib.build.run_host") as run_host,
            mock.patch("pathlib.Path.exists", return_value=True),
        ):
            ensure_fbnotify_assets(skip_build=False)

        require_tools.assert_called_once_with(["npm"])
        run_host.assert_called_once()

    def test_assert_host_ports_available_checks_proxy_and_extra_bindings(self) -> None:
        extra = [("ttyd-client-2", "127.0.0.1", TTYD_BASE_PORT)]
        with mock.patch("lib.ports.assert_bindings_available") as assert_bindings_available:
            assert_host_ports_available(extra_bindings=extra)

        assert_bindings_available.assert_called_once()
        bindings = assert_bindings_available.call_args.args[0]
        self.assertEqual([*fixed_proxy_bindings(), *extra], bindings)
        self.assertIn(("fbnotify", "127.0.0.1", 18703), fixed_proxy_bindings())

    def test_ensure_geoip_mmdbs_downloads_missing_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            with mock.patch(
                "lib.build.urllib.request.urlopen",
                side_effect=[FakeHTTPResponse(b"asn"), FakeHTTPResponse(b"country")],
            ) as urlopen:
                paths = ensure_geoip_mmdbs(Path(tmpdir))

            self.assertEqual(2, urlopen.call_count)
            self.assertEqual(b"asn", paths["asn"].read_bytes())
            self.assertEqual(b"country", paths["country"].read_bytes())

    def test_ensure_geoip_mmdbs_reuses_cached_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            workdir = Path(tmpdir)
            mmdb_dir = workdir / "mmdb"
            mmdb_dir.mkdir()
            (mmdb_dir / "GeoLite2-ASN.mmdb").write_bytes(b"cached-asn")
            (mmdb_dir / "Country-without-asn.mmdb").write_bytes(b"cached-country")

            with mock.patch("lib.build.urllib.request.urlopen") as urlopen:
                paths = ensure_geoip_mmdbs(workdir)

            urlopen.assert_not_called()
            self.assertEqual(b"cached-asn", paths["asn"].read_bytes())
            self.assertEqual(b"cached-country", paths["country"].read_bytes())

    def test_ensure_geoip_mmdbs_downloads_only_missing_file(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            workdir = Path(tmpdir)
            mmdb_dir = workdir / "mmdb"
            mmdb_dir.mkdir()
            (mmdb_dir / "GeoLite2-ASN.mmdb").write_bytes(b"cached-asn")

            with mock.patch(
                "lib.build.urllib.request.urlopen",
                return_value=FakeHTTPResponse(b"country"),
            ) as urlopen:
                paths = ensure_geoip_mmdbs(workdir)

            urlopen.assert_called_once()
            self.assertEqual(b"cached-asn", paths["asn"].read_bytes())
            self.assertEqual(b"country", paths["country"].read_bytes())

    def test_ensure_geoip_mmdbs_fails_on_non_200_response(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            with mock.patch(
                "lib.build.urllib.request.urlopen",
                return_value=FakeHTTPResponse(b"", status=503),
            ):
                with self.assertRaises(RuntimeError):
                    ensure_geoip_mmdbs(Path(tmpdir))

    def test_cmd_net_up_does_not_download_mmdbs(self) -> None:
        args = argparse.Namespace(workdir="/tmp/coordlab-test", client=[])
        state = LabState(
            phase=1,
            active=True,
            created_at="2026-04-08T00:00:00+00:00",
            work_dir="/tmp/coordlab-test",
            namespaces={"hub": NamespaceInfo(pid=1, parent=None, role="hub")},
        )
        with (
            mock.patch("cli.net.load_state", return_value=None),
            mock.patch("cli.net.require_tools"),
            mock.patch("cli.net.parse_client_specs", return_value={}),
            mock.patch("cli.net.netns.build_topology", return_value=mock.Mock()),
            mock.patch("cli.net.netns.verify_connectivity"),
            mock.patch("cli.net.build_state", return_value=state),
            mock.patch("cli.net.save_state"),
            mock.patch("cli.net.print_basic_status"),
            mock.patch("lib.build.ensure_geoip_mmdbs") as ensure_geoip_mmdbs_mock,
        ):
            self.assertEqual(0, cmd_net_up(args))

        ensure_geoip_mmdbs_mock.assert_not_called()

    def test_print_basic_status_includes_node_features_and_artifact_dirs(self) -> None:
        state = LabState(
            phase=5,
            active=True,
            created_at="2026-04-08T00:00:00+00:00",
            work_dir="/tmp/coordlab-phase3",
            namespaces={"node-1": NamespaceInfo(pid=101, parent="hub", role="node")},
            node_features={
                "node-1": NodeFeatureInfo(
                    geoip=GeoIPFeatureInfo(
                        enabled=True,
                        asn_db_url="https://example.test/asn.mmdb",
                        asn_db_path="/tmp/coordlab-phase3/mmdb/GeoLite2-ASN.mmdb",
                        country_db_url="https://example.test/country.mmdb",
                        country_db_path="/tmp/coordlab-phase3/mmdb/Country-without-asn.mmdb",
                        refresh_interval="24h",
                    ),
                    ip_log=IPLogFeatureInfo(
                        enabled=True,
                        db_path="/tmp/coordlab-phase3/data/node-1-iplog.sqlite",
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
        )

        with (
            mock.patch("lib.output.is_alive", return_value=True),
            io.StringIO() as buffer,
            redirect_stdout(buffer),
        ):
            print_basic_status(state)
            output = buffer.getvalue()

        self.assertIn("geoip: enabled", output)
        self.assertIn("ip_log: enabled", output)
        self.assertIn("firewall: enabled default=allow", output)
        self.assertIn("mmdb=/tmp/coordlab-phase3/mmdb", output)
        self.assertIn("data=/tmp/coordlab-phase3/data", output)

    def test_mint_fbcoord_node_tokens_logs_in_and_records_returned_tokens(self) -> None:
        with (
            mock.patch(
                "lib.fbcoord.ns_http_request_with_headers",
                return_value=(
                    200,
                    {"Set-Cookie": "fbcoord_session=test-session; Max-Age=86400; HttpOnly; Secure"},
                    '{"ok":true}',
                ),
            ) as login_request,
            mock.patch(
                "lib.fbcoord.ns_http_request",
                side_effect=[
                    (200, '{"token":"node-1-token","info":{"node_id":"node-1"}}'),
                    (200, '{"token":"node-2-token","info":{"node_id":"node-2"}}'),
                ],
            ) as node_request,
        ):
            minted = mint_fbcoord_node_tokens(
                "http://10.99.0.2:8787",
                101,
                "operator-token",
                ("node-1", "node-2"),
            )

        self.assertEqual(
            {
                "node-1": "node-1-token",
                "node-2": "node-2-token",
            },
            minted,
        )
        login_request.assert_called_once_with(
            101,
            "http://10.99.0.2:8787/api/auth/login",
            method="POST",
            headers={"Content-Type": "application/json"},
            body='{"token": "operator-token"}',
        )
        self.assertEqual(2, node_request.call_count)
        self.assertEqual(
            mock.call(
                101,
                "http://10.99.0.2:8787/api/node-tokens",
                method="POST",
                headers={
                    "Content-Type": "application/json",
                    "Cookie": "fbcoord_session=test-session",
                },
                body='{"node_id": "node-1"}',
            ),
            node_request.call_args_list[0],
        )

    def test_bootstrap_fbnotify_creates_capture_route_and_emitter_tokens(self) -> None:
        with (
            mock.patch(
                "lib.fbnotify.ns_http_request_with_headers",
                return_value=(
                    200,
                    {"Set-Cookie": "fbnotify_session=test-session; Max-Age=86400; HttpOnly; Secure"},
                    '{"ok":true}',
                ),
            ) as login_request,
            mock.patch(
                "lib.fbnotify.ns_http_request",
                side_effect=[
                    (200, '{"id":"tgt_capture"}'),
                    (200, '{"id":"route_default"}'),
                    (200, '{"key_id":"node-1-key","token":"node-1-token"}'),
                    (200, '{"key_id":"node-2-key","token":"node-2-token"}'),
                    (200, '{"key_id":"fbcoord-key","token":"fbcoord-token"}'),
                ],
            ) as request,
        ):
            emitters = bootstrap_fbnotify("http://10.99.0.30:8787", 101, "operator-token")

        self.assertEqual("node-1-key", emitters["node-1"].key_id)
        self.assertEqual("fbcoord", emitters["fbcoord"].source_service)
        login_request.assert_called_once()
        self.assertEqual(
            mock.call(
                101,
                "http://10.99.0.30:8787/api/targets",
                method="POST",
                headers={"Content-Type": "application/json", "Cookie": "fbnotify_session=test-session"},
                body='{"name": "coordlab-capture", "type": "capture", "config": {}}',
            ),
            request.call_args_list[0],
        )

    def test_build_fbnotify_ingress_headers_signs_expected_payload(self) -> None:
        headers = build_fbnotify_ingress_headers(
            "key-1",
            "secret-token",
            '{"event_name":"demo.test"}',
            header_timestamp=1775779200,
        )
        self.assertEqual("key-1", headers["X-FBNotify-Key-Id"])
        self.assertEqual("1775779200", headers["X-FBNotify-Timestamp"])
        self.assertEqual("application/json", headers["Content-Type"])
        self.assertTrue(headers["X-FBNotify-Signature"])

    def test_fixed_fbnotify_node_token_env_names(self) -> None:
        self.assertEqual("FBNOTIFY_TOKEN_NODE_1", FBNOTIFY_NODE_TOKEN_ENVS["node-1"])
        self.assertEqual("FBNOTIFY_TOKEN_NODE_2", FBNOTIFY_NODE_TOKEN_ENVS["node-2"])


if __name__ == "__main__":
    unittest.main()
