from __future__ import annotations

import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from unittest import mock

from coordlab import (
    TTYD_BASE_PORT,
    allocate_live_ttyd_port,
    allocate_ttyd_ports,
    assert_host_ports_available,
    build_ttyd_command,
    ensure_fbforward_binaries,
    fixed_proxy_bindings,
    parse_client_specs,
)
from lib.state import TerminalInfo


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
            mock.patch("coordlab.require_tools") as require_tools,
            mock.patch("coordlab.run_host") as run_host,
            mock.patch("pathlib.Path.exists", return_value=True),
        ):
            ensure_fbforward_binaries(skip_build=False)

        require_tools.assert_called_once_with(["make"])
        run_host.assert_called_once()

    def test_assert_host_ports_available_checks_proxy_and_extra_bindings(self) -> None:
        extra = [("ttyd-client-2", "127.0.0.1", TTYD_BASE_PORT)]
        with mock.patch("coordlab.assert_bindings_available") as assert_bindings_available:
            assert_host_ports_available(extra_bindings=extra)

        assert_bindings_available.assert_called_once()
        bindings = assert_bindings_available.call_args.args[0]
        self.assertEqual([*fixed_proxy_bindings(), *extra], bindings)


if __name__ == "__main__":
    unittest.main()
