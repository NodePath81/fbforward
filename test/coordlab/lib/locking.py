from __future__ import annotations

import fcntl
from collections.abc import Iterator
from contextlib import contextmanager
from pathlib import Path

CLIENT_MUTATION_LOCK_FILENAME = "client-mutation.lock"
NETWORK_MUTATION_LOCK_FILENAME = "network-mutation.lock"


def client_mutation_lock_path(workdir: Path) -> Path:
    return workdir / CLIENT_MUTATION_LOCK_FILENAME


def network_mutation_lock_path(workdir: Path) -> Path:
    return workdir / NETWORK_MUTATION_LOCK_FILENAME


@contextmanager
def _acquire_lock(lock_path: Path, error_message: str) -> Iterator[Path]:
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    handle = lock_path.open("a+", encoding="utf-8")
    try:
        try:
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise RuntimeError(error_message) from exc
        yield lock_path
    finally:
        try:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
        finally:
            handle.close()


@contextmanager
def acquire_client_mutation_lock(workdir: Path) -> Iterator[Path]:
    with _acquire_lock(client_mutation_lock_path(workdir), "client mutation already in progress") as lock_path:
        yield lock_path


@contextmanager
def acquire_network_mutation_lock(workdir: Path) -> Iterator[Path]:
    with _acquire_lock(network_mutation_lock_path(workdir), "network mutation already in progress") as lock_path:
        yield lock_path
