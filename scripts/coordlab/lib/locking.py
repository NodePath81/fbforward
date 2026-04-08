from __future__ import annotations

import fcntl
from collections.abc import Iterator
from contextlib import contextmanager
from pathlib import Path

CLIENT_MUTATION_LOCK_FILENAME = "client-mutation.lock"


def client_mutation_lock_path(workdir: Path) -> Path:
    return workdir / CLIENT_MUTATION_LOCK_FILENAME


@contextmanager
def acquire_client_mutation_lock(workdir: Path) -> Iterator[Path]:
    lock_path = client_mutation_lock_path(workdir)
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    handle = lock_path.open("a+", encoding="utf-8")
    try:
        try:
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise RuntimeError("client mutation already in progress") from exc
        yield lock_path
    finally:
        try:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
        finally:
            handle.close()
