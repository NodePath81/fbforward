from __future__ import annotations

import os
import signal
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path

from .state import ProcessInfo


@dataclass(slots=True)
class ManagedProcess:
    name: str
    ns: str
    popen: subprocess.Popen[bytes]
    log_path: str
    order: int
    log_handle: object

    @property
    def pid(self) -> int:
        return self.popen.pid


def is_alive(pid: int) -> bool:
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def terminate_pid(pid: int, timeout_sec: float = 5.0) -> None:
    if pid <= 0 or not is_alive(pid):
        return

    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        return

    deadline = time.monotonic() + timeout_sec
    while time.monotonic() < deadline:
        if not is_alive(pid):
            return
        time.sleep(0.1)

    try:
        os.kill(pid, signal.SIGKILL)
    except ProcessLookupError:
        return


def terminate_process_group(pid: int, timeout_sec: float = 5.0) -> None:
    if pid <= 0 or not is_alive(pid):
        return

    try:
        os.killpg(pid, signal.SIGTERM)
    except ProcessLookupError:
        return

    deadline = time.monotonic() + timeout_sec
    while time.monotonic() < deadline:
        if not is_alive(pid):
            return
        time.sleep(0.1)

    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        return


class ProcessManager:
    def __init__(self, logs_dir: str | Path) -> None:
        self.logs_dir = Path(logs_dir)
        self.logs_dir.mkdir(parents=True, exist_ok=True)
        self._processes: dict[str, ManagedProcess] = {}
        self._start_order: list[str] = []

    def _spawn(
        self,
        cmd: list[str],
        name: str,
        *,
        ns_name: str,
        cwd: str | None = None,
        env: dict[str, str] | None = None,
    ) -> ManagedProcess:
        if name in self._processes:
            raise RuntimeError(f"process {name} is already running")

        log_path = self.logs_dir / f"{name}.log"
        log_handle = log_path.open("wb")
        full_env = os.environ.copy()
        if env:
            full_env.update(env)
        process = subprocess.Popen(
            cmd,
            cwd=cwd,
            env=full_env,
            stdout=log_handle,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        managed = ManagedProcess(
            name=name,
            ns=ns_name,
            popen=process,
            log_path=str(log_path),
            order=len(self._start_order),
            log_handle=log_handle,
        )
        self._processes[name] = managed
        self._start_order.append(name)
        return managed

    def start(
        self,
        ns_pid: int,
        ns_name: str,
        cmd: list[str],
        name: str,
        *,
        cwd: str | None = None,
        env: dict[str, str] | None = None,
    ) -> ManagedProcess:
        return self._spawn(
            [
                "nsenter",
                "--preserve-credentials",
                "--keep-caps",
                "-t",
                str(ns_pid),
                "-U",
                "-n",
                "--",
                *cmd,
            ],
            name,
            ns_name=ns_name,
            cwd=cwd,
            env=env,
        )

    def start_host(
        self,
        cmd: list[str],
        name: str,
        *,
        cwd: str | None = None,
        env: dict[str, str] | None = None,
    ) -> ManagedProcess:
        return self._spawn(cmd, name, ns_name="host", cwd=cwd, env=env)

    def stop(self, name: str, timeout_sec: float = 5.0) -> None:
        managed = self._processes.pop(name, None)
        if managed is None:
            return
        try:
            terminate_process_group(managed.pid, timeout_sec=timeout_sec)
        finally:
            try:
                managed.log_handle.close()
            except Exception:
                pass

    def stop_all(self, timeout_sec: float = 5.0) -> None:
        for name in reversed(self._start_order):
            self.stop(name, timeout_sec=timeout_sec)
        self._start_order.clear()

    def is_alive(self, name: str) -> bool:
        managed = self._processes.get(name)
        return managed is not None and managed.popen.poll() is None and is_alive(managed.pid)

    def get(self, name: str) -> ManagedProcess | None:
        return self._processes.get(name)

    def infos(self) -> dict[str, ProcessInfo]:
        return {
            name: ProcessInfo(
                pid=managed.pid,
                ns=managed.ns,
                log_path=managed.log_path,
                order=managed.order,
            )
            for name, managed in self._processes.items()
        }
