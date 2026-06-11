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
import os
import signal
import sys
import tempfile
import time
import uuid
from typing import Any, Final

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
        LLMFullResponseEndFrame,
        LLMFullResponseStartFrame,
        LLMMessagesAppendFrame,
        LLMRunFrame,
        TextFrame,
        TranscriptionFrame,
        InterimTranscriptionFrame,
        TTSAudioRawFrame,
        TTSSpeakFrame,
        UserStartedSpeakingFrame,
        UserStoppedSpeakingFrame,
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

from config import get_api_key, get_auth_token, get_llm_model, get_ws_url, load_config
from llm_picker import build_user_picked_llm
from pipeline_status import (
    build_pipeline_status,
    resolve_chain_provider_label,
    resolve_user_pick_llm,
)
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
from pipecat_stt import LocalWhisperSTT, MobileAudioRawFrame
from pipecat_tts_cartesia import CartesiaTTSService
from pipecat_tts_kokoro import KokoroTTSService
from pipecat_tts_vibevoice import VibeVoiceTTSService
from pipecat_tts_macos_say import MacOSSayTTSService
import active_client
import model_status

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
_command_queue: asyncio.Queue[Any] = asyncio.Queue()
"""Queue carrying HUD-originated text commands and their full payload.

Historically holds plain ``str`` (e.g. ``"__mute__"``). v0.3.0 / TASK-006
widens it to ``Any`` so meeting commands can carry an optional ``title``
field alongside the ``text``. ``_handle_command`` enqueues either a bare
string (legacy callers) or a dict ``{"text": ..., "title": ..., ...}``
(meeting commands). ``_command_loop`` normalises both shapes back to a
``(text, data)`` pair so existing handlers keep working unchanged.
"""
_mobile_audio_queue: asyncio.Queue[bytes] = asyncio.Queue(maxsize=200)

# v6 subsystems -- set during pipeline init, used by processors.
_output_poller: Any = None
_slack_poller: Any = None
_research_agent: Any = None
_briefing_system: Any = None
_event_store: Any = None

# v0.3.0 / Path A: handle to the running LLM service.  Populated by
# ``create_pipeline_components`` so the ``mobile_active`` control frame
# handler can flip the persona overlay immediately without waiting for
# the 5-second context enricher tick.  ``None`` before the pipeline is
# built.
_llm_service_handle: AnthropicLLMService | None = None  # noqa: F821 -- forward ref OK

# v0.3.0 / TASK-006: handle to the running PipelineTask so the PTT
# handlers can inject UserStartedSpeakingFrame / UserStoppedSpeakingFrame
# directly into the live pipeline without forking the LLM dispatch path.
# Populated by ``create_pipeline_components`` and never cleared mid-run
# (the next pipeline build overwrites it).  ``None`` before any pipeline
# has been built, in which case the PTT handlers fall back to the
# ``_ptt_active_flag`` toggle for the STT gate and log a debug warning
# explaining that the turn-start/stop frame injection was skipped.
_pipeline_task_handle: Any = None  # PipelineTask; Any to avoid forward-ref issues at module load.

# v0.3.0 / TASK-006: PTT (push-to-talk) lifecycle state.  Tracks open PTT
# windows so out-of-order frames can be detected and a stuck "active"
# state can be auto-recovered if the release frame never arrives (e.g.
# the user quits the app mid-hold).  Keys are arbitrary session ids; for
# now only the Mac overlay surface ever sends PTT frames, so the single
# key ``"mac"`` is used.  Values are wall-clock timestamps from
# ``time.monotonic()``.
_PTT_STATE: dict[str, float] = {}

# Hard upper bound on a single PTT hold; after this the daemon force-
# releases the gate even if no ``ptt_release`` frame arrived.  5 s is
# long enough for normal voice commands but short enough that a stuck
# overlay doesn't permanently brick the mic gate.
_PTT_SAFETY_TIMEOUT_S: Final[float] = 5.0

# Live asyncio.Task handle for the safety-timeout coroutine spawned by
# ``_handle_ptt_active``.  Cancelled on ``_handle_ptt_release`` or when a
# second ``ptt_active`` arrives.
_ptt_safety_task: asyncio.Task[None] | None = None

# Module-level flag that ``pipecat_stt`` and other gate-checking code can
# consult to decide whether the local mic is currently in "force open"
# (PTT hold) mode.  This is the documented fallback when frame injection
# into the live pipeline is unavailable (no _pipeline_task_handle yet).
# v0.3.0 keeps the flag as a public observable surface even when frames
# are also injected -- callers that prefer a synchronous read get one,
# while the pipeline gets the proper Pipecat turn-start/stop frames.
_ptt_active_flag: bool = False

# v0.3.0 / TASK-003: Meeting mode lifecycle state. TASK-006 owns the
# state-machine logic that flips _MEETING_ACTIVE and populates the
# buffer; TASK-007 reads the buffer to write the markdown notes file;
# TASK-008 emits the spoken recap. This module owns ONLY the
# declarations + the documented contract for what each field means.
#
# Why these are module-level rather than instance fields on a class:
# the daemon's existing state (mute flags, PTT flags, pipeline task
# handle, LLM service handle) all live at module scope so the WS
# command-loop closure can read+write them without a `nonlocal`
# dance. Meeting mode follows the same pattern for consistency.
#
# Failure-mode coverage: importing the module always reinitialises
# these to their zero-value defaults, so a daemon crash mid-meeting
# never bricks the next session into a phantom "active" state.

_MEETING_ACTIVE: bool = False
"""True while a meeting is in progress. Flipped by TASK-006's
``__meeting_start__`` and ``__meeting_stop__`` HUD command handlers.
Read by the user-turn-stop dispatcher to decide whether to dispatch
the LLM (False -> normal flow, True -> buffer-and-skip)."""

_MEETING_TITLE: str | None = None
"""Human-readable title for the current meeting. Set by
``__meeting_start__`` from the WS command payload's ``title`` field.
Empty / missing -> ``"untitled"`` (handled by TASK-007's slugify)."""

_MEETING_STARTED_AT: float | None = None
"""``time.monotonic()`` timestamp at which the meeting began. Used
by TASK-007 to compute meeting duration in the markdown header and
by the safety-bounded buffer logic in TASK-006."""

# Each buffer entry shape (set by TASK-006 transcript callback):
#   {
#     "ts":      "2026-05-27T14:30:00+00:00",   # ISO 8601
#     "source":  "mic" | "system",              # which audio stream
#     "speaker": "user" | "other" | "unknown",  # best-effort tag
#     "text":    "Hi everyone, let's start.",
#   }
_MEETING_BUFFER: list[dict[str, Any]] = []
"""Ordered list of transcript entries captured during the current
meeting. Append-only during ``_MEETING_ACTIVE=True`` (with oldest-
entry eviction when the rolling char count exceeds
``_MEETING_BUFFER_CAP``). Cleared by ``__meeting_stop__`` after the
markdown file is written."""

_MEETING_BUFFER_CHARS: int = 0
"""Rolling character count of all ``text`` fields in
``_MEETING_BUFFER``. Used to enforce the cap without recomputing
``sum(len(e["text"]) for e in _MEETING_BUFFER)`` on every append."""

_MEETING_BUFFER_CAP: Final[int] = 100_000
"""Hard upper bound on the meeting transcript buffer in characters.
At ~5 chars per word that's ~20k words / ~3 hours of dense speech.
TASK-006 evicts oldest entries when the cap is exceeded -- the
``Summary`` and ``Action Items`` sections in TASK-007's output then
reflect the surviving window, with a footnote in the markdown
explaining the truncation. Documented here so the cap can be tuned
without hunting through handler code."""

_SUPPRESS_LLM_TURN: bool = False
"""When True, the user-turn-stop event accumulates the transcript
into ``_MEETING_BUFFER`` instead of dispatching the LLM. Flipped
True by ``__meeting_start__`` and back to False by
``__meeting_stop__``. NOT a synonym for ``_MEETING_ACTIVE``: future
features may want LLM suppression without the full meeting state
(e.g. a "silent listen" mode), so we keep the two flags separate."""

_PRE_MEETING_STATE: dict[str, Any] | None = None
"""Snapshot of the STT-gate / TTS-mute flags captured at
``__meeting_start__`` so ``__meeting_stop__`` can restore them.
Populated keys: ``stt_force_muted``, ``wake_gate_armed``,
``ws_bridge_muted``, ``router_tts_muted``. ``None`` outside a
meeting. Reset to ``None`` at the tail of ``__meeting_stop__`` so a
subsequent start captures fresh state rather than the previous
meeting's residue."""

_LAST_SYSTEM_AUDIO_TS: float | None = None
"""``time.monotonic()`` timestamp of the most recent ``system_audio``
frame injected via :func:`_handle_system_audio`. The transcript
finaliser in :class:`WSBridgeProcessor` consults this to tag the
upstream source: if a transcript finalises within ~0.5s of the last
system-audio inject, we attribute it to system audio (i.e. speakers
playing remote meeting participants); otherwise it's mic-side speech.
``None`` until the first ``system_audio`` frame arrives."""

# v0.4.0 / TASK-052: Volume normalisation between mic and system audio.
# Mic and system audio peaks routinely differ by 12-30 dB on Windows (the
# loopback capture path tends to be much louder than condenser mics). The
# unnormalised mix produces transcripts where one speaker looks SHOUTED
# and the other barely registers. We track a rolling peak (in dBFS) for
# each source and apply a bounded gain to the system audio so its peak
# tracks the mic peak within ``_VOL_NORM_TARGET_RANGE_DB``.
#
# Design notes:
#  - We normalise SYSTEM AUDIO only (mic stays untouched). Mic flows
#    through Pipecat's transport and is what the user has tuned for
#    everything else in the daemon -- changing it would ripple through
#    VAD/wake-word/STT calibration.
#  - The mic-peak tracker is updated by :class:`WSBridgeProcessor` on
#    each ``InputAudioRawFrame``/``AudioRawFrame`` it sees. Cheap O(N)
#    on the frame, dominated by Pipecat's existing per-frame work.
#  - Gain is clamped to ``[_VOL_NORM_MIN_GAIN, _VOL_NORM_MAX_GAIN]``.
#    The upper bound prevents amplifying silent system audio into noise
#    when the user is on a one-way call (acceptance criterion #3).
#  - We use a tiny EMA (alpha=0.15) so peaks decay over ~6-7 frames.
#    Long enough to ride out short silences within a phrase, short
#    enough to react when one speaker hands off to the other.
_MIC_PEAK_DBFS: float = -60.0
"""Exponential moving average of recent mic-audio peak in dBFS. Updated
by :class:`WSBridgeProcessor` on each ``AudioRawFrame``/``InputAudioRawFrame``
during meeting mode. Floor of -60 dBFS represents "effectively silent"
(matches the silence floor used in ``_pcm16_peak_dbfs``)."""

_SYSTEM_PEAK_DBFS: float = -60.0
"""Exponential moving average of recent system-audio peak in dBFS.
Updated in :func:`_handle_system_audio` BEFORE gain is applied so the
estimate reflects the source level, not the normalised level."""

_VOL_NORM_TARGET_RANGE_DB: Final[float] = 6.0
"""Acceptance criterion #1: peaks must be within this many dB of each
other after normalisation. We aim for system audio peak == mic peak;
the 6 dB band is the headroom we tolerate before re-applying gain."""

_VOL_NORM_MAX_GAIN_DB: Final[float] = 18.0
"""Upper bound on the gain we will apply to system audio. Caps the
amplification of a quiet remote talker so a momentarily-silent mic
doesn't drag the gain sky-high (acceptance criterion #3). 18 dB ~= 8x
linear, which covers a quiet remote on a loud-mic setup without
crossing into noise-amplification territory."""

_VOL_NORM_MIN_GAIN_DB: Final[float] = -12.0
"""Lower bound on the gain we will apply to system audio. Attenuates
overly hot system audio (e.g. someone joins on speakerphone) so the
mixed transcript doesn't clip the STT input."""

_VOL_NORM_SILENCE_FLOOR_DBFS: Final[float] = -50.0
"""If the mic peak EMA is below this, we treat the mic as silent and
SKIP normalisation -- we don't want to boost system audio to match
silence (acceptance criterion #3). Likewise if system audio itself is
below this floor we leave it alone (boosting silence == boosting
electrical noise)."""

_VOL_NORM_EMA_ALPHA: Final[float] = 0.15
"""Smoothing factor for the rolling peak EMA. Higher = react faster
to changes; lower = smoother. 0.15 gives ~6-7 frame half-life, which
at 16 kHz / typical 20 ms Pipecat frames is ~140 ms -- snappy enough
to follow speaker handoffs without flapping on a single loud word."""

# v0.3.0 / TASK-007: cache of the recap text produced by the most-
# recent ``generate_meeting_notes`` call. TASK-008's
# ``__meeting_recap__`` handler reads this for replay-without-
# re-summarising. None means no meeting has been finalised this
# session (a recap before the first meeting ends is a no-op).
_LAST_MEETING_RECAP: str | None = None
"""Cached recap text from the most-recent ``generate_meeting_notes``
call. TASK-008's ``__meeting_recap__`` handler reads this for replay-
without-re-summarising. None means no meeting has been finalised this
session."""

# v0.1.5 pipeline-status cache. ``create_pipeline_components`` populates
# this with the last payload it shipped over the WS so a late-mounting
# HUD client can request a fresh copy via ``request_pipeline_status``
# without forcing a daemon restart. ``None`` means the pipeline hasn't
# been built yet this session.
_last_pipeline_status: dict[str, Any] | None = None

# The active WS connection used to re-emit ``pipeline_status`` on request.
# Populated by ``create_pipeline_components`` and cleared on disconnect.
_pipeline_status_ws: ClientConnection | None = None


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
    """Handle a text command typed in the HUD input.

    Some HUD clients forward control messages (e.g. ``request_pipeline_status``)
    by wrapping the JSON inside a ``command`` envelope's ``text`` field instead
    of sending it as a top-level WS message. If we don't catch that here, the
    JSON ends up injected into the LLM context as a user turn and the model
    starts talking to itself about pipeline status. Detect a JSON object with a
    known ``type`` and route through the normal dispatcher.
    """
    text = data.get("text", "").strip()
    if not text:
        return
    logger.info("Command from HUD: %s", text)

    if text.startswith("{") and text.endswith("}"):
        try:
            inner = json.loads(text)
        except (json.JSONDecodeError, TypeError):
            inner = None
        if isinstance(inner, dict):
            inner_type = inner.get("type")
            handler = _MESSAGE_HANDLERS.get(inner_type) if inner_type else None
            if handler is not None:
                handler(inner)
                return

    # v0.3.0 / TASK-006: meeting-mode HUD commands may carry payload
    # fields (e.g. ``title``) alongside ``text``. We forward the whole
    # ``data`` dict to the command loop for those so the loop can read
    # ``data.get("title")`` etc. Plain commands (mute, unmute,
    # interrupt) keep the legacy bare-string path -- the command loop
    # accepts both shapes for backwards compatibility.
    _MEETING_COMMAND_TEXTS = (
        "__meeting_start__",
        "__meeting_stop__",
        "__meeting_recap__",
    )
    try:
        if text in _MEETING_COMMAND_TEXTS:
            _command_queue.put_nowait(data)
        else:
            _command_queue.put_nowait(text)
    except asyncio.QueueFull:
        pass


def _handle_mobile_audio(data: dict[str, Any]) -> None:
    """Handle base64-encoded mobile audio (CAF / M4A container, not raw PCM).

    The mobile push-to-talk records into iOS .caf or Android .m4a and ships
    the whole utterance as one blob on press release. Pipecat STT expects
    raw 16-bit signed-LE PCM at 16kHz mono, so we queue the encoded bytes
    here and let the async mobile audio loop transcode via ffmpeg before
    pushing AudioRawFrame.
    """
    audio_b64 = data.get("data", "")
    if not audio_b64:
        logger.info("Mobile audio frame has no 'data' field -- dropping")
        return
    try:
        encoded = base64.b64decode(audio_b64)
        _mobile_audio_queue.put_nowait(encoded)
        # INFO so we can see this even without --debug while diagnosing.
        logger.info("Mobile audio queued (encoded): %d bytes", len(encoded))
    except Exception:
        logger.warning("Failed to decode/queue mobile audio", exc_info=True)


async def _ffmpeg_decode_to_pcm16le(encoded: bytes) -> bytes:
    """Transcode a CAF / M4A blob to raw 16kHz mono s16le PCM via ffmpeg.

    iOS .caf containers require seeking back to read the format header,
    which fails when ffmpeg reads from a pipe. So we write the blob to a
    temp file and feed that file path to ffmpeg. Returns empty bytes on
    any failure -- the caller treats that as "drop this utterance".
    """
    tmp_path: str | None = None
    try:
        with tempfile.NamedTemporaryFile(
            prefix="jarvis-mobile-", suffix=".bin", delete=False
        ) as tmp:
            tmp.write(encoded)
            tmp_path = tmp.name
    except Exception:
        logger.warning("Mobile audio: failed to write temp file", exc_info=True)
        return b""

    try:
        try:
            proc = await asyncio.create_subprocess_exec(
                "ffmpeg",
                "-hide_banner",
                "-loglevel", "error",
                "-i", tmp_path,
                "-f", "s16le",
                "-acodec", "pcm_s16le",
                "-ac", "1",
                "-ar", "16000",
                "pipe:1",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
        except FileNotFoundError:
            logger.warning(
                "Mobile audio: 'ffmpeg' not on PATH -- mobile STT will not work. "
                "Install via 'brew install ffmpeg'."
            )
            return b""
        except Exception:
            logger.warning("Mobile audio: ffmpeg spawn failed", exc_info=True)
            return b""

        try:
            stdout, stderr = await proc.communicate()
        except Exception:
            logger.warning("Mobile audio: ffmpeg communicate failed", exc_info=True)
            return b""

        if proc.returncode != 0:
            logger.warning(
                "Mobile audio: ffmpeg exited %s: %s",
                proc.returncode,
                stderr.decode("utf-8", errors="replace").strip(),
            )
            return b""
        return stdout
    finally:
        if tmp_path:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass


def _handle_mobile_active(data: dict[str, Any]) -> None:
    """Handle the ``mobile_active`` control frame from the Go bridge.

    Sent by ``handlers_jarvis_mobile_ws.go`` immediately after every
    forwarded ``mobile_audio`` chunk.  Flips the active-interlocutor
    state to "mobile" for the upcoming turn so the TTS router picks the
    Friday voice, the LLM persona overlay activates, and the local Mac
    speaker is suppressed for the response.  Decays automatically after
    ``active_client.MOBILE_GRACE_SECONDS`` of silence.

    Also refreshes the LLM system instruction with the Friday persona
    overlay immediately, without waiting for the next 5-second context
    enricher tick -- otherwise the first few mobile turns could fire
    before the prompt updates.
    """
    del data  # No payload fields today; the type alone is the signal.
    active_client.set_mobile_active()
    logger.debug("set_mobile_active() fired (mobile_active received)")

    # Refresh the persona overlay on every mobile_active hit, not just the
    # mac->mobile transition. The 5-second context enricher periodically
    # rebuilds the system instruction and may drop the Friday addendum if
    # ``active_client`` has decayed back to mac between turns. Refreshing on
    # every press guarantees the upcoming turn always sees the Friday prompt.
    if _llm_service_handle is not None:
        try:
            update_system_instruction(
                _llm_service_handle, active_client_value="mobile"
            )
            logger.info("LLM persona overlay refreshed: Friday (mobile)")
        except Exception:
            logger.debug(
                "Failed to refresh LLM system instruction for mobile",
                exc_info=True,
            )


# ---------------------------------------------------------------------------
# v0.4.0 / TASK-052: Volume normalisation helpers
# ---------------------------------------------------------------------------


def _pcm16_peak_dbfs(pcm: bytes) -> float:
    """Compute the peak amplitude of a 16-bit signed-LE mono PCM buffer in dBFS.

    Returns ``_VOL_NORM_SILENCE_FLOOR_DBFS`` for empty / all-zero buffers
    so the caller doesn't have to special-case silence (the silence-floor
    check in :func:`_normalize_system_pcm` already treats values at/below
    that floor as "don't normalise").

    The output is a negative float capped at 0 dBFS (max int16 = 32767
    is 0 dBFS by definition). We use ``numpy`` because:

    1. The standard library's ``audioop`` was removed in Python 3.13 and
       the daemon ships Python 3.13 (TASK-051 setup).
    2. numpy is already imported lazily by ``WakeWordGate`` and the
       startup-audio pre-roll path, so the import cost is amortised.

    Falling back to a slow pure-Python loop on numpy import failure keeps
    the daemon usable in degraded environments (e.g. minimal CI matrix
    runs) -- TASK-052 is volume polish, not a hard dependency.
    """
    if not pcm:
        return _VOL_NORM_SILENCE_FLOOR_DBFS
    try:
        import numpy as np

        samples = np.frombuffer(pcm, dtype=np.int16)
        if samples.size == 0:
            return _VOL_NORM_SILENCE_FLOOR_DBFS
        peak = int(np.abs(samples).max())
    except Exception:  # noqa: BLE001 -- defensive fallback
        # Pure-Python fallback: scan int16 LE samples manually.
        peak = 0
        for i in range(0, len(pcm) - 1, 2):
            lo = pcm[i]
            hi = pcm[i + 1]
            val = lo | (hi << 8)
            if val >= 0x8000:
                val -= 0x10000
            if val < 0:
                val = -val
            if val > peak:
                peak = val
    if peak <= 0:
        return _VOL_NORM_SILENCE_FLOOR_DBFS
    # 20 * log10(peak / 32767). Use math.log10 to avoid pulling numpy
    # into the hot path twice.
    import math

    dbfs = 20.0 * math.log10(peak / 32767.0)
    if dbfs < _VOL_NORM_SILENCE_FLOOR_DBFS:
        return _VOL_NORM_SILENCE_FLOOR_DBFS
    return dbfs


def _update_peak_ema(prev_dbfs: float, sample_dbfs: float) -> float:
    """Update the rolling peak EMA with a new sample.

    Pure function so the meeting-mode tests can reproduce the math
    without touching module globals.
    """
    return (
        _VOL_NORM_EMA_ALPHA * sample_dbfs
        + (1.0 - _VOL_NORM_EMA_ALPHA) * prev_dbfs
    )


def _observe_mic_peak(pcm: bytes) -> None:
    """Feed a mic-audio PCM buffer into the mic-peak EMA.

    Called from :class:`WSBridgeProcessor` for each ``AudioRawFrame`` /
    ``InputAudioRawFrame`` flowing through the pipeline while a meeting
    is active. Cheap no-op outside meeting mode (the caller already
    guards on ``_MEETING_ACTIVE`` but we re-check defensively so the
    helper is safe to call from anywhere).
    """
    global _MIC_PEAK_DBFS
    if not _MEETING_ACTIVE:
        return
    sample_dbfs = _pcm16_peak_dbfs(pcm)
    _MIC_PEAK_DBFS = _update_peak_ema(_MIC_PEAK_DBFS, sample_dbfs)


def _normalize_system_pcm(pcm: bytes) -> bytes:
    """Apply bounded volume gain to system-audio PCM to match mic peak.

    Mutates the module-level ``_SYSTEM_PEAK_DBFS`` with the pre-gain peak
    of ``pcm`` so subsequent calls converge on the right target. Returns
    the (possibly amplified, possibly attenuated, possibly unchanged) PCM
    buffer.

    Behaviour matrix (acceptance criteria mapping):

    1. Mic loud, system quiet  -> apply +gain (capped at +18 dB).
       => post-norm peaks within ``_VOL_NORM_TARGET_RANGE_DB`` of mic.
    2. Mic quiet, system loud  -> apply -gain (capped at -12 dB).
       => no audible clipping.
    3. Mic silent (below floor) -> NO-OP. Don't amplify system audio
       to match silence (silent-source guard).
    4. System silent (below floor) -> NO-OP. Don't amplify noise floor.
    5. Already within target band -> NO-OP. Idempotent on aligned input.

    Clipping guard: after computing gain, we additionally check that the
    post-gain peak would not exceed 0 dBFS (32767). If it would, we
    further attenuate to leave 1 dB of headroom. This catches the edge
    case where the mic peak EMA is hot (e.g. user just clapped) and the
    target would push system samples into saturation.
    """
    global _SYSTEM_PEAK_DBFS

    if not pcm:
        return pcm

    system_dbfs = _pcm16_peak_dbfs(pcm)
    _SYSTEM_PEAK_DBFS = _update_peak_ema(_SYSTEM_PEAK_DBFS, system_dbfs)

    # Acceptance criterion #3: don't over-amplify silence on either side.
    if _MIC_PEAK_DBFS <= _VOL_NORM_SILENCE_FLOOR_DBFS:
        return pcm
    if system_dbfs <= _VOL_NORM_SILENCE_FLOOR_DBFS:
        return pcm

    delta_db = _MIC_PEAK_DBFS - system_dbfs

    # Within the target band? Leave it alone (idempotent + cheap).
    if abs(delta_db) <= _VOL_NORM_TARGET_RANGE_DB:
        return pcm

    # Clamp the gain so we never explode quiet input or pump down loud
    # input below useful levels.
    if delta_db > _VOL_NORM_MAX_GAIN_DB:
        gain_db = _VOL_NORM_MAX_GAIN_DB
    elif delta_db < _VOL_NORM_MIN_GAIN_DB:
        gain_db = _VOL_NORM_MIN_GAIN_DB
    else:
        gain_db = delta_db

    # Clipping guard: if applying the gain would push system_dbfs above
    # -1 dBFS, ratchet the gain back so we keep 1 dB of headroom.
    post_peak_dbfs = system_dbfs + gain_db
    if post_peak_dbfs > -1.0:
        gain_db -= post_peak_dbfs + 1.0

    # Linear gain. 10^(gain_db / 20).
    import math

    gain_linear = math.pow(10.0, gain_db / 20.0)

    try:
        import numpy as np

        samples = np.frombuffer(pcm, dtype=np.int16).astype(np.float32)
        scaled = samples * gain_linear
        # Hard-clip just in case the EMA-based headroom estimate is off
        # on a transient. Hard clip at int16 bounds rather than letting
        # the cast wrap.
        np.clip(scaled, -32768.0, 32767.0, out=scaled)
        return scaled.astype(np.int16).tobytes()
    except Exception:  # noqa: BLE001 -- defensive fallback
        # Pure-Python fallback. Slow, but the daemon must never crash on
        # an injected system-audio frame.
        logger.debug(
            "_normalize_system_pcm: numpy path failed, using fallback"
        )
        out = bytearray(len(pcm))
        for i in range(0, len(pcm) - 1, 2):
            lo = pcm[i]
            hi = pcm[i + 1]
            val = lo | (hi << 8)
            if val >= 0x8000:
                val -= 0x10000
            scaled_val = int(val * gain_linear)
            if scaled_val > 32767:
                scaled_val = 32767
            elif scaled_val < -32768:
                scaled_val = -32768
            if scaled_val < 0:
                scaled_val += 0x10000
            out[i] = scaled_val & 0xFF
            out[i + 1] = (scaled_val >> 8) & 0xFF
        return bytes(out)


def _handle_system_audio(data: dict[str, Any]) -> None:
    """Handle a ``system_audio`` control frame from the Go bridge.

    Sent during meeting mode by the ScreenCaptureKit bridge (TASK-004)
    forwarded via ``app_meeting.go`` (TASK-005). The ``data`` field is
    base64-encoded 16-bit mono 16 kHz PCM (matches CanonicalAudioFormat
    in ``internal/screencapture``). We decode it and inject as a
    ``MobileAudioRawFrame``-style frame so the existing STT pipeline
    picks it up as ordinary input.

    Tagged with ``source="system"`` via the module-level
    ``_LAST_SYSTEM_AUDIO_TS`` flag the transcript callback in
    ``WSBridgeProcessor`` reads when appending to ``_MEETING_BUFFER``
    -- that's how the markdown writer knows the entry came from
    speakers rather than the mic.

    No-op (with a debug log) when meeting mode is not active: a stale
    frame arriving after ``__meeting_stop__`` should be dropped
    silently rather than fed to the pipeline.
    """
    global _LAST_SYSTEM_AUDIO_TS

    if not _MEETING_ACTIVE:
        logger.debug("system_audio dropped: meeting not active")
        return

    b64 = data.get("data", "")
    if not b64:
        logger.warning("system_audio: missing data field")
        return
    try:
        pcm = base64.b64decode(b64)
    except Exception as exc:  # noqa: BLE001 -- defensive: bad frame must not crash
        logger.warning("system_audio: base64 decode failed: %r", exc)
        return

    _LAST_SYSTEM_AUDIO_TS = time.monotonic()

    # v0.4.0 / TASK-052: Normalise system-audio peak to match the
    # rolling mic peak (within ``_VOL_NORM_TARGET_RANGE_DB``) before
    # injecting. This is the "buffer-append step" the task brief calls
    # out -- it's the last hook we control before the audio joins the
    # mic stream in the STT pipeline. The mic-peak EMA is fed by
    # ``WSBridgeProcessor.process_frame`` (see the AudioRawFrame branch
    # there). Guarded by ``_normalize_system_pcm`` against amplifying
    # silence on either side.
    pcm = _normalize_system_pcm(pcm)

    # Inject as a MobileAudioRawFrame so STT consumes it on the same
    # pipeline as the mic path. The mic + system streams mix into one
    # transcript; the source tag on _MEETING_BUFFER entries is set by
    # which flag was most-recently true when the transcript finalised
    # (see WSBridgeProcessor.process_frame -> TranscriptionFrame).
    _inject_pipeline_frames(
        [MobileAudioRawFrame(audio=pcm, sample_rate=16000, num_channels=1)]
    )


async def _ptt_safety_timeout(key: str) -> None:
    """Sleep ``_PTT_SAFETY_TIMEOUT_S`` and force-release if still active.

    Spawned by ``_handle_ptt_active`` as an ``asyncio.Task`` so the
    dispatcher stays synchronous.  Cancelled either by a real
    ``ptt_release`` or by a subsequent ``ptt_active`` (which spawns a
    fresh timeout).
    """
    try:
        await asyncio.sleep(_PTT_SAFETY_TIMEOUT_S)
    except asyncio.CancelledError:
        return
    if key in _PTT_STATE:
        logger.warning(
            "PTT safety timeout fired for %r after %.1fs -- force-releasing gate",
            key,
            _PTT_SAFETY_TIMEOUT_S,
        )
        _handle_ptt_release({})


async def _speak_meeting_recap(text: str) -> None:
    """TASK-008: Speak the given recap text via the existing TTS pipeline.

    Reuses the pipeline task handle that TASK-006 already exposes
    (``_pipeline_task_handle``) and the same frame-injection pattern PTT
    and meeting handlers use elsewhere in the daemon. The frame class is
    :class:`pipecat.frames.frames.TTSSpeakFrame` -- the same one the
    daemon already uses for one-shot synthesis (auto-greeting,
    LLM-failover notices, alerts; see e.g. the greeting block around
    ``await task.queue_frames([TTSSpeakFrame(text=greeting)])``). The
    ``RouterTTSService`` (around line ~1386) explicitly documents this
    frame as the one-shot synthesis path, and ``TTSSpeakFrame`` is
    already imported at module top-level alongside the other Pipecat
    frame types.

    No-op when ``_pipeline_task_handle`` is None (pipeline not yet
    built). No-op when the recap text is empty / whitespace-only.

    Recap text is read aloud in the user's configured voice; by the time
    :func:`_dispatch_meeting_finalisation` reaches this call,
    ``__meeting_stop__`` has already restored the
    ``RouterTTSService.meeting_muted`` flag to its pre-meeting value
    (see TASK-006's ``__meeting_stop__`` block which restores from
    ``_PRE_MEETING_STATE`` BEFORE scheduling the finalisation task).
    The recap therefore flows through TTS as an ordinary utterance.

    Feedback-loop note: this emits a ``TTSSpeakFrame`` which causes the
    pipeline to broadcast ``BotStartedSpeakingFrame`` /
    ``BotStoppedSpeakingFrame``. The ``LocalWhisperSTT`` instance gates
    its own input on ``_bot_speaking`` (see ``pipecat_stt.py`` -- the
    flag flips True on ``BotStartedSpeakingFrame`` and the audio
    pre-processor drops mic frames while it's True), so the recap
    audio can't be picked up and re-transcribed as user input. Meeting
    mode is also fully cleared by the time we get here, so even if the
    recap somehow leaked into STT, ``_SUPPRESS_LLM_TURN`` is False and
    ``_MEETING_ACTIVE`` is False -- the worst case is a stray
    transcript, never a phantom meeting.

    Failure-mode: pipeline injection raising must NOT propagate;
    swallow + log so a recap-speak failure never crashes the WS
    command-loop / finalisation task.
    """
    text = (text or "").strip()
    if not text:
        return
    if _pipeline_task_handle is None:
        logger.debug("recap speak skipped: pipeline task not yet built")
        return
    try:
        await _pipeline_task_handle.queue_frames([TTSSpeakFrame(text=text)])
    except Exception as exc:  # noqa: BLE001 -- defensive: never crash on TTS
        logger.warning("recap speak failed: %r", exc)


async def _dispatch_meeting_finalisation(
    title: str,
    buffer: list[dict[str, Any]],
    ws: Any,
) -> None:
    """TASK-007 implementation: call generate_meeting_notes, cache the
    recap for TASK-008, emit a ``meeting_notes_written`` WS event so
    the Go ``StopMeeting`` binding (TASK-009) can resolve.

    Scheduled via :func:`asyncio.create_task` from ``__meeting_stop__``
    so the WS acknowledgement (``state=idle``) returns immediately and
    a slow LLM summary call doesn't block the UI.

    Failure handling: every external call (config load, LLM call, file
    write, WS notify) is defensively wrapped so a failure at any layer
    surfaces as a log warning rather than a daemon crash. The user
    always gets *some* markdown file (raw-only fallback at minimum),
    and the WS event is emitted on success so the Go side can resolve.
    """
    global _LAST_MEETING_RECAP

    # Lazy import: meeting_notes.py is loaded only when a meeting
    # actually ends, keeping the module-load graph shallow and letting
    # TASK-013 mock the LLM cleanly without dragging in the daemon.
    try:
        from meeting_notes import generate_meeting_notes
    except ImportError as exc:
        logger.error("meeting_notes import failed: %r", exc)
        return

    cfg = _load_config_safe()
    notes_dir_raw = cfg.get("meetingNotesDir") if isinstance(cfg, dict) else ""
    notes_dir: str = notes_dir_raw if isinstance(notes_dir_raw, str) and notes_dir_raw else "~/.jarvis/meetings"

    if _llm_service_handle is None:
        logger.warning(
            "meeting finalisation: LLM service not initialised; "
            "using raw-only fallback"
        )

    try:
        markdown_path, recap = await generate_meeting_notes(
            title=title,
            buffer=buffer,
            llm_service=_llm_service_handle,
            notes_dir=notes_dir,
        )
    except Exception as exc:  # noqa: BLE001 -- never crash the daemon
        logger.exception("meeting finalisation failed: %r", exc)
        return

    _LAST_MEETING_RECAP = recap

    # TASK-008: Speak the recap aloud if non-empty. Empty recap happens
    # in the documented failure cases handled inside
    # generate_meeting_notes (TASK-007):
    #   - buffer was empty -> stub markdown + empty recap
    #   - LLM summary call failed -> raw-only fallback + empty recap
    # Both cases should NOT trigger an audible recap. The ``and buffer``
    # guard is belt-and-braces against any future regression where
    # TASK-007 returns a non-empty recap on an empty buffer.
    if recap and buffer:
        await _speak_meeting_recap(recap)

    # Notify the Go side so StopMeeting can resolve. WS shape mirrors
    # the established ``await ws.send(json.dumps(...))`` pattern used
    # by send_state / send_transcript / send_response upthread.
    try:
        await ws.send(json.dumps({
            "type": "meeting_notes_written",
            "path": markdown_path,
            "title": title,
            "buffer_entries": len(buffer),
        }))
    except Exception as exc:  # noqa: BLE001 -- non-fatal; just a notify
        logger.warning("meeting finalisation: ws notification failed: %r", exc)

    logger.info(
        "meeting finalisation complete: title=%r, path=%s, recap_chars=%d",
        title,
        markdown_path,
        len(recap),
    )


def _load_config_safe() -> dict[str, Any]:
    """Load ~/.jarvis/config.json defensively.

    Returns an empty dict on any failure (missing file, malformed JSON,
    permission error). The caller treats an empty dict as "use built-in
    defaults" -- never crash the daemon on a missing or corrupt config.

    Why inline JSON read instead of the ``config.load_config`` helper:
    ``load_config()`` is synchronous and the daemon has a module-level
    config import chain. This helper is the minimum surface needed by
    the meeting finalisation task and avoids tangling that import graph
    in case the TASK-006 → TASK-007 ordering ever moves around.
    """
    from pathlib import Path

    try:
        path = Path("~/.jarvis/config.json").expanduser()
        if path.exists():
            data = json.loads(path.read_text(encoding="utf-8"))
            if isinstance(data, dict):
                return data
    except Exception:  # noqa: BLE001 -- intentionally swallow
        logger.debug("meeting finalisation: config load failed", exc_info=True)
    return {}


def _inject_pipeline_frames(frames: list[Any]) -> bool:
    """Best-effort frame injection into the live PipelineTask.

    Returns True if the frames were scheduled, False if no pipeline task
    is available yet (cold start before ``create_pipeline_components``)
    or if scheduling raised.  Callers should treat False as "the gate
    flag will have to carry the turn" -- the LLM dispatch path is still
    driven by the existing VAD machinery downstream.
    """
    if _pipeline_task_handle is None:
        logger.debug(
            "PTT: pipeline task not yet built; relying on _ptt_active_flag only"
        )
        return False
    try:
        asyncio.create_task(
            _pipeline_task_handle.queue_frames(frames),
            name=f"ptt-inject-{type(frames[0]).__name__ if frames else 'empty'}",
        )
        return True
    except Exception:  # noqa: BLE001 -- defensive: never crash the dispatcher
        logger.warning("PTT: failed to inject frames into pipeline", exc_info=True)
        return False


def _handle_ptt_active(data: dict[str, Any]) -> None:
    """Handle a ``ptt_active`` control frame from the Go bridge.

    Sent by ``handlers_jarvis_ws.go`` when the global PTT hotkey
    (default ``Option+Space``) is pressed.  Opens the Mac STT input
    gate, transitions the daemon's published state to ``listening``,
    and injects a ``UserStartedSpeakingFrame`` into the live pipeline
    so the LLM-context aggregator behaves exactly as it would for a
    VAD-driven turn.

    Idempotent: a second ``ptt_active`` without an intervening release
    is logged at WARNING level and otherwise ignored -- the existing
    window remains open and the safety timeout is NOT reset (a stuck
    overlay can still self-recover after the original 5 s budget).
    """
    del data  # No payload fields today; the type alone is the signal.

    global _ptt_active_flag, _ptt_safety_task

    key = "mac"
    if key in _PTT_STATE:
        logger.warning(
            "ptt_active received while already active for %r -- ignoring "
            "(use ptt_release first); existing safety timeout retained",
            key,
        )
        return

    now = time.monotonic()
    _PTT_STATE[key] = now
    _ptt_active_flag = True

    # Mark the Mac as the active interlocutor so TTS routing + persona
    # overlay match the existing local-mic flow.
    active_client.set_mac_active(now=now)

    # Publish "listening" so connected UI clients render the orb in the
    # right mode.  Scheduled as a background task because this dispatcher
    # is synchronous; the WS handle lives in ``_pipeline_status_ws``
    # which is populated by the same ``create_pipeline_components`` call
    # that populates ``_pipeline_task_handle``.
    ws = _pipeline_status_ws
    if ws is not None:
        try:
            asyncio.create_task(
                send_state(ws, "listening"),
                name="ptt-active-state",
            )
        except Exception:  # noqa: BLE001
            logger.debug("PTT: failed to schedule state=listening send", exc_info=True)

    # Inject the same turn-start frame Pipecat's VAD path emits.  The
    # LLM-context aggregator listens for this frame and opens a fresh
    # user-turn boundary; no fork of the dispatch logic is needed.
    _inject_pipeline_frames([UserStartedSpeakingFrame()])

    # Schedule the safety timeout that force-releases if no release frame
    # arrives within _PTT_SAFETY_TIMEOUT_S.  Cancel any leftover task from
    # a previous mis-paired cycle defensively.
    if _ptt_safety_task is not None and not _ptt_safety_task.done():
        _ptt_safety_task.cancel()
    safety_coro = _ptt_safety_timeout(key)
    try:
        _ptt_safety_task = asyncio.create_task(
            safety_coro,
            name="ptt-safety-timeout",
        )
    except RuntimeError:
        # No running loop (e.g. in a unit test calling the handler
        # synchronously).  Skip the timeout -- the test will drive
        # release explicitly.  Close the unscheduled coroutine to avoid
        # a "coroutine was never awaited" RuntimeWarning.
        safety_coro.close()
        _ptt_safety_task = None
        logger.debug("PTT: no running loop; safety timeout not scheduled")

    logger.info("PTT active (overlay hotkey pressed): gate open")


def _handle_ptt_release(data: dict[str, Any]) -> None:
    """Handle a ``ptt_release`` control frame from the Go bridge.

    Sent by ``handlers_jarvis_ws.go`` when the PTT hotkey is released.
    Closes the gate, finalizes the current transcription window via an
    injected ``UserStoppedSpeakingFrame`` (the same frame VAD emits when
    the user stops speaking), and lets the existing LLM-turn pipeline
    do its job downstream.

    Idempotent failure case: a ``ptt_release`` arriving without a prior
    ``ptt_active`` is logged at WARNING level and returns without
    raising.  This is the documented failure mode from TASK-006 acceptance
    criteria (out-of-order frames must not crash the daemon).
    """
    del data  # No payload fields today.

    global _ptt_active_flag, _ptt_safety_task

    key = "mac"
    if key not in _PTT_STATE:
        logger.warning(
            "ptt_release received without prior ptt_active for %r -- ignoring",
            key,
        )
        return

    # Cancel the safety timeout if still pending.
    if _ptt_safety_task is not None and not _ptt_safety_task.done():
        _ptt_safety_task.cancel()
    _ptt_safety_task = None

    _PTT_STATE.pop(key, None)
    _ptt_active_flag = False

    # Inject the matching turn-stop frame so the LLM aggregator flushes
    # the user turn into the dispatch path used by the VAD-driven flow.
    # The downstream STT processor finalizes any in-flight transcription
    # and the LLM picks it up exactly as if VAD had ended the turn.
    _inject_pipeline_frames([UserStoppedSpeakingFrame()])

    logger.info("PTT release (overlay hotkey released): gate closed, turn dispatched")


async def send_mobile_tts(
    ws: ClientConnection,
    pcm_chunk: bytes,
    sample_rate: int = 16000,
) -> None:
    """Send a TTS audio chunk to mobile clients via the WS bridge.

    The Go server forwards ``mobile_tts`` messages to connected mobile
    WebSocket clients so they can play Jarvis audio remotely. ``sample_rate``
    must match the rate Pipecat is emitting (currently 16kHz for the
    MacOSSayTTSService router target) -- a mismatch makes playback sound
    slowed or sped on the phone.
    """
    try:
        await ws.send(json.dumps({
            "type": "mobile_tts",
            "data": base64.b64encode(pcm_chunk).decode(),
            "sampleRate": sample_rate,
        }))
    except Exception:
        pass  # Don't crash on WS errors for audio streaming


def _handle_retry_model_download(data: dict[str, Any]) -> None:
    """Handle a model-download retry request from the HUD.

    The HUD sends this when the user clicks "retry" on a failed model in
    the first-run overlay. We re-enter ``model_status.ensure_model`` with
    ``force=True`` on a background task so this dispatcher stays sync.
    """
    asyncio.create_task(
        model_status.handle_retry_message(data),
        name=f"retry-model-{data.get('model', 'unknown')}",
    )


def _handle_request_pipeline_status(data: dict[str, Any]) -> None:
    """Handle a HUD ``request_pipeline_status`` message.

    The HUD sends this on every WS reconnect so a late-mounting client
    gets the current pipeline indicator state without needing a daemon
    restart. We replay the cached payload from the most recent
    ``create_pipeline_components`` build. If the cache is empty (pipeline
    hasn't been built yet) we skip silently — the post-build emit will
    catch the HUD up shortly. ``data`` is currently ignored; reserved for
    future filter / version params.
    """
    del data  # Reserved; currently no fields.
    payload = _last_pipeline_status
    ws = _pipeline_status_ws
    if payload is None or ws is None:
        logger.debug(
            "request_pipeline_status received before pipeline build; "
            "deferring (cache=%s, ws=%s)",
            payload is not None,
            ws is not None,
        )
        return
    asyncio.create_task(
        ws.send(json.dumps(payload)),
        name="resend-pipeline-status",
    )


def _handle_request_model_setup(data: dict[str, Any]) -> None:
    """Handle a HUD ``request_model_setup`` message (v0.1.6).

    Fix for the race condition where the daemon emits the first
    ``model_setup`` event ~1-2s before the React HUD's WS connection
    is established. Without this, a fresh-install user never saw the
    FirstRunDownloadOverlay because the ``downloading`` state arrived
    while no client was subscribed. The HUD now sends this message on
    mount, and ``model_status`` replays the most recent cached payload.
    """
    asyncio.create_task(
        model_status.handle_request_setup_message(data),
        name="resend-model-setup",
    )


_MESSAGE_HANDLERS: dict[str, Any] = {
    "context": _handle_context,
    "tool_result": _handle_tool_result,
    "command": _handle_command,
    "mobile_audio": _handle_mobile_audio,
    "mobile_active": _handle_mobile_active,
    "ptt_active": _handle_ptt_active,
    "ptt_release": _handle_ptt_release,
    "system_audio": _handle_system_audio,
    "retry_model_download": _handle_retry_model_download,
    "request_pipeline_status": _handle_request_pipeline_status,
    "request_model_setup": _handle_request_model_setup,
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

        # v0.4.0 / TASK-052: Feed mic-audio peaks into the volume-norm
        # EMA so :func:`_handle_system_audio` can match system-audio
        # peaks to the mic side. Only active during meeting mode; the
        # ``_observe_mic_peak`` helper short-circuits otherwise. We use
        # ``MobileAudioRawFrame`` exclusion to avoid double-counting
        # system audio that we already injected via _handle_system_audio
        # (those frames re-enter as InputAudioRawFrame subclasses).
        if _MEETING_ACTIVE and isinstance(frame, AudioRawFrame):
            if not isinstance(frame, MobileAudioRawFrame):
                audio_bytes = getattr(frame, "audio", None)
                if isinstance(audio_bytes, (bytes, bytearray)):
                    try:
                        _observe_mic_peak(bytes(audio_bytes))
                    except Exception:  # noqa: BLE001 -- never let metering crash audio
                        logger.debug(
                            "WSBridge: mic peak observation failed",
                            exc_info=True,
                        )

        # v0.3.0 / TASK-006: LLM suppression during meeting mode.
        # ``__meeting_start__`` flips ``_SUPPRESS_LLM_TURN`` True so that
        # user-turn-stop events feed the meeting buffer (see the
        # TranscriptionFrame branch below) rather than dispatch an LLM
        # response. The UserStoppedSpeakingFrame downstream of the
        # aggregator is what kicks the LLM run, so swallowing it here is
        # the simplest hook -- everything else (state, transcript
        # broadcast) still flows because we run BEFORE the
        # super().push_frame() call at the tail of the method.
        if _SUPPRESS_LLM_TURN and isinstance(frame, UserStoppedSpeakingFrame):
            logger.debug(
                "WSBridge: meeting mode suppression -- swallowing "
                "UserStoppedSpeakingFrame so the LLM does not dispatch"
            )
            return

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

                # v0.3.0 / TASK-006: meeting buffer capture. When a
                # meeting is active, accumulate transcripts into
                # ``_MEETING_BUFFER`` with the source tag derived from
                # how recently a ``system_audio`` frame landed. The
                # transcript still flows downstream (we want the HUD to
                # see live transcription during the meeting) but the LLM
                # is gated off via the UserStoppedSpeakingFrame
                # short-circuit at the top of this method.
                if _MEETING_ACTIVE:
                    self._append_meeting_buffer(frame.text)

                text = frame.text.strip()
                if text:
                    # v0.3.0/TASK-018 -- confirmation gate: yes/no replies
                    # resolve a pending tool confirmation instead of
                    # dispatching to the LLM.
                    if _tool_executor is not None and _tool_executor.resolve_pending_confirmation(text):
                        await send_transcript(self._ws, text, partial=False)
                        logger.info("Confirmation answered: %s", text)
                        return

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

    @staticmethod
    def _append_meeting_buffer(text: str) -> None:
        """Append a finalised transcript to the meeting transcript buffer.

        Called from ``process_frame`` on every ``TranscriptionFrame``
        while ``_MEETING_ACTIVE`` is True. The source tag is derived
        from how recently a ``system_audio`` frame landed (see
        :func:`_handle_system_audio`): within 0.5s -> ``"system"``
        (speakers), otherwise ``"mic"``.

        Implements the rolling-window cap: when the running
        char-count exceeds ``_MEETING_BUFFER_CAP`` we pop entries from
        the front until under the cap. This bounds memory on long
        meetings without truncating the most recent (and most useful
        for summarisation) speech.

        Empty / whitespace-only text is dropped. The frame still flows
        downstream so the HUD's live-transcript UI keeps updating
        regardless of buffering policy.
        """
        global _MEETING_BUFFER_CHARS

        cleaned = (text or "").strip()
        if not cleaned:
            return

        now = time.monotonic()
        last_sys = _LAST_SYSTEM_AUDIO_TS
        is_system = last_sys is not None and (now - last_sys) < 0.5
        source = "system" if is_system else "mic"
        speaker = "other" if is_system else "user"

        entry: dict[str, Any] = {
            "ts": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "source": source,
            "speaker": speaker,
            "text": cleaned,
        }
        _MEETING_BUFFER.append(entry)
        _MEETING_BUFFER_CHARS += len(cleaned)

        # Rolling-window eviction: drop oldest entries while we're over
        # the cap. Keep at least one entry so a single oversized turn
        # doesn't blank the buffer entirely.
        while (
            _MEETING_BUFFER_CHARS > _MEETING_BUFFER_CAP
            and len(_MEETING_BUFFER) > 1
        ):
            evicted = _MEETING_BUFFER.pop(0)
            _MEETING_BUFFER_CHARS -= len(evicted.get("text", ""))

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


# Grace window after the LLM starts generating during which a VAD-triggered
# UserStartedSpeakingFrame is treated as a phantom (tail of the user's own
# utterance, breath, or low-amplitude room noise) and dropped instead of
# cancelling the in-flight response. Without this guard, a single false VAD
# tick between LLM start and TTS start silently kills Jarvis's reply before
# we ever hear it.
_INTERRUPT_GRACE_SECONDS: float = 2.5


class InterruptionHandler(FrameProcessor):
    """Allows the user to interrupt Jarvis by speaking during TTS playback.

    Tracks bot speaking state via BotStarted/StoppedSpeakingFrame. When
    UserStartedSpeakingFrame arrives during bot speech, cancels the current
    TTS output so the pipeline transitions to listening.

    Also enforces a ~2.5s grace window after the LLM starts responding,
    during which VAD-only UserStartedSpeakingFrames are dropped (a phantom
    VAD tick during the LLM/TTS handoff was killing the reply before audio
    ever played).
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
        self._llm_responding = False
        self._llm_started_at: float = 0.0

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        # MUST call super first so Pipecat's base class handles StartFrame
        # and marks this processor as started. Without this, every frame
        # is rejected with "StartFrame not received yet".
        await super().process_frame(frame, direction)

        if isinstance(frame, LLMFullResponseStartFrame):
            self._llm_responding = True
            self._llm_started_at = time.monotonic()
        elif isinstance(frame, LLMFullResponseEndFrame):
            self._llm_responding = False
        elif isinstance(frame, BotStartedSpeakingFrame):
            self._bot_speaking = True
        elif isinstance(frame, BotStoppedSpeakingFrame):
            self._bot_speaking = False
            # Re-arm the wake gate as soon as the bot finishes a reply so
            # the post-detection open window doesn't keep accepting random
            # background chatter for the rest of its 6s lifetime.
            if self._wake_gate is not None and self._wake_gate.armed:
                self._wake_gate.rearm()
        elif isinstance(frame, UserStartedSpeakingFrame):
            # Path A (v0.3.0): Mark the Mac as the active interlocutor when
            # local-mic VAD fires.  We guard against clobbering a fresh
            # mobile turn: ``mobile_active`` is forwarded with every mobile
            # audio chunk, and that same audio also drives VAD through this
            # same pipeline.  If the last mobile activity was within a
            # 2-second window we assume this VAD tick is the mobile audio
            # itself and skip the flip.  Outside that window (or never), the
            # frame is genuine local-mic speech and we mark Mac active.
            #
            # Order matters: suppress phantom + bot-speaking cases FIRST so
            # those VAD ticks never touch active_client.  Otherwise an
            # acoustic blip during a mobile turn (or the bot's own TTS
            # leaking into the Mac mic) would flip active_client to "mac"
            # mid-response, re-routing Friday's voice onto the Mac speaker.

            # Suppress phantom VAD ticks during the LLM→TTS handoff window.
            within_grace = (
                self._llm_responding
                and (time.monotonic() - self._llm_started_at) < _INTERRUPT_GRACE_SECONDS
            )
            if within_grace and not self._bot_speaking:
                logger.info(
                    "Suppressing phantom UserStartedSpeakingFrame (LLM responding, +%.2fs)",
                    time.monotonic() - self._llm_started_at,
                )
                return  # don't push -- swallow the frame entirely
            if self._bot_speaking:
                # Bot's own audio is hitting the Mac mic -- don't re-route
                # the turn we're in the middle of.  Treat as interrupt only.
                logger.info("User interrupted Jarvis -- stopping speech")
                self._bot_speaking = False
                await self.push_frame(BotStoppedSpeakingFrame(), FrameDirection.DOWNSTREAM)
                if self._ws_bridge:
                    await self._ws_bridge._set_state("listening")
            else:
                # Genuine local-mic turn start: only now is it safe to flip
                # active_client to "mac".  The fresh-mobile guard still
                # applies so VAD picking up the phone's outgoing audio
                # doesn't clobber the mobile turn that just kicked off.
                _last_mobile = active_client.get_last_mobile_activity_at()
                _fresh_mobile = (
                    _last_mobile is not None
                    and (time.monotonic() - _last_mobile) < 2.0
                )
                if not _fresh_mobile:
                    active_client.set_mac_active()

        await self.push_frame(frame, direction)


# ---------------------------------------------------------------------------
# Path A (v0.3.0): per-turn TTS routing
# ---------------------------------------------------------------------------


class RouterTTSService(FrameProcessor):
    """Composite TTS service that delegates per-turn to one of two providers.

    Owns a VibeVoice instance (the Mac "Jarvis" voice -- British male) and a
    macOS ``say`` instance (the phone "Friday" voice -- British female).  At
    the start of every turn the router consults ``active_client.get_active()``
    and pins the chosen inner service for the remainder of that turn.

    Routing decision points:

      * ``TTSSpeakFrame``  -- one-shot synthesis (auto-greeting, alerts).
        Pick the inner service immediately, route the frame, done.
      * ``LLMFullResponseStartFrame`` -- streaming response from the LLM.
        Pick the inner service for the whole response and remember it until
        ``LLMFullResponseEndFrame`` so all the intermediate ``TextFrame``s
        flow into the same provider (otherwise mid-sentence the voice would
        flip and we'd get a Frankenvoice).

    Non-routed frames (e.g. ``EndFrame``, pass-throughs) are forwarded
    downstream unchanged; the router itself isn't a synthesiser, just a
    multiplexer.

    Why a multiplexer rather than ``task.set_processors(...)`` swapping?  The
    pipeline holds references to processors at build time; swapping live
    requires rebuilding the queue plumbing and racing in-flight frames.  A
    single processor that picks downstream is mechanically simpler and keeps
    Pipecat's StartFrame propagation intact for both inner services.
    """

    def __init__(
        self,
        mac_tts: FrameProcessor,
        mobile_tts: FrameProcessor,
        **kwargs: Any,
    ) -> None:
        super().__init__(**kwargs)
        self._mac_tts = mac_tts
        self._mobile_tts = mobile_tts
        # When we're inside an LLM response we keep routing every TextFrame
        # to the same provider as the ``LLMFullResponseStartFrame``.
        self._pinned_inner: FrameProcessor | None = None
        self._started_inners: set[int] = set()
        # v0.3.0 / TASK-006: meeting-mode TTS suppression. While True
        # we drop TTS-synthesis-bearing frames (TTSSpeakFrame and the
        # LLMFullResponseStart/Text/End trio that drive streaming
        # synthesis) so Jarvis stays silent for the duration of the
        # meeting. We deliberately do NOT swallow state frames
        # (BotStartedSpeakingFrame, control frames, EndFrame, etc.)
        # -- those still need to flow downstream so the HUD, the
        # interruption handler, and pipeline teardown all see
        # consistent state. ``__meeting_start__`` flips this True;
        # ``__meeting_stop__`` restores the prior value from
        # ``_PRE_MEETING_STATE``.
        self.meeting_muted: bool = False

    @property
    def mac_tts(self) -> FrameProcessor:
        return self._mac_tts

    @property
    def mobile_tts(self) -> FrameProcessor:
        return self._mobile_tts

    # ----- Side-channel hook setters -----------------------------------
    # The voice_session() bootstrap accesses these via ``hasattr(tts, ...)``
    # on the outer RouterTTSService; fan the assignment out to both inner
    # services so audio_level / mobile_tts callbacks fire regardless of
    # which provider speaks.

    def __setattr__(self, name: str, value: Any) -> None:
        super().__setattr__(name, value)
        if name in ("_audio_send_fn", "_mobile_tts_fn"):
            mac = self.__dict__.get("_mac_tts")
            mob = self.__dict__.get("_mobile_tts")
            if mac is not None and hasattr(mac, name):
                object.__setattr__(mac, name, value)
            if mob is not None and hasattr(mob, name):
                object.__setattr__(mob, name, value)

    # ----- Pipeline linking -------------------------------------------

    def link(self, processor: Any) -> None:
        """Link this router AND both inner services to ``processor``.

        When the inner services call ``push_frame()`` to emit
        ``TTSAudioRawFrame`` / ``TTSStartedFrame`` / etc., Pipecat looks
        up ``self._next`` -- which would normally be ``None`` because the
        inners aren't in the pipeline graph.  We mirror the link onto
        both inners so their emitted frames continue downstream to the
        speaker transport / assistant aggregator as if the active inner
        were the pipeline's TTS stage itself.
        """
        super().link(processor)
        for inner in (self._mac_tts, self._mobile_tts):
            try:
                # Don't set processor._prev again; the router already owns
                # the upstream side of the link.  We only want to wire
                # _next so inner.push_frame() routes correctly.
                inner._next = processor  # type: ignore[attr-defined]
            except Exception:
                logger.debug(
                    "Failed to set _next on inner TTS %s",
                    type(inner).__name__,
                    exc_info=True,
                )

    # ----- Prewarm -----------------------------------------------------

    async def prewarm(self) -> None:
        """Prewarm both inner services so either is ready on first turn."""
        for inner in (self._mac_tts, self._mobile_tts):
            if hasattr(inner, "prewarm"):
                try:
                    await inner.prewarm()
                except Exception:
                    logger.debug(
                        "Inner TTS prewarm failed for %s",
                        type(inner).__name__,
                        exc_info=True,
                    )

    # ----- Inner-service routing --------------------------------------

    def _select_inner(self) -> FrameProcessor:
        """Pick the inner service for a freshly starting turn."""
        active = active_client.get_active()
        if active == "mobile":
            logger.info(
                "RouterTTS: routing turn to MOBILE provider (%s)",
                type(self._mobile_tts).__name__,
            )
            return self._mobile_tts
        logger.info(
            "RouterTTS: routing turn to MAC provider (%s)",
            type(self._mac_tts).__name__,
        )
        return self._mac_tts

    async def _ensure_started(self, inner: FrameProcessor, frame: Frame) -> None:
        """Replay Pipecat's StartFrame to ``inner`` exactly once.

        Inner services are added downstream of the router and would
        normally receive their own StartFrame -- but because we shortcut
        the pipeline and call inner.process_frame() directly, we have to
        bootstrap them ourselves.  ``process_frame`` on FrameProcessor
        rejects all frames before its first StartFrame, so we re-send the
        router's own StartFrame to each inner the first time it's used.
        """
        ident = id(inner)
        if ident in self._started_inners:
            return
        # Lazy import to avoid a circular reference at module load.
        try:
            from pipecat.frames.frames import StartFrame  # type: ignore
        except Exception:
            return
        if isinstance(frame, StartFrame):
            await inner.process_frame(frame, FrameDirection.DOWNSTREAM)
            self._started_inners.add(ident)

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        # Propagate the pipeline StartFrame to both inner services so they
        # accept subsequent frames.  Pipecat's base class rejects any
        # non-StartFrame frame before its own internal start flag is set.
        try:
            from pipecat.frames.frames import StartFrame  # type: ignore
        except Exception:
            StartFrame = None  # type: ignore

        if StartFrame is not None and isinstance(frame, StartFrame):
            for inner in (self._mac_tts, self._mobile_tts):
                try:
                    await inner.process_frame(frame, direction)
                    self._started_inners.add(id(inner))
                except Exception:
                    logger.debug(
                        "Inner TTS StartFrame propagation failed for %s",
                        type(inner).__name__,
                        exc_info=True,
                    )
            await self.push_frame(frame, direction)
            return

        # v0.3.0 / TASK-006: meeting-mode TTS suppression. Drop the
        # synthesis-bearing frames (TTSSpeakFrame + the LLM-response
        # streaming trio) so neither inner provider speaks during a
        # meeting. We deliberately do NOT swallow other frames -- state
        # events (BotStarted/Stopped), control frames, EndFrame must
        # still flow so the HUD, interruption handler, and pipeline
        # teardown stay consistent. Mirrors the ``force_muted``
        # short-circuit in ``pipecat_stt.LocalWhisperSTT``.
        if self.meeting_muted and isinstance(
            frame,
            (
                TTSSpeakFrame,
                LLMFullResponseStartFrame,
                TextFrame,
                LLMFullResponseEndFrame,
            ),
        ):
            # Reset any pinned inner on the end-of-response boundary so
            # the next non-meeting turn picks the right provider afresh.
            if isinstance(frame, LLMFullResponseEndFrame):
                self._pinned_inner = None
            logger.debug(
                "RouterTTS: meeting_muted, dropping %s",
                type(frame).__name__,
            )
            return

        # One-shot synth: pick provider per-frame.
        if isinstance(frame, TTSSpeakFrame):
            inner = self._select_inner()
            await self._ensure_started(inner, frame)
            await inner.process_frame(frame, direction)
            return

        # Streaming response: pin the chosen provider for the whole response.
        if isinstance(frame, LLMFullResponseStartFrame):
            self._pinned_inner = self._select_inner()
            await self._pinned_inner.process_frame(frame, direction)
            return

        if isinstance(frame, (TextFrame, LLMFullResponseEndFrame)):
            inner = self._pinned_inner or self._select_inner()
            await inner.process_frame(frame, direction)
            if isinstance(frame, LLMFullResponseEndFrame):
                self._pinned_inner = None
            return

        # Everything else flows through unchanged.
        await self.push_frame(frame, direction)


class ClientAwareTransportRouter(FrameProcessor):
    """Gate audio frames to ``LocalAudioOutputTransport`` per active client.

    Sits in the pipeline between the (Router)TTS service and the speaker
    transport.  When the active client is ``"mobile"`` we drop
    ``TTSAudioRawFrame`` so the Mac speaker stays silent while Friday's
    voice plays only on the phone (the daemon's per-chunk
    ``_mobile_tts_fn`` callback already streams those bytes to the mobile
    WS).  When the active client is ``"mac"`` everything passes through
    unchanged and the Mac speaker plays normally.

    Bot-speaking state frames (``BotStartedSpeakingFrame`` /
    ``BotStoppedSpeakingFrame``) are *not* dropped -- the HUD and
    interruption handler still need accurate state regardless of which
    speaker the audio actually plays through.
    """

    def __init__(self, **kwargs: Any) -> None:
        super().__init__(**kwargs)

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        # Pipecat's TTSAudioRawFrame is the only audio payload reaching the
        # speaker transport.  Drop it on mobile turns so the Mac stays silent.
        if isinstance(frame, TTSAudioRawFrame):
            if active_client.get_active() == "mobile":
                # Audio for this turn is being broadcast to the phone via the
                # TTS service's ``_mobile_tts_fn`` callback -- silence the
                # Mac speaker by short-circuiting the frame here.
                return

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
    Priority: nvidiaAPIKey > googleAPIKey > jarvisAPIKey (sk-or-) > jarvisAPIKey (sk-ant-) > ollama.

    Reads the user-facing `jarvisAPIKey` (with `dexAPIKey` legacy fallback)
    via `get_api_key`, so a fresh OpenRouter / Anthropic key set in the
    Connections panel is honoured on the next daemon restart.
    """
    if config.get("nvidiaAPIKey"):
        return "nvidia"
    if config.get("googleAPIKey"):
        return "google"
    api_key = get_api_key(config)
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
    # `jarvis_key` reads `jarvisAPIKey` first (current key name), falling
    # back to `dexAPIKey` for pre-rename configs via the `get_api_key` helper.
    # Reading `dexAPIKey` directly here was the bug that made a fresh
    # OpenRouter key set in Settings appear to do nothing.
    jarvis_key = get_api_key(config).strip()
    if jarvis_key.startswith("sk-or-"):
        chain.append({
            "name": "OpenRouter",
            "base_url": "https://openrouter.ai/api/v1",
            "api_key": jarvis_key,
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


def _resolve_input_device_index(device_name: str | None) -> int | None:
    """Map a PyAudio device name (from the frontend dropdown) to its index.

    The frontend stores the human-readable device name (e.g. ``"MacBook Pro
    Microphone"``) in ``micInputDevice``. PyAudio addresses devices by integer
    index, which is unstable across reboots, so we re-resolve the name on
    every daemon start.

    Returns ``None`` (= system default) when:
        - ``device_name`` is blank / ``None``
        - PyAudio isn't importable
        - the name doesn't match any input-capable device

    A successful match is logged at INFO so the user can confirm the daemon
    picked the right device.
    """
    if not device_name:
        return None
    try:
        import pyaudio
    except ImportError:
        logger.warning(
            "micInputDevice=%r requested but pyaudio not installed; "
            "falling back to system default",
            device_name,
        )
        return None

    pa = pyaudio.PyAudio()
    try:
        target = device_name.strip().lower()
        # Exact match first, then case-insensitive substring fallback.
        exact: int | None = None
        partial: int | None = None
        for idx in range(pa.get_device_count()):
            try:
                info = pa.get_device_info_by_index(idx)
            except Exception:  # noqa: BLE001
                continue
            if int(info.get("maxInputChannels", 0)) <= 0:
                continue
            name = str(info.get("name", "")).strip().lower()
            if name == target:
                exact = idx
                break
            if partial is None and target and target in name:
                partial = idx
        chosen = exact if exact is not None else partial
        if chosen is None:
            logger.warning(
                "micInputDevice=%r did not match any input device; "
                "falling back to system default",
                device_name,
            )
            return None
        info = pa.get_device_info_by_index(chosen)
        logger.info(
            "Mic input device: %s (index=%d, requested=%r)",
            info.get("name"),
            chosen,
            device_name,
        )
        return chosen
    finally:
        pa.terminate()


def _create_audio_transport(config: dict[str, Any]) -> Any:
    """Build the audio transport: LiveKit room if enabled, else local mic+speaker.

    LiveKit mode requires `livekitUrl`, `livekitApiKey`, `livekitApiSecret`, and
    `livekitRoomName` in config. When `useLiveKitTransport` is unset/false, the
    daemon falls back to LocalAudioTransport (current default behaviour).

    Honors ``micInputDevice`` (v0.1.2): if set, the named PyAudio device is
    resolved to an index and passed to ``LocalAudioTransportParams``. Unmatched
    names silently fall back to the system default.
    """
    from config import get_mic_device

    use_livekit = bool(config.get("useLiveKitTransport"))
    if not use_livekit:
        mic_device_index = _resolve_input_device_index(get_mic_device(config))
        logger.info(
            "Audio transport: LocalAudioTransport (Mac mic + speaker, input_device_index=%s)",
            mic_device_index,
        )
        return LocalAudioTransport(
            LocalAudioTransportParams(
                audio_in_enabled=True,
                audio_out_enabled=True,
                input_device_index=mic_device_index,
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
        mic_device_index = _resolve_input_device_index(get_mic_device(config))
        return LocalAudioTransport(
            LocalAudioTransportParams(
                audio_in_enabled=True,
                audio_out_enabled=True,
                input_device_index=mic_device_index,
            )
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


# ---------------------------------------------------------------------------
# v0.1.2 — STT / TTS service factories driven by config
# ---------------------------------------------------------------------------
# These helpers translate the high-level user choice (``ttsProvider``,
# ``sttModel``, ``voicePreset``) into concrete service instances. They each
# return a ``(service, choice_label)`` tuple so ``create_pipeline_components``
# can log the resolved choice in its single-line startup summary.

# Map the user-facing sttModel value to the ``LocalWhisperSTT(model_name=...)``
# argument. Keys are exactly the strings the frontend dropdown writes to
# config; values are the short names ``_resolve_whisper_repo`` already
# understands (``small.en`` -> ``mlx-community/whisper-small.en-mlx`` etc.).
_STT_MODEL_TO_WHISPER_NAME: dict[str, str] = {
    "whisper-small.en": "small.en",
    "whisper-tiny.en": "tiny.en",
    # The ``faster-whisper`` value selects the cpu fallback path inside
    # LocalWhisperSTT. The class loads faster-whisper with this same
    # short-name string, so default to small.en sizing.
    "faster-whisper": "small.en",
}


def _build_stt_service(
    config: dict[str, Any],
) -> tuple[FrameProcessor, str]:
    """Build the STT service based on ``sttModel`` in config.

    Returns ``(stt_service, resolved_choice)`` where ``resolved_choice`` is
    the value to log in the v0.1.2 startup summary line.
    """
    from config import get_stt_model

    choice = get_stt_model(config)
    model_name = _STT_MODEL_TO_WHISPER_NAME.get(choice, "small.en")
    stt = LocalWhisperSTT(model_name=model_name)
    logger.info(
        "STT: LocalWhisperSTT (choice=%s, model_name=%s)", choice, model_name
    )
    return stt, choice


def _build_tts_service(
    config: dict[str, Any],
) -> tuple[FrameProcessor | None, str, str | None]:
    """Build the TTS service based on ``ttsProvider`` / ``voicePreset``.

    Returns ``(tts_service, resolved_provider, resolved_voice)``. The
    resolution rules implement the v0.1.2 contract:

    * If ``ttsProvider`` is unset (empty / missing) we keep the legacy
      auto-fallback chain (VibeVoice -> Kokoro -> Cartesia).
    * If the user picked a specific provider but its precondition isn't
      met (missing dependency, missing API key), we log a clear warning
      and fall back to the first bundled option that works — never crash.
    * Cartesia without ``cartesiaAPIKey`` -> warn + fall back to vibevoice
      / kokoro (NOT silently to a different cloud provider).
    """
    from config import get_cartesia_api_key, get_tts_provider, get_voice_preset

    # Was the provider explicitly set by the user? (Used to distinguish
    # "user picked vibevoice" from "user didn't pick anything, run the
    # legacy auto chain".)
    raw = config.get("ttsProvider")
    explicit = isinstance(raw, str) and raw.strip() != ""
    provider = get_tts_provider(config) if explicit else ""

    cartesia_key = get_cartesia_api_key(config)
    # Legacy free-form keys used by older configs still work alongside the
    # new ``voicePreset`` slot. ``voicePreset`` (v0.1.2) wins when both set.
    legacy_cartesia_voice = (config.get("cartesiaVoiceId") or "").strip()
    legacy_kokoro_voice = (config.get("kokoroVoice") or "").strip() or "af_sarah"
    legacy_kokoro_speed = float(config.get("kokoroSpeed", 1.0))
    legacy_kokoro_lang = (config.get("kokoroLang") or "").strip() or "en-us"
    legacy_vibe_voice = (config.get("vibevoiceVoice") or "").strip() or "en-Carter_man"

    explicit_voice = get_voice_preset(config)  # None when user hasn't set it

    def _try_vibevoice() -> FrameProcessor | None:
        try:
            import vibevoice  # noqa: F401
        except ImportError:
            return None
        voice = explicit_voice or legacy_vibe_voice
        svc = VibeVoiceTTSService(voice=voice)
        logger.info(
            "TTS: VibeVoiceTTSService (local, free, ~300ms TTFB, voice=%s)", voice
        )
        return svc

    def _try_kokoro() -> FrameProcessor | None:
        try:
            import kokoro_onnx  # noqa: F401
        except ImportError:
            return None
        voice = explicit_voice or legacy_kokoro_voice
        svc = KokoroTTSService(
            voice=voice, speed=legacy_kokoro_speed, lang=legacy_kokoro_lang
        )
        logger.info("TTS: KokoroTTSService (local, free, voice=%s)", voice)
        return svc

    def _try_cartesia() -> FrameProcessor | None:
        if not cartesia_key:
            return None
        voice = explicit_voice or legacy_cartesia_voice or (
            "1463a4e1-56a1-4b41-b257-728d56e93605"
        )
        svc = CartesiaTTSService(api_key=cartesia_key, voice_id=voice)
        logger.info("TTS: CartesiaTTSService (Sonic 3, voice=%s)", voice)
        return svc

    # --- Explicit provider path: user picked one in settings ----------
    if explicit:
        if provider == "cartesia":
            svc = _try_cartesia()
            if svc is not None:
                return svc, "cartesia", explicit_voice
            logger.error(
                "ttsProvider=cartesia but cartesiaAPIKey is missing — "
                "falling back to local TTS"
            )
        elif provider == "kokoro":
            svc = _try_kokoro()
            if svc is not None:
                return svc, "kokoro", explicit_voice
            logger.error(
                "ttsProvider=kokoro but kokoro_onnx not installed — "
                "falling back to alternative local TTS"
            )
        elif provider == "vibevoice":
            svc = _try_vibevoice()
            if svc is not None:
                return svc, "vibevoice", explicit_voice
            logger.error(
                "ttsProvider=vibevoice but vibevoice not installed — "
                "falling back to alternative local TTS"
            )

        # Explicit choice failed — try the remaining locals in priority order.
        for name, fn in (
            ("vibevoice", _try_vibevoice),
            ("kokoro", _try_kokoro),
            ("cartesia", _try_cartesia),
        ):
            if name == provider:
                continue
            svc = fn()
            if svc is not None:
                logger.warning(
                    "TTS: requested provider=%s unavailable, using %s instead",
                    provider, name,
                )
                return svc, name, explicit_voice

        raise RuntimeError(
            f"No TTS provider available: requested={provider!r}, "
            "no bundled fallback works. Install vibevoice or kokoro_onnx, "
            "or set cartesiaAPIKey."
        )

    # --- Auto path (no explicit ttsProvider) — preserve v0.1.1 chain --
    for name, fn in (
        ("vibevoice", _try_vibevoice),
        ("kokoro", _try_kokoro),
        ("cartesia", _try_cartesia),
    ):
        svc = fn()
        if svc is not None:
            return svc, name, explicit_voice

    raise RuntimeError(
        "No TTS provider available: install vibevoice or kokoro_onnx, "
        "or set cartesiaAPIKey in config."
    )


def _export_api_keys_to_env(config: dict[str, Any]) -> None:
    """Mirror v0.1.2 API keys from config to the conventional env vars.

    Pipecat's Anthropic / OpenAI / Google clients pick up keys from env
    variables by default. We only set the variable when the corresponding
    config key is populated AND the variable isn't already set in the
    process environment — this lets ``ANTHROPIC_API_KEY=... python main.py``
    overrides keep working for power users.
    """
    from config import get_anthropic_api_key, get_google_api_key

    import os

    google_key = get_google_api_key(config)
    if google_key and not os.environ.get("GOOGLE_API_KEY"):
        os.environ["GOOGLE_API_KEY"] = google_key
        logger.debug("Exported googleAPIKey -> GOOGLE_API_KEY env var")

    anthropic_key = get_anthropic_api_key(config)
    if anthropic_key and not os.environ.get("ANTHROPIC_API_KEY"):
        os.environ["ANTHROPIC_API_KEY"] = anthropic_key
        logger.debug("Exported anthropicAPIKey -> ANTHROPIC_API_KEY env var")


def create_pipeline_components(
    config: dict[str, Any],
    ws: ClientConnection,
    memory: ConversationMemory,
    vmem: VectorMemory | None = None,
) -> tuple[Pipeline, PipelineTask, LocalAudioTransport, WSBridgeProcessor, LLMContext, AnthropicLLMService, FrameProcessor | None, "WakeWordGate | None", FrameProcessor | None]:
    """Build the Pipecat voice pipeline and return its components.

    Returns:
        (pipeline, task, transport, ws_bridge, llm_context, llm_service,
         stt, wake_gate, tts)

    ``wake_gate`` is ``None`` when ``wakeWordEnabled=false`` in config — the
    gate is not inserted into the pipeline at all in that case.
    """
    # --- v0.1.2 API key wiring (must happen BEFORE LLM/TTS construction so ---
    # ---             SDK clients reading from env see the right values)   ---
    _export_api_keys_to_env(config)

    # --- Audio transport: LiveKit room (mobile-equal) or local mic+speaker ---
    transport = _create_audio_transport(config)

    # --- STT service (v0.1.2: honor sttModel from config) ---
    # ``LocalWhisperSTT`` accepts a Whisper variant short name (small.en /
    # tiny.en) and routes faster-whisper internally.
    stt, stt_choice = _build_stt_service(config)

    # --- TTS service (v0.1.2: honor ttsProvider from config) ---
    mac_tts, tts_choice, voice_choice = _build_tts_service(config)

    # --- Path A (v0.3.0): Friday voice for mobile turns -----------------
    # Wrap the Mac-side TTS (Jarvis voice -- VibeVoice male preset) and a
    # second VibeVoice instance for Friday with a female preset.  Both
    # speak through the same neural engine so the two voices sound equally
    # natural.  The RouterTTSService picks per-turn based on
    # ``active_client.get_active()``.
    #
    # ``fridayVoice`` config key:
    #   * VibeVoice preset name (e.g. ``en-Alice_woman``) -- routes to a
    #     VibeVoiceTTSService instance (natural neural voice). Default.
    #   * Bare voice name like ``Fiona`` / ``Samantha`` -- routes to the
    #     legacy MacOSSayTTSService (robotic but instantaneous).  Detected
    #     by the absence of an ``en-`` prefix.
    #
    # ``mac_tts`` may be None on bootstrap (no provider available) -- in
    # that case we skip the router entirely so the legacy "no TTS" path
    # keeps working.
    tts: FrameProcessor | None
    if mac_tts is not None:
        friday_voice = (config.get("fridayVoice") or "").strip() or "en-Emma_woman"
        # Heuristic: VibeVoice presets are namespaced ``en-Name_gender``;
        # macOS say voices are bare proper names ("Fiona", "Samantha").
        if friday_voice.startswith("en-") and "_" in friday_voice:
            mobile_tts_svc = VibeVoiceTTSService(voice=friday_voice)
            mobile_provider_label = "VibeVoiceTTSService"
        else:
            mobile_tts_svc = MacOSSayTTSService(voice=friday_voice)
            mobile_provider_label = "MacOSSayTTSService"
        logger.info(
            "TTS router: mac=%s (%s) / mobile=%s (voice=%s)",
            type(mac_tts).__name__, voice_choice or "<default>",
            mobile_provider_label, friday_voice,
        )
        tts = RouterTTSService(mac_tts=mac_tts, mobile_tts=mobile_tts_svc)
    else:
        tts = None

    # --- v0.1.5: explicit user pick from the Connections panel LLM dropdown ---
    # If ``cfg.llmModel`` is set to one of the four supported values, that is
    # the source of truth and we skip the legacy key-driven detection below.
    # Anything unsupported / missing returns None and we fall through to the
    # legacy chain (preserving v0.1.0--v0.1.4 behaviour for users who haven't
    # touched the dropdown).
    user_picked_llm = build_user_picked_llm(
        config,
        system_instruction=JARVIS_SYSTEM_FULL,
        anthropic_service_cls=AnthropicLLMService,
        chain_state=_llm_chain_state,
    )

    # --- LLM provider chain: OpenRouter → Google AI Studio → Ollama (with runtime failover) ---
    # Anthropic-direct (sk-ant-) takes a separate path because its SDK isn't OpenAI-compatible.
    # `jarvis_key` reads jarvisAPIKey first, dexAPIKey fallback; reading the
    # legacy key directly was a bug — a fresh key set via Settings never
    # reached the daemon's LLM selector.
    jarvis_key = get_api_key(config).strip()
    google_key = (config.get("googleAPIKey") or "").strip()
    nvidia_key = (config.get("nvidiaAPIKey") or "").strip()
    use_anthropic_direct = (
        jarvis_key.startswith("sk-ant-")
        and not jarvis_key.startswith("sk-or-")
        and not google_key
        and not nvidia_key
    )

    # v0.1.5 pipeline-status: track the resolved provider / model / source
    # for the HUD pipeline indicator. Every branch below populates these
    # three locals before the post-build emit reads them.
    llm_provider_label: str
    llm_model_label: str
    llm_source_label: str

    if user_picked_llm is not None:
        # The user's explicit dropdown choice short-circuits the chain.
        # ``_build_user_picked_llm`` has already logged the chosen provider
        # at INFO and reset ``_llm_chain_state``.
        llm = user_picked_llm
        # ``get_llm_model`` returned a validated pick (the picker would have
        # returned None otherwise), so ``resolve_user_pick_llm`` is safe to
        # unwrap. Fall back to ("unknown", pick) if a future prefix lands
        # in VALID_LLM_MODELS before this resolver learns about it.
        _pick_raw = get_llm_model(config) or ""
        _resolved = resolve_user_pick_llm(_pick_raw)
        if _resolved is not None:
            llm_provider_label, llm_model_label = _resolved
        else:
            llm_provider_label, llm_model_label = "unknown", _pick_raw
        llm_source_label = "user-pick"
    elif use_anthropic_direct:
        from anthropic import AsyncAnthropic
        default_model = "claude-haiku-4-5-20251001"
        llm_model = config.get("dexModel") or default_model
        llm = AnthropicLLMService(
            api_key=jarvis_key,
            client=AsyncAnthropic(api_key=jarvis_key),
            settings=AnthropicLLMService.Settings(
                model=llm_model,
                system_instruction=JARVIS_SYSTEM_FULL,
            ),
        )
        logger.info("LLM: Anthropic direct (%s) — failover chain disabled (incompatible SDK)", llm_model)
        _llm_chain_state["providers"] = []
        _llm_chain_state["service"] = None
        llm_provider_label = "anthropic"
        llm_model_label = llm_model
        llm_source_label = "key-detected"
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
        llm_provider_label = resolve_chain_provider_label(primary["name"])
        llm_model_label = primary["model"]
        llm_source_label = "key-detected"

    # --- Context aggregators (handles conversation history + VAD) ---
    from pipecat.turns.user_mute.always_user_mute_strategy import AlwaysUserMuteStrategy
    from pipecat.turns.user_start.transcription_user_turn_start_strategy import (
        TranscriptionUserTurnStartStrategy,
    )
    from pipecat.turns.user_start.vad_user_turn_start_strategy import (
        VADUserTurnStartStrategy,
    )
    from pipecat.turns.user_turn_strategies import UserTurnStrategies

    context = SanitizedLLMContext()
    user_aggregator, assistant_aggregator = LLMContextAggregatorPair(
        context,
        user_params=LLMUserAggregatorParams(
            vad_analyzer=SileroVADAnalyzer(
                params=VADParams(
                    confidence=0.85,  # Stricter Silero threshold -- reject low-confidence speech ticks
                    start_secs=1.5,   # Require 1.5s of sustained speech (rejects breath, throat noise, tail)
                    stop_secs=0.6,    # 600ms silence before end-of-speech (prevents premature cutoff)
                    min_volume=0.95,  # Very high volume threshold -- only direct speech, not TV/music
                ),
            ),
            # Disable interruption broadcast on user-turn-start. Without AEC,
            # phantom VAD ticks during the LLM->TTS handoff were cancelling the
            # in-flight reply before any audio played. Turn detection still
            # runs (for context aggregation), it just no longer kills the
            # current response. The InterruptionHandler downstream still allows
            # interruptions during actual bot speech via its own logic.
            user_turn_strategies=UserTurnStrategies(
                start=[
                    VADUserTurnStartStrategy(enable_interruptions=False),
                    TranscriptionUserTurnStartStrategy(enable_interruptions=False),
                ],
            ),
            # Mute mic input while bot is speaking to prevent echo feedback.
            # Local audio has no AEC, so bot's TTS output is picked up by the mic.
            user_mute_strategies=[AlwaysUserMuteStrategy()],
        ),
    )

    # --- WS bridge processor (forwards events to Go app) ---
    ws_bridge = WSBridgeProcessor(ws=ws, memory=memory, vmem=vmem)
    response_flush = ResponseFlushProcessor(bridge=ws_bridge)

    # --- Wake word gate (v0.1.2: optional, gated by wakeWordEnabled) ---
    # When wakeWordEnabled=False, we skip the gate entirely so the mic feeds
    # STT directly (always-listening mode). When True (default), we still
    # insert it disarmed — same legacy behaviour as v0.1.1 where downstream
    # callers (e.g. an explicit "hey jarvis" trigger) can arm it later.
    from config import get_wake_word_enabled
    wake_enabled = get_wake_word_enabled(config)
    wake_gate: WakeWordGate | None
    if wake_enabled:
        wake_gate = WakeWordGate()  # Starts disarmed (always-listening)
    else:
        wake_gate = None
        logger.info("Wake word gating disabled (wakeWordEnabled=false) — mic feeds STT directly")

    # InterruptionHandler also re-arms the wake gate when the bot finishes a
    # reply, so the post-detection open window can't keep capturing ambient
    # noise after the conversation turn ends. Pass ``None`` when the gate is
    # disabled and the handler will simply skip re-arming.
    interruption_handler = InterruptionHandler(ws_bridge=ws_bridge, wake_gate=wake_gate)

    # --- Assemble pipeline ---
    # The pipeline order:
    #   mic -> [wake_gate] -> [STT] -> ws_bridge -> user_aggregator
    #   -> LLM -> [RouterTTS] -> interruption_handler
    #   -> [ClientAwareTransportRouter] -> speaker
    #   -> assistant_aggregator -> response_flush
    #
    # The ClientAwareTransportRouter (v0.3.0/Path A) gates TTSAudioRawFrame
    # so the Mac speaker stays silent during mobile turns -- Friday's voice
    # is still synthesised but only streamed to the phone via the TTS
    # service's _mobile_tts_fn callback.
    #
    # Context sanitization happens inside SanitizedLLMContext.get_messages()
    # which splits mixed tool_result/text user messages before the LLM reads them.
    stages: list[Any] = [transport.input()]
    if wake_gate is not None:
        stages.append(wake_gate)

    if stt is not None:
        stages.append(stt)

    stages.append(ws_bridge)
    stages.append(user_aggregator)
    stages.append(llm)

    if tts is not None:
        stages.append(tts)

    stages.append(interruption_handler)
    stages.append(ClientAwareTransportRouter())
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

    # --- v0.1.2 startup summary: one-liner with the resolved voice config ---
    logger.info(
        "voice config: tts=%s stt=%s voice=%s wake_word=%s",
        tts_choice,
        stt_choice,
        voice_choice or "<provider default>",
        wake_enabled,
    )

    # --- v0.1.5 pipeline-status: emit a single event so the HUD pipeline
    # --- indicator can render the resolved TTS / STT / LLM choices without
    # --- polling. The payload reflects POST-FALLBACK reality (e.g. user
    # --- picked cartesia but the daemon fell back to vibevoice), so it can
    # --- differ from the raw ``cfg.*`` values. We cache the payload + the
    # --- ws ref so a late-mounting client can request a replay via
    # --- ``request_pipeline_status`` without forcing a daemon restart.
    global _last_pipeline_status, _pipeline_status_ws
    wake_sensitivity = config.get("jarvisWakeSensitivity", 0.5)
    try:
        wake_sensitivity = float(wake_sensitivity)
    except (TypeError, ValueError):
        wake_sensitivity = 0.5
    pipeline_status_payload = build_pipeline_status(
        tts_provider=tts_choice,
        tts_voice=voice_choice,
        stt_model=stt_choice,
        llm_provider=llm_provider_label,
        llm_model=llm_model_label,
        llm_source=llm_source_label,
        wake_word_enabled=wake_enabled,
        wake_word_sensitivity=wake_sensitivity,
    )
    _last_pipeline_status = pipeline_status_payload
    _pipeline_status_ws = ws
    try:
        # The pattern for daemon -> Go events is ``await ws.send(json.dumps(...))``;
        # see ``send_state`` / ``send_transcript`` / ``send_response`` upthread.
        # We schedule the send instead of awaiting because callers of this
        # function aren't all in the right phase to await (and the post-build
        # ordering doesn't matter — the HUD just needs the payload eventually).
        asyncio.create_task(
            ws.send(json.dumps(pipeline_status_payload)),
            name="emit-pipeline-status",
        )
    except Exception:
        logger.warning("Failed to emit pipeline_status", exc_info=True)

    # Path A (v0.3.0): expose the LLM handle so the mobile_active control
    # frame handler can refresh the persona overlay immediately, without
    # waiting for the context enricher's 5-second tick.
    global _llm_service_handle
    _llm_service_handle = llm

    # v0.3.0 / TASK-006: expose the pipeline task so the PTT control-
    # frame handlers can inject UserStartedSpeakingFrame /
    # UserStoppedSpeakingFrame directly into the running pipeline.  This
    # lets the overlay hotkey reuse the existing LLM dispatch path
    # instead of forking it.  See ``_handle_ptt_active`` /
    # ``_handle_ptt_release``.
    global _pipeline_task_handle
    _pipeline_task_handle = task

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
            #
            # Path A (v0.3.0): pass the current active_client so the Friday
            # persona addendum is prepended when the phone is the active
            # interlocutor.  Mac turns leave the prompt unmodified.
            current_active = active_client.get_active()
            update_system_instruction(
                llm,
                enriched,
                recalled_memories=recalled,
                active_client_value=current_active,
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

    # Register the WS sink for model_status before any prefetch / lazy-load
    # runs so download-progress events have somewhere to go. The loop ref is
    # what ProgressTqdm (running inside snapshot_download's worker thread)
    # uses to schedule emits back onto the event loop.
    model_status.set_event_sink(_ws_send, asyncio.get_running_loop())

    # Kick off the model prefetch in the background. On a fresh DMG install
    # this downloads ~2.4 GB while the user sees the first-run overlay; on
    # subsequent launches it's a cache-hit no-op that emits model_setup
    # state=ready immediately so the HUD knows to skip the overlay.
    asyncio.create_task(
        model_status.prefetch_models(config),
        name="model-prefetch",
    )

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
    # Same hasattr-trap as _mobile_tts_fn below: the RouterTTSService doesn't
    # pre-init this attribute, so guarding on hasattr would silently skip the
    # wiring and the orb would never animate. The Router's __setattr__ fans
    # the assignment out to inner services that do declare it.
    if tts is not None:
        async def _send_audio_level(level: float) -> None:
            await send_audio_level(ws, level)
        tts._audio_send_fn = _send_audio_level
        logger.info("TTS audio level events wired to WS for orb animation")

    # --- Wire TTS audio chunks to mobile clients ---
    # Path A (v0.3.0): suppress the mobile broadcast on Mac-active turns so
    # Friday's voice doesn't leak onto the phone when Jarvis is replying via
    # the Mac speaker.  When mobile is the active interlocutor the broadcast
    # passes through normally so the phone receives Serena's audio.
    #
    # Note: we don't gate on ``hasattr(tts, '_mobile_tts_fn')`` because the
    # RouterTTSService doesn't pre-initialise that attribute -- the inner
    # MacOSSayTTSService does (set to None in its __init__).  The Router's
    # __setattr__ fans the assignment out to both inner services, so blindly
    # setting it here is safe and is the only way the inner Friday provider
    # ever gets a non-None callback.
    if tts is not None:
        # Pull the rate from the mobile-side TTS service. The router exposes
        # the mobile-leg processor via ``.mobile_tts``; fall back to a sane
        # 16k default if the router shape ever changes.
        mobile_tts_sr = 16000
        try:
            inner = getattr(tts, "mobile_tts", None)
            inner_sr = getattr(inner, "_sample_rate", None)
            if isinstance(inner_sr, int) and inner_sr > 0:
                mobile_tts_sr = inner_sr
        except Exception:
            logger.debug(
                "TTS mobile SR introspection failed -- defaulting to 16kHz",
                exc_info=True,
            )

        _mobile_tts_diag = {"chunks": 0, "bytes": 0, "skipped_mac": 0}

        async def _send_mobile_tts_chunk(pcm_chunk: bytes) -> None:
            active = active_client.get_active()
            if active == "mac":
                _mobile_tts_diag["skipped_mac"] += 1
                if _mobile_tts_diag["skipped_mac"] in (1, 10, 100):
                    logger.info(
                        "Mobile TTS skipped (active=mac, count=%d)",
                        _mobile_tts_diag["skipped_mac"],
                    )
                return
            _mobile_tts_diag["chunks"] += 1
            _mobile_tts_diag["bytes"] += len(pcm_chunk)
            if _mobile_tts_diag["chunks"] in (1, 5, 25, 50):
                logger.info(
                    "Mobile TTS chunk sent (#%d, %d bytes, total=%d bytes, active=%s)",
                    _mobile_tts_diag["chunks"], len(pcm_chunk),
                    _mobile_tts_diag["bytes"], active,
                )
            await send_mobile_tts(ws, pcm_chunk, sample_rate=mobile_tts_sr)
        tts._mobile_tts_fn = _send_mobile_tts_chunk
        logger.info(
            "TTS mobile audio forwarding wired to WS (gated by active_client, sr=%d)",
            mobile_tts_sr,
        )

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

    # Register all tool handlers (Go + MCP + local + executor) with the Pipecat LLM.
    # _tool_executor handles spotify_*/mac_* tools declared in tools.py.
    tool_bridge.register_with_pipecat(llm_service, tool_executor=_tool_executor)

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

        # Path A (v0.3.0): if the first interlocutor turned out to be the
        # phone (mobile audio arrived during pipeline boot and flipped
        # active_client to "mobile"), introduce Friday by name and the
        # RouterTTSService will route this through ``say -v Serena``.
        if active_client.get_active() == "mobile":
            greeting = f"{greeting} Friday here."

        logger.info("Auto-greeting: %s", greeting)
        await task.queue_frames([TTSSpeakFrame(text=greeting)])
        await send_response(ws, greeting)
    else:
        logger.info("Reconnected — skipping greeting")

    logger.info("Pipecat voice pipeline ready -- all subsystems online")

    # --- Command loop: process HUD text commands ---
    async def _command_loop() -> None:
        # v0.3.0 / TASK-006: declare all meeting-mode module-level state
        # rebinding up-front. Python's ``global`` must precede any
        # assignment to the named variables in the function body, and
        # multiple branches below assign overlapping subsets -- one
        # function-level declaration covers them all without the
        # SyntaxError that nested ``global`` blocks would trigger.
        global _MEETING_ACTIVE  # noqa: PLW0603
        global _MEETING_TITLE  # noqa: PLW0603
        global _MEETING_STARTED_AT  # noqa: PLW0603
        global _SUPPRESS_LLM_TURN  # noqa: PLW0603
        global _MEETING_BUFFER_CHARS  # noqa: PLW0603
        global _PRE_MEETING_STATE  # noqa: PLW0603

        while True:
            item = await _command_queue.get()

            # v0.3.0 / TASK-006: meeting commands ride in as a dict so
            # ``title`` can travel alongside ``text``. Plain mute /
            # unmute / interrupt commands still arrive as a bare string
            # (legacy producer path). Normalise both shapes here so the
            # rest of the loop is shape-agnostic.
            if isinstance(item, dict):
                data: dict[str, Any] | None = item
                text = str(item.get("text", "")).strip()
            else:
                data = None
                text = str(item)

            logger.info("Processing HUD command: %s", text)

            # Handle mute/unmute — three layers:
            # (1) wake_gate.armed blocks audio before STT (wake word required)
            # (2) STT echo-suppression flag stops transcription
            # (3) ws_bridge.muted drops any transcripts that slip through
            if text == "__mute__":
                ws_bridge.muted = True
                if wake_gate is not None:
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
                if wake_gate is not None:
                    wake_gate.armed = False  # Disable wake word, always listen
                stt.force_muted = False
                stt._bot_speaking = False
                stt._buffer.clear()
                stt._buffer_samples = 0
                logger.info("Mic unmuted — always listening")
                await send_state(ws, "idle")
                continue

            # v0.3.0 overlay (TASK-010 follow-up): explicit interrupt from the
            # overlay's Stop button. Reuse the same machinery the speech-based
            # interruption path uses by signalling a brief user-turn so the
            # InterruptionHandler cancels in-flight TTS. We bracket the user
            # turn with start+stop frames immediately so the LLM doesn't try
            # to generate a response to "nothing".
            if text == "__interrupt__":
                logger.info("HUD interrupt — cancelling in-flight TTS")
                try:
                    await task.queue_frames([
                        UserStartedSpeakingFrame(),
                        UserStoppedSpeakingFrame(),
                    ])
                except Exception as exc:  # noqa: BLE001 — defensive log
                    logger.warning("__interrupt__: frame queue failed: %r", exc)
                continue

            # v0.3.0 / TASK-006: meeting-mode lifecycle commands. Mirror
            # the __mute__ / __unmute__ pattern -- inline blocks that
            # mutate module-level + pipeline-component state and emit a
            # ``state`` event to the HUD. The summarisation +
            # spoken-recap pipeline is owned by TASK-007 / TASK-008 and
            # lands via the ``_dispatch_meeting_finalisation`` hook.
            if text == "__meeting_start__":
                # ``global`` declarations live at the top of
                # ``_command_loop`` -- Python compiles them function-wide,
                # so a per-branch redeclaration after an earlier
                # assignment in the function would raise SyntaxError.

                # Idempotent: double-start logs WARNING and is otherwise a
                # no-op so a flaky frontend can't reset state mid-meeting
                # and lose the buffer.
                if _MEETING_ACTIVE:
                    logger.warning(
                        "__meeting_start__ received while already active "
                        "-- ignoring"
                    )
                    continue

                # Title arrives via the outer command payload (the
                # dispatcher hands us the parsed dict). Empty / missing
                # falls back to "untitled" so downstream slugify in
                # TASK-007 never sees a None.
                title = (
                    data.get("title") if isinstance(data, dict) else None
                ) or "untitled"

                _MEETING_ACTIVE = True
                _MEETING_TITLE = title
                _MEETING_STARTED_AT = time.monotonic()
                _MEETING_BUFFER.clear()
                _MEETING_BUFFER_CHARS = 0
                _SUPPRESS_LLM_TURN = True

                # Stash pre-meeting gate state so __meeting_stop__ can
                # restore it. This handles the case where the user had
                # muted Jarvis before starting the meeting -- we open
                # the gates during the meeting, then return them to
                # their prior values on stop.
                _PRE_MEETING_STATE = {
                    "stt_force_muted": stt.force_muted,
                    "wake_gate_armed": (
                        wake_gate.armed if wake_gate is not None else False
                    ),
                    "ws_bridge_muted": ws_bridge.muted,
                    "router_tts_muted": getattr(tts, "meeting_muted", False),
                }

                # Force gates open so we capture continuously; suppress
                # TTS so Jarvis stays quiet during the meeting.
                stt.force_muted = False
                if wake_gate is not None:
                    wake_gate.armed = False
                ws_bridge.muted = False
                if tts is not None:
                    tts.meeting_muted = True

                logger.info(
                    "Meeting started: title=%r at t=%.2f",
                    title,
                    _MEETING_STARTED_AT,
                )
                await send_state(ws, "meeting_active")
                continue

            if text == "__meeting_stop__":
                # ``global`` declarations are at the top of
                # ``_command_loop`` (see __meeting_start__ for context).
                if not _MEETING_ACTIVE:
                    logger.warning(
                        "__meeting_stop__ received with no active meeting "
                        "-- ignoring"
                    )
                    # Echo a state event so a frontend that thinks it's
                    # recording gets corrected.
                    await send_state(ws, "idle")
                    continue

                # Snapshot the buffer for the summary task BEFORE
                # clearing state. TASK-007's generate_meeting_notes is
                # async and we don't await it inline -- schedule it as
                # a background task and acknowledge the stop event
                # immediately so the UI doesn't hang on a slow LLM call.
                buffer_snapshot = list(_MEETING_BUFFER)
                title_snapshot = _MEETING_TITLE or "untitled"

                # Restore stashed gates. If _PRE_MEETING_STATE is None
                # (shouldn't happen given the idempotency check above,
                # but defensively):
                if _PRE_MEETING_STATE is not None:
                    stt.force_muted = _PRE_MEETING_STATE["stt_force_muted"]
                    if wake_gate is not None:
                        wake_gate.armed = _PRE_MEETING_STATE["wake_gate_armed"]
                    ws_bridge.muted = _PRE_MEETING_STATE["ws_bridge_muted"]
                    if tts is not None:
                        tts.meeting_muted = _PRE_MEETING_STATE[
                            "router_tts_muted"
                        ]
                    _PRE_MEETING_STATE = None

                _MEETING_ACTIVE = False
                _MEETING_TITLE = None
                _MEETING_STARTED_AT = None
                _SUPPRESS_LLM_TURN = False
                # TASK-008 reads from the snapshot, not the live buffer
                _MEETING_BUFFER.clear()

                logger.info(
                    "Meeting stopped: title=%r, buffer_entries=%d",
                    title_snapshot,
                    len(buffer_snapshot),
                )
                await send_state(ws, "idle")

                # TASK-007/TASK-008 hook: schedule background summary +
                # recap. For TASK-006 this is a no-op pass-through.
                asyncio.create_task(
                    _dispatch_meeting_finalisation(
                        title_snapshot, buffer_snapshot, ws
                    ),
                    name="meeting-finalisation",
                )
                continue

            if text == "__meeting_recap__":
                # TASK-008: replay the cached recap (set by
                # ``_dispatch_meeting_finalisation`` after the most
                # recent meeting ended) via the same TTS-injection
                # helper used on initial speak. Reading
                # ``_LAST_MEETING_RECAP`` does not require a ``global``
                # declaration -- Python only needs ``global`` for
                # assignment. ``None`` means no meeting has finalised
                # this session, in which case the command is a debug
                # no-op rather than an error (the user may have hit the
                # "wrap up" affordance speculatively).
                if _LAST_MEETING_RECAP:
                    logger.info(
                        "HUD recap replay: %d chars",
                        len(_LAST_MEETING_RECAP),
                    )
                    await _speak_meeting_recap(_LAST_MEETING_RECAP)
                else:
                    logger.debug(
                        "__meeting_recap__ received with no cached recap "
                        "-- no-op"
                    )
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

    # --- Mobile audio loop: transcode + inject mobile PCM into STT pipeline ---
    async def _mobile_audio_loop() -> None:
        """Drain encoded mobile audio from the queue, transcribe, inject to LLM.

        Mobile sends one CAF (iOS) / M4A (Android) blob per push-to-talk
        release. We ffmpeg-transcode to 16kHz mono s16le PCM, then run STT
        synchronously and inject the resulting transcript directly into the
        LLM context via ``LLMMessagesAppendFrame`` + ``LLMRunFrame`` -- the
        same path the HUD text-command flow uses.

        Why this bypasses the normal STT->VAD->TurnDetector chain:
        push-to-talk is a one-shot utterance with no continuous audio after
        release. The pipeline's turn-stop strategy is a SmartTurn analyzer
        that needs streaming frames to detect end-of-turn. We already know
        the turn ended (the phone released the button), so it's simpler and
        deterministic to drive the LLM directly.
        """
        import numpy as np

        while True:
            try:
                encoded = await _mobile_audio_queue.get()
                logger.info("Mobile audio loop: dequeued %d encoded bytes", len(encoded))
                pcm_bytes = await _ffmpeg_decode_to_pcm16le(encoded)
                if not pcm_bytes:
                    logger.warning(
                        "Mobile audio: ffmpeg transcode produced no PCM, dropping "
                        "(%d encoded bytes in)", len(encoded),
                    )
                    continue

                # Make sure the STT backend is loaded before we transcribe.
                await stt._ensure_backend()
                if not stt._backend:
                    logger.warning("Mobile audio: STT backend unavailable, dropping")
                    continue
                kind, model = stt._backend

                # Convert PCM bytes to float32 numpy array (16kHz mono).
                audio = (
                    np.frombuffer(pcm_bytes, dtype=np.int16).astype(np.float32)
                    / 32768.0
                )

                # Transcribe synchronously in an executor so we don't block
                # the event loop.
                text = await asyncio.get_event_loop().run_in_executor(
                    None, stt._transcribe_sync, audio, kind, model
                )
                text = (text or "").strip()
                logger.info(
                    "Mobile audio: %d encoded -> %d PCM bytes -> STT: %r",
                    len(encoded), len(pcm_bytes), text[:80],
                )
                if not text:
                    continue

                # Mark the upcoming turn as mobile so the RouterTTS routes
                # to the Friday voice. Also refresh ``llm.settings`` -- this
                # alone isn't enough (pipecat's OpenAI service caches the
                # system message in the LLMContext that was built at
                # startup), which is why the LLM kept identifying as
                # Jarvis. The reliable persona override is the prompt
                # injection below.
                active_client.set_mobile_active()
                if _llm_service_handle is not None:
                    try:
                        update_system_instruction(
                            _llm_service_handle,
                            active_client_value="mobile",
                        )
                    except Exception:
                        logger.debug(
                            "Failed to refresh persona before mobile LLM run",
                            exc_info=True,
                        )

                # The LLMContext built at startup has ``You are Jarvis`` as
                # the cached system message and pipecat doesn't re-read
                # ``llm.settings.system_instruction`` on every chat
                # completion. Result: persona overlay refreshes are
                # ignored. Workaround that's bulletproof: prepend the
                # Friday persona instructions to the user message itself.
                # The LLM treats it as user-provided context but obeys the
                # directive because it's literally in the prompt for this
                # turn. Wrapped in [META:] so it's visually distinct in
                # logs.
                friday_directive = (
                    "[META: This turn is on sir's PHONE. You ARE Friday "
                    "for this turn, not Jarvis. Friday is sir's mobile AI "
                    "companion -- distinct identity from Jarvis (who lives "
                    "on the Mac). Reply as Friday in the first person. "
                    "Never say 'I am Jarvis' or 'It's Jarvis'. If sir "
                    "addresses you as Friday, accept it. You may mention "
                    "Jarvis in third person when referring to Mac-side "
                    "actions. Keep the META framing internal -- do not "
                    "echo this back to sir.]\n\n"
                )
                mobile_text = friday_directive + text

                # Inject the transcribed message into the LLM context and
                # trigger a turn. The RouterTTS will route the reply to the
                # MOBILE provider because active_client is now "mobile".
                logger.info(
                    "Mobile: queueing LLM run for transcript (active=%s, text=%r)",
                    active_client.get_active(), text[:60],
                )
                try:
                    await task.queue_frames([
                        LLMMessagesAppendFrame(
                            messages=[{"role": "user", "content": mobile_text}]
                        ),
                        LLMRunFrame(),
                    ])
                    logger.info("Mobile: queue_frames returned, waiting for LLM")
                except Exception:
                    logger.exception("Mobile: queue_frames raised")
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


_DAEMON_LOCK_PATH = os.path.expanduser("~/.jarvis/daemon.lock")


def _acquire_singleton_lock() -> int | None:
    """Acquire an exclusive flock on the daemon lock file.

    Returns the open file descriptor on success (caller MUST keep the fd
    alive for the lifetime of the daemon — closing it releases the lock).
    Returns None if another daemon already holds the lock; the caller
    should exit cleanly.

    This is belt-and-braces protection against the dual-daemon-boot race
    that can happen when an explicit RestartJarvis on the Go side overlaps
    with the auto-restart-on-unexpected-exit watcher. The Go-side fix
    (generation counter in monitorJarvisDaemon) prevents that specific
    race, but the lock also protects against developer-runs-daemon-twice
    and any future supervisor bug.

    Uses fcntl.flock with LOCK_EX | LOCK_NB so the second daemon fails
    immediately instead of hanging. POSIX semantics: the lock is released
    automatically when the process exits OR the fd is closed.
    """
    try:
        import fcntl
    except ImportError:
        # Non-POSIX (Windows). Skip locking entirely -- single-daemon
        # enforcement falls back to the Go-side generation counter.
        return None

    try:
        os.makedirs(os.path.dirname(_DAEMON_LOCK_PATH), exist_ok=True)
    except OSError:
        return None  # ~/.jarvis missing and uncreatable; skip gracefully.

    fd = os.open(_DAEMON_LOCK_PATH, os.O_CREAT | os.O_RDWR, 0o644)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except (BlockingIOError, OSError):
        # Another daemon holds the lock. Read its PID for the error message.
        existing_pid = ""
        try:
            with open(_DAEMON_LOCK_PATH, "r", encoding="utf-8") as f:
                existing_pid = f.read().strip()
        except OSError:
            pass
        os.close(fd)
        print(
            "[jarvis-daemon] FATAL: another daemon is already running "
            f"(pid={existing_pid or 'unknown'}, lock={_DAEMON_LOCK_PATH}). "
            "Refusing to start a second instance.",
            file=sys.stderr,
        )
        return None

    # Got the lock. Stamp our PID for diagnostics.
    try:
        os.ftruncate(fd, 0)
        os.write(fd, f"{os.getpid()}\n".encode())
    except OSError:
        pass  # Best-effort -- the lock is what matters, not the PID stamp.
    return fd


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

    # Single-instance guard. Exit 0 on collision so the Go supervisor
    # treats it as a graceful no-op and does not kick into restart loop.
    lock_fd = _acquire_singleton_lock()
    if lock_fd is None:
        # Either we collided with a running daemon (message already
        # printed) or we're on a non-POSIX platform. On collision we
        # exit; on non-POSIX we keep going but without the lock guard.
        import platform

        if platform.system() != "Windows":
            sys.exit(0)
    # else: lock_fd stays open in this scope for the lifetime of main().
    # Holding the variable is enough -- Python won't gc the fd while it's
    # referenced. Explicit close happens implicitly on process exit, which
    # releases the lock per POSIX flock semantics.

    try:
        asyncio.run(_async_main(debug=args.debug))
    except KeyboardInterrupt:
        logger.info("Interrupted, shutting down")
    finally:
        # Belt-and-braces: explicitly close the lock fd so the lock is
        # released even if the OS is slow to reap our process.
        if lock_fd is not None:
            try:
                os.close(lock_fd)
            except OSError:
                pass


if __name__ == "__main__":
    main()
