from __future__ import annotations

import asyncio
import os
import signal
import sys
import textwrap
from contextlib import suppress
from pathlib import Path

from .state import ProxyInfo, load_state

RELAY_SCRIPT = textwrap.dedent(
    """\
    from contextlib import suppress
    import os
    import socket
    import sys
    import threading

    host = sys.argv[1]
    port = int(sys.argv[2])
    sock = socket.create_connection((host, port), timeout=10)

    def stdin_to_socket() -> None:
        try:
            while True:
                chunk = os.read(0, 65536)
                if not chunk:
                    with suppress(OSError):
                        sock.shutdown(socket.SHUT_WR)
                    return
                sock.sendall(chunk)
        except OSError:
            return

    threading.Thread(target=stdin_to_socket, daemon=True).start()
    try:
        while True:
            chunk = sock.recv(65536)
            if not chunk:
                break
            os.write(1, chunk)
    finally:
        sock.close()
    """
)


class ProxyDaemon:
    def __init__(self, proxies: dict[str, ProxyInfo]) -> None:
        self.proxies = proxies
        self._servers: list[asyncio.AbstractServer] = []
        self._shutdown = asyncio.Event()

    async def run(self) -> None:
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGTERM, signal.SIGINT):
            with suppress(NotImplementedError):
                loop.add_signal_handler(sig, self._shutdown.set)

        for name, spec in sorted(self.proxies.items()):
            server = await asyncio.start_server(
                lambda reader, writer, proxy_name=name, proxy_spec=spec: self._handle_connection(
                    proxy_name, proxy_spec, reader, writer
                ),
                host=spec.listen_host,
                port=spec.host_port,
            )
            self._servers.append(server)

        await self._shutdown.wait()
        await self.close()

    async def close(self) -> None:
        for server in self._servers:
            server.close()
        for server in self._servers:
            await server.wait_closed()
        self._servers.clear()

    async def _handle_connection(
        self,
        name: str,
        spec: ProxyInfo,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> None:
        proc = await asyncio.create_subprocess_exec(
            "nsenter",
            "--preserve-credentials",
            "--keep-caps",
            "-t",
            str(spec.target_ns),
            "-U",
            "-n",
            "--",
            sys.executable,
            "-c",
            RELAY_SCRIPT,
            spec.target_host,
            str(spec.target_port),
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL,
        )

        async def reader_to_proc() -> None:
            try:
                while True:
                    chunk = await reader.read(65536)
                    if not chunk:
                        break
                    assert proc.stdin is not None
                    proc.stdin.write(chunk)
                    await proc.stdin.drain()
            finally:
                if proc.stdin is not None:
                    with suppress(Exception):
                        proc.stdin.close()
                        await proc.stdin.wait_closed()

        async def proc_to_writer() -> None:
            try:
                assert proc.stdout is not None
                while True:
                    chunk = await proc.stdout.read(65536)
                    if not chunk:
                        break
                    writer.write(chunk)
                    await writer.drain()
            finally:
                with suppress(Exception):
                    writer.close()
                    await writer.wait_closed()

        tasks = [
            asyncio.create_task(reader_to_proc(), name=f"{name}-reader"),
            asyncio.create_task(proc_to_writer(), name=f"{name}-writer"),
        ]
        done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
        for task in pending:
            task.cancel()
        for task in done:
            with suppress(Exception):
                await task
        for task in pending:
            with suppress(asyncio.CancelledError, Exception):
                await task

        if proc.returncode is None:
            with suppress(ProcessLookupError):
                proc.terminate()
            with suppress(Exception):
                await asyncio.wait_for(proc.wait(), timeout=2)
        if proc.returncode is None:
            with suppress(ProcessLookupError):
                proc.kill()
            with suppress(Exception):
                await proc.wait()


def run_proxy_daemon(state_path: str | Path) -> None:
    state = load_state(state_path)
    if state is None:
        raise RuntimeError(f"coordlab state not found: {state_path}")
    if not state.proxies:
        raise RuntimeError("coordlab state contains no proxy definitions")

    normalized: dict[str, ProxyInfo] = {}
    for name, spec in state.proxies.items():
        target_pid = state.namespaces[spec.target_ns].pid
        normalized[name] = ProxyInfo(
            listen_host=spec.listen_host,
            host_port=spec.host_port,
            target_ns=str(target_pid),
            target_host=spec.target_host,
            target_port=spec.target_port,
        )

    asyncio.run(ProxyDaemon(normalized).run())
