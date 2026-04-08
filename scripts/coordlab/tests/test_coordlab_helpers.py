from __future__ import annotations

import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from coordlab import TTYD_BASE_PORT, allocate_ttyd_ports, build_ttyd_command, parse_client_specs


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
        command = build_ttyd_command(ns_pid=4242, port=TTYD_BASE_PORT)
        self.assertEqual("ttyd", command[0])
        self.assertIn("--port", command)
        self.assertIn(str(TTYD_BASE_PORT), command)
        self.assertIn("nsenter", command)
        self.assertIn("4242", command)
        self.assertEqual(["/bin/bash", "-l"], command[-2:])


if __name__ == "__main__":
    unittest.main()
