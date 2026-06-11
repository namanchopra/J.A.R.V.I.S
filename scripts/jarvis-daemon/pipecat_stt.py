"""Custom Pipecat STT processor using NVIDIA Parakeet (primary) with
mlx-whisper / faster-whisper fallback.

Wraps local speech-to-text transcription as a Pipecat FrameProcessor.
Accumulates audio frames, transcribes periodically, and emits
TranscriptionFrame when speech ends.

Backend priority (platform-aware, per TASK-037 in plans/jarvis-windows-port):
  1. NVIDIA NeMo Parakeet (``nemo.collections.asr``)         -- any platform
  2. mlx-whisper                                            -- darwin/arm64 only
  3. faster-whisper (CPU/GPU via CTranslate2)               -- any platform

The mlx-whisper attempt is skipped entirely outside darwin/arm64 because the
MLX backend is hard-pinned to Apple Silicon (it imports the ``mlx`` Metal
runtime) -- attempting it on Windows / Linux just spams the log with the
same ImportError and adds startup latency. On those platforms we go
directly to faster-whisper, which is CTranslate2-based and prefers a CUDA
GPU when available, falling back cleanly to CPU otherwise.

Key design: Pipecat 1.0's ``start(StartFrame)`` override is no longer called,
so the model is loaded lazily on the first audio frame. The STT doesn't
depend on VAD events from the downstream aggregator -- it transcribes on a
timer and detects end-of-speech by comparing consecutive transcriptions.
"""
from __future__ import annotations

import asyncio
import logging
import os
import platform
import sys
import tempfile
import time
from dataclasses import dataclass
from typing import Final

import numpy as np

from pipecat.frames.frames import (
    AudioRawFrame,
    BotStartedSpeakingFrame,
    BotStoppedSpeakingFrame,
    EndFrame,
    Frame,
    InputAudioRawFrame,
    InterimTranscriptionFrame,
    TranscriptionFrame,
    UserStartedSpeakingFrame,
    UserStoppedSpeakingFrame,
)
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor

logger: Final = logging.getLogger("jarvis-daemon.pipecat_stt")


@dataclass
class MobileAudioRawFrame(InputAudioRawFrame):
    """Marker subclass for audio sourced from the mobile WS bridge.

    Inherits from ``InputAudioRawFrame`` (not bare ``AudioRawFrame``) so the
    Pipecat ``Frame`` metadata fields (``id``, ``transport_destination``,
    ``broadcast_sibling_id``, etc.) are initialised properly.

    The HUD mute (``force_muted``) and the bot-speaking gate exist to keep
    the Mac mic from capturing echo or background noise while Jarvis is
    talking. Mobile audio arrives via push-to-talk -- the user has made an
    explicit intent gesture, so neither gate should silence it. STT
    distinguishes mobile frames via this subclass and bypasses both gates.
    """


SAMPLE_RATE: Final[int] = 16000
TRANSCRIBE_INTERVAL_S: Final[float] = 0.5
MAX_BUFFER_S: Final[float] = 15.0
SILENCE_FINALIZE_S: Final[float] = 1.0


def _resolve_model_dir() -> str | None:
    """Return the directory where Jarvis bundled models live.

    Priority:
      1. ``JARVIS_BUNDLED_MODELS_DIR`` env var (set by the Go side in a
         production .app bundle to ``<Resources>/models``).
      2. ``~/.jarvis/models`` (dev-mode default after Phase 1 migration).
      3. ``None`` — caller falls back to the HuggingFace cache / network.
    """
    bundled = os.environ.get("JARVIS_BUNDLED_MODELS_DIR")
    if bundled and os.path.isdir(bundled):
        return bundled
    home_jarvis = os.path.expanduser("~/.jarvis/models")
    if os.path.isdir(home_jarvis):
        return home_jarvis
    return None


def _resolve_whisper_repo(model_name: str) -> str:
    """Resolve the mlx-whisper ``path_or_hf_repo`` argument.

    If a local snapshot is bundled (production .app) or cached
    (``~/.jarvis/models/whisper-*``), return its absolute path so
    mlx-whisper loads from disk and never hits the network. Otherwise
    return the canonical HuggingFace repo id and let mlx-whisper download
    into its own cache.

    Two on-disk layouts are supported under the resolved models dir:
      - ``whisper-small``       (matches build/scripts/fetch-models.sh)
      - ``whisper-small.en-mlx`` (matches the upstream HF repo name)
    """
    resolved_dir = _resolve_model_dir()
    if resolved_dir:
        candidates = [
            os.path.join(resolved_dir, f"whisper-{model_name}"),
            os.path.join(resolved_dir, f"whisper-{model_name}-mlx"),
            os.path.join(resolved_dir, f"whisper-{model_name}.en-mlx"),
        ]
        for path in candidates:
            if os.path.isdir(path):
                return path
    return f"mlx-community/whisper-{model_name}-mlx"

def _is_apple_silicon() -> bool:
    """Return True iff the daemon is running on darwin/arm64.

    mlx-whisper requires Apple's MLX Metal runtime, which is built for
    darwin/arm64 only. We use this guard to skip the mlx-whisper import
    attempt on Windows / Linux / Intel macOS rather than letting it fail
    with a confusing ImportError every cold start.

    Implemented in terms of ``sys.platform`` + ``platform.machine`` so it
    works on every interpreter we ship (CPython 3.13 from python-build-
    standalone on Win11 and the macOS stock Python alike).
    """
    if not sys.platform.startswith("darwin"):
        return False
    machine = platform.machine().lower()
    return machine in {"arm64", "aarch64"}


def _detect_cuda() -> bool:
    """Return True if a CUDA-capable GPU is visible to PyTorch.

    Used by the faster-whisper loader on non-Apple platforms to decide
    between ``device="cuda"`` (with FP16 compute) and ``device="cpu"``
    (with INT8 compute). Any failure -- torch missing, NVML failure,
    driver mismatch -- is treated as "no CUDA available" so we fall
    back to CPU rather than crash the STT load path. This mirrors the
    fallback behaviour TASK-037 acceptance criterion #3 mandates.

    We import torch lazily and guard every step so this helper can be
    called from any platform without side-effects.
    """
    try:
        import torch  # noqa: PLC0415 -- deferred to avoid a hard dep
    except Exception:
        # torch isn't installed (or DLLs failed to load on Windows) --
        # just fall back to CPU. faster-whisper's CT2 backend ships its
        # own CUDA loader, but PyTorch is the most reliable way to ask
        # "is a usable GPU present?" on this machine.
        return False
    try:
        return bool(torch.cuda.is_available())
    except Exception:
        return False


# Parakeet model identifiers, tried in order.
_PARAKEET_MODELS: Final[tuple[str, ...]] = (
    "nvidia/parakeet-tdt-0.6b-v2",
    "nvidia/parakeet-ctc-0.6b",
)

# Whisper hallucination patterns — common false transcriptions on silence/noise.
_HALLUCINATION_PATTERNS: Final[set[str]] = {
    "you", "thank you", "thanks", "bye", "okay",
    ".", "..", "...", ". . .", ". . . . .", "! ! !",
    "thank you.", "thanks for watching.", "thanks for watching",
    "subscribe", "like and subscribe",
    "the end", "the end.", "you know",
}


def _is_hallucination(text: str) -> bool:
    """Return True if the text is likely a Whisper hallucination."""
    t = text.strip().lower().rstrip(".")
    if t in _HALLUCINATION_PATTERNS:
        return True
    # Repetitive single chars or very short non-word output
    if len(t) < 3:
        return True
    # All punctuation / whitespace
    if not any(c.isalpha() for c in t):
        return True
    # Repetitive patterns (e.g., "g g g g g g")
    words = t.split()
    if len(words) >= 3 and len(set(words)) == 1:
        return True
    # Repeated phrases: split into sentences and check for duplicates.
    # Catches "I'm going to the hospital. I'm going to the hospital. ..."
    sentences = [s.strip() for s in text.strip().replace(".", "\n").split("\n") if s.strip()]
    if len(sentences) >= 3 and len(set(s.lower() for s in sentences)) <= 2:
        return True
    # Very long output from silence is almost always hallucination.
    # Real speech rarely produces 200+ chars in a single STT chunk.
    if len(t) > 200 and len(sentences) >= 5 and len(set(s.lower() for s in sentences)) <= 3:
        return True
    # N-gram repetition loop. Whisper, when fed extended near-silence or
    # microphone hum, can output things like:
    #   "I'm going to do a little bit of a little bit of a little bit of ..."
    # The sentence-level check above misses this because there are no
    # full-sentence boundaries. Use the unique-word ratio instead: real
    # speech maintains >35% unique tokens; a stuck loop falls well below.
    if len(words) >= 12:
        unique_ratio = len(set(words)) / len(words)
        if unique_ratio < 0.35:
            return True
    return False


class LocalWhisperSTT(FrameProcessor):
    """Pipecat STT using Parakeet (primary) with mlx-whisper/faster-whisper fallback."""

    def __init__(self, model_name: str = "small.en", **kwargs):
        super().__init__(**kwargs)
        self._model_name = model_name
        self._backend = None
        self._backend_loaded = False
        self._buffer: list[np.ndarray] = []
        self._buffer_samples = 0
        self._prev_text = ""
        self._last_transcribe = 0.0
        self._last_new_text_time = 0.0
        self._transcribing = False  # Guard against overlapping calls
        self._bot_speaking = False  # Suppress audio during bot speech to prevent echo
        self.force_muted = False   # Hard mute — survives BotStoppedSpeakingFrame resets

    # ------------------------------------------------------------------
    # Backend loading
    # ------------------------------------------------------------------

    async def _ensure_backend(self):
        """Load the STT model on first use (lazy init)."""
        if self._backend_loaded:
            return
        self._backend_loaded = True
        # Route the (potentially huge) first-run download through the
        # model_status reporter so the HUD's first-run overlay can show
        # progress. ensure_model is a no-op when the cache is already
        # warm. We only gate the mlx-whisper download path -- Parakeet
        # and faster-whisper handle their own caches internally and are
        # also best-effort fallbacks; reporting their progress would
        # confuse the HUD when the user only ever needs one backend.
        try:
            import model_status
            await model_status.ensure_model("whisper")
        except Exception:
            logger.debug("model_status.ensure_model('whisper') failed; "
                         "falling through to backend loader", exc_info=True)
        logger.info("Loading STT model (may take a few seconds)...")
        self._backend = await asyncio.get_event_loop().run_in_executor(
            None, self._load_backend
        )
        if self._backend:
            logger.info(
                "LocalWhisperSTT ready (backend=%s)", self._backend[0]
            )
        else:
            logger.error("LocalWhisperSTT: no backend available")

    def _load_backend(self):
        """Try backends in priority order. Returns ``(kind, model)`` or ``None``.

        Priority:
          1. Parakeet (NeMo ASR) -- best accuracy for English, any platform
          2. mlx-whisper         -- fast on Apple Silicon (darwin/arm64 only)
          3. faster-whisper      -- broad hardware support (CUDA + CPU)

        Platform-aware ordering per TASK-037: mlx-whisper is skipped on
        non-Apple-Silicon machines (Windows / Linux / Intel macOS) because
        its Metal runtime hard-requires darwin/arm64. Skipping it there
        avoids an ImportError + several seconds of fruitless retries on
        every cold start, and ensures Windows users land on the
        cross-platform faster-whisper path which is the only Whisper
        implementation that works on their hardware.
        """
        # --- 1. NVIDIA Parakeet via NeMo ---
        backend = self._try_load_parakeet()
        if backend is not None:
            return backend

        # --- 2. mlx-whisper (Apple Silicon only) ---
        # Hard-gate on darwin/arm64. The mlx_whisper package will import
        # fine on Linux/Windows but every transcribe() call fails because
        # mlx itself only ships Metal kernels. Better to skip cleanly here
        # so Windows logs read "Using faster-whisper backend" first try.
        if _is_apple_silicon():
            backend = self._try_load_mlx_whisper()
            if backend is not None:
                return backend
        else:
            logger.info(
                "Skipping mlx-whisper backend (requires darwin/arm64, "
                "platform=%s/%s)",
                sys.platform,
                platform.machine(),
            )

        # --- 3. faster-whisper (CTranslate2) ---
        backend = self._try_load_faster_whisper()
        if backend is not None:
            return backend

        logger.error("No STT backend available")
        return None

    def _try_load_parakeet(self):
        """Attempt to load NVIDIA Parakeet via ``nemo.collections.asr``."""
        try:
            import nemo.collections.asr as nemo_asr  # noqa: F401
        except ImportError:
            logger.info("nemo.collections.asr not installed, skipping Parakeet")
            return None

        for model_id in _PARAKEET_MODELS:
            try:
                model = nemo_asr.models.ASRModel.from_pretrained(model_id)
                logger.info("Using Parakeet backend (model=%s)", model_id)
                return ("parakeet", model)
            except Exception as exc:
                logger.info(
                    "Parakeet model %s unavailable (%s), trying next",
                    model_id,
                    exc,
                )

        logger.info("No Parakeet model could be loaded, falling back to Whisper")
        return None

    def _try_load_mlx_whisper(self):
        """Attempt to load mlx-whisper (Apple Silicon optimised)."""
        try:
            import mlx_whisper  # noqa: F401

            repo = _resolve_whisper_repo(self._model_name)
            # Warm-up transcription to validate the model is usable.
            _dummy = np.zeros(SAMPLE_RATE, dtype=np.float32)
            mlx_whisper.transcribe(_dummy, path_or_hf_repo=repo, language="en")
            logger.info("Using mlx-whisper backend (model=%s)", repo)
            return ("mlx", None)
        except Exception as exc:
            logger.info("mlx-whisper not available (%s), trying faster-whisper", exc)
            return None

    def _try_load_faster_whisper(self):
        """Attempt to load faster-whisper (CTranslate2).

        Device + compute_type selection (TASK-037 AC #1 and AC #3):
          - CUDA visible: ``device="cuda"`` + ``compute_type="float16"`` --
            the recommended config for CTranslate2 on NVIDIA GPUs; ~5-6x
            faster than the base Whisper at the same accuracy. If CUDA
            initialisation fails at WhisperModel() construction time
            (driver mismatch, runtime DLL missing, OOM at load) we fall
            back to CPU automatically rather than failing the whole STT
            load.
          - No CUDA: ``device="cpu"`` + ``compute_type="int8"`` -- INT8 is
            the only compute_type that performs acceptably on CPU; FP32
            CPU inference is too slow to keep up with realtime audio.

        Models are loaded from the standard HuggingFace cache
        (``~/.cache/huggingface/hub`` on every platform) which is the
        same cache the macOS path uses for mlx-whisper, satisfying the
        "model loaded from same HF cache" acceptance criterion.
        """
        try:
            from faster_whisper import WhisperModel
        except Exception as exc:
            logger.error("faster-whisper not importable: %s", exc)
            return None

        cuda_available = _detect_cuda()
        if cuda_available:
            try:
                model = WhisperModel(
                    self._model_name,
                    device="cuda",
                    compute_type="float16",
                )
                logger.info(
                    "Using faster-whisper backend (device=cuda, "
                    "compute_type=float16, model=%s)",
                    self._model_name,
                )
                return ("faster-whisper", model)
            except Exception as exc:
                # Most common path: CUDA visible but ctranslate2's CUDA
                # runtime can't load (driver/cuDNN mismatch, OOM on a
                # busy GPU). Fall back to CPU rather than bubbling up --
                # AC #3 explicitly requires "CPU fallback works".
                logger.warning(
                    "faster-whisper CUDA load failed (%s); "
                    "falling back to CPU",
                    exc,
                )

        try:
            model = WhisperModel(
                self._model_name,
                device="cpu",
                compute_type="int8",
            )
            logger.info(
                "Using faster-whisper backend (device=cpu, "
                "compute_type=int8, model=%s)",
                self._model_name,
            )
            return ("faster-whisper", model)
        except Exception as exc:
            logger.error("faster-whisper CPU load failed: %s", exc)
            return None

    # ------------------------------------------------------------------
    # Frame processing
    # ------------------------------------------------------------------

    async def process_frame(self, frame: Frame, direction: FrameDirection):
        await super().process_frame(frame, direction)

        # Track bot speaking state to suppress echo.
        # BotStartedSpeakingFrame is broadcast by the output transport
        # and propagates to all processors in the pipeline.
        if isinstance(frame, BotStartedSpeakingFrame):
            self._bot_speaking = True
            # Clear buffer so we don't transcribe bot's own speech
            self._buffer.clear()
            self._buffer_samples = 0
            self._prev_text = ""
            self._last_new_text_time = 0.0
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, BotStoppedSpeakingFrame):
            self._bot_speaking = False
            # Clear buffer again — any audio captured during speech is bot echo
            self._buffer.clear()
            self._buffer_samples = 0
            self._prev_text = ""
            self._last_new_text_time = 0.0
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, AudioRawFrame):
            # Skip audio while bot is speaking or hard-muted, EXCEPT for
            # mobile push-to-talk audio -- the phone user has made an
            # explicit press-and-hold gesture, so the HUD mute (Mac-mic
            # gate) and the bot-speaking gate must not silence them.
            is_mobile = isinstance(frame, MobileAudioRawFrame)
            if (self._bot_speaking or self.force_muted) and not is_mobile:
                await self.push_frame(frame, direction)
                return

            await self._ensure_backend()

            if not self._backend:
                await self.push_frame(frame, direction)
                return

            # Convert 16-bit PCM bytes to float32 numpy array.
            audio = (
                np.frombuffer(frame.audio, dtype=np.int16).astype(np.float32)
                / 32768.0
            )
            self._buffer.append(audio)
            self._buffer_samples += len(audio)

            # Trim buffer to MAX_BUFFER_S to keep transcription fast.
            max_samples = int(MAX_BUFFER_S * SAMPLE_RATE)
            while self._buffer_samples > max_samples and self._buffer:
                removed = self._buffer.pop(0)
                self._buffer_samples -= len(removed)

            # Transcribe periodically (avoid overlapping calls).
            now = time.monotonic()
            if (
                not self._transcribing
                and (now - self._last_transcribe) >= TRANSCRIBE_INTERVAL_S
                and self._buffer_samples >= SAMPLE_RATE
            ):
                await self._do_transcription()

            # Detect end-of-speech: had text before, but no new text for
            # SILENCE_FINALIZE_S.
            if (
                self._prev_text
                and self._last_new_text_time > 0
                and (now - self._last_new_text_time) > SILENCE_FINALIZE_S
            ):
                await self._emit_final()

        elif isinstance(frame, EndFrame):
            if self._prev_text:
                await self._emit_final()

        # Always pass frames through.
        await self.push_frame(frame, direction)

    # ------------------------------------------------------------------
    # Transcription
    # ------------------------------------------------------------------

    async def _do_transcription(self):
        """Run transcription in a thread executor."""
        if not self._buffer or not self._backend or self._transcribing:
            return

        self._transcribing = True
        try:
            audio = np.concatenate(self._buffer)
            kind, model = self._backend
            text = await asyncio.get_event_loop().run_in_executor(
                None, self._transcribe_sync, audio, kind, model
            )
            self._last_transcribe = time.monotonic()

            if text and text.strip():
                clean = text.strip()
                if clean != self._prev_text:
                    self._prev_text = clean
                    self._last_new_text_time = time.monotonic()
                    logger.debug("STT interim: %s", clean[:80])
                    await self.push_frame(
                        InterimTranscriptionFrame(
                            text=clean,
                            user_id="user",
                            timestamp=str(time.time()),
                        )
                    )
            else:
                # Silence detected -- if we had accumulated text, finalize.
                if self._prev_text:
                    await self._emit_final()

        finally:
            self._transcribing = False

    async def _emit_final(self):
        """Emit a final ``TranscriptionFrame`` and reset state."""
        if not self._prev_text:
            return
        final_text = self._prev_text.strip()
        self._prev_text = ""
        self._last_new_text_time = 0.0
        self._buffer.clear()
        self._buffer_samples = 0

        if final_text and not _is_hallucination(final_text):
            logger.info("STT final: %s", final_text[:80])
            await self.push_frame(
                TranscriptionFrame(
                    text=final_text,
                    user_id="user",
                    timestamp=str(time.time()),
                )
            )
        elif final_text:
            logger.debug("STT filtered hallucination: %s", final_text[:40])

    def _transcribe_sync(self, audio: np.ndarray, kind: str, model) -> str:
        """Synchronous transcription dispatched by backend type.

        Runs inside ``run_in_executor`` -- MUST NOT touch the event loop.
        """
        try:
            if kind == "parakeet":
                return self._transcribe_parakeet(audio, model)
            elif kind == "mlx":
                return self._transcribe_mlx(audio)
            else:
                return self._transcribe_faster_whisper(audio, model)
        except Exception:
            logger.debug("Transcription error", exc_info=True)
            return ""

    # -- Parakeet -------------------------------------------------------

    @staticmethod
    def _transcribe_parakeet(audio: np.ndarray, model) -> str:
        """Transcribe using NVIDIA Parakeet (NeMo ASR).

        Parakeet's ``model.transcribe()`` accepts file paths or tensors.
        We write a temporary WAV file to avoid API surface differences
        across NeMo versions.
        """
        import soundfile as sf

        tmp_fd, tmp_path = tempfile.mkstemp(suffix=".wav")
        try:
            os.close(tmp_fd)
            sf.write(tmp_path, audio, samplerate=SAMPLE_RATE)
            results = model.transcribe([tmp_path])

            # NeMo returns different shapes depending on the model variant.
            # Handle both ``Hypothesis`` objects and plain strings.
            if not results:
                return ""

            first = results[0]

            # ``results`` may be a tuple ``(hypotheses, _)`` in some NeMo
            # versions.
            if isinstance(first, (list, tuple)):
                first = first[0] if first else ""

            if hasattr(first, "text"):
                return first.text.strip()
            return str(first).strip()
        finally:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass

    # -- mlx-whisper ----------------------------------------------------

    def _transcribe_mlx(self, audio: np.ndarray) -> str:
        """Transcribe using mlx-whisper (Apple Silicon)."""
        import mlx_whisper

        repo = _resolve_whisper_repo(self._model_name)
        result = mlx_whisper.transcribe(
            audio, path_or_hf_repo=repo, language="en"
        )
        return result.get("text", "").strip()

    # -- faster-whisper -------------------------------------------------

    @staticmethod
    def _transcribe_faster_whisper(audio: np.ndarray, model) -> str:
        """Transcribe using faster-whisper (CTranslate2)."""
        segments, _ = model.transcribe(
            audio, language="en", beam_size=1, vad_filter=True, temperature=0.0
        )
        return " ".join(s.text.strip() for s in segments).strip()
