"""Background monitor for the Jarvis voice daemon.

Runs registered pollers as independent asyncio tasks on configurable intervals.
Each poller is an async callable that returns a list of ``JarvisEvent`` dicts (or
``None`` / empty list when there is nothing to report).  Events are forwarded to
a callback for downstream processing (alerting, TTS, HUD updates).

The monitor is designed to run as a sibling task alongside the mic and command
loops inside the voice-loop ``TaskGroup``.

Usage::

    monitor = BackgroundMonitor(
        event_callback=handle_event,
        ws_send_fn=ws_send,
        tts_engine=tts,
        get_context_fn=lambda: _context,
    )
    monitor.add_poller("sessions", poll_sessions, interval_s=5.0)
    await monitor.start()
    # ... later ...
    await monitor.stop()
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable, Callable
from typing import Any, Final

logger: Final = logging.getLogger("jarvis-daemon.monitor")

# Type alias for a poller function.
# A poller is an async callable that returns a list of event dicts (or None).
type PollFn = Callable[[], Awaitable[list[dict[str, Any]] | None]]

# Type alias for the event callback.
type EventCallback = Callable[[dict[str, Any]], Awaitable[None]]


class BackgroundMonitor:
    """Runs background pollers as async tasks.

    Collects events emitted by pollers and forwards them to *event_callback*.
    Each poller runs in its own ``asyncio.Task`` with an independent interval,
    so a slow or failing poller does not block the others.
    """

    def __init__(
        self,
        event_callback: EventCallback,
        ws_send_fn: Callable[[dict[str, Any]], Awaitable[None]],
        tts_engine: Any,
        get_context_fn: Callable[[], dict[str, Any]],
    ) -> None:
        """Initialise the monitor.

        Parameters
        ----------
        event_callback:
            Async callable invoked for every event a poller emits.
        ws_send_fn:
            Async callable that sends a dict message to Go via WebSocket.
        tts_engine:
            ``TTSEngine`` instance -- available for pollers / alerter to speak.
        get_context_fn:
            Callable returning the latest context dict pushed by Go.
        """
        self._event_cb = event_callback
        self._ws_send = ws_send_fn
        self._tts = tts_engine
        self._get_context = get_context_fn

        # Registered pollers: (name, async_poll_fn, interval_seconds).
        self._pollers: list[tuple[str, PollFn, float]] = []
        # Running asyncio tasks -- one per poller.
        self._tasks: list[asyncio.Task[None]] = []
        self._running: bool = False

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def add_poller(
        self,
        name: str,
        poll_fn: PollFn,
        interval_s: float,
    ) -> None:
        """Register a poller.

        Must be called **before** :meth:`start`.  ``poll_fn`` is an async
        callable that returns ``list[dict]`` (events) or ``None``.

        Parameters
        ----------
        name:
            Human-readable label for logging.
        poll_fn:
            Async callable that performs the poll and returns events.
        interval_s:
            Seconds to sleep between consecutive invocations.
        """
        if self._running:
            logger.warning(
                "Cannot add poller '%s' while monitor is running", name,
            )
            return
        self._pollers.append((name, poll_fn, interval_s))

    async def start(self) -> None:
        """Launch all registered pollers as ``asyncio.Task`` instances."""
        if self._running:
            logger.warning("Background monitor already running")
            return

        self._running = True
        for name, poll_fn, interval in self._pollers:
            task = asyncio.create_task(
                self._run_poller(name, poll_fn, interval),
                name=f"poller-{name}",
            )
            self._tasks.append(task)

        logger.info(
            "Background monitor started with %d pollers", len(self._pollers),
        )

    async def stop(self) -> None:
        """Cancel all poller tasks and wait for them to finish."""
        self._running = False
        for task in self._tasks:
            task.cancel()
        # Gather with return_exceptions so CancelledError doesn't propagate.
        await asyncio.gather(*self._tasks, return_exceptions=True)
        self._tasks.clear()
        logger.info("Background monitor stopped")

    # ------------------------------------------------------------------
    # Internal
    # ------------------------------------------------------------------

    async def _run_poller(
        self,
        name: str,
        poll_fn: PollFn,
        interval: float,
    ) -> None:
        """Run a single poller in a loop with *interval* seconds between calls.

        Exceptions from ``poll_fn`` are logged and swallowed so that one
        failing poller does not take down the monitor.
        """
        logger.info("Poller '%s' started (interval=%.0fs)", name, interval)

        while self._running:
            try:
                events = await poll_fn()
                if events:
                    for event in events:
                        try:
                            await self._event_cb(event)
                        except Exception:
                            logger.exception(
                                "Event callback failed for poller '%s'", name,
                            )
            except asyncio.CancelledError:
                break
            except Exception:
                logger.exception("Poller '%s' error", name)

            # Sleep between polls, respecting cancellation.
            try:
                await asyncio.sleep(interval)
            except asyncio.CancelledError:
                break

        logger.info("Poller '%s' stopped", name)
