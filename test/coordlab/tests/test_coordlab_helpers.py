from __future__ import annotations

import argparse
import io
import sys
import tempfile
import unittest
from contextlib import ExitStack, redirect_stdout
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from unittest import mock

from cli.lifecycle import cmd_up
from cli.net import cmd_net_up
from lib.build import ensure_fbforward_binaries, ensure_fbnotify_assets, ensure_geoip_mmdbs
from lib.env import parse_client_specs
from lib.fbcoord import mint_fbcoord_node_tokens
from lib.fbnotify import (
    FBNOTIFY_NODE_TOKEN_ENVS,
    NotificationWaitTimeout,
    bootstrap_fbnotify,
    build_fbnotify_ingress_headers,
    wait_for_ntfybox_messages,
)
from lib.output import print_basic_status
from lib.ports import TTYD_BASE_PORT, assert_host_ports_available, fixed_proxy_bindings
from lib.terminal import (
    allocate_live_ttyd_port,
    allocate_ttyd_ports,
    build_ttyd_command,
)
from lib.state import (
    FBNotifyEmitterInfo,
    FBNotifyInfo,
    FirewallFeatureInfo,
    GeoIPFeatureInfo,
    IPLogFeatureInfo,
    LabState,
    NamespaceInfo,
    NodeFeatureInfo,
    ProcessInfo,
    ProxyInfo,
    TerminalInfo,
    TokenInfo,
)


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

    def test_wait_for_ntfybox_messages_matches_attribute_filters(self) -> None:
        state = LabState(
            phase=5,
            active=True,
            created_at="2026-04-10T00:00:00+00:00",
            work_dir="/tmp/coordlab",
            fbnotify=FBNotifyInfo(available=True, public_url="http://127.0.0.1:18703"),
        )
        messages = [
            {
                "event_name": "upstream.active_cleared",
                "source_service": "fbforward",
                "source_instance": "node-1",
                "severity": "critical",
                "payload": '{"attributes":{"notification.state":"active"}}',
            }
        ]
        with mock.patch("lib.fbnotify.list_ntfybox_messages", return_value=messages):
            matches = wait_for_ntfybox_messages(
                state,
                event_name="upstream.active_cleared",
                source_service="fbforward",
                attr_filters=[("notification.state", "active")],
                timeout_sec=1,
                interval_sec=0,
            )

        self.assertEqual(messages, matches)

    def test_wait_for_ntfybox_messages_times_out_with_last_snapshot(self) -> None:
        state = LabState(
            phase=5,
            active=True,
            created_at="2026-04-10T00:00:00+00:00",
            work_dir="/tmp/coordlab",
            fbnotify=FBNotifyInfo(available=True, public_url="http://127.0.0.1:18703"),
        )
        messages = [{"event_name": "other.event"}]
        with (
            mock.patch("lib.fbnotify.list_ntfybox_messages", return_value=messages),
            mock.patch("lib.fbnotify.time.monotonic", side_effect=[0.0, 0.1, 1.0]),
        ):
            with self.assertRaises(NotificationWaitTimeout) as ctx:
                wait_for_ntfybox_messages(
                    state,
                    event_name="upstream.active_cleared",
                    timeout_sec=0.5,
                    interval_sec=0,
                )

        self.assertEqual(messages, ctx.exception.messages)

    def test_cmd_up_degrades_when_fbnotify_public_proxy_verification_fails(self) -> None:
        workdir = Path("/tmp/coordlab-failure")
        args = argparse.Namespace(
            workdir=str(workdir),
            skip_build=True,
            skip_connectivity_check=False,
            client=[],
        )
        saved_states: list[LabState] = []

        class FakeProcessManager:
            def __init__(self, _logs_dir):
                self._infos: dict[str, ProcessInfo] = {}

            def start(self, _ns_pid, ns_name, _cmd, name, **_kwargs):
                self._infos[name] = ProcessInfo(
                    pid=1000 + len(self._infos),
                    ns=ns_name,
                    log_path=f"/tmp/{name}.log",
                    order=len(self._infos),
                )
                return mock.Mock(pid=self._infos[name].pid)

            def start_host(self, _cmd, name, **_kwargs):
                self._infos[name] = ProcessInfo(
                    pid=2000 + len(self._infos),
                    ns="host",
                    log_path=f"/tmp/{name}.log",
                    order=len(self._infos),
                )
                return mock.Mock(pid=self._infos[name].pid)

            def stop(self, name, timeout_sec=5.0):
                self._infos.pop(name, None)

            def stop_all(self, timeout_sec=5.0):
                self._infos.clear()

            def infos(self):
                return dict(self._infos)

        def fake_load_state(_path):
            if not saved_states:
                return None
            return saved_states[-1]

        def fake_save_state(_path, state):
            saved_states.append(state)

        def fake_build_state(
            workdir,
            _topology,
            phase,
            *,
            active,
            processes=None,
            proxies=None,
            terminals=None,
            node_features=None,
            tokens=None,
            fbnotify=None,
            **_kwargs,
        ):
            return LabState(
                phase=phase,
                active=active,
                created_at="2026-04-10T00:00:00+00:00",
                work_dir=str(workdir),
                namespaces={
                    "fbnotify": NamespaceInfo(pid=11, parent="hub", role="fbnotify"),
                    "fbcoord": NamespaceInfo(pid=12, parent="hub", role="fbcoord"),
                    "node-1": NamespaceInfo(pid=13, parent="hub", role="node"),
                    "node-2": NamespaceInfo(pid=14, parent="hub", role="node"),
                },
                processes=processes or {},
                proxies=proxies or {},
                terminals=terminals or {},
                node_features=node_features or {},
                tokens=tokens or TokenInfo(),
                fbnotify=fbnotify or FBNotifyInfo(),
            )

        topology = mock.Mock(
            namespaces={
                "fbnotify": mock.Mock(pid=11),
                "fbcoord": mock.Mock(pid=12),
                "node-1": mock.Mock(pid=13),
                "node-2": mock.Mock(pid=14),
                "upstream-1": mock.Mock(pid=15),
                "upstream-2": mock.Mock(pid=16),
            },
            clients={},
        )

        generated_tokens = mock.Mock(
            tokens=TokenInfo(control_token="control-token", operator_token="operator-token", node_tokens={}),
            operator_pepper="operator-pepper",
        )
        emitters = {
            "node-1": FBNotifyEmitterInfo(
                key_id="node-1-key",
                token="node-1-secret",
                source_service="fbforward",
                source_instance="node-1",
            ),
            "node-2": FBNotifyEmitterInfo(
                key_id="node-2-key",
                token="node-2-secret",
                source_service="fbforward",
                source_instance="node-2",
            ),
            "fbcoord": FBNotifyEmitterInfo(
                key_id="fbcoord-key",
                token="fbcoord-secret",
                source_service="fbcoord",
                source_instance="fbcoord",
            ),
        }

        with ExitStack() as stack:
            stack.enter_context(mock.patch("cli.lifecycle.load_state", side_effect=fake_load_state))
            stack.enter_context(mock.patch("cli.lifecycle.save_state", side_effect=fake_save_state))
            stack.enter_context(mock.patch("cli.lifecycle.parse_client_specs", return_value={}))
            stack.enter_context(mock.patch("cli.lifecycle.require_tools"))
            stack.enter_context(mock.patch("cli.lifecycle.assert_host_ports_available"))
            stack.enter_context(mock.patch("cli.lifecycle.ensure_fbforward_binaries"))
            stack.enter_context(mock.patch("cli.lifecycle.ensure_fbcoord_assets"))
            stack.enter_context(mock.patch("cli.lifecycle.ensure_geoip_mmdbs"))
            stack.enter_context(mock.patch("cli.lifecycle.build_node_feature_summary", return_value={}))
            stack.enter_context(mock.patch("cli.lifecycle.netns.build_topology", return_value=topology))
            stack.enter_context(mock.patch("cli.lifecycle.netns.verify_connectivity"))
            stack.enter_context(mock.patch("cli.lifecycle.netns.destroy_topology"))
            stack.enter_context(mock.patch("cli.lifecycle.build_state", side_effect=fake_build_state))
            stack.enter_context(mock.patch("cli.lifecycle.coordconfig.generate_tokens", return_value=generated_tokens))
            stack.enter_context(mock.patch("cli.lifecycle.ProcessManager", FakeProcessManager))
            stack.enter_context(mock.patch("cli.lifecycle.ensure_fbnotify_assets"))
            stack.enter_context(
                mock.patch("cli.lifecycle.coordconfig.prepare_fbnotify_runtime", return_value=workdir / "fbnotify-runtime")
            )
            stack.enter_context(mock.patch("cli.lifecycle.verify_fbnotify_health_in_namespace"))
            stack.enter_context(mock.patch("cli.lifecycle.bootstrap_fbnotify", return_value=emitters))
            prepare_fbcoord_runtime = stack.enter_context(
                mock.patch("cli.lifecycle.coordconfig.prepare_fbcoord_runtime", return_value=workdir / "fbcoord-runtime")
            )
            stack.enter_context(mock.patch("cli.lifecycle.verify_fbcoord_health_in_namespace"))
            stack.enter_context(
                mock.patch(
                    "cli.lifecycle.mint_fbcoord_node_tokens",
                    return_value={"node-1": "node-token-1", "node-2": "node-token-2"},
                )
            )
            stack.enter_context(
                mock.patch(
                    "cli.lifecycle.coordconfig.generate_fbforward_config",
                    side_effect=[workdir / "node-1.yml", workdir / "node-2.yml"],
                )
            )
            stack.enter_context(mock.patch("cli.lifecycle.validate_fbforward_config"))
            stack.enter_context(mock.patch("cli.lifecycle.verify_fbforward_rpc_in_namespace"))
            stack.enter_context(mock.patch("cli.lifecycle.fbnotify_namespace_base_url", return_value="http://10.99.0.30:8787"))
            stack.enter_context(
                mock.patch("cli.lifecycle.fbnotify_ingest_url", return_value="http://10.99.0.30:8787/v1/events")
            )
            stack.enter_context(mock.patch("cli.lifecycle.fbcoord_namespace_base_url", return_value="http://10.99.0.10:8787"))
            stack.enter_context(
                mock.patch("cli.lifecycle.readiness.verify_fbnotify_public", side_effect=RuntimeError("public proxy failed"))
            )
            stack.enter_context(mock.patch("cli.lifecycle.readiness.wait_http_ok"))
            stack.enter_context(
                mock.patch(
                    "cli.lifecycle.readiness.wait_for_status",
                    return_value={"mode": "coordination", "coordination": {"connected": True}},
                )
            )
            stack.enter_context(mock.patch("cli.lifecycle.apply_coordination_mode"))
            stack.enter_context(mock.patch("cli.lifecycle.readiness.verify_fbcoord_api"))
            stack.enter_context(
                mock.patch(
                    "cli.lifecycle.readiness.verify_fbcoord_notify_config",
                    return_value={
                        "configured": True,
                        "source": "bootstrap-env",
                        "endpoint": "http://10.99.0.30:8787/v1/events",
                        "key_id": "fbcoord-key",
                        "source_instance": "fbcoord",
                        "masked_prefix": "fbcoord-...",
                        "updated_at": 1234,
                    },
                )
            )
            stack.enter_context(mock.patch("cli.lifecycle.start_ttyd_terminals", return_value={}))
            stack.enter_context(mock.patch("cli.lifecycle.render_summary", return_value="ok"))
            with io.StringIO() as buffer, redirect_stdout(buffer):
                self.assertEqual(0, cmd_up(args))
                output = buffer.getvalue()

        self.assertGreaterEqual(len(saved_states), 3)
        final_state = saved_states[-1]
        self.assertFalse(final_state.fbnotify.available)
        self.assertEqual("public proxy failed", final_state.fbnotify.error)
        self.assertNotIn("fbnotify", final_state.proxies)
        self.assertIn("fbcoord", final_state.proxies)
        self.assertIn(f"runtime={workdir / 'fbcoord-runtime'}", output)
        self.assertIn("FBNOTIFY_URL=set", output)
        self.assertIn("FBNOTIFY_KEY_ID=set", output)
        self.assertIn("FBNOTIFY_TOKEN=set", output)
        self.assertIn("FBNOTIFY_SOURCE_INSTANCE=set", output)
        self.assertTrue(final_state.fbnotify.fbcoord_notify.verified)
        self.assertEqual("fbcoord-key", final_state.fbnotify.fbcoord_notify.key_id)
        prepare_fbcoord_runtime.assert_called_once()
        self.assertEqual(workdir, prepare_fbcoord_runtime.call_args.args[0])
        self.assertEqual("operator-token", prepare_fbcoord_runtime.call_args.args[1])
        self.assertEqual("operator-pepper", prepare_fbcoord_runtime.call_args.args[2])
        notify_bootstrap = prepare_fbcoord_runtime.call_args.kwargs["fbnotify"]
        self.assertEqual("http://10.99.0.30:8787/v1/events", notify_bootstrap.endpoint)
        self.assertEqual("fbcoord-key", notify_bootstrap.key_id)
        self.assertEqual("fbcoord-secret", notify_bootstrap.token)
        self.assertEqual("fbcoord", notify_bootstrap.source_instance)

    def test_cmd_up_omits_fbcoord_notify_bootstrap_when_fbnotify_setup_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            workdir = Path(tmpdir)
            args = argparse.Namespace(workdir=str(workdir), skip_build=False, skip_connectivity_check=False, client=[])
            saved_states: list[LabState] = []

            class FakeProcessManager:
                def __init__(self, _logs_dir):
                    self._infos: dict[str, ProcessInfo] = {}

                def start(self, _ns_pid, ns_name, _cmd, name, **_kwargs):
                    self._infos[name] = ProcessInfo(
                        pid=1000 + len(self._infos),
                        ns=ns_name,
                        log_path=f"/tmp/{name}.log",
                        order=len(self._infos),
                    )
                    return mock.Mock(pid=self._infos[name].pid)

                def start_host(self, _cmd, name, **_kwargs):
                    self._infos[name] = ProcessInfo(
                        pid=2000 + len(self._infos),
                        ns="host",
                        log_path=f"/tmp/{name}.log",
                        order=len(self._infos),
                    )
                    return mock.Mock(pid=self._infos[name].pid)

                def stop(self, name, timeout_sec=5.0):
                    self._infos.pop(name, None)

                def stop_all(self, timeout_sec=5.0):
                    self._infos.clear()

                def infos(self):
                    return dict(self._infos)

            def fake_load_state(_path: Path):
                if not saved_states:
                    return None
                return saved_states[-1]

            def fake_save_state(_path: Path, state: LabState) -> None:
                saved_states.append(state)

            def fake_build_state(workdir_arg, topology_arg, phase, **kwargs) -> LabState:
                return LabState(
                    phase=5,
                    active=True,
                    created_at="2026-04-10T00:00:00+00:00",
                    work_dir=str(workdir_arg),
                    namespaces={},
                    processes=kwargs["processes"],
                    proxies=kwargs["proxies"],
                    terminals=kwargs.get("terminals", {}),
                    node_features={},
                    tokens=kwargs["tokens"],
                    fbnotify=kwargs["fbnotify"],
                    topology=topology_arg,
                    clients={},
                )

            topology = mock.Mock(
                namespaces={
                    "fbnotify": NamespaceInfo(pid=30, parent="hub", role="fbnotify"),
                    "fbcoord": NamespaceInfo(pid=10, parent="hub", role="fbcoord"),
                    "node-1": NamespaceInfo(pid=11, parent="hub", role="node"),
                    "node-2": NamespaceInfo(pid=12, parent="hub", role="node"),
                    "upstream-1": NamespaceInfo(pid=20, parent="hub-up", role="upstream"),
                    "upstream-2": NamespaceInfo(pid=21, parent="hub-up", role="upstream"),
                },
                links=[],
                clients={},
            )

            generated_tokens = mock.Mock(
                tokens=TokenInfo(control_token="control-token", operator_token="operator-token", node_tokens={}),
                operator_pepper="operator-pepper",
            )

            with ExitStack() as stack:
                stack.enter_context(mock.patch("cli.lifecycle.load_state", side_effect=fake_load_state))
                stack.enter_context(mock.patch("cli.lifecycle.save_state", side_effect=fake_save_state))
                stack.enter_context(mock.patch("cli.lifecycle.parse_client_specs", return_value={}))
                stack.enter_context(mock.patch("cli.lifecycle.require_tools"))
                stack.enter_context(mock.patch("cli.lifecycle.assert_host_ports_available"))
                stack.enter_context(mock.patch("cli.lifecycle.ensure_fbforward_binaries"))
                stack.enter_context(mock.patch("cli.lifecycle.ensure_fbcoord_assets"))
                stack.enter_context(mock.patch("cli.lifecycle.ensure_geoip_mmdbs"))
                stack.enter_context(mock.patch("cli.lifecycle.build_node_feature_summary", return_value={}))
                stack.enter_context(mock.patch("cli.lifecycle.netns.build_topology", return_value=topology))
                stack.enter_context(mock.patch("cli.lifecycle.netns.verify_connectivity"))
                stack.enter_context(mock.patch("cli.lifecycle.netns.destroy_topology"))
                stack.enter_context(mock.patch("cli.lifecycle.build_state", side_effect=fake_build_state))
                stack.enter_context(mock.patch("cli.lifecycle.coordconfig.generate_tokens", return_value=generated_tokens))
                stack.enter_context(mock.patch("cli.lifecycle.ProcessManager", FakeProcessManager))
                stack.enter_context(
                    mock.patch("cli.lifecycle.ensure_fbnotify_assets", side_effect=RuntimeError("fbnotify build failed"))
                )
                prepare_fbcoord_runtime = stack.enter_context(
                    mock.patch("cli.lifecycle.coordconfig.prepare_fbcoord_runtime", return_value=workdir / "fbcoord-runtime")
                )
                stack.enter_context(mock.patch("cli.lifecycle.verify_fbcoord_health_in_namespace"))
                stack.enter_context(
                    mock.patch(
                        "cli.lifecycle.mint_fbcoord_node_tokens",
                        return_value={"node-1": "node-token-1", "node-2": "node-token-2"},
                    )
                )
                stack.enter_context(
                    mock.patch(
                        "cli.lifecycle.coordconfig.generate_fbforward_config",
                        side_effect=[workdir / "node-1.yml", workdir / "node-2.yml"],
                    )
                )
                stack.enter_context(mock.patch("cli.lifecycle.validate_fbforward_config"))
                stack.enter_context(mock.patch("cli.lifecycle.verify_fbforward_rpc_in_namespace"))
                stack.enter_context(mock.patch("cli.lifecycle.fbnotify_namespace_base_url", return_value="http://10.99.0.30:8787"))
                stack.enter_context(
                    mock.patch("cli.lifecycle.fbnotify_ingest_url", return_value="http://10.99.0.30:8787/v1/events")
                )
                stack.enter_context(mock.patch("cli.lifecycle.fbcoord_namespace_base_url", return_value="http://10.99.0.10:8787"))
                stack.enter_context(mock.patch("cli.lifecycle.readiness.wait_http_ok"))
                stack.enter_context(
                    mock.patch(
                        "cli.lifecycle.readiness.wait_for_status",
                        return_value={"mode": "coordination", "coordination": {"connected": True}},
                    )
                )
                stack.enter_context(mock.patch("cli.lifecycle.apply_coordination_mode"))
                stack.enter_context(mock.patch("cli.lifecycle.readiness.verify_fbcoord_api"))
                stack.enter_context(mock.patch("cli.lifecycle.start_ttyd_terminals", return_value={}))
                stack.enter_context(mock.patch("cli.lifecycle.render_summary", return_value="ok"))
                with io.StringIO() as buffer, redirect_stdout(buffer):
                    self.assertEqual(0, cmd_up(args))
                    output = buffer.getvalue()

            self.assertGreaterEqual(len(saved_states), 1)
            final_state = saved_states[-1]
            self.assertFalse(final_state.fbnotify.available)
            self.assertEqual("fbnotify build failed", final_state.fbnotify.error)
            prepare_fbcoord_runtime.assert_called_once()
            self.assertIsNone(prepare_fbcoord_runtime.call_args.kwargs["fbnotify"])
            self.assertIn("FBNOTIFY_URL=unset", output)
            self.assertIn("FBNOTIFY_KEY_ID=unset", output)
            self.assertIn("FBNOTIFY_TOKEN=unset", output)
            self.assertIn("FBNOTIFY_SOURCE_INSTANCE=unset", output)


if __name__ == "__main__":
    unittest.main()
