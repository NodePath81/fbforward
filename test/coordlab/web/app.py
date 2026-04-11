from __future__ import annotations

from collections.abc import Mapping
from pathlib import Path

import httpx
from flask import Flask, jsonify, render_template, request

from lib.clients import run_locked_add_client, run_locked_remove_client
from lib.fbnotify import clear_ntfybox_messages, list_ntfybox_messages
from lib.lab import load_active_state, normalize_state_topology
from lib.network_control import (
    build_network_controller_from_state,
    run_locked_clear_all_shaping,
    run_locked_clear_shaping,
    run_locked_set_connected,
    run_locked_set_shaping,
)
from lib.output import proxy_url, status_payload
from lib.paths import state_path_for
from lib.process import is_alive
from lib.rpc import get_status
from lib.state import LabState, load_state

DEFAULT_LOG_LINES = 100
MAX_LOG_LINES = 500
MIN_LOG_LINES = 1
NODE_PROCESS_NAMES = {
    "node-1": "fbforward-node-1",
    "node-2": "fbforward-node-2",
}
FBCOORD_PROCESS_NAME = "fbcoord"


def load_lab_state(workdir: Path) -> LabState | None:
    state = load_state(state_path_for(workdir))
    if state is None:
        return None
    return normalize_state_topology(state)


def load_active_state_or_error(workdir: Path) -> tuple[LabState | None, tuple[dict, int] | None]:
    try:
        return load_active_state(workdir), None
    except RuntimeError as exc:
        return None, ({"error": str(exc)}, 409)


def shaping_payload(state: LabState) -> dict:
    controller = build_network_controller_from_state(state)
    shaping_state = controller.get_shaping_all()
    return {
        "active": True,
        "targets": [
            {
                "target": target_name,
                "display_name": entry.display_name,
                "kind": entry.kind,
                "router_ns": entry.router_ns,
                "namespace": entry.namespace,
                "device": entry.device,
                "delay_ms": entry.delay_ms,
                "loss_pct": entry.loss_pct,
                "connected": entry.connected,
            }
            for target_name, entry in sorted(shaping_state.items())
        ],
    }


def link_state_payload(state: LabState) -> dict:
    controller = build_network_controller_from_state(state)
    link_states = controller.get_links()
    return {
        "active": True,
        "targets": [
            {
                "target": target_name,
                "display_name": link_state.display_name,
                "kind": link_state.kind,
                "router_ns": link_state.router_ns,
                "namespace": link_state.namespace,
                "device": link_state.device,
                "peer_device": link_state.peer_device,
                "connected": link_state.connected,
                "shape_capable": link_state.shape_capable,
            }
            for target_name, link_state in sorted(link_states.items())
        ],
    }


def fetch_fbcoord_state(state: LabState) -> dict:
    base_url = proxy_url(state, "fbcoord")
    if not base_url:
        raise RuntimeError("fbcoord proxy is not configured")
    if not state.tokens.operator_token:
        raise RuntimeError("fbcoord operator token is missing from coordlab state")

    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        login = client.post(f"{base_url}/api/auth/login", json={"token": state.tokens.operator_token})
        if login.status_code != 200:
            raise RuntimeError(f"fbcoord login failed: status={login.status_code} body={login.text.strip()}")
        cookie = login.headers.get("set-cookie", "").split(";", 1)[0].strip()
        if not cookie:
            raise RuntimeError("fbcoord login did not return a session cookie")
        response = client.get(f"{base_url}/api/state", headers={"Cookie": cookie})
        if response.status_code != 200:
            raise RuntimeError(f"fbcoord state fetch failed: status={response.status_code} body={response.text.strip()}")
        return response.json()


def fetch_node_status(state: LabState, node_name: str) -> dict:
    base_url = proxy_url(state, node_name)
    if not base_url:
        raise RuntimeError(f"{node_name} proxy is not configured")
    if not state.tokens.control_token:
        raise RuntimeError("control token is missing from coordlab state")
    return get_status(base_url, state.tokens.control_token)


def process_is_alive(state: LabState, process_name: str) -> bool | None:
    process = state.processes.get(process_name)
    if process is None:
        return None
    return is_alive(process.pid)


def coordination_payload(state: LabState) -> dict:
    payload = {
        "active": True,
        "fbcoord": None,
        "nodes": {"node-1": None, "node-2": None},
        "errors": {},
    }

    node_process_alive: dict[str, bool] = {}
    for node_name, process_name in NODE_PROCESS_NAMES.items():
        alive = process_is_alive(state, process_name)
        if alive is None:
            payload["errors"][node_name] = f"coordlab state is missing process metadata for {process_name}"
            node_process_alive[node_name] = False
            continue
        if not alive:
            payload["errors"][node_name] = "process exited; see log"
            node_process_alive[node_name] = False
            continue
        node_process_alive[node_name] = True
        try:
            payload["nodes"][node_name] = fetch_node_status(state, node_name)
        except Exception as exc:
            payload["errors"][node_name] = str(exc)

    fbcoord_alive = process_is_alive(state, FBCOORD_PROCESS_NAME)
    if fbcoord_alive is None:
        payload["errors"]["fbcoord"] = f"coordlab state is missing process metadata for {FBCOORD_PROCESS_NAME}"
        return payload
    if not fbcoord_alive:
        payload["errors"]["fbcoord"] = "fbcoord process exited; see log"
        return payload

    try:
        payload["fbcoord"] = fetch_fbcoord_state(state)
    except Exception as exc:
        payload["errors"]["fbcoord"] = str(exc)

    return payload


def clamp_log_lines(value: str | None) -> int:
    if value is None or value == "":
        return DEFAULT_LOG_LINES
    try:
        parsed = int(value)
    except ValueError as exc:
        raise ValueError("lines must be an integer") from exc
    return max(MIN_LOG_LINES, min(MAX_LOG_LINES, parsed))


def read_log_text(path: Path, lines: int) -> str:
    if not path.exists():
        return ""
    content = path.read_text(encoding="utf-8", errors="replace").splitlines()
    if not content:
        return ""
    return "\n".join(content[-lines:])


def parse_shaping_body(body: Mapping[str, object] | None) -> tuple[int, float]:
    if body is None or not isinstance(body, Mapping):
        raise ValueError("expected json body")
    try:
        delay_ms = int(body.get("delay_ms", 0))
        loss_pct = float(body.get("loss_pct", 0))
    except (TypeError, ValueError) as exc:
        raise ValueError("delay_ms must be an integer and loss_pct must be a number") from exc
    return delay_ms, loss_pct


def parse_client_body(body: Mapping[str, object] | None) -> tuple[str, str]:
    if body is None or not isinstance(body, Mapping):
        raise ValueError("expected json body")
    name = body.get("name")
    identity_ip = body.get("identity_ip")
    if not isinstance(name, str) or not name:
        raise ValueError("name must be a non-empty string")
    if not isinstance(identity_ip, str) or not identity_ip:
        raise ValueError("identity_ip must be a non-empty string")
    return name, identity_ip


def create_app(workdir: Path | str) -> Flask:
    workdir = Path(workdir).expanduser().resolve()
    app_root = Path(__file__).resolve().parent
    app = Flask(
        __name__,
        template_folder=str(app_root / "templates"),
        static_folder=str(app_root / "static"),
    )

    @app.get("/")
    def index():
        return render_template("index.html")

    @app.get("/api/status")
    def api_status():
        return jsonify(status_payload(load_lab_state(workdir), workdir))

    @app.get("/api/coordination")
    def api_coordination():
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        return jsonify(coordination_payload(state))

    @app.get("/api/shaping")
    def api_shaping():
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        try:
            return jsonify(shaping_payload(state))
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.get("/api/link-state")
    def api_link_state():
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        try:
            return jsonify(link_state_payload(state))
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.post("/api/clients")
    def api_clients_add():
        try:
            name, identity_ip = parse_client_body(request.get_json(silent=True))
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400

        try:
            updated = run_locked_add_client(workdir, name, identity_ip)
            return jsonify(status_payload(updated, workdir))
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except KeyError as exc:
            return jsonify({"error": exc.args[0]}), 404
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.delete("/api/clients/<name>")
    def api_clients_delete(name: str):
        try:
            updated = run_locked_remove_client(workdir, name)
            return jsonify(status_payload(updated, workdir))
        except KeyError as exc:
            return jsonify({"error": exc.args[0]}), 404
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.put("/api/shaping/<target>")
    def api_shaping_set(target: str):
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        try:
            delay_ms, loss_pct = parse_shaping_body(request.get_json(silent=True))
            updated = run_locked_set_shaping(workdir, target, delay_ms, loss_pct)
            return jsonify(shaping_payload(updated))
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.delete("/api/shaping/<target>")
    def api_shaping_clear(target: str):
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        try:
            updated = run_locked_clear_shaping(workdir, target)
            return jsonify(shaping_payload(updated))
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.delete("/api/shaping")
    def api_shaping_clear_all():
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        try:
            updated = run_locked_clear_all_shaping(workdir)
            return jsonify(shaping_payload(updated))
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.put("/api/link-state/<target>")
    def api_link_state_set(target: str):
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True)
        if body is None or not isinstance(body, Mapping) or "connected" not in body:
            return jsonify({"error": "expected json body with connected boolean"}), 400
        connected = body.get("connected")
        if not isinstance(connected, bool):
            return jsonify({"error": "connected must be a boolean"}), 400
        try:
            updated = run_locked_set_connected(workdir, target, connected)
            return jsonify(link_state_payload(updated))
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.get("/api/logs/<process_name>")
    def api_logs(process_name: str):
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status

        process = state.processes.get(process_name)
        if process is None:
            return jsonify({"error": f"unknown process: {process_name}"}), 404

        try:
            lines = clamp_log_lines(request.args.get("lines"))
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400

        text = read_log_text(Path(process.log_path), lines)
        return jsonify({"process": process_name, "lines": lines, "text": text})

    @app.get("/api/ntfybox")
    def api_ntfybox_list():
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        try:
            return jsonify({"messages": list_ntfybox_messages(state)})
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    @app.delete("/api/ntfybox")
    def api_ntfybox_clear():
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        try:
            clear_ntfybox_messages(state)
            return jsonify({"ok": True})
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 409

    return app
