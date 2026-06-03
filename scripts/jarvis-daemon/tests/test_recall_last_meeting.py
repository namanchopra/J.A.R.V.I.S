"""Tests for the ``recall_last_meeting`` tool branch in tools.py.

The tool is pure-local: it reads markdown files from the configured
``meetingNotesDir`` and returns the most-recently-modified one's full
content along with metadata. No Wails round-trip, no LLM call inside
the tool itself.

These tests follow the same sync-pytest pattern as
``test_meeting_handlers.py`` -- the daemon venv ships pytest 9 without
``pytest-asyncio``, so coroutines are driven by ``asyncio.run``.
"""

from __future__ import annotations

import asyncio
import os
import stat as stat_mod
import time
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock

import pytest

import tools


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_executor() -> tools.ToolExecutor:
    """Build a ``ToolExecutor`` with a no-op WS send.

    ``recall_last_meeting`` never sends over the WS so the AsyncMock is
    here purely to satisfy the constructor's type contract.
    """
    return tools.ToolExecutor(ws_send_fn=AsyncMock())


def _run(coro: Any) -> Any:
    """Tiny helper to drive a coroutine to completion under sync pytest."""
    return asyncio.run(coro)


# ---------------------------------------------------------------------------
# Test 1: returns the most-recently-modified .md file
# ---------------------------------------------------------------------------


def test_returns_content_of_most_recently_modified_md(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Two markdown files in the notes dir -- the newer one wins.

    Mtime ordering is the contract documented on
    ``_recall_last_meeting``: we sort by ``p.stat().st_mtime`` reverse,
    so the most recently modified file ends up at index 0. We force
    ordering explicitly via ``os.utime`` to avoid relying on filesystem
    write order (some CI filesystems coalesce mtimes within a tick).
    """
    older = tmp_path / "2026-05-26-old.md"
    older.write_text("# Old meeting\nDiscussed cookies.", encoding="utf-8")

    newer = tmp_path / "2026-05-27-new.md"
    newer.write_text(
        "# New meeting\nAction items: deploy on Tuesday",
        encoding="utf-8",
    )

    # Set explicit mtimes so the sort is deterministic.
    now = time.time()
    os.utime(older, (now - 3600, now - 3600))
    os.utime(newer, (now, now))

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._recall_last_meeting())

    assert result["error"] is None
    assert result["result"] is not None
    assert result["result"]["filename"] == "2026-05-27-new.md"
    assert "Action items: deploy on Tuesday" in result["result"]["content"]


# ---------------------------------------------------------------------------
# Test 2: missing notes directory -> user-actionable error
# ---------------------------------------------------------------------------


def test_returns_error_when_dir_missing(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """A configured but non-existent directory must NOT raise -- the user
    might have just installed the build and not held a meeting yet.

    The error string includes "does not exist" so the LLM can phrase a
    friendly answer ("you don't have any meeting notes yet").
    """
    missing = tmp_path / "this-path-does-not-exist"
    assert not missing.exists()

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(missing)},
    )

    executor = _make_executor()
    result = _run(executor._recall_last_meeting())

    assert result["result"] is None
    assert result["error"] is not None
    assert "does not exist" in result["error"]


# ---------------------------------------------------------------------------
# Test 3: empty directory -> guidance for the user
# ---------------------------------------------------------------------------


def test_returns_error_when_dir_is_empty(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """An empty notes directory returns a guidance string the LLM can
    relay verbatim to the user.

    ``tmp_path`` exists (pytest creates it) but has no ``*.md`` files.
    The branch must return an error rather than blow up on
    ``md_files[0]``.
    """
    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._recall_last_meeting())

    assert result["result"] is None
    assert result["error"] is not None
    # The phrasing in tools.py says "no meeting notes found yet" so a
    # regression that drops the guidance text fails loudly here.
    assert "no meeting notes" in result["error"].lower()


# ---------------------------------------------------------------------------
# Test 4: success result includes filename + metadata + content
# ---------------------------------------------------------------------------


def test_returns_filename_and_metadata_alongside_content(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """The success-path result has all four keys populated.

    The LLM uses ``filename`` and ``modified_at`` to ground its reply
    ("In your sync from yesterday at 3pm..."), and ``content`` for the
    actual Q&A. ``size_bytes`` is informational but cheap to include.
    """
    note = tmp_path / "2026-06-01-12-00-standup.md"
    body = "# Standup\n\n## Summary\nDiscussed release plan.\n"
    note.write_text(body, encoding="utf-8")

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._recall_last_meeting())

    assert result["error"] is None
    payload = result["result"]
    assert payload is not None

    # All four documented keys present and non-empty.
    assert payload["filename"] == "2026-06-01-12-00-standup.md"
    assert payload["modified_at"]
    # ISO 8601 UTC timestamp shape: "...T...+00:00".
    assert "T" in payload["modified_at"]
    assert payload["modified_at"].endswith("+00:00")
    assert payload["size_bytes"] == len(body.encode("utf-8"))
    assert payload["content"] == body


# ---------------------------------------------------------------------------
# Test 5: unreadable file -> graceful error, no exception bubbles
# ---------------------------------------------------------------------------


def test_handles_unreadable_file_gracefully(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """An unreadable file (chmod 000) must surface a friendly error
    string rather than letting the ``OSError`` propagate.

    Skipped on platforms where the test process can still read the file
    after chmod -- typically when running as root (CI containers). The
    branch is still exercised on developer machines where the daemon
    runs as a normal user.
    """
    note = tmp_path / "locked.md"
    note.write_text("# Secret meeting", encoding="utf-8")
    os.chmod(note, 0o000)

    try:
        # Verify the chmod actually blocks reads in this environment.
        # If we can still read (e.g. running as root), skip the test
        # rather than emit a false negative.
        try:
            note.read_text(encoding="utf-8")
            pytest.skip("chmod 000 doesn't block reads in this environment")
        except OSError:
            pass  # Expected -- file is now unreadable.

        monkeypatch.setattr(
            tools,
            "_load_config_safe_local",
            lambda: {"meetingNotesDir": str(tmp_path)},
        )

        executor = _make_executor()
        result = _run(executor._recall_last_meeting())

        assert result["result"] is None
        assert result["error"] is not None
        assert "locked.md" in result["error"] or "failed to read" in result["error"]
    finally:
        # Restore perms so pytest can clean up tmp_path on teardown.
        os.chmod(note, stat_mod.S_IRUSR | stat_mod.S_IWUSR)


# ---------------------------------------------------------------------------
# Bonus: tool is registered in the public surface
# ---------------------------------------------------------------------------


def test_tool_is_registered_in_tool_names() -> None:
    """``recall_last_meeting`` shows up in ``get_tool_names()`` so the
    Anthropic / Ollama tool-schema converters pick it up automatically.
    """
    assert "recall_last_meeting" in tools.get_tool_names()


def test_tool_is_registered_in_pipecat_llm_tools() -> None:
    """``recall_last_meeting`` is present in the LLM-visible tools list.

    Per ``project_jarvis_daemon_layout.md`` the canonical tool list the
    LLM sees lives in ``pipecat_llm.TOOLS``; tools must be added there
    too or the LLM won't know they exist.

    Note: the alias is now joined by ``recall_meeting`` and
    ``list_recent_meetings`` (see the meeting-tool tests below).
    """
    import pipecat_llm

    names = [t["name"] for t in pipecat_llm.TOOLS]
    assert "recall_last_meeting" in names


# ===========================================================================
# list_recent_meetings tests
# ===========================================================================
# Tests for the second meeting-recall tool: returns metadata for the N
# most-recent meeting notes so the LLM can pick one by title or date
# before calling recall_meeting. The implementation lives in
# ``ToolExecutor._list_recent_meetings``.
# ===========================================================================


def _touch_with_mtime(path: Path, mtime: float) -> None:
    """Write a tiny placeholder body and force the mtime.

    Filesystems coalesce mtimes within a tick on some platforms (notably
    APFS at sub-second resolution), so the stagger has to be explicit.
    """
    if not path.exists():
        path.write_text(f"# {path.stem}\nbody\n", encoding="utf-8")
    os.utime(path, (mtime, mtime))


def test_list_recent_meetings_returns_n_most_recent(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Five files with staggered mtimes; ``limit=3`` returns the 3 newest
    in newest-first order.

    Establishes the core contract: results are sorted by mtime descending
    and capped at ``limit``. A regression that reversed the sort or
    miscounted the slice would fail here.
    """
    now = time.time()
    # Create 5 files with mtimes -5h ... -1h. Index 4 is the newest.
    files = []
    for i in range(5):
        f = tmp_path / f"meeting-{i:02d}.md"
        f.write_text(f"# Meeting {i}\nbody\n", encoding="utf-8")
        _touch_with_mtime(f, now - (5 - i) * 3600)
        files.append(f)

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._list_recent_meetings(limit=3))

    assert result["error"] is None
    payload = result["result"]
    assert payload is not None
    assert payload["count"] == 3
    meetings = payload["meetings"]
    assert len(meetings) == 3
    # Newest-first ordering: 04 > 03 > 02.
    assert meetings[0]["filename"] == "meeting-04.md"
    assert meetings[1]["filename"] == "meeting-03.md"
    assert meetings[2]["filename"] == "meeting-02.md"


def test_list_recent_meetings_default_limit_is_10(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Populate 15 files; calling with no args returns 10.

    Documents the default. The LLM rarely passes a limit, so the default
    has to be sensible -- 10 covers "what meetings did I have this week"
    without overflowing a typical turn's context budget.
    """
    now = time.time()
    for i in range(15):
        f = tmp_path / f"meeting-{i:02d}.md"
        f.write_text(f"# Meeting {i}\n", encoding="utf-8")
        _touch_with_mtime(f, now - (15 - i) * 60)

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._list_recent_meetings(limit=None))

    assert result["error"] is None
    payload = result["result"]
    assert payload is not None
    assert payload["count"] == 10
    assert len(payload["meetings"]) == 10


def test_list_recent_meetings_clamps_huge_limit(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """``limit=99999`` is clamped to 50.

    Stops a runaway LLM from asking for 10k entries and choking the
    WS payload. The clamp is ``max(1, min(50, n))`` so this also
    implicitly covers the lower bound -- ``limit=0`` would become 1.
    """
    now = time.time()
    # Populate more than 50 so the clamp actually has something to cut.
    for i in range(55):
        f = tmp_path / f"meeting-{i:03d}.md"
        f.write_text(f"# Meeting {i}\n", encoding="utf-8")
        _touch_with_mtime(f, now - (55 - i) * 60)

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._list_recent_meetings(limit=99999))

    assert result["error"] is None
    payload = result["result"]
    assert payload is not None
    # Hard ceiling at 50 regardless of how big the LLM asks.
    assert payload["count"] == 50
    assert len(payload["meetings"]) == 50


def test_list_recent_meetings_extracts_h1_as_title(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Files with a ``# H1`` line return that as title; files without
    fall back to the filename stem.

    The title field is the LLM's most useful hook for picking a meeting
    by name ("the deploy review") rather than by opaque
    YYYY-MM-DD-HH-MM-slug.md, so the H1 must win when present.
    """
    with_h1 = tmp_path / "2026-05-27-sync.md"
    with_h1.write_text(
        "# Quarterly Review\n\nlots of content here\n", encoding="utf-8"
    )

    no_h1 = tmp_path / "2026-05-26-empty.md"
    no_h1.write_text("just some text, no heading\n", encoding="utf-8")

    # Stagger mtimes so the newest is the with_h1 file (just for
    # ordering predictability in the assertion below).
    now = time.time()
    _touch_with_mtime(no_h1, now - 3600)
    _touch_with_mtime(with_h1, now)

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._list_recent_meetings(limit=10))

    assert result["error"] is None
    payload = result["result"]
    assert payload is not None
    by_name = {m["filename"]: m for m in payload["meetings"]}

    # H1 wins for the file that has one.
    assert by_name["2026-05-27-sync.md"]["title"] == "Quarterly Review"
    # Fallback to stem for the file with no H1.
    assert by_name["2026-05-26-empty.md"]["title"] == "2026-05-26-empty"


def test_list_recent_meetings_empty_dir(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """An empty notes directory returns ``{count: 0, meetings: []}`` with
    no error.

    Documents the deliberate divergence from ``recall_meeting``: an empty
    list is a valid LIST result (the LLM can phrase "you don't have any
    meeting notes yet"), whereas recall_meeting can't satisfy its
    "return a meeting's contents" contract and so does surface an error.
    """
    # tmp_path exists but is empty.
    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._list_recent_meetings(limit=10))

    # No error -- emptiness is information, not failure.
    assert result["error"] is None
    payload = result["result"]
    assert payload is not None
    assert payload["count"] == 0
    assert payload["meetings"] == []


# ===========================================================================
# recall_meeting (filename) tests
# ===========================================================================
# Tests for the NEW filename argument on the renamed _recall_meeting
# method. The no-arg path is already covered by the original tests at
# the top of this file (those still call _recall_last_meeting() which is
# now a back-compat alias for _recall_meeting(None)).
# ===========================================================================


def test_recall_meeting_by_filename_loads_specific_file(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """When ``filename`` is provided, that exact file is loaded -- NOT
    the newest. Critical for "what did we discuss in the Tuesday sync?"
    style queries where the user names a meeting other than the most
    recent.
    """
    older = tmp_path / "older.md"
    older.write_text(
        "# Older meeting\nDiscussed payroll integration.", encoding="utf-8"
    )
    newer = tmp_path / "newer.md"
    newer.write_text(
        "# Newer meeting\nDiscussed deploy plan.", encoding="utf-8"
    )

    now = time.time()
    os.utime(older, (now - 3600, now - 3600))
    os.utime(newer, (now, now))

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._recall_meeting("older.md"))

    assert result["error"] is None
    payload = result["result"]
    assert payload is not None
    assert payload["filename"] == "older.md"
    # The KEY assertion: we got the OLDER file's body even though
    # newer.md is the most-recently-modified.
    assert "payroll integration" in payload["content"]
    assert "deploy plan" not in payload["content"]


def test_recall_meeting_rejects_path_traversal(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Path-traversal attempts (``..`` / ``/`` / ``\\``) are hard-rejected.

    The LLM has no business resolving anything outside meetingNotesDir.
    We don't try to sanitise -- sanitisation invites bypass attacks.
    We just refuse any filename containing a separator or parent token,
    so e.g. ``../../etc/passwd`` and ``foo/bar.md`` both fail loudly.
    """
    # Plant a sentinel file in the notes dir so we can verify the call
    # didn't accidentally walk to a sibling directory.
    real_file = tmp_path / "real.md"
    real_file.write_text("# Real meeting\n", encoding="utf-8")

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()

    # Each of these MUST be rejected without reading anything.
    for evil in (
        "../../etc/passwd",
        "/etc/passwd",
        "..\\..\\windows\\system32\\config",
        "foo/bar.md",
        "../sibling.md",
    ):
        result = _run(executor._recall_meeting(evil))
        assert result["result"] is None, f"traversal opened a file for {evil!r}"
        assert result["error"] is not None
        assert "invalid filename" in result["error"], (
            f"unexpected error for {evil!r}: {result['error']!r}"
        )


def test_recall_meeting_no_filename_returns_latest(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Back-compat: passing ``None`` returns the most-recent meeting
    (the original ``_recall_last_meeting`` behaviour).

    Belt-and-braces sister to the older tests at the top of this file
    that call ``_recall_last_meeting()``: this one exercises the same
    code path but via the new method signature so a regression that
    only fixed the alias wouldn't pass both.
    """
    older = tmp_path / "older.md"
    older.write_text("# Older\nfoo\n", encoding="utf-8")
    newer = tmp_path / "newer.md"
    newer.write_text("# Newer\nbar\n", encoding="utf-8")

    now = time.time()
    os.utime(older, (now - 3600, now - 3600))
    os.utime(newer, (now, now))

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._recall_meeting(None))

    assert result["error"] is None
    assert result["result"] is not None
    assert result["result"]["filename"] == "newer.md"
    assert "bar" in result["result"]["content"]


def test_recall_meeting_missing_filename_returns_helpful_error(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """A valid-looking filename that doesn't exist returns a friendly
    error pointing the LLM at ``list_recent_meetings`` for discovery.
    """
    # Empty dir; the filename has no separators so traversal check passes,
    # but the file genuinely doesn't exist.
    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor._recall_meeting("missing-meeting.md"))

    assert result["result"] is None
    assert result["error"] is not None
    assert "meeting not found" in result["error"]
    # The error must steer the LLM toward the right next call.
    assert "list_recent_meetings" in result["error"]


def test_recall_meeting_appends_md_extension(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """If the LLM drops the ``.md`` suffix when echoing a filename back,
    the executor adds it. Convenience contract documented inline in
    ``_recall_meeting``.
    """
    note = tmp_path / "deploy-review.md"
    note.write_text("# Deploy Review\nrolled forward\n", encoding="utf-8")

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    # No trailing ``.md``.
    result = _run(executor._recall_meeting("deploy-review"))

    assert result["error"] is None
    assert result["result"] is not None
    assert result["result"]["filename"] == "deploy-review.md"
    assert "rolled forward" in result["result"]["content"]


# ===========================================================================
# Registration + executor dispatch
# ===========================================================================


def test_new_tools_are_registered_in_tool_names() -> None:
    """Both the new tools and the back-compat alias are present in
    ``tools.get_tool_names()``.

    Without this the Ollama / Anthropic tool-schema converters would
    drop them silently.
    """
    names = tools.get_tool_names()
    assert "recall_meeting" in names
    assert "list_recent_meetings" in names
    assert "recall_last_meeting" in names  # Back-compat alias preserved.


def test_new_tools_are_registered_in_pipecat_llm_tools() -> None:
    """Both new tools surface in ``pipecat_llm.TOOLS`` -- the LLM-visible
    list.
    """
    import pipecat_llm

    names = [t["name"] for t in pipecat_llm.TOOLS]
    assert "recall_meeting" in names
    assert "list_recent_meetings" in names
    assert "recall_last_meeting" in names


def test_executor_dispatches_recall_last_meeting_alias(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """End-to-end dispatch: calling ``executor.execute("recall_last_meeting", {})``
    works and returns the latest meeting (verifies the alias branch in
    :meth:`ToolExecutor.execute`).
    """
    note = tmp_path / "only.md"
    note.write_text("# Only meeting\nbody\n", encoding="utf-8")

    monkeypatch.setattr(
        tools,
        "_load_config_safe_local",
        lambda: {"meetingNotesDir": str(tmp_path)},
    )

    executor = _make_executor()
    result = _run(executor.execute("recall_last_meeting", {}))

    # The local-only branch returns the same dict shape as the method
    # (no ``ok`` key) -- the alias is a thin passthrough.
    assert result["error"] is None
    assert result["result"] is not None
    assert result["result"]["filename"] == "only.md"
