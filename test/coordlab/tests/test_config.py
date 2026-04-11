from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib import config as coordconfig
from lib.netns import Namespace, Topology, default_links
from lib.state import FirewallRuleInfo


def fake_topology(tmpdir: str) -> Topology:
    namespaces = {
        "hub": Namespace(name="hub", pid=101, parent=None, role="hub"),
        "hub-up": Namespace(name="hub-up", pid=102, parent="hub", role="hub-up"),
        "fbcoord": Namespace(name="fbcoord", pid=103, parent="hub", role="fbcoord"),
        "fbnotify": Namespace(name="fbnotify", pid=109, parent="hub", role="fbnotify"),
        "node-1": Namespace(name="node-1", pid=104, parent="hub", role="node"),
        "node-2": Namespace(name="node-2", pid=105, parent="hub", role="node"),
        "upstream-1": Namespace(name="upstream-1", pid=106, parent="hub-up", role="upstream"),
        "upstream-2": Namespace(name="upstream-2", pid=107, parent="hub-up", role="upstream"),
        "internet": Namespace(name="internet", pid=108, parent="hub", role="internet"),
    }
    return Topology(work_dir=tmpdir, namespaces=namespaces, links=default_links(), base_cidr="10.99.0.0/24")


class ConfigHelpersTest(unittest.TestCase):
    def test_generate_tokens_returns_hex_values(self) -> None:
        generated = coordconfig.generate_tokens()
        self.assertEqual(64, len(generated.tokens.control_token))
        self.assertEqual(64, len(generated.tokens.operator_token))
        self.assertEqual({}, generated.tokens.node_tokens)
        self.assertEqual(64, len(generated.operator_pepper))
        int(generated.tokens.control_token, 16)
        int(generated.tokens.operator_token, 16)
        int(generated.operator_pepper, 16)

    def test_generate_fbforward_config_contains_expected_service_values(self) -> None:
        generated = coordconfig.generate_tokens()
        generated.tokens.node_tokens["node-1"] = "node-1-token"
        with tempfile.TemporaryDirectory() as tmpdir:
            topology = fake_topology(tmpdir)
            config_path = coordconfig.generate_fbforward_config("node-1", topology, generated.tokens, tmpdir)
            rendered = config_path.read_text(encoding="utf-8")
            self.assertTrue((Path(tmpdir) / coordconfig.DATA_DIRNAME).exists())

        self.assertIn("hostname: node-1", rendered)
        self.assertIn('auth_token: "', rendered)
        self.assertIn("endpoint: http://10.99.0.2:8787", rendered)
        self.assertIn("host: 10.99.0.22", rendered)
        self.assertIn("host: 10.99.0.26", rendered)
        self.assertIn("heartbeat_interval: 10s", rendered)
        self.assertIn('token: "node-1-token"', rendered)
        self.assertNotIn("pool:", rendered)
        self.assertNotIn("node_id:", rendered)
        self.assertIn("geoip:", rendered)
        self.assertIn(f'asn_db_url: "{coordconfig.GEOIP_ASN_DB_URL}"', rendered)
        self.assertIn(f'country_db_url: "{coordconfig.GEOIP_COUNTRY_DB_URL}"', rendered)
        self.assertIn(f'asn_db_path: "{Path(tmpdir) / coordconfig.MMDB_DIRNAME / coordconfig.GEOIP_ASN_DB_FILENAME}"', rendered)
        self.assertIn(
            f'country_db_path: "{Path(tmpdir) / coordconfig.MMDB_DIRNAME / coordconfig.GEOIP_COUNTRY_DB_FILENAME}"',
            rendered,
        )
        self.assertIn("refresh_interval: 24h", rendered)
        self.assertIn("ip_log:", rendered)
        self.assertIn(f'db_path: "{Path(tmpdir) / coordconfig.DATA_DIRNAME / "node-1-iplog.sqlite"}"', rendered)
        self.assertIn("retention: 24h", rendered)
        self.assertIn("geo_queue_size: 128", rendered)
        self.assertIn("write_queue_size: 128", rendered)
        self.assertIn("batch_size: 10", rendered)
        self.assertIn("flush_interval: 2s", rendered)
        self.assertIn("prune_interval: 1h", rendered)
        self.assertIn("firewall:", rendered)
        self.assertIn("default: allow", rendered)
        self.assertIn("cidr: 198.51.100.0/24", rendered)
        self.assertIn("asn: 15169", rendered)
        self.assertIn("country: AU", rendered)

    def test_generate_fbforward_config_includes_notify_block_when_provided(self) -> None:
        generated = coordconfig.generate_tokens()
        generated.tokens.node_tokens["node-1"] = "node-1-token"
        with tempfile.TemporaryDirectory() as tmpdir:
            topology = fake_topology(tmpdir)
            config_path = coordconfig.generate_fbforward_config(
                "node-1",
                topology,
                generated.tokens,
                tmpdir,
                fbnotify=coordconfig.FBNotifyNodeConfig(
                    endpoint="http://10.99.0.30:8787/v1/events",
                    key_id="notify-key-1",
                    token="notify-token-1",
                    source_instance="node-1",
                ),
            )
            rendered = config_path.read_text(encoding="utf-8")

        self.assertIn("notify:", rendered)
        self.assertIn("enabled: true", rendered)
        self.assertIn("endpoint: http://10.99.0.30:8787/v1/events", rendered)
        self.assertIn('key_id: "notify-key-1"', rendered)
        self.assertIn('token: "notify-token-1"', rendered)
        self.assertIn("source_instance: node-1", rendered)

    def test_build_node_feature_info_uses_per_node_db_path_and_curated_firewall_rules(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            node1 = coordconfig.build_node_feature_info("node-1", tmpdir)
            node2 = coordconfig.build_node_feature_info("node-2", tmpdir)

        self.assertTrue(node1.geoip.enabled)
        self.assertEqual(coordconfig.GEOIP_ASN_DB_URL, node1.geoip.asn_db_url)
        self.assertEqual(coordconfig.GEOIP_COUNTRY_DB_URL, node1.geoip.country_db_url)
        self.assertTrue(node1.ip_log.enabled)
        self.assertEqual(str(Path(tmpdir) / coordconfig.DATA_DIRNAME / "node-1-iplog.sqlite"), node1.ip_log.db_path)
        self.assertNotEqual(node1.ip_log.db_path, node2.ip_log.db_path)
        self.assertEqual("allow", node1.firewall.default_policy)
        self.assertEqual(
            [
                FirewallRuleInfo(action="deny", cidr="198.51.100.0/24"),
                FirewallRuleInfo(action="deny", asn=15169),
                FirewallRuleInfo(action="deny", country="AU"),
            ],
            node1.firewall.rules,
        )

    def test_prepare_fbcoord_runtime_writes_dev_vars_and_links_node_modules(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            runtime_dir = coordconfig.prepare_fbcoord_runtime(tmpdir, "operator-token", "operator-pepper")
            self.assertTrue((runtime_dir / ".dev.vars").exists())
            self.assertEqual(
                "FBCOORD_TOKEN=operator-token\nFBCOORD_TOKEN_PEPPER=operator-pepper\n",
                (runtime_dir / ".dev.vars").read_text(encoding="utf-8"),
            )
            self.assertTrue((runtime_dir / "src/worker.ts").exists())
            self.assertTrue((runtime_dir / "node_modules").is_symlink())

    def test_prepare_fbcoord_runtime_writes_notify_bootstrap_vars_when_present(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            runtime_dir = coordconfig.prepare_fbcoord_runtime(
                tmpdir,
                "operator-token",
                "operator-pepper",
                fbnotify=coordconfig.FBCoordNotifyBootstrapConfig(
                    endpoint="http://10.99.0.30:8787/v1/events",
                    key_id="fbcoord-key",
                    token="fbcoord-secret",
                    source_instance="fbcoord",
                ),
            )
            self.assertEqual(
                (
                    "FBCOORD_TOKEN=operator-token\n"
                    "FBCOORD_TOKEN_PEPPER=operator-pepper\n"
                    "FBNOTIFY_URL=http://10.99.0.30:8787/v1/events\n"
                    "FBNOTIFY_KEY_ID=fbcoord-key\n"
                    "FBNOTIFY_TOKEN=fbcoord-secret\n"
                    "FBNOTIFY_SOURCE_INSTANCE=fbcoord\n"
                ),
                (runtime_dir / ".dev.vars").read_text(encoding="utf-8"),
            )

    def test_prepare_fbnotify_runtime_writes_dev_vars_and_links_node_modules(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            runtime_dir = coordconfig.prepare_fbnotify_runtime(tmpdir, "notify-token", "notify-pepper")
            self.assertTrue((runtime_dir / ".dev.vars").exists())
            self.assertEqual(
                "FBNOTIFY_OPERATOR_TOKEN=notify-token\nFBNOTIFY_TOKEN_PEPPER=notify-pepper\n",
                (runtime_dir / ".dev.vars").read_text(encoding="utf-8"),
            )
            self.assertTrue((runtime_dir / "src/worker.ts").exists())
            if (coordconfig.FBNOTIFY_SOURCE_DIR / "node_modules").exists():
                self.assertTrue((runtime_dir / "node_modules").is_symlink())


if __name__ == "__main__":
    unittest.main()
