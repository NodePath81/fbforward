from __future__ import annotations

import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.netns import DEFAULT_BASE_CIDR, allocate_subnets, default_links
from lib.netns import compute_link_order, compute_namespace_order


class NetnsHelpersTest(unittest.TestCase):
    def test_allocate_subnets_returns_requested_count(self) -> None:
        subnets = allocate_subnets(DEFAULT_BASE_CIDR, 7)
        self.assertEqual(7, len(subnets))
        self.assertEqual("10.99.0.0/30", str(subnets[0]))
        self.assertEqual("10.99.0.24/30", str(subnets[-1]))

    def test_default_links_follow_expected_order(self) -> None:
        links = default_links()
        pairs = [(link.left_ns, link.right_ns, link.left_if, link.right_if) for link in links]
        self.assertEqual(
            [
                ("hub", "fbcoord", "hub-fbcoord", "fbcoord-peer"),
                ("hub", "node-1", "hub-node1", "node1-peer"),
                ("hub", "node-2", "hub-node2", "node2-peer"),
                ("hub", "internet", "hub-inet", "inet-hub"),
                ("internet", "hub-up", "inet-hubup", "hubup-inet"),
                ("hub-up", "upstream-1", "hubup-u1", "upstream1-peer"),
                ("hub-up", "upstream-2", "hubup-u2", "upstream2-peer"),
            ],
            pairs,
        )

    def test_compute_namespace_order_appends_client_edge_and_clients(self) -> None:
        order = compute_namespace_order(["client-2", "client-1"])
        self.assertEqual(("client-edge", "hub", "client-edge"), order[-3])
        self.assertEqual(("client-1", "client-edge", "client"), order[-2])
        self.assertEqual(("client-2", "client-edge", "client"), order[-1])

    def test_compute_link_order_appends_client_links(self) -> None:
        order = compute_link_order(["client-2", "client-1"])
        self.assertEqual(("internet", "client-edge", "inet-cedge", "cedge-inet"), order[-3])
        self.assertEqual(("client-edge", "client-1", "cedge-c1", "c1-peer"), order[-2])
        self.assertEqual(("client-edge", "client-2", "cedge-c2", "c2-peer"), order[-1])

    def test_default_links_expands_for_clients(self) -> None:
        links = default_links(client_names=["client-2", "client-1"])
        pairs = [(link.left_ns, link.right_ns, link.left_if, link.right_if) for link in links[-3:]]
        self.assertEqual(
            [
                ("internet", "client-edge", "inet-cedge", "cedge-inet"),
                ("client-edge", "client-1", "cedge-c1", "c1-peer"),
                ("client-edge", "client-2", "cedge-c2", "c2-peer"),
            ],
            pairs,
        )


if __name__ == "__main__":
    unittest.main()
