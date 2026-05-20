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

        # v0.3.0/TASK-018 -- confirmation gate state. When a destructive
        # tool's policy is "ask", ``_confirm`` parks an asyncio Future
        # here and emits a ``confirmation_required`` WS event. The
        # transcript router on the daemon side (main.py) calls
        # ``resolve_pending_confirmation`` on every final user utterance.
        # The first yes/no synonym resolves the future; non-matching
        # speech is ignored so the user can think before answering.
        self._pending_confirmation: asyncio.Future[bool] | None = None
        self._pending_confirmation_tool: str | None = None

        # Per-``execute`` policy cache. Populated lazily on the first
        # mac_* dispatch within a single call so Settings UI edits take
        # effect for the next voice command without a daemon restart.
        self._policy_cache: dict[str, str] | None = None

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

        # v0.3.0/TASK-015 -- mac_* executor dispatch.
        macctl_result = await self._maybe_execute_macctl(name, args)
        if macctl_result is not None:
            return macctl_result

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

    # ------------------------------------------------------------------
    # v0.3.0/TASK-018 -- confirmation gate.
    # ------------------------------------------------------------------
    # A tool whose policy is "ask" must surface a yes/no question to the
    # user before we invoke the Wails method. We:
    #   1. Park an ``asyncio.Future[bool]`` on the executor.
    #   2. Emit a ``confirmation_required`` WS event so the Mac UI can
    #      show a banner AND the TTS pipeline speaks the question.
    #   3. Wait up to ``timeout`` seconds for the transcript router
    #      (main.py) to call ``resolve_pending_confirmation`` with the
    #      user's reply.
    #
    # On timeout we default to deny ("Got it, skipping.") which matches
    # the spec's safe-by-default stance.
    #
    # The Go-side policy check inside each macctl controller method is
    # still active as a second layer of defense -- if the user changes
    # policy from "ask" to "deny" between the question and the Wails
    # call, Go will refuse and the dispatch branch will surface
    # ErrPolicyDeny as a denial message.
    # ------------------------------------------------------------------

    async def _confirm(
        self,
        tool: str,
        question: str,
        *,
        timeout: float = 30.0,
    ) -> bool:
        """Ask the user via TTS and await yes/no on the transcript pipe.

        Args:
            tool: Tool name being confirmed (e.g. ``"mac_quit_app"``).
            question: Friendly question to speak (e.g. ``"Quit Slack?"``).
            timeout: Max seconds to wait before defaulting to deny.

        Returns:
            ``True`` when the user explicitly affirmed, ``False`` on
            explicit deny OR on timeout. Never raises -- transport
            errors are treated as deny.
        """
        loop = asyncio.get_running_loop()
        fut: asyncio.Future[bool] = loop.create_future()

        # Replace any prior pending confirmation. In normal operation
        # there should be at most one outstanding -- ``execute`` serialises
        # tool calls -- but if one was abandoned we refuse it now.
        prev = self._pending_confirmation
        if prev is not None and not prev.done():
            prev.set_result(False)

        self._pending_confirmation = fut
        self._pending_confirmation_tool = tool

        payload: dict[str, Any] = {
            "type": "confirmation_required",
            "tool": tool,
            "question": question,
            "timeout_seconds": timeout,
        }

        try:
            try:
                await self._send(payload)
            except Exception:
                logger.exception(
                    "Failed to emit confirmation_required for %s -- defaulting to deny",
                    tool,
                )
                return False

            try:
                return await asyncio.wait_for(fut, timeout=timeout)
            except asyncio.TimeoutError:
                logger.info(
                    "Confirmation for %s timed out after %.1fs -- defaulting to deny",
                    tool,
                    timeout,
                )
                return False
        finally:
            # Only clear if this future is still the active one.
            if self._pending_confirmation is fut:
                self._pending_confirmation = None
                self._pending_confirmation_tool = None

    def resolve_pending_confirmation(self, transcript_text: str) -> bool:
        """Resolve an outstanding ``_confirm`` future from user transcript.

        Called by the transcript router (``main.py``) on every final user
        utterance. Matches a small set of yes/no synonyms; any other
        speech leaves the future pending so the user can talk before
        deciding.

        Args:
            transcript_text: Final user transcript text.

        Returns:
            ``True`` when the transcript was a yes/no answer that resolved
            the pending confirmation. ``False`` if there was no pending
            confirmation, the future was already done, or the text was
            not a recognised yes/no synonym.
        """
        fut = self._pending_confirmation
        if fut is None or fut.done():
            return False

        lower = transcript_text.strip().lower().rstrip(".!?")
        if not lower:
            return False

        # Hand-picked synonyms covering common confirmations users speak
        # in practice. Anything else (including "maybe", "wait") leaves
        # the future pending so the user can think out loud.
        if lower in {
            "yes",
            "yeah",
            "yep",
            "yup",
            "sure",
            "go ahead",
            "do it",
            "ok",
            "okay",
            "affirmative",
            "confirm",
            "confirmed",
            "please do",
        }:
            fut.set_result(True)
            return True
        if lower in {
            "no",
            "nope",
            "nah",
            "cancel",
            "stop",
            "don't",
            "do not",
            "skip",
            "skip it",
            "negative",
            "abort",
            "never mind",
        }:
            fut.set_result(False)
            return True
        return False

    def _confirmation_question(
        self, tool: str, params: dict[str, Any]
    ) -> str:
        """Return a short voice-friendly question for a destructive tool."""
        if tool == "mac_quit_app":
            return f"Quit {params.get('name', 'the app')}?"
        if tool == "mac_open_app":
            return f"Open {params.get('name', 'the app')}?"
        if tool == "mac_set_volume":
            pct = params.get("pct", params.get("percent", "?"))
            return f"Set volume to {pct}%?"
        if tool == "mac_set_brightness":
            pct = params.get("pct", params.get("percent", "?"))
            return f"Set brightness to {pct}%?"
        if tool == "mac_clipboard_set":
            return "Replace your clipboard?"
        if tool == "mac_open_path":
            return f"Open {params.get('path', params.get('url', 'that'))}?"
        if tool == "mac_run_shortcut":
            shortcut_name = (params.get("name") or "").strip()
            if shortcut_name:
                return f"Run the {shortcut_name} shortcut?"
            return "Run that shortcut?"
        if tool == "mac_toggle_dnd":
            return "Toggle Do Not Disturb?"
        if tool == "mac_focus_window":
            return f"Focus {params.get('app', 'the app')}?"
        if tool == "mac_mute":
            return "Mute the system?"
        if tool == "mac_unmute":
            return "Unmute the system?"
        if tool == "mac_screenshot":
            return "Take a screenshot?"
        # Generic fallback for new mac_* tools.
        return f"Run {tool.replace('_', ' ')}?"

    async def _policy_get(self, tool: str) -> str:
        """Return the current policy decision for ``tool``.

        Decisions are one of ``"allow"``, ``"ask"`` or ``"deny"``. Result
        is cached per-``execute`` call so multiple dispatches in a single
        call don't ping Go for every check.

        Returns ``"ask"`` as the safe default when the policy can't be
        fetched (Go-side error, missing tool entry, malformed response).
        """
        if self._policy_cache is None:
            result = await self._call_wails("GetMacctlPolicy", [])
            err = result.get("error")
            policy_obj: dict[str, str] = {}
            if err is None:
                value = result.get("result")
                if isinstance(value, dict):
                    # Policy may surface as a flat ``{tool: decision}``
                    # map or nested under a ``tools`` key depending on
                    # how Go serialises it. Accept both.
                    nested = (
                        value.get("tools")
                        if isinstance(value.get("tools"), dict)
                        else None
                    )
                    src = nested if nested is not None else value
                    for k, v in src.items():
                        if isinstance(v, str):
                            policy_obj[str(k)] = v.lower()
            else:
                logger.debug(
                    "GetMacctlPolicy failed (%s) -- defaulting to ask", err
                )
            self._policy_cache = policy_obj

        decision = self._policy_cache.get(tool, "ask").lower()
        if decision not in ("allow", "ask", "deny"):
            decision = "ask"
        return decision

    async def _maybe_confirm_destructive(
        self,
        tool: str,
        params: dict[str, Any],
    ) -> dict[str, Any] | None:
        """Apply the confirmation gate before invoking a destructive tool.

        Returns:
          - ``None`` -- policy is ``allow``; caller should proceed.
          - ``{"ok": False, "message": "..."}`` -- policy is ``deny``,
            or the user denied / timed out. Caller should return this
            verbatim without invoking the Wails method.

        Go-side ``policy.Check`` still acts as defense-in-depth; this
        gate's job is to surface a *voice* question for the ``ask`` case
        which Go alone can't do.
        """
        decision = await self._policy_get(tool)
        if decision == "allow":
            return None
        if decision == "deny":
            pretty = tool.removeprefix("mac_").replace("_", " ")
            return {
                "ok": False,
                "message": f"I'm not permitted to {pretty}.",
            }

        # decision == "ask"
        question = self._confirmation_question(tool, params)
        approved = await self._confirm(tool, question)
        if approved:
            return None
        # Single friendly response covers explicit "no" and timeout.
        return {"ok": False, "message": "Got it, skipping."}

    # v0.3.0/TASK-015 -- mac_* executor implementation.
    #
    # Lives in its own method (rather than inline in execute()) so the
    # fallback generic dispatch above stays compact and so unit tests can
    # exercise the mac_* surface in isolation by calling
    # ``executor._maybe_execute_macctl(...)`` directly.
    #
    # Returns ``None`` when ``name`` is not a mac_* tool, signalling
    # ``execute`` to continue with the generic WS forwarder. Returns the
    # full ``{"ok": ..., "message": ...}`` envelope otherwise.
    # -----------------------------------------------------------------------
    async def _maybe_execute_macctl(
        self,
        name: str,
        params: dict[str, Any],
    ) -> dict[str, Any] | None:
        """Dispatch mac_* tool calls; return None for non-mac names."""
        if not name.startswith("mac_"):
            return None

        # v0.3.0/TASK-018 -- reset the per-call policy cache. ``execute``
        # serialises calls so populating once per dispatch is safe and
        # lets Settings UI edits take effect for the next voice command.
        self._policy_cache = None

        def _is_policy_deny(err: Any) -> bool:
            err_str = str(err).lower()
            return "errpolicydeny" in err_str or "denied by policy" in err_str

        # ---- Apps + windows ----
        if name == "mac_open_app":
            app_name = (params.get("name") or "").strip()
            if not app_name:
                return {"ok": False, "message": "I need an app name to open."}
            result = await self._call_wails("MacOpenApp", [app_name])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": f"I'm not permitted to open {app_name}."}
                return {"ok": False, "message": f"Couldn't open {app_name}: {err}"}
            return {"ok": True, "message": f"Opened {app_name}."}

        if name == "mac_quit_app":
            app_name = (params.get("name") or "").strip()
            if not app_name:
                return {"ok": False, "message": "I need an app name to quit."}
            # v0.3.0/TASK-018 -- confirmation gate. Demonstrates the
            # ``ask``/``deny`` paths via policy + transcript loop.
            # Returns ``None`` when the call should proceed (policy=allow
            # or user said yes), or a voice-friendly denial envelope.
            gate = await self._maybe_confirm_destructive(
                "mac_quit_app", {"name": app_name}
            )
            if gate is not None:
                return gate
            result = await self._call_wails("MacQuitApp", [app_name])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": f"I'm not permitted to quit {app_name}."}
                return {"ok": False, "message": f"Couldn't quit {app_name}: {err}"}
            return {"ok": True, "message": f"Quit {app_name}."}

        if name == "mac_focus_window":
            app_name = (params.get("app") or "").strip()
            title = (params.get("title") or "").strip()
            if not app_name or not title:
                return {
                    "ok": False,
                    "message": "I need both an app and a window title to focus.",
                }
            result = await self._call_wails("MacFocusWindow", [app_name, title])
            err = result.get("error")
            if err:
                err_str = str(err).lower()
                if _is_policy_deny(err):
                    return {
                        "ok": False,
                        "message": f"I'm not permitted to focus {app_name} windows.",
                    }
                if "window not found" in err_str or "errwindownotfound" in err_str:
                    return {
                        "ok": False,
                        "message": f"I couldn't find a {app_name} window matching '{title}'.",
                    }
                return {"ok": False, "message": f"Couldn't focus {app_name}: {err}"}
            return {"ok": True, "message": f"Focused {app_name}."}

        # ---- Audio + display ----
        if name == "mac_set_volume":
            raw_pct = params.get("pct", params.get("percent"))
            try:
                pct = int(raw_pct)
            except (TypeError, ValueError):
                return {
                    "ok": False,
                    "message": "I need a volume percentage between 0 and 100.",
                }
            if pct < 0 or pct > 100:
                return {
                    "ok": False,
                    "message": "Volume must be between 0 and 100.",
                }
            result = await self._call_wails("MacSetVolume", [pct])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": "I'm not permitted to change the volume."}
                return {"ok": False, "message": f"Couldn't set volume: {err}"}
            return {"ok": True, "message": f"Volume set to {pct}."}

        if name == "mac_mute":
            result = await self._call_wails("MacMute", [])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": "I'm not permitted to mute the system."}
                return {"ok": False, "message": f"Couldn't mute: {err}"}
            return {"ok": True, "message": "Muted."}

        if name == "mac_unmute":
            result = await self._call_wails("MacUnmute", [])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": "I'm not permitted to unmute the system."}
                return {"ok": False, "message": f"Couldn't unmute: {err}"}
            return {"ok": True, "message": "Unmuted."}

        if name == "mac_set_brightness":
            raw_pct = params.get("pct", params.get("percent"))
            try:
                pct = int(raw_pct)
            except (TypeError, ValueError):
                return {
                    "ok": False,
                    "message": "I need a brightness percentage between 0 and 100.",
                }
            if pct < 0 or pct > 100:
                return {
                    "ok": False,
                    "message": "Brightness must be between 0 and 100.",
                }
            result = await self._call_wails("MacSetBrightness", [pct])
            err = result.get("error")
            if err:
                err_str = str(err).lower()
                if _is_policy_deny(err):
                    return {
                        "ok": False,
                        "message": "I'm not permitted to change the brightness.",
                    }
                if "errtoolunavailable" in err_str or "tool unavailable" in err_str:
                    return {
                        "ok": False,
                        "message": (
                            "The brightness CLI isn't installed -- "
                            "install it with `brew install brightness` first."
                        ),
                    }
                return {"ok": False, "message": f"Couldn't set brightness: {err}"}
            return {"ok": True, "message": f"Brightness set to {pct}."}

        if name == "mac_toggle_dnd":
            result = await self._call_wails("MacToggleDND", [])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": "I'm not permitted to toggle Do Not Disturb."}
                return {"ok": False, "message": f"Couldn't toggle Do Not Disturb: {err}"}
            return {"ok": True, "message": "Toggled Do Not Disturb."}

        # ---- Files + spotlight + screenshots ----
        if name == "mac_open_path":
            path = (params.get("path") or params.get("url") or "").strip()
            if not path:
                return {"ok": False, "message": "I need a path or URL to open."}
            result = await self._call_wails("MacOpenPath", [path])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": f"I'm not permitted to open {path}."}
                return {"ok": False, "message": f"Couldn't open {path}: {err}"}
            return {"ok": True, "message": f"Opened {path}."}

        if name == "mac_spotlight":
            query = (params.get("query") or "").strip()
            if not query:
                return {"ok": False, "message": "I need a search query."}
            result = await self._call_wails("MacSpotlight", [query])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": "I'm not permitted to run Spotlight searches."}
                return {"ok": False, "message": f"Spotlight failed: {err}"}
            hits = result.get("result") or ""
            count = len([line for line in str(hits).splitlines() if line.strip()])
            if count == 0:
                return {"ok": True, "message": f"No results for '{query}'.", "results": ""}
            return {
                "ok": True,
                "message": f"Found {count} result{'s' if count != 1 else ''} for '{query}'.",
                "results": hits,
            }

        if name == "mac_screenshot":
            target = (params.get("target") or "screen").strip().lower()
            if target not in ("screen", "window", "selection"):
                return {
                    "ok": False,
                    "message": "Screenshot target must be screen, window, or selection.",
                }
            result = await self._call_wails("MacScreenshot", [target])
            err = result.get("error")
            if err:
                err_str = str(err).lower()
                if _is_policy_deny(err):
                    return {"ok": False, "message": "I'm not permitted to take screenshots."}
                if "user cancelled" in err_str:
                    return {
                        "ok": False,
                        "message": "I didn't capture anything -- let me know if you want to try again.",
                    }
                return {"ok": False, "message": f"Screenshot failed: {err}"}
            path = result.get("result") or ""
            return {"ok": True, "message": "Took a screenshot.", "path": path}

        # ---- Clipboard ----
        if name == "mac_clipboard_get":
            result = await self._call_wails("MacClipboardGet", [])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": "I'm not permitted to read the clipboard."}
                return {"ok": False, "message": f"Couldn't read clipboard: {err}"}
            content = result.get("result") or ""
            if not str(content).strip():
                return {"ok": True, "message": "Your clipboard is empty.", "content": ""}
            return {"ok": True, "message": "Read the clipboard.", "content": content}

        if name == "mac_clipboard_set":
            text = params.get("text")
            if text is None:
                return {"ok": False, "message": "I need some text to copy to the clipboard."}
            result = await self._call_wails("MacClipboardSet", [str(text)])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {"ok": False, "message": "I'm not permitted to write to the clipboard."}
                return {"ok": False, "message": f"Couldn't set clipboard: {err}"}
            return {"ok": True, "message": "Copied to the clipboard."}

        # ---- Shortcuts ----
        if name == "mac_list_shortcuts":
            result = await self._call_wails("MacListShortcuts", [])
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {
                        "ok": False,
                        "message": "I'm not permitted to list shortcuts.",
                    }
                return {"ok": False, "message": f"Couldn't list shortcuts: {err}"}
            shortcuts = result.get("result") or []
            if not isinstance(shortcuts, list):
                shortcuts = []
            count = len(shortcuts)
            if count == 0:
                return {
                    "ok": True,
                    "message": "You don't have any Shortcuts installed.",
                    "shortcuts": [],
                }
            return {
                "ok": True,
                "message": f"You have {count} shortcut{'s' if count != 1 else ''}.",
                "shortcuts": shortcuts,
            }

        if name == "mac_run_shortcut":
            shortcut_name = (params.get("name") or "").strip()
            if not shortcut_name:
                return {"ok": False, "message": "I need a shortcut name to run."}
            shortcut_input = params.get("input") or ""
            # Long-running shortcuts may take more than the default 10s
            # bridge timeout (e.g. a "Take Note" shortcut that opens Notes
            # before returning). Bump the per-call timeout to 30s.
            result = await self._call_wails(
                "MacRunShortcut",
                [shortcut_name, str(shortcut_input)],
                timeout=30.0,
            )
            err = result.get("error")
            if err:
                if _is_policy_deny(err):
                    return {
                        "ok": False,
                        "message": f"I'm not permitted to run the {shortcut_name} shortcut.",
                    }
                return {
                    "ok": False,
                    "message": f"Couldn't run {shortcut_name}: {err}",
                }
            output = result.get("result") or ""
            return {
                "ok": True,
                "message": f"Ran the {shortcut_name} shortcut.",
                "output": str(output),
            }

        # Unknown mac_* tool -- fall through to the generic forwarder so a
        # future tool added to TOOL_DEFINITIONS works without an explicit
        # branch here. The Go-side dispatcher will return an "unknown tool"
        # error if it doesn't recognise the name either.
        return None

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


