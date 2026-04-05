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


def fake_topology(tmpdir: str) -> Topology:
    namespaces = {
        "hub": Namespace(name="hub", pid=101, parent=None, role="hub"),
        "hub-up": Namespace(name="hub-up", pid=102, parent="hub", role="hub-up"),
        "fbcoord": Namespace(name="fbcoord", pid=103, parent="hub", role="fbcoord"),
        "node-1": Namespace(name="node-1", pid=104, parent="hub", role="node"),
        "node-2": Namespace(name="node-2", pid=105, parent="hub", role="node"),
        "upstream-1": Namespace(name="upstream-1", pid=106, parent="hub-up", role="upstream"),
        "upstream-2": Namespace(name="upstream-2", pid=107, parent="hub-up", role="upstream"),
        "internet": Namespace(name="internet", pid=108, parent="hub", role="internet"),
    }
    return Topology(work_dir=tmpdir, namespaces=namespaces, links=default_links(), base_cidr="10.99.0.0/24")


class ConfigHelpersTest(unittest.TestCase):
    def test_generate_tokens_returns_hex_values(self) -> None:
        tokens = coordconfig.generate_tokens()
        self.assertEqual(64, len(tokens.coord_token))
        self.assertEqual(64, len(tokens.control_token))
        int(tokens.coord_token, 16)
        int(tokens.control_token, 16)

    def test_generate_fbforward_config_contains_expected_service_values(self) -> None:
        tokens = coordconfig.generate_tokens()
        with tempfile.TemporaryDirectory() as tmpdir:
            topology = fake_topology(tmpdir)
            config_path = coordconfig.generate_fbforward_config("node-1", topology, tokens, tmpdir)
            rendered = config_path.read_text(encoding="utf-8")

        self.assertIn("hostname: node-1", rendered)
        self.assertIn('auth_token: "', rendered)
        self.assertIn("endpoint: http://10.99.0.2:8787", rendered)
        self.assertIn("host: 10.99.0.22", rendered)
        self.assertIn("host: 10.99.0.26", rendered)
        self.assertIn("pool: lab", rendered)
        self.assertIn("heartbeat_interval: 10s", rendered)

    def test_prepare_fbcoord_runtime_writes_dev_vars_and_links_node_modules(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            runtime_dir = coordconfig.prepare_fbcoord_runtime(tmpdir, "coord-token")
            self.assertTrue((runtime_dir / ".dev.vars").exists())
            self.assertEqual("FBCOORD_TOKEN=coord-token\n", (runtime_dir / ".dev.vars").read_text(encoding="utf-8"))
            self.assertTrue((runtime_dir / "src/worker.ts").exists())
            self.assertTrue((runtime_dir / "node_modules").is_symlink())


if __name__ == "__main__":
    unittest.main()
