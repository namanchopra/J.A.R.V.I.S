"""Pipecat TTS processor using Cartesia Sonic 3 (streaming, ~40ms TTFB).

Cloud-based TTS that streams PCM audio over a persistent WebSocket — no
temp files, no ffmpeg, no disk I/O. Maintains the connection for low-latency
back-to-back synthesis and reconnects automatically on failure. On synthesis
error, logs and stops; there is no silent cloud fallback.
"""
from __future__ import annotations

import array
import asyncio
import base64
import json
import logging
import math
import re
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

logger: Final = logging.getLogger("jarvis-daemon.pipecat_tts_cartesia")

SAMPLE_RATE: Final[int] = 24000
_SENTENCE_END = re.compile(r'[.!?;]\s*$')
_CONNECT_TIMEOUT: Final[float] = 5.0
_SYNTH_TIMEOUT: Final[float] = 15.0


class CartesiaTTSService(FrameProcessor):
    """Pipecat TTS using Cartesia Sonic 3 streaming WebSocket API.

    40ms TTFB, streams PCM audio chunks directly — no temp files.
    Keeps a persistent WebSocket connection open across calls.
    On synthesis error, logs and stops (no silent fallback).
    """

    def __init__(
        self,
        api_key: str,
        voice_id: str = "1463a4e1-56a1-4b41-b257-728d56e93605",
        model: str = "sonic-3",
        **kwargs,
    ):
        super().__init__(**kwargs)
        self._api_key = api_key
        self._voice_id = voice_id
        self._model = model
        self._text_buffer: str = ""
        self._in_response = False
        self._ws = None  # Persistent WebSocket connection
        self._ws_lock = asyncio.Lock()
        self._consecutive_failures = 0
        # Audio level callback for orb animation -- set externally by main.py.
        self._audio_send_fn: Callable[[float], Coroutine[Any, Any, None]] | None = None
        # Mobile TTS callback -- sends raw PCM chunks to mobile clients via WS.
        self._mobile_tts_fn: Callable[[bytes], Coroutine[Any, Any, None]] | None = None
        self._audio_chunk_counter: int = 0

    def _maybe_send_mobile_tts(self, pcm_chunk: bytes) -> None:
        """Forward raw PCM audio to mobile clients via the WS bridge.

        Called for every TTS audio chunk. The callback is wired by main.py
        to ``send_mobile_tts()`` which base64-encodes and sends over WS.
        """
        if self._mobile_tts_fn is None:
            return
        try:
            asyncio.create_task(self._mobile_tts_fn(pcm_chunk))
        except Exception:
            pass

    def _maybe_send_audio_level(self, pcm_chunk: bytes) -> None:
        """Compute RMS of a PCM s16le chunk and fire the audio level callback.

        Throttled by chunk count (every 3rd chunk ≈ every 60ms of audio)
        instead of wall-clock time, because Cartesia delivers all PCM data
        in a tight loop where time.monotonic() barely advances.
        """
        if self._audio_send_fn is None:
            return

        # Send every 3rd chunk (~60ms of audio at 20ms/chunk)
        self._audio_chunk_counter += 1
        if self._audio_chunk_counter % 3 != 0:
            return

        # Decode PCM s16le samples and compute RMS
        try:
            samples = array.array("h", pcm_chunk)  # signed 16-bit
            if len(samples) == 0:
                return
            mean_sq = sum(s * s for s in samples) / len(samples)
            rms = math.sqrt(mean_sq) / 32768.0  # Normalize to 0.0-1.0
            level = min(1.0, rms * 8.0)  # Scale up for visual range
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
            await self._close_ws()
            await self.push_frame(frame, direction)
            return

        await self.push_frame(frame, direction)

    async def _synthesize(self, text: str):
        """Stream audio from Cartesia WebSocket API."""
        logger.info("TTS synthesizing: %s", text[:60])
        self._audio_chunk_counter = 0  # Reset counter for each synthesis
        await self.push_frame(TTSStartedFrame())

        try:
            await self._synthesize_cartesia(text)
            self._consecutive_failures = 0
        except Exception:
            self._consecutive_failures += 1
            logger.exception(
                "Cartesia TTS failed (attempt %d) for: %s",
                self._consecutive_failures, text[:60],
            )
            await self._close_ws()  # Force reconnect on next call
        finally:
            await self.push_frame(TTSStoppedFrame())
            # Reset orb animation level to 0 when speech ends.
            if self._audio_send_fn is not None:
                asyncio.create_task(self._audio_send_fn(0.0))

    async def _get_ws(self):
        """Get or create a persistent WebSocket connection."""
        import websockets

        if self._ws is not None:
            try:
                # Check if connection is still alive
                await self._ws.ping()
                return self._ws
            except Exception:
                self._ws = None

        url = (
            f"wss://api.cartesia.ai/tts/websocket"
            f"?api_key={self._api_key}"
            f"&cartesia_version=2026-03-01"
        )
        self._ws = await asyncio.wait_for(
            websockets.connect(url),
            timeout=_CONNECT_TIMEOUT,
        )
        logger.debug("Cartesia WebSocket connected")
        return self._ws

    async def _close_ws(self):
        """Close the persistent WebSocket connection."""
        if self._ws is not None:
            try:
                await self._ws.close()
            except Exception:
                pass
            self._ws = None

    async def _synthesize_cartesia(self, text: str):
        """Stream PCM audio from Cartesia Sonic 3 via persistent WebSocket."""
        async with self._ws_lock:
            ws = await self._get_ws()

            conn_id = uuid.uuid4().hex
            request = {
                "model_id": self._model,
                "transcript": text,
                "voice": {"mode": "id", "id": self._voice_id},
                "output_format": {
                    "container": "raw",
                    "encoding": "pcm_s16le",
                    "sample_rate": SAMPLE_RATE,
                },
                "context_id": conn_id,
            }
            await ws.send(json.dumps(request))

            chunk_size = SAMPLE_RATE * 2 // 50  # 20ms of 16-bit mono

            # Read with timeout to prevent hanging
            async def _read_response():
                async for message in ws:
                    if isinstance(message, str):
                        data = json.loads(message)

                        if data.get("error"):
                            raise RuntimeError(f"Cartesia error: {data['error']}")

                        if data.get("done", False):
                            break

                        audio_b64 = data.get("data", "")
                        if audio_b64:
                            pcm_bytes = base64.b64decode(audio_b64)
                            for i in range(0, len(pcm_bytes), chunk_size):
                                chunk = pcm_bytes[i:i + chunk_size]
                                await self.push_frame(TTSAudioRawFrame(
                                    audio=chunk,
                                    sample_rate=SAMPLE_RATE,
                                    num_channels=1,
                                ))
                                self._maybe_send_audio_level(chunk)
                                self._maybe_send_mobile_tts(chunk)

                    elif isinstance(message, bytes):
                        for i in range(0, len(message), chunk_size):
                            chunk = message[i:i + chunk_size]
                            await self.push_frame(TTSAudioRawFrame(
                                audio=chunk,
                                sample_rate=SAMPLE_RATE,
                                num_channels=1,
                            ))
                            self._maybe_send_audio_level(chunk)
                            self._maybe_send_mobile_tts(chunk)

            await asyncio.wait_for(_read_response(), timeout=_SYNTH_TIMEOUT)

