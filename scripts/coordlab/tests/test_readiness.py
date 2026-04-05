from __future__ import annotations

import sys
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from lib import readiness


class FakeClient:
    def __init__(self, get_responses=None, post_responses=None):
        self._get_responses = list(get_responses or [])
        self._post_responses = list(post_responses or [])

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False

    def get(self, _url, headers=None):
        if not self._get_responses:
            raise AssertionError("unexpected GET")
        self.last_get_headers = headers
        return self._get_responses.pop(0)

    def post(self, _url, json=None, headers=None):
        if not self._post_responses:
            raise AssertionError("unexpected POST")
        self.last_post_headers = headers
        return self._post_responses.pop(0)


class ReadinessHelpersTest(unittest.TestCase):
    def test_wait_http_ok_returns_successful_response(self) -> None:
        bad = mock.Mock(status_code=503)
        good = mock.Mock(status_code=200)
        with mock.patch("lib.readiness.httpx.Client", return_value=FakeClient(get_responses=[bad, good])):
            response = readiness.wait_http_ok("http://127.0.0.1:18700/healthz", timeout_sec=1, interval_sec=0)
        self.assertIs(good, response)

    def test_wait_for_status_retries_until_predicate_matches(self) -> None:
        statuses = [
            {"mode": "auto", "coordination": {"connected": False}},
            {"mode": "coordination", "coordination": {"connected": True}},
        ]
        with mock.patch("lib.readiness.rpc.get_status", side_effect=statuses):
            status = readiness.wait_for_status(
                "http://127.0.0.1:18701",
                "token",
                predicate=lambda payload: payload["coordination"]["connected"],
                timeout_sec=1,
                interval_sec=0,
            )
        self.assertEqual("coordination", status["mode"])

    def test_verify_fbcoord_api_confirms_expected_pool(self) -> None:
        login = mock.Mock(status_code=200, text='{"ok":true}')
        login.headers = {"set-cookie": "fbcoord_session=test-session; Max-Age=86400; HttpOnly; Secure"}
        pools = mock.Mock(status_code=200)
        pools.json.return_value = {"pools": [{"name": "lab", "node_count": 2}]}
        client = FakeClient(get_responses=[pools], post_responses=[login])
        with mock.patch("lib.readiness.httpx.Client", return_value=client):
            entries = readiness.verify_fbcoord_api("http://127.0.0.1:18700", "coord-token", expected_pool="lab")
        self.assertEqual("lab", entries[0]["name"])
        self.assertEqual({"Cookie": "fbcoord_session=test-session"}, client.last_get_headers)


if __name__ == "__main__":
    unittest.main()
