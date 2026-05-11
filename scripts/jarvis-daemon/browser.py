"""Playwright browser controller for Slack and web actions.

Controls a persistent Chromium browser that stays open between actions (no
cold-start per request).  Supports: open URL, Slack messaging, Slack reading,
and generic click/type on any page.

All public methods return ``{"ok": bool, ...}`` dicts matching the tool result
format used throughout the daemon.

Usage::

    from browser import BrowserController

    bc = BrowserController()
    started = await bc.start()
    if started:
        result = await bc.open_url("https://github.com")
        # {"ok": True, "title": "GitHub"}

        result = await bc.slack_send("#engineering", "deployed auth-service")
        # {"ok": True, "channel": "#engineering", "message": "deployed auth-service"}

        await bc.stop()

If Playwright is not installed, ``start()`` returns ``False`` and every action
method returns ``{"ok": False, "error": "Browser not available"}``.
"""

from __future__ import annotations

import asyncio
import logging
from typing import Any, Final

logger: Final = logging.getLogger("jarvis-daemon.browser")

# ---------------------------------------------------------------------------
# Conditional Playwright import
# ---------------------------------------------------------------------------

try:
    from playwright.async_api import Browser, Page, Playwright, async_playwright

    _PLAYWRIGHT_AVAILABLE = True
except ImportError:
    _PLAYWRIGHT_AVAILABLE = False

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

_NAV_TIMEOUT_MS: Final[int] = 15_000
_SLACK_NAV_TIMEOUT_MS: Final[int] = 20_000
_SLACK_LOAD_DELAY: Final[float] = 2.0
_SLACK_SEARCH_DELAY: Final[float] = 1.0
_SLACK_CHANNEL_LOAD_DELAY: Final[float] = 1.5
_SLACK_READ_SETTLE_DELAY: Final[float] = 2.0
_SLACK_TYPING_DELAY_MS: Final[int] = 50
_SLACK_MSG_TYPING_DELAY_MS: Final[int] = 30
_SLACK_SEND_SETTLE: Final[float] = 0.5
_SLACK_SEARCH_OPEN_DELAY: Final[float] = 0.5
_SLACK_MSG_SETTLE: Final[float] = 0.3
_MSG_PREVIEW_MAX_LEN: Final[int] = 200
_DEFAULT_READ_COUNT: Final[int] = 5
_CLICK_TIMEOUT_MS: Final[int] = 5_000
_TYPE_DELAY_MS: Final[int] = 30

# Slack selectors -- using data-qa attributes for stability across UI updates.
_SLACK_MSG_INPUT_SEL: Final[str] = '[data-qa="message_input"] [contenteditable="true"]'
_SLACK_MSG_LIST_ITEM_SEL: Final[str] = '[data-qa="virtual-list-item"]'


# ---------------------------------------------------------------------------
# Error helpers
# ---------------------------------------------------------------------------

def _unavailable_error() -> dict[str, Any]:
    """Return the standard error dict when the browser is not available."""
    return {"ok": False, "error": "Browser not available"}


def _not_installed_error() -> dict[str, Any]:
    """Return the standard error dict when Playwright is not installed."""
    return {
        "ok": False,
        "error": (
            "Playwright is not installed. "
            "Run: pip install playwright && python -m playwright install chromium"
        ),
    }


# ---------------------------------------------------------------------------
# BrowserController
# ---------------------------------------------------------------------------

class BrowserController:
    """Controls a persistent Chromium browser for Slack and web actions.

    The browser stays open between actions (no cold start per request).
    Supports: open URL, Slack messaging, generic click/type.
    """

    def __init__(self) -> None:
        self._playwright: Playwright | None = None  # type: ignore[name-defined]
        self._browser: Browser | None = None  # type: ignore[name-defined]
        self._page: Page | None = None  # type: ignore[name-defined]
        self._available: bool = False

    # -- Lifecycle -----------------------------------------------------------

    async def start(self) -> bool:
        """Launch a persistent Chromium browser.

        Returns ``True`` on success, ``False`` if Playwright is not installed
        or the browser fails to launch.
        """
        if not _PLAYWRIGHT_AVAILABLE:
            logger.warning("Playwright package not installed")
            return False

        try:
            self._playwright = await async_playwright().start()
            self._browser = await self._playwright.chromium.launch(headless=False)
            self._page = await self._browser.new_page()
            self._available = True
            logger.info("Browser controller started")
            return True
        except Exception as exc:
            logger.warning("Browser controller failed to start: %s", exc)
            self._available = False
            return False

    async def stop(self) -> None:
        """Close the browser and clean up Playwright resources."""
        if self._browser is not None:
            try:
                await self._browser.close()
            except Exception as exc:
                logger.warning("Error closing browser: %s", exc)
            self._browser = None

        if self._playwright is not None:
            try:
                await self._playwright.stop()
            except Exception as exc:
                logger.warning("Error stopping Playwright: %s", exc)
            self._playwright = None

        self._page = None
        self._available = False
        logger.info("Browser controller stopped")

    @property
    def available(self) -> bool:
        """``True`` when the browser is launched and ready for actions."""
        return self._available

    # -- URL navigation ------------------------------------------------------

    async def open_url(self, url: str) -> dict[str, Any]:
        """Navigate to *url* and return the page title.

        Returns::

            {"ok": True, "title": "Page Title"}
            {"ok": False, "error": "..."}
        """
        if not self._available or self._page is None:
            return _unavailable_error()

        try:
            await self._page.goto(
                url,
                wait_until="domcontentloaded",
                timeout=_NAV_TIMEOUT_MS,
            )
            title = await self._page.title()
            logger.info("Navigated to %s (%s)", url, title)
            return {"ok": True, "title": title}
        except Exception as exc:
            logger.warning("open_url failed for %s: %s", url, exc)
            return {"ok": False, "error": str(exc)}

    # -- Slack ---------------------------------------------------------------

    async def _navigate_to_slack_channel(self, channel: str) -> dict[str, Any] | None:
        """Navigate to a Slack channel using Cmd+K search.

        Returns ``None`` on success or an error dict on failure.
        This is shared between ``slack_send`` and ``slack_read``.
        """
        if self._page is None:
            return _unavailable_error()

        # Navigate to Slack if not already there.
        current_url = self._page.url
        if "app.slack.com" not in current_url:
            await self._page.goto(
                "https://app.slack.com",
                wait_until="domcontentloaded",
                timeout=_SLACK_NAV_TIMEOUT_MS,
            )
            await asyncio.sleep(_SLACK_LOAD_DELAY)

        # Open quick switcher with Cmd+K and search for the channel.
        await self._page.keyboard.press("Meta+k")
        await asyncio.sleep(_SLACK_SEARCH_OPEN_DELAY)
        await self._page.keyboard.type(channel, delay=_SLACK_TYPING_DELAY_MS)
        await asyncio.sleep(_SLACK_SEARCH_DELAY)
        await self._page.keyboard.press("Enter")
        await asyncio.sleep(_SLACK_CHANNEL_LOAD_DELAY)

        return None

    async def slack_send(self, channel: str, message: str) -> dict[str, Any]:
        """Send a message to a Slack channel via the web app.

        Navigates to ``app.slack.com``, finds the channel using Cmd+K quick
        switcher, types the message, and sends with Enter.

        Assumes the user is already logged into Slack in the browser.

        Returns::

            {"ok": True, "channel": "#engineering", "message": "deployed"}
            {"ok": False, "error": "Slack send failed: ..."}
        """
        if not self._available or self._page is None:
            return _unavailable_error()

        try:
            nav_error = await self._navigate_to_slack_channel(channel)
            if nav_error is not None:
                return nav_error

            # Type message in the message input (contenteditable div).
            msg_input = self._page.locator(_SLACK_MSG_INPUT_SEL)
            await msg_input.click(timeout=_CLICK_TIMEOUT_MS)
            await msg_input.type(message, delay=_SLACK_MSG_TYPING_DELAY_MS)
            await asyncio.sleep(_SLACK_MSG_SETTLE)

            # Send with Enter.
            await self._page.keyboard.press("Enter")
            await asyncio.sleep(_SLACK_SEND_SETTLE)

            logger.info("Sent Slack message to %s: %s", channel, message[:80])
            return {"ok": True, "channel": channel, "message": message}
        except Exception as exc:
            logger.warning("slack_send failed for %s: %s", channel, exc)
            return {"ok": False, "error": f"Slack send failed: {exc}"}

    async def slack_read(
        self,
        channel: str,
        count: int = _DEFAULT_READ_COUNT,
    ) -> dict[str, Any]:
        """Read recent messages from a Slack channel.

        Navigates to the channel and scrapes the last *count* visible
        messages from the virtual list.

        Returns::

            {"ok": True, "channel": "#eng", "messages": ["msg1", "msg2"]}
            {"ok": False, "error": "Slack read failed: ..."}
        """
        if not self._available or self._page is None:
            return _unavailable_error()

        try:
            nav_error = await self._navigate_to_slack_channel(channel)
            if nav_error is not None:
                return nav_error

            # Allow extra time for messages to render after channel load.
            await asyncio.sleep(_SLACK_READ_SETTLE_DELAY)

            # Read messages from the virtual list.
            messages = await self._page.locator(_SLACK_MSG_LIST_ITEM_SEL).all()
            recent = messages[-count:] if len(messages) > count else messages

            texts: list[str] = []
            for msg in recent:
                text = await msg.inner_text()
                stripped = text.strip()
                # Cap each message to avoid huge payloads.
                texts.append(stripped[:_MSG_PREVIEW_MAX_LEN])

            logger.info("Read %d messages from Slack %s", len(texts), channel)
            return {"ok": True, "channel": channel, "messages": texts}
        except Exception as exc:
            logger.warning("slack_read failed for %s: %s", channel, exc)
            return {"ok": False, "error": f"Slack read failed: {exc}"}

    # -- Generic page interactions -------------------------------------------

    async def click(self, selector: str) -> dict[str, Any]:
        """Click an element by CSS selector.

        Returns::

            {"ok": True, "selector": "button.submit"}
            {"ok": False, "error": "..."}
        """
        if not self._available or self._page is None:
            return _unavailable_error()

        try:
            await self._page.click(selector, timeout=_CLICK_TIMEOUT_MS)
            logger.info("Clicked element: %s", selector)
            return {"ok": True, "selector": selector}
        except Exception as exc:
            logger.warning("click failed for %s: %s", selector, exc)
            return {"ok": False, "error": f"Click failed: {exc}"}

    async def type_text(self, selector: str, text: str) -> dict[str, Any]:
        """Type text into an element identified by CSS selector.

        Returns::

            {"ok": True, "selector": "input.search", "text": "hello"}
            {"ok": False, "error": "..."}
        """
        if not self._available or self._page is None:
            return _unavailable_error()

        try:
            locator = self._page.locator(selector)
            await locator.click(timeout=_CLICK_TIMEOUT_MS)
            await locator.type(text, delay=_TYPE_DELAY_MS)
            logger.info("Typed into %s: %s", selector, text[:80])
            return {"ok": True, "selector": selector, "text": text}
        except Exception as exc:
            logger.warning("type_text failed for %s: %s", selector, exc)
            return {"ok": False, "error": f"Type failed: {exc}"}

    # -- Screenshot (utility for debugging) ----------------------------------

    async def screenshot(self, path: str | None = None) -> dict[str, Any]:
        """Take a screenshot of the current page.

        If *path* is not provided, saves to ``/tmp/jarvis-browser-screenshot.png``.

        Returns::

            {"ok": True, "path": "/tmp/jarvis-browser-screenshot.png"}
            {"ok": False, "error": "..."}
        """
        if not self._available or self._page is None:
            return _unavailable_error()

        save_path = path or "/tmp/jarvis-browser-screenshot.png"
        try:
            await self._page.screenshot(path=save_path)
            logger.info("Screenshot saved to %s", save_path)
            return {"ok": True, "path": save_path}
        except Exception as exc:
            logger.warning("screenshot failed: %s", exc)
            return {"ok": False, "error": f"Screenshot failed: {exc}"}

    # -- Current page info ---------------------------------------------------

    async def get_page_info(self) -> dict[str, Any]:
        """Return the current page URL and title.

        Returns::

            {"ok": True, "url": "https://...", "title": "Page Title"}
            {"ok": False, "error": "..."}
        """
        if not self._available or self._page is None:
            return _unavailable_error()

        try:
            url = self._page.url
            title = await self._page.title()
            return {"ok": True, "url": url, "title": title}
        except Exception as exc:
            logger.warning("get_page_info failed: %s", exc)
            return {"ok": False, "error": f"Page info failed: {exc}"}
