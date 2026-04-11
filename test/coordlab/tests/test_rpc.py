from __future__ import annotations

import sys
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib import rpc


class RPCHelpersTest(unittest.TestCase):
    def test_get_status_returns_result_payload(self) -> None:
        response = mock.Mock()
        response.status_code = 200
        response.json.return_value = {"ok": True, "result": {"mode": "auto"}}
        with mock.patch("lib.rpc.httpx.post", return_value=response) as post:
            result = rpc.get_status("http://127.0.0.1:18701", "token")

        self.assertEqual({"mode": "auto"}, result)
        _, kwargs = post.call_args
        self.assertEqual("GetStatus", kwargs["json"]["method"])
        self.assertEqual("Bearer token", kwargs["headers"]["Authorization"])

    def test_set_upstream_posts_expected_payload(self) -> None:
        response = mock.Mock()
        response.status_code = 200
        response.json.return_value = {"ok": True}
        with mock.patch("lib.rpc.httpx.post", return_value=response) as post:
            rpc.set_upstream("http://127.0.0.1:18702", "token", "manual", tag="us-2")

        _, kwargs = post.call_args
        self.assertEqual("SetUpstream", kwargs["json"]["method"])
        self.assertEqual({"mode": "manual", "tag": "us-2"}, kwargs["json"]["params"])

    def test_set_mode_coordination_posts_expected_payload(self) -> None:
        response = mock.Mock()
        response.status_code = 200
        response.json.return_value = {"ok": True}
        with mock.patch("lib.rpc.httpx.post", return_value=response) as post:
            rpc.set_mode_coordination("http://127.0.0.1:18702", "token")

        _, kwargs = post.call_args
        self.assertEqual({"mode": "coordination"}, kwargs["json"]["params"])

    def test_fetch_metrics_returns_response_text(self) -> None:
        response = mock.Mock(status_code=200, text="metric 1\n")
        with mock.patch("lib.rpc.httpx.get", return_value=response) as get:
            metrics = rpc.fetch_metrics("http://127.0.0.1:18701")

        self.assertEqual("metric 1\n", metrics)
        get.assert_called_once()

    def test_fetch_metrics_raises_on_non_200(self) -> None:
        response = mock.Mock(status_code=503, text="unavailable")
        with mock.patch("lib.rpc.httpx.get", return_value=response):
            with self.assertRaises(RuntimeError) as ctx:
                rpc.fetch_metrics("http://127.0.0.1:18701")

        self.assertIn("status=503", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
