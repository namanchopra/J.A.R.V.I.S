"""Always-on microphone stream using PyAudio.

Provides an async generator of float32 numpy audio chunks at 16 kHz mono.
Supports muting for echo suppression during TTS playback and exposes a
real-time RMS audio level for orb reactivity.

Requires PortAudio:  brew install portaudio
Requires PyAudio:    pip install pyaudio
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import AsyncIterator
from typing import Final

import numpy as np

logger: Final = logging.getLogger("jarvis-daemon.mic")

# ---------------------------------------------------------------------------
# Audio constants
# ---------------------------------------------------------------------------
SAMPLE_RATE: Final[int] = 16_000
CHUNK_SIZE: Final[int] = 1_024  # ~64 ms at 16 kHz
CHANNELS: Final[int] = 1
SAMPLE_FORMAT_NUMPY_DTYPE: Final = np.int16
RMS_SCALE_FACTOR: Final[float] = 5.0


# ---------------------------------------------------------------------------
# Exceptions
# ---------------------------------------------------------------------------
class MicUnavailableError(Exception):
    """Raised when the microphone cannot be accessed.

    Common causes: no input device connected, macOS microphone permission
    denied, or PortAudio not installed (``brew install portaudio``).
    """


# ---------------------------------------------------------------------------
# MicStream
# ---------------------------------------------------------------------------
class MicStream:
    """Always-on microphone stream backed by PyAudio.

    Usage::

        mic = MicStream()
        await mic.start()
        async for chunk in mic:
            # chunk is a float32 numpy array of shape (CHUNK_SIZE,)
            process(chunk)
        await mic.stop()

    The stream keeps running even when muted -- only delivery to consumers
    is suppressed so the underlying PortAudio device stays open (avoids
    costly re-initialisation).
    """

    def __init__(self) -> None:
        self._muted: bool = False
        self._running: bool = False
        self._queue: asyncio.Queue[np.ndarray] = asyncio.Queue(maxsize=50)
        self._stream: object | None = None  # pyaudio.Stream (typed as object to defer import)
        self._pa: object | None = None  # pyaudio.PyAudio
        self._audio_level: float = 0.0
        self._loop: asyncio.AbstractEventLoop | None = None

    # -- public properties ---------------------------------------------------

    @property
    def audio_level(self) -> float:
        """Current RMS audio level normalised to 0.0 -- 1.0."""
        return self._audio_level

    @property
    def muted(self) -> bool:
        """Whether the mic stream is muted (echo suppression)."""
        return self._muted

    @muted.setter
    def muted(self, value: bool) -> None:
        self._muted = value
        if value:
            logger.debug("mic muted -- draining queue (%d frames)", self._queue.qsize())
            self._drain_queue()

    @property
    def running(self) -> bool:
        """Whether the mic stream is currently active."""
        return self._running

    # -- lifecycle -----------------------------------------------------------

    async def start(self) -> None:
        """Open the microphone and begin pushing audio chunks.

        Raises:
            MicUnavailableError: If PyAudio cannot be imported or the default
                input device cannot be opened.
            RuntimeError: If the stream is already running.
        """
        if self._running:
            raise RuntimeError("MicStream is already running")

        try:
            import pyaudio  # noqa: PLC0415 -- deferred so import error is catchable
        except ImportError as exc:
            raise MicUnavailableError(
                "PyAudio is not installed. Install it with: "
                "pip install pyaudio  (requires PortAudio: brew install portaudio)"
            ) from exc

        self._loop = asyncio.get_running_loop()

        pa = pyaudio.PyAudio()
        self._pa = pa

        try:
            stream = pa.open(
                format=pyaudio.paInt16,
                channels=CHANNELS,
                rate=SAMPLE_RATE,
                input=True,
                frames_per_buffer=CHUNK_SIZE,
                stream_callback=self._audio_callback,
            )
        except (OSError, IOError) as exc:
            pa.terminate()
            self._pa = None
            raise MicUnavailableError(
                f"Failed to open microphone: {exc}. "
                "Check that a mic is connected and macOS microphone permission is granted."
            ) from exc

        self._stream = stream
        self._running = True
        stream.start_stream()
        logger.info(
            "mic started (rate=%d, chunk=%d, channels=%d)",
            SAMPLE_RATE,
            CHUNK_SIZE,
            CHANNELS,
        )

    async def stop(self) -> None:
        """Stop the mic stream and release all PyAudio resources."""
        if not self._running:
            return

        self._running = False

        import pyaudio as _pa_module  # noqa: PLC0415

        stream = self._stream
        if stream is not None and isinstance(stream, _pa_module.Stream):
            try:
                stream.stop_stream()
                stream.close()
            except Exception:
                logger.exception("error closing PyAudio stream")
            self._stream = None

        pa = self._pa
        if pa is not None and isinstance(pa, _pa_module.PyAudio):
            pa.terminate()
            self._pa = None

        self._drain_queue()
        self._audio_level = 0.0
        logger.info("mic stopped")

    # -- async iteration -----------------------------------------------------

    def __aiter__(self) -> AsyncIterator[np.ndarray]:
        return self._iterate()

    async def _iterate(self) -> AsyncIterator[np.ndarray]:
        """Yield audio chunks from the internal queue while running."""
        while self._running:
            try:
                chunk = await asyncio.wait_for(self._queue.get(), timeout=0.1)
                yield chunk
            except asyncio.TimeoutError:
                continue

    # -- internal ------------------------------------------------------------

    def _audio_callback(
        self,
        in_data: bytes | None,
        frame_count: int,  # noqa: ARG002
        time_info: dict,  # noqa: ARG002
        status: int,  # noqa: ARG002
    ) -> tuple[None, int]:
        """PyAudio callback -- runs on a PortAudio C thread.

        Converts raw int16 PCM to float32 [-1.0, 1.0], calculates RMS, and
        pushes the chunk to the async queue via ``call_soon_threadsafe``.
        """
        import pyaudio  # noqa: PLC0415

        if in_data is None:
            return (None, pyaudio.paContinue)

        audio = np.frombuffer(in_data, dtype=np.int16).astype(np.float32) / 32768.0

        # RMS audio level scaled to 0.0 -- 1.0
        rms = float(np.sqrt(np.mean(audio**2)))
        self._audio_level = min(rms * RMS_SCALE_FACTOR, 1.0)

        if not self._muted and self._loop is not None:
            def _safe_put(chunk: np.ndarray) -> None:
                try:
                    self._queue.put_nowait(chunk)
                except asyncio.QueueFull:
                    pass  # Drop frame — consumer is slow
            try:
                self._loop.call_soon_threadsafe(_safe_put, audio)
            except RuntimeError:
                pass  # Loop closed

        return (None, pyaudio.paContinue)

    def _drain_queue(self) -> None:
        """Discard all queued audio chunks."""
        while not self._queue.empty():
            try:
                self._queue.get_nowait()
            except asyncio.QueueEmpty:
                break
