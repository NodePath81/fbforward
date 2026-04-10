from __future__ import annotations

import sys
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib.output import render_summary
from lib.state import (
    ClientInfo,
    FBNotifyEmitterInfo,
    FBNotifyInfo,
    FirewallFeatureInfo,
    FirewallRuleInfo,
    GeoIPFeatureInfo,
    IPLogFeatureInfo,
    LabState,
    NamespaceInfo,
    NodeFeatureInfo,
    ProcessInfo,
    ProxyInfo,
    TerminalInfo,
    TokenInfo,
    TopologyInfo,
)


class OutputSummaryTest(unittest.TestCase):
    def test_render_summary_includes_proxy_urls_and_commands(self) -> None:
        state = LabState(
            phase=3,
            active=True,
            created_at="2026-04-05T00:00:00+00:00",
            work_dir="/tmp/coordlab-phase3",
            namespaces={"node-1": NamespaceInfo(pid=100, parent="hub", role="node")},
            processes={
                "coordlab-proxy": ProcessInfo(pid=300, ns="host", log_path="/tmp/proxy.log", order=5),
                "fbforward-node-1": ProcessInfo(pid=301, ns="node-1", log_path="/tmp/node1.log", order=3),
            },
            proxies={
                "fbcoord": ProxyInfo(
                    listen_host="127.0.0.1",
                    host_port=18700,
                    target_ns="fbcoord",
                    target_host="127.0.0.1",
                    target_port=8787,
                ),
                "fbnotify": ProxyInfo(
                    listen_host="127.0.0.1",
                    host_port=18703,
                    target_ns="fbnotify",
                    target_host="127.0.0.1",
                    target_port=8787,
                ),
                "node-1": ProxyInfo(
                    listen_host="127.0.0.1",
                    host_port=18701,
                    target_ns="node-1",
                    target_host="127.0.0.1",
                    target_port=8080,
                ),
            },
            clients={
                "client-1": ClientInfo(identity_ip="198.51.100.10"),
            },
            terminals={
                "client-1": TerminalInfo(host_port=18900, pid=401),
                "upstream-1": TerminalInfo(host_port=18901, pid=402),
            },
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
                    firewall=FirewallFeatureInfo(
                        enabled=True,
                        default_policy="allow",
                        rules=[
                            FirewallRuleInfo(action="deny", cidr="198.51.100.0/24"),
                            FirewallRuleInfo(action="deny", asn=15169),
                        ],
                    ),
                ),
            },
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
            topology=TopologyInfo(base_cidr="10.99.0.0/24"),
        )

        with mock.patch("lib.output.is_alive", return_value=True):
            summary = render_summary(state, "/repo/.venv/bin/python")

        self.assertIn("http://127.0.0.1:18700", summary)
        self.assertIn("http://127.0.0.1:18703", summary)
        self.assertIn("http://127.0.0.1:18701", summary)
        self.assertIn("/repo/.venv/bin/python", summary)
        self.assertIn("coordlab-proxy: alive", summary)
        self.assertIn("client-1", summary)
        self.assertIn("198.51.100.10", summary)
        self.assertIn("http://127.0.0.1:18900", summary)
        self.assertIn("http://127.0.0.1:18901", summary)
        self.assertIn("geoip: enabled", summary)
        self.assertIn("/tmp/coordlab-phase3/mmdb/GeoLite2-ASN.mmdb", summary)
        self.assertIn("ip_log: enabled", summary)
        self.assertIn("/tmp/coordlab-phase3/data/node-1-iplog.sqlite", summary)
        self.assertIn("firewall: enabled default=allow", summary)
        self.assertIn("operator:", summary)
        self.assertIn("node[node-1]:", summary)
        self.assertIn("fbnotify:", summary)
        self.assertIn("/tmp/coordlab-phase3/mmdb", summary)
        self.assertIn("/tmp/coordlab-phase3/data", summary)
        self.assertIn(" web --workdir ", summary)
        self.assertNotIn("fbnotify-secret-node-1", summary)
        self.assertNotIn("fbnotify-operator-token", summary)


if __name__ == "__main__":
    unittest.main()
