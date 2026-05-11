"""Session state poller for the Jarvis voice daemon.

Watches session state transitions from context data pushed by the Go app.
Compares the current session list against a previous snapshot on each poll
and emits ``JarvisEvent`` instances when sessions complete, fail, or need input.

The first poll establishes a baseline snapshot without emitting any events,
so that daemon startup does not fire stale alerts for sessions that were
already running.

Expected context shape (from Go ``dexContextMsg``)::

    {
        "sessions": [
            {"name": "auth-service", "status": "running", "hasQuestion": false, "duration": "2h 30m"},
            ...
        ],
        ...
    }
"""

from __future__ import annotations

import logging
from typing import Any, Callable, Final

from priority import JarvisEvent, score_event

logger: Final = logging.getLogger("jarvis-daemon.pollers.sessions")


class SessionPoller:
    """Watches session state transitions from context data.

    Compares current session list against previous snapshot.
    Emits events when sessions complete, fail, or need input.
    """

    def __init__(self, get_context_fn: Callable[[], dict[str, Any]]) -> None:
        self._get_context = get_context_fn
        self._prev_sessions: dict[str, dict[str, Any]] = {}  # name -> {status, hasQuestion}
        self._initialized: bool = False

    async def poll(self) -> list[JarvisEvent]:
        """Check for session state changes. Returns list of events."""
        context = self._get_context()
        if not context:
            return []

        sessions: list[dict[str, Any]] = context.get("sessions", [])
        current: dict[str, dict[str, Any]] = {}
        for s in sessions:
            name = s.get("name", "unknown")
            current[name] = {
                "status": s.get("status", "unknown"),
                "hasQuestion": s.get("hasQuestion", False),
            }

        events: list[JarvisEvent] = []

        if not self._initialized:
            # First poll -- establish baseline, don't emit events.
            self._prev_sessions = current
            self._initialized = True
            logger.info("Session baseline: %d sessions", len(current))
            return []

        # Detect new sessions.
        for name in current:
            if name not in self._prev_sessions:
                events.append(score_event("session", "started", name))

        # Detect sessions that disappeared (completed or stopped).
        for name in self._prev_sessions:
            if name not in current:
                events.append(score_event("session", "completed", name))

        # Detect state changes for existing sessions.
        for name, state in current.items():
            prev = self._prev_sessions.get(name)
            if prev is None:
                continue

            # hasQuestion changed to True -> needs input.
            if state["hasQuestion"] and not prev.get("hasQuestion"):
                events.append(score_event("session", "needs_input", name))

        self._prev_sessions = current

        for e in events:
            logger.info(
                "Session event: %s %s (priority=%d)", e.type, e.title, e.priority,
            )

        return events
