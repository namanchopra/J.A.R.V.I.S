"""Pipecat TTS processor backed by the macOS ``say`` CLI (Friday persona).

Used as the "Friday" voice on the phone interlocutor path.  ``say`` ships
with macOS and supports a wide catalogue of voices via ``say -v``.  We
default to ``Serena`` -- the bundled British female voice -- so Friday
sounds distinct from the local VibeVoice "Jarvis" voice that responds on
the Mac.

The processor is wire-compatible with ``VibeVoiceTTSService``:

* Reads ``TTSSpeakFrame`` directly *and* buffers streaming ``TextFrame``
  output between ``LLMFullResponseStartFrame`` / ``LLMFullResponseEndFrame``
  bounds, flushing per sentence.
* Emits the same frame triplet -- ``TTSStartedFrame``, one or more
  ``TTSAudioRawFrame`` (PCM s16le mono @ 16 kHz), and ``TTSStoppedFrame``.
* Exposes ``_audio_send_fn`` and ``_mobile_tts_fn`` hooks that the daemon
  wires up to broadcast audio levels to the HUD orb and audio chunks to
  mobile clients respectively.

The synthesis path shells out to ``say -o /tmp/jarvis_friday_<ts>.aiff``
and then transcodes the AIFF to raw PCM via ``ffmpeg``.  ``ffmpeg`` is the
only external binary required (already present on most dev machines; on a
fresh install ``brew install ffmpeg``).  ``afconvert`` would be available
without any extra dependency but its output framing is harder to parse, so
we lean on ffmpeg for a clean byte stream.
"""

from __future__ import annotations

import array
import asyncio
import logging
import math
import os
import re
import shutil
import subprocess
import tempfile
import time
import uuid
from collections.abc import Callable, Coroutine
from typing import Any, Final

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

logger: Final = logging.getLogger("jarvis-daemon.pipecat_tts_macos_say")

# Match VibeVoice's downstream contract -- the rest of the pipeline pushes
# Mac speaker + mobile frames at this rate, so the two providers stay
# interchangeable per turn.
SAMPLE_RATE: Final[int] = 16_000

_SENTENCE_END = re.compile(r"[.!?;]\s*$")

# ffmpeg arguments to read an AIFF file from stdin (-i pipe:0 if we ever go
# that way) or by path and emit raw PCM s16le mono at SAMPLE_RATE on stdout.
_FFMPEG_OUT_FLAGS: Final[tuple[str, ...]] = (
    "-f", "s16le",
    "-acodec", "pcm_s16le",
    "-ac", "1",
    "-ar", str(SAMPLE_RATE),
    "-loglevel", "error",
)


def _have_binary(name: str) -> bool:
    """Return True when ``name`` resolves on $PATH."""
    return shutil.which(name) is not None


class MacOSSayTTSService(FrameProcessor):
    """Pipecat TTS using macOS ``say -v <voice>`` (free, on-device).

    Default voice is ``Serena`` (British female).  Override with the
    ``voice`` constructor argument or by setting ``vibevoiceVoice`` in
    config for the Mac Jarvis voice (this class is only ever instantiated
    for the mobile Friday voice in the current router, but exposing the
    arg keeps it reusable).
    """

    def __init__(
        self,
        voice: str = "Fiona",
        sample_rate: int = SAMPLE_RATE,
        chunk_ms: int = 20,
        **kwargs: Any,
    ) -> None:
        super().__init__(**kwargs)
        self._voice = voice
        self._sample_rate = sample_rate
        # 20 ms of PCM s16le mono = sample_rate / 50 samples * 2 bytes
        self._chunk_bytes = (sample_rate // (1000 // chunk_ms)) * 2

        self._text_buffer: str = ""
        self._in_response = False

        # Audio level callback for orb animation -- set externally by main.py.
        self._audio_send_fn: Callable[[float], Coroutine[Any, Any, None]] | None = None
        # Mobile TTS callback -- sends raw PCM chunks to mobile clients via WS.
        self._mobile_tts_fn: Callable[[bytes], Coroutine[Any, Any, None]] | None = None
        self._audio_chunk_counter: int = 0

        # Cache the ``say`` / ``ffmpeg`` availability check so we only warn
        # once per process even if the user spams the bot.
        self._tooling_checked = False
        self._tooling_ok = False

    # ------------------------------------------------------------------
    # Pipecat lifecycle
    # ------------------------------------------------------------------

    async def prewarm(self) -> None:
        """No-op prewarm -- ``say`` has no model weights to load.

        Exposed so the daemon's ``hasattr(tts, "prewarm")`` block in
        ``voice_session`` runs uniformly for either provider.  We still
        do the binary-availability check here so a missing ffmpeg
        surfaces in the logs before the first turn.
        """
        self._check_tooling()

    def _check_tooling(self) -> bool:
        if self._tooling_checked:
            return self._tooling_ok
        self._tooling_checked = True
        if not _have_binary("say"):
            logger.error("MacOSSayTTSService: 'say' binary not found on PATH")
            self._tooling_ok = False
            return False
        if not _have_binary("ffmpeg"):
            logger.error(
                "MacOSSayTTSService: 'ffmpeg' not found on PATH -- install via "
                "'brew install ffmpeg' for the Friday voice to work"
            )
            self._tooling_ok = False
            return False
        self._tooling_ok = True
        return True

    # ------------------------------------------------------------------
    # Frame processing -- mirrors VibeVoiceTTSService line-for-line so the
    # behaviour matches whichever provider the router selects per turn.
    # ------------------------------------------------------------------

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
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
            # Forward the TextFrame so the downstream assistant aggregator
            # sees Jarvis's reply for context retention (mirrors VibeVoice).
            await self.push_frame(frame, direction)
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

    # ------------------------------------------------------------------
    # Synthesis
    # ------------------------------------------------------------------

    async def _synthesize(self, text: str) -> None:
        """Synthesize ``text`` to audio via ``say`` + ffmpeg transcode."""
        logger.info("Friday TTS (say -v %s): %s", self._voice, text[:60])
        self._audio_chunk_counter = 0
        await self.push_frame(TTSStartedFrame())

        try:
            if not self._check_tooling():
                return
            pcm_bytes = await asyncio.get_event_loop().run_in_executor(
                None, self._synthesize_blocking, text
            )
            await self._push_pcm_chunks(pcm_bytes)
        except Exception:
            logger.exception("MacOSSayTTSService failed for: %s", text[:60])
        finally:
            await self.push_frame(TTSStoppedFrame())
            if self._audio_send_fn is not None:
                asyncio.create_task(self._audio_send_fn(0.0))

    def _synthesize_blocking(self, text: str) -> bytes:
        """Run ``say -> AIFF -> ffmpeg -> PCM`` synchronously.

        Returns the raw PCM s16le mono byte stream at ``self._sample_rate``.
        Cleans up temporary files even on failure.
        """
        ts = f"{int(time.time() * 1000)}_{uuid.uuid4().hex[:6]}"
        aiff_path = os.path.join(tempfile.gettempdir(), f"jarvis_friday_{ts}.aiff")

        try:
            # -r controls words-per-minute. macOS default is ~175wpm which sounds
            # rushed on the phone. 150wpm matches the Mac VibeVoice cadence.
            say_cmd = ["say", "-v", self._voice, "-r", "150", "-o", aiff_path, text]
            say_res = subprocess.run(
                say_cmd,
                capture_output=True,
                check=False,
            )
            if say_res.returncode != 0:
                stderr = say_res.stderr.decode("utf-8", errors="replace")
                raise RuntimeError(
                    f"say exited with code {say_res.returncode}: {stderr.strip()}"
                )

            if not os.path.isfile(aiff_path) or os.path.getsize(aiff_path) == 0:
                raise RuntimeError(f"say produced no output at {aiff_path}")

            ffmpeg_cmd = [
                "ffmpeg",
                "-y",
                "-i", aiff_path,
                *_FFMPEG_OUT_FLAGS,
                "pipe:1",
            ]
            ff_res = subprocess.run(
                ffmpeg_cmd,
                capture_output=True,
                check=False,
            )
            if ff_res.returncode != 0:
                stderr = ff_res.stderr.decode("utf-8", errors="replace")
                raise RuntimeError(
                    f"ffmpeg exited with code {ff_res.returncode}: {stderr.strip()}"
                )
            return ff_res.stdout
        finally:
            try:
                if os.path.exists(aiff_path):
                    os.remove(aiff_path)
            except OSError:
                logger.debug("Failed to remove %s", aiff_path, exc_info=True)

    async def _push_pcm_chunks(self, pcm_bytes: bytes) -> None:
        """Split a PCM s16le blob into 20 ms frames and push downstream."""
        if not pcm_bytes:
            return
        chunk_size = self._chunk_bytes
        for i in range(0, len(pcm_bytes), chunk_size):
            chunk = pcm_bytes[i:i + chunk_size]
            await self.push_frame(TTSAudioRawFrame(
                audio=chunk,
                sample_rate=self._sample_rate,
                num_channels=1,
            ))
            self._maybe_send_audio_level(chunk)
            self._maybe_send_mobile_tts(chunk)

    # ------------------------------------------------------------------
    # Side-channel callbacks (identical to VibeVoice's wiring)
    # ------------------------------------------------------------------

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

        Throttled to every 3rd chunk to avoid flooding the WS with HUD
        updates (matches VibeVoiceTTSService's behaviour).
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

        asyncio.create_task(self._audio_send_fn(level))
