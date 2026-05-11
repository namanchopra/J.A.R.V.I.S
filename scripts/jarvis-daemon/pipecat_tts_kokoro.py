"""Pipecat TTS processor using Kokoro (local, free, ~real-time on Apple Silicon).

Drop-in replacement for CartesiaTTSService. Uses kokoro-onnx for local neural
TTS — no API key, no network calls, no cost. Falls back to EdgeTTSService on failure.

Model files are auto-downloaded on first use to ~/.awm/models/kokoro/.

Requires: pip install kokoro-onnx
"""
from __future__ import annotations

import array
import asyncio
import logging
import math
import os
import re
from collections.abc import Callable, Coroutine
from typing import Any, Final

import numpy as np

from pipecat.frames.frames import (
    EndFrame,
    Frame,
    LLMFullResponseEndFrame,
    LLMFullResponseStartFrame,
    TextFrame,
    TTSAudioRawFrame,
    TTSSpeakFrame,
    TTSStartedFrame,
    TTSStoppedFrame,
)
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor

logger: Final = logging.getLogger("jarvis-daemon.pipecat_tts_kokoro")

SAMPLE_RATE: Final[int] = 24000
_SENTENCE_END = re.compile(r'[.!?;]\s*$')

# Model file paths — stored under ~/.awm/models/kokoro/
_MODELS_DIR: Final[str] = os.path.expanduser("~/.awm/models/kokoro")
_MODEL_FILE: Final[str] = os.path.join(_MODELS_DIR, "kokoro-v1.0.onnx")
_VOICES_FILE: Final[str] = os.path.join(_MODELS_DIR, "voices-v1.0.bin")

# Download URLs for model files
_MODEL_URL: Final[str] = (
    "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/kokoro-v1.0.onnx"
)
_VOICES_URL: Final[str] = (
    "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/voices-v1.0.bin"
)


def _ensure_models() -> tuple[str, str]:
    """Download Kokoro model files if not present. Returns (model_path, voices_path)."""
    os.makedirs(_MODELS_DIR, exist_ok=True)

    for filepath, url, name in [
        (_MODEL_FILE, _MODEL_URL, "kokoro-v1.0.onnx"),
        (_VOICES_FILE, _VOICES_URL, "voices-v1.0.bin"),
    ]:
        if not os.path.exists(filepath):
            logger.info("Downloading %s (~300MB)...", name)
            import urllib.request
            urllib.request.urlretrieve(url, filepath)
            logger.info("Downloaded %s to %s", name, filepath)

    return _MODEL_FILE, _VOICES_FILE


def _float32_to_pcm_s16le(samples: np.ndarray) -> bytes:
    """Convert float32 numpy audio samples to PCM s16le bytes."""
    # Clip to [-1.0, 1.0] then scale to int16 range
    clipped = np.clip(samples, -1.0, 1.0)
    pcm_int16 = (clipped * 32767).astype(np.int16)
    return pcm_int16.tobytes()


class KokoroTTSService(FrameProcessor):
    """Pipecat TTS using Kokoro (local, free, ~real-time).

    Runs the Kokoro 82M parameter neural TTS model locally via ONNX Runtime.
    No API key needed. ~300MB model download on first use.
    Falls back to EdgeTTSService on failure.
    """

    def __init__(
        self,
        voice: str = "af_sarah",
        speed: float = 1.0,
        lang: str = "en-us",
        fallback_voice: str = "en-GB-RyanNeural",
        **kwargs,
    ):
        super().__init__(**kwargs)
        self._voice = voice
        self._speed = speed
        self._lang = lang
        self._fallback_voice = fallback_voice
        self._text_buffer: str = ""
        self._in_response = False
        self._kokoro = None  # Lazy-loaded Kokoro instance
        self._kokoro_lock = asyncio.Lock()
        self._consecutive_failures = 0
        # Audio level callback for orb animation — set externally by main.py.
        self._audio_send_fn: Callable[[float], Coroutine[Any, Any, None]] | None = None
        # Mobile TTS callback — sends raw PCM chunks to mobile clients via WS.
        self._mobile_tts_fn: Callable[[bytes], Coroutine[Any, Any, None]] | None = None
        self._audio_chunk_counter: int = 0

    async def _get_kokoro(self):
        """Lazy-load the Kokoro model (downloads on first use)."""
        if self._kokoro is not None:
            return self._kokoro

        async with self._kokoro_lock:
            if self._kokoro is not None:
                return self._kokoro

            from kokoro_onnx import Kokoro

            model_path, voices_path = await asyncio.get_event_loop().run_in_executor(
                None, _ensure_models
            )
            # Load model in executor to avoid blocking the event loop
            self._kokoro = await asyncio.get_event_loop().run_in_executor(
                None, lambda: Kokoro(model_path, voices_path)
            )
            logger.info("Kokoro TTS model loaded (voice=%s, speed=%.1f)", self._voice, self._speed)
            return self._kokoro

    def _maybe_send_mobile_tts(self, pcm_chunk: bytes) -> None:
        """Forward raw PCM audio to mobile clients via the WS bridge."""
        if self._mobile_tts_fn is None:
            return
        try:
            asyncio.create_task(self._mobile_tts_fn(pcm_chunk))
        except Exception:
            pass

    def _maybe_send_audio_level(self, pcm_chunk: bytes) -> None:
        """Compute RMS of a PCM s16le chunk and fire the audio level callback.

        Throttled by chunk count (every 3rd chunk ≈ every 60ms of audio).
        """
        if self._audio_send_fn is None:
            return

        self._audio_chunk_counter += 1
        if self._audio_chunk_counter % 3 != 0:
            return

        try:
            samples = array.array("h", pcm_chunk)
            if len(samples) == 0:
                return
            mean_sq = sum(s * s for s in samples) / len(samples)
            rms = math.sqrt(mean_sq) / 32768.0
            level = min(1.0, rms * 8.0)
        except Exception:
            return

        logger.info("audio_level: %.3f (chunk #%d)", level, self._audio_chunk_counter)
        asyncio.create_task(self._audio_send_fn(level))

    async def process_frame(self, frame: Frame, direction: FrameDirection):
        await super().process_frame(frame, direction)

        if isinstance(frame, TTSSpeakFrame):
            text = frame.text if hasattr(frame, "text") else str(frame)
            if text.strip():
                await self._synthesize(text.strip())
            return

        if isinstance(frame, LLMFullResponseStartFrame):
            self._in_response = True
            self._text_buffer = ""
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, TextFrame):
            text = frame.text if hasattr(frame, "text") else ""
            self._text_buffer += text
            if _SENTENCE_END.search(self._text_buffer):
                chunk = self._text_buffer.strip()
                self._text_buffer = ""
                if chunk:
                    await self._synthesize(chunk)
            return

        if isinstance(frame, LLMFullResponseEndFrame):
            self._in_response = False
            if self._text_buffer.strip():
                await self._synthesize(self._text_buffer.strip())
                self._text_buffer = ""
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, EndFrame):
            await self.push_frame(frame, direction)
            return

        await self.push_frame(frame, direction)

    async def _synthesize(self, text: str):
        """Synthesize text to audio via Kokoro, with Edge TTS fallback."""
        logger.info("TTS synthesizing: %s", text[:60])
        self._audio_chunk_counter = 0
        await self.push_frame(TTSStartedFrame())

        try:
            await self._synthesize_kokoro(text)
            self._consecutive_failures = 0
        except Exception as e:
            self._consecutive_failures += 1
            logger.warning(
                "Kokoro TTS failed (%s, attempt %d), falling back to Edge TTS",
                e, self._consecutive_failures,
            )
            try:
                await self._synthesize_edge_fallback(text)
            except Exception:
                logger.exception("Edge TTS fallback also failed for: %s", text[:60])
        finally:
            await self.push_frame(TTSStoppedFrame())
            if self._audio_send_fn is not None:
                asyncio.create_task(self._audio_send_fn(0.0))

    async def _synthesize_kokoro(self, text: str):
        """Generate audio using Kokoro local model with streaming."""
        kokoro = await self._get_kokoro()

        chunk_size = SAMPLE_RATE * 2 // 50  # 20ms of 16-bit mono PCM

        # Use streaming API for lower time-to-first-byte
        stream = kokoro.create_stream(
            text,
            voice=self._voice,
            speed=self._speed,
            lang=self._lang,
        )

        async for samples, sample_rate in stream:
            # Convert float32 numpy array to PCM s16le bytes
            pcm_data = _float32_to_pcm_s16le(samples)

            # Push in 20ms chunks for consistent frame timing
            for i in range(0, len(pcm_data), chunk_size):
                chunk = pcm_data[i:i + chunk_size]
                await self.push_frame(TTSAudioRawFrame(
                    audio=chunk,
                    sample_rate=SAMPLE_RATE,
                    num_channels=1,
                ))
                self._maybe_send_audio_level(chunk)
                self._maybe_send_mobile_tts(chunk)

    async def _synthesize_edge_fallback(self, text: str):
        """Fallback to Edge TTS when Kokoro fails."""
        import edge_tts
        import tempfile

        with tempfile.NamedTemporaryFile(suffix=".mp3", delete=False) as tmp:
            tmp_path = tmp.name

        try:
            communicate = edge_tts.Communicate(text, self._fallback_voice)
            await communicate.save(tmp_path)

            proc = await asyncio.create_subprocess_exec(
                "ffmpeg", "-i", tmp_path,
                "-f", "s16le", "-acodec", "pcm_s16le",
                "-ar", str(SAMPLE_RATE), "-ac", "1",
                "-loglevel", "quiet", "-",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.DEVNULL,
            )
            pcm_data, _ = await proc.communicate()

            if pcm_data:
                chunk_size = SAMPLE_RATE * 2 // 50
                for i in range(0, len(pcm_data), chunk_size):
                    chunk = pcm_data[i:i + chunk_size]
                    await self.push_frame(TTSAudioRawFrame(
                        audio=chunk,
                        sample_rate=SAMPLE_RATE,
                        num_channels=1,
                    ))
                    self._maybe_send_audio_level(chunk)
                    self._maybe_send_mobile_tts(chunk)
            else:
                logger.warning("Edge TTS produced no audio for: %s", text[:40])
        finally:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
