"""Shared pytest fixtures for install-daemon.sh black-box tests.

These tests exercise scripts/setup/install-daemon.sh end-to-end via subprocess,
with a fake HOME, a fake `uv` binary stub, and a fake bundled daemon source
tree. Network paths (Phase 1's python-build-standalone fetch) are exercised
only via the "skip" sentinel path — every test pre-creates the per-phase
sentinel so we never touch real network during the suite.

PATH-injected stub binaries (fake_path_dir fixture) let individual tests
override `xcode-select`, `df`, or `curl` without modifying install-daemon.sh.
"""

from __future__ import annotations

import hashlib
import os
import subprocess
import time
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

import pytest

# Resolve once at import time so cwd churn during tests doesn't break us.
REPO_ROOT: Path = Path(__file__).resolve().parents[3]
INSTALL_DAEMON_SCRIPT: Path = REPO_ROOT / "scripts" / "setup" / "install-daemon.sh"
SETUP_VERSION: str = "0.2.0"


# ---------------------------------------------------------------------------
# Result container
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class ScriptResult:
    """Black-box result of invoking install-daemon.sh."""

    returncode: int
    stdout: str
    stderr: str
    elapsed_seconds: float
    home: Path

    def phase_markers(self) -> list[str]:
        """Lines from stderr that begin with one of the PHASE_* prefixes."""
        prefixes = (
            "PHASE: ",
            "PHASE_PROGRESS: ",
            "PHASE_BYTES: ",
            "PHASE_ETA: ",
            "PHASE_DONE: ",
            "PHASE_ERROR: ",
        )
        return [
            line for line in self.stderr.splitlines()
            if line.startswith(prefixes)
        ]


# ---------------------------------------------------------------------------
# Fixtures: throw-away HOME, fake uv binary, fake daemon source
# ---------------------------------------------------------------------------


@pytest.fixture
def fake_home(tmp_path: Path) -> Path:
    """Returns a tmp dir to be used as $HOME for the script."""
    home = tmp_path / "home"
    home.mkdir()
    return home


@pytest.fixture
def jarvis_home(fake_home: Path) -> Path:
    """Convenience: the script's $HOME/.jarvis directory (auto-created)."""
    jh = fake_home / ".jarvis"
    jh.mkdir(parents=True, exist_ok=True)
    return jh


@pytest.fixture
def fake_uv(tmp_path: Path) -> Path:
    """A fake `uv` binary that:
       - on `uv venv <dir> ...`  : creates <dir>/bin/python3 (executable stub)
       - on `uv pip install ...` : succeeds (no-op)
       - on anything else        : echoes args to stderr and exits 0
    """
    uv = tmp_path / "fake-uv"
    uv.write_text(
        "#!/usr/bin/env bash\n"
        "set -euo pipefail\n"
        "echo \"fake-uv: $*\" >&2\n"
        "case \"${1:-}\" in\n"
        "  venv)\n"
        "    target=\"${2:-}\"\n"
        "    if [[ -z \"${target}\" ]]; then\n"
        "      echo 'fake-uv venv: missing target dir' >&2\n"
        "      exit 1\n"
        "    fi\n"
        "    mkdir -p \"${target}/bin\"\n"
        "    cat >\"${target}/bin/python3\" <<'PY'\n"
        "#!/usr/bin/env bash\n"
        "echo \"Python 3.13.13 (fake)\"\n"
        "PY\n"
        "    chmod +x \"${target}/bin/python3\"\n"
        "    : >\"${target}/pyvenv.cfg\"\n"
        "    ;;\n"
        "  pip)\n"
        "    # uv pip install ... — succeed with no side effects.\n"
        "    ;;\n"
        "  *)\n"
        "    ;;\n"
        "esac\n"
        "exit 0\n"
    )
    uv.chmod(0o755)
    return uv


@pytest.fixture
def fake_daemon_src(tmp_path: Path) -> Path:
    """A minimal bundled daemon source tree: just requirements.txt + main.py."""
    src = tmp_path / "daemon-src"
    src.mkdir()
    (src / "requirements.txt").write_text("pipecat-ai==0.1.0\nnumpy==2.0.0\n")
    (src / "main.py").write_text("# stub daemon entry point\n")
    return src


@pytest.fixture
def fake_path_dir(tmp_path: Path) -> Path:
    """Empty dir prepended to PATH. Tests drop stub binaries here to override
    selected tools (xcode-select, df, etc.) without affecting the rest of PATH.
    """
    p = tmp_path / "stubs"
    p.mkdir()
    return p


# ---------------------------------------------------------------------------
# Sentinel seed helpers — exported as fixtures so tests can call them.
# ---------------------------------------------------------------------------


def _seed_python_sentinel(jarvis_home_dir: Path) -> None:
    """Pre-create the python_install per-phase sentinel + a stub python3.

    Phase 1 then takes the skip path:
      `check_sentinel "${PYTHON_SENTINEL}" && [[ -x "${PYTHON_BIN}" ]]`
    """
    python_dir = jarvis_home_dir / "python"
    bin_dir = python_dir / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    py3 = bin_dir / "python3"
    py3.write_text(
        "#!/usr/bin/env bash\n"
        "echo 'Python 3.13.13 (stub)'\n"
        "exit 0\n"
    )
    py3.chmod(0o755)
    sentinel = python_dir / ".fetch-complete"
    sentinel.write_text(
        "pbs_tag=20260510\n"
        "cpython_version=3.13.13\n"
        "sha256=stub-sha-not-validated-by-script\n"
    )


def _seed_venv_sentinel(jarvis_home_dir: Path, requirements_path: Path) -> None:
    """Pre-create the venv_install per-phase sentinel + matching requirements sha.

    Phase 2 then takes the skip path. The recorded sha MUST match the actual
    sha of requirements.txt or the skip path bails.
    """
    venv_dir = jarvis_home_dir / "jarvis-daemon-env"
    bin_dir = venv_dir / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    py3 = bin_dir / "python3"
    py3.write_text(
        "#!/usr/bin/env bash\n"
        "echo 'Python 3.13.13 (stub venv)'\n"
        "exit 0\n"
    )
    py3.chmod(0o755)

    req_sha = hashlib.sha256(requirements_path.read_bytes()).hexdigest()
    (venv_dir / ".requirements-sha256").write_text(req_sha + "\n")

    (venv_dir / ".venv-complete").write_text(
        f"requirements_sha256={req_sha}\n"
        "uv_binary=/fake/uv\n"
    )


def _seed_final_sentinel(jarvis_home_dir: Path) -> None:
    """Pre-create the v0.2.0 final sentinel so main() takes the fast-path."""
    (jarvis_home_dir / f".setup-version-{SETUP_VERSION}").write_text(
        f"version: {SETUP_VERSION}\n"
        "timestamp: 2026-05-12T00:00:00Z\n"
        "requirements_sha256: stub\n"
        "python_pbs_tag: 20260510\n"
    )


@pytest.fixture
def seed_python_sentinel() -> Callable[[Path], None]:
    return _seed_python_sentinel


@pytest.fixture
def seed_venv_sentinel() -> Callable[[Path, Path], None]:
    return _seed_venv_sentinel


@pytest.fixture
def seed_final_sentinel() -> Callable[[Path], None]:
    return _seed_final_sentinel


# ---------------------------------------------------------------------------
# Runner: invoke install-daemon.sh with controlled env + capture stdio
# ---------------------------------------------------------------------------


def _build_path(extra_dirs: list[Path] | None = None) -> str:
    """Build a PATH string. extra_dirs are prepended (highest priority)."""
    base = os.environ.get("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
    if not extra_dirs:
        return base
    return os.pathsep.join([str(d) for d in extra_dirs] + [base])


@pytest.fixture
def run_install_daemon(
    fake_home: Path,
    fake_uv: Path,
    fake_daemon_src: Path,
    fake_path_dir: Path,
) -> Callable[..., ScriptResult]:
    """Returns a callable that runs install-daemon.sh against fake_home.

    Keyword args:
      env_overrides: dict[str, str] — extra env vars
      path_prepend:  list[Path]      — additional stub dirs prepended to PATH
      timeout:       float           — subprocess timeout (default 30s)
    """
    def _run(
        env_overrides: dict[str, str] | None = None,
        path_prepend: list[Path] | None = None,
        timeout: float = 30.0,
    ) -> ScriptResult:
        env: dict[str, str] = dict(os.environ)
        env["HOME"] = str(fake_home)

        extra_dirs: list[Path] = [fake_path_dir]
        if path_prepend:
            extra_dirs.extend(path_prepend)
        env["PATH"] = _build_path(extra_dirs)

        if env_overrides:
            for k, v in env_overrides.items():
                env[k] = v

        start = time.monotonic()
        proc = subprocess.run(
            ["bash", str(INSTALL_DAEMON_SCRIPT),
             str(fake_uv), str(fake_daemon_src)],
            env=env,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
        elapsed = time.monotonic() - start

        return ScriptResult(
            returncode=proc.returncode,
            stdout=proc.stdout,
            stderr=proc.stderr,
            elapsed_seconds=elapsed,
            home=fake_home,
        )

    return _run


# ---------------------------------------------------------------------------
# PATH-stub builders for tests
# ---------------------------------------------------------------------------


def write_executable_stub(path: Path, body: str) -> Path:
    """Write a bash script to `path` and chmod +x. Returns path."""
    if not body.startswith("#!"):
        body = f"#!/usr/bin/env bash\n{body}"
    if not body.endswith("\n"):
        body = body + "\n"
    path.write_text(body)
    path.chmod(0o755)
    return path


@pytest.fixture
def make_stub() -> Callable[[Path, str, str], Path]:
    """Returns a helper that writes a stub binary in a given dir."""
    def _make(dir_: Path, name: str, body: str) -> Path:
        return write_executable_stub(dir_ / name, body)
    return _make


# ---------------------------------------------------------------------------
# Skip markers — re-exportable
# ---------------------------------------------------------------------------


requires_arm64 = pytest.mark.skipif(
    os.uname().machine != "arm64",
    reason="install-daemon.sh requires Apple Silicon (arm64)",
)
