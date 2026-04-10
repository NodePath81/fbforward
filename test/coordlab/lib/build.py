from __future__ import annotations

import shutil
import tempfile
import urllib.request
from pathlib import Path

from . import config as coordconfig
from .env import require_tools
from .lab import run_host
from .paths import REPO_ROOT, VENV_PYTHON, mmdb_dir_for

FBFORWARD_BIN = REPO_ROOT / "build/bin/fbforward"
FBMEASURE_BIN = REPO_ROOT / "build/bin/fbmeasure"
FBCOORD_BUILD_SENTINEL = REPO_ROOT / "fbcoord/ui/dist/index.html"
FBNOTIFY_BUILD_SENTINEL = coordconfig.FBNOTIFY_BUILD_SENTINEL


def ensure_fbforward_binaries(skip_build: bool) -> None:
    if skip_build:
        missing = [str(path) for path in (FBFORWARD_BIN, FBMEASURE_BIN) if not path.exists()]
        if not missing:
            return
        raise RuntimeError(f"missing required binaries with --skip-build: {', '.join(missing)}")
    require_tools(["make"])
    run_host(["make", "build"], cwd=REPO_ROOT)


def ensure_fbcoord_assets(skip_build: bool) -> None:
    if not (coordconfig.FBCOORD_SOURCE_DIR / "node_modules").exists():
        raise RuntimeError("fbcoord/node_modules is missing; run `npm --prefix fbcoord install` before coordlab up")
    if skip_build:
        if not FBCOORD_BUILD_SENTINEL.exists():
            raise RuntimeError(f"missing fbcoord build output with --skip-build: {FBCOORD_BUILD_SENTINEL}")
        return
    require_tools(["npm"])
    run_host(["npm", "--prefix", "fbcoord", "run", "build"], cwd=REPO_ROOT)


def ensure_fbnotify_assets(skip_build: bool) -> None:
    if not (coordconfig.FBNOTIFY_SOURCE_DIR / "node_modules").exists():
        raise RuntimeError("fbnotify/node_modules is missing; run `npm --prefix fbnotify install` before coordlab up")
    if skip_build:
        if not FBNOTIFY_BUILD_SENTINEL.exists():
            raise RuntimeError(f"missing fbnotify build output with --skip-build: {FBNOTIFY_BUILD_SENTINEL}")
        return
    require_tools(["npm"])
    run_host(["npm", "--prefix", "fbnotify", "run", "build"], cwd=REPO_ROOT)


def download_geoip_mmdb(url: str, target: Path) -> Path:
    target.parent.mkdir(parents=True, exist_ok=True)
    temp_path: Path | None = None
    try:
        with urllib.request.urlopen(url, timeout=30) as response:
            status = getattr(response, "status", 200)
            if status != 200:
                raise RuntimeError(f"failed to download {url}: HTTP {status}")
            with tempfile.NamedTemporaryFile(dir=target.parent, prefix=f"{target.name}.", suffix=".tmp", delete=False) as tmp_file:
                shutil.copyfileobj(response, tmp_file)
                temp_path = Path(tmp_file.name)
        if temp_path is None:
            raise RuntimeError(f"failed to download {url}: temporary file was not created")
        temp_path.replace(target)
        return target
    except Exception as exc:
        if temp_path is not None and temp_path.exists():
            temp_path.unlink()
        if isinstance(exc, RuntimeError):
            raise
        raise RuntimeError(f"failed to download {url}: {exc}") from exc


def ensure_geoip_mmdbs(workdir: Path) -> dict[str, Path]:
    mmdb_dir = mmdb_dir_for(workdir)
    mmdb_dir.mkdir(parents=True, exist_ok=True)
    targets = {
        "asn": (coordconfig.GEOIP_ASN_DB_URL, mmdb_dir / coordconfig.GEOIP_ASN_DB_FILENAME),
        "country": (coordconfig.GEOIP_COUNTRY_DB_URL, mmdb_dir / coordconfig.GEOIP_COUNTRY_DB_FILENAME),
    }
    paths: dict[str, Path] = {}
    for key, (url, target) in targets.items():
        paths[key] = target if target.exists() else download_geoip_mmdb(url, target)
    return paths


def wrangler_command() -> list[str]:
    if shutil.which("wrangler"):
        run_host(["wrangler", "--version"], cwd=REPO_ROOT)
        return ["wrangler", "dev"]

    node = shutil.which("node")
    candidates = sorted(
        Path.home().glob(".npm/_npx/*/node_modules/wrangler/wrangler-dist/cli.js"),
        key=lambda path: path.stat().st_mtime,
        reverse=True,
    )
    if node is not None and candidates:
        return [node, str(candidates[0]), "dev"]

    if shutil.which("npx"):
        run_host(["npx", "--yes", "wrangler", "--version"], cwd=REPO_ROOT)
        node = shutil.which("node")
        if node is None:
            raise RuntimeError("node is required for the npx-based wrangler fallback")
        candidates = sorted(
            Path.home().glob(".npm/_npx/*/node_modules/wrangler/wrangler-dist/cli.js"),
            key=lambda path: path.stat().st_mtime,
            reverse=True,
        )
        if not candidates:
            raise RuntimeError("unable to locate cached wrangler CLI after npx warmup")
        return [node, str(candidates[0]), "dev"]
    raise RuntimeError("wrangler is not available and npx is missing")
