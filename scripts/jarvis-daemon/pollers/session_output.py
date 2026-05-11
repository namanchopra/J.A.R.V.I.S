"""Session output poller for the Jarvis voice daemon.

Periodically reads terminal output for active sessions via the Go WebSocket
tool ``read_session_output``.  Parses the last lines to determine what each
session is doing and maintains a per-session summary that Jarvis can query
(e.g. "what's maya doing?").

Rate-limited to ``MAX_SESSIONS_PER_CYCLE`` reads per poll cycle to avoid
overloading CMux.  Sessions are read in round-robin order so every session
gets coverage over successive cycles.  An output hash is stored per session
to skip analysis when nothing has changed.

State transitions (e.g. working -> error) emit ``JarvisEvent`` instances that
feed into the priority engine for alerting.
"""

from __future__ import annotations

import asyncio
import logging
import re
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Final

from priority import JarvisEvent, score_event

logger: Final = logging.getLogger("jarvis-daemon.pollers.session_output")

# -- Configuration -----------------------------------------------------------

MAX_SESSIONS_PER_CYCLE: Final[int] = 3
READ_TIMEOUT_S: Final[float] = 5.0
STALL_THRESHOLD_CYCLES: Final[int] = 3
ERROR_ESCALATION_THRESHOLD: Final[int] = 2

# -- Pattern sets ------------------------------------------------------------

ERROR_PATTERNS: Final[list[re.Pattern[str]]] = [
    re.compile(r"error", re.IGNORECASE),
    re.compile(r"failed", re.IGNORECASE),
    re.compile(r"TypeError|SyntaxError|ReferenceError|ImportError"),
    re.compile(r"panic:|FATAL|CRITICAL"),
    re.compile(r"compile.*error", re.IGNORECASE),
    re.compile(r"test.*fail", re.IGNORECASE),
    re.compile(r"npm ERR!"),
    re.compile(r"go:.*cannot"),
]

COMPLETION_PATTERNS: Final[list[re.Pattern[str]]] = [
    re.compile(r"passed|success|complete|done", re.IGNORECASE),
    re.compile(r"All \d+ tests passed"),
    re.compile(r"Build succeeded"),
]

WAITING_PATTERNS: Final[list[re.Pattern[str]]] = [
    re.compile(r"Do you want to|Allow|y/n|Yes/No", re.IGNORECASE),
    re.compile(r"waiting for.*input", re.IGNORECASE),
]

# -- Summary model -----------------------------------------------------------


@dataclass(slots=True)
class SessionSummary:
    """Current understanding of what a session is doing."""

    name: str
    status: str = "unknown"  # "working", "error", "completed", "waiting", "idle"
    last_action: str = ""  # Brief description of recent activity
    error_summary: str = ""  # If errors detected, a brief summary
    last_output_hash: int = 0  # To detect changes
    updated_at: float = field(default_factory=time.time)
    unchanged_count: int = 0  # consecutive polls with no output change
    error_repeat_count: int = 0  # consecutive polls with same error


# -- Poller ------------------------------------------------------------------


class SessionOutputPoller:
    """Reads terminal output for active sessions and maintains summaries.

    Rate-limited to ``MAX_SESSIONS_PER_CYCLE`` per poll to avoid
    overloading CMux.  Stores per-session summaries that Jarvis can
    use to answer "what's maya doing?".
    """

    def __init__(
        self,
        tool_execute_fn: Callable[..., Any],
        get_context_fn: Callable[[], dict[str, Any]],
    ) -> None:
        """Initialise the poller.

        Args:
            tool_execute_fn: Async callable ``(name, args) -> dict`` that
                sends tool calls to the Go app via WebSocket.
            get_context_fn: Callable returning the latest context dict
                pushed by the Go app (contains ``sessions`` list).
        """
        self._execute = tool_execute_fn
        self._get_context = get_context_fn
        self.summaries: dict[str, SessionSummary] = {}
        self._read_index: int = 0  # Round-robin cursor

    async def poll(self) -> list[JarvisEvent]:
        """Read output for up to ``MAX_SESSIONS_PER_CYCLE`` sessions.

        Returns a list of ``JarvisEvent`` instances for any detected state
        transitions (e.g. a session entering an error state).
        """
        context = self._get_context()
        if not context:
            return []

        sessions: list[dict[str, Any]] = context.get("sessions", [])
        if not sessions:
            return []

        events: list[JarvisEvent] = []

        # Round-robin: advance the cursor through the session list.
        start = self._read_index
        to_read = sessions[start : start + MAX_SESSIONS_PER_CYCLE]
        self._read_index = start + MAX_SESSIONS_PER_CYCLE
        if self._read_index >= len(sessions):
            self._read_index = 0

        for session in to_read:
            name: str = session.get("name", "unknown")
            try:
                result = await asyncio.wait_for(
                    self._execute("read_session_output", {"project": name}),
                    timeout=READ_TIMEOUT_S,
                )

                if not result.get("ok"):
                    continue

                output: str = result.get("output", "")
                output_hash = hash(output)

                prev = self.summaries.get(name)
                if prev and prev.last_output_hash == output_hash:
                    # Output unchanged — check for stall
                    if prev.status == "working":
                        prev.unchanged_count += 1
                        if prev.unchanged_count == STALL_THRESHOLD_CYCLES:
                            events.append(
                                score_event(
                                    "session",
                                    "stalled",
                                    name,
                                    f"No output change for {STALL_THRESHOLD_CYCLES * 10}+ seconds",
                                )
                            )
                    continue

                summary = self._analyze_output(name, output)
                # Reset unchanged_count since output actually changed.
                summary.unchanged_count = 0

                # Detect state transitions and emit events.
                if prev and prev.status != summary.status:
                    if summary.status == "error":
                        events.append(
                            score_event(
                                "session",
                                "error",
                                name,
                                summary.error_summary[:100],
                            )
                        )
                    elif summary.status == "completed":
                        events.append(score_event("session", "completed", name))
                    elif summary.status == "waiting":
                        events.append(score_event("session", "needs_input", name))

                # Track repeated errors for escalation.
                if prev and prev.status == "error" and summary.status == "error":
                    if prev.error_summary == summary.error_summary:
                        summary.error_repeat_count = prev.error_repeat_count + 1
                        if summary.error_repeat_count >= ERROR_ESCALATION_THRESHOLD:
                            events.append(
                                score_event(
                                    "session",
                                    "error",
                                    name,
                                    f"REPEATED: {summary.error_summary[:80]}",
                                )
                            )

                self.summaries[name] = summary

            except asyncio.TimeoutError:
                logger.debug("Timeout reading output for %s", name)
            except Exception:
                logger.debug("Error reading output for %s", name, exc_info=True)

        return events

    def get_summary(self, project: str) -> SessionSummary | None:
        """Get the current summary for a project (for voice queries).

        Performs a case-insensitive substring match against stored session
        names, so ``get_summary("maya")`` matches a session named
        ``"maya-web"`` or ``"MAYA-backend"``.
        """
        project_lower = project.lower()
        for name, summary in self.summaries.items():
            if project_lower in name.lower():
                return summary
        return None

    # -- Internal helpers ----------------------------------------------------

    def _analyze_output(self, name: str, output: str) -> SessionSummary:
        """Parse terminal output to determine session state.

        Checks the last 10 lines for error, completion, and waiting
        patterns.  Waiting patterns take highest precedence (a prompt
        for input overrides other signals), followed by errors, then
        completion.  If nothing matches the session is assumed to be
        working.
        """
        lines = output.strip().split("\n")
        last_lines = lines[-10:] if len(lines) > 10 else lines
        last_text = "\n".join(last_lines)

        status = "working"
        error_summary = ""
        last_action = last_lines[-1].strip()[:80] if last_lines else ""

        # Check for waiting first (highest precedence).
        for pattern in WAITING_PATTERNS:
            if pattern.search(last_text):
                return SessionSummary(
                    name=name,
                    status="waiting",
                    last_action=last_action,
                    error_summary="",
                    last_output_hash=hash(output),
                    updated_at=time.time(),
                )

        # Check for errors.
        for pattern in ERROR_PATTERNS:
            match = pattern.search(last_text)
            if match:
                status = "error"
                # Find the most recent line containing the error.
                for line in reversed(last_lines):
                    if pattern.search(line):
                        error_summary = line.strip()[:100]
                        break
                break

        # Check for completion (only if no errors found).
        if status != "error":
            for pattern in COMPLETION_PATTERNS:
                if pattern.search(last_text):
                    status = "completed"
                    break

        return SessionSummary(
            name=name,
            status=status,
            last_action=last_action,
            error_summary=error_summary,
            last_output_hash=hash(output),
            updated_at=time.time(),
        )
