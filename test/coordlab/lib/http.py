from __future__ import annotations

import json
import textwrap

from . import netns
from .paths import VENV_PYTHON

HTTP_HELPER = textwrap.dedent(
    """\
    import json
    import sys
    import urllib.error
    import urllib.request

    url = sys.argv[1]
    method = sys.argv[2]
    headers = json.loads(sys.argv[3])
    body = sys.argv[4].encode("utf-8") if len(sys.argv) > 4 and sys.argv[4] else None
    req = urllib.request.Request(url, data=body, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            print(resp.status)
            print(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        print(exc.code)
        print(exc.read().decode("utf-8"))
    """
)

HTTP_HEADERS_HELPER = textwrap.dedent(
    """\
    import json
    import sys
    import urllib.error
    import urllib.request

    url = sys.argv[1]
    method = sys.argv[2]
    headers = json.loads(sys.argv[3])
    body = sys.argv[4].encode("utf-8") if len(sys.argv) > 4 and sys.argv[4] else None
    req = urllib.request.Request(url, data=body, method=method, headers=headers)

    def emit(status, response, payload):
        print(status)
        print(json.dumps(dict(response.headers.items())))
        print(payload)

    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            emit(resp.status, resp, resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        emit(exc.code, exc, exc.read().decode("utf-8"))
    """
)


def ns_http_request(pid: int, url: str, *, method: str = "GET", headers: dict[str, str] | None = None, body: str = "") -> tuple[int, str]:
    result = netns.nsenter_run(
        pid,
        [
            str(VENV_PYTHON),
            "-c",
            HTTP_HELPER,
            url,
            method,
            json.dumps(headers or {}),
            body,
        ],
    )
    lines = result.stdout.splitlines()
    if not lines:
        raise RuntimeError(f"no HTTP response returned for {url}")
    status = int(lines[0].strip())
    body_text = "\n".join(lines[1:])
    return status, body_text


def ns_http_request_with_headers(
    pid: int,
    url: str,
    *,
    method: str = "GET",
    headers: dict[str, str] | None = None,
    body: str = "",
) -> tuple[int, dict[str, str], str]:
    result = netns.nsenter_run(
        pid,
        [
            str(VENV_PYTHON),
            "-c",
            HTTP_HEADERS_HELPER,
            url,
            method,
            json.dumps(headers or {}),
            body,
        ],
    )
    lines = result.stdout.splitlines()
    if len(lines) < 2:
        raise RuntimeError(f"no HTTP headers returned for {url}")
    status = int(lines[0].strip())
    response_headers = json.loads(lines[1])
    body_text = "\n".join(lines[2:])
    return status, response_headers, body_text
