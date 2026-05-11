"""Custom Pipecat TTS processor using Microsoft Edge TTS (free neural voices).

Wraps edge-tts as a Pipecat FrameProcessor. Accumulates streamed text from the
LLM until a sentence boundary, then generates audio via edge-tts and outputs
TTSAudioRawFrame for playback.

Key design: Pipecat's LLM streams many small TextFrames (token by token).
We MUST accumulate text and synthesize whole sentences, NOT each token.
"""
from __future__ import annotations

import asyncio
import logging
import os
import re
import tempfile
from typing import Final

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

logger: Final = logging.getLogger("jarvis-daemon.pipecat_tts")

VOICE: Final[str] = "en-GB-RyanNeural"
SAMPLE_RATE: Final[int] = 24000  # Edge TTS outputs 24kHz

# Sentence-ending punctuation pattern.
_SENTENCE_END = re.compile(r'[.!?;]\s*$')


class EdgeTTSService(FrameProcessor):
    """Pipecat TTS using Microsoft Edge neural voices (free, no API key).

    Accumulates streamed text tokens until a sentence boundary or response end,
    then synthesizes the whole chunk at once via edge-tts.
    """

    def __init__(self, voice: str = VOICE, **kwargs):
        super().__init__(**kwargs)
        self._voice = voice
        self._text_buffer: str = ""
        self._in_response = False

    async def process_frame(self, frame: Frame, direction: FrameDirection):
        await super().process_frame(frame, direction)

        if isinstance(frame, TTSSpeakFrame):
            # Direct speak command (e.g. greeting) — synthesize immediately.
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
            # Accumulate streamed LLM tokens.
            text = frame.text if hasattr(frame, "text") else ""
            self._text_buffer += text

            # Synthesize at sentence boundaries for natural pacing.
            if _SENTENCE_END.search(self._text_buffer):
                chunk = self._text_buffer.strip()
                self._text_buffer = ""
                if chunk:
                    await self._synthesize(chunk)
            return  # Don't pass text frames downstream — we output audio.

        if isinstance(frame, LLMFullResponseEndFrame):
            # Flush any remaining text.
            self._in_response = False
            if self._text_buffer.strip():
                await self._synthesize(self._text_buffer.strip())
                self._text_buffer = ""
            await self.push_frame(frame, direction)
            return

        # Pass everything else through.
        await self.push_frame(frame, direction)

    async def _synthesize(self, text: str):
        """Generate speech audio from text via edge-tts."""
        try:
            import edge_tts
        except ImportError:
            logger.error("edge-tts not installed: pip install edge-tts")
            return

        logger.info("TTS synthesizing: %s", text[:60])
        await self.push_frame(TTSStartedFrame())

        try:
            with tempfile.NamedTemporaryFile(suffix=".mp3", delete=False) as tmp:
                tmp_path = tmp.name

            communicate = edge_tts.Communicate(text, self._voice)
            await communicate.save(tmp_path)

            # Decode MP3 to PCM using ffmpeg.
            proc = await asyncio.create_subprocess_exec(
                "ffmpeg", "-i", tmp_path,
                "-f", "s16le", "-acodec", "pcm_s16le",
                "-ar", str(SAMPLE_RATE), "-ac", "1",
                "-loglevel", "quiet", "-",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.DEVNULL,
            )
            pcm_data, _ = await proc.communicate()

            os.unlink(tmp_path)

            if pcm_data:
                chunk_size = SAMPLE_RATE * 2 // 50  # 20ms of 16-bit audio
                for i in range(0, len(pcm_data), chunk_size):
                    chunk = pcm_data[i:i + chunk_size]
                    await self.push_frame(TTSAudioRawFrame(
                        audio=chunk,
                        sample_rate=SAMPLE_RATE,
                        num_channels=1,
                    ))

        except Exception:
            logger.exception("Edge TTS synthesis failed for: %s", text[:60])
        finally:
            await self.push_frame(TTSStoppedFrame())
