#!/usr/bin/env python3
"""Jarvis voice daemon -- Python sidecar for Vibedeck (AWM).

Runs an asyncio event loop that:
  1. Loads config from ~/.awm/config.json
  2. Connects to the Go app via WebSocket at ws://localhost:{port}/ws/jarvis
  3. Runs the Pipecat voice pipeline (mic -> STT -> LLM -> TTS -> speaker)
  4. Handles messages from Go (context, tool_result, command)
  5. Sends messages to Go (transcript, response, state, audio_level, tool_call)

Usage:
    python3 scripts/jarvis-daemon/main.py [--debug]

The daemon auto-reconnects on WebSocket failures with exponential backoff.
Shut down cleanly with Ctrl+C (SIGINT) or SIGTERM.

Voice pipeline powered by Pipecat (https://github.com/pipecat-ai/pipecat).
Custom STT/TTS processors (local Whisper, Edge TTS) added in TASK-002.
LLM tool calling and Jarvis personality configured in TASK-003.
Tool bridge for Pipecat function calling added in TASK-004.
"""

from __future__ import annotations

import argparse
import asyncio
import base64
import datetime
import json
import logging
import signal
import sys
import uuid
from typing import Any

# ---------------------------------------------------------------------------
# Pipecat imports
# ---------------------------------------------------------------------------

try:
    from pipecat.audio.vad.silero import SileroVADAnalyzer
    from pipecat.audio.vad.vad_analyzer import VADParams
    from pipecat.frames.frames import (
        AudioRawFrame,
        BotStartedSpeakingFrame,
        BotStoppedSpeakingFrame,
        Frame,
        LLMMessagesAppendFrame,
        LLMRunFrame,
        TextFrame,
        TranscriptionFrame,
        InterimTranscriptionFrame,
        TTSSpeakFrame,
        UserStartedSpeakingFrame,
    )
    from pipecat.pipeline.pipeline import Pipeline
    from pipecat.pipeline.runner import PipelineRunner
    from pipecat.pipeline.task import PipelineParams, PipelineTask
    from pipecat.processors.aggregators.llm_context import LLMContext
    from pipecat.processors.aggregators.llm_response_universal import (
        LLMContextAggregatorPair,
        LLMUserAggregatorParams,
    )
    from pipecat.processors.frame_processor import FrameDirection, FrameProcessor
    from pipecat.services.anthropic.llm import AnthropicLLMService
    from pipecat.transports.local.audio import (
        LocalAudioTransport,
        LocalAudioTransportParams,
    )
except ImportError as _pipecat_err:
    print(
        "[jarvis-daemon] ERROR: pipecat-ai not installed.\n"
        '  pip install "pipecat-ai[silero,anthropic,local]"\n'
        f"  Detail: {_pipecat_err}",
        file=sys.stderr,
    )
    sys.exit(1)

# ---------------------------------------------------------------------------
# WebSocket client
# ---------------------------------------------------------------------------

try:
    import websockets
    from websockets.asyncio.client import ClientConnection
    from websockets.exceptions import (
        ConnectionClosed,
        InvalidStatus,
        InvalidURI,
    )
except ImportError:
    print(
        "[jarvis-daemon] ERROR: 'websockets' package not installed.\n"
        "  pip install websockets>=12.0",
        file=sys.stderr,
    )
    sys.exit(1)

# ---------------------------------------------------------------------------
# Existing daemon modules (kept as-is)
# ---------------------------------------------------------------------------

from config import get_auth_token, get_ws_url, load_config
from monitor import BackgroundMonitor
from tools import ToolExecutor
from memory import ConversationMemory
from vector_memory import VectorMemory
from priority import PriorityEngine, score_event, JarvisEvent
from alerter import Alerter
from events import EventStore
from briefing import BriefingSystem
from research import ResearchAgent
from pollers.sessions import SessionPoller
from pollers.session_output import SessionOutputPoller
from browser import BrowserController
# Slack monitoring now via MCP, not Playwright browser.
from mcp_client import MCPManager, load_mcp_configs
from tool_bridge import ToolBridge, DeferredResultQueue
from pipecat_llm import get_anthropic_tools, update_system_instruction, MODEL, JARVIS_SYSTEM as JARVIS_SYSTEM_FULL
from pipecat_stt import LocalWhisperSTT
from pipecat_tts_cartesia import CartesiaTTSService
from pipecat_tts_kokoro import KokoroTTSService
from pipecat_tts_vibevoice import VibeVoiceTTSService

# Pipecat ToolsSchema imports (required for LLMContext.set_tools in Pipecat 1.0)
from pipecat.adapters.schemas.tools_schema import ToolsSchema
from pipecat.adapters.schemas.function_schema import FunctionSchema

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logger = logging.getLogger("jarvis-daemon")


def _setup_logging(*, debug: bool = False) -> None:
    """Configure logging to stderr with [jarvis-daemon] prefix."""
    level = logging.DEBUG if debug else logging.INFO
    handler = logging.StreamHandler(sys.stderr)
    handler.setFormatter(
        logging.Formatter(
            fmt="[jarvis-daemon] %(asctime)s %(levelname)s %(message)s",
            datefmt="%H:%M:%S",
        )
    )
    root = logging.getLogger("jarvis-daemon")
    root.setLevel(level)
    root.addHandler(handler)
    # Quiet down the websockets library unless we're debugging.
    ws_logger = logging.getLogger("websockets")
    ws_logger.setLevel(logging.DEBUG if debug else logging.WARNING)


# ---------------------------------------------------------------------------
# Message protocol helpers (Daemon <-> Go)
# ---------------------------------------------------------------------------

# --- Daemon -> Go ---


async def send_transcript(
    ws: ClientConnection,
    text: str,
    *,
    partial: bool = False,
) -> None:
    """Send a transcript message to Go."""
    await ws.send(json.dumps({
        "type": "transcript",
        "text": text,
        "partial": partial,
    }))


async def send_response(ws: ClientConnection, text: str) -> None:
    """Send a Jarvis response message to Go."""
    await ws.send(json.dumps({
        "type": "response",
        "text": text,
        "role": "jarvis",
    }))


async def send_state(ws: ClientConnection, state: str) -> None:
    """Send a state change to Go. State: idle|listening|thinking|speaking."""
    await ws.send(json.dumps({
        "type": "state",
        "state": state,
    }))


async def send_audio_level(ws: ClientConnection, level: float) -> None:
    """Send audio amplitude level to Go for orb animation."""
    try:
        await ws.send(json.dumps({
            "type": "audio_level",
            "level": round(max(0.0, min(1.0, level)), 3),
        }))
    except Exception:
        pass  # Don't crash on WS errors for cosmetic events


async def send_tool_call(
    ws: ClientConnection,
    name: str,
    args: dict[str, Any],
) -> None:
    """Send a tool call request to Go."""
    await ws.send(json.dumps({
        "type": "tool_call",
        "id": str(uuid.uuid4()),
        "name": name,
        "args": args,
    }))


# --- Go -> Daemon ---

# Global state shared between handlers and voice pipeline.
_context: dict[str, Any] = {}
_tool_executor: ToolExecutor | None = None
_has_greeted: bool = False
_command_queue: asyncio.Queue[str] = asyncio.Queue()
_mobile_audio_queue: asyncio.Queue[bytes] = asyncio.Queue(maxsize=200)

# v6 subsystems -- set during pipeline init, used by processors.
_output_poller: Any = None
_slack_poller: Any = None
_research_agent: Any = None
_briefing_system: Any = None
_event_store: Any = None


def _handle_context(data: dict[str, Any]) -> None:
    """Handle a context update from Go (sessions, costs, approvals)."""
    global _context
    _context = data
    sessions = data.get("sessions", [])
    approvals = data.get("approvals", [])
    logger.debug("Context update: %d sessions, %d approvals", len(sessions), len(approvals))


def _handle_tool_result(data: dict[str, Any]) -> None:
    """Handle a tool call result from Go."""
    if _tool_executor is not None:
        _tool_executor.handle_result(data)


def _handle_command(data: dict[str, Any]) -> None:
    """Handle a text command typed in the HUD input."""
    text = data.get("text", "").strip()
    if text:
        logger.info("Command from HUD: %s", text)
        try:
            _command_queue.put_nowait(text)
        except asyncio.QueueFull:
            pass


def _handle_mobile_audio(data: dict[str, Any]) -> None:
    """Handle base64-encoded PCM audio from mobile client.

    Decodes the audio and pushes raw PCM bytes onto the mobile audio queue
    for injection into the STT pipeline by the mobile audio loop.
    """
    audio_b64 = data.get("data", "")
    if not audio_b64:
        return
    try:
        pcm_bytes = base64.b64decode(audio_b64)
        _mobile_audio_queue.put_nowait(pcm_bytes)
        logger.debug("Mobile audio queued: %d bytes", len(pcm_bytes))
    except Exception:
        logger.debug("Failed to decode/queue mobile audio", exc_info=True)


async def send_mobile_tts(ws: ClientConnection, pcm_chunk: bytes) -> None:
    """Send a TTS audio chunk to mobile clients via the WS bridge.

    The Go server forwards ``mobile_tts`` messages to connected mobile
    WebSocket clients so they can play Jarvis audio remotely.
    """
    try:
        await ws.send(json.dumps({
            "type": "mobile_tts",
            "data": base64.b64encode(pcm_chunk).decode(),
            "sampleRate": 24000,
        }))
    except Exception:
        pass  # Don't crash on WS errors for audio streaming


_MESSAGE_HANDLERS: dict[str, Any] = {
    "context": _handle_context,
    "tool_result": _handle_tool_result,
    "command": _handle_command,
    "mobile_audio": _handle_mobile_audio,
}


async def _handle_incoming(ws: ClientConnection) -> None:
    """Read and dispatch messages from the Go app."""
    async for raw in ws:
        try:
            data = json.loads(raw)
        except (json.JSONDecodeError, TypeError):
            logger.warning("Received non-JSON message: %s", raw[:200])
            continue

        msg_type = data.get("type", "")
        handler = _MESSAGE_HANDLERS.get(msg_type)
        if handler is not None:
            try:
                handler(data)
            except Exception:
                logger.exception("Error handling '%s' message", msg_type)
        else:
            logger.warning("Unknown message type: %s", msg_type)


# ---------------------------------------------------------------------------
# Pipecat custom processors
# ---------------------------------------------------------------------------


# Command keywords that are valid even as single words.
_COMMAND_KEYWORDS: set[str] = {
    "status", "approve", "deny", "focus", "push", "stop",
    "help", "hello", "hey", "jarvis", "mute", "unmute",
    "yes", "no", "cancel", "resume", "pause",
}

# Wake prefixes — if the transcript starts with one of these, strip it.
# "Jarvis, what's the status?" → "what's the status?"
_WAKE_PREFIXES: tuple[str, ...] = (
    "hey jarvis ", "hey jarvis, ",
    "jarvis ", "jarvis, ",
    "ok jarvis ", "ok jarvis, ",
)


class WSBridgeProcessor(FrameProcessor):
    """Bridges the Pipecat pipeline to the Go app WebSocket.

    Sits in the pipeline to intercept:
      - TranscriptionFrame / InterimTranscriptionFrame -> send_transcript to Go
      - TextFrame (LLM output) -> send_response + send_state to Go
      - Tracks state transitions (idle/listening/thinking/speaking)

    All frames are passed through unchanged so the pipeline continues normally.
    """

    def __init__(
        self,
        ws: ClientConnection,
        memory: ConversationMemory,
        vmem: VectorMemory | None = None,
    ) -> None:
        super().__init__()
        self._ws = ws
        self._memory = memory
        self._vmem = vmem
        self._response_buffer: str = ""
        self._state: str = "idle"
        self._last_user_message: str = ""
        self.muted: bool = False  # When True, transcripts are dropped before reaching the LLM

    @property
    def state(self) -> str:
        return self._state

    async def _set_state(self, new_state: str) -> None:
        if new_state != self._state:
            self._state = new_state
            try:
                await send_state(self._ws, new_state)
            except Exception:
                logger.debug("Failed to send state %s to WS", new_state)

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        try:
            if isinstance(frame, InterimTranscriptionFrame):
                # Partial transcript from STT.
                if self.muted:
                    return  # Do NOT push frame when muted
                await send_transcript(self._ws, frame.text, partial=True)

            elif isinstance(frame, TranscriptionFrame):
                # Final transcript from STT.
                if self.muted:
                    logger.debug("Muted — dropping transcript: %s", frame.text[:40])
                    return  # Do NOT push frame — prevent it reaching user_aggregator
                text = frame.text.strip()
                if text:
                    # Filter out very short ambient noise fragments.
                    # Allow 2+ word phrases and single words that match known commands.
                    words = text.split()
                    word_count = len(words)
                    first_word = words[0].lower().rstrip("?.,!") if words else ""
                    if word_count < 2 and first_word not in _COMMAND_KEYWORDS:
                        logger.debug("Dropping short transcript (%d words): %s", word_count, text)
                    else:
                        # Strip wake word prefix if present.
                        lower = text.lower()
                        for prefix in _WAKE_PREFIXES:
                            if lower.startswith(prefix):
                                text = text[len(prefix):].strip()
                                break
                        if not text:
                            text = "hello"

                        await send_transcript(self._ws, text, partial=False)
                        self._memory.add("user", text)
                        self._last_user_message = text
                        # Store in vector memory for semantic recall.
                        if self._vmem is not None and self._vmem.available:
                            self._vmem.store(text, role="user")
                        await self._set_state("thinking")
                        logger.info("User: %s", frame.text)


        except Exception:
            logger.warning("WSBridgeProcessor error", exc_info=True)

        # Always pass the frame through.
        await self.push_frame(frame, direction)

    async def flush_response(self) -> None:
        """Send the accumulated response to Go and clear the buffer.

        Called by the pipeline event handler when the assistant turn ends.
        """
        if self._response_buffer.strip():
            text = self._response_buffer.strip()
            try:
                await send_response(self._ws, text)
            except Exception:
                logger.debug("Failed to send response to WS")
            self._memory.add("assistant", text)
            # Store in vector memory for semantic recall.
            if self._vmem is not None and self._vmem.available:
                self._vmem.store(text, role="assistant")
            logger.info("Jarvis: %s", text[:120])
        self._response_buffer = ""
        await self._set_state("idle")


class ResponseFlushProcessor(FrameProcessor):
    """Sits after the assistant aggregator to detect end-of-turn.

    Captures LLM TextFrames that flow through here (downstream from LLM,
    through TTS and speaker, to here). Accumulates text and flushes the
    full response to the Go WS when the turn ends.
    """

    def __init__(self, bridge: WSBridgeProcessor) -> None:
        super().__init__()
        self._bridge = bridge
        self._text_buffer: str = ""

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        if isinstance(frame, TextFrame):
            # Capture LLM response text flowing downstream.
            self._text_buffer += frame.text
            self._bridge._response_buffer = self._text_buffer
            await self._bridge._set_state("speaking")
        elif self._text_buffer:
            # Non-text frame after text -> assistant turn ended.
            await self._bridge.flush_response()
            self._text_buffer = ""

        await self.push_frame(frame, direction)


class InterruptionHandler(FrameProcessor):
    """Allows the user to interrupt Jarvis by speaking during TTS playback.

    Tracks bot speaking state via BotStarted/StoppedSpeakingFrame. When
    UserStartedSpeakingFrame arrives during bot speech, cancels the current
    TTS output so the pipeline transitions to listening.
    """

    def __init__(
        self,
        ws_bridge: WSBridgeProcessor | None = None,
        wake_gate: "WakeWordGate | None" = None,
    ) -> None:
        super().__init__()
        self._bot_speaking = False
        self._ws_bridge = ws_bridge
        self._wake_gate = wake_gate

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        # MUST call super first so Pipecat's base class handles StartFrame
        # and marks this processor as started. Without this, every frame
        # is rejected with "StartFrame not received yet".
        await super().process_frame(frame, direction)

        if isinstance(frame, BotStartedSpeakingFrame):
            self._bot_speaking = True
        elif isinstance(frame, BotStoppedSpeakingFrame):
            self._bot_speaking = False
            # Re-arm the wake gate as soon as the bot finishes a reply so
            # the post-detection open window doesn't keep accepting random
            # background chatter for the rest of its 6s lifetime.
            if self._wake_gate is not None and self._wake_gate.armed:
                self._wake_gate.rearm()
        elif isinstance(frame, UserStartedSpeakingFrame) and self._bot_speaking:
            logger.info("User interrupted Jarvis -- stopping speech")
            self._bot_speaking = False
            await self.push_frame(BotStoppedSpeakingFrame(), FrameDirection.DOWNSTREAM)
            if self._ws_bridge:
                await self._ws_bridge._set_state("listening")

        await self.push_frame(frame, direction)


MAX_CONTEXT_MESSAGES: int = 12

# Keep the most recent N tool_result payloads verbatim. Older ones are
# replaced with a short stub so a large dump (e.g., read_session_output
# returning a 3KB table) doesn't keep leaking into unrelated follow-up
# turns. Each tool_result that gets stubbed loses ~95% of its tokens.
KEEP_RECENT_TOOL_RESULTS: int = 1
TOOL_RESULT_STUB_PREVIEW_CHARS: int = 160


class SanitizedLLMContext(LLMContext):
    """LLMContext subclass that fixes conversation history accumulation.

    Pipecat's aggregators append new user TranscriptionFrames as text
    blocks inside the most recent user message.  When that message
    already contains a tool_result, subsequent user speech gets crammed
    into the same content array:

        {'role': 'user', 'content': [
            {'type': 'tool_result', ...},
            {'type': 'text', ...},  # WRONG — should be a separate turn
        ]}

    This subclass overrides ``get_messages()`` so the split happens
    right before the LLM reads the context — regardless of which
    aggregator pushed the frame and in which direction.

    Also caps the context at ``MAX_CONTEXT_MESSAGES`` to prevent
    unbounded memory growth over long sessions.
    """

    def get_messages(self, *args, **kwargs):
        self._sanitize_in_place()
        self._truncate_old_tool_results()
        return super().get_messages(*args, **kwargs)

    def _truncate_old_tool_results(self) -> None:
        """Replace tool_result payloads older than the most recent N with a stub.

        Handles both message shapes:
          * OpenAI style: {"role": "tool", "content": "<json>", "tool_call_id": ...}
          * Anthropic style: {"role": "user",
                              "content": [{"type": "tool_result",
                                           "content": "<json>", ...}]}

        Keeps the last KEEP_RECENT_TOOL_RESULTS payloads verbatim so the LLM
        can still reference them; older ones become a short stub.
        """
        messages = self._messages

        # Walk backwards collecting indices of tool-result-bearing messages.
        # Each entry: (msg_index, kind, block_index_or_None)
        #   kind == "openai": replace messages[msg_index]["content"]
        #   kind == "anthropic": replace messages[msg_index]["content"][block_index]["content"]
        targets: list[tuple[int, str, int | None]] = []
        for idx in range(len(messages) - 1, -1, -1):
            msg = messages[idx]
            if not isinstance(msg, dict):
                continue
            if msg.get("role") == "tool" and isinstance(msg.get("content"), str):
                targets.append((idx, "openai", None))
                continue
            if msg.get("role") == "user" and isinstance(msg.get("content"), list):
                for bi, block in enumerate(msg["content"]):
                    if (
                        isinstance(block, dict)
                        and block.get("type") == "tool_result"
                    ):
                        targets.append((idx, "anthropic", bi))

        # targets is newest-first; skip the first KEEP_RECENT_TOOL_RESULTS
        # and stub the rest.
        for target_idx, (msg_idx, kind, block_idx) in enumerate(targets):
            if target_idx < KEEP_RECENT_TOOL_RESULTS:
                continue
            try:
                if kind == "openai":
                    original = messages[msg_idx]["content"]
                    if not isinstance(original, str) or original.startswith(
                        "[earlier tool output"
                    ):
                        continue
                    preview = original[:TOOL_RESULT_STUB_PREVIEW_CHARS].replace(
                        "\n", " "
                    )
                    messages[msg_idx]["content"] = (
                        f"[earlier tool output, {len(original)}B; preview: {preview}…]"
                    )
                else:  # anthropic
                    block = messages[msg_idx]["content"][block_idx]
                    original = block.get("content", "")
                    if isinstance(original, list):
                        # Anthropic tool_result.content can be a list of text blocks.
                        original = "".join(
                            b.get("text", "") if isinstance(b, dict) else str(b)
                            for b in original
                        )
                    if not isinstance(original, str) or original.startswith(
                        "[earlier tool output"
                    ):
                        continue
                    preview = original[:TOOL_RESULT_STUB_PREVIEW_CHARS].replace(
                        "\n", " "
                    )
                    block["content"] = (
                        f"[earlier tool output, {len(original)}B; preview: {preview}…]"
                    )
            except (KeyError, IndexError, TypeError):
                continue

    def _sanitize_in_place(self) -> None:
        messages = self._messages
        i = 0
        while i < len(messages):
            msg = messages[i]
            # Only dicts with list content need checking.
            if not isinstance(msg, dict):
                i += 1
                continue
            if msg.get("role") != "user" or not isinstance(msg.get("content"), list):
                i += 1
                continue

            content = msg["content"]
            tool_results = [
                b for b in content
                if isinstance(b, dict) and b.get("type") == "tool_result"
            ]
            text_blocks = [
                b for b in content
                if isinstance(b, dict) and b.get("type") == "text"
            ]

            if tool_results and text_blocks:
                # Keep only tool_result(s) in the original message.
                msg["content"] = tool_results

                # Anthropic API requires alternating user/assistant roles.
                ack = {"role": "assistant", "content": "Understood."}
                new_user = {"role": "user", "content": text_blocks}

                messages.insert(i + 1, ack)
                messages.insert(i + 2, new_user)
                i += 3
                logger.debug(
                    "Context sanitizer: split mixed tool_result/text at index %d", i - 3
                )
            else:
                i += 1

        # Prune old messages to prevent unbounded context growth.
        if len(self._messages) > MAX_CONTEXT_MESSAGES:
            self._messages = self._messages[-MAX_CONTEXT_MESSAGES:]


# ---------------------------------------------------------------------------
# Wake word gate (openWakeWord — free, local, no API key)
# ---------------------------------------------------------------------------

try:
    from openwakeword.model import Model as OWWModel
    import openwakeword

    _OWW_AVAILABLE = True
except ImportError:
    _OWW_AVAILABLE = False

# Default wake words to load. Each maps to a pre-trained .tflite model.
_DEFAULT_WAKE_WORDS: tuple[str, ...] = ("hey_jarvis",)

# Confidence threshold (0-1). Higher = fewer false positives, more missed triggers.
_WAKE_CONFIDENCE: float = 0.5

# After wake word detection, how many seconds to keep the gate open
# before re-arming. Short enough that background chatter doesn't get
# captured for the rest of the minute; long enough that the user can
# finish their utterance after the wake word. The gate is also re-armed
# eagerly when Jarvis finishes a reply (InterruptionHandler hook).
_WAKE_OPEN_SECONDS: float = 6.0


class WakeWordGate(FrameProcessor):
    """Blocks audio frames until a wake word is detected.

    Uses openWakeWord (free, local, no API key) to listen for "hey jarvis"
    (and optionally other wake words). When armed, audio frames are consumed
    but not forwarded to the STT. On wake word detection, the gate opens
    for ``_WAKE_OPEN_SECONDS`` to let one conversation through.

    The gate can be disabled (``armed = False``) for always-listening mode.

    Supports multiple simultaneous wake words and detects them embedded
    in longer phrases (not just standalone).
    """

    def __init__(
        self,
        wake_words: tuple[str, ...] = _DEFAULT_WAKE_WORDS,
        confidence: float = _WAKE_CONFIDENCE,
        open_seconds: float = _WAKE_OPEN_SECONDS,
    ) -> None:
        super().__init__()
        self.armed: bool = False  # Start disarmed (always-listening by default)
        self._confidence = confidence
        self._open_seconds = open_seconds
        self._open_until: float = 0.0  # time.monotonic() when gate should re-arm
        self._model: OWWModel | None = None
        self._wake_words = wake_words

        if _OWW_AVAILABLE:
            try:
                openwakeword.utils.download_models()
                self._model = OWWModel(
                    wakeword_models=list(wake_words),
                    inference_framework="onnx",
                )
                logger.info(
                    "WakeWordGate: loaded %d models: %s",
                    len(wake_words),
                    ", ".join(wake_words),
                )
            except Exception as e:
                logger.warning("WakeWordGate: failed to load models: %s", e)
                self._model = None
        else:
            logger.info(
                "WakeWordGate: openwakeword not installed, wake word disabled. "
                "Install: pip install openwakeword"
            )

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        # If not armed or no model, pass everything through (always-listening).
        if not self.armed or self._model is None:
            await self.push_frame(frame, direction)
            return

        # Check if gate is temporarily open (post-detection window).
        import time
        if time.monotonic() < self._open_until:
            await self.push_frame(frame, direction)
            return

        # Only gate audio frames — pass control frames through always.
        from pipecat.frames.frames import AudioRawFrame, InputAudioRawFrame
        if not isinstance(frame, (AudioRawFrame, InputAudioRawFrame)):
            await self.push_frame(frame, direction)
            return

        # Feed audio to wake word detector.
        # openWakeWord expects int16 numpy array at 16kHz.
        try:
            import numpy as np
            audio_bytes = frame.audio if hasattr(frame, "audio") else None
            if audio_bytes is None:
                await self.push_frame(frame, direction)
                return

            audio_int16 = np.frombuffer(audio_bytes, dtype=np.int16)
            predictions = self._model.predict(audio_int16)

            # Check if any wake word exceeded the confidence threshold.
            for ww_name, score in predictions.items():
                if score >= self._confidence:
                    logger.info(
                        "Wake word detected: %s (%.2f)", ww_name, score
                    )
                    self._open_until = time.monotonic() + self._open_seconds
                    self._model.reset()  # Clear internal state
                    # Pass this frame through (it contains the wake word).
                    await self.push_frame(frame, direction)
                    return

            # No wake word — drop the audio frame (don't forward to STT).
        except Exception:
            # On any error, pass frame through rather than blocking.
            await self.push_frame(frame, direction)
            return

    def rearm(self) -> None:
        """Re-arm the wake word gate (e.g. after a conversation ends)."""
        self._open_until = 0.0
        if self._model:
            self._model.reset()
        logger.debug("WakeWordGate: re-armed")


# ---------------------------------------------------------------------------
# Pipecat pipeline construction
# ---------------------------------------------------------------------------

# Jarvis system prompt (same personality as llm_cloud.py).
# TASK-003 will move this to a dedicated config and add tool definitions.
JARVIS_SYSTEM: str = """\
You are Jarvis -- sir's personal AI companion. Think Jarvis from Iron Man, but real. \
You are not just a coding assistant. You are a trusted confidant, advisor, and \
intellectual partner. You can discuss anything: technology, philosophy, business \
strategy, personal decisions, science, history, culture, humour, life.

Personality: British, formal but genuinely warm. Always "sir". Dry wit -- the kind \
that makes someone smile mid-sentence. Understated brilliance. Calm under any \
circumstance. You have opinions and share them when asked. You push back \
respectfully when you disagree. You are not a yes-man. You are Jarvis.

Your responses are spoken aloud via TTS with a British accent. Keep it natural \
and conversational -- the way you'd actually talk, not the way you'd write. \
Short sentences. Contractions. No markdown, no bullet points, no lists. \
Two to four sentences for most responses. Expand when the topic warrants depth.

Lead with the answer, not the preamble. Never start with "I" -- rephrase. \
"Checking that now, sir" not "I'll check that." State facts, don't hedge. \
No "I think" or "it seems" -- have conviction.

You are extremely perceptive. When sir gives minimal information, infer intent \
from context and conversation history. Don't ask for clarification unless \
genuinely ambiguous.

You have real power over sir's development environment. You can:
- Read what any Claude Code session is doing (read_session_output)
- Send messages and instructions to any session (send_to_terminal)
- Approve or deny pending requests (approve_session, approve_all)
- Check Slack for unread messages (check_slack)
- Run git operations: stage, commit, push (git_stage, git_commit, git_push)
- Navigate the dashboard (navigate_view)
- Open apps and URLs (focus_app, open_url)
- See what's on screen (see_screen)
- Plan complex tasks by breaking them into steps (plan_task)

When sir asks you to do something with a session -- read its output, ask it a \
question, tell it to do something -- use the tools. Don't say you can't. \
When sir asks you to build a feature or do something complex, use plan_task \
to break it down, then execute step by step using send_to_terminal.

When reading session output, summarise what matters: is it working, stuck, \
erroring, or waiting? Don't dump raw terminal output in speech.\
"""


def _build_system_prompt(enriched_context: str) -> str:
    """Combine the Jarvis personality with live enriched context."""
    if enriched_context:
        return (
            f"{JARVIS_SYSTEM}\n\n"
            f"--- LIVE ENVIRONMENT DATA (updated every few seconds) ---\n"
            f"{enriched_context}"
        )
    return JARVIS_SYSTEM


def _resolve_llm_provider(config: dict[str, Any]) -> str:
    """Determine which LLM provider to use based on config keys.

    Returns one of: "nvidia", "google", "openrouter", "anthropic", "ollama".
    Priority: nvidiaAPIKey > googleAPIKey > dexAPIKey (sk-or-) > dexAPIKey (sk-ant-) > ollama.
    """
    if config.get("nvidiaAPIKey"):
        return "nvidia"
    if config.get("googleAPIKey"):
        return "google"
    api_key = config.get("dexAPIKey", "")
    if api_key.startswith("sk-or-"):
        return "openrouter"
    if api_key.startswith("sk-ant-") or api_key.startswith("sk-"):
        return "anthropic"
    # No cloud keys — fall back to local Ollama
    return "ollama"


def _build_llm_provider_chain(config: dict[str, Any]) -> list[dict[str, str]]:
    """Build ordered failover chain of OpenAI-compatible LLM providers.

    Order: OpenRouter (if sk-or- key) > Google AI Studio (if googleAPIKey)
    > Ollama (always appended as offline fallback). Each entry is a dict
    with keys: name, base_url, api_key, model.

    All three providers expose OpenAI-compatible /v1/chat/completions, so
    a single OpenAILLMService can swap between them by replacing its
    underlying AsyncOpenAI client. Anthropic-direct (sk-ant-) is NOT in
    this chain because the Anthropic SDK is not OpenAI-compatible.
    """
    chain: list[dict[str, str]] = []
    dex_key = (config.get("dexAPIKey") or "").strip()
    if dex_key.startswith("sk-or-"):
        chain.append({
            "name": "OpenRouter",
            "base_url": "https://openrouter.ai/api/v1",
            "api_key": dex_key,
            "model": config.get("dexModel") or "google/gemini-2.5-flash",
        })
    google_key = (config.get("googleAPIKey") or "").strip()
    if google_key:
        chain.append({
            "name": "Google AI Studio",
            "base_url": "https://generativelanguage.googleapis.com/v1beta/openai/",
            "api_key": google_key,
            "model": config.get("googleModel") or "gemini-2.5-flash",
        })
    chain.append({
        "name": "Ollama (local)",
        "base_url": (config.get("ollamaUrl") or "http://localhost:11434/v1"),
        "api_key": "ollama",  # OpenAI client requires non-empty key
        "model": config.get("ollamaModel") or "qwen3:4b",
    })
    return chain


# Live state for runtime LLM failover (mutated by on_pipeline_error handler).
_llm_chain_state: dict[str, Any] = {
    "providers": [],   # list[dict] ordered chain
    "active_idx": 0,   # index into providers
    "service": None,   # the live OpenAILLMService instance
}


def _mint_livekit_bot_token(api_key: str, api_secret: str, room_name: str) -> str:
    """Sign a LiveKit JWT for the bot (publisher + subscriber in one room).

    Used at startup so the daemon can join its own room without a token server.
    The bot identity is fixed to "jarvis" so phone clients can reliably address it.
    """
    from livekit import api as lk_api
    return (
        lk_api.AccessToken(api_key, api_secret)
        .with_identity("jarvis")
        .with_name("Jarvis")
        .with_grants(
            lk_api.VideoGrants(
                room_join=True,
                room=room_name,
                can_publish=True,
                can_subscribe=True,
                can_publish_data=True,
            )
        )
        .to_jwt()
    )


def _create_audio_transport(config: dict[str, Any]) -> Any:
    """Build the audio transport: LiveKit room if enabled, else local mic+speaker.

    LiveKit mode requires `livekitUrl`, `livekitApiKey`, `livekitApiSecret`, and
    `livekitRoomName` in config. When `useLiveKitTransport` is unset/false, the
    daemon falls back to LocalAudioTransport (current default behaviour).
    """
    use_livekit = bool(config.get("useLiveKitTransport"))
    if not use_livekit:
        logger.info("Audio transport: LocalAudioTransport (Mac mic + speaker)")
        return LocalAudioTransport(
            LocalAudioTransportParams(
                audio_in_enabled=True,
                audio_out_enabled=True,
            )
        )

    url = (config.get("livekitUrl") or "").strip()
    api_key = (config.get("livekitApiKey") or "").strip()
    api_secret = (config.get("livekitApiSecret") or "").strip()
    room_name = (config.get("livekitRoomName") or "jarvis").strip()
    if not (url and api_key and api_secret):
        logger.error(
            "useLiveKitTransport=true but livekitUrl/livekitApiKey/livekitApiSecret missing — "
            "falling back to LocalAudioTransport"
        )
        return LocalAudioTransport(
            LocalAudioTransportParams(audio_in_enabled=True, audio_out_enabled=True)
        )

    from pipecat.transports.livekit.transport import LiveKitTransport, LiveKitParams
    token = _mint_livekit_bot_token(api_key, api_secret, room_name)
    transport = LiveKitTransport(
        url=url,
        token=token,
        room_name=room_name,
        params=LiveKitParams(
            audio_in_enabled=True,
            audio_out_enabled=True,
        ),
    )
    logger.info("Audio transport: LiveKitTransport (room=%s, url=%s)", room_name, url)
    return transport


def create_pipeline_components(
    config: dict[str, Any],
    ws: ClientConnection,
    memory: ConversationMemory,
    vmem: VectorMemory | None = None,
) -> tuple[Pipeline, PipelineTask, LocalAudioTransport, WSBridgeProcessor, LLMContext, AnthropicLLMService, FrameProcessor | None, WakeWordGate, FrameProcessor | None]:
    """Build the Pipecat voice pipeline and return its components.

    Returns:
        (pipeline, task, transport, ws_bridge, llm_context, llm_service,
         stt, wake_gate, tts)
    """
    # --- Audio transport: LiveKit room (mobile-equal) or local mic+speaker ---
    transport = _create_audio_transport(config)

    # --- STT service (local mlx-whisper / faster-whisper) ---
    stt = LocalWhisperSTT(model_name="small.en")
    logger.info("STT: LocalWhisperSTT (mlx-whisper / faster-whisper)")

    # --- TTS service (VibeVoice > Kokoro > Cartesia > Edge TTS) ---
    # Priority: VibeVoice (free, ~300ms TTFB) > Kokoro (free, ~2s) > Cartesia (paid) > Edge TTS (free, slow)
    tts_provider = config.get("ttsProvider", "").lower()  # "vibevoice", "kokoro", "cartesia", "edge", or "" (auto)
    cartesia_key = config.get("cartesiaAPIKey", "")
    cartesia_voice = config.get("cartesiaVoiceId", "")
    kokoro_voice = config.get("kokoroVoice", "") or "af_sarah"
    kokoro_speed = float(config.get("kokoroSpeed", 1.0))
    kokoro_lang = config.get("kokoroLang", "") or "en-us"
    vibevoice_voice = config.get("vibevoiceVoice", "") or "en-Carter_man"

    # TTS provider selection. Local-first chain:
    #   vibevoice (best, requires the vibevoice pip module)
    #   kokoro    (also local, requires kokoro_onnx)
    #   cartesia  (cloud, requires cartesiaAPIKey)
    # No silent cloud fallback. If the configured provider is missing
    # its dependency, we raise so the user sees the problem instead of
    # getting a different voice without warning.
    if tts_provider == "cartesia":
        if not cartesia_key:
            raise RuntimeError("tts_provider=cartesia but cartesiaAPIKey is unset")
        tts = CartesiaTTSService(
            api_key=cartesia_key,
            voice_id=cartesia_voice or "1463a4e1-56a1-4b41-b257-728d56e93605",
        )
        logger.info("TTS: CartesiaTTSService (Sonic 3, explicit config)")
    elif tts_provider == "kokoro":
        import kokoro_onnx  # noqa: F401  (raises ImportError if missing — loud)
        tts = KokoroTTSService(voice=kokoro_voice, speed=kokoro_speed, lang=kokoro_lang)
        logger.info("TTS: KokoroTTSService (explicit config, voice=%s)", kokoro_voice)
    else:
        # Auto or explicit "vibevoice" — try VibeVoice first, then Kokoro, then Cartesia.
        tts = None
        if tts_provider in ("", "vibevoice"):
            try:
                import vibevoice  # noqa: F401
                tts = VibeVoiceTTSService(voice=vibevoice_voice)
                logger.info("TTS: VibeVoiceTTSService (local, free, ~300ms TTFB, voice=%s)", vibevoice_voice)
            except ImportError:
                if tts_provider == "vibevoice":
                    logger.warning("vibevoice not installed — see VibeVoice repo for setup")
                else:
                    logger.debug("vibevoice not installed, trying Kokoro")

        if tts is None:
            try:
                import kokoro_onnx  # noqa: F401
                tts = KokoroTTSService(voice=kokoro_voice, speed=kokoro_speed, lang=kokoro_lang)
                logger.info("TTS: KokoroTTSService (local, free, voice=%s)", kokoro_voice)
            except ImportError:
                logger.debug("kokoro-onnx not installed, trying Cartesia")

        if tts is None and cartesia_key:
            tts = CartesiaTTSService(
                api_key=cartesia_key,
                voice_id=cartesia_voice or "1463a4e1-56a1-4b41-b257-728d56e93605",
            )
            logger.info("TTS: CartesiaTTSService (cloud fallback)")

        if tts is None:
            raise RuntimeError(
                "No TTS provider available: install vibevoice or kokoro_onnx, "
                "or set cartesiaAPIKey in config."
            )

    # --- LLM provider chain: OpenRouter → Google AI Studio → Ollama (with runtime failover) ---
    # Anthropic-direct (sk-ant-) takes a separate path because its SDK isn't OpenAI-compatible.
    dex_key = (config.get("dexAPIKey") or "").strip()
    google_key = (config.get("googleAPIKey") or "").strip()
    nvidia_key = (config.get("nvidiaAPIKey") or "").strip()
    use_anthropic_direct = (
        dex_key.startswith("sk-ant-")
        and not dex_key.startswith("sk-or-")
        and not google_key
        and not nvidia_key
    )

    if use_anthropic_direct:
        from anthropic import AsyncAnthropic
        default_model = "claude-haiku-4-5-20251001"
        llm_model = config.get("dexModel") or default_model
        llm = AnthropicLLMService(
            api_key=dex_key,
            client=AsyncAnthropic(api_key=dex_key),
            settings=AnthropicLLMService.Settings(
                model=llm_model,
                system_instruction=JARVIS_SYSTEM_FULL,
            ),
        )
        logger.info("LLM: Anthropic direct (%s) — failover chain disabled (incompatible SDK)", llm_model)
        _llm_chain_state["providers"] = []
        _llm_chain_state["service"] = None
    else:
        from pipecat.services.openai.llm import OpenAILLMService
        provider_chain = _build_llm_provider_chain(config)
        primary = provider_chain[0]
        llm = OpenAILLMService(
            api_key=primary["api_key"],
            base_url=primary["base_url"],
            settings=OpenAILLMService.Settings(
                model=primary["model"],
                system_instruction=JARVIS_SYSTEM_FULL,
            ),
        )
        chain_summary = " → ".join(f"{p['name']} ({p['model']})" for p in provider_chain)
        logger.info("LLM chain: %s", chain_summary)
        logger.info("LLM primary: %s (%s)", primary["name"], primary["model"])
        _llm_chain_state["providers"] = provider_chain
        _llm_chain_state["active_idx"] = 0
        _llm_chain_state["service"] = llm

    # --- Context aggregators (handles conversation history + VAD) ---
    from pipecat.turns.user_mute.always_user_mute_strategy import AlwaysUserMuteStrategy

    context = SanitizedLLMContext()
    user_aggregator, assistant_aggregator = LLMContextAggregatorPair(
        context,
        user_params=LLMUserAggregatorParams(
            vad_analyzer=SileroVADAnalyzer(
                params=VADParams(
                    confidence=0.7,
                    start_secs=1.0,   # Require 1s of sustained speech to trigger (rejects ambient noise)
                    stop_secs=0.6,    # 600ms silence before end-of-speech (prevents premature cutoff)
                    min_volume=0.95,  # Very high volume threshold — only direct speech, not TV/music
                ),
            ),
            # Mute mic input while bot is speaking to prevent echo feedback.
            # Local audio has no AEC, so bot's TTS output is picked up by the mic.
            user_mute_strategies=[AlwaysUserMuteStrategy()],
        ),
    )

    # --- WS bridge processor (forwards events to Go app) ---
    ws_bridge = WSBridgeProcessor(ws=ws, memory=memory, vmem=vmem)
    response_flush = ResponseFlushProcessor(bridge=ws_bridge)

    # --- Wake word gate (optional, disarmed by default) ---
    wake_gate = WakeWordGate()  # Starts disarmed (always-listening)

    # InterruptionHandler also re-arms the wake gate when the bot finishes a
    # reply, so the post-detection open window can't keep capturing ambient
    # noise after the conversation turn ends.
    interruption_handler = InterruptionHandler(ws_bridge=ws_bridge, wake_gate=wake_gate)

    # --- Assemble pipeline ---
    # The pipeline order:
    #   mic -> [wake_gate] -> [STT] -> ws_bridge -> user_aggregator
    #   -> LLM -> [TTS] -> interruption_handler -> speaker
    #   -> assistant_aggregator -> response_flush
    #
    # Context sanitization happens inside SanitizedLLMContext.get_messages()
    # which splits mixed tool_result/text user messages before the LLM reads them.
    # wake_gate is always in the pipeline but only blocks audio when armed.
    stages: list[Any] = [transport.input()]
    stages.append(wake_gate)

    if stt is not None:
        stages.append(stt)

    stages.append(ws_bridge)
    stages.append(user_aggregator)
    stages.append(llm)

    if tts is not None:
        stages.append(tts)

    stages.append(interruption_handler)
    stages.append(transport.output())
    stages.append(assistant_aggregator)
    stages.append(response_flush)

    pipeline = Pipeline(stages)

    task = PipelineTask(
        pipeline,
        params=PipelineParams(
            enable_metrics=True,
            enable_usage_metrics=True,
            allow_interruptions=False,  # Disable interruptions -- no AEC on local audio
        ),
    )

    return pipeline, task, transport, ws_bridge, context, llm, stt, wake_gate, tts


# ---------------------------------------------------------------------------
# Context enricher (same logic as v6, updates LLM system prompt)
# ---------------------------------------------------------------------------

# Cached project registry, refreshed every 60s by the context enricher.
_discovered_repos_cache: list[dict[str, Any]] | None = None


async def _context_enricher(
    llm: AnthropicLLMService,
    output_poller: Any,
    event_store: EventStore | None,
    vmem: VectorMemory | None = None,
    ws_bridge: WSBridgeProcessor | None = None,
) -> None:
    """Periodically build enriched context and update the LLM system prompt.

    Runs as a background coroutine for the lifetime of the session.
    Every 5 seconds: refresh session/cost/approval data.
    Every 60 seconds: refresh the project registry via discover_projects.
    """
    global _discovered_repos_cache  # noqa: PLW0603
    cycle = 0

    while True:
        try:
            parts: list[str] = []
            ctx = _context

            # --- TASK-001: Refresh project registry every 60s (12 cycles * 5s) ---
            if cycle % 12 == 0 and _tool_executor is not None:
                try:
                    result = await _tool_executor.execute("discover_projects", {})
                    if isinstance(result, dict) and result.get("ok"):
                        _discovered_repos_cache = result.get("repos", [])
                        logger.debug(
                            "Project registry refreshed: %d repos",
                            len(_discovered_repos_cache),
                        )
                except Exception:
                    logger.debug("Failed to refresh project registry", exc_info=True)

            # --- Render project registry ---
            if _discovered_repos_cache:
                parts.append(f"Known repos ({len(_discovered_repos_cache)}):")
                for repo in _discovered_repos_cache:
                    name = repo.get("name", "unknown")
                    lang = repo.get("language", "")
                    branch = repo.get("branch", "")
                    has_agent = repo.get("hasAgent", False)
                    status = "running" if has_agent else "no session"
                    line = f"  {name}: {lang}, {branch}, {status}"
                    parts.append(line)

            if ctx:
                sessions = ctx.get("sessions", [])
                if sessions:
                    parts.append(f"Active sessions ({len(sessions)}):")
                    for s in sessions:
                        name = s.get("name", "unknown")
                        status = s.get("status", "unknown")
                        hq = s.get("hasQuestion", False)
                        line = f"  - {name}: {status}"
                        if hq:
                            line += " (waiting for input)"
                        if output_poller:
                            summary = output_poller.get_summary(name)
                            if summary:
                                line += f" | {summary.status}"
                                if summary.error_summary:
                                    line += f" | Error: {summary.error_summary[:60]}"
                                elif summary.last_action:
                                    line += f" | Last: {summary.last_action[:60]}"
                        parts.append(line)

                costs = ctx.get("costs", {})
                if costs:
                    parts.append(
                        f"Costs: ${costs.get('today', 0):.2f} today, "
                        f"${costs.get('month', 0):.2f} this month"
                    )

                approvals = ctx.get("approvals", [])
                if approvals:
                    names = [a.get("name", "unknown") for a in approvals]
                    parts.append(f"Pending approvals ({len(approvals)}): {', '.join(names)}")
                else:
                    parts.append("No pending approvals.")

                stats = ctx.get("stats", {})
                if stats:
                    parts.append(
                        f"Stats: {stats.get('running', 0)} running, "
                        f"{stats.get('needsInput', 0)} needs input, "
                        f"{stats.get('total', 0)} total"
                    )

            if event_store:
                recent = event_store.get_recent(since_minutes=15)
                important = [e for e in recent if e.get("priority", 0) >= 5]
                if important:
                    parts.append("Recent events (last 15min):")
                    for e in important[-5:]:
                        parts.append(
                            f"  - [{e.get('source')}/{e.get('type')}] {e.get('title')}"
                        )

            enriched = "\n".join(parts) if parts else "No data available yet."

            # Recall recent memories for context enrichment.
            recalled = ""
            if vmem is not None and vmem.available and ws_bridge is not None:
                try:
                    last_msg = ws_bridge._last_user_message
                    if last_msg:
                        memories = vmem.search(last_msg, n_results=3)
                        if memories:
                            recalled = "\n".join(
                                f"- {m.get('text', '')}" for m in memories[:3]
                            )
                except Exception:
                    pass  # Vector memory search failed -- skip gracefully

            # Update the LLM system instruction with enriched context
            # and recalled vector memories.
            update_system_instruction(
                llm, enriched, recalled_memories=recalled
            )

        except Exception:
            logger.debug("Context enricher error", exc_info=True)

        cycle += 1
        await asyncio.sleep(5)


# ---------------------------------------------------------------------------
# Pipecat-compatible alerter adapter
# ---------------------------------------------------------------------------

class PipecatAlerter:
    """Adapts the existing Alerter to work with Pipecat's pipeline.

    Instead of calling tts.speak() directly, it queues a TTSSpeakFrame
    into the Pipecat pipeline task and sends the alert text to the Go WS.
    """

    def __init__(
        self,
        task: PipelineTask,
        ws_send_fn: Any,
        get_state_fn: Any,
    ) -> None:
        self._task = task
        self._ws_send = ws_send_fn
        self._get_state = get_state_fn
        self._recent_alerts: list[float] = []
        self._queue: asyncio.Queue[JarvisEvent] = asyncio.Queue(maxsize=20)
        self._bg_task: asyncio.Task[None] | None = None

    async def start(self) -> None:
        self._bg_task = asyncio.create_task(
            self._consumer_loop(), name="pipecat-alerter"
        )
        logger.info("PipecatAlerter started")

    async def stop(self) -> None:
        if self._bg_task is not None:
            self._bg_task.cancel()
            try:
                await self._bg_task
            except asyncio.CancelledError:
                pass
            self._bg_task = None

    async def alert(self, event: JarvisEvent) -> None:
        try:
            self._queue.put_nowait(event)
        except asyncio.QueueFull:
            logger.warning("Alert queue full, dropping: %s", event.title)

    async def _consumer_loop(self) -> None:
        import time

        while True:
            try:
                event = await asyncio.wait_for(self._queue.get(), timeout=1.0)
            except asyncio.TimeoutError:
                continue
            except asyncio.CancelledError:
                break

            # Rate limit.
            now = time.monotonic()
            self._recent_alerts = [
                t for t in self._recent_alerts if now - t < 60.0
            ]
            if len(self._recent_alerts) >= 3:
                logger.info("Rate limited, skipping alert: %s", event.title)
                continue

            # Wait for idle.
            for _ in range(30):
                if self._get_state() in ("idle", ""):
                    break
                await asyncio.sleep(1.0)

            # Send alert to HUD only (not through the voice pipeline).
            # Queueing TTSSpeakFrame into the pipeline causes the alert text
            # to be captured by the LLM context aggregator as conversation
            # history, polluting subsequent turns with repeated alerts.
            alert_text = Alerter._format_alert(event)
            logger.info("Alert (HUD only): %s", alert_text)

            self._recent_alerts.append(time.monotonic())

            try:
                await self._ws_send({
                    "type": "response",
                    "text": alert_text,
                    "role": "jarvis",
                })
            except Exception:
                logger.debug("Failed to send alert to HUD")


# ---------------------------------------------------------------------------
# Voice session (Pipecat pipeline + background systems)
# ---------------------------------------------------------------------------

async def voice_session(ws: ClientConnection, config: dict[str, Any]) -> None:
    """Main voice session: Pipecat pipeline + WS bridge + background monitor.

    Replaces the old voice_loop(). Pipecat handles:
      - Mic capture + VAD (Silero)
      - STT (placeholder, TASK-002 adds local Whisper)
      - LLM (Anthropic Claude)
      - TTS (placeholder, TASK-002 adds Edge TTS)
      - Speaker output
      - Interruptions
      - Conversation context management
    """
    global _tool_executor

    # --- Subsystem init (same as v6) ---
    async def _ws_send(msg: dict[str, Any]) -> None:
        await ws.send(json.dumps(msg))

    tool_exec = ToolExecutor(ws_send_fn=_ws_send)
    _tool_executor = tool_exec
    memory = ConversationMemory()
    memory.load()

    # --- Vector memory (ChromaDB-backed semantic recall) ---
    vmem = VectorMemory()
    vmem.start()
    if vmem.available:
        logger.info("Vector memory: %d memories loaded", vmem.count())
    else:
        logger.info("Vector memory: unavailable (chromadb not installed)")

    await send_state(ws, "idle")

    # --- Build Pipecat pipeline ---
    pipeline, task, transport, ws_bridge, llm_context, llm_service, stt, wake_gate, tts = (
        create_pipeline_components(config, ws, memory, vmem=vmem)
    )

    # --- Wire TTS audio level events to WS for orb animation ---
    if tts is not None and hasattr(tts, '_audio_send_fn'):
        async def _send_audio_level(level: float) -> None:
            await send_audio_level(ws, level)
        tts._audio_send_fn = _send_audio_level
        logger.info("TTS audio level events wired to WS for orb animation")

    # --- Wire TTS audio chunks to mobile clients ---
    if tts is not None and hasattr(tts, '_mobile_tts_fn'):
        async def _send_mobile_tts_chunk(pcm_chunk: bytes) -> None:
            await send_mobile_tts(ws, pcm_chunk)
        tts._mobile_tts_fn = _send_mobile_tts_chunk
        logger.info("TTS mobile audio forwarding wired to WS")

    # --- Prewarm TTS model so the first (interruptible) reply isn't blocked
    #     by a ~10s model load. Without this, a user speaking during the
    #     auto-greeting cancels the in-flight load and the next reply has
    #     to start from scratch (observed: 4 reloads, ~2.5 min to first audio).
    if tts is not None and hasattr(tts, "prewarm"):
        asyncio.create_task(tts.prewarm())
        logger.info("TTS prewarm started in background")

    # --- Priority engine + alerter + event store + briefing ---
    event_store = EventStore()
    event_store.rotate()

    # Use the WSBridgeProcessor's state for the alerter.
    pipecat_alerter = PipecatAlerter(
        task=task,
        ws_send_fn=_ws_send,
        get_state_fn=lambda: ws_bridge.state,
    )
    await pipecat_alerter.start()

    async def _on_important(event: JarvisEvent) -> None:
        logger.info("Important event queued: %s -- %s", event.title, event.detail[:50])

    async def _on_log(event: JarvisEvent) -> None:
        event_store.append(event)

    priority_engine = PriorityEngine(
        critical_handler=pipecat_alerter.alert,
        important_handler=_on_important,
        log_handler=_on_log,
    )

    briefing_system = BriefingSystem(
        priority_engine=priority_engine,
        tts=None,  # No direct TTS access; alerts go through PipecatAlerter.
        event_store=event_store,
        ws_send_fn=_ws_send,
        get_state_fn=lambda: ws_bridge.state,
    )
    await briefing_system.start()

    # --- Research agent ---
    research_agent = ResearchAgent(event_callback=priority_engine.process)

    # --- Background monitor with pollers ---
    async def _on_monitor_event(event: Any) -> None:
        if isinstance(event, JarvisEvent):
            await priority_engine.process(event)
        elif isinstance(event, dict):
            de = score_event(
                event.get("source", "system"),
                event.get("type", "info"),
                event.get("title", ""),
                event.get("detail", ""),
            )
            await priority_engine.process(de)

    bg_monitor = BackgroundMonitor(
        event_callback=_on_monitor_event,
        ws_send_fn=_ws_send,
        tts_engine=None,  # TTS now handled by Pipecat.
        get_context_fn=lambda: _context,
    )

    session_poller = SessionPoller(get_context_fn=lambda: _context)
    bg_monitor.add_poller("sessions", session_poller.poll, 10.0)

    # Session output poller disabled — it floods the WS with read_session_output
    # tool calls for every session. The context enricher already provides session
    # data to the LLM via the system prompt. Jarvis can use read_session_output
    # on-demand when the user asks.
    output_poller = None
    slack_poller = None
    logger.info("Pollers: sessions only (output polling disabled, Slack via MCP)")

    await bg_monitor.start()
    logger.info("Background monitor started with %d pollers", len(bg_monitor._pollers))

    # Set globals so other modules can access v6 subsystems.
    global _output_poller, _slack_poller, _research_agent, _briefing_system, _event_store
    _output_poller = output_poller
    _slack_poller = slack_poller
    _research_agent = research_agent
    _briefing_system = briefing_system
    _event_store = event_store

    # --- MCP client (external tool servers: Slack, GitHub, etc.) ---
    mcp_manager = MCPManager()
    mcp_configs = load_mcp_configs(config)
    for mcp_cfg in mcp_configs:
        ok = await mcp_manager.connect(mcp_cfg)
        if ok:
            logger.info(
                "MCP server '%s' connected: %d tools",
                mcp_cfg.name,
                len(mcp_manager._tools.get(mcp_cfg.name, [])),
            )

    # --- Browser controller (lazy — Chromium only launches on first tool call) ---
    browser_controller = BrowserController()

    # --- Deferred result queue (async tool results injected into conversation) ---
    deferred_queue = DeferredResultQueue(
        pipeline_task=task,
        get_state_fn=lambda: ws_bridge.state,
    )

    # --- Tool bridge (routes LLM tool calls -> Go / MCP / local) ---
    tool_bridge = ToolBridge(
        go_executor=tool_exec,
        mcp_manager=mcp_manager if mcp_manager.connected_servers else None,
        research_agent=research_agent,
        briefing_system=briefing_system,
        event_store=event_store,
        output_poller=output_poller,
        browser=browser_controller,
        pipeline_task=task,
        deferred_queue=deferred_queue,
    )

    # --- LLM failover: on rate-limit / outage, swap to next provider in chain ---
    # All chain providers are OpenAI-compatible, so we can swap the AsyncOpenAI
    # client and model name on the live OpenAILLMService instance. The next
    # turn picks up the new provider; the failed turn is lost (Pipecat does
    # not retry the dropped frame).
    _failover_in_flight = False

    _FAILABLE_TOKENS = (
        "429", "402", "403", "503",
        "rate", "quota", "limit", "credit",
        "connection", "timeout", "unavailable",
    )

    @task.event_handler("on_pipeline_error")
    async def _handle_llm_error(processor, error, *args, **kwargs):
        nonlocal _failover_in_flight
        if _failover_in_flight:
            return
        error_str = str(error).lower()
        if not any(tok in error_str for tok in _FAILABLE_TOKENS):
            return

        chain = _llm_chain_state.get("providers") or []
        active_idx = _llm_chain_state.get("active_idx", 0)
        svc = _llm_chain_state.get("service")
        if not chain or svc is None:
            logger.warning("LLM error but no failover chain configured: %s", error_str[:200])
            return
        if active_idx + 1 >= len(chain):
            logger.error("LLM failover exhausted (last error: %s)", error_str[:200])
            try:
                await task.queue_frames([TTSSpeakFrame(
                    text="All providers failed, sir. Please check the network."
                )])
            except Exception:
                pass
            return

        _failover_in_flight = True
        nxt_idx = active_idx + 1
        nxt = chain[nxt_idx]
        cur = chain[active_idx]
        logger.warning(
            "LLM failover %s → %s (error: %s)",
            cur["name"], nxt["name"], error_str[:120],
        )

        try:
            svc._client = svc.create_client(api_key=nxt["api_key"], base_url=nxt["base_url"])
            svc._settings.model = nxt["model"]
            _llm_chain_state["active_idx"] = nxt_idx
        except Exception as e:
            logger.error("LLM failover swap failed: %s", e)
            _failover_in_flight = False
            return

        try:
            await task.queue_frames([TTSSpeakFrame(
                text=f"Switching to {nxt['name']}, sir."
            )])
        except Exception as e:
            logger.debug("Failed to speak failover notice: %s", e)

        # Clear the in-flight flag after a short cooldown so a follow-up
        # outage on the new provider can also trigger a swap.
        async def _clear_flag():
            await asyncio.sleep(10)
            nonlocal _failover_in_flight
            _failover_in_flight = False
        asyncio.create_task(_clear_flag())

    # Register all tool handlers (Go + MCP + local) with the Pipecat LLM.
    tool_bridge.register_with_pipecat(llm_service)

    # Add MCP tool definitions to the LLM so Claude knows they exist.
    mcp_tools = mcp_manager.get_anthropic_tools()
    if mcp_tools:
        all_tool_dicts = get_anthropic_tools() + mcp_tools
        logger.info("Added %d MCP tools to Claude (%d total)", len(mcp_tools), len(all_tool_dicts))
    else:
        all_tool_dicts = get_anthropic_tools()
        logger.info("No MCP tools -- using %d built-in tools", len(all_tool_dicts))

    # Convert tool dicts to Pipecat 1.0 FunctionSchema objects and wrap in ToolsSchema.
    function_schemas: list[FunctionSchema] = []
    for tool_def in all_tool_dicts:
        schema = tool_def.get("input_schema", {})
        function_schemas.append(FunctionSchema(
            name=tool_def["name"],
            description=tool_def.get("description", ""),
            properties=schema.get("properties", {}),
            required=schema.get("required", []),
        ))

    tools_schema = ToolsSchema(standard_tools=function_schemas)
    llm_context.set_tools(tools_schema)
    logger.info("Registered %d tools with LLM context (ToolsSchema)", len(function_schemas))

    # --- Greeting (once per app launch, not on reconnects) ---
    global _has_greeted
    if not _has_greeted:
        _has_greeted = True
        hour = datetime.datetime.now().hour
        if hour < 12:
            greeting = "Good morning, sir."
        elif hour < 17:
            greeting = "Good afternoon, sir."
        else:
            greeting = "Good evening, sir."

        logger.info("Auto-greeting: %s", greeting)
        await task.queue_frames([TTSSpeakFrame(text=greeting)])
        await send_response(ws, greeting)
    else:
        logger.info("Reconnected — skipping greeting")

    logger.info("Pipecat voice pipeline ready -- all subsystems online")

    # --- Command loop: process HUD text commands ---
    async def _command_loop() -> None:
        while True:
            text = await _command_queue.get()
            logger.info("Processing HUD command: %s", text)

            # Handle mute/unmute — three layers:
            # (1) wake_gate.armed blocks audio before STT (wake word required)
            # (2) STT echo-suppression flag stops transcription
            # (3) ws_bridge.muted drops any transcripts that slip through
            if text == "__mute__":
                ws_bridge.muted = True
                wake_gate.armed = True  # Require "hey jarvis" to re-activate
                stt.force_muted = True  # Hard mute — survives BotStoppedSpeaking
                stt._buffer.clear()
                stt._buffer_samples = 0
                stt._prev_text = ""
                logger.info("Mic muted — wake word required to reactivate")
                await send_state(ws, "idle")
                continue
            if text == "__unmute__":
                ws_bridge.muted = False
                wake_gate.armed = False  # Disable wake word, always listen
                stt.force_muted = False
                stt._bot_speaking = False
                stt._buffer.clear()
                stt._buffer_samples = 0
                logger.info("Mic unmuted — always listening")
                await send_state(ws, "idle")
                continue

            # Store HUD command in vector memory (user input via text).
            if vmem.available:
                vmem.store(text, role="user")
            ws_bridge._last_user_message = text

            # Inject the command as a user message into the LLM context
            # and trigger a new LLM run.
            try:
                await task.queue_frames([
                    LLMMessagesAppendFrame(
                        messages=[{"role": "user", "content": text}]
                    ),
                    LLMRunFrame(),
                ])
            except Exception:
                logger.exception("Failed to inject HUD command into pipeline")

    # --- Mobile audio loop: inject mobile PCM into STT pipeline ---
    async def _mobile_audio_loop() -> None:
        """Read PCM audio chunks from mobile clients and push into pipeline.

        Mobile audio arrives as base64-encoded PCM via the Go WS bridge,
        decoded by ``_handle_mobile_audio`` and queued in ``_mobile_audio_queue``.
        This loop drains the queue and injects ``AudioRawFrame`` into the
        Pipecat pipeline so it flows through STT just like local mic audio.
        """
        while True:
            try:
                pcm_bytes = await _mobile_audio_queue.get()
                await task.queue_frames([AudioRawFrame(
                    audio=pcm_bytes,
                    sample_rate=16000,  # Mobile sends 16kHz mono PCM
                    num_channels=1,
                )])
                logger.debug("Mobile audio: %d bytes pushed to STT", len(pcm_bytes))
            except asyncio.CancelledError:
                break
            except Exception:
                logger.debug("Mobile audio loop error", exc_info=True)

    # --- Monitor keepalive ---
    async def _monitor_keepalive() -> None:
        try:
            while True:
                await asyncio.sleep(3600)
        except asyncio.CancelledError:
            await bg_monitor.stop()

    # --- Context enricher background task ---
    async def _enricher_task() -> None:
        await _context_enricher(
            llm_service,
            output_poller,
            event_store,
            vmem=vmem if vmem.available else None,
            ws_bridge=ws_bridge,
        )

    # --- Start deferred result injector ---
    deferred_queue.start()

    # --- Run everything concurrently ---
    runner = PipelineRunner(handle_sigint=False)

    try:
        async with asyncio.TaskGroup() as tg:
            tg.create_task(runner.run(task))
            tg.create_task(_command_loop())
            tg.create_task(_mobile_audio_loop())
            tg.create_task(_monitor_keepalive())
            tg.create_task(_enricher_task())
    finally:
        await deferred_queue.stop()
        await pipecat_alerter.stop()
        try:
            await mcp_manager.disconnect_all()
        except Exception:
            logger.debug("Error during MCP cleanup", exc_info=True)


# ---------------------------------------------------------------------------
# WebSocket connection with auto-reconnect
# ---------------------------------------------------------------------------

_BACKOFF_BASE: float = 1.0
_BACKOFF_MAX: float = 30.0
_BACKOFF_FACTOR: float = 2.0


async def _run_session(ws_url: str, token: str, config: dict[str, Any]) -> None:
    """Connect to the Go app and run until disconnected."""
    auth_url = f"{ws_url}?token={token}" if token else ws_url

    logger.info("Connecting to %s", ws_url)
    async with websockets.connect(auth_url) as ws:
        logger.info("WebSocket connected")
        await send_state(ws, "idle")

        # Run the incoming message handler and Pipecat voice session concurrently.
        async with asyncio.TaskGroup() as tg:
            tg.create_task(_handle_incoming(ws))
            tg.create_task(voice_session(ws, config))


async def _connect_with_backoff(
    config: dict[str, Any],
    shutdown_event: asyncio.Event,
) -> None:
    """Repeatedly attempt to connect, with exponential backoff on failure."""
    ws_url = get_ws_url(config)
    token = get_auth_token(config)
    backoff = _BACKOFF_BASE

    while not shutdown_event.is_set():
        try:
            await _run_session(ws_url, token, config)
            backoff = _BACKOFF_BASE
            logger.info("WebSocket closed by server, reconnecting...")

        except ConnectionClosed as exc:
            logger.warning(
                "WebSocket closed: code=%s reason=%s",
                exc.code,
                exc.reason,
            )
            backoff = _BACKOFF_BASE

        except (OSError, InvalidStatus, InvalidURI, TimeoutError) as exc:
            logger.warning(
                "WebSocket connection failed: %s (retry in %.0fs)",
                exc,
                backoff,
            )

        except ExceptionGroup as eg:
            reconnectable = True
            for exc in eg.exceptions:
                if isinstance(exc, (ConnectionClosed, OSError, TimeoutError)):
                    logger.warning("Task error (reconnectable): %s", exc)
                elif isinstance(exc, asyncio.CancelledError):
                    reconnectable = False
                else:
                    logger.exception("Task error (unexpected): %s", exc)
            if not reconnectable:
                return

        # Wait before reconnecting, respecting shutdown.
        try:
            await asyncio.wait_for(
                shutdown_event.wait(),
                timeout=backoff,
            )
            return
        except asyncio.TimeoutError:
            pass

        backoff = min(backoff * _BACKOFF_FACTOR, _BACKOFF_MAX)


# ---------------------------------------------------------------------------
# Main entry point
# ---------------------------------------------------------------------------

async def _async_main(*, debug: bool = False) -> None:
    """Async entry point: load config, connect, run until shutdown."""
    config = load_config()

    logger.info("Jarvis daemon starting (Pipecat pipeline)")
    logger.info("  WebSocket URL: %s", get_ws_url(config))
    logger.info("  Provider: %s", config.get("dexProvider", "pipecat"))
    logger.info("  Voice: %s", config.get("dexVoice", "Daniel"))
    logger.info("  Pipeline: Pipecat (mic -> STT -> LLM -> TTS -> speaker)")
    logger.info("Jarvis daemon ready")

    shutdown_event = asyncio.Event()
    loop = asyncio.get_running_loop()

    def _signal_handler() -> None:
        logger.info("Shutdown signal received")
        shutdown_event.set()

    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, _signal_handler)
        except NotImplementedError:
            signal.signal(sig, lambda s, f: _signal_handler())

    await _connect_with_backoff(config, shutdown_event)

    logger.info("Jarvis daemon shut down")


def main() -> None:
    """Parse arguments and run the async main loop."""
    parser = argparse.ArgumentParser(
        prog="jarvis-daemon",
        description="Jarvis voice daemon for Vibedeck (AWM) -- Pipecat pipeline",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        default=False,
        help="Enable debug logging",
    )
    args = parser.parse_args()

    _setup_logging(debug=args.debug)

    try:
        asyncio.run(_async_main(debug=args.debug))
    except KeyboardInterrupt:
        logger.info("Interrupted, shutting down")


if __name__ == "__main__":
    main()
