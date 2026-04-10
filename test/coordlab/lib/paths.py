from __future__ import annotations

from pathlib import Path

from . import config as coordconfig

REPO_ROOT = Path(__file__).resolve().parents[3]
COORDLAB_SCRIPT = Path(__file__).resolve().parents[1] / "coordlab.py"
DEFAULT_WORKDIR = Path("/tmp/coordlab")
STATE_FILENAME = "state.json"
VENV_PYTHON = REPO_ROOT / ".venv/bin/python"
REQUIREMENTS_FILE = REPO_ROOT / "test/coordlab/requirements.txt"
CONFIGS_DIRNAME = "configs"
LOGS_DIRNAME = "logs"
RUNTIME_DIRNAME = coordconfig.FBCOORD_RUNTIME_DIR
FBNOTIFY_RUNTIME_DIRNAME = coordconfig.FBNOTIFY_RUNTIME_DIR


def state_path_for(workdir: Path) -> Path:
    return workdir / STATE_FILENAME


def configs_dir_for(workdir: Path) -> Path:
    return workdir / CONFIGS_DIRNAME


def logs_dir_for(workdir: Path) -> Path:
    return workdir / LOGS_DIRNAME


def runtime_dir_for(workdir: Path) -> Path:
    return workdir / RUNTIME_DIRNAME


def fbnotify_runtime_dir_for(workdir: Path) -> Path:
    return workdir / FBNOTIFY_RUNTIME_DIRNAME


def mmdb_dir_for(workdir: Path) -> Path:
    return coordconfig.mmdb_dir_for(workdir)


def data_dir_for(workdir: Path) -> Path:
    return coordconfig.data_dir_for(workdir)
