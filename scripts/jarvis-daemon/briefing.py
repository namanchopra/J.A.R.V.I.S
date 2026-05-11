"""Periodic briefing system for the Jarvis voice daemon.

Compiles medium-priority events into natural Jarvis-style spoken summaries
every 15 minutes. The briefing system queries the EventStore for events that
accumulated since the last briefing, groups them by source (sessions, Slack,
research, approvals), and speaks a concise summary via TTS.

Briefings only fire when:
  - At least one reportable event exists (priority >= 5).
  - Jarvis is idle (not mid-conversation).

The ``get_briefing_text()`` method is also available for on-demand briefings
(e.g. "what happened while I was away?").

Usage::

    briefing = BriefingSystem(
        priority_engine=priority,
        tts=tts_engine,
        event_store=event_store,
        ws_send_fn=ws_send,
        get_state_fn=get_state,
    )
    await briefing.start()

    # On-demand:
    text = await briefing.get_briefing_text()

    # Shutdown:
    await briefing.stop()
"""

from __future__ import annotations

import asyncio
import logging
import time
from typing import TYPE_CHECKING, Final

if TYPE_CHECKING:
    from collections.abc import Awaitable, Callable
    from typing import Any

    from events import EventStore
    from priority import PriorityEngine

logger: Final = logging.getLogger("jarvis-daemon.briefing")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

BRIEFING_INTERVAL_S: Final[float] = 15 * 60  # 15 minutes
"""Default interval between periodic briefings (seconds)."""

MIN_EVENTS_FOR_BRIEFING: Final[int] = 1
"""Minimum number of reportable events required to trigger a briefing."""

IMPORTANT_THRESHOLD: Final[int] = 5
"""Events below this priority are excluded from briefings."""


class BriefingSystem:
    """Compiles and speaks periodic briefings from queued events.

    Every 15 minutes, checks for medium-priority events that haven't
    been spoken as alerts.  Compiles a natural summary and speaks it.
    Only speaks if there are events to report AND Jarvis is idle.

    Parameters
    ----------
    priority_engine:
        ``PriorityEngine`` instance for accessing recent events and
        clearing the dedup cache after a briefing is delivered.
    tts:
        ``TTSEngine`` instance for speaking the briefing.
    event_store:
        ``EventStore`` instance for querying persisted events.
    ws_send_fn:
        Async callable that sends a dict message to Go via WebSocket.
    get_state_fn:
        Callable (sync) returning the current Jarvis state string
        (``"idle"``, ``"listening"``, ``"thinking"``, ``"speaking"``).
    interval_s:
        Seconds between periodic briefings.  Defaults to 15 minutes.
    """

    def __init__(
        self,
        priority_engine: PriorityEngine,
        tts: TTSEngine,
        event_store: EventStore,
        ws_send_fn: Callable[[dict[str, Any]], Awaitable[None]],
        get_state_fn: Callable[[], str],
        interval_s: float = BRIEFING_INTERVAL_S,
    ) -> None:
        self._priority = priority_engine
        self._tts = tts
        self._store = event_store
        self._ws_send = ws_send_fn
        self._get_state = get_state_fn
        self._interval = interval_s
        self._task: asyncio.Task[None] | None = None
        self._last_briefing: float = time.time()

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Start the periodic briefing loop as a background task."""
        self._task = asyncio.create_task(
            self._loop(),
            name="briefing-loop",
        )
        logger.info(
            "Briefing system started (interval=%.0fm)", self._interval / 60,
        )

    async def stop(self) -> None:
        """Cancel the briefing loop and wait for it to finish."""
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None
        logger.info("Briefing system stopped")

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def get_briefing_text(self) -> str:
        """Generate a briefing from recent events (for on-demand use too).

        Queries the event store for events since the last briefing,
        filters to priority >= 5 (important/critical), and compiles
        a natural language summary.

        Returns an empty string if there are no reportable events.
        """
        minutes = int((time.time() - self._last_briefing) / 60)
        if minutes < 1:
            minutes = 15

        events = self._store.get_recent(since_minutes=minutes)
        # Filter to medium+ priority only (important and critical).
        events = [
            e for e in events
            if e.get("priority", 0) >= IMPORTANT_THRESHOLD
        ]

        if len(events) < MIN_EVENTS_FOR_BRIEFING:
            return ""

        return self._compile(events, minutes)

    # ------------------------------------------------------------------
    # Periodic loop
    # ------------------------------------------------------------------

    async def _loop(self) -> None:
        """Periodic briefing loop.

        Sleeps for the configured interval, then checks whether Jarvis is
        idle and there are events worth reporting.  If so, speaks the
        briefing and sends it to the HUD.
        """
        while True:
            try:
                await asyncio.sleep(self._interval)

                # Only brief if idle -- don't interrupt a conversation.
                state = self._get_state()
                if state not in ("idle", ""):
                    logger.debug("Skipping briefing -- Jarvis is %s", state)
                    continue

                text = await self.get_briefing_text()
                if not text:
                    logger.debug("No events for briefing")
                    continue

                logger.info("Speaking briefing: %s", text[:80])
                if self._tts is None:
                    logger.debug("No TTS engine; sending briefing to HUD only")
                else:
                    await self._tts.speak(text)

                # Send to HUD for display.
                try:
                    await self._ws_send({
                        "type": "response",
                        "text": text,
                        "role": "jarvis",
                    })
                except Exception:
                    logger.exception("Failed to send briefing to HUD")

                self._last_briefing = time.time()
                self._priority.clear_dedup()

            except asyncio.CancelledError:
                break
            except Exception:
                logger.exception("Briefing loop error")

    # ------------------------------------------------------------------
    # Compilation
    # ------------------------------------------------------------------

    def _compile(self, events: list[dict[str, str]], minutes: int) -> str:
        """Compile events into a natural Jarvis-style spoken summary.

        Groups events by source (sessions, Slack, research, approvals)
        and produces a concise overview suitable for TTS.

        Parameters
        ----------
        events:
            List of event dicts from ``EventStore.get_recent()``.
        minutes:
            How many minutes the briefing window covers (for context).

        Returns an empty string if no parts could be generated.
        """
        parts: list[str] = []

        # Group by source.
        session_events = [e for e in events if e.get("source") == "session"]
        slack_events = [e for e in events if e.get("source") == "slack"]
        research_events = [e for e in events if e.get("source") == "research"]
        approval_events = [e for e in events if e.get("source") == "approval"]

        opener = "Quick update, sir."

        # --- Sessions ---
        completed = [
            e for e in session_events if e.get("type") == "completed"
        ]
        failed = [
            e for e in session_events
            if e.get("type") in ("failed", "error")
        ]
        needs_input = [
            e for e in session_events if e.get("type") == "needs_input"
        ]

        if failed:
            names = ", ".join(e.get("title", "unknown") for e in failed[:3])
            verb = "has" if len(failed) == 1 else "have"
            parts.append(f"{names} {verb} failed")
        if completed:
            names = ", ".join(
                e.get("title", "unknown") for e in completed[:3]
            )
            parts.append(f"{names} completed")
        if needs_input:
            names = ", ".join(
                e.get("title", "unknown") for e in needs_input[:3]
            )
            verb = "needs" if len(needs_input) == 1 else "need"
            parts.append(f"{names} {verb} your attention")

        # --- Slack ---
        dms = [e for e in slack_events if e.get("type") == "dm"]
        mentions = [e for e in slack_events if e.get("type") == "mention"]
        if dms:
            noun = "DM" if len(dms) == 1 else "DMs"
            parts.append(f"{len(dms)} unread Slack {noun}")
        if mentions:
            noun = "mention" if len(mentions) == 1 else "mentions"
            parts.append(f"{len(mentions)} Slack {noun}")

        # --- Research ---
        if research_events:
            noun = "task" if len(research_events) == 1 else "tasks"
            parts.append(f"{len(research_events)} research {noun} completed")

        # --- Approvals ---
        if approval_events:
            noun = "approval" if len(approval_events) == 1 else "approvals"
            parts.append(f"{len(approval_events)} pending {noun}")

        if not parts:
            return ""

        body = ". ".join(parts) + "."
        return f"{opener} {body}"
