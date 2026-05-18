"""Black-box pytest suite for scripts/setup/install-daemon.sh.

Each test runs the real script via subprocess against a throw-away $HOME,
asserting on its stderr PHASE_* contract + sentinel-file side effects.
Phase 1's network download is exercised only via the skip-path (sentinel
pre-created) so the suite stays offline + fast (<30s).

Covers:
  - preflight: arch, xcode-select, df (disk space)
  - phase_python skip path emits PHASE / PHASE_DONE
  - phase_venv runs only after phase_python
  - full-sentinel fast-path replays markers without re-running phases
  - idempotency: second run is fast and writes no extra phase work
  - final sentinel contains the expected setup version + fields
  - phase_python downloads SHA mismatch -> PHASE_ERROR
"""

from __future__ import annotations

import hashlib
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Callable

import pytest


# These mirror conftest.SETUP_VERSION / conftest.requires_arm64 — duplicated
# here so the module doesn't depend on conftest being importable as a normal
# module. (pytest auto-loads conftest fixtures; that's what matters.)
SETUP_VERSION: str = "0.2.0"

requires_arm64 = pytest.mark.skipif(
    os.uname().machine != "arm64",
    reason="install-daemon.sh requires Apple Silicon (arm64)",
)


@dataclass(frozen=True)
class _ScriptResultProto:
    """Type-hint stand-in for conftest.ScriptResult — the fixture builds the
    real instance and pytest injects it. We never construct one here."""
    returncode: int
    stdout: str
    stderr: str
    elapsed_seconds: float
    home: Path


# ---------------------------------------------------------------------------
# Small assertion helpers
# ---------------------------------------------------------------------------


def _find_marker_index(stderr_lines: list[str], marker: str) -> int:
    """Return the line index of the first occurrence of marker, or -1."""
    for i, line in enumerate(stderr_lines):
        if line.strip() == marker:
            return i
    return -1


def _assert_phase_pair(result: _ScriptResultProto, phase: str) -> None:
    """Assert stderr contains a PHASE / PHASE_DONE pair for `phase` in order."""
    lines = result.stderr.splitlines()
    start = _find_marker_index(lines, f"PHASE: {phase}")
    done = _find_marker_index(lines, f"PHASE_DONE: {phase}")
    assert start != -1, (
        f"missing 'PHASE: {phase}' in stderr.\n--- STDERR ---\n{result.stderr}"
    )
    assert done != -1, (
        f"missing 'PHASE_DONE: {phase}' in stderr.\n--- STDERR ---\n{result.stderr}"
    )
    assert done > start, (
        f"'PHASE_DONE: {phase}' (idx {done}) must come after "
        f"'PHASE: {phase}' (idx {start}).\n--- STDERR ---\n{result.stderr}"
    )


# ---------------------------------------------------------------------------
# Preflight: positive (arch check passes on arm64)
# ---------------------------------------------------------------------------


@requires_arm64
def test_preflight_apple_silicon_check(
    run_install_daemon: Callable[..., _ScriptResultProto],
    jarvis_home: Path,
    seed_python_sentinel: Callable[[Path], None],
    seed_venv_sentinel: Callable[[Path, Path], None],
    seed_final_sentinel: Callable[[Path], None],
    fake_daemon_src: Path,
) -> None:
    """On arm64 the preflight passes — no arch error emitted.

    To keep the test offline we seed all sentinels so main() takes the
    fast-path after preflight succeeds.
    """
    seed_python_sentinel(jarvis_home)
    seed_venv_sentinel(jarvis_home, fake_daemon_src / "requirements.txt")
    seed_final_sentinel(jarvis_home)

    result = run_install_daemon()
    assert result.returncode == 0, (
        f"expected 0, got {result.returncode}\nSTDERR:\n{result.stderr}"
    )
    assert "PHASE_ERROR" not in result.stderr
    # If preflight had detected a non-arm64 host it would have emitted this:
    assert "requires Apple Silicon" not in result.stderr


# ---------------------------------------------------------------------------
# Preflight: xcode-select missing
# ---------------------------------------------------------------------------


def test_preflight_xcode_cli_missing_emits_phase_error(
    run_install_daemon: Callable[..., _ScriptResultProto],
    fake_path_dir: Path,
    make_stub: Callable[[Path, str, str], Path],
) -> None:
    """When `xcode-select -p` exits non-zero, preflight emits PHASE_ERROR + exit 1.

    We hide the real xcode-select by dropping a stub earlier in PATH that
    exits 1 (mimicking a system without CLI tools).
    """
    make_stub(fake_path_dir, "xcode-select", "exit 1\n")
    result = run_install_daemon()

    assert result.returncode != 0
    assert "PHASE: python_install" in result.stderr
    assert "PHASE_ERROR" in result.stderr
    assert "Xcode Command Line Tools" in result.stderr


# ---------------------------------------------------------------------------
# Preflight: insufficient disk
# ---------------------------------------------------------------------------


def test_disk_space_check_fails_with_low_disk(
    run_install_daemon: Callable[..., _ScriptResultProto],
    fake_path_dir: Path,
    make_stub: Callable[[Path, str, str], Path],
) -> None:
    """A PATH-injected fake `df` that reports 100 MB free triggers PHASE_ERROR.

    The script parses `df -k $HOME` and pulls col 4 of row 2 as "available KB".
    Our stub prints a fixed header + one data row with 102400 KB (100 MB)
    available, well below the 4 GB threshold.
    """
    make_stub(
        fake_path_dir,
        "df",
        "echo 'Filesystem     1024-blocks      Used Available Capacity iused ifree %iused  Mounted on'\n"
        "echo '/dev/fake         500000000 400000000    102400      99%     0     0   100% /'\n"
        "exit 0\n",
    )

    result = run_install_daemon()

    assert result.returncode != 0
    assert "PHASE_ERROR" in result.stderr
    assert "insufficient disk space" in result.stderr
    # Phase context for the error: the script sets it to python_install.
    assert "PHASE: python_install" in result.stderr


# ---------------------------------------------------------------------------
# Phase python: skip path on sentinel present
# ---------------------------------------------------------------------------


@requires_arm64
def test_phase_python_emits_correct_markers_on_skip(
    run_install_daemon: Callable[..., _ScriptResultProto],
    jarvis_home: Path,
    seed_python_sentinel: Callable[[Path], None],
) -> None:
    """Pre-create the python sentinel + interpreter — phase 1 must take the
    skip path: emit PHASE / PROGRESS 100 / DONE with no real download.

    Phase 2 will then attempt to run; we don't care about its outcome here,
    we just assert phase_python's marker contract on the skip path.
    """
    seed_python_sentinel(jarvis_home)

    result = run_install_daemon()

    stderr_lines = result.stderr.splitlines()
    phase_idx = _find_marker_index(stderr_lines, "PHASE: python_install")
    done_idx = _find_marker_index(stderr_lines, "PHASE_DONE: python_install")
    assert phase_idx != -1, f"missing PHASE: python_install\n{result.stderr}"
    assert done_idx != -1, f"missing PHASE_DONE: python_install\n{result.stderr}"
    assert done_idx > phase_idx

    # Between PHASE and PHASE_DONE for python_install, only one PROGRESS:100
    # should appear (the skip-path emission). No PHASE_BYTES / PHASE_ETA
    # because those imply we were polling a real download.
    between = stderr_lines[phase_idx:done_idx + 1]
    progress_lines = [l for l in between if l.startswith("PHASE_PROGRESS:")]
    assert progress_lines == ["PHASE_PROGRESS: 100"], (
        f"expected exactly one PROGRESS:100 on skip path, got {progress_lines}\n"
        f"STDERR:\n{result.stderr}"
    )
    assert not any(l.startswith("PHASE_BYTES:") for l in between), (
        "PHASE_BYTES should NOT be emitted on the python skip path"
    )
    assert not any(l.startswith("PHASE_ETA:") for l in between), (
        "PHASE_ETA should NOT be emitted on the python skip path"
    )


# ---------------------------------------------------------------------------
# Phase ordering: venv comes after python
# ---------------------------------------------------------------------------


@requires_arm64
def test_phase_venv_runs_after_phase_python_completes(
    run_install_daemon: Callable[..., _ScriptResultProto],
    jarvis_home: Path,
    seed_python_sentinel: Callable[[Path], None],
) -> None:
    """Markers MUST appear in order: PHASE python -> PHASE_DONE python ->
    PHASE venv -> PHASE_DONE venv. The script must NOT emit a venv marker
    before python is done.
    """
    seed_python_sentinel(jarvis_home)

    result = run_install_daemon()
    assert result.returncode == 0, (
        f"script failed unexpectedly:\n{result.stderr}"
    )

    lines = result.stderr.splitlines()
    p_phase = _find_marker_index(lines, "PHASE: python_install")
    p_done = _find_marker_index(lines, "PHASE_DONE: python_install")
    v_phase = _find_marker_index(lines, "PHASE: venv_install")
    v_done = _find_marker_index(lines, "PHASE_DONE: venv_install")

    assert p_phase != -1 and p_done != -1, "python markers missing"
    assert v_phase != -1 and v_done != -1, "venv markers missing"
    assert p_phase < p_done < v_phase < v_done, (
        f"ordering wrong: python_phase={p_phase} python_done={p_done} "
        f"venv_phase={v_phase} venv_done={v_done}\nSTDERR:\n{result.stderr}"
    )


# ---------------------------------------------------------------------------
# Full happy path: final sentinel written with correct contents
# ---------------------------------------------------------------------------


@requires_arm64
def test_full_run_writes_setup_version_sentinel(
    run_install_daemon: Callable[..., _ScriptResultProto],
    jarvis_home: Path,
    fake_daemon_src: Path,
    seed_python_sentinel: Callable[[Path], None],
) -> None:
    """End-to-end (heavily mocked) happy path: assert ~/.jarvis/.setup-version-0.2.0
    exists with the documented fields after a successful run.

    The python phase takes the skip-path (sentinel pre-seeded); the venv phase
    runs against the fake uv binary which fabricates a bin/python3 inside the
    venv dir. The script's final write_sentinel() then runs.
    """
    seed_python_sentinel(jarvis_home)

    result = run_install_daemon()
    assert result.returncode == 0, f"script failed:\n{result.stderr}"

    sentinel = jarvis_home / f".setup-version-{SETUP_VERSION}"
    assert sentinel.is_file(), (
        f"final sentinel missing at {sentinel}\nSTDERR:\n{result.stderr}"
    )

    contents = sentinel.read_text()
    assert f"version: {SETUP_VERSION}" in contents
    assert "timestamp: " in contents
    assert "requirements_sha256: " in contents
    assert "python_pbs_tag: 20260510" in contents

    # The recorded requirements sha must match the actual file we shipped.
    expected_sha = hashlib.sha256(
        (fake_daemon_src / "requirements.txt").read_bytes()
    ).hexdigest()
    assert f"requirements_sha256: {expected_sha}" in contents, (
        f"sha mismatch in sentinel.\nExpected: {expected_sha}\n"
        f"Sentinel contents:\n{contents}"
    )

    # Daemon source must have been rsync'd to ~/.jarvis/jarvis-daemon/
    daemon_dst = jarvis_home / "jarvis-daemon"
    assert (daemon_dst / "requirements.txt").is_file()
    assert (daemon_dst / "main.py").is_file()


# ---------------------------------------------------------------------------
# Idempotency: second run is a fast no-op
# ---------------------------------------------------------------------------


@requires_arm64
def test_idempotency_second_run_is_noop(
    run_install_daemon: Callable[..., _ScriptResultProto],
    jarvis_home: Path,
    seed_python_sentinel: Callable[[Path], None],
) -> None:
    """After a successful first run, a second run must:
       - exit 0
       - finish quickly (< 5s — generous bound for CI noise)
       - emit the replayed PHASE/DONE markers from main()'s fast-path
       - NOT emit PHASE_BYTES (proving no download happened)
    """
    seed_python_sentinel(jarvis_home)

    # First run — establishes the final sentinel via the normal path.
    first = run_install_daemon()
    assert first.returncode == 0, f"first run failed:\n{first.stderr}"
    sentinel = jarvis_home / f".setup-version-{SETUP_VERSION}"
    assert sentinel.is_file()

    # Second run — fast-path replay.
    second = run_install_daemon()
    assert second.returncode == 0
    assert second.elapsed_seconds < 5.0, (
        f"second run took {second.elapsed_seconds:.2f}s — fast-path should be <1s"
    )
    _assert_phase_pair(second, "python_install")
    _assert_phase_pair(second, "venv_install")
    assert "PHASE_BYTES" not in second.stderr, (
        "second run must not download anything"
    )
    assert "PHASE_ERROR" not in second.stderr


# ---------------------------------------------------------------------------
# Phase python: SHA mismatch on downloaded tarball
# ---------------------------------------------------------------------------


def test_phase_python_sha_mismatch_emits_error(
    run_install_daemon: Callable[..., _ScriptResultProto],
    fake_path_dir: Path,
    make_stub: Callable[[Path, str, str], Path],
) -> None:
    """Drive Phase 1's real download path with a stub `curl` that serves:
       - a SHA256SUMS file claiming a specific hash for the asset
       - a tarball whose ACTUAL hash differs from that claim

    The script must:
       - successfully start the download (no preflight failure)
       - detect the SHA mismatch after the download completes
       - emit PHASE_ERROR with a "SHA256 mismatch" message
       - exit non-zero
    """
    # The asset filename the script will look for in SHA256SUMS.
    pbs_asset = "cpython-3.13.13+20260510-aarch64-apple-darwin-install_only.tar.gz"
    # A SHA we'll write into SHA256SUMS that won't match the tarball bytes.
    fake_sha = "0" * 64
    # Tarball "content" — anything non-empty whose sha won't be 64 zeros.
    fake_tarball_content = "this is definitely not a valid tarball"

    # The stub curl introspects $@ to figure out which URL is being fetched
    # and which output file (-o / --output) to write. The script invokes
    # curl with `--head` for content-length probing — we handle that too.
    stub_body = (
        "#!/usr/bin/env bash\n"
        "set -e\n"
        "\n"
        "# Capture every invocation so the test can debug failures.\n"
        "echo \"fake-curl: $*\" >&2\n"
        "\n"
        "is_head=0\n"
        "out=\"\"\n"
        "url=\"\"\n"
        "while [[ $# -gt 0 ]]; do\n"
        "  case \"$1\" in\n"
        "    --head|-I) is_head=1 ;;\n"
        "    -o|--output) out=\"$2\"; shift ;;\n"
        "    http*) url=\"$1\" ;;\n"
        "  esac\n"
        "  shift\n"
        "done\n"
        "\n"
        "if [[ \"$is_head\" -eq 1 ]]; then\n"
        f"  echo 'HTTP/2 200'\n"
        f"  echo 'Content-Length: {len(fake_tarball_content)}'\n"
        "  echo ''\n"
        "  exit 0\n"
        "fi\n"
        "\n"
        "case \"$url\" in\n"
        "  *SHA256SUMS)\n"
        f"    printf '%s  %s\\n' '{fake_sha}' '{pbs_asset}' >\"${{out:-/dev/stdout}}\"\n"
        "    exit 0\n"
        "    ;;\n"
        f"  *{pbs_asset})\n"
        f"    printf '%s' '{fake_tarball_content}' >\"${{out:-/dev/stdout}}\"\n"
        "    exit 0\n"
        "    ;;\n"
        "esac\n"
        "\n"
        "echo \"fake-curl: unrecognised URL $url\" >&2\n"
        "exit 22\n"
    )
    make_stub(fake_path_dir, "curl", stub_body)

    result = run_install_daemon(timeout=15.0)

    assert result.returncode != 0, (
        f"expected non-zero exit on SHA mismatch.\nSTDERR:\n{result.stderr}"
    )
    assert "PHASE: python_install" in result.stderr
    assert "PHASE_ERROR" in result.stderr
    assert "SHA256 mismatch" in result.stderr, (
        f"expected SHA256 mismatch error.\nSTDERR:\n{result.stderr}"
    )
