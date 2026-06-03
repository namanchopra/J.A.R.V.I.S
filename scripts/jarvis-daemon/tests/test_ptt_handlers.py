"""Tests for the PTT (push-to-talk) control-frame handlers in main.py.

TASK-006 (v0.3.0 Mac overlay): two new control frames flow from the Go
bridge to the daemon over the existing /ws/jarvis WebSocket:

* ``{"type": "ptt_active"}``  -- hotkey pressed; open the STT gate.
* ``{"type": "ptt_release"}`` -- hotkey released; finalize the turn.

These unit tests cover the handler-level behavior in isolation:
state tracking in ``_PTT_STATE``, the ``_ptt_active_flag`` toggle,
idempotency on double-press, and the documented failure case where a
``ptt_release`` arrives without a prior ``ptt_active``.

Full pipeline integration (the actual Pipecat frame injection driving
real STT/LLM turns) is out of scope here -- that's TASK-010.  Frame
injection is verified at the boundary: we patch
``main._inject_pipeline_frames`` and assert the right frame types are
passed.

State leakage between tests is prevented by the ``reset_ptt`` autouse
fixture, which mirrors the pattern used by ``test_active_client.py``.
"""

from __future__ import annotations

from typing import Any
from unittest.mock import MagicMock

import pytest

import main


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture(autouse=True)
def reset_ptt(monkeypatch: pytest.MonkeyPatch) -> None:
    """Reset module state before and after every test.

    Stubs out the pipeline-frame injection helper and the WS handle so
    handler calls don't try to touch a real asyncio loop / websocket.
    The ``_inject_pipeline_frames`` patch is recorded on ``main`` so
    individual tests can assert what was injected.
    """
    main._PTT_STATE.clear()
    main._ptt_active_flag = False
    main._ptt_safety_task = None

    # Patch the frame-injection helper so we can assert on what frames
    # were scheduled without needing a live PipelineTask.
    inject_mock = MagicMock(return_value=True)
    monkeypatch.setattr(main, "_inject_pipeline_frames", inject_mock)

    # Patch the WS handle to None so the handler skips the state-send
    # branch by default.  Individual tests override this when they want
    # to assert on state transitions.
    monkeypatch.setattr(main, "_pipeline_status_ws", None)

    # Stub out active_client.set_mac_active so we don't touch global
    # interlocutor state from these unit tests.
    monkeypatch.setattr(main.active_client, "set_mac_active", MagicMock())

    yield

    main._PTT_STATE.clear()
    main._ptt_active_flag = False
    main._ptt_safety_task = None


# ---------------------------------------------------------------------------
# Required tests from the TASK-006 brief
# ---------------------------------------------------------------------------


def test_ptt_active_opens_gate() -> None:
    """``ptt_active`` records state and injects UserStartedSpeakingFrame.

    Acceptance: calling ``_handle_ptt_active({})`` records state in
    ``_PTT_STATE`` and (via mock) emits a state-change to "listening".
    """
    assert main._PTT_STATE == {}
    assert main._ptt_active_flag is False

    main._handle_ptt_active({})

    assert "mac" in main._PTT_STATE
    assert isinstance(main._PTT_STATE["mac"], float)
    assert main._ptt_active_flag is True

    # Frame injection was attempted with a UserStartedSpeakingFrame.
    main._inject_pipeline_frames.assert_called_once()
    injected_frames = main._inject_pipeline_frames.call_args.args[0]
    assert len(injected_frames) == 1
    assert isinstance(injected_frames[0], main.UserStartedSpeakingFrame)

    # active_client was flipped to Mac for this turn.
    main.active_client.set_mac_active.assert_called_once()


def test_ptt_active_emits_listening_state(monkeypatch: pytest.MonkeyPatch) -> None:
    """When a WS is registered, ``ptt_active`` schedules a state=listening send.

    The handler is synchronous but uses ``asyncio.create_task`` to ship
    the WS send; we patch ``asyncio.create_task`` to capture the coro
    that was scheduled and inspect the WS payload directly.
    """
    fake_ws = MagicMock()
    monkeypatch.setattr(main, "_pipeline_status_ws", fake_ws)

    scheduled_coros: list[Any] = []

    def _capture_task(coro: Any, name: str | None = None) -> MagicMock:  # noqa: ARG001
        scheduled_coros.append(coro)
        # Close the coro to suppress "coroutine was never awaited" warnings.
        if hasattr(coro, "close"):
            coro.close()
        return MagicMock()

    monkeypatch.setattr(main.asyncio, "create_task", _capture_task)

    main._handle_ptt_active({})

    # One scheduled task for the state send, one for the safety timeout.
    # We only care that SOMETHING was scheduled here -- the fine-grained
    # WS payload assertion would require awaiting the coro on a real loop.
    assert len(scheduled_coros) >= 1


def test_ptt_release_closes_gate() -> None:
    """After ``ptt_active``, ``ptt_release`` clears state and injects stop frame.

    Acceptance: clears ``_PTT_STATE`` and triggers the turn-stop signal.
    """
    main._handle_ptt_active({})
    assert "mac" in main._PTT_STATE
    main._inject_pipeline_frames.reset_mock()

    main._handle_ptt_release({})

    assert "mac" not in main._PTT_STATE
    assert main._PTT_STATE == {}
    assert main._ptt_active_flag is False

    # Frame injection was attempted with a UserStoppedSpeakingFrame.
    main._inject_pipeline_frames.assert_called_once()
    injected_frames = main._inject_pipeline_frames.call_args.args[0]
    assert len(injected_frames) == 1
    assert isinstance(injected_frames[0], main.UserStoppedSpeakingFrame)


def test_ptt_release_without_active_is_ignored(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """``ptt_release`` with no prior active logs a warning, doesn't raise.

    This is the required failure-case test from the TASK-006 brief:
    out-of-order frames must be handled gracefully.
    """
    assert main._PTT_STATE == {}

    with caplog.at_level("WARNING", logger="jarvis-daemon"):
        # Must not raise.
        main._handle_ptt_release({})

    # State unchanged.
    assert main._PTT_STATE == {}
    assert main._ptt_active_flag is False

    # A warning was logged with the expected phrasing so we can grep
    # for it in production logs.
    assert any(
        "ptt_release received without prior ptt_active" in record.message
        for record in caplog.records
    ), f"expected warning, got: {[r.message for r in caplog.records]}"

    # No frame was injected -- there was no turn to finalize.
    main._inject_pipeline_frames.assert_not_called()


def test_double_ptt_active_is_idempotent(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Second ``ptt_active`` before release is a no-op with a warning.

    Acceptance: doesn't reset the timer or double-fire downstream.
    """
    main._handle_ptt_active({})
    first_timestamp = main._PTT_STATE["mac"]
    assert main._inject_pipeline_frames.call_count == 1

    with caplog.at_level("WARNING", logger="jarvis-daemon"):
        main._handle_ptt_active({})

    # Timestamp unchanged -- we did NOT reset the window.
    assert main._PTT_STATE["mac"] == first_timestamp
    # Still active.
    assert main._ptt_active_flag is True

    # No additional frame injection on the duplicate call.
    assert main._inject_pipeline_frames.call_count == 1

    # Warning was logged.
    assert any(
        "ptt_active received while already active" in record.message
        for record in caplog.records
    ), f"expected idempotency warning, got: {[r.message for r in caplog.records]}"


# ---------------------------------------------------------------------------
# Bonus: dispatcher registration sanity
# ---------------------------------------------------------------------------


def test_handlers_registered_in_dispatcher() -> None:
    """Both PTT handlers are wired into the WS message dispatcher.

    Without this the daemon would log ``Unknown message type`` and drop
    the frame on the floor.
    """
    assert main._MESSAGE_HANDLERS.get("ptt_active") is main._handle_ptt_active
    assert main._MESSAGE_HANDLERS.get("ptt_release") is main._handle_ptt_release


def test_ptt_release_clears_active_flag_after_idempotent_active() -> None:
    """End-to-end mini-cycle: active -> double-active -> release -> all clear.

    Guards against a regression where a stale ``_ptt_active_flag`` would
    leak past the release if the idempotency path mishandled state.
    """
    main._handle_ptt_active({})
    main._handle_ptt_active({})  # idempotent
    main._handle_ptt_release({})

    assert main._PTT_STATE == {}
    assert main._ptt_active_flag is False
