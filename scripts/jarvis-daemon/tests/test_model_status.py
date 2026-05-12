"""Tests for ``model_status`` -- the daemon-side model download reporter.

Covers the four contractual surfaces:

1. cache-hit short-circuits the download
2. ProgressTqdm emits a well-shaped ``model_download/progress`` event
3. download failures emit ``model_download/state=error`` and don't raise
4. each event variant matches the schema the HUD wires to
"""

from __future__ import annotations

import asyncio
import io
from typing import Any

import pytest

import model_status


# ---------------------------------------------------------------------------
# Test harness: capture WS events in a list
# ---------------------------------------------------------------------------


class _Recorder:
    """Async event sink that just appends to a list. Replaces the real WS."""

    def __init__(self) -> None:
        self.events: list[dict[str, Any]] = []

    async def __call__(self, payload: dict[str, Any]) -> None:
        self.events.append(payload)


@pytest.fixture
def recorder() -> _Recorder:
    rec = _Recorder()
    # The non-async tests below run via asyncio.run / loop.run_until_complete,
    # so each call sets up its own loop and re-registers the sink. We still
    # register a placeholder here so the cache-hit short-circuit test
    # (which never reaches _emit_sync) has a sink set.
    try:
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        model_status.set_event_sink(rec, loop)
    except RuntimeError:
        pass
    # Reset shared module state so tests are order-independent.
    model_status._inflight.clear()  # type: ignore[attr-defined]
    return rec


# ---------------------------------------------------------------------------
# 1. Cache hit skips the download entirely
# ---------------------------------------------------------------------------


def test_is_cached_returns_true_when_hf_reports_file(monkeypatch: pytest.MonkeyPatch) -> None:
    """try_to_load_from_cache returning a path means the model is on disk."""
    fake_path = "/fake/cache/snapshots/abc/config.json"

    def _fake_loader(**kwargs: Any) -> str:
        return fake_path

    monkeypatch.setattr("huggingface_hub.try_to_load_from_cache", _fake_loader)
    # try_to_load_from_cache only counts as cached if the file actually
    # exists on disk -- patch os.path.isfile to confirm the contract.
    monkeypatch.setattr("os.path.isfile", lambda p: p == fake_path)
    # Ensure the bundled-dir short-circuit doesn't fire.
    monkeypatch.setattr(model_status, "_bundled_dir_has_model", lambda _n: False)

    assert model_status.is_cached("vibevoice") is True


def test_ensure_model_short_circuits_when_cached(recorder: _Recorder,
                                                  monkeypatch: pytest.MonkeyPatch) -> None:
    """ensure_model should not invoke any downloader when the cache is warm."""
    monkeypatch.setattr(model_status, "is_cached", lambda _n: True)

    called = {"snapshot": False, "kokoro": False}

    def _explode_snapshot(_name: str) -> None:
        called["snapshot"] = True
        raise AssertionError("snapshot_download must not run on cache hit")

    def _explode_kokoro() -> None:
        called["kokoro"] = True
        raise AssertionError("kokoro download must not run on cache hit")

    monkeypatch.setattr(model_status, "_download_hf_snapshot", _explode_snapshot)
    monkeypatch.setattr(model_status, "_download_kokoro", _explode_kokoro)

    async def _drive() -> None:
        model_status.set_event_sink(recorder, asyncio.get_running_loop())
        await model_status.ensure_model("vibevoice")

    asyncio.run(_drive())

    assert called == {"snapshot": False, "kokoro": False}
    assert recorder.events == []


# ---------------------------------------------------------------------------
# 2. ProgressTqdm emits a well-shaped progress event
# ---------------------------------------------------------------------------


def test_progress_tqdm_emits_shaped_event(recorder: _Recorder,
                                          monkeypatch: pytest.MonkeyPatch) -> None:
    """Simulating tqdm.update(n) must produce a model_download/progress event.

    The real call site runs inside a thread spawned by snapshot_download,
    so ``_emit_sync`` schedules onto the daemon's event loop via
    ``run_coroutine_threadsafe``. In this test we run the whole flow from
    inside a single asyncio.run() so the scheduled coroutines fire on the
    same loop the recorder lives on.
    """

    async def _run() -> None:
        state = model_status._DownloadState(model="vibevoice", total_bytes=0)
        # Re-register the sink against the currently-running loop so
        # run_coroutine_threadsafe in _emit_sync targets the right loop.
        model_status.set_event_sink(recorder, asyncio.get_running_loop())
        model_status.ProgressTqdm._state = state
        state.last_emit = 0.0

        try:
            # disable=True short-circuits tqdm.update so self.n never moves;
            # route output to a StringIO sink so the bar updates internally
            # without polluting test output.
            bar = model_status.ProgressTqdm(
                total=1_000_000,
                unit="B",
                file=io.StringIO(),
            )
            assert state.total_bytes == 1_000_000
            # Reset the throttle clock between updates so both emits fire
            # in this fast test (the real loop runs at human download speeds).
            bar.update(200_000)
            state.last_emit = 0.0
            bar.update(300_000)
            bar.close()
        finally:
            model_status.ProgressTqdm._state = None

        # Let any scheduled coroutines actually run.
        await asyncio.sleep(0)
        await asyncio.sleep(0)

    asyncio.run(_run())

    assert len(recorder.events) >= 1
    progress_events = [e for e in recorder.events if e.get("state") == "progress"]
    assert progress_events, "expected at least one progress event"
    last = progress_events[-1]
    assert last["type"] == "model_download"
    assert last["model"] == "vibevoice"
    assert last["total_bytes"] == 1_000_000
    assert last["downloaded_bytes"] == 500_000
    assert last["pct"] == 50
    if "speed_bytes_per_sec" in last:
        assert isinstance(last["speed_bytes_per_sec"], int)
    if "eta_seconds" in last:
        assert isinstance(last["eta_seconds"], int)


# ---------------------------------------------------------------------------
# 3. Download failure emits error and does not raise
# ---------------------------------------------------------------------------


def test_download_failure_emits_error_event(recorder: _Recorder,
                                             monkeypatch: pytest.MonkeyPatch) -> None:
    """A raised exception inside the worker becomes a model_download/error event."""
    monkeypatch.setattr(model_status, "is_cached", lambda _n: False)

    def _boom(_name: str) -> None:
        raise RuntimeError("simulated network failure")

    monkeypatch.setattr(model_status, "_download_hf_snapshot", _boom)

    # ensure_model must absorb the exception so the daemon keeps running.
    async def _drive() -> None:
        model_status.set_event_sink(recorder, asyncio.get_running_loop())
        await model_status.ensure_model("vibevoice")

    asyncio.run(_drive())

    states = [e.get("state") for e in recorder.events if e.get("type") == "model_download"]
    assert "started" in states
    assert "error" in states
    error_event = next(e for e in recorder.events if e.get("state") == "error")
    assert error_event["model"] == "vibevoice"
    assert "simulated network failure" in error_event["error"]
    # 'done' must NOT appear when an error fires.
    assert "done" not in states


# ---------------------------------------------------------------------------
# 4. Schema-shape assertions for each event variant
# ---------------------------------------------------------------------------


def test_event_shapes_match_hud_contract() -> None:
    """Build each event variant directly and assert the field set matches."""

    # model_setup state=downloading
    setup_dl = {
        "type": "model_setup",
        "state": "downloading",
        "models_pending": [
            {"name": "vibevoice", "approx_size_bytes": 1_900_000_000},
            {"name": "whisper", "approx_size_bytes": 460_000_000},
        ],
    }
    assert set(setup_dl.keys()) == {"type", "state", "models_pending"}
    assert setup_dl["state"] in ("ready", "downloading")
    for m in setup_dl["models_pending"]:
        assert set(m.keys()) == {"name", "approx_size_bytes"}
        assert m["name"] in {"vibevoice", "whisper", "kokoro"}
        assert isinstance(m["approx_size_bytes"], int)

    # model_setup state=ready
    setup_ok = {
        "type": "model_setup",
        "state": "ready",
        "models_pending": [],
    }
    assert setup_ok["state"] == "ready"
    assert setup_ok["models_pending"] == []

    # model_download/started
    started = {
        "type": "model_download",
        "model": "vibevoice",
        "state": "started",
        "total_bytes": 1_900_000_000,
    }
    assert started["state"] == "started"
    assert isinstance(started["total_bytes"], int)

    # model_download/progress
    state = model_status._DownloadState(
        model="whisper",
        total_bytes=460_000_000,
        downloaded_bytes=230_000_000,
    )
    progress = model_status._build_progress_payload(state)
    assert progress["type"] == "model_download"
    assert progress["state"] == "progress"
    assert progress["model"] == "whisper"
    assert progress["total_bytes"] == 460_000_000
    assert progress["downloaded_bytes"] == 230_000_000
    assert progress["pct"] == 50
    # optional fields if present are correctly typed
    if "speed_bytes_per_sec" in progress:
        assert isinstance(progress["speed_bytes_per_sec"], int)
    if "eta_seconds" in progress:
        assert isinstance(progress["eta_seconds"], int)

    # model_download/done
    done = {
        "type": "model_download",
        "model": "whisper",
        "state": "done",
    }
    assert done["state"] == "done"
    assert set(done.keys()) == {"type", "model", "state"}

    # model_download/error
    error = {
        "type": "model_download",
        "model": "vibevoice",
        "state": "error",
        "error": "RuntimeError: out of disk",
    }
    assert error["state"] == "error"
    assert isinstance(error["error"], str)


# ---------------------------------------------------------------------------
# 5. required_models_for honours config
# ---------------------------------------------------------------------------


def test_required_models_for_default_is_vibevoice_plus_whisper() -> None:
    """Default (empty ttsProvider) should request vibevoice + whisper."""
    assert model_status.required_models_for({}) == ["vibevoice", "whisper"]


def test_required_models_for_kokoro_picks_kokoro() -> None:
    """ttsProvider=kokoro should swap vibevoice out for kokoro."""
    assert model_status.required_models_for({"ttsProvider": "kokoro"}) == [
        "kokoro",
        "whisper",
    ]


def test_required_models_for_cartesia_is_stt_only() -> None:
    """ttsProvider=cartesia is cloud-only, so only whisper needs pre-fetch."""
    assert model_status.required_models_for({"ttsProvider": "cartesia"}) == ["whisper"]


# ---------------------------------------------------------------------------
# 6. prefetch emits the expected setup + per-model events when everything cached
# ---------------------------------------------------------------------------


def test_prefetch_when_all_cached_emits_only_ready(recorder: _Recorder,
                                                    monkeypatch: pytest.MonkeyPatch) -> None:
    """If is_cached returns True for every model, prefetch is a fast no-op."""
    monkeypatch.setattr(model_status, "is_cached", lambda _n: True)

    async def _drive() -> None:
        model_status.set_event_sink(recorder, asyncio.get_running_loop())
        await model_status.prefetch_models({})

    asyncio.run(_drive())

    setup_events = [e for e in recorder.events if e.get("type") == "model_setup"]
    assert len(setup_events) == 1
    assert setup_events[0]["state"] == "ready"
    assert setup_events[0]["models_pending"] == []
    # No download events should fire.
    assert not [e for e in recorder.events if e.get("type") == "model_download"]


# ---------------------------------------------------------------------------
# v0.1.6: HUD `request_model_setup` re-emits the latest cached payload.
# Fixes the race where the daemon emitted model_setup ~1-2s before the
# React HUD connected, so a fresh DMG install missed the overlay entirely.
# ---------------------------------------------------------------------------


def test_request_setup_replays_latest_payload(recorder: _Recorder) -> None:
    """After prefetch emits a `downloading` setup event, `handle_request_setup_message`
    re-emits the cached payload so a late-mounting HUD can pick up the state.
    """
    async def _drive() -> None:
        model_status.set_event_sink(recorder, asyncio.get_running_loop())
        # Seed the cache as if prefetch had emitted a downloading payload.
        await model_status._emit_setup(["vibevoice", "whisper"])
        recorder.events.clear()
        # Late-mounting HUD asks for the latest state.
        await model_status.handle_request_setup_message({})

    asyncio.run(_drive())

    assert len(recorder.events) == 1
    replay = recorder.events[0]
    assert replay["type"] == "model_setup"
    assert replay["state"] == "downloading"
    assert {m["name"] for m in replay["models_pending"]} == {"vibevoice", "whisper"}


def test_request_setup_emits_ready_when_cache_empty(recorder: _Recorder) -> None:
    """When the daemon has just started and prefetch hasn't run yet, the
    handler emits a synthetic `ready` so the HUD doesn't sit with no state.
    """
    # Make sure the module global is clear so we exercise the empty branch.
    model_status._latest_setup_payload = None  # type: ignore[attr-defined]

    async def _drive() -> None:
        model_status.set_event_sink(recorder, asyncio.get_running_loop())
        await model_status.handle_request_setup_message({})

    asyncio.run(_drive())

    assert len(recorder.events) == 1
    assert recorder.events[0]["type"] == "model_setup"
    assert recorder.events[0]["state"] == "ready"
    assert recorder.events[0]["models_pending"] == []
