"""Background research agent for the Jarvis voice daemon.

Spawns ``claude --print`` processes in the background to answer research
questions.  When the user says "research X" (or the LLM decides a query
warrants research), this agent:

1. Returns an immediate confirmation so the voice pipeline can respond.
2. Spawns ``claude --print --model sonnet`` as a subprocess.
3. Captures stdout when the process completes (or kills it on timeout).
4. Extracts a 2-3 sentence spoken summary from the output.
5. Emits a ``research/completed`` or ``research/failed`` JarvisEvent.

Concurrency is capped at ``MAX_CONCURRENT`` (2) to avoid hammering the
Claude API.  Each task has a ``TIMEOUT_S`` (120 s) hard deadline -- the
subprocess is killed if it exceeds this.

Usage::

    from research import ResearchAgent

    agent = ResearchAgent(event_callback=priority_engine.process)
    ack = await agent.research("websockets vs SSE for real-time apps")
    # ack == "On it, sir. Will report back shortly."

    # ... later, the event_callback fires with a research/completed event.

    result = agent.get_last_result()
    print(result.summary)  # 2-3 sentence TTS-friendly summary
"""

from __future__ import annotations

import asyncio
import logging
import os
import re
import shutil
import time
from dataclasses import dataclass, field
from typing import Awaitable, Callable, Final

from priority import JarvisEvent, score_event

logger: Final = logging.getLogger("jarvis-daemon.research")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

MAX_CONCURRENT: Final[int] = 2
TIMEOUT_S: Final[float] = 120.0  # 2 minutes max per research task

# Type alias for the async event callback.
type EventCallback = Callable[[JarvisEvent], Awaitable[None]]


# ---------------------------------------------------------------------------
# Result model
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class ResearchResult:
    """Immutable record of a completed (or failed) research task."""

    query: str
    output: str
    summary: str  # 2-3 sentence TTS-friendly summary
    duration: float
    success: bool
    timestamp: float = field(default_factory=time.time)


# ---------------------------------------------------------------------------
# Agent
# ---------------------------------------------------------------------------


class ResearchAgent:
    """Spawns background ``claude --print`` processes for research queries.

    When the user says "research X", this agent:

    1. Responds immediately ("On it, sir").
    2. Spawns ``claude --print`` in background.
    3. Captures output.
    4. Creates a 2-3 sentence spoken summary.
    5. Emits a ``research/completed`` event via *event_callback*.
    """

    def __init__(self, event_callback: EventCallback | None = None) -> None:
        """Initialise the research agent.

        Parameters
        ----------
        event_callback:
            Async callable invoked when a research task completes or fails.
            Receives a ``JarvisEvent`` from ``score_event("research", ...)``.
        """
        self._event_cb: EventCallback | None = event_callback
        self._active: list[asyncio.Task[None]] = []
        self._results: list[ResearchResult] = []

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def research(self, query: str) -> str:
        """Start a background research task.

        Returns immediately with a spoken confirmation string.  The actual
        research runs as a detached ``asyncio.Task``.

        Parameters
        ----------
        query:
            The research question to send to ``claude --print``.

        Returns
        -------
        str
            A short acknowledgement suitable for TTS.
        """
        query = query.strip()
        if not query:
            return "I need a question to research, sir."

        if len(self._active) >= MAX_CONCURRENT:
            return (
                "Already working on two research tasks, sir. "
                "I'll start this one as soon as a slot opens up."
            )

        task = asyncio.create_task(
            self._run_research(query),
            name=f"research-{query[:40]}",
        )
        self._active.append(task)
        task.add_done_callback(self._on_task_done)

        logger.info("Research started: '%s'", query[:80])
        return "On it, sir. Will report back shortly."

    def get_last_result(self, query: str = "") -> ResearchResult | None:
        """Return the most recent result, optionally filtered by query.

        Parameters
        ----------
        query:
            If non-empty, return the most recent result whose query
            contains this substring (case-insensitive).

        Returns
        -------
        ResearchResult | None
            The matching result, or ``None`` if no results exist.
        """
        if not self._results:
            return None

        if query:
            query_lower = query.lower()
            for result in reversed(self._results):
                if query_lower in result.query.lower():
                    return result
            return None

        return self._results[-1]

    @property
    def active_count(self) -> int:
        """Number of research tasks currently running."""
        return len(self._active)

    @property
    def results(self) -> list[ResearchResult]:
        """All completed results (oldest first)."""
        return list(self._results)

    async def stop(self) -> None:
        """Cancel all active research tasks.

        Called during daemon shutdown to clean up subprocesses.
        """
        if not self._active:
            return

        logger.info("Stopping %d active research task(s)", len(self._active))
        for task in self._active:
            task.cancel()

        await asyncio.gather(*self._active, return_exceptions=True)
        self._active.clear()
        logger.info("All research tasks stopped")

    # ------------------------------------------------------------------
    # Internal
    # ------------------------------------------------------------------

    def _on_task_done(self, task: asyncio.Task[None]) -> None:
        """Remove a finished task from the active list."""
        if task in self._active:
            self._active.remove(task)

    async def _run_research(self, query: str) -> None:
        """Run ``claude --print`` in a subprocess and emit the result event."""
        start = time.time()

        try:
            claude_path = self._find_claude()
            if claude_path is None:
                logger.error("Claude CLI not found in PATH")
                await self._emit_failure(
                    query,
                    "Claude CLI not found in PATH. Cannot run research.",
                )
                return

            proc = await asyncio.create_subprocess_exec(
                claude_path,
                "--print",
                "--output-format", "text",
                "--max-turns", "1",
                "--model", "sonnet",
                query,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                env={**os.environ, "DISABLE_AUTOUPDATE": "1"},
            )

            try:
                stdout, stderr = await asyncio.wait_for(
                    proc.communicate(),
                    timeout=TIMEOUT_S,
                )
            except asyncio.TimeoutError:
                proc.kill()
                await proc.wait()
                duration = time.time() - start
                logger.warning(
                    "Research timed out after %.0fs: '%s'",
                    duration,
                    query[:60],
                )
                await self._emit_failure(
                    query,
                    f"Research timed out after {TIMEOUT_S:.0f}s",
                )
                return

            output = stdout.decode("utf-8", errors="replace").strip()
            duration = time.time() - start

            if not output:
                stderr_text = stderr.decode("utf-8", errors="replace").strip()
                logger.warning(
                    "No output from Claude for '%s'. stderr: %s",
                    query[:60],
                    stderr_text[:200],
                )
                await self._emit_failure(query, "No output from Claude")
                return

            summary = _summarize(output)

            result = ResearchResult(
                query=query,
                output=output,
                summary=summary,
                duration=duration,
                success=True,
            )
            self._results.append(result)

            logger.info(
                "Research complete: '%s' in %.1fs (%d chars)",
                query[:50],
                duration,
                len(output),
            )

            if self._event_cb is not None:
                await self._event_cb(score_event(
                    "research",
                    "completed",
                    query,
                    summary,
                ))

        except asyncio.CancelledError:
            logger.info("Research cancelled: '%s'", query[:60])
            raise
        except Exception:
            logger.exception("Research failed: '%s'", query[:60])
            await self._emit_failure(query, "Internal error during research")

    async def _emit_failure(self, query: str, detail: str) -> None:
        """Record a failed result and emit a failure event."""
        result = ResearchResult(
            query=query,
            output="",
            summary=detail,
            duration=0.0,
            success=False,
        )
        self._results.append(result)

        if self._event_cb is not None:
            await self._event_cb(score_event(
                "research",
                "failed",
                query,
                detail,
            ))

    @staticmethod
    def _find_claude() -> str | None:
        """Find the ``claude`` CLI binary on PATH."""
        return shutil.which("claude")


# ---------------------------------------------------------------------------
# Summary extraction
# ---------------------------------------------------------------------------

# Sentence-ending punctuation followed by whitespace.
_SENTENCE_RE: Final[re.Pattern[str]] = re.compile(r"(?<=[.!?])\s+")

# Maximum length of a TTS summary in characters.
_MAX_SUMMARY_LEN: Final[int] = 300

# Number of sentences to include in the summary.
_SUMMARY_SENTENCES: Final[int] = 3


def _summarize(output: str) -> str:
    """Extract a 2-3 sentence summary from research output for TTS.

    Takes the first few sentences from the output.  If the output starts
    with a markdown heading or bullet list, those are stripped first so the
    summary reads naturally when spoken.

    Parameters
    ----------
    output:
        Full text output from ``claude --print``.

    Returns
    -------
    str
        A concise summary suitable for text-to-speech.
    """
    # Strip markdown headings and leading bullets for cleaner TTS.
    cleaned = re.sub(r"^#{1,6}\s+.*\n?", "", output, flags=re.MULTILINE)
    cleaned = re.sub(r"^\s*[-*]\s+", "", cleaned, flags=re.MULTILINE)
    cleaned = cleaned.strip()

    if not cleaned:
        return output[:_MAX_SUMMARY_LEN]

    # Split into sentences and take the first few.
    sentences = _SENTENCE_RE.split(cleaned[:1000])
    summary_parts = sentences[:_SUMMARY_SENTENCES]
    summary = " ".join(s.strip() for s in summary_parts if s.strip())

    if len(summary) > _MAX_SUMMARY_LEN:
        summary = summary[:_MAX_SUMMARY_LEN - 3] + "..."

    return summary
