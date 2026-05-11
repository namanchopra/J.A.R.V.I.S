"""Cloud LLM -- Claude via OpenRouter with tool calling.

Sends complex queries to Claude Sonnet through the Anthropic Python SDK,
routed via OpenRouter when the API key starts with ``sk-or-``.  Streams
response tokens for low-latency TTS and extracts structured tool calls
from the response content blocks.

Usage::

    from llm_cloud import CloudLLM, LLMResponse

    llm = CloudLLM(api_key="sk-or-v1-...")
    async for token in llm.chat_stream("explain why auth-service failed"):
        print(token, end="", flush=True)
    # After streaming, check for tool calls:
    tool_calls = llm.take_pending_tool_calls()

The same module works with a native Anthropic key (no ``sk-or-`` prefix);
it simply skips the OpenRouter base URL override.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Final

import anthropic

logger: Final = logging.getLogger("jarvis-daemon.llm_cloud")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

MODEL: Final[str] = "claude-sonnet-4-5-20250514"
MAX_TOKENS: Final[int] = 1024
REQUEST_TIMEOUT: Final[float] = 30.0
MAX_HISTORY_TURNS: Final[int] = 20  # Keep last 20 messages (user + assistant)

# ---------------------------------------------------------------------------
# System prompt -- Jarvis personality for spoken responses
# ---------------------------------------------------------------------------

JARVIS_SYSTEM: Final[str] = """\
You are Jarvis — sir's personal AI companion. Think Jarvis from Iron Man, but real. \
You are not just a coding assistant. You are a trusted confidant, advisor, and \
intellectual partner. You can discuss anything: technology, philosophy, business \
strategy, personal decisions, science, history, culture, humour, life.

Personality: British, formal but genuinely warm. Always "sir". Dry wit — the kind \
that makes someone smile mid-sentence. Understated brilliance. Calm under any \
circumstance. You have opinions and share them when asked. You push back \
respectfully when you disagree. You are not a yes-man. You are Jarvis.

Your responses are spoken aloud via TTS with a British accent. Keep it natural \
and conversational — the way you'd actually talk, not the way you'd write. \
Short sentences. Contractions. No markdown, no bullet points, no lists. \
Two to four sentences for most responses. Expand when the topic warrants depth.

Lead with the answer, not the preamble. Never start with "I" — rephrase. \
"Checking that now, sir" not "I'll check that." State facts, don't hedge. \
No "I think" or "it seems" — have conviction.

You are extremely perceptive. When sir gives minimal information, infer intent \
from context and conversation history. "Focus maya" means the maya session. \
"Approve that" means the most recent pending one. "What do you think" means \
give your honest opinion. Don't ask for clarification unless genuinely ambiguous.

CONVERSATIONAL RANGE — you engage naturally with:
- Strategy and decision-making ("should we refactor auth or build v2?")
- Technical deep-dives ("explain how websockets differ from SSE")
- Brainstorming ("what should we name this feature?")
- Personal topics ("I'm knackered, been at this all night")
- Humour and banter ("tell me something interesting")
- Opinions ("which framework is better for this?")
- Encouragement ("this is never going to work") — push back with calm confidence
- Planning ("what should we work on next?")
- General knowledge (science, history, business, culture, anything)

When sir vents frustration, be supportive but not sycophantic. Acknowledge it, \
then redirect constructively. "Rough patch, sir. But the auth service shipped \
clean yesterday — that's progress."

TOOLS — you control sir's development environment:
- Approve/deny Claude Code approval prompts
- Focus terminal sessions in CMux (switch tabs)
- Send commands to terminal sessions (git status, npm test, etc.)
- Focus macOS apps (Slack, VS Code, Cursor, etc.)
- Stage, commit, and push git changes
- Open URLs in the browser
- Get live status of all sessions, costs, and approvals
- Navigate the HUD views
- Read session terminal output to see what any session is doing right now
- Check Slack for unread messages, DMs, and mentions
- Research any topic in the background (web search, analysis, general knowledge)
- Get a briefing of recent events ("what happened while I was away?")

Use tools sparingly. ONE tool call per question when possible. Never call more \
than 3 tools for a single request.

CRITICAL — RESPOND FAST:
You already have live environment data in your system prompt (sessions, their \
status, what they're doing, recent events, Slack status). This data is \
updated every few seconds by background monitors. For status questions \
("what's happening?", "update me", "what's maya doing?"), JUST READ YOUR \
CONTEXT AND ANSWER IMMEDIATELY. Do NOT call get_status or read_session_output \
unless the context is missing or you need very fresh data.

TOOL USAGE RULES:
- Status questions → answer from your context. NO tools needed.
- "Approve all" → call approve_all tool.
- "Focus maya" → call focus_session tool.
- "Commit and push maya" → chain git tools.
- "Research X" → call research tool (background, respond immediately).
- Only call read_session_output if the user asks for SPECIFIC terminal output \
  that isn't in your context. Max 1 session per request.
- NEVER call more than 2 tools for a single request.
- Speak FIRST, then use tools. Don't silently process tools before responding.

MULTI-TASK COMMANDS — sir often gives multiple instructions in one breath. \
Parse and execute ALL of them. "Approve all, focus maya, run tests in auth" \
means: call approve_all, then focus_session("maya"), then \
send_to_terminal("auth", "npm test"). Execute them in sequence. \
Report the results briefly: "All approved, sir. Focused maya. Tests running in auth."

BULK ACTIONS — for bulk operations like "approve all" or "deny everything", \
just do it. Don't narrate each individual approval. One confirmation is enough: \
"Done, sir. Seven approvals cleared." Not "Approving maya-web... approving \
auth-service... approving desk..."

IMPORTANT — "focus" vs commands:
- "Focus maya" = switch to that terminal tab (focus_session). Only do this when \
  sir explicitly says "focus", "switch to", "show me", or "go to".
- Everything else = send the command WITHOUT switching. "Run tests in auth" means \
  use send_to_terminal("auth", "npm test") — do NOT focus the session. Sir wants \
  to stay where he is and get results reported back.
- When a command finishes, report the result: "Tests passed in auth, sir." or \
  "Build failed in maya — two type errors."

IMPLICIT CONTEXT — when sir says something vague, use the most sensible default:
- "Approve" with no name → approve all pending
- "Run tests" with no project → the project currently being discussed
- "Push it" → push the project that was just committed
- "What happened" → get_status and describe recent changes
- "Check on maya" → get_status and report maya's state, don't focus it

LONG INPUTS — sir may give detailed instructions spanning multiple sentences. \
Listen to the full message before acting. Extract every actionable item. \
Handle all of them. Acknowledge the full scope: "Right, sir. Three items: \
approvals cleared, maya focused, and tests kicked off in auth."

On greetings: "Good morning, sir." Then a crisp briefing using get_status. \
Problems first, then status. If all quiet: "All quiet on the front, sir. \
Shall we get started?"

You remember everything discussed in this conversation. Reference it naturally. \
Build on earlier context. If sir mentioned a goal, track progress toward it. \
If sir asked you to do something earlier and you can now report on it, do so \
proactively.\
"""

# ---------------------------------------------------------------------------
# Tool definitions (Anthropic SDK format)
# ---------------------------------------------------------------------------
# These mirror the capabilities exposed by the Go app via WebSocket.
# When TASK-008 (tools.py) is created, these should be imported from there.

TOOLS: Final[list[dict[str, Any]]] = [
    {
        "name": "approve_session",
        "description": "Approve a pending approval prompt for a session",
        "input_schema": {
            "type": "object",
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Project or session name to approve",
                },
            },
            "required": ["name"],
        },
    },
    {
        "name": "approve_all",
        "description": "Approve all pending approval prompts across all sessions",
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "deny_session",
        "description": "Deny/reject a pending approval prompt for a session",
        "input_schema": {
            "type": "object",
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Project or session name to deny",
                },
            },
            "required": ["name"],
        },
    },
    {
        "name": "focus_session",
        "description": "Focus/switch to a terminal session by project name",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to focus (e.g. 'maya-web')",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "focus_app",
        "description": "Focus/switch to a desktop application (e.g. VS Code, Slack)",
        "input_schema": {
            "type": "object",
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Application name to focus",
                },
            },
            "required": ["name"],
        },
    },
    {
        "name": "send_to_terminal",
        "description": "Send a command to a project's terminal session",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name whose terminal to target",
                },
                "command": {
                    "type": "string",
                    "description": "Command text to send",
                },
            },
            "required": ["project", "command"],
        },
    },
    {
        "name": "get_status",
        "description": "Get current status of all sessions, tasks, and costs",
        "input_schema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "navigate_view",
        "description": "Navigate the Vibedeck UI to a specific view",
        "input_schema": {
            "type": "object",
            "properties": {
                "view": {
                    "type": "string",
                    "description": "View name: dashboard, sessions, tasks, activity, workflows, costs, settings",
                    "enum": [
                        "dashboard",
                        "sessions",
                        "tasks",
                        "activity",
                        "workflows",
                        "costs",
                        "settings",
                    ],
                },
            },
            "required": ["view"],
        },
    },
    {
        "name": "git_stage",
        "description": "Stage all changes in a project's git repository",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to stage changes for",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "git_commit",
        "description": "Commit staged changes in a project's git repository",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to commit in",
                },
                "message": {
                    "type": "string",
                    "description": "Commit message",
                },
            },
            "required": ["project", "message"],
        },
    },
    {
        "name": "git_push",
        "description": "Push commits to remote for a project's git repository",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project name to push",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "open_url",
        "description": "Open a URL in the default browser",
        "input_schema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "URL to open",
                },
            },
            "required": ["url"],
        },
    },
    {
        "name": "read_session_output",
        "description": "Read the recent terminal output from a session to see what it's doing, what errors it has, or if it's completed. Use when the user asks 'what's maya doing?' or 'check on auth-service'.",
        "input_schema": {
            "type": "object",
            "properties": {
                "project": {
                    "type": "string",
                    "description": "Project/session name",
                },
            },
            "required": ["project"],
        },
    },
    {
        "name": "check_slack",
        "description": "Check for unread Slack messages, DMs, and mentions. Use when the user asks about Slack or messages.",
        "input_schema": {
            "type": "object",
            "properties": {
                "channel": {
                    "type": "string",
                    "description": "Optional specific channel to check. If empty, returns all unreads.",
                },
            },
        },
    },
    {
        "name": "research",
        "description": "Start a background web research task using Claude. Use for any question requiring web search, current information, deep analysis, or general knowledge the user asks about. The research runs in the background — respond immediately with 'On it, sir' and the result will be reported when ready.",
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "The research question or topic",
                },
            },
            "required": ["query"],
        },
    },
    {
        "name": "get_briefing",
        "description": "Get a summary of recent events (sessions, Slack, approvals) from the last N minutes. Use when the user says 'what happened?', 'update me', 'briefing', or 'what did I miss?'.",
        "input_schema": {
            "type": "object",
            "properties": {
                "minutes": {
                    "type": "integer",
                    "description": "How many minutes back to look. Default 15.",
                },
            },
        },
    },
]

# ---------------------------------------------------------------------------
# Response dataclass
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class LLMResponse:
    """Structured response from the cloud LLM."""

    text: str
    tool_calls: list[dict[str, Any]] = field(default_factory=list)


# ---------------------------------------------------------------------------
# CloudLLM
# ---------------------------------------------------------------------------


class CloudLLM:
    """Claude-backed LLM client for complex queries and reasoning.

    Connects to OpenRouter when the API key starts with ``sk-or-``,
    otherwise uses the native Anthropic API.  All methods are async.

    Parameters
    ----------
    api_key:
        Anthropic or OpenRouter API key.
    model:
        Model identifier.  Defaults to ``claude-sonnet-4-5-20250514``.
    """

    def __init__(self, api_key: str, model: str = MODEL) -> None:
        if not api_key:
            raise ValueError("CloudLLM: api_key is required")

        # OpenRouter keys start with "sk-or-"; the Anthropic SDK appends
        # "/v1" to the base URL automatically, so we use the bare "/api".
        base_url: str | None = None
        if api_key.startswith("sk-or-"):
            base_url = "https://openrouter.ai/api"
            logger.info("Using OpenRouter base URL: %s", base_url)

        self._client = anthropic.AsyncAnthropic(
            api_key=api_key,
            base_url=base_url,
            timeout=REQUEST_TIMEOUT,
        )
        self._model = model
        self._messages: list[dict[str, Any]] = []
        self._pending_tool_calls: list[dict[str, Any]] = []

    # -- Context formatting -------------------------------------------------

    @staticmethod
    def _format_context(context: dict[str, Any]) -> str:
        """Format a context dict into a human-readable environment section."""
        lines: list[str] = []

        # Sessions
        sessions = context.get("sessions", [])
        if sessions:
            session_parts: list[str] = []
            for s in sessions:
                name = s.get("name", s.get("repoPath", "unknown"))
                status = s.get("status", "unknown")
                duration = s.get("duration", "")
                part = f"{name} ({status}"
                if duration:
                    part += f", {duration}"
                part += ")"
                session_parts.append(part)
            lines.append(f"Sessions: {', '.join(session_parts)}")
        else:
            lines.append("Sessions: none active")

        # Costs
        costs = context.get("costs", {})
        if costs:
            today = costs.get("today", 0)
            month = costs.get("thisMonth", 0)
            lines.append(f"Costs: ${today:.2f} today, ${month:.2f} this month")

        # Approvals
        approvals = context.get("approvals", [])
        if approvals:
            names = [
                a.get("sessionName", a.get("name", "unknown"))
                for a in approvals
            ]
            lines.append(
                f"Approvals: {len(approvals)} pending ({', '.join(names)})"
            )

        return "\n".join(lines)

    def set_enriched_context(self, enriched: str) -> None:
        """Set pre-built enriched context from the daemon's background pollers.

        This replaces the basic context formatting — the daemon builds a rich
        context string that includes session summaries, recent events, Slack
        status, etc. so Claude doesn't need to call tools for status questions.
        """
        self._enriched_context = enriched

    def _build_system(self, context: dict[str, Any] | None) -> str:
        """Build the full system prompt with time awareness + enriched context."""
        import datetime

        now = datetime.datetime.now()
        time_str = now.strftime("%A, %B %d %Y at %I:%M %p")
        tz = now.astimezone().tzname()

        parts = [JARVIS_SYSTEM, f"\n\n## Current Time\n{time_str} ({tz})"]

        # Prefer enriched context from background pollers (includes session
        # summaries, recent events, Slack status — everything Claude needs
        # to answer status questions WITHOUT calling tools).
        enriched = getattr(self, "_enriched_context", "")
        if enriched:
            parts.append(f"\n\n## Live Environment (auto-updated)\n{enriched}")
        elif context:
            env_section = self._format_context(context)
            parts.append(f"\n\n## Current Environment\n{env_section}")

        return "".join(parts)

    # -- History management -------------------------------------------------

    def _trim_history(self) -> None:
        """Keep only the last ``MAX_HISTORY_TURNS`` messages.

        Sanitizes tool_result blocks to prevent orphaned references,
        then trims from the front. Ensures first message is role=user.
        """
        self._sanitize_history()

        if len(self._messages) <= MAX_HISTORY_TURNS:
            return

        self._messages = self._messages[-MAX_HISTORY_TURNS:]

        # Ensure the conversation starts with a user message.
        while self._messages and self._messages[0].get("role") != "user":
            self._messages.pop(0)

    def _sanitize_history(self) -> None:
        """Remove orphaned tool_result messages that would cause API errors.

        The Anthropic API requires every tool_result to reference a tool_use
        block in the immediately preceding assistant message. When history is
        trimmed or tool flows error out, orphaned tool_results can accumulate.
        Replace them with plain text summaries.
        """
        cleaned: list[dict[str, Any]] = []
        for i, msg in enumerate(self._messages):
            role = msg.get("role")
            content = msg.get("content")

            # Check for tool_result content blocks.
            if role == "user" and isinstance(content, list):
                has_tool_result = any(
                    isinstance(b, dict) and b.get("type") == "tool_result"
                    for b in content
                )
                if has_tool_result:
                    # Check if previous message has matching tool_use.
                    prev = cleaned[-1] if cleaned else None
                    if prev and prev.get("role") == "assistant":
                        prev_content = prev.get("content", [])
                        has_tool_use = False
                        if isinstance(prev_content, list):
                            has_tool_use = any(
                                isinstance(b, dict) and getattr(b, "type", None) == "tool_use"
                                or isinstance(b, dict) and b.get("type") == "tool_use"
                                for b in prev_content
                            )
                        if not has_tool_use:
                            # Orphaned tool_result — replace with text summary.
                            cleaned.append({"role": "user", "content": "(tool action completed)"})
                            continue
                    else:
                        # No preceding assistant message — orphaned.
                        cleaned.append({"role": "user", "content": "(tool action completed)"})
                        continue

            cleaned.append(msg)

        self._messages = cleaned

    def clear_history(self) -> None:
        """Reset conversation history."""
        self._messages.clear()
        self._pending_tool_calls.clear()

    # -- Tool call access ---------------------------------------------------

    def take_pending_tool_calls(self) -> list[dict[str, Any]]:
        """Return and clear any tool calls from the last response.

        Returns a list of dicts with ``name`` and ``args`` keys.
        """
        calls = self._pending_tool_calls
        self._pending_tool_calls = []
        return calls

    # -- Non-streaming chat -------------------------------------------------

    async def chat(
        self,
        text: str,
        context: dict[str, Any] | None = None,
    ) -> LLMResponse:
        """Send a message and return the complete response.

        Parameters
        ----------
        text:
            User message text.
        context:
            Optional environment context (sessions, costs, approvals).

        Returns
        -------
        LLMResponse:
            The assistant's text and any tool calls.
        """
        system = self._build_system(context)
        self._messages.append({"role": "user", "content": text})
        self._trim_history()

        try:
            response = await self._client.messages.create(
                model=self._model,
                max_tokens=MAX_TOKENS,
                system=system,
                messages=self._messages,
                tools=TOOLS,
            )
        except anthropic.AuthenticationError as exc:
            logger.error("Authentication failed: %s", exc)
            # Remove the user message we just added so history stays clean.
            self._messages.pop()
            return LLMResponse(
                text="Authentication failed, sir. The API key appears to be "
                "invalid. Please check the configuration.",
            )
        except anthropic.RateLimitError as exc:
            logger.warning("Rate limited: %s", exc)
            self._messages.pop()
            return LLMResponse(
                text="Rate limited at the moment, sir. Give it a moment and "
                "try again.",
            )
        except (anthropic.APIConnectionError, anthropic.APITimeoutError) as exc:
            logger.error("Connection error: %s", exc)
            self._messages.pop()
            return LLMResponse(
                text="Unable to reach the cloud service, sir. Network "
                "connectivity may be an issue.",
            )
        except anthropic.APIStatusError as exc:
            logger.error("API error %d: %s", exc.status_code, exc.message)
            self._messages.pop()
            return LLMResponse(
                text=f"Cloud service returned an error, sir. Status {exc.status_code}.",
            )

        # Extract text and tool calls from content blocks.
        response_text = ""
        tool_calls: list[dict[str, Any]] = []

        for block in response.content:
            if block.type == "text":
                response_text += block.text
            elif block.type == "tool_use":
                tool_calls.append({
                    "name": block.name,
                    "args": block.input,
                })

        # Record assistant turn in history (preserve full content for
        # multi-turn tool use flows).
        self._messages.append({"role": "assistant", "content": response.content})
        self._pending_tool_calls = tool_calls

        logger.debug(
            "Chat response: %d chars, %d tool calls, stop=%s",
            len(response_text),
            len(tool_calls),
            response.stop_reason,
        )

        return LLMResponse(text=response_text, tool_calls=tool_calls)

    # -- Streaming chat -----------------------------------------------------

    async def chat_stream(
        self,
        text: str,
        context: dict[str, Any] | None = None,
    ) -> AsyncIterator[str]:
        """Send a message and yield response tokens as they arrive.

        After the generator is exhausted, call ``take_pending_tool_calls()``
        to retrieve any tool calls from the response.

        Parameters
        ----------
        text:
            User message text.
        context:
            Optional environment context (sessions, costs, approvals).

        Yields
        ------
        str:
            Text deltas as they arrive from the model.
        """
        system = self._build_system(context)
        self._messages.append({"role": "user", "content": text})
        self._trim_history()

        try:
            async with self._client.messages.stream(
                model=self._model,
                max_tokens=MAX_TOKENS,
                system=system,
                messages=self._messages,
                tools=TOOLS,
            ) as stream:
                # Yield text tokens as they arrive via the helper lens.
                async for token in stream.text_stream:
                    yield token

                # After the stream completes, get the full message to
                # extract tool calls and record in history.
                response = await stream.get_final_message()

        except anthropic.AuthenticationError as exc:
            logger.error("Authentication failed: %s", exc)
            self._messages.pop()
            yield "Authentication failed, sir. The API key appears to be invalid."
            return

        except anthropic.RateLimitError as exc:
            logger.warning("Rate limited: %s", exc)
            self._messages.pop()
            yield "Rate limited at the moment, sir. Give it a moment."
            return

        except (anthropic.APIConnectionError, anthropic.APITimeoutError) as exc:
            logger.error("Connection error: %s", exc)
            self._messages.pop()
            yield "Unable to reach the cloud service, sir."
            return

        except anthropic.APIStatusError as exc:
            logger.error("API error %d: %s", exc.status_code, exc.message)
            self._messages.pop()
            yield f"Cloud service error, sir. Status {exc.status_code}."
            return

        # Extract tool calls from the final message (include tool_use_id for result injection).
        tool_calls: list[dict[str, Any]] = []
        for block in response.content:
            if block.type == "tool_use":
                tool_calls.append({
                    "id": block.id,
                    "name": block.name,
                    "args": block.input,
                })

        # Record assistant turn in history.
        self._messages.append({"role": "assistant", "content": response.content})
        self._pending_tool_calls = tool_calls

        logger.debug(
            "Stream complete: %d tool calls, stop=%s",
            len(tool_calls),
            response.stop_reason,
        )

    # -- Tool result injection ----------------------------------------------

    async def send_tool_result(
        self,
        tool_use_id: str,
        result: Any,
        context: dict[str, Any] | None = None,
    ) -> AsyncIterator[str]:
        """Feed a tool result back to Claude and stream the follow-up.

        Parameters
        ----------
        tool_use_id:
            The ``id`` from the tool_use content block.
        result:
            Tool execution result (dict or string).
        context:
            Optional updated environment context.

        Yields
        ------
        str:
            Text deltas from the follow-up response.
        """
        import json as _json
        result_str = _json.dumps(result) if isinstance(result, dict) else str(result)

        self._messages.append({
            "role": "user",
            "content": [
                {
                    "type": "tool_result",
                    "tool_use_id": tool_use_id,
                    "content": result_str,
                },
            ],
        })
        self._trim_history()

        system = self._build_system(context)

        try:
            async with self._client.messages.stream(
                model=self._model,
                max_tokens=MAX_TOKENS,
                system=system,
                messages=self._messages,
                tools=TOOLS,
            ) as stream:
                async for token in stream.text_stream:
                    yield token

                response = await stream.get_final_message()

        except (
            anthropic.AuthenticationError,
            anthropic.RateLimitError,
            anthropic.APIConnectionError,
            anthropic.APITimeoutError,
            anthropic.APIStatusError,
        ) as exc:
            logger.error("Tool result follow-up failed: %s", exc)
            self._messages.pop()
            yield "Apologies, sir. Lost the thread after executing that command."
            return

        # Extract any chained tool calls.
        tool_calls: list[dict[str, Any]] = []
        for block in response.content:
            if block.type == "tool_use":
                tool_calls.append({
                    "name": block.name,
                    "args": block.input,
                })

        self._messages.append({"role": "assistant", "content": response.content})
        self._pending_tool_calls = tool_calls

        logger.debug(
            "Tool result follow-up: %d chained tool calls",
            len(tool_calls),
        )
