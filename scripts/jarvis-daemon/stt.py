"""Streaming speech-to-text using mlx-whisper with a rolling-window approach.

mlx-whisper is a batch library -- it does NOT support native streaming
transcription.  We simulate streaming by accumulating audio in a buffer and
transcribing the full buffer every ~500 ms.  The new transcription is compared
against the previous one to detect new or changed words.  Partial transcript
events are emitted as text updates; a final transcript is emitted when
silence is detected (RMS below threshold for 700 ms).

Falls back to faster-whisper on non-Apple-Silicon machines.

Requires one of:
  - mlx-whisper:    pip install mlx-whisper   (Apple Silicon)
  - faster-whisper: pip install faster-whisper (any platform)
"""

from __future__ import annotations

import asyncio
import logging
import time
from dataclasses import dataclass, field
from typing import Final, Literal

import numpy as np

logger: Final = logging.getLogger("jarvis-daemon.stt")

# ---------------------------------------------------------------------------
# Audio constants (must match mic.py)
# ---------------------------------------------------------------------------
SAMPLE_RATE: Final[int] = 16_000

# ---------------------------------------------------------------------------
# Tuning constants
# ---------------------------------------------------------------------------
BASE_INTERVAL_S: Final[float] = 0.5
"""Minimum interval between transcription passes (seconds)."""

MAX_INTERVAL_S: Final[float] = 2.0
"""Upper bound for the adaptive interval (seconds)."""

INTERVAL_HEADROOM: Final[float] = 0.1
"""Extra headroom added to the last transcription duration to derive the
adaptive interval.  Ensures we always have a little breathing room."""

SILENCE_RMS_THRESHOLD: Final[float] = 0.01
"""RMS below this value is considered silence."""

SILENCE_DURATION_S: Final[float] = 1.2
"""Consecutive silence (in seconds) required to finalize a transcript.
1.2s lets people pause between sentences without being cut off."""

MAX_BUFFER_S: Final[float] = 30.0
"""Maximum audio buffered before a forced transcription + reset."""

CHUNK_DURATION_S: Final[float] = 1024 / SAMPLE_RATE
"""Duration of a single mic chunk (~64 ms at 16 kHz / 1024 samples)."""

SILENCE_CHUNKS: Final[int] = int(SILENCE_DURATION_S / CHUNK_DURATION_S)
"""Number of consecutive silent chunks that trigger end-of-speech."""


# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------
@dataclass(frozen=True, slots=True)
class TranscriptEvent:
    """A partial or final transcript emitted by ``StreamingSTT``."""

    text: str
    """The transcribed text."""

    partial: bool
    """``True`` while the user is still speaking (rolling window updating).
    ``False`` when silence has been detected and this is the final text."""

    confidence: float = 1.0
    """Confidence score in the range 0.0 -- 1.0.  For mlx-whisper, this is
    derived from the average segment log-probability."""


# ---------------------------------------------------------------------------
# Backend abstraction
# ---------------------------------------------------------------------------
@dataclass(slots=True)
class _TranscriptionBackend:
    """Thin wrapper around either mlx_whisper or faster_whisper."""

    kind: Literal["mlx", "faster-whisper"]
    model_name: str
    _fw_model: object | None = field(default=None, repr=False)
    """Only set when ``kind == 'faster-whisper'``."""

    # -- loading -------------------------------------------------------------

    def load(self) -> None:
        """Load / warm up the model.  Called once during ``start()``."""
        if self.kind == "mlx":
            import mlx_whisper  # noqa: PLC0415 -- deferred

            repo = f"mlx-community/whisper-{self.model_name}-mlx"
            logger.info("warming up mlx-whisper model %s", repo)
            # Transcribe a tiny silent buffer to trigger model download + JIT.
            silence = np.zeros(SAMPLE_RATE, dtype=np.float32)
            mlx_whisper.transcribe(
                silence, path_or_hf_repo=repo, language="en",
            )
            logger.info("mlx-whisper model ready")
        else:
            from faster_whisper import WhisperModel  # noqa: PLC0415

            logger.info("loading faster-whisper model %s", self.model_name)
            self._fw_model = WhisperModel(
                self.model_name, device="auto", compute_type="int8",
            )
            logger.info("faster-whisper model ready")

    # -- transcription -------------------------------------------------------

    def transcribe(self, audio: np.ndarray) -> tuple[str, float]:
        """Run batch transcription on *audio* (float32, 16 kHz mono).

        Returns ``(text, confidence)`` where confidence is 0.0 -- 1.0.
        """
        if self.kind == "mlx":
            return self._transcribe_mlx(audio)
        return self._transcribe_fw(audio)

    def _transcribe_mlx(self, audio: np.ndarray) -> tuple[str, float]:
        import mlx_whisper  # noqa: PLC0415

        repo = f"mlx-community/whisper-{self.model_name}-mlx"
        result = mlx_whisper.transcribe(
            audio, path_or_hf_repo=repo, language="en",
        )
        text: str = result.get("text", "").strip()

        # Derive confidence from average segment log-probabilities.
        segments = result.get("segments", [])
        if segments:
            avg_logprob = sum(
                s.get("avg_logprob", -1.0) for s in segments
            ) / len(segments)
            # avg_logprob is typically in [-1.0, 0.0].  Map to [0, 1].
            confidence = max(0.0, min(1.0, 1.0 + avg_logprob))
        else:
            confidence = 0.0

        return text, confidence

    def _transcribe_fw(self, audio: np.ndarray) -> tuple[str, float]:
        from faster_whisper import WhisperModel  # noqa: PLC0415

        model: WhisperModel = self._fw_model  # type: ignore[assignment]
        segments_gen, info = model.transcribe(  # type: ignore[union-attr]
            audio, language="en", beam_size=1,
        )
        segments = list(segments_gen)
        text = " ".join(s.text.strip() for s in segments).strip()

        if segments:
            avg_logprob = sum(s.avg_log_prob for s in segments) / len(segments)
            confidence = max(0.0, min(1.0, 1.0 + avg_logprob))
        else:
            confidence = 0.0

        return text, confidence


# ---------------------------------------------------------------------------
# StreamingSTT
# ---------------------------------------------------------------------------
class StreamingSTT:
    """Near-real-time speech-to-text using a rolling-window approach.

    Accumulates audio in a buffer.  Every ~500 ms, transcribes the full
    buffer.  Compares against the previous transcription to find new or
    changed words.  Emits partial transcript events as text updates and a
    final transcript when silence is detected.

    Falls back to ``faster-whisper`` if ``mlx-whisper`` is not importable
    (non-Apple-Silicon machines).

    Usage::

        stt = StreamingSTT()
        await stt.start()          # loads the model (once)

        async for chunk in mic:
            event = await stt.process_chunk(chunk)
            if event is not None:
                print(event)
    """

    def __init__(self, model_name: str = "small.en") -> None:
        self._model_name = model_name
        self._backend: _TranscriptionBackend | None = None

        # Audio buffer -- list of float32 arrays concatenated on transcription.
        self._buffer: list[np.ndarray] = []
        self._buffer_samples: int = 0

        # Rolling-window state.
        self._prev_text: str = ""
        self._last_transcribe_time: float = 0.0
        self._last_transcribe_duration: float = 0.0
        self._interval: float = BASE_INTERVAL_S

        # Silence tracking.
        self._silent_chunks: int = 0
        self._has_speech: bool = False

    # -- lifecycle -----------------------------------------------------------

    async def start(self) -> None:
        """Load the transcription model.  Call once before ``process_chunk``.

        Runs model loading in a thread executor so the event loop is not
        blocked during download / JIT warm-up.
        """
        backend = self._select_backend()
        loop = asyncio.get_running_loop()
        t0 = time.monotonic()
        await loop.run_in_executor(None, backend.load)
        elapsed = time.monotonic() - t0
        self._backend = backend
        logger.info(
            "STT ready (backend=%s, model=%s, load_time=%.1fs)",
            backend.kind,
            self._model_name,
            elapsed,
        )

    async def process_chunk(self, audio: np.ndarray) -> TranscriptEvent | None:
        """Feed an audio chunk from the mic stream.

        Returns a ``TranscriptEvent`` when there is new or finalized text,
        or ``None`` if nothing noteworthy happened (e.g. between
        transcription intervals).
        """
        if self._backend is None:
            raise RuntimeError(
                "StreamingSTT.start() must be awaited before processing chunks"
            )

        # 1. Silence tracking (before buffering).
        rms = float(np.sqrt(np.mean(audio ** 2)))
        if rms < SILENCE_RMS_THRESHOLD:
            self._silent_chunks += 1
        else:
            self._silent_chunks = 0
            self._has_speech = True

        # 2. Append to buffer.
        self._buffer.append(audio)
        self._buffer_samples += audio.shape[0]

        # 3. If silence detected after speech -> finalize.
        if (
            self._has_speech
            and self._silent_chunks >= SILENCE_CHUNKS
            and self._prev_text
        ):
            return await self._finalize()

        # 4. Guard: cap buffer at MAX_BUFFER_S -> forced transcription.
        buffer_duration = self._buffer_samples / SAMPLE_RATE
        if buffer_duration >= MAX_BUFFER_S:
            logger.warning(
                "buffer reached %.0fs cap -- forcing transcription",
                MAX_BUFFER_S,
            )
            return await self._finalize()

        # 5. Only run transcription at the adaptive interval.
        now = time.monotonic()
        if now - self._last_transcribe_time < self._interval:
            return None

        # Skip transcription if we have no speech activity at all.
        if not self._has_speech:
            return None

        # 6. Transcribe the full buffer.
        return await self._transcribe_buffer(partial=True)

    async def reset(self) -> None:
        """Clear the audio buffer and rolling-window state.

        Call after processing a complete utterance (i.e. after receiving a
        final ``TranscriptEvent``).
        """
        self._buffer.clear()
        self._buffer_samples = 0
        self._prev_text = ""
        self._silent_chunks = 0
        self._has_speech = False
        self._last_transcribe_time = 0.0
        self._last_transcribe_duration = 0.0
        self._interval = BASE_INTERVAL_S

    # -- internal helpers ----------------------------------------------------

    def _select_backend(self) -> _TranscriptionBackend:
        """Detect available backend: mlx-whisper preferred, faster-whisper
        as fallback.

        Raises ``RuntimeError`` if neither is installed.
        """
        try:
            import mlx_whisper  # noqa: F401, PLC0415

            logger.info("using mlx-whisper backend (Apple Silicon)")
            return _TranscriptionBackend(kind="mlx", model_name=self._model_name)
        except ImportError:
            pass

        try:
            import faster_whisper  # noqa: F401, PLC0415

            logger.info("using faster-whisper backend (fallback)")
            return _TranscriptionBackend(
                kind="faster-whisper", model_name=self._model_name,
            )
        except ImportError:
            pass

        raise RuntimeError(
            "No STT backend available. Install one of:\n"
            "  pip install mlx-whisper   (Apple Silicon, recommended)\n"
            "  pip install faster-whisper (any platform, fallback)"
        )

    async def _transcribe_buffer(self, *, partial: bool) -> TranscriptEvent | None:
        """Concatenate the buffer and run transcription in a thread.

        Returns a ``TranscriptEvent`` if the text changed, else ``None``.
        """
        if not self._buffer:
            return None

        audio = np.concatenate(self._buffer)
        backend = self._backend
        assert backend is not None  # guarded by process_chunk

        loop = asyncio.get_running_loop()
        t0 = time.monotonic()
        text, confidence = await loop.run_in_executor(
            None, backend.transcribe, audio,
        )
        elapsed = time.monotonic() - t0

        # Update adaptive interval.
        self._last_transcribe_time = time.monotonic()
        self._last_transcribe_duration = elapsed
        self._interval = max(
            BASE_INTERVAL_S,
            min(MAX_INTERVAL_S, elapsed + INTERVAL_HEADROOM),
        )

        # Ignore empty or hallucinated silence transcripts.
        if not text:
            return None

        # Only emit if the text actually changed.
        if text == self._prev_text:
            return None

        self._prev_text = text
        logger.debug(
            "transcript (partial=%s, %.2fs, interval=%.2fs): %s",
            partial,
            elapsed,
            self._interval,
            text[:80],
        )

        return TranscriptEvent(text=text, partial=partial, confidence=confidence)

    async def _finalize(self) -> TranscriptEvent | None:
        """Run a final transcription pass, emit a final event, and reset.

        If the buffer has speech, transcribes one last time for accuracy.
        Returns the final ``TranscriptEvent`` (with ``partial=False``).
        """
        event = await self._transcribe_buffer(partial=False)

        # If the last transcription didn't change the text but we already
        # have accumulated text, emit that as the final event.
        if event is None and self._prev_text:
            event = TranscriptEvent(
                text=self._prev_text, partial=False, confidence=1.0,
            )

        await self.reset()
        return event
