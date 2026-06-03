"""Tool bridge for Pipecat — routes LLM tool calls to the appropriate backend.

Pipecat's AnthropicLLMService calls registered functions when Claude returns
tool_use blocks. This bridge registers handlers for all tools and dispatches
to: Go WebSocket (sessions, git, approvals), MCP servers (Slack, GitHub),
or local handlers (research, briefing, memory search, shell, files).

Slow tools (research, browse_url) run asynchronously — the LLM gets an
immediate acknowledgment and the real result is injected into the
conversation when it's ready.
"""
from __future__ import annotations

import asyncio
import fnmatch
import json
import logging
import os
import re
import shlex
from typing import Any, Callable, Final

logger: Final = logging.getLogger("jarvis-daemon.tool_bridge")

# ---------------------------------------------------------------------------
# Security helpers
# ---------------------------------------------------------------------------

_SECRET_PATTERNS: Final[list[re.Pattern[str]]] = [
    re.compile(r"sk-or-[\w\-]{20,}"),
    re.compile(r"sk-ant-[\w\-]{20,}"),
    re.compile(r"sk-[\w\-]{20,}"),
    re.compile(r"xoxb-[\w\-]{20,}"),
    re.compile(r"xoxp-[\w\-]{20,}"),
    re.compile(r"ghp_[\w]{20,}"),
    re.compile(r"gho_[\w]{20,}"),
    re.compile(r"github_pat_[\w]{20,}"),
    re.compile(r"AKIA[\w]{16,}"),
    re.compile(r"Bearer\s+[\w\-\.]{20,}"),
]


def _redact_secrets(text: str) -> str:
    """Replace known secret patterns with [REDACTED]."""
    for pat in _SECRET_PATTERNS:
        text = pat.sub("[REDACTED]", text)
    return text


_project_roots_cache: list[str] | None = None


def _load_project_roots() -> list[str]:
    """Load projectRootPaths from ~/.awm/config.json. Cached after first call."""
    global _project_roots_cache  # noqa: PLW0603
    if _project_roots_cache is not None:
        return _project_roots_cache

    config_path = os.path.expanduser("~/.awm/config.json")
    try:
        with open(config_path) as f:
            cfg = json.load(f)
        raw = cfg.get("projectRootPaths")
        if isinstance(raw, list):
            _project_roots_cache = [
                os.path.realpath(os.path.expanduser(p))
                for p in raw
                if isinstance(p, str) and p.strip()
            ]
        else:
            _project_roots_cache = []
    except (OSError, json.JSONDecodeError) as exc:
        logger.warning("Failed to read project roots from config: %s", exc)
        _project_roots_cache = []
    return _project_roots_cache


_BANNED_PATH_SEGMENTS: Final[set[str]] = {".ssh", ".aws", ".gnupg"}
_BANNED_EXACT_PATHS: Final[set[str]] = {
    os.path.realpath(os.path.expanduser("~/.awm/config.json")),
}


def _is_path_allowed(path: str, project_roots: list[str]) -> bool:
    """Return True only if *path* is under an allowed project root.

    Always returns False for sensitive directories (.ssh, .aws, .gnupg)
    and for the AWM config file itself.
    """
    resolved = os.path.realpath(os.path.expanduser(path))

    # Block sensitive path segments anywhere in the resolved path.
    parts = resolved.split(os.sep)
    for segment in parts:
        if segment in _BANNED_PATH_SEGMENTS:
            return False

    # Block exact sensitive files.
    if resolved in _BANNED_EXACT_PATHS:
        return False

    # Must be under at least one project root.
    for root in project_roots:
        if resolved == root or resolved.startswith(root + os.sep):
            return True
    return False


# ---------------------------------------------------------------------------
# Shell command security
# ---------------------------------------------------------------------------

_SHELL_WHITELIST: Final[set[str]] = {
    "ls", "head", "tail", "find", "grep", "wc", "du", "df",
    "ps", "which", "date", "whoami", "pwd", "tree",
}

_SHELL_BLOCKED: Final[set[str]] = {
    "cat", "env", "echo", "curl", "wget", "rm", "mv",
    "sudo", "kill", "chmod",
}

# Commands that accept path arguments which need sandbox validation.
_PATH_COMMANDS: Final[set[str]] = {
    "ls", "find", "grep", "head", "tail", "du", "tree",
}

_DANGEROUS_PIPE_TARGETS: Final[set[str]] = {"rm", "sudo", "kill", "mv"}

_BLOCKED_FILE_PATTERNS: Final[list[str]] = [
    "*.pem", "*.key", "*.p12", "*.pfx",
    ".env", ".env.*",
    "*credentials*", "*secret*", "*token*",
    "id_rsa", "id_ed25519",
]

# Tools dispatched to the Go app via WebSocket.
GO_TOOLS: Final[set[str]] = {
    "approve_session", "approve_all", "deny_session",
    "focus_session", "focus_app", "send_to_terminal",
    "get_status", "navigate_view",
    "git_stage", "git_commit", "git_push",
    "open_url", "read_session_output",
    "highlight_hud_panel",
    "plan_task", "create_todo", "complete_todo",
    "run_workflow", "launch_session",
    # TASK-001: Discovery
    "discover_projects",
    # TASK-003: Discovery and repo info
    "search_repos", "get_repo_info",
    # TASK-004: Git diff
    "get_repo_diff", "get_staged_diff",
    # TASK-005: Git advanced ops
    "git_create_branch", "git_stash", "git_stash_list",
    "git_stash_apply", "git_discard_file",
    # TASK-009: Session management
    "stop_session", "broadcast_to_all", "open_pr",
    # TASK-010: Workspace and orchestration
    "create_workspace", "divide_and_conquer",
    "get_impact_warnings", "launch_from_template",
}

# Tools handled locally in the daemon.
LOCAL_TOOLS: Final[set[str]] = {
    "research", "get_briefing", "check_slack",
    "see_screen", "browse_url",
    "run_shell", "read_file", "get_clipboard",
}

# Tools that run in the background — LLM gets an immediate ack and the
# real result is injected into the conversation asynchronously.
DEFERRED_TOOLS: Final[set[str]] = {
    "research",
    "browse_url",
}


class DeferredResultQueue:
    """Collects background tool results and injects them into the conversation.

    Slow tools return an immediate "checking…" ack to the LLM so Jarvis can
    keep talking. The real result runs in a background task and lands here.
    A consumer loop waits for a natural pause (bot idle) then injects the
    result as a user message and triggers a new LLM turn, so Jarvis
    naturally weaves it into the conversation.
    """

    def __init__(
        self,
        pipeline_task: Any = None,
        get_state_fn: Callable[[], str] | None = None,
    ):
        self._task = pipeline_task
        self._get_state = get_state_fn or (lambda: "idle")
        self._queue: asyncio.Queue[tuple[str, dict, Any]] = asyncio.Queue()
        self._bg_task: asyncio.Task[None] | None = None

    def start(self) -> None:
        self._bg_task = asyncio.create_task(
            self._consumer_loop(), name="deferred-result-injector"
        )
        logger.info("Deferred result injector started")

    async def stop(self) -> None:
        if self._bg_task:
            self._bg_task.cancel()
            try:
                await self._bg_task
            except asyncio.CancelledError:
                pass

    async def put(self, tool_name: str, args: dict, result: Any) -> None:
        await self._queue.put((tool_name, args, result))

    async def _consumer_loop(self) -> None:
        from pipecat.frames.frames import LLMMessagesAppendFrame, LLMRunFrame

        while True:
            try:
                tool_name, args, result = await self._queue.get()
            except asyncio.CancelledError:
                break

            # Wait for a natural pause — don't interrupt mid-sentence.
            for _ in range(120):  # up to 60 seconds
                state = self._get_state()
                if state in ("idle", "listening", ""):
                    break
                await asyncio.sleep(0.5)

            # Build a concise summary for injection.
            if isinstance(result, dict):
                summary = json.dumps(result, default=str)
            else:
                summary = str(result)
            # Truncate huge results (e.g. full web pages).
            if len(summary) > 2000:
                summary = summary[:2000] + "… (truncated)"

            args_brief = ", ".join(f"{k}={v!r}" for k, v in args.items())[:80]
            msg_text = (
                f"[Background result — {tool_name}({args_brief})]: {summary}"
            )
            logger.info("Injecting deferred result: %s", msg_text[:120])

            try:
                await self._task.queue_frames([
                    LLMMessagesAppendFrame(
                        messages=[{"role": "user", "content": msg_text}]
                    ),
                    LLMRunFrame(),
                ])
            except Exception:
                logger.exception("Failed to inject deferred result")


class ToolBridge:
    """Routes tool calls from Pipecat's LLM to the right execution backend.

    Fast tools (Go WebSocket, approvals, git) run synchronously.
    Slow tools in DEFERRED_TOOLS run in the background — the LLM gets an
    immediate ack and the real result is injected later via DeferredResultQueue.
    """

    def __init__(
        self,
        go_executor: Any = None,
        mcp_manager: Any = None,
        research_agent: Any = None,
        briefing_system: Any = None,
        event_store: Any = None,
        output_poller: Any = None,
        browser: Any = None,
        pipeline_task: Any = None,
        deferred_queue: DeferredResultQueue | None = None,
    ):
        self._go = go_executor
        self._mcp = mcp_manager
        self._research = research_agent
        self._briefing = briefing_system
        self._events = event_store
        self._output = output_poller
        self._browser = browser
        self._pipeline_task = pipeline_task
        self._deferred = deferred_queue

    async def handle_tool_call(self, name: str, args: dict[str, Any]) -> Any:
        """Execute a tool call and return the result.

        Deferred tools return an immediate ack — the real result arrives
        later via the DeferredResultQueue.
        """
        logger.info("Tool call: %s(%s)", name, str(args)[:80])

        # Deferred execution for slow tools.
        if name in DEFERRED_TOOLS and self._deferred is not None:
            asyncio.create_task(self._run_deferred(name, dict(args)))
            return {
                "ok": True,
                "status": "pending",
                "message": f"Working on {name} in the background — result will follow shortly.",
            }

        result = await self._execute_tool(name, args)

        # After sending a command to a session, schedule a follow-up read
        # so Jarvis automatically reports back what the session responded.
        if (
            name == "send_to_terminal"
            and isinstance(result, dict)
            and result.get("ok")
            and self._deferred is not None
        ):
            project = args.get("project", "")
            asyncio.create_task(self._follow_up_read(project))

        return result

    async def _follow_up_read(self, project: str, delay: float = 15.0) -> None:
        """Read session output after a delay and inject into conversation.

        When Jarvis sends a command to a session, the user expects a report
        back. This waits for the session to process, reads the output, and
        injects the result so Jarvis naturally says what happened.
        """
        await asyncio.sleep(delay)
        try:
            result = await self._execute_tool(
                "read_session_output", {"project": project}
            )
        except Exception as e:
            result = {"ok": False, "error": str(e)}
        if self._deferred:
            await self._deferred.put(
                "read_session_output",
                {"project": project, "_follow_up": True},
                result,
            )

    async def _run_deferred(self, name: str, args: dict[str, Any]) -> None:
        """Execute a tool in the background and queue the result."""
        try:
            result = await self._execute_tool(name, args)
        except Exception as e:
            result = {"ok": False, "error": str(e)}
        if self._deferred:
            await self._deferred.put(name, args, result)

    async def _execute_tool(self, name: str, args: dict[str, Any]) -> Any:
        """Actually run a tool (synchronous path)."""
        try:
            # Go tools — dispatch via WebSocket
            if name in GO_TOOLS and self._go:
                return await self._go.execute(name, args)

            # Local tools
            if name == "research":
                query = args.get("query", "")
                if self._research:
                    msg = await self._research.research(query)
                    return {"ok": True, "message": msg}
                return {"ok": False, "message": "Research not available"}

            if name == "get_briefing":
                if self._briefing:
                    text = await self._briefing.get_briefing_text()
                    return {"ok": True, "briefing": text or "All quiet, sir."}
                return {"ok": True, "briefing": "All quiet, sir."}

            if name == "check_slack":
                if self._events:
                    recent = self._events.get_recent(since_minutes=30)
                    slack = [e for e in recent if e.get("source") == "slack"]
                    if slack:
                        msgs = [f"{e.get('type')}: {e.get('title')}" for e in slack[:5]]
                        return {"ok": True, "unreads": msgs}
                return {"ok": True, "unreads": [], "message": "No unread messages"}

            if name == "read_session_output":
                project = args.get("project", "")
                if self._output:
                    summary = self._output.get_summary(project)
                    if summary:
                        return {
                            "ok": True, "project": summary.name,
                            "status": summary.status,
                            "last_action": summary.last_action,
                            "error": summary.error_summary,
                        }
                if self._go:
                    return await self._go.execute(name, args)
                return {"ok": False, "message": "No session data available"}

            # Vision — capture screenshot and inject image into LLM context
            if name == "see_screen":
                from vision import capture_screenshot

                mode = args.get("mode", "screen")
                question = args.get("question", "Describe what you see on the screen.")
                result = await capture_screenshot(mode=mode)
                if not result.get("ok"):
                    return result
                # Inject image into LLM context so the model can "see" it
                if self._pipeline_task is not None:
                    try:
                        from pipecat.frames.frames import LLMMessagesAppendFrame

                        image_message = {
                            "role": "user",
                            "content": [
                                {
                                    "type": "image",
                                    "source": {
                                        "type": "base64",
                                        "media_type": result["media_type"],
                                        "data": result["base64"],
                                    },
                                },
                                {"type": "text", "text": question},
                            ],
                        }
                        await self._pipeline_task.queue_frames(
                            [LLMMessagesAppendFrame(messages=[image_message])]
                        )
                    except Exception as e:
                        logger.warning("Failed to inject image into LLM context: %s", e)
                return {"ok": True, "message": "Screenshot captured, analyzing now."}

            # Browser — open a URL via headless Chromium
            if name == "browse_url":
                url = args.get("url", "")
                if not self._browser:
                    return {"ok": False, "error": "Browser not available"}
                if not self._browser.available:
                    started = await self._browser.start()
                    if not started:
                        return {"ok": False, "error": "Browser failed to start"}
                return await self._browser.open_url(url)

            # Sandboxed local shell command
            if name == "run_shell":
                return await _handle_run_shell(args)

            # Sandboxed file reader
            if name == "read_file":
                return await _handle_read_file(args)

            # Clipboard reader
            if name == "get_clipboard":
                return await _handle_get_clipboard()

            # Slack tools (slack_send, slack_read, etc.) are handled by the
            # MCP Slack server, not locally. They fall through to the MCP
            # dispatch below.

            # MCP tools — dispatch to MCP server
            if self._mcp and self._mcp.is_mcp_tool(name):
                return await self._mcp.call_tool(name, args)

            logger.warning("Unknown tool: %s", name)
            return {"ok": False, "error": f"Unknown tool: {name}"}

        except Exception as e:
            logger.exception("Tool execution failed: %s", name)
            return {"ok": False, "error": str(e)}

    def register_with_pipecat(
        self, llm_service: Any, tool_executor: Any = None
    ) -> None:
        """Register all tool handlers with a Pipecat AnthropicLLMService.

        Pipecat uses ``llm.register_function(name, handler)`` to wire up
        tool call execution.

        ``tool_executor`` is the ``tools.ToolExecutor`` used to dispatch
        any tools declared only in ``tools.py:TOOL_DEFINITIONS`` (spotify_*,
        mac_*). Without it, those tools end up in the advertised LLM schema
        with no handler registered, and Pipecat rejects the call at runtime
        with "tool X has no registered handler".
        """
        all_tools = set(GO_TOOLS) | set(LOCAL_TOOLS)

        if self._mcp:
            for tool in self._mcp.get_all_tools():
                all_tools.add(tool["name"])

        # Pull in spotify_*/mac_*/etc. from tools.py so they get handlers
        # too. These dispatch via ToolExecutor.execute, not handle_tool_call.
        executor_tools: set[str] = set()
        if tool_executor is not None:
            try:
                from tools import TOOL_DEFINITIONS as _EXTRA_DEFS
                for defn in _EXTRA_DEFS:
                    nm = defn.get("name")
                    if nm and nm not in all_tools:
                        executor_tools.add(nm)
            except ImportError:
                logger.debug("tools.py not importable — skipping executor tools")

        for tool_name in all_tools:
            # Pipecat 1.0 passes FunctionCallParams as the first arg to handlers.
            # Extract function_name and arguments from it.
            async def _handler(params, _tn: str = tool_name) -> Any:
                # params may be a FunctionCallParams object (Pipecat 1.0) or a string
                if hasattr(params, "function_name"):
                    name = params.function_name
                    args = params.arguments or {}
                elif hasattr(params, "name"):
                    name = params.name
                    args = getattr(params, "arguments", {}) or {}
                else:
                    name = _tn
                    args = params if isinstance(params, dict) else {}
                result = await self.handle_tool_call(name, args)
                # If Pipecat provides a result_callback, use it to return the result
                if hasattr(params, "result_callback") and params.result_callback:
                    await params.result_callback(result)
                return result

            try:
                llm_service.register_function(tool_name, _handler)
            except Exception:
                logger.debug("Failed to register tool %s with Pipecat", tool_name)

        # Register tools.py-only tools (spotify_*/mac_*) with a handler that
        # dispatches through ToolExecutor.execute. Same FunctionCallParams
        # unwrapping pattern as above.
        for tool_name in executor_tools:
            async def _exec_handler(
                params, _tn: str = tool_name, _exec: Any = tool_executor
            ) -> Any:
                if hasattr(params, "function_name"):
                    name = params.function_name
                    args = params.arguments or {}
                elif hasattr(params, "name"):
                    name = params.name
                    args = getattr(params, "arguments", {}) or {}
                else:
                    name = _tn
                    args = params if isinstance(params, dict) else {}
                logger.info("Tool call (executor): %s(%s)", name, str(args)[:80])
                try:
                    result = await _exec.execute(name, args)
                except Exception as exc:
                    logger.exception("Executor tool failed: %s", name)
                    result = {"ok": False, "error": str(exc)}
                if hasattr(params, "result_callback") and params.result_callback:
                    await params.result_callback(result)
                return result

            try:
                llm_service.register_function(tool_name, _exec_handler)
            except Exception:
                logger.debug(
                    "Failed to register executor tool %s with Pipecat", tool_name
                )

        total = len(all_tools) + len(executor_tools)
        logger.info(
            "Registered %d tools with Pipecat LLM service (%d via bridge, %d via executor)",
            total, len(all_tools), len(executor_tools),
        )


# ---------------------------------------------------------------------------
# Local sandboxed tool handlers
# ---------------------------------------------------------------------------

_MAX_OUTPUT_BYTES: Final[int] = 4096  # 4 KB


async def _handle_run_shell(args: dict[str, Any]) -> dict[str, Any]:
    """Execute a sandboxed shell command with strict allowlisting."""
    command = args.get("command", "").strip()
    if not command:
        return {"ok": False, "error": "Empty command"}

    # Parse the command to validate the first word (the program).
    try:
        tokens = shlex.split(command)
    except ValueError as exc:
        return {"ok": False, "error": f"Invalid command syntax: {exc}"}

    if not tokens:
        return {"ok": False, "error": "Empty command"}

    program = tokens[0].split("/")[-1]  # handle /usr/bin/ls etc.

    if program in _SHELL_BLOCKED:
        return {"ok": False, "error": f"Command blocked: {program}"}
    if program not in _SHELL_WHITELIST:
        return {"ok": False, "error": f"Command not allowed: {program}. Allowed: {', '.join(sorted(_SHELL_WHITELIST))}"}

    # Block shell metacharacters for output redirection / command injection.
    for dangerous in (">", ">>", "$(", "`", ";", "&&", "||", "\n", "\r"):
        if dangerous in command:
            return {"ok": False, "error": f"Blocked shell metacharacter: {repr(dangerous)}"}

    # Block dangerous pipe targets.
    for target in _DANGEROUS_PIPE_TARGETS:
        if f"| {target}" in command or f"|{target}" in command:
            return {"ok": False, "error": f"Piping to {target} is blocked"}

    # Validate path arguments for path-taking commands.
    if program in _PATH_COMMANDS:
        roots = _load_project_roots()
        # Extract path-like arguments (skip flags starting with -).
        path_args = [t for t in tokens[1:] if not t.startswith("-")]
        for pa in path_args:
            expanded = os.path.expanduser(pa)
            # Only validate things that look like paths (contain / or ~).
            if "/" in pa or pa.startswith("~") or os.path.isabs(expanded):
                if not _is_path_allowed(expanded, roots):
                    return {
                        "ok": False,
                        "error": f"Path not in allowed project roots: {pa}",
                    }

    # Execute with timeout.  Use create_subprocess_exec (not shell) so that
    # shell metacharacters cannot bypass the whitelist even if the blocked-
    # character check above is incomplete.
    try:
        proc = await asyncio.create_subprocess_exec(
            *tokens,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.STDOUT,
        )
        stdout, _ = await asyncio.wait_for(proc.communicate(), timeout=5.0)
    except asyncio.TimeoutError:
        try:
            proc.kill()  # type: ignore[union-attr]
        except ProcessLookupError:
            pass
        return {"ok": False, "error": "Command timed out (5s limit)"}
    except OSError as exc:
        return {"ok": False, "error": f"Failed to run command: {exc}"}

    output = stdout.decode("utf-8", errors="replace") if stdout else ""
    if len(output) > _MAX_OUTPUT_BYTES:
        output = output[:_MAX_OUTPUT_BYTES] + "\n… (truncated to 4KB)"
    output = _redact_secrets(output)

    return {
        "ok": proc.returncode == 0,
        "exit_code": proc.returncode,
        "output": output,
    }


async def _handle_read_file(args: dict[str, Any]) -> dict[str, Any]:
    """Read a file from an allowed project directory with security checks."""
    path = args.get("path", "").strip()
    if not path:
        return {"ok": False, "error": "No path provided"}

    roots = _load_project_roots()
    if not _is_path_allowed(path, roots):
        return {"ok": False, "error": f"Path not in allowed project roots: {path}"}

    resolved = os.path.realpath(os.path.expanduser(path))
    basename = os.path.basename(resolved)

    # Block sensitive file patterns.
    for pattern in _BLOCKED_FILE_PATTERNS:
        if fnmatch.fnmatch(basename, pattern):
            return {"ok": False, "error": f"Blocked file pattern: {basename}"}

    if not os.path.isfile(resolved):
        return {"ok": False, "error": f"Not a file or does not exist: {path}"}

    try:
        with open(resolved, "rb") as f:
            raw = f.read(_MAX_OUTPUT_BYTES)
    except OSError as exc:
        return {"ok": False, "error": f"Failed to read file: {exc}"}

    # Detect binary files by checking for null bytes in the first 512 bytes.
    if b"\x00" in raw[:512]:
        return {"ok": False, "error": "Binary file detected — refusing to read"}

    content = raw.decode("utf-8", errors="replace")
    truncated = len(raw) == _MAX_OUTPUT_BYTES
    if truncated:
        content += "\n… (truncated to 4KB)"
    content = _redact_secrets(content)

    return {"ok": True, "path": resolved, "content": content, "truncated": truncated}


async def _handle_get_clipboard() -> dict[str, Any]:
    """Read the macOS clipboard via pbpaste with secret redaction."""
    try:
        proc = await asyncio.create_subprocess_exec(
            "pbpaste",
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, _ = await asyncio.wait_for(proc.communicate(), timeout=3.0)
    except asyncio.TimeoutError:
        return {"ok": False, "error": "Clipboard read timed out"}
    except OSError as exc:
        return {"ok": False, "error": f"Failed to read clipboard: {exc}"}

    content = stdout.decode("utf-8", errors="replace") if stdout else ""
    max_clipboard = 2048  # 2 KB limit
    if len(content) > max_clipboard:
        content = content[:max_clipboard] + "\n… (truncated to 2KB)"
    content = _redact_secrets(content)

    return {"ok": True, "content": content}
