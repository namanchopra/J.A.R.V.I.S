"""Windows-only verification for ``recall_meeting`` + ``list_recent_meetings`` (TASK-047, v0.4.0).

The recall tools live in ``tools.ToolExecutor`` and are pure-Python +
filesystem-based (no Wails round-trip, no LLM call inside the tool
itself). They read markdown files from ``meetingNotesDir`` -- which on
Windows resolves to ``%USERPROFILE%\\.jarvis\\meetings``. The existing
``test_recall_last_meeting.py`` covers the cross-platform contract via
``tmp_path`` (which on Windows already returns a Windows-flavoured
``pathlib.WindowsPath``), so most of the work was already done. This
file ADDS explicit Windows-only verification that the three TASK-047
acceptance criteria hold on a real Windows runner:

  1. ``recall_meeting(filename="2026-06-10-test.md")`` reads the file
     when the notes directory is a Windows-style path
     (e.g. ``C:\\Users\\runner\\.jarvis\\meetings``).
  2. Path-traversal blocking treats ``..\\`` exactly the same as ``../``
     -- the implementation in ``tools._recall_meeting`` rejects both via
     a unified ``"/" in fname or "\\" in fname or ".." in fname`` check
     so we re-assert that contract here with Windows-flavoured payloads.
  3. When the notes directory does not exist, the tool returns a clear
     "meeting notes directory does not exist" error rather than crashing
     -- with a Windows-style path in the error message.

Platform model:
  - All tests are gated by ``requires_windows`` -- on macOS / Linux they
    skip with a clear reason, mirroring the ``test_daemon_startup_windows.py``
    pattern from TASK-013. The Windows CI matrix (TASK-018
    release-windows.yml) runs this file on ``windows-2022``.

  - We import ``tools`` directly (same as ``test_recall_last_meeting.py``)
    -- the conftest.py prepends the daemon source dir to ``sys.path`` so
    no package install is needed.

  - ``tmp_path`` is used for the notes directory rather than touching the
    real ``%USERPROFILE%\\.jarvis\\meetings``. On Windows CI tmp_path
    resolves to a path under ``C:\\Users\\runneradmin\\AppData\\Local\\Temp``
    which is structurally identical (Windows separators, drive letter,
    backslashes) to the real production path -- close enough to verify
    the Windows path-handling contract without touching the runner's
    real user profile.
"""

from __future__ import annotations

import asyncio
import sys
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock

import pytest

import tools


# ---------------------------------------------------------------------------
# Platform guard
# ---------------------------------------------------------------------------

IS_WINDOWS: bool = sys.platform.startswith("win")

requires_windows = pytest.mark.skipif(
    not IS_WINDOWS,
    reason="TASK-047 recall-tool Windows verification runs on Windows only "
    "(CI: windows-2022). Cross-platform coverage lives in "
    "test_recall_last_meeting.py.",
)


# ---------------------------------------------------------------------------
# Helpers (mirror test_recall_last_meeting.py so the two files stay in sync)
# ---------------------------------------------------------------------------


def _make_executor() -> tools.ToolExecutor:
    """Build a ``ToolExecutor`` with a no-op WS send.

    The recall tools never send over the WS so the AsyncMock is here
    purely to satisfy the constructor's type contract.
    """
    return tools.ToolExecutor(ws_send_fn=AsyncMock())


def _run(coro: Any) -> Any:
    """Tiny helper to drive a coroutine to completion under sync pytest."""
    return asyncio.run(coro)


# ---------------------------------------------------------------------------
# AC #1: recall_meeting(filename=...) reads the file on Windows paths
# ---------------------------------------------------------------------------


@requires_windows
def test_recall_meeting_reads_file_under_windows_path(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """``recall_meeting(filename="2026-06-10-test.md")`` reads the file
    when meetingNotesDir is a Windows-style absolute path.

    On Windows, ``tmp_path`` is a ``pathlib.WindowsPath`` with a drive
    letter + backslash separators (e.g.
    ``C:\\Users\\runneradmin\\AppData\\Local\\Temp\\pytest-of-runner\\...``).
    We pass it as a string to ``meetingNotesDir`` exactly the way the
    user's ``config.json`` would on a real machine, then assert the tool
    walks the Windows path correctly and returns the file body.
    """
    note = tmp_path / "2026-06-10-test.md"
    note.write_text(
        "# Daily sync\n\nDiscussed Windows port.\n", encoding="utf-8"
    )

    # Sanity: the path we're about to pass into config really IS a
    # Windows path. This is belt-and-braces; if pathlib ever changes its
    # mind about tmp_path's flavour on Windows we want a loud signal.
    assert isinstance(tmp_path, Path)
    assert "\\" in str(tmp_path), (
        f"expected backslash separators in tmp_path on Windows, got {tmp_path!r}"
    )

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._recall_meeting("2026-06-10-test.md"))

    assert result["error"] is None, (
        f"unexpected error on Windows path read: {result['error']!r}"
    )
    payload = result["result"]
    assert payload is not None
    assert payload["filename"] == "2026-06-10-test.md"
    assert "Windows port" in payload["content"]


# ---------------------------------------------------------------------------
# AC #2: path-traversal blocked for both ``../`` AND ``..\\``
# ---------------------------------------------------------------------------


@requires_windows
def test_recall_meeting_blocks_windows_traversal(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """``..\\`` (Windows-style traversal) is rejected exactly the same as
    ``../``.

    The implementation at ``tools._recall_meeting`` uses a unified
    ``"/" in fname or "\\" in fname or ".." in fname`` check, so any
    payload containing a backslash OR the parent-dir token is a hard
    reject. We exercise the Windows-specific variants here so a future
    regression that only stripped forward-slashes (POSIX-only thinking)
    fails loudly on Windows CI.

    The error message must include ``"invalid filename"`` so the LLM
    can phrase a sensible reply.
    """
    # Plant a real file so we can confirm the call didn't open anything
    # via the traversal payloads.
    sentinel = tmp_path / "real.md"
    sentinel.write_text("# Real meeting\n", encoding="utf-8")

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()

    # Windows-flavoured traversal payloads. Each must be hard-rejected
    # before any filesystem operation runs.
    windows_evils = (
        "..\\..\\Windows\\System32\\config\\SAM",
        "..\\sibling.md",
        "subdir\\nested.md",
        "C:\\Windows\\System32\\drivers\\etc\\hosts",
        "\\\\?\\C:\\Users\\runner\\secrets.md",  # UNC-style
    )
    for evil in windows_evils:
        result = _run(executor._recall_meeting(evil))
        assert result["result"] is None, (
            f"Windows traversal payload opened a file: {evil!r}"
        )
        assert result["error"] is not None
        assert "invalid filename" in result["error"], (
            f"unexpected error for Windows traversal {evil!r}: "
            f"{result['error']!r}"
        )


# ---------------------------------------------------------------------------
# AC #3 (failure case): missing notes dir returns helpful error
# ---------------------------------------------------------------------------


@requires_windows
def test_recall_meeting_missing_dir_returns_helpful_error_on_windows(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Missing notes directory on Windows returns a clear error rather
    than crashing or returning ``None`` silently.

    Re-asserts the cross-platform contract specifically on Windows so a
    regression that introduced a POSIX-only ``os.path.exists`` call (or
    similar) would surface here. The error string must include
    ``"does not exist"`` so the LLM can phrase "you don't have any
    meeting notes yet" rather than "internal error".
    """
    missing = tmp_path / "no-such-meetings-dir"
    assert not missing.exists()

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(missing)},
    )

    executor = _make_executor()

    # Both code paths (with filename + without) must surface the same
    # "does not exist" error -- the dir check happens before the
    # filename branch in tools._recall_meeting, so this is just defence
    # in depth.
    with_name = _run(executor._recall_meeting("2026-06-10-test.md"))
    assert with_name["result"] is None
    assert with_name["error"] is not None
    assert "does not exist" in with_name["error"]

    no_name = _run(executor._recall_meeting(None))
    assert no_name["result"] is None
    assert no_name["error"] is not None
    assert "does not exist" in no_name["error"]


# ---------------------------------------------------------------------------
# AC #3 sibling: list_recent_meetings also handles missing dir cleanly
# ---------------------------------------------------------------------------


@requires_windows
def test_list_recent_meetings_missing_dir_returns_helpful_error_on_windows(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """``list_recent_meetings`` mirrors recall_meeting's missing-dir
    handling on Windows.

    Both meeting-recall tools must surface the same error shape when the
    notes directory is missing, so the LLM doesn't have to special-case
    the two tools. ``test_recall_last_meeting.py`` already covers this
    cross-platform via ``tmp_path``; this test re-asserts on Windows
    explicitly so a Windows-only ``Path.exists()`` regression fails here.
    """
    missing = tmp_path / "no-such-meetings-dir"
    assert not missing.exists()

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(missing)},
    )

    executor = _make_executor()
    result = _run(executor._list_recent_meetings(limit=10))

    assert result["result"] is None
    assert result["error"] is not None
    assert "does not exist" in result["error"]


# ---------------------------------------------------------------------------
# Bonus: list_recent_meetings + recall_meeting roundtrip on a Windows path
# ---------------------------------------------------------------------------


@requires_windows
def test_recall_meeting_roundtrip_via_list_on_windows(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """End-to-end Windows happy path: list_recent_meetings -> pick one ->
    recall_meeting with that filename.

    This is the canonical interaction the LLM performs when the user
    says "what did we discuss yesterday?". Running it on a real Windows
    path catches subtle regressions where one tool serialises the path
    differently from the other (e.g. one uses ``str(path)``, the other
    uses ``path.as_posix()`` -- a difference that wouldn't show up on
    macOS).
    """
    # Populate two files; we'll recall the older one via filename to
    # prove the listing's filename is round-trippable.
    older = tmp_path / "2026-06-09-deploy-review.md"
    older.write_text(
        "# Deploy Review\n\nRolled forward to v0.4.0.\n", encoding="utf-8"
    )
    newer = tmp_path / "2026-06-10-standup.md"
    newer.write_text("# Standup\n\nQuick status sync.\n", encoding="utf-8")

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()

    listing = _run(executor._list_recent_meetings(limit=10))
    assert listing["error"] is None
    payload = listing["result"]
    assert payload is not None
    filenames = {m["filename"] for m in payload["meetings"]}
    assert "2026-06-09-deploy-review.md" in filenames
    assert "2026-06-10-standup.md" in filenames

    # Now recall the older one by name -- the filename string from the
    # listing must be directly usable.
    recalled = _run(executor._recall_meeting("2026-06-09-deploy-review.md"))
    assert recalled["error"] is None
    assert recalled["result"] is not None
    assert recalled["result"]["filename"] == "2026-06-09-deploy-review.md"
    assert "Rolled forward to v0.4.0" in recalled["result"]["content"]
