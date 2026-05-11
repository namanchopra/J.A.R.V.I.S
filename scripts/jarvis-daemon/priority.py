"""Priority scoring engine for the Jarvis voice daemon.

Background pollers emit events (session state changes, Slack messages, research
results). Each event gets a priority score that decides the action:

  - **Critical (8-10):** Interrupt the user immediately via voice.
  - **Important (5-7):** Queue for the next briefing summary.
  - **Low (1-4):** Log only, no notification.

Dedup prevents the same alert (source+type+title) from firing more than once
within a 60-second cooldown window. The recent-events buffer is capped at 100
entries for briefing queries.
"""

from __future__ import annotations

import logging
import time
from dataclasses import dataclass, field
from typing import Awaitable, Callable, Final

logger: Final = logging.getLogger("jarvis-daemon.priority")

# ---------------------------------------------------------------------------
# Event model
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class JarvisEvent:
    """An event from any data source with a priority score."""

    source: str  # "session", "slack", "research", "approval", "system"
    type: str  # "failed", "completed", "needs_input", "dm", "mention", ...
    title: str  # "auth-service failed"
    detail: str = ""  # Error message, Slack message content, etc.
    priority: int = 5  # 0-10
    timestamp: float = field(default_factory=time.time)


# ---------------------------------------------------------------------------
# Scoring rules
# ---------------------------------------------------------------------------

# Maps (source, type) to a base priority score.
SCORE_TABLE: Final[dict[tuple[str, str], int]] = {
    # Session lifecycle
    ("session", "failed"): 9,
    ("session", "error"): 9,
    ("session", "completed"): 6,
    ("session", "stalled"): 6,
    ("session", "needs_input"): 7,
    ("session", "started"): 3,
    ("session", "working"): 1,
    # Slack
    ("slack", "dm"): 8,
    ("slack", "mention"): 7,
    ("slack", "channel"): 3,
    # Approvals
    ("approval", "pending"): 7,
    # Research
    ("research", "completed"): 6,
    ("research", "failed"): 4,
    # System
    ("system", "error"): 8,
    ("system", "info"): 2,
}

# Priority thresholds
CRITICAL_THRESHOLD: Final[int] = 8  # Interrupt user immediately
IMPORTANT_THRESHOLD: Final[int] = 5  # Queue for briefing
# Below IMPORTANT = just log

# Dedup cooldown in seconds
_DEDUP_COOLDOWN_SECONDS: Final[float] = 60.0

# Max recent events kept in memory
_MAX_RECENT_EVENTS: Final[int] = 100

# Dedup cache cleanup: entries older than this are purged
_DEDUP_EXPIRY_SECONDS: Final[float] = 120.0


# ---------------------------------------------------------------------------
# Convenience constructor
# ---------------------------------------------------------------------------


def score_event(
    source: str,
    type: str,
    title: str,
    detail: str = "",
) -> JarvisEvent:
    """Create a JarvisEvent with automatic priority scoring from the score table.

    Unknown (source, type) pairs default to priority 5.
    """
    priority = SCORE_TABLE.get((source, type), 5)
    return JarvisEvent(
        source=source,
        type=type,
        title=title,
        detail=detail,
        priority=priority,
    )


# ---------------------------------------------------------------------------
# Engine
# ---------------------------------------------------------------------------

# Type alias for async event handlers.
EventHandler = Callable[[JarvisEvent], Awaitable[None]]


class PriorityEngine:
    """Receives events, scores them, dispatches to handlers based on priority.

    Handler routing:
      - ``critical_handler``: called for priority >= 8 (interrupt user).
      - ``important_handler``: called for priority 5-7 (queue for briefing).
      - ``log_handler``: called for every event regardless of priority.

    Dedup logic prevents the same ``source:type:title`` from triggering a
    critical alert more than once within a 60-second window.
    """

    def __init__(
        self,
        critical_handler: EventHandler | None = None,
        important_handler: EventHandler | None = None,
        log_handler: EventHandler | None = None,
    ) -> None:
        self._critical = critical_handler
        self._important = important_handler
        self._log = log_handler
        self._recent: list[JarvisEvent] = []
        self._dedup: dict[str, float] = {}  # dedup_key -> last_alert_time

    # -- public API ----------------------------------------------------------

    async def process(self, event: JarvisEvent) -> None:
        """Score and dispatch an event to the appropriate handler."""
        # Store for briefings (ring buffer, capped)
        self._recent.append(event)
        if len(self._recent) > _MAX_RECENT_EVENTS:
            self._recent = self._recent[-_MAX_RECENT_EVENTS:]

        # Always log
        if self._log is not None:
            await self._log(event)

        # Dedup: same source+type+title within cooldown = skip alert
        dedup_key = f"{event.source}:{event.type}:{event.title}"
        now = time.time()
        last_alert = self._dedup.get(dedup_key)
        if last_alert is not None and (now - last_alert) < _DEDUP_COOLDOWN_SECONDS:
            logger.debug("Dedup: skipping alert for %s", dedup_key)
            return

        # Dispatch based on priority
        if event.priority >= CRITICAL_THRESHOLD and self._critical is not None:
            self._dedup[dedup_key] = now
            await self._critical(event)
        elif event.priority >= IMPORTANT_THRESHOLD and self._important is not None:
            await self._important(event)

        # Check for event correlations (skip for correlation events to prevent recursion)
        if event.source != "correlation":
            await self._check_correlations(event)

    async def _check_correlations(self, event: JarvisEvent) -> None:
        """Check recent events for causal patterns and emit correlated alerts."""
        now = event.timestamp

        # Pattern 1: Session fails within 60s of another session's push/completion
        if event.type in ("failed", "error"):
            for prev in reversed(self._recent[:-1]):
                if now - prev.timestamp > 60:
                    break
                if (
                    prev.source == "session"
                    and prev.type == "completed"
                    and prev.title != event.title
                ):
                    correlated = JarvisEvent(
                        source="correlation",
                        type="causal",
                        title=f"{event.title} failed after {prev.title} completed",
                        detail=f"Possible breaking change: {event.detail[:80]}",
                        priority=8,
                        timestamp=now,
                    )
                    await self.process(correlated)
                    return

        # Pattern 2: Multiple sessions erroring within 30s
        if event.type in ("failed", "error") and event.source == "session":
            recent_errors = [
                e
                for e in self._recent
                if e.source == "session"
                and e.type in ("failed", "error")
                and now - e.timestamp <= 30
                and e.title != event.title
            ]
            if len(recent_errors) >= 2:
                names = ", ".join(set(e.title for e in recent_errors))
                correlated = JarvisEvent(
                    source="correlation",
                    type="systemic",
                    title=f"Multiple sessions failing: {event.title}, {names}",
                    detail="Possible shared dependency issue",
                    priority=9,
                    timestamp=now,
                )
                await self.process(correlated)
                return

    def get_recent(self, since_minutes: int = 15) -> list[JarvisEvent]:
        """Return events from the last *since_minutes* minutes for briefings."""
        cutoff = time.time() - (since_minutes * 60)
        return [e for e in self._recent if e.timestamp >= cutoff]

    def clear_dedup(self) -> None:
        """Purge stale entries from the dedup cache.

        Entries older than 120 seconds are removed. Call this periodically
        (e.g. after delivering a briefing) to avoid unbounded growth.
        """
        threshold = time.time() - _DEDUP_EXPIRY_SECONDS
        self._dedup = {k: v for k, v in self._dedup.items() if v > threshold}
