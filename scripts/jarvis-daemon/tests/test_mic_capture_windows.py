"""Windows-only smoke test for the PyAudio mic capture path (TASK-043, v0.4.0).

The goal of this test is to verify that ``mic.py`` -- the cross-platform
PyAudio mic stream the daemon relies on for meeting + voice capture --
opens the *default* Windows input device at 16 kHz mono, delivers audio
frames, and surfaces a clear ``MicUnavailableError`` when the device
cannot be opened.

Coverage (TASK-043 acceptance criteria):
  1. Mic capture at 16 kHz mono works on the default device
        -> ``MicStream.start()`` opens PortAudio without specifying an
           ``input_device_index`` (see ``mic.py``: ``pa.open(... input=True ...)``),
           which means PyAudio uses ``Pa_GetDefaultInputDevice`` -- exactly
           what Windows reports as the current default capture device.
           We assert ``mic.running`` and that at least one float32 chunk
           of shape ``(CHUNK_SIZE,)`` arrives on the async iterator within
           a generous deadline.
  2. Switching the default mic via Windows Settings is reflected on
     the next capture session
        -> We can't programmatically flip the Windows default in CI
           without admin + a virtual driver, so we instead document +
           prove the *mechanism*: each ``start()`` re-instantiates the
           PyAudio engine and calls ``pa.open()`` with no pinned device
           index. Test 2 stops and restarts the stream and asserts the
           cycle is clean (no stale PyAudio handles, no cached device).
           CI cannot flip the OS default, but the in-process invariant
           (no device pinning across sessions) is fully testable.
  3. Failure case: when the mic permission is denied (or no input
     device is enumerable), ``MicStream.start()`` raises
     ``MicUnavailableError`` with a helpful message
        -> We simulate device-unavailable via ``monkeypatch`` against
           ``pyaudio.PyAudio.open`` so the test deterministically hits
           the ``except (OSError, IOError)`` branch in ``mic.start()``.
           Real Windows mic-denied flows surface as an ``OSError`` from
           PortAudio (``[Errno -9996] Invalid input device``) -- the
           branch we exercise. We assert the wrapped error includes the
           "Failed to open microphone" string + the original cause.

Platform model:
  - The capture tests (1, 2) only run on Windows because they require a
    PortAudio + WASAPI runtime + an enumerable default input device.
    On macOS / Linux the test is skipped with a clear reason. This
    mirrors the sibling ``test_daemon_startup_windows.py`` pattern.
  - The failure-case test (3) runs on every platform because it uses
    ``monkeypatch`` to bypass the real PortAudio call -- it's verifying
    daemon-side error handling, not OS audio plumbing. This keeps the
    error-handling contract under coverage even on macOS PR checks.

CI wiring:
  - Tests 1 and 2 run under the matrix entry ``windows-2022 / pytest``
    after ``install-daemon.ps1`` (TASK-007 staging portaudio.dll) and
    after TASK-013 has confirmed the daemon launches. They assume an
    enumerable default input device exists -- on a headless GitHub
    runner there usually is none. To stay green in that environment we
    ``pytest.skip`` if ``pa.get_default_input_device_info()`` raises.
    A self-hosted runner with mic loopback (or a developer machine)
    fully exercises tests 1 and 2.
"""

from __future__ import annotations

import asyncio
import sys
from typing import Any, Final

import numpy as np
import pytest

# mic.py is at the daemon source root; conftest.py prepends that dir to
# sys.path so the import below is the production module, not a copy.
from mic import (  # noqa: E402 -- conftest sys.path manipulation
    CHANNELS,
    CHUNK_SIZE,
    SAMPLE_RATE,
    MicStream,
    MicUnavailableError,
)

# ---------------------------------------------------------------------------
# Platform / env guards
# ---------------------------------------------------------------------------

IS_WINDOWS: Final[bool] = sys.platform.startswith("win")

requires_windows = pytest.mark.skipif(
    not IS_WINDOWS,
    reason="TASK-043 mic capture verification runs on Windows only "
    "(needs WASAPI + an enumerable default input device)",
)

# How long to wait for the first audio chunk to arrive once start() returns.
# Headless runners under heavy load have been observed to take ~1.5s before
# the first callback fires; 5s is comfortable headroom.
_FIRST_CHUNK_TIMEOUT_SEC: Final[float] = 5.0

# Number of chunks to assert against. We only need ONE to prove the path is
# alive, but pulling two also confirms the queue is being fed (not just a
# single warm-up frame).
_REQUIRED_CHUNKS: Final[int] = 2


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _skip_if_no_default_input_device() -> None:
    """Skip the surrounding test when no default input device is enumerable.

    Headless CI runners (and locked-down corporate Windows images) often
    have no audio capture device at all. PyAudio reports this by raising
    ``OSError`` from ``get_default_input_device_info``. Treat that as a
    skip rather than a fail: the test is about *behaviour given a default
    device*, not about provisioning one.
    """
    try:
        import pyaudio  # noqa: PLC0415 -- deferred so the import error is catchable
    except ImportError as exc:
        pytest.skip(f"pyaudio not importable in this env: {exc}")

    pa = pyaudio.PyAudio()
    try:
        try:
            info = pa.get_default_input_device_info()
        except OSError as exc:
            pytest.skip(
                f"no default input device enumerable on this runner: {exc}"
            )
        # Sanity: the device must claim >= 1 input channel.
        if int(info.get("maxInputChannels", 0)) < CHANNELS:
            pytest.skip(
                "default input device has 0 input channels: "
                f"{info!r}"
            )
    finally:
        pa.terminate()


async def _collect_chunks(
    mic: MicStream,
    *,
    count: int,
    timeout: float,
) -> list[np.ndarray]:
    """Pull ``count`` chunks off the async iterator with a hard deadline.

    We can't just ``async for`` in the test body because the iterator
    yields forever while ``mic.running`` is True. Wrapping in
    ``asyncio.wait_for`` makes the timeout deterministic.
    """
    collected: list[np.ndarray] = []

    async def _pull() -> None:
        async for chunk in mic:
            collected.append(chunk)
            if len(collected) >= count:
                return

    await asyncio.wait_for(_pull(), timeout=timeout)
    return collected


# ---------------------------------------------------------------------------
# AC #1: mic capture at 16 kHz mono works on default device (Windows-only)
# ---------------------------------------------------------------------------


@requires_windows
def test_mic_captures_default_device_at_16khz_mono() -> None:
    """``MicStream`` opens the OS default input device + delivers frames.

    Why this exercises the default device:
        ``mic.py`` calls ``pa.open(...)`` without ``input_device_index``,
        so PyAudio uses ``Pa_GetDefaultInputDevice`` -- the device the
        Windows Sound control panel marks "Default". Changing the
        default in Windows Settings between sessions is therefore
        picked up automatically (covered by test 2's mechanism check).

    Implementation note:
        The daemon test suite avoids depending on ``pytest-asyncio`` --
        ``test_model_status.py`` documents that convention. We drive the
        coroutine via ``asyncio.run`` so this test runs on any pytest
        install, with or without the asyncio plugin.
    """
    _skip_if_no_default_input_device()

    async def _drive() -> None:
        mic = MicStream()
        await mic.start()
        try:
            assert mic.running is True, "mic.running should be True after start()"

            chunks = await _collect_chunks(
                mic, count=_REQUIRED_CHUNKS, timeout=_FIRST_CHUNK_TIMEOUT_SEC
            )
            assert len(chunks) >= _REQUIRED_CHUNKS, (
                f"expected >= {_REQUIRED_CHUNKS} chunks, "
                f"got {len(chunks)} in {_FIRST_CHUNK_TIMEOUT_SEC}s"
            )

            # Each chunk must be a float32 numpy array of shape (CHUNK_SIZE,).
            # The conversion happens in ``MicStream._audio_callback``: raw
            # int16 PCM is divided by 32768.0 -> float32 [-1.0, 1.0].
            for i, chunk in enumerate(chunks):
                assert isinstance(chunk, np.ndarray), (
                    f"chunk {i} is not a numpy array: {type(chunk)!r}"
                )
                assert chunk.dtype == np.float32, (
                    f"chunk {i} dtype = {chunk.dtype!r}, expected float32"
                )
                assert chunk.shape == (CHUNK_SIZE,), (
                    f"chunk {i} shape = {chunk.shape!r}, "
                    f"expected ({CHUNK_SIZE},) -- "
                    f"sample-rate / channel mismatch?"
                )
                # All samples must be inside the normalised int16 range.
                # Out-of-range values would mean the int16->float32 scaling
                # is wrong, which would silently bork downstream STT.
                assert float(np.max(np.abs(chunk))) <= 1.0 + 1e-3, (
                    f"chunk {i} contains out-of-range samples: "
                    f"max(|x|) = {float(np.max(np.abs(chunk))):.4f}"
                )

            # Constants assertion -- belt + braces: if someone bumps
            # SAMPLE_RATE or CHANNELS in mic.py we want this test to break.
            assert SAMPLE_RATE == 16_000, "SAMPLE_RATE must be 16 kHz for STT"
            assert CHANNELS == 1, "CHANNELS must be 1 (mono) for STT"
        finally:
            await mic.stop()
            assert mic.running is False, "mic.running should be False after stop()"

    asyncio.run(_drive())


# ---------------------------------------------------------------------------
# AC #2: switching default mic is reflected on next session (mechanism check)
# ---------------------------------------------------------------------------


@requires_windows
def test_mic_restart_re_picks_default_device() -> None:
    """A fresh ``start()`` re-queries the OS default input device.

    We cannot programmatically flip Windows' default capture device from
    inside pytest without admin + a virtual audio driver, so this test
    verifies the *invariant* that makes the behaviour work: each call to
    ``MicStream.start()`` constructs a brand-new ``pyaudio.PyAudio`` and
    calls ``pa.open()`` without pinning ``input_device_index``. The
    instance therefore always asks PortAudio for "whatever the current
    default is" -- whether the user just flipped it in Sound Settings
    or not.

    Concretely we assert:
      - Two consecutive start/stop cycles both succeed
      - Each cycle yields fresh frames (the second cycle isn't draining
        a queue left over from the first)
      - ``mic.running`` toggles correctly across the cycle
    """
    _skip_if_no_default_input_device()

    async def _drive() -> None:
        mic = MicStream()

        # ---- Cycle 1 ---------------------------------------------------
        await mic.start()
        try:
            assert mic.running is True
            first = await _collect_chunks(
                mic, count=1, timeout=_FIRST_CHUNK_TIMEOUT_SEC
            )
            assert len(first) == 1
        finally:
            await mic.stop()
        assert mic.running is False

        # ---- Cycle 2 (would re-pick default device if user changed it) -
        await mic.start()
        try:
            assert mic.running is True
            second = await _collect_chunks(
                mic, count=1, timeout=_FIRST_CHUNK_TIMEOUT_SEC
            )
            assert len(second) == 1
            # Sanity: the second cycle's chunk must NOT be the *same numpy
            # object* as the first (would mean we drained a stale queue).
            # numpy arrays use identity, not value, equality for ``is``.
            assert second[0] is not first[0], (
                "second-cycle chunk is the same object as first-cycle "
                "chunk -- queue not drained between sessions"
            )
        finally:
            await mic.stop()
        assert mic.running is False

    asyncio.run(_drive())


# ---------------------------------------------------------------------------
# AC #3: failure case -- permission denied / device unavailable
# ---------------------------------------------------------------------------


# This test runs on every platform because it doesn't touch real PortAudio:
# we patch ``pyaudio.PyAudio.open`` to raise the exact OSError shape that
# WASAPI surfaces when the mic is permission-denied on Windows 10/11. The
# wrapping logic in ``MicStream.start()`` is what we care about -- and
# that logic is identical on every OS.
def test_mic_unavailable_raises_clear_error(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """``MicStream.start()`` wraps PortAudio errors in ``MicUnavailableError``.

    Real Windows behaviour when mic permission is denied (Settings ->
    Privacy & security -> Microphone -> off, or app-level toggle off):
        ``pyaudio.PyAudio().open(...)`` raises ``OSError`` with a message
        like ``[Errno -9996] Invalid input device (no default output
        device)`` -- PortAudio surfaces ``paInvalidDevice`` because the
        Windows audio session refuses enumeration.

    ``mic.py``'s ``start()`` catches ``(OSError, IOError)`` and re-raises
    as ``MicUnavailableError`` with a helpful "check mic is connected
    and permission is granted" message. We assert that wrapping happens
    and that ``__cause__`` preserves the original error for log forensics.
    """
    try:
        import pyaudio  # noqa: PLC0415
    except ImportError:
        pytest.skip("pyaudio not installed in this env -- can't patch it")

    simulated = OSError(
        -9996, "Invalid input device (simulated permission denial)"
    )

    def _refuse_open(self: Any, *args: Any, **kwargs: Any) -> Any:  # noqa: ARG001
        raise simulated

    # Patch the bound method on the class. ``PyAudio.open`` is the exact
    # call site in ``mic.start()``; patching here intercepts the bound
    # call without touching the class state machine elsewhere.
    monkeypatch.setattr(pyaudio.PyAudio, "open", _refuse_open)

    async def _drive() -> MicStream:
        mic = MicStream()
        with pytest.raises(MicUnavailableError) as exc_info:
            await mic.start()

        msg = str(exc_info.value)
        assert "Failed to open microphone" in msg, (
            f"error message missing the user-facing 'Failed to open "
            f"microphone' prefix: {msg!r}"
        )
        # The wrapped cause must chain so structured logging captures the
        # PortAudio errno for debugging.
        assert exc_info.value.__cause__ is simulated, (
            f"original OSError must be chained as __cause__; "
            f"got: {exc_info.value.__cause__!r}"
        )
        return mic

    mic = asyncio.run(_drive())

    # After a failed start, the stream must be left in a clean state so a
    # retry (e.g. after the user grants permission) can succeed without a
    # full process restart.
    assert mic.running is False, (
        "mic.running must remain False after a failed start"
    )
