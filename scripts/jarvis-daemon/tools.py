"""Tool definitions and executor bridge for the Jarvis voice daemon.

Defines the set of tools the LLM can invoke (approve, focus, git ops, etc.)
and provides format converters for Ollama (OpenAI-compatible) and Anthropic
function-calling schemas.

The ``ToolExecutor`` sends tool calls to the Go app over WebSocket and uses
asyncio Futures to wait for results.  The Go app responds with a
``tool_result`` message whose ``id`` matches the original call.

Usage::

    from tools import ToolExecutor, get_ollama_tools, get_anthropic_tools

    executor = ToolExecutor(ws_send_fn=ws.send_json)
    result = await executor.execute("focus_session", {"project": "maya-web"})
    # result: {"ok": True, "message": "Focused maya-web"}

    # When a tool_result WS message arrives from Go:
    executor.handle_result({"id": "...", "result": {"ok": True}})
"""

from __future__ import annotations

import asyncio
import logging
import uuid
from collections.abc import Awaitable, Callable
from typing import Any, Final

logger: Final = logging.getLogger("jarvis-daemon.tools")

# ---------------------------------------------------------------------------
# Tool definitions (shared source of truth)
# ---------------------------------------------------------------------------

# Each entry maps a tool name to its metadata.  The ``params`` dict uses
# JSON Schema-style type strings ("string", "boolean", etc.) as values.
# All params are required unless the dict is empty (no params).

TOOL_DEFINITIONS: Final[list[dict[str, Any]]] = [
    {
        "name": "approve_session",
        "description": "Approve a pending approval prompt for a session",
        "params": {"name": "string"},
    },
    {
        "name": "approve_all",
        "description": "Approve all pending approval prompts",
        "params": {},
    },
    {
        "name": "deny_session",
        "description": "Deny a pending approval prompt for a session",
        "params": {"name": "string"},
    },
    {
        "name": "focus_session",
        "description": "Focus a terminal session by project name",
        "params": {"project": "string"},
    },
    {
        "name": "focus_app",
        "description": "Focus a macOS application by name",
        "params": {"name": "string"},
    },
    {
        "name": "send_to_terminal",
        "description": "Send a command or message to a Claude Code session's terminal. Use to give instructions, approve requests, run commands, or interact with a session.",
        "params": {"project": "string", "command": "string"},
    },
    {
        "name": "get_status",
        "description": "Get current session, cost, and approval status",
        "params": {},
    },
    {
        "name": "navigate_view",
        "description": "Navigate the HUD to a specific view",
        "params": {"view": "string"},
    },
    {
        "name": "git_stage",
        "description": "Stage all changes in a project repo",
        "params": {"project": "string"},
    },
    {
        "name": "git_commit",
        "description": "Commit staged changes with a message",
        "params": {"project": "string", "message": "string"},
    },
    {
        "name": "git_push",
        "description": "Push commits to the remote",
        "params": {"project": "string"},
    },
    {
        "name": "open_url",
        "description": "Open a URL in the default browser",
        "params": {"url": "string"},
    },
    {
        "name": "read_session_output",
        "description": "Read recent terminal output from a session to see what it's doing, what errors it has, or what it's asking. Use this to understand a session's current state before taking action.",
        "params": {"project": "string"},
    },
    {
        "name": "check_slack",
        "description": "Check for unread Slack messages, DMs, and mentions",
        "params": {},
    },
    {
        "name": "research",
        "description": "Start a background web research task on any topic",
        "params": {"query": "string"},
    },
    {
        "name": "get_briefing",
        "description": "Get a summary of recent events from the last N minutes",
        "params": {},
    },
    {
        "name": "see_screen",
        "description": "Capture and analyze a screenshot of the screen",
        "params": {"question": "string", "mode": "string"},
    },
    {
        "name": "browse_url",
        "description": "Open a URL in the browser and return page content",
        "params": {"url": "string"},
    },
    # Slack tools are provided by MCP Slack server, not defined here.
    {
        "name": "highlight_hud_panel",
        "description": "Highlight a HUD panel to draw attention",
        "params": {"panel": "string", "action": "string"},
    },
    {
        "name": "plan_task",
        "description": "Create a step-by-step plan for a complex task",
        "params": {"goal": "string", "steps": "string"},
    },
    {
        "name": "create_todo",
        "description": "Add a todo item to a session's checklist",
        "params": {"project": "string", "title": "string"},
    },
    {
        "name": "complete_todo",
        "description": "Mark a todo item as done",
        "params": {"project": "string", "title": "string"},
    },
    {
        "name": "run_workflow",
        "description": "Execute a multi-phase agent workflow pipeline",
        "params": {"phases": "string"},
    },
    # ---------------------------------------------------------------------------
    # v0.3.0 -- Spotify tools (TASK-004 declarations; impls in TASK-010)
    # ---------------------------------------------------------------------------
    {
        "name": "spotify_search_and_play",
        "description": "Search Spotify by name and play the top result on the user's Mac.",
        "params": {"query": "string"},
    },
    {
        "name": "spotify_pause",
        "description": "Pause Spotify playback on the user's Mac.",
        "params": {},
    },
    {
        "name": "spotify_resume",
        "description": "Resume Spotify playback on the user's Mac.",
        "params": {},
    },
    {
        "name": "spotify_skip",
        "description": "Skip to the next track in Spotify.",
        "params": {},
    },
    {
        "name": "spotify_previous",
        "description": "Go back to the previous track in Spotify.",
        "params": {},
    },
    {
        "name": "spotify_what_is_playing",
        "description": "Get the currently-playing Spotify track (name, artist, position).",
        "params": {},
    },
    {
        "name": "spotify_set_volume",
        "description": "Set Spotify playback volume (0-100).",
        "params": {"percent": "integer"},
    },
    {
        "name": "spotify_like_current",
        "description": "Add the currently-playing track to the user's Liked Songs.",
        "params": {},
    },
    {
        "name": "spotify_queue",
        "description": "Queue a track to play next by name.",
        "params": {"query": "string"},
    },
    # ---------------------------------------------------------------------------
    # v0.3.0 -- Mac control tools (TASK-004 declarations; impls in TASK-015)
    # ---------------------------------------------------------------------------
    {
        "name": "mac_open_app",
        "description": "Open or activate a macOS application by name.",
        "params": {"name": "string"},
    },
    {
        "name": "mac_quit_app",
        "description": "Quit a macOS application by name (destructive).",
        "params": {"name": "string"},
    },
    {
        "name": "mac_focus_window",
        "description": "Focus a specific window of a macOS application by title substring.",
        "params": {"app": "string", "title": "string"},
    },
    {
        "name": "mac_set_volume",
        "description": "Set system audio output volume (0-100).",
        "params": {"percent": "integer"},
    },
    {
        "name": "mac_mute",
        "description": "Mute the system audio output.",
        "params": {},
    },
    {
        "name": "mac_unmute",
        "description": "Unmute the system audio output.",
        "params": {},
    },
    {
        "name": "mac_set_brightness",
        "description": "Set screen brightness (0-100).",
        "params": {"percent": "integer"},
    },
    {
        "name": "mac_toggle_dnd",
        "description": "Toggle Do Not Disturb / Focus mode on macOS.",
        "params": {},
    },
    {
        "name": "mac_open_path",
        "description": "Open a file path or URL in the default app via macOS 'open'.",
        "params": {"path": "string"},
    },
    {
        "name": "mac_spotlight",
        "description": "Search the macOS Spotlight index for files matching a query (returns up to 20 paths).",
        "params": {"query": "string"},
    },
    {
        "name": "mac_screenshot",
        "description": "Take a screenshot. Target: 'screen' (full screen), 'window' (interactive window pick), or 'selection' (rectangular crop). Returns the saved PNG path.",
        "params": {"target": "string"},
    },
    {
        "name": "mac_clipboard_get",
        "description": "Read the current macOS clipboard contents as text.",
        "params": {},
    },
    {
        "name": "mac_clipboard_set",
        "description": "Replace the macOS clipboard contents with the given text.",
        "params": {"text": "string"},
    },
    {
        "name": "mac_list_shortcuts",
        "description": "List all macOS Shortcuts.app shortcuts available to the user.",
        "params": {},
    },
    {
        "name": "mac_run_shortcut",
        "description": "Run a macOS Shortcut by name, optionally with an input string. Returns the shortcut's stdout.",
        "params": {"name": "string", "input": "string"},
    },
]

# Mapping from our shorthand type names to JSON Schema types.
_TYPE_MAP: Final[dict[str, str]] = {
    "string": "string",
    "boolean": "boolean",
    "number": "number",
    "integer": "integer",
}


# ---------------------------------------------------------------------------
# Format converters
# ---------------------------------------------------------------------------

def _build_json_schema(params: dict[str, str]) -> dict[str, Any]:
    """Convert a ``{name: type_str}`` dict into a JSON Schema object.

    Returns a ``{"type": "object", "properties": {...}, "required": [...]}``
    dict suitable for both OpenAI and Anthropic tool schemas.
    """
    if not params:
        return {"type": "object", "properties": {}}

    properties: dict[str, dict[str, str]] = {}
    required: list[str] = []

    for param_name, param_type in params.items():
        json_type = _TYPE_MAP.get(param_type, "string")
        properties[param_name] = {"type": json_type}
        required.append(param_name)

    schema: dict[str, Any] = {
        "type": "object",
        "properties": properties,
    }
    if required:
        schema["required"] = required

    return schema


def get_ollama_tools() -> list[dict[str, Any]]:
    """Return tool definitions in Ollama / OpenAI function-calling format.

    Each entry looks like::

        {
            "type": "function",
            "function": {
                "name": "focus_session",
                "description": "Focus a terminal session by project name",
                "parameters": {
                    "type": "object",
                    "properties": {"project": {"type": "string"}},
                    "required": ["project"]
                }
            }
        }
    """
    tools: list[dict[str, Any]] = []
    for defn in TOOL_DEFINITIONS:
        tools.append({
            "type": "function",
            "function": {
                "name": defn["name"],
                "description": defn["description"],
                "parameters": _build_json_schema(defn["params"]),
            },
        })
    return tools


def get_anthropic_tools() -> list[dict[str, Any]]:
    """Return tool definitions in Anthropic function-calling format.

    Each entry looks like::

        {
            "name": "focus_session",
            "description": "Focus a terminal session by project name",
            "input_schema": {
                "type": "object",
                "properties": {"project": {"type": "string"}},
                "required": ["project"]
            }
        }
    """
    tools: list[dict[str, Any]] = []
    for defn in TOOL_DEFINITIONS:
        tools.append({
            "name": defn["name"],
            "description": defn["description"],
            "input_schema": _build_json_schema(defn["params"]),
        })
    return tools


def get_tool_names() -> list[str]:
    """Return a flat list of all defined tool names."""
    return [defn["name"] for defn in TOOL_DEFINITIONS]


# ---------------------------------------------------------------------------
# Tool executor
# ---------------------------------------------------------------------------

# Type alias for the async send function passed to ToolExecutor.
type WsSendFn = Callable[[dict[str, Any]], Awaitable[None]]


class ToolExecutor:
    """Sends tool calls to the Go app via WebSocket and waits for results.

    The executor keeps a dict of pending futures keyed by call ID.  When
    ``execute()`` is called it sends a ``tool_call`` message over the WS
    connection and awaits the matching future.  The Go app sends back a
    ``tool_result`` message which is fed into ``handle_result()`` to resolve
    the future.

    Usage::

        executor = ToolExecutor(ws_send_fn=ws.send_json)

        # In the voice pipeline:
        result = await executor.execute("focus_session", {"project": "maya-web"})

        # In the WS message handler:
        if msg["type"] == "tool_result":
            executor.handle_result(msg)
    """

    def __init__(self, ws_send_fn: WsSendFn) -> None:
        """Initialise the executor.

        Args:
            ws_send_fn: Async callable that sends a dict over the WebSocket
                connection (e.g. ``ws.send`` wrapped to JSON-encode, or a
                helper that calls ``ws.send(json.dumps(payload))``).
        """
        self._send: WsSendFn = ws_send_fn
        self._pending: dict[str, asyncio.Future[dict[str, Any]]] = {}

    @property
    def pending_count(self) -> int:
        """Number of tool calls currently awaiting a result."""
        return len(self._pending)

    async def _call_wails(
        self,
        method: str,
        args: list[Any] | dict[str, Any] | None = None,
        *,
        timeout: float = 10.0,
    ) -> dict[str, Any]:
        """Invoke a Wails-bound method on the Go App by its CamelCase name.

        Sends a ``tool_call`` WS message with ``name`` set to the Wails
        method (e.g. ``SpotifySearchAndPlay``) and awaits the Go app's
        ``tool_result``. The result is normalised into a
        ``{"result": <value>, "error": <str|None>}`` shape so callers
        (the spotify_* / mac_* executor branches) can treat success and
        failure uniformly without having to know the raw shape Go sends.

        Args:
            method: Wails method name in CamelCase (e.g. ``SpotifyPause``).
            args: Positional args as a list, or kwargs as a dict.  Defaults
                to ``[]``.
            timeout: Max seconds to wait for the result.

        Returns:
            Dict with two keys:
              - ``result``: The successful return value from Go (string,
                dict, list, or None).
              - ``error``: A string describing the failure, or ``None``
                when the call succeeded.

            Never raises -- transport, timeout and protocol failures are
            all surfaced via the ``error`` key so the calling branch can
            shape a voice-friendly message.
        """
        if args is None:
            args = []

        call_id = str(uuid.uuid4())
        loop = asyncio.get_running_loop()
        future: asyncio.Future[dict[str, Any]] = loop.create_future()
        self._pending[call_id] = future

        payload: dict[str, Any] = {
            "type": "tool_call",
            "id": call_id,
            "name": method,
            "args": args,
        }

        logger.debug("wails_call -> %s(%s) id=%s", method, args, call_id)

        try:
            await self._send(payload)
        except Exception as exc:
            self._pending.pop(call_id, None)
            logger.exception("Failed to send wails_call for %s", method)
            return {"result": None, "error": f"transport: {exc}"}

        try:
            raw = await asyncio.wait_for(future, timeout=timeout)
        except asyncio.TimeoutError:
            logger.warning(
                "Wails call %s timed out after %.1fs (id=%s)", method, timeout, call_id
            )
            return {"result": None, "error": f"timeout after {timeout}s"}
        finally:
            self._pending.pop(call_id, None)

        # Normalise the response. Go-side tool_result messages may come back
        # in a few shapes; accept any of them:
        #   1. {"result": ..., "error": "..."}        -- already normalised
        #   2. {"ok": True,  "message": "..."}        -- daemon-style ok
        #   3. {"ok": False, "error":   "..."}        -- daemon-style err
        #   4. {"ok": False, "message": "..."}        -- daemon-style err (alt)
        #   5. a bare string / scalar                 -- treat as success value
        if isinstance(raw, dict):
            if "error" in raw and "result" in raw:
                return {"result": raw.get("result"), "error": raw.get("error")}
            if raw.get("ok") is False:
                err = raw.get("error") or raw.get("message") or "unknown error"
                return {"result": None, "error": str(err)}
            # ok=True or no ok key — treat message/result as the success value.
            value = raw.get("result")
            if value is None:
                value = raw.get("message")
            return {"result": value, "error": None}

        return {"result": raw, "error": None}

    async def execute(
        self,
        name: str,
        args: dict[str, Any] | None = None,
        *,
        timeout: float = 10.0,
    ) -> dict[str, Any]:
        """Send a tool call to the Go app and wait for the result.

        Args:
            name: Tool name (must match one of ``TOOL_DEFINITIONS``).
            args: Tool arguments dict.  Defaults to ``{}`` for no-arg tools.
            timeout: Maximum seconds to wait for a result.

        Returns:
            The ``result`` dict from the Go app's ``tool_result`` message.
            On timeout, returns ``{"ok": False, "error": "..."}``.
        """
        if args is None:
            args = {}

        # -----------------------------------------------------------------
        # v0.3.0 -- Spotify tools (TASK-010 executor branches)
        # -----------------------------------------------------------------
        # These tools have shaped behaviour (friendly empty-arg messages,
        # ErrNotAuthenticated surfacing, "not yet wired" placeholders) so
        # they short-circuit before the generic WS dispatch below.
        # The 6 placeholders never touch the WS at all so they're safe to
        # call against a no-op ws_send_fn during unit tests.
        if name == "spotify_search_and_play":
            query = str(args.get("query", "")).strip()
            if not query:
                return {"ok": False, "message": "I need a song name to play."}
            result = await self._call_wails(
                "SpotifySearchAndPlay", [query], timeout=timeout
            )
            if result.get("error"):
                err_text = str(result["error"])
                if (
                    "ErrNotAuthenticated" in err_text
                    or "not authenticated" in err_text.lower()
                ):
                    return {
                        "ok": False,
                        "message": "I need you to connect Spotify first — open Settings → Connections.",
                    }
                return {"ok": False, "message": f"Couldn't play that: {err_text}"}
            return {"ok": True, "message": result.get("result") or "Playing."}

        if name == "spotify_pause":
            result = await self._call_wails("SpotifyPause", [], timeout=timeout)
            err = result.get("error")
            if err is None:
                return {"ok": True, "message": "Paused."}
            return {"ok": False, "message": f"Couldn't pause: {err}"}

        if name == "spotify_resume":
            result = await self._call_wails("SpotifyResume", [], timeout=timeout)
            err = result.get("error")
            if err is None:
                return {"ok": True, "message": "Resuming."}
            return {"ok": False, "message": f"Couldn't resume: {err}"}

        # Six Spotify tools whose Go-side AppleScript helpers exist in
        # internal/spotify but aren't exposed as Wails bindings yet
        # (skip/previous/now-playing/set-volume/like/queue). Return a
        # friendly "landing soon" message instead of crashing or sending
        # a tool_call the Go side can't route.
        if name in {
            "spotify_skip",
            "spotify_previous",
            "spotify_what_is_playing",
            "spotify_set_volume",
            "spotify_like_current",
            "spotify_queue",
        }:
            action = name.removeprefix("spotify_").replace("_", " ")
            return {
                "ok": False,
                "message": f"Spotify {action} isn't wired up yet — landing in a follow-up.",
            }

        call_id = str(uuid.uuid4())
        loop = asyncio.get_running_loop()
        future: asyncio.Future[dict[str, Any]] = loop.create_future()
        self._pending[call_id] = future

        payload: dict[str, Any] = {
            "type": "tool_call",
            "id": call_id,
            "name": name,
            "args": args,
        }

        logger.debug("tool_call -> %s(%s) id=%s", name, args, call_id)

        try:
            await self._send(payload)
        except Exception:
            self._pending.pop(call_id, None)
            logger.exception("Failed to send tool_call for %s", name)
            return {"ok": False, "error": f"Failed to send tool call: {name}"}

        try:
            result = await asyncio.wait_for(future, timeout=timeout)
            logger.debug("tool_result <- %s id=%s result=%s", name, call_id, result)
            return result
        except asyncio.TimeoutError:
            logger.warning("Tool %s timed out after %.1fs (id=%s)", name, timeout, call_id)
            return {"ok": False, "error": f"Tool {name} timed out after {timeout}s"}
        finally:
            self._pending.pop(call_id, None)

    def handle_result(self, msg: dict[str, Any]) -> None:
        """Resolve the pending future for an incoming ``tool_result`` message.

        Called by the WS message handler when a ``tool_result`` message
        arrives from the Go app.  If the ``id`` doesn't match any pending
        call, a warning is logged and the message is dropped.

        Args:
            msg: The full ``tool_result`` message dict with ``id`` and
                ``result`` keys.
        """
        call_id = msg.get("id")
        if not call_id:
            logger.warning("tool_result message missing 'id' field: %s", msg)
            return

        future = self._pending.get(call_id)
        if future is None:
            logger.warning(
                "Received tool_result for unknown id=%s (may have timed out)",
                call_id,
            )
            return

        if future.done():
            logger.warning(
                "Future for id=%s already resolved (duplicate result?)",
                call_id,
            )
            return

        result = msg.get("result", {})
        future.set_result(result)

    async def execute_all(
        self,
        tool_calls: list[dict[str, Any]],
        *,
        timeout: float = 10.0,
    ) -> list[dict[str, Any]]:
        """Execute multiple tool calls sequentially and return all results.

        Each call is awaited before the next begins.  This is intentional --
        tool calls often have side effects and ordering matters (e.g. stage
        then commit then push).

        Args:
            tool_calls: List of dicts with ``name`` and optional ``args`` keys.
            timeout: Per-call timeout in seconds.

        Returns:
            List of ``{"name": str, "result": dict}`` in the same order as
            the input.
        """
        results: list[dict[str, Any]] = []
        for tc in tool_calls:
            name = tc.get("name", "")
            args = tc.get("args", {})
            if not name:
                logger.warning("Skipping tool call with empty name: %s", tc)
                results.append({"name": "", "result": {"ok": False, "error": "Empty tool name"}})
                continue
            result = await self.execute(name, args, timeout=timeout)
            results.append({"name": name, "result": result})
        return results

    def cancel_all(self) -> int:
        """Cancel all pending futures.  Returns the number cancelled.

        Useful during shutdown to unblock any ``execute()`` calls that are
        still waiting.
        """
        count = 0
        for call_id, future in list(self._pending.items()):
            if not future.done():
                future.cancel()
                count += 1
            self._pending.pop(call_id, None)
        if count:
            logger.info("Cancelled %d pending tool call(s)", count)
        return count
