from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.clients import add_client, remove_client
from lib.netns import default_links
from lib.state import (
    ClientInfo,
    LabState,
    LinkInfo,
    NamespaceInfo,
    ProcessInfo,
    ShapingInfo,
    TokenInfo,
    TopologyInfo,
)


def sample_state(workdir: Path, *, with_client: bool) -> LabState:
    namespaces = {
        "hub": NamespaceInfo(pid=100, parent=None, role="hub"),
        "hub-up": NamespaceInfo(pid=101, parent="hub", role="hub-up"),
        "internet": NamespaceInfo(pid=102, parent="hub", role="internet"),
        "fbcoord": NamespaceInfo(pid=103, parent="hub", role="fbcoord"),
        "node-1": NamespaceInfo(pid=104, parent="hub", role="node"),
        "node-2": NamespaceInfo(pid=105, parent="hub", role="node"),
        "upstream-1": NamespaceInfo(pid=106, parent="hub-up", role="upstream"),
        "upstream-2": NamespaceInfo(pid=107, parent="hub-up", role="upstream"),
    }
    clients: dict[str, ClientInfo] = {}
    processes: dict[str, ProcessInfo] = {
        "fbcoord": ProcessInfo(pid=200, ns="fbcoord", log_path=str(workdir / "fbcoord.log"), order=1),
    }
    links = [
        LinkInfo(
            left_ns=link.left_ns,
            right_ns=link.right_ns,
            left_if=link.left_if,
            right_if=link.right_if,
            subnet=link.subnet,
            left_ip=link.left_ip,
            right_ip=link.right_ip,
        )
        for link in default_links()
    ]
    next_subnet_index = len(links)

    if with_client:
        namespaces["client-edge"] = NamespaceInfo(pid=108, parent="hub", role="client-edge")
        namespaces["client-1"] = NamespaceInfo(pid=109, parent="client-edge", role="client")
        clients["client-1"] = ClientInfo(identity_ip="198.51.100.10")
        processes["ttyd-client-1"] = ProcessInfo(pid=300, ns="host", log_path=str(workdir / "ttyd-client-1.log"), order=2)
        processes["ttyd-upstream-1"] = ProcessInfo(pid=301, ns="host", log_path=str(workdir / "ttyd-upstream-1.log"), order=3)
        for link in default_links(client_names=["client-1"])[-2:]:
            links.append(
                LinkInfo(
                    left_ns=link.left_ns,
                    right_ns=link.right_ns,
                    left_if=link.left_if,
                    right_if=link.right_if,
                    subnet=link.subnet,
                    left_ip=link.left_ip,
                    right_ip=link.right_ip,
                )
            )
        next_subnet_index = len(default_links(client_names=["client-1"]))

    return LabState(
        phase=5,
        active=True,
        created_at="2026-04-08T00:00:00+00:00",
        work_dir=str(workdir),
        namespaces=namespaces,
        processes=processes,
        clients=clients,
        terminals={},
        shaping=ShapingInfo(),
        tokens=TokenInfo(
            control_token="control",
            operator_token="operator",
            node_tokens={"node-1": "node-1-token", "node-2": "node-2-token"},
        ),
        topology=TopologyInfo(base_cidr="10.99.0.0/24", links=links, next_subnet_index=next_subnet_index),
    )


class LiveClientMutationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.workdir = Path(self.tempdir.name)

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_add_client_creates_client_edge_when_missing(self) -> None:
        state = sample_state(self.workdir, with_client=False)
        with (
            mock.patch("lib.clients.assert_bindings_available") as assert_bindings_available,
            mock.patch("lib.clients.start_terminal_process", return_value=(401, str(self.workdir / "logs/ttyd-client-2.log"))),
            mock.patch("lib.clients.save_current_state"),
            mock.patch("lib.clients.netns.create_client_edge", return_value=(
                mock.Mock(name="client-edge", pid=210, parent="hub", role="client-edge"),
                mock.Mock(
                    left_ns="internet",
                    right_ns="client-edge",
                    left_if="inet-cedge",
                    right_if="cedge-inet",
                    subnet="10.99.0.28/30",
                    left_ip="10.99.0.29",
                    right_ip="10.99.0.30",
                ),
                8,
            )),
            mock.patch("lib.clients.netns.create_client_namespace", return_value=(
                mock.Mock(name="client-2", pid=211, parent="client-edge", role="client"),
                mock.Mock(
                    left_ns="client-edge",
                    right_ns="client-2",
                    left_if="cedge-c1",
                    right_if="c1-peer",
                    subnet="10.99.0.32/30",
                    left_ip="10.99.0.33",
                    right_ip="10.99.0.34",
                ),
                9,
            )),
            mock.patch("lib.clients.netns.verify_connectivity") as verify_connectivity,
        ):
            updated = add_client(state, "client-2", "203.0.113.20")

        self.assertIn("client-edge", updated.namespaces)
        self.assertIn("client-2", updated.namespaces)
        self.assertEqual("203.0.113.20", updated.clients["client-2"].identity_ip)
        self.assertEqual(18900, updated.terminals["client-2"].host_port)
        self.assertIn("ttyd-client-2", updated.processes)
        self.assertEqual(9, updated.topology.next_subnet_index)
        self.assertEqual("10.99.0.28/30", updated.topology.links[-2].subnet)
        self.assertEqual("10.99.0.32/30", updated.topology.links[-1].subnet)
        assert_bindings_available.assert_called_once_with(
            [("ttyd-client-2", "127.0.0.1", 18900)],
            error_prefix="coordlab ttyd ports are already in use",
        )
        verify_connectivity.assert_called_once()

    def test_add_client_skips_connectivity_check_when_requested(self) -> None:
        state = sample_state(self.workdir, with_client=False)
        with (
            mock.patch("lib.clients.assert_bindings_available"),
            mock.patch("lib.clients.start_terminal_process", return_value=(401, str(self.workdir / "logs/ttyd-client-2.log"))),
            mock.patch("lib.clients.save_current_state"),
            mock.patch("lib.clients.netns.create_client_edge", return_value=(
                mock.Mock(name="client-edge", pid=210, parent="hub", role="client-edge"),
                mock.Mock(
                    left_ns="internet",
                    right_ns="client-edge",
                    left_if="inet-cedge",
                    right_if="cedge-inet",
                    subnet="10.99.0.28/30",
                    left_ip="10.99.0.29",
                    right_ip="10.99.0.30",
                ),
                8,
            )),
            mock.patch("lib.clients.netns.create_client_namespace", return_value=(
                mock.Mock(name="client-2", pid=211, parent="client-edge", role="client"),
                mock.Mock(
                    left_ns="client-edge",
                    right_ns="client-2",
                    left_if="cedge-c1",
                    right_if="c1-peer",
                    subnet="10.99.0.32/30",
                    left_ip="10.99.0.33",
                    right_ip="10.99.0.34",
                ),
                9,
            )),
            mock.patch("lib.clients.netns.verify_connectivity") as verify_connectivity,
        ):
            add_client(state, "client-2", "203.0.113.20", skip_connectivity_check=True)

        verify_connectivity.assert_not_called()

    def test_add_client_reports_only_ttyd_port_conflict(self) -> None:
        state = sample_state(self.workdir, with_client=False)
        with mock.patch(
            "lib.clients.assert_bindings_available",
            side_effect=RuntimeError("coordlab ttyd ports are already in use: ttyd-client-2:127.0.0.1:18900"),
        ):
            with self.assertRaises(RuntimeError) as ctx:
                add_client(state, "client-2", "203.0.113.20")

        self.assertIn("ttyd-client-2:127.0.0.1:18900", str(ctx.exception))
        self.assertNotIn("fbcoord", str(ctx.exception))

    def test_remove_client_stops_terminal_and_preserves_client_edge(self) -> None:
        state = sample_state(self.workdir, with_client=True)
        state.terminals["client-1"] = mock.Mock(host_port=18900, pid=300)
        with (
            mock.patch("lib.clients.save_current_state"),
            mock.patch("lib.clients.terminate_process_group") as terminate_process_group,
            mock.patch("lib.clients.netns.remove_client_namespace"),
        ):
            updated = remove_client(state, "client-1")

        terminate_process_group.assert_called_once_with(300, timeout_sec=5)
        self.assertIn("client-edge", updated.namespaces)
        self.assertNotIn("client-1", updated.namespaces)
        self.assertNotIn("client-1", updated.clients)
        self.assertNotIn("client-1", updated.terminals)
        self.assertNotIn("ttyd-client-1", updated.processes)
        self.assertEqual(10, updated.topology.next_subnet_index)


if __name__ == "__main__":
    unittest.main()
