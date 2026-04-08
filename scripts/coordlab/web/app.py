from __future__ import annotations

from collections.abc import Mapping
from pathlib import Path

import httpx
from flask import Flask, jsonify, render_template, request

from lib.output import proxy_url, terminal_url
from lib.process import is_alive
from lib.rpc import get_status
from lib.linkstate import LinkStateController
from lib.shaping import TrafficShaper
from lib.state import LabState, load_state

STATE_FILENAME = "state.json"
DEFAULT_POOL = "lab"
DEFAULT_LOG_LINES = 100
MAX_LOG_LINES = 500
MIN_LOG_LINES = 1
NODE_PROCESS_NAMES = {
    "node-1": "fbforward-node-1",
    "node-2": "fbforward-node-2",
}
FBCOORD_PROCESS_NAME = "fbcoord"


def state_path_for(workdir: Path) -> Path:
    return workdir / STATE_FILENAME


def load_lab_state(workdir: Path) -> LabState | None:
    return load_state(state_path_for(workdir))


def lab_inactive_payload(workdir: Path, error: str) -> dict:
    return {
        "active": False,
        "error": error,
        "phase": None,
        "work_dir": str(workdir),
        "state_path": str(state_path_for(workdir)),
        "namespaces": [],
        "processes": [],
        "proxies": {},
        "clients": {},
        "terminals": {},
        "service_links": {},
        "shaping_targets": [],
        "topology_links": [],
    }


def proxy_dict(state: LabState) -> dict[str, dict]:
    return {
        name: {
            "listen_host": proxy.listen_host,
            "host_port": proxy.host_port,
            "target_ns": proxy.target_ns,
            "target_host": proxy.target_host,
            "target_port": proxy.target_port,
        }
        for name, proxy in sorted(state.proxies.items())
    }


def service_links(state: LabState) -> dict[str, str]:
    links: dict[str, str] = {}
    for name in ("fbcoord", "node-1", "node-2"):
        url = proxy_url(state, name)
        if url:
            links[name] = url
    return links


def client_dict(state: LabState) -> dict[str, dict]:
    return {
        name: {
            "identity_ip": info.identity_ip,
        }
        for name, info in sorted(state.clients.items())
    }


def terminal_dict(state: LabState) -> dict[str, dict]:
    return {
        name: {
            "host_port": info.host_port,
            "pid": info.pid,
            "alive": is_alive(info.pid),
            "url": terminal_url(info.host_port),
        }
        for name, info in sorted(state.terminals.items())
    }


def status_payload(state: LabState | None, workdir: Path) -> dict:
    if state is None:
        return lab_inactive_payload(workdir, f"no coordlab state found at {state_path_for(workdir)}")
    if not state.active:
        return {
            **lab_inactive_payload(workdir, f"coordlab state is not active: {state_path_for(workdir)}"),
            "phase": state.phase,
        }

    return {
        "active": True,
        "phase": state.phase,
        "work_dir": state.work_dir,
        "state_path": str(state_path_for(workdir)),
        "namespaces": [
            {
                "name": name,
                "pid": info.pid,
                "parent": info.parent,
                "role": info.role,
                "alive": is_alive(info.pid),
            }
            for name, info in sorted(state.namespaces.items())
        ],
        "processes": [
            {
                "name": name,
                "pid": info.pid,
                "ns": info.ns,
                "alive": is_alive(info.pid),
                "log_path": info.log_path,
                "order": info.order,
            }
            for name, info in sorted(state.processes.items(), key=lambda item: (item[1].order, item[0]))
        ],
        "proxies": proxy_dict(state),
        "clients": client_dict(state),
        "terminals": terminal_dict(state),
        "service_links": service_links(state),
        "shaping_targets": [
            {
                "target": target_name,
                "router_ns": target.router_ns,
                "tag": target.tag,
                "namespace": target.namespace,
                "device": target.device,
            }
            for target_name, target in sorted(state.shaping.targets.items())
        ],
        "topology_links": [
            {
                "left_ns": link.left_ns,
                "right_ns": link.right_ns,
                "left_if": link.left_if,
                "right_if": link.right_if,
                "left_ip": link.left_ip,
                "right_ip": link.right_ip,
                "subnet": link.subnet,
            }
            for link in state.topology.links
        ],
    }


def load_active_state_or_error(workdir: Path) -> tuple[LabState | None, tuple[dict, int] | None]:
    state = load_lab_state(workdir)
    if state is None:
        return None, ({"error": f"no coordlab state found at {state_path_for(workdir)}"}, 409)
    if not state.active:
        return None, ({"error": f"coordlab state is not active: {state_path_for(workdir)}"}, 409)
    return state, None


def build_shaper_from_state(state: LabState) -> TrafficShaper:
    router_pids = resolve_target_router_pids(state, kind="shaping")
    return TrafficShaper(router_pids, state.shaping)


def build_link_state_controller_from_state(state: LabState) -> LinkStateController:
    router_pids = resolve_target_router_pids(state, kind="link-state")
    return LinkStateController(router_pids, state.shaping)


def resolve_target_router_pids(state: LabState, *, kind: str) -> dict[str, int]:
    if not state.shaping.targets:
        raise RuntimeError("coordlab state does not contain target topology")
    router_pids: dict[str, int] = {}
    for target_name, target in sorted(state.shaping.targets.items()):
        router_info = state.namespaces.get(target.router_ns)
        if router_info is None:
            raise RuntimeError(
                f"coordlab state references unknown {kind} router namespace: {target.router_ns} for {target_name}"
            )
        if not is_alive(router_info.pid):
            raise RuntimeError(f"{kind} router namespace is not alive: {target.router_ns} pid={router_info.pid}")
        router_pids[target.router_ns] = router_info.pid
    return router_pids


def shaping_payload(state: LabState, shaper: TrafficShaper | None = None) -> dict:
    if shaper is None:
        shaper = build_shaper_from_state(state)
    shaping_state = shaper.get_all()
    return {
        "active": True,
        "targets": [
            {
                "target": target_name,
                "router_ns": target.router_ns,
                "tag": target.tag,
                "namespace": target.namespace,
                "device": target.device,
                "delay_ms": shaping_state[target_name].delay_ms if shaping_state[target_name] else 0,
                "loss_pct": shaping_state[target_name].loss_pct if shaping_state[target_name] else 0.0,
            }
            for target_name, target in sorted(state.shaping.targets.items())
        ],
    }


def link_state_payload(state: LabState, controller: LinkStateController | None = None) -> dict:
    if controller is None:
        controller = build_link_state_controller_from_state(state)
    link_states = controller.get_all()
    return {
        "active": True,
        "targets": [
            {
                "target": target_name,
                "router_ns": link_state.router_ns,
                "namespace": link_state.namespace,
                "device": link_state.device,
                "connected": link_state.connected,
            }
            for target_name, link_state in sorted(link_states.items())
        ],
    }


def fetch_fbcoord_pool(state: LabState, *, pool: str = DEFAULT_POOL) -> dict:
    base_url = proxy_url(state, "fbcoord")
    if not base_url:
        raise RuntimeError("fbcoord proxy is not configured")
    if not state.tokens.coord_token:
        raise RuntimeError("coordination token is missing from coordlab state")

    with httpx.Client(timeout=5.0, follow_redirects=True) as client:
        login = client.post(f"{base_url}/api/auth/login", json={"token": state.tokens.coord_token})
        if login.status_code != 200:
            raise RuntimeError(f"fbcoord login failed: status={login.status_code} body={login.text.strip()}")
        cookie = login.headers.get("set-cookie", "").split(";", 1)[0].strip()
        if not cookie:
            raise RuntimeError("fbcoord login did not return a session cookie")
        response = client.get(f"{base_url}/api/pools/{pool}", headers={"Cookie": cookie})
        if response.status_code != 200:
            raise RuntimeError(f"fbcoord pool fetch failed: status={response.status_code} body={response.text.strip()}")
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
        payload["fbcoord"] = fetch_fbcoord_pool(state, pool=DEFAULT_POOL)
    except Exception as exc:
        message = str(exc)
        if "status=404" in message and node_process_alive and not any(node_process_alive.values()):
            message = "pool disappeared after node disconnect"
        payload["errors"]["fbcoord"] = message

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

    @app.put("/api/shaping/<target>")
    def api_shaping_set(target: str):
        state, error = load_active_state_or_error(workdir)
        if error is not None:
            payload, status = error
            return jsonify(payload), status
        try:
            delay_ms, loss_pct = parse_shaping_body(request.get_json(silent=True))
            shaper = build_shaper_from_state(state)
            shaper.set(target, delay_ms=delay_ms, loss_pct=loss_pct)
            return jsonify(shaping_payload(state, shaper))
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
            shaper = build_shaper_from_state(state)
            shaper.clear(target)
            return jsonify(shaping_payload(state, shaper))
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
            shaper = build_shaper_from_state(state)
            shaper.clear_all()
            return jsonify(shaping_payload(state, shaper))
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
            controller = build_link_state_controller_from_state(state)
            controller.set_connected(target, connected)
            return jsonify(link_state_payload(state, controller))
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

    return app
