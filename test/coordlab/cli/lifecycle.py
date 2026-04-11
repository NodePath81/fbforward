from __future__ import annotations

import argparse
import shutil
from pathlib import Path
from secrets import token_hex

from lib import config as coordconfig
from lib import netns
from lib import readiness
from lib.build import (
    FBFORWARD_BIN,
    FBMEASURE_BIN,
    ensure_fbcoord_assets,
    ensure_fbforward_binaries,
    ensure_fbnotify_assets,
    ensure_geoip_mmdbs,
    wrangler_command,
)
from lib.env import parse_client_specs, require_tools
from lib.fbcoord import (
    apply_coordination_mode,
    fbcoord_namespace_base_url,
    mint_fbcoord_node_tokens,
    verify_fbcoord_health_in_namespace,
    verify_fbforward_rpc_in_namespace,
)
from lib.fbnotify import (
    FBNOTIFY_NODE_TOKEN_ENVS,
    bootstrap_fbnotify,
    fbnotify_ingest_url,
    fbnotify_namespace_base_url,
    fbnotify_public_url,
    verify_fbnotify_health_in_namespace,
)
from lib.lab import (
    build_node_feature_summary,
    build_state,
    existing_lab_is_alive,
    normalize_state_topology,
    namespace_shutdown_order,
    process_shutdown_order,
    validate_fbforward_config,
)
from lib.output import exception_message, print_json, render_summary, status_payload
from lib.paths import (
    COORDLAB_SCRIPT,
    DEFAULT_WORKDIR,
    REPO_ROOT,
    VENV_PYTHON,
    data_dir_for,
    fbnotify_runtime_dir_for,
    logs_dir_for,
    state_path_for,
)
from lib.ports import PROXY_PROCESS_NAME, assert_host_ports_available, build_proxy_infos
from lib.process import ProcessManager, terminate_pid, terminate_process_group
from lib.state import FBNotifyInfo, load_state, save_state
from lib.terminal import allocate_ttyd_ports, start_ttyd_terminals


def register_parser(subparsers) -> None:
    for name, handler, help_text in (
        ("up", cmd_up, "start the Phase 5 coordlab services and host proxies"),
        ("down", cmd_down, "stop the Phase 5 coordlab services and topology"),
        ("status", cmd_status, "show the Phase 5 coordlab state"),
    ):
        sub = subparsers.add_parser(name, help=help_text)
        sub.add_argument("--workdir", default=str(DEFAULT_WORKDIR))
        if name == "up":
            sub.add_argument("--skip-build", action="store_true")
            sub.add_argument("--skip-connectivity-check", action="store_true")
            sub.add_argument("--client", action="append", default=[], metavar="NAME=IP")
        if name == "status":
            sub.add_argument("--json", action="store_true")
        sub.set_defaults(handler=handler)


def cmd_up(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    workdir.mkdir(parents=True, exist_ok=True)
    state_path = state_path_for(workdir)
    existing = load_state(state_path)
    if existing is not None:
        alive = existing_lab_is_alive(existing)
        if alive:
            raise RuntimeError(
                f"existing coordlab state is still active in {workdir}: alive entries={', '.join(alive)}"
            )

    client_specs = parse_client_specs(args.client)
    ttyd_ports = allocate_ttyd_ports(client_specs.keys())
    require_tools(["unshare", "nsenter", "ip", "sysctl", "ping", str(VENV_PYTHON), "ttyd"])
    assert_host_ports_available(
        extra_bindings=[(f"ttyd-{name}", "127.0.0.1", port) for name, port in sorted(ttyd_ports.items())]
    )
    ensure_fbforward_binaries(args.skip_build)
    ensure_fbcoord_assets(args.skip_build)
    ensure_geoip_mmdbs(workdir)
    data_dir_for(workdir).mkdir(parents=True, exist_ok=True)

    generated_tokens = coordconfig.generate_tokens()
    tokens = generated_tokens.tokens
    node_features = build_node_feature_summary(workdir)
    topology = netns.build_topology(str(workdir), client_specs=client_specs)
    manager = ProcessManager(logs_dir_for(workdir))
    fbnotify_info = FBNotifyInfo(
        available=False,
        error="",
        public_url=fbnotify_public_url(),
        internal_base_url=fbnotify_namespace_base_url(topology),
        internal_ingest_url=fbnotify_ingest_url(topology),
        operator_token=token_hex(32),
        emitters={},
    )
    fbnotify_pepper = token_hex(32)
    fbnotify_internal_ready = False

    try:
        if not args.skip_connectivity_check:
            netns.verify_connectivity(topology)

        try:
            ensure_fbnotify_assets(args.skip_build)
            fbnotify_runtime_dir = coordconfig.prepare_fbnotify_runtime(
                workdir,
                fbnotify_info.operator_token,
                fbnotify_pepper,
            )
            manager.start(
                topology.namespaces["fbnotify"].pid,
                "fbnotify",
                [*wrangler_command(), "--ip", "0.0.0.0", "--port", "8787"],
                "fbnotify",
                cwd=str(fbnotify_runtime_dir),
                env={
                    "FBNOTIFY_OPERATOR_TOKEN": fbnotify_info.operator_token,
                    "FBNOTIFY_TOKEN_PEPPER": fbnotify_pepper,
                },
            )
            verify_fbnotify_health_in_namespace(topology, manager)
            fbnotify_info.emitters = bootstrap_fbnotify(
                fbnotify_info.internal_base_url,
                topology.namespaces["node-1"].pid,
                fbnotify_info.operator_token,
            )
            fbnotify_internal_ready = True
        except Exception as exc:
            fbnotify_info.error = exception_message(exc)
            manager.stop("fbnotify")

        manager.start(
            topology.namespaces["upstream-1"].pid,
            "upstream-1",
            [str(FBMEASURE_BIN), "--port", "9876"],
            "fbmeasure-upstream-1",
        )
        manager.start(
            topology.namespaces["upstream-2"].pid,
            "upstream-2",
            [str(FBMEASURE_BIN), "--port", "9876"],
            "fbmeasure-upstream-2",
        )

        runtime_dir = coordconfig.prepare_fbcoord_runtime(
            workdir,
            tokens.operator_token,
            generated_tokens.operator_pepper,
        )
        fbcoord_env = {
            "FBCOORD_TOKEN": tokens.operator_token,
            "FBCOORD_TOKEN_PEPPER": generated_tokens.operator_pepper,
        }
        if fbnotify_internal_ready:
            fbcoord_notify = fbnotify_info.emitters["fbcoord"]
            fbcoord_env.update(
                {
                    "FBNOTIFY_URL": fbnotify_info.internal_ingest_url,
                    "FBNOTIFY_KEY_ID": fbcoord_notify.key_id,
                    "FBNOTIFY_TOKEN": fbcoord_notify.token,
                    "FBNOTIFY_SOURCE_INSTANCE": fbcoord_notify.source_instance,
                }
            )
        print(
            "coordlab fbcoord launch:"
            f" runtime={runtime_dir}"
            f" FBNOTIFY_URL={'set' if fbcoord_env.get('FBNOTIFY_URL') else 'unset'}"
            f" FBNOTIFY_KEY_ID={'set' if fbcoord_env.get('FBNOTIFY_KEY_ID') else 'unset'}"
            f" FBNOTIFY_SOURCE_INSTANCE={'set' if fbcoord_env.get('FBNOTIFY_SOURCE_INSTANCE') else 'unset'}"
        )
        manager.start(
            topology.namespaces["fbcoord"].pid,
            "fbcoord",
            [*wrangler_command(), "--ip", "0.0.0.0", "--port", "8787"],
            "fbcoord",
            cwd=str(runtime_dir),
            env=fbcoord_env,
        )
        verify_fbcoord_health_in_namespace(topology, manager)

        tokens.node_tokens = mint_fbcoord_node_tokens(
            fbcoord_namespace_base_url(topology),
            topology.namespaces["node-1"].pid,
            tokens.operator_token,
            ("node-1", "node-2"),
        )

        config_paths: dict[str, Path] = {}
        node_envs: dict[str, dict[str, str]] = {"node-1": {}, "node-2": {}}
        for node in ("node-1", "node-2"):
            fbnotify_node_cfg = None
            if fbnotify_internal_ready:
                emitter = fbnotify_info.emitters[node]
                token_env = FBNOTIFY_NODE_TOKEN_ENVS[node]
                fbnotify_node_cfg = coordconfig.FBNotifyNodeConfig(
                    endpoint=fbnotify_info.internal_ingest_url,
                    key_id=emitter.key_id,
                    token_env=token_env,
                    source_instance=emitter.source_instance,
                )
                node_envs[node][token_env] = emitter.token
            config_paths[node] = coordconfig.generate_fbforward_config(
                node,
                topology,
                tokens,
                workdir,
                fbnotify=fbnotify_node_cfg,
            )
        for node, config_path in config_paths.items():
            validate_fbforward_config(config_path, env=node_envs[node] or None)

        manager.start(
            topology.namespaces["node-1"].pid,
            "node-1",
            [str(FBFORWARD_BIN), "run", "--config", str(config_paths["node-1"])],
            "fbforward-node-1",
            env=node_envs["node-1"] or None,
        )
        manager.start(
            topology.namespaces["node-2"].pid,
            "node-2",
            [str(FBFORWARD_BIN), "run", "--config", str(config_paths["node-2"])],
            "fbforward-node-2",
            env=node_envs["node-2"] or None,
        )

        verify_fbforward_rpc_in_namespace(topology, manager, "node-1", tokens.control_token)
        verify_fbforward_rpc_in_namespace(topology, manager, "node-2", tokens.control_token)

        def save_runtime_state(*, proxies, terminals=None) -> None:
            state = build_state(
                workdir,
                topology,
                phase=5,
                active=True,
                processes=manager.infos(),
                proxies=proxies,
                terminals=terminals,
                node_features=node_features,
                tokens=tokens,
                fbnotify=fbnotify_info,
            )
            save_state(state_path, state)

        proxies = build_proxy_infos(include_fbnotify=fbnotify_internal_ready)
        save_runtime_state(proxies=proxies)

        manager.start_host(
            [str(VENV_PYTHON), str(COORDLAB_SCRIPT), "proxy-daemon", "--state", str(state_path)],
            PROXY_PROCESS_NAME,
            cwd=str(REPO_ROOT),
        )
        save_runtime_state(proxies=proxies)

        if fbnotify_internal_ready:
            try:
                readiness.verify_fbnotify_public(fbnotify_info.public_url, fbnotify_info.operator_token)
                fbnotify_info.available = True
                fbnotify_info.error = ""
            except Exception as exc:
                manager.stop(PROXY_PROCESS_NAME)
                fbnotify_info.available = False
                fbnotify_info.error = exception_message(exc)
                proxies = build_proxy_infos(include_fbnotify=False)
                save_runtime_state(proxies=proxies)
                manager.start_host(
                    [str(VENV_PYTHON), str(COORDLAB_SCRIPT), "proxy-daemon", "--state", str(state_path)],
                    PROXY_PROCESS_NAME,
                    cwd=str(REPO_ROOT),
                )
        save_runtime_state(proxies=proxies)

        fbcoord_url = f"http://{proxies['fbcoord'].listen_host}:{proxies['fbcoord'].host_port}"
        node1_url = f"http://{proxies['node-1'].listen_host}:{proxies['node-1'].host_port}"
        node2_url = f"http://{proxies['node-2'].listen_host}:{proxies['node-2'].host_port}"

        readiness.wait_http_ok(f"{fbcoord_url}/healthz")
        readiness.wait_for_status(node1_url, tokens.control_token, predicate=lambda status: True)
        readiness.wait_for_status(node2_url, tokens.control_token, predicate=lambda status: True)

        apply_coordination_mode(node1_url, tokens.control_token, skip_build=args.skip_build)
        apply_coordination_mode(node2_url, tokens.control_token, skip_build=args.skip_build)

        def coordination_connected(status: dict) -> bool:
            coordination = status.get("coordination") or {}
            return status.get("mode") == "coordination" and bool(coordination.get("connected"))

        readiness.wait_for_status(node1_url, tokens.control_token, predicate=coordination_connected)
        readiness.wait_for_status(node2_url, tokens.control_token, predicate=coordination_connected)
        readiness.verify_fbcoord_api(fbcoord_url, tokens.operator_token, expected_node_ids=("node-1", "node-2"))

        terminals = start_ttyd_terminals(manager, topology, ttyd_ports)
        save_runtime_state(proxies=proxies, terminals=terminals)
        state = load_state(state_path)
        assert state is not None
        if args.skip_connectivity_check:
            print("coordlab note: skipping connectivity preflight")
        print(render_summary(state, str(VENV_PYTHON)))
        return 0
    except Exception:
        manager.stop_all()
        netns.destroy_topology(topology)
        raise


def cmd_down(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        print(f"no coordlab state found at {state_path}")
        return 0

    proxy_info = state.processes.get(PROXY_PROCESS_NAME)
    if proxy_info is not None:
        terminate_process_group(proxy_info.pid, timeout_sec=5)
    for name, info in process_shutdown_order(state.processes):
        if name == PROXY_PROCESS_NAME:
            continue
        terminate_process_group(info.pid, timeout_sec=5)
    for _, info in namespace_shutdown_order(state.namespaces):
        terminate_pid(info.pid, timeout_sec=5)

    fbnotify_runtime_dir = fbnotify_runtime_dir_for(workdir)
    if fbnotify_runtime_dir.exists():
        shutil.rmtree(fbnotify_runtime_dir)

    state.active = False
    save_state(state_path, state)
    print(f"coordlab services stopped for {workdir}")
    return 0


def cmd_status(args: argparse.Namespace) -> int:
    workdir = Path(args.workdir).expanduser().resolve()
    state_path = state_path_for(workdir)
    state = load_state(state_path)
    if state is None:
        if args.json:
            print_json(status_payload(None, workdir))
            return 1
        print(f"no coordlab state found at {state_path}")
        return 1
    state = normalize_state_topology(state)
    if args.json:
        print_json(status_payload(state, workdir))
        return 0
    print(render_summary(state, str(VENV_PYTHON)))
    return 0
