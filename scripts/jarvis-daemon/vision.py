"""Screenshot capture for the Jarvis voice daemon.

Captures the macOS screen via ``screencapture``, resizes with ``sips`` to
keep LLM token usage low, and returns a base64-encoded PNG suitable for
multimodal content blocks (e.g. Gemini 2.5 Flash).
"""

from __future__ import annotations

import asyncio
import base64
import logging
from pathlib import Path
from typing import Any, Final

logger: Final = logging.getLogger("jarvis-daemon.vision")

_SCREENSHOT_PATH: Final[Path] = Path("/tmp/jarvis-screenshot.png")
_MAX_WIDTH: Final[int] = 1568
_MIN_FILE_BYTES: Final[int] = 1000  # below this = permission denied


async def _get_frontmost_window_id() -> int | None:
    """Get the window ID of the frontmost application's main window via AppleScript."""
    script = (
        'tell application "System Events" to get id of first window '
        'of (first application process whose frontmost is true)'
    )
    proc = await asyncio.create_subprocess_exec(
        "osascript", "-e", script,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    stdout, stderr = await proc.communicate()
    if proc.returncode != 0:
        logger.warning("Failed to get frontmost window ID: %s", stderr.decode().strip())
        return None
    try:
        return int(stdout.decode().strip())
    except ValueError:
        logger.warning("Could not parse window ID: %s", stdout.decode().strip())
        return None


async def capture_screenshot(mode: str = "screen") -> dict[str, Any]:
    """Capture the macOS screen or active window and return a base64-encoded PNG.

    Supports ``mode="screen"`` (full screen) and ``mode="window"`` (frontmost
    window).  Window mode falls back to full screen if the window ID cannot be
    detected.  Returns ``{"ok": True, "base64": ..., "image_path": ...,
    "media_type": ...}`` on success, or ``{"ok": False, "error": ...}`` on
    failure.
    """
    if mode not in ("screen", "window"):
        return {"ok": False, "error": f"Unsupported capture mode: {mode}"}

    # 1. Build the screencapture command (-x suppresses shutter sound).
    if mode == "window":
        window_id = await _get_frontmost_window_id()
        if window_id is not None:
            cmd = ["screencapture", "-x", "-l", str(window_id), str(_SCREENSHOT_PATH)]
        else:
            logger.warning("Window ID detection failed, falling back to full screen")
            cmd = ["screencapture", "-x", str(_SCREENSHOT_PATH)]
    else:
        cmd = ["screencapture", "-x", str(_SCREENSHOT_PATH)]

    try:
        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.DEVNULL,
            stderr=asyncio.subprocess.PIPE,
        )
        _, stderr = await proc.communicate()
    except FileNotFoundError:
        return {"ok": False, "error": "screencapture binary not found"}

    if proc.returncode != 0:
        msg = stderr.decode().strip() if stderr else "unknown error"
        return {"ok": False, "error": f"screencapture failed: {msg}"}

    # 2. Check Screen Recording permission (tiny/empty file = denied).
    if not _SCREENSHOT_PATH.exists() or _SCREENSHOT_PATH.stat().st_size < _MIN_FILE_BYTES:
        logger.warning("Screenshot too small -- Screen Recording permission likely denied")
        return {"ok": False, "error": "Screen Recording permission denied (grant in System Settings > Privacy)"}

    # 3. Resize if wider than max width to reduce tokens.
    try:
        proc = await asyncio.create_subprocess_exec(
            "sips", "--resampleWidth", str(_MAX_WIDTH), str(_SCREENSHOT_PATH),
            stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.DEVNULL,
        )
        await proc.communicate()
    except FileNotFoundError:
        logger.warning("sips not found, skipping resize")

    # 4. Read and base64-encode.
    try:
        raw = _SCREENSHOT_PATH.read_bytes()
    except OSError as exc:
        return {"ok": False, "error": f"Failed to read screenshot: {exc}"}

    encoded = base64.b64encode(raw).decode("ascii")
    logger.info("Captured screenshot (%d bytes, base64 %d chars)", len(raw), len(encoded))
    return {
        "ok": True,
        "base64": encoded,
        "image_path": str(_SCREENSHOT_PATH),
        "media_type": "image/png",
    }
