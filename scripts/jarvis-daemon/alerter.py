"""Alert speaker for the Jarvis voice daemon.

When the priority engine scores an event >= 8 (CRITICAL), the alerter speaks
it aloud via TTS and sends it to the HUD.  Alerts respect the voice loop's
conversation state -- they only fire when Jarvis is idle (not mid-conversation).

Rate limiting prevents alert fatigue: at most 3 alerts per rolling 60-second
window.  A bounded async queue decouples event producers (pollers) from TTS
playback so that alert delivery never blocks the callers.

Usage::

    alerter = Alerter(tts=tts_engine, ws_send_fn=ws_send, get_state_fn=get_state)
    await alerter.start()

    # From the priority engine's critical_handler:
    await alerter.alert(event)

    # On shutdown:
    await alerter.stop()
"""

from __future__ import annotations

import asyncio
import logging
import time
from typing import TYPE_CHECKING, Final

if TYPE_CHECKING:
    from collections.abc import Awaitable, Callable
    from typing import Any

    from priority import JarvisEvent
    from tts import TTSEngine

logger: Final = logging.getLogger("jarvis-daemon.alerter")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

MAX_ALERTS_PER_MINUTE: Final[int] = 3
"""Maximum number of alerts that can be spoken in a rolling 60-second window."""

ALERT_COOLDOWN_S: Final[float] = 60.0
"""Rolling window (seconds) for the rate limiter."""

_IDLE_POLL_INTERVAL_S: Final[float] = 1.0
"""How often to poll the voice-loop state while waiting for idle."""

_MAX_IDLE_WAIT_S: Final[float] = 30.0
"""Maximum time (seconds) to wait for Jarvis to become idle before speaking."""

_QUEUE_MAX_SIZE: Final[int] = 20
"""Bounded queue capacity -- oldest alerts are dropped when full."""


class Alerter:
    """Speaks high-priority events via TTS when Jarvis is idle.

    Respects rate limits and conversation state to avoid
    overwhelming the user.

    Parameters
    ----------
    tts:
        ``TTSEngine`` instance for speaking alerts.
    ws_send_fn:
        Async callable that sends a dict message to Go via WebSocket.
    get_state_fn:
        Callable (sync) returning the current Jarvis state string
        (``"idle"``, ``"listening"``, ``"thinking"``, ``"speaking"``).
    """

    def __init__(
        self,
        tts: TTSEngine,
        ws_send_fn: Callable[[dict[str, Any]], Awaitable[None]],
        get_state_fn: Callable[[], str],
    ) -> None:
        self._tts = tts
        self._ws_send = ws_send_fn
        self._get_state = get_state_fn
        self._recent_alerts: list[float] = []  # timestamps of recently spoken alerts
        self._queue: asyncio.Queue[JarvisEvent] = asyncio.Queue(maxsize=_QUEUE_MAX_SIZE)
        self._task: asyncio.Task[None] | None = None

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Start the alert consumer loop as a background task."""
        self._task = asyncio.create_task(
            self._consumer_loop(),
            name="alerter-consumer",
        )
        logger.info("Alerter started")

    async def stop(self) -> None:
        """Cancel the consumer and drain any pending alerts."""
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None
        logger.info("Alerter stopped")

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def alert(self, event: JarvisEvent) -> None:
        """Queue a high-priority event for speaking.

        Non-blocking.  If the queue is full the event is dropped with a
        warning (the priority engine already deduplicates, so dropping the
        occasional overflow is safe).
        """
        try:
            self._queue.put_nowait(event)
        except asyncio.QueueFull:
            logger.warning("Alert queue full, dropping: %s", event.title)

    # ------------------------------------------------------------------
    # Consumer loop
    # ------------------------------------------------------------------

    async def _consumer_loop(self) -> None:
        """Process queued alerts, respecting rate limits and conversation state."""
        while True:
            # Block until an event arrives (with a short timeout so
            # cancellation is responsive).
            try:
                event = await asyncio.wait_for(self._queue.get(), timeout=1.0)
            except asyncio.TimeoutError:
                continue
            except asyncio.CancelledError:
                break

            # --- Rate limit: max N alerts per rolling window ---------------
            now = time.monotonic()
            self._recent_alerts = [
                t for t in self._recent_alerts
                if now - t < ALERT_COOLDOWN_S
            ]
            if len(self._recent_alerts) >= MAX_ALERTS_PER_MINUTE:
                logger.info("Rate limited, skipping alert: %s", event.title)
                continue

            # --- Wait for idle state (don't interrupt mid-conversation) ----
            if not await self._wait_for_idle():
                # Still not idle after max wait -- speak anyway for critical
                # alerts; the user will hear it once TTS finishes its current
                # utterance.
                logger.warning(
                    "Jarvis not idle after %.0fs, speaking alert anyway: %s",
                    _MAX_IDLE_WAIT_S,
                    event.title,
                )

            # --- Speak the alert -------------------------------------------
            alert_text = self._format_alert(event)
            suggestion = self._suggest_action(event)
            if suggestion:
                alert_text += suggestion
            logger.info("Speaking alert: %s", alert_text)

            await self._tts.speak(alert_text)
            self._recent_alerts.append(time.monotonic())

            # --- Forward to HUD --------------------------------------------
            try:
                await self._ws_send({
                    "type": "response",
                    "text": alert_text,
                    "role": "jarvis",
                })
            except Exception:
                logger.exception("Failed to send alert to HUD")

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    async def _wait_for_idle(self) -> bool:
        """Poll the voice-loop state until Jarvis is idle (or timeout).

        Returns ``True`` if idle was reached, ``False`` on timeout.
        """
        checks = int(_MAX_IDLE_WAIT_S / _IDLE_POLL_INTERVAL_S)
        for _ in range(checks):
            state = self._get_state()
            if state in ("idle", ""):
                return True
            await asyncio.sleep(_IDLE_POLL_INTERVAL_S)
        return False

    @staticmethod
    def _format_alert(event: JarvisEvent) -> str:
        """Format an event into natural Jarvis-style speech.

        The phrasing varies by event source and type so that each alert
        sounds natural rather than robotic.
        """
        source = event.source
        etype = event.type
        title = event.title
        detail = event.detail[:100] if event.detail else ""

        if source == "session" and etype == "failed":
            return (
                f"Sir, {title} has failed. {detail}"
                if detail
                else f"Sir, {title} has failed."
            )
        elif source == "session" and etype == "completed":
            return f"{title} has completed, sir."
        elif source == "session" and etype == "needs_input":
            return f"Sir, {title} needs your attention."
        elif source == "session" and etype == "error":
            return (
                f"Sir, an error in {title}. {detail}"
                if detail
                else f"Sir, an error in {title}."
            )
        elif source == "slack" and etype == "dm":
            return (
                f"You have a direct message from {title}, sir. {detail}"
                if detail
                else f"You have a direct message from {title}, sir."
            )
        elif source == "slack" and etype == "mention":
            return f"You were mentioned in {title}, sir."
        elif source == "approval" and etype == "pending":
            return f"New approval waiting in {title}, sir."
        elif source == "research" and etype == "completed":
            return (
                f"Research complete, sir. {detail}"
                if detail
                else f"Research complete, sir."
            )
        elif source == "system" and etype == "error":
            return (
                f"Sir, system alert: {title}. {detail}"
                if detail
                else f"Sir, system alert: {title}."
            )
        else:
            return (
                f"Sir, {title}. {detail}" if detail else f"Sir, {title}."
            )

    @staticmethod
    def _suggest_action(event: JarvisEvent) -> str:
        """Return an actionable suggestion to append to alert text, or empty string.

        Phrased for natural Jarvis-style British TTS delivery.
        """
        detail_lower = event.detail.lower() if event.detail else ""

        if event.type in ("failed", "error"):
            if "type" in detail_lower and "error" in detail_lower:
                return " Want me to read the full output?"
            if "test" in detail_lower and "fail" in detail_lower:
                return " Shall I run the tests again?"
            if "build" in detail_lower:
                return " Want me to check the recent commits?"
            # Generic error fallback
            return " Want me to take a look?"

        if event.type == "stalled":
            return " Shall I check on it?"

        if event.type == "needs_input":
            return " Shall I approve it?"

        if event.source == "correlation" and event.type == "causal":
            return " Want me to check the diff?"

        if event.source == "correlation" and event.type == "systemic":
            return " Shall I read the errors?"

        return ""
