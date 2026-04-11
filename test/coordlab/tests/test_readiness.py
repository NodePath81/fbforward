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

    def test_verify_fbcoord_api_confirms_expected_node_ids(self) -> None:
        login = mock.Mock(status_code=200, text='{"ok":true}')
        login.headers = {"set-cookie": "fbcoord_session=test-session; Max-Age=86400; HttpOnly; Secure"}
        state = mock.Mock(status_code=200)
        state.json.return_value = {"pick": {"version": 1, "upstream": "us-1"}, "node_count": 2, "nodes": [{"node_id": "node-1"}, {"node_id": "node-2"}]}
        client = FakeClient(get_responses=[state], post_responses=[login])
        with mock.patch("lib.readiness.httpx.Client", return_value=client):
            payload = readiness.verify_fbcoord_api("http://127.0.0.1:18700", "operator-token", expected_node_ids=("node-1", "node-2"))
        self.assertEqual(2, payload["node_count"])
        self.assertEqual({"Cookie": "fbcoord_session=test-session"}, client.last_get_headers)

    def test_verify_fbnotify_api_fetches_capture_messages(self) -> None:
        login = mock.Mock(status_code=200, text='{"ok":true}')
        login.headers = {"set-cookie": "fbnotify_session=test-session; Max-Age=86400; HttpOnly; Secure"}
        capture = mock.Mock(status_code=200)
        capture.json.return_value = {"messages": []}
        client = FakeClient(get_responses=[capture], post_responses=[login])
        with mock.patch("lib.readiness.httpx.Client", return_value=client):
            payload = readiness.verify_fbnotify_api("http://127.0.0.1:18703", "operator-token")
        self.assertEqual([], payload["messages"])
        self.assertEqual({"Cookie": "fbnotify_session=test-session"}, client.last_get_headers)

    def test_verify_fbcoord_notify_config_confirms_expected_sender_fields(self) -> None:
        login = mock.Mock(status_code=200, text='{"ok":true}')
        login.headers = {"set-cookie": "fbcoord_session=test-session; Max-Age=86400; HttpOnly; Secure"}
        config = mock.Mock(status_code=200)
        config.json.return_value = {
            "configured": True,
            "source": "bootstrap-env",
            "endpoint": "http://10.99.0.30:8787/v1/events",
            "key_id": "fbcoord-key",
            "source_instance": "fbcoord",
            "masked_prefix": "fbcoord-...",
            "updated_at": 1234,
            "missing": [],
        }
        client = FakeClient(get_responses=[config], post_responses=[login])
        with mock.patch("lib.readiness.httpx.Client", return_value=client):
            payload = readiness.verify_fbcoord_notify_config(
                "http://127.0.0.1:18700",
                "operator-token",
                expected_endpoint="http://10.99.0.30:8787/v1/events",
                expected_key_id="fbcoord-key",
                expected_source_instance="fbcoord",
            )
        self.assertTrue(payload["configured"])
        self.assertEqual({"Cookie": "fbcoord_session=test-session"}, client.last_get_headers)

    def test_verify_fbnotify_public_waits_for_health_then_fetches_api(self) -> None:
        with (
            mock.patch("lib.readiness.wait_http_ok") as wait_http_ok,
            mock.patch("lib.readiness.verify_fbnotify_api", return_value={"messages": []}) as verify_fbnotify_api,
        ):
            payload = readiness.verify_fbnotify_public("http://127.0.0.1:18703", "operator-token")

        self.assertEqual({"messages": []}, payload)
        wait_http_ok.assert_called_once_with("http://127.0.0.1:18703/healthz")
        verify_fbnotify_api.assert_called_once_with("http://127.0.0.1:18703", "operator-token")


if __name__ == "__main__":
    unittest.main()
