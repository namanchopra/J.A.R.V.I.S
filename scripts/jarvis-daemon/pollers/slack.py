"""Slack unread-message poller for the Jarvis voice daemon.

Periodically checks the Slack web app (via Playwright browser controller) for
unread DMs and channel messages.  Classifies by type (DM > mention > channel)
and returns priority-scored ``JarvisEvent`` instances for the alerter pipeline.

First poll establishes a baseline: existing unreads are marked as seen so the
user is not flooded with stale alerts on daemon startup.

Usage::

    from browser import BrowserController
    from pollers.slack import SlackPoller

    browser = BrowserController()
    await browser.start()

    poller = SlackPoller(browser)
    events = await poller.poll()  # list[JarvisEvent]

Integration with ``BackgroundMonitor``::

    monitor.add_poller("slack", poller.poll_dicts, interval_s=30.0)
"""

from __future__ import annotations

import asyncio
import hashlib
import logging
from dataclasses import asdict
from typing import Any, Final

from priority import JarvisEvent, score_event

logger: Final = logging.getLogger("jarvis-daemon.pollers.slack")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

# Maximum channels to inspect per poll cycle to avoid overloading the browser.
_MAX_CHANNELS_PER_POLL: Final[int] = 5

# Delay (seconds) to let Slack fully render after navigation.
_SLACK_LOAD_DELAY: Final[float] = 3.0

# Slack sidebar selectors -- prefer data-qa attributes for stability.
_SIDEBAR_ITEM_SEL: Final[str] = '[data-qa="channel_sidebar_item"]'
_SIDEBAR_UNREADS_ATTR: Final[str] = "data-has-unreads"
_SIDEBAR_CHANNEL_NAME_ATTR: Final[str] = "data-channel-name"
_SIDEBAR_DM_ICON_SEL: Final[str] = '[data-qa="sidebar-im-icon"]'


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _message_key(channel_name: str) -> str:
    """Deterministic dedup key for a channel's unread batch."""
    return hashlib.sha256(f"slack:{channel_name}".encode()).hexdigest()[:16]


# ---------------------------------------------------------------------------
# SlackPoller
# ---------------------------------------------------------------------------


class SlackPoller:
    """Monitors Slack for unread messages via Playwright browser.

    Checks for unread DMs and channel messages in the sidebar.  Classifies
    by type (DM > mention > channel) and emits priority-scored events:

    - DM unreads: priority 8 (critical -- interrupt user).
    - Channel unreads: priority 3 (low -- log only).

    The first poll is a **baseline**: it records existing unreads as seen
    without emitting events, so the user is not alerted for messages that
    were already present when the daemon started.

    Dedup: a channel's unread is only reported once until ``clear_seen()``
    is called (e.g., after the user reads a briefing or the channel is
    explicitly opened).
    """

    def __init__(self, browser_controller: Any) -> None:
        """Initialise the poller.

        Parameters
        ----------
        browser_controller:
            A ``BrowserController`` instance from ``browser.py``.  May be
            ``None`` if the browser is unavailable.
        """
        self._browser = browser_controller
        self._seen_messages: set[str] = set()  # Dedup by message key
        self._initialized: bool = False

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def poll(self) -> list[JarvisEvent]:
        """Check Slack for unreads.  Returns a list of scored events.

        Returns an empty list when:
        - The browser controller is ``None`` or not available.
        - The browser page is ``None`` (not yet started).
        - An error occurs during scraping (logged, never raised).
        - This is the first poll (baseline establishment).
        """
        if not self._browser or not self._browser.available:
            return []

        events: list[JarvisEvent] = []

        try:
            page = self._browser._page
            if page is None:
                return []

            # Navigate to Slack if the browser is on a different site.
            current_url = page.url
            if "app.slack.com" not in current_url:
                result = await self._browser.open_url("https://app.slack.com")
                if not result.get("ok"):
                    logger.debug("Could not navigate to Slack: %s", result.get("error"))
                    return []
                await asyncio.sleep(_SLACK_LOAD_DELAY)

            # Scrape unread channels from the sidebar.
            events = await self._scrape_sidebar_unreads(page)

            # First poll is baseline -- record what's there, emit nothing.
            if not self._initialized:
                self._initialized = True
                logger.info(
                    "Slack baseline established: %d unread channels detected",
                    len(events),
                )
                return []

        except Exception:
            logger.exception("Slack poll error")
            return []

        for event in events:
            logger.info(
                "Slack event: %s %s (priority=%d)",
                event.type,
                event.title,
                event.priority,
            )

        return events

    async def poll_dicts(self) -> list[dict[str, Any]] | None:
        """Wrapper for ``BackgroundMonitor`` integration.

        The monitor's ``PollFn`` type expects ``list[dict] | None``, so this
        method converts ``JarvisEvent`` dataclass instances to plain dicts.
        """
        events = await self.poll()
        if not events:
            return None
        return [asdict(e) for e in events]

    def clear_seen(self) -> None:
        """Reset seen messages (e.g., after a briefing or manual clear).

        After clearing, the next poll will re-detect unreads and emit events
        for any channels that still have unread messages.
        """
        self._seen_messages.clear()
        logger.debug("Slack seen-messages cache cleared")

    # ------------------------------------------------------------------
    # Internal
    # ------------------------------------------------------------------

    async def _scrape_sidebar_unreads(self, page: Any) -> list[JarvisEvent]:
        """Scrape the Slack sidebar for channels with unread indicators.

        Parameters
        ----------
        page:
            The Playwright ``Page`` instance currently on ``app.slack.com``.

        Returns
        -------
        list[JarvisEvent]
            Scored events for each newly-detected unread channel.
        """
        events: list[JarvisEvent] = []

        try:
            # Query sidebar items that have the unreads flag set.
            unreads = await page.locator(
                f'{_SIDEBAR_ITEM_SEL}[{_SIDEBAR_UNREADS_ATTR}="true"]',
            ).all()

            for item in unreads[:_MAX_CHANNELS_PER_POLL]:
                try:
                    name = await item.get_attribute(_SIDEBAR_CHANNEL_NAME_ATTR) or ""
                    if not name:
                        continue

                    # Dedup: skip channels we have already reported.
                    msg_key = _message_key(name)
                    if msg_key in self._seen_messages:
                        continue
                    self._seen_messages.add(msg_key)

                    # Classify: DMs have a distinct icon in the sidebar.
                    is_dm = await item.locator(_SIDEBAR_DM_ICON_SEL).count() > 0

                    if is_dm:
                        events.append(
                            score_event(
                                "slack",
                                "dm",
                                name,
                                f"Unread DM from {name}",
                            ),
                        )
                    else:
                        events.append(
                            score_event(
                                "slack",
                                "channel",
                                f"#{name}",
                                f"New messages in #{name}",
                            ),
                        )
                except Exception:
                    # Individual item failure should not abort the loop.
                    continue

        except Exception as exc:
            logger.debug("Slack sidebar parse failed: %s", exc)

        return events
