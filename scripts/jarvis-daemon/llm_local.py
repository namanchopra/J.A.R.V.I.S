"""Local LLM integration via Ollama (Qwen3:4b) with tool calling.

Provides fast, offline AI responses for simple voice commands (approve,
focus, status, git operations, etc.).  Falls back to cloud LLM when
Ollama is unavailable.

Uses the ``ollama`` Python library with ``AsyncClient`` for async chat.
Tool calls use non-streaming mode (streaming + tools has known Ollama bugs).
The ``chat_stream()`` method streams tokens without tool support for TTS.

Requires:
    pip install ollama>=0.4
    ollama pull qwen3:4b
"""

from __future__ import annotations

import asyncio
import json
import logging
from dataclasses import dataclass, field
from typing import Any, Final

logger: Final = logging.getLogger("jarvis-daemon.llm_local")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

OLLAMA_URL: Final[str] = "http://localhost:11434"
MODEL: Final[str] = "qwen3:4b"
REQUEST_TIMEOUT_S: Final[float] = 30.0  # First call loads model (~15s)
MAX_HISTORY_TURNS: Final[int] = 10

# ---------------------------------------------------------------------------
# Response type
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class LLMResponse:
    """Result of a local LLM chat call."""

    text: str
    """Spoken response text (for TTS)."""

    tool_calls: list[dict[str, Any]] = field(default_factory=list)
    """Tool invocations: ``[{"name": "...", "args": {...}}]``."""


# ---------------------------------------------------------------------------
# System prompt
# ---------------------------------------------------------------------------

JARVIS_SYSTEM: Final[str] = (
    "You are Jarvis, a voice AI assistant like Jarvis from Iron Man. British, "
    "formal but warm. Always address the user as \"sir\". Dry wit, calm, "
    "concise. One to two sentences max.\n\n"
    "You have tools to control the user's development environment. Use them "
    "when asked. Always confirm actions naturally: \"Focusing maya-web now, "
    "sir.\" or \"All sessions approved, sir.\"\n\n"
    "Your responses are spoken aloud via TTS. Keep them short and "
    "natural-sounding. No markdown, no bullet points."
)

# ---------------------------------------------------------------------------
# Tool definitions (OpenAI-compatible format for Ollama)
# ---------------------------------------------------------------------------

TOOLS: Final[list[dict[str, Any]]] = [
    {
        "type": "function",
        "function": {
            "name": "approve_session",
            "description": "Approve a pending approval prompt for a session",
            "parameters": {
                "type": "object",
                "properties": {
                    "name": {
                        "type": "string",
                        "description": "Session/project name",
                    },
                },
                "required": ["name"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "approve_all",
            "description": "Approve all pending approval prompts",
            "parameters": {"type": "object", "properties": {}},
        },
    },
    {
        "type": "function",
        "function": {
            "name": "deny_session",
            "description": "Deny a pending approval prompt",
            "parameters": {
                "type": "object",
                "properties": {
                    "name": {
                        "type": "string",
                        "description": "Session/project name",
                    },
                },
                "required": ["name"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "focus_session",
            "description": "Focus/switch to a terminal session",
            "parameters": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                },
                "required": ["project"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "focus_app",
            "description": "Focus/open a macOS application",
            "parameters": {
                "type": "object",
                "properties": {
                    "name": {"type": "string"},
                },
                "required": ["name"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "send_to_terminal",
            "description": "Send a command to a terminal session",
            "parameters": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                    "command": {"type": "string"},
                },
                "required": ["project", "command"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_status",
            "description": "Get current status of all sessions, costs, and approvals",
            "parameters": {"type": "object", "properties": {}},
        },
    },
    {
        "type": "function",
        "function": {
            "name": "navigate_view",
            "description": "Navigate the HUD to a specific view",
            "parameters": {
                "type": "object",
                "properties": {
                    "view": {
                        "type": "string",
                        "enum": ["sessions", "tasks", "settings"],
                    },
                },
                "required": ["view"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "git_stage",
            "description": "Stage all changes in a project",
            "parameters": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                },
                "required": ["project"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "git_commit",
            "description": "Commit staged changes with a message",
            "parameters": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                    "message": {"type": "string"},
                },
                "required": ["project", "message"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "git_push",
            "description": "Push commits to remote",
            "parameters": {
                "type": "object",
                "properties": {
                    "project": {"type": "string"},
                },
                "required": ["project"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "open_url",
            "description": "Open a URL in the browser",
            "parameters": {
                "type": "object",
                "properties": {
                    "url": {"type": "string"},
                },
                "required": ["url"],
            },
        },
    },
]


# ---------------------------------------------------------------------------
# LocalLLM
# ---------------------------------------------------------------------------


class LocalLLM:
    """Async local LLM client backed by Ollama.

    Usage::

        llm = LocalLLM()
        if await llm.check_available():
            response = await llm.chat("approve all sessions")
            print(response.text, response.tool_calls)

        # Streaming (no tool calls):
        async for token in llm.chat_stream("what's the status"):
            print(token, end="", flush=True)
    """

    def __init__(
        self,
        model: str = MODEL,
        ollama_url: str = OLLAMA_URL,
    ) -> None:
        self._model = model
        self._url = ollama_url
        self._available: bool = False
        self._messages: list[dict[str, Any]] = []

    # -- availability --------------------------------------------------------

    async def check_available(self) -> bool:
        """Check if Ollama is running and the configured model is pulled.

        Returns ``True`` if the model is ready, ``False`` otherwise.
        Sets ``self._available`` so callers can cache the result.
        """
        try:
            import ollama as _ollama  # noqa: PLC0415
        except ImportError:
            logger.warning("ollama package not installed")
            self._available = False
            return False

        client = _ollama.AsyncClient(host=self._url)
        try:
            response = await asyncio.wait_for(
                client.list(),
                timeout=REQUEST_TIMEOUT_S,
            )
            # response.models is a list of model objects; each has a .model
            # attribute like "qwen3:4b".
            model_names: list[str] = []
            models = getattr(response, "models", None) or []
            for m in models:
                # The ollama library returns model objects with a `model`
                # attribute.  Older versions may return dicts.
                name = getattr(m, "model", None) or (
                    m.get("model", "") if isinstance(m, dict) else ""
                )
                if name:
                    model_names.append(name)

            available = any(
                self._model in name for name in model_names
            )
            if available:
                logger.info(
                    "Ollama available: model=%s url=%s",
                    self._model,
                    self._url,
                )
            else:
                logger.warning(
                    "Ollama running but model '%s' not found. "
                    "Available: %s. Run: ollama pull %s",
                    self._model,
                    ", ".join(model_names[:10]) or "(none)",
                    self._model,
                )
            self._available = available
            return available

        except (TimeoutError, asyncio.TimeoutError):
            logger.warning("Ollama health check timed out (%ss)", REQUEST_TIMEOUT_S)
            self._available = False
            return False
        except Exception:
            logger.debug("Ollama not reachable at %s", self._url, exc_info=True)
            self._available = False
            return False

    @property
    def available(self) -> bool:
        """Last cached availability result (call ``check_available()`` first)."""
        return self._available

    # -- chat (non-streaming, with tools) ------------------------------------

    async def chat(
        self,
        text: str,
        context: dict[str, Any] | None = None,
    ) -> LLMResponse:
        """Send a message and get a complete response with tool calls.

        Uses non-streaming mode because Ollama's streaming + tools has known
        bugs where tool_calls are dropped from streamed chunks.

        Args:
            text: User message (voice transcript).
            context: Optional environment context dict with keys like
                ``sessions``, ``costs``, ``approvals`` to inject into the
                system prompt for situational awareness.

        Returns:
            ``LLMResponse`` with spoken text and any tool invocations.

        Raises:
            RuntimeError: If Ollama is not available.
            TimeoutError: If the request exceeds the timeout.
        """
        import ollama as _ollama  # noqa: PLC0415

        system_prompt = self._build_system_prompt(context)
        self._append_user_message(text)

        messages = [{"role": "system", "content": system_prompt}, *self._messages]

        client = _ollama.AsyncClient(host=self._url)
        try:
            response = await asyncio.wait_for(
                client.chat(
                    model=self._model,
                    messages=messages,
                    tools=TOOLS,
                ),
                timeout=REQUEST_TIMEOUT_S,
            )
        except (TimeoutError, asyncio.TimeoutError):
            logger.warning("Ollama chat timed out after %ss", REQUEST_TIMEOUT_S)
            raise TimeoutError(
                f"Ollama did not respond within {REQUEST_TIMEOUT_S}s"
            )

        # Extract text content.
        content = self._extract_content(response)

        # Extract tool calls.
        tool_calls = self._extract_tool_calls(response)

        # Append assistant reply to conversation history.
        assistant_msg: dict[str, Any] = {"role": "assistant", "content": content}
        if tool_calls:
            assistant_msg["tool_calls"] = tool_calls
        self._messages.append(assistant_msg)
        self._trim_history()

        logger.info(
            "LLM response: text=%r tools=%d",
            content[:80] if content else "(empty)",
            len(tool_calls),
        )

        return LLMResponse(text=content, tool_calls=tool_calls)

    # -- chat_stream (streaming, no tools) -----------------------------------

    async def chat_stream(
        self,
        text: str,
        context: dict[str, Any] | None = None,
    ):
        """Async generator yielding response tokens as they arrive.

        Streaming mode does NOT support tool calls (Ollama limitation).
        Use ``chat()`` when tool execution is needed.

        Args:
            text: User message (voice transcript).
            context: Optional environment context dict.

        Yields:
            ``str`` tokens as they are generated.
        """
        import ollama as _ollama  # noqa: PLC0415

        system_prompt = self._build_system_prompt(context)
        self._append_user_message(text)

        messages = [{"role": "system", "content": system_prompt}, *self._messages]

        client = _ollama.AsyncClient(host=self._url)

        full_response: list[str] = []
        think_buffer = ""
        in_think_block = False

        try:
            stream = await asyncio.wait_for(
                client.chat(
                    model=self._model,
                    messages=messages,
                    stream=True,
                ),
                timeout=REQUEST_TIMEOUT_S,
            )

            async for chunk in stream:
                token = self._extract_content(chunk)
                if not token:
                    continue

                # Filter out <think>...</think> blocks from Qwen3's
                # thinking mode that may leak through despite /no_think.
                if in_think_block:
                    think_buffer += token
                    if "</think>" in think_buffer:
                        # Think block ended -- extract any text after it.
                        idx = think_buffer.find("</think>")
                        remainder = think_buffer[idx + len("</think>"):]
                        think_buffer = ""
                        in_think_block = False
                        if remainder.strip():
                            full_response.append(remainder)
                            yield remainder
                    continue

                if "<think>" in token:
                    # Think block starting -- may be mid-token.
                    idx = token.find("<think>")
                    before = token[:idx]
                    if before:
                        full_response.append(before)
                        yield before
                    think_buffer = token[idx:]
                    in_think_block = True
                    continue

                full_response.append(token)
                yield token

        except (TimeoutError, asyncio.TimeoutError):
            logger.warning("Ollama stream timed out after %ss", REQUEST_TIMEOUT_S)
            return

        # Store the full response in history.
        full_text = "".join(full_response).strip()
        if full_text:
            self._messages.append({"role": "assistant", "content": full_text})
            self._trim_history()

    # -- history management --------------------------------------------------

    def clear_history(self) -> None:
        """Clear the conversation history."""
        self._messages.clear()
        logger.debug("Conversation history cleared")

    # -- internal helpers ----------------------------------------------------

    def _build_system_prompt(self, context: dict[str, Any] | None) -> str:
        """Build the system prompt, optionally injecting environment context.

        When context is provided (sessions, costs, approvals), it is appended
        as a concise summary so the LLM can give situationally-aware answers.
        """
        prompt = JARVIS_SYSTEM

        if not context:
            return prompt

        parts: list[str] = [prompt, "\n\n--- Current Environment ---"]

        sessions = context.get("sessions")
        if sessions and isinstance(sessions, list):
            active = [
                s for s in sessions
                if isinstance(s, dict)
                and s.get("status") in ("running", "launching", "needs-input")
            ]
            if active:
                lines = [f"Active sessions ({len(active)}):"]
                for s in active[:10]:
                    name = s.get("name") or s.get("repoPath", "unknown")
                    status = s.get("status", "unknown")
                    lines.append(f"  - {name} [{status}]")
                parts.append("\n".join(lines))

        approvals = context.get("approvals")
        if approvals and isinstance(approvals, list):
            parts.append(f"Pending approvals: {len(approvals)}")
            for a in approvals[:5]:
                if isinstance(a, dict):
                    name = a.get("sessionName") or a.get("cwd", "unknown")
                    parts.append(f"  - {name}")

        costs = context.get("costs")
        if costs and isinstance(costs, dict):
            today = costs.get("today")
            if today is not None:
                parts.append(f"Spend today: ${today:.2f}")

        return "\n".join(parts)

    def _append_user_message(self, text: str) -> None:
        """Add a user message to conversation history.

        Appends ``/no_think`` to suppress Qwen3's thinking mode for faster
        responses.  This directive is invisible to the model's output.
        """
        # /no_think tells Qwen3 to skip the internal chain-of-thought,
        # giving us faster and more concise responses for voice.
        self._messages.append({
            "role": "user",
            "content": f"{text} /no_think",
        })
        self._trim_history()

    def _trim_history(self) -> None:
        """Keep only the last ``MAX_HISTORY_TURNS`` messages.

        Each turn is a user + assistant pair, so we keep
        ``MAX_HISTORY_TURNS * 2`` messages.
        """
        max_messages = MAX_HISTORY_TURNS * 2
        if len(self._messages) > max_messages:
            self._messages = self._messages[-max_messages:]

    @staticmethod
    def _extract_content(response: Any) -> str:
        """Extract text content from an Ollama response or chunk.

        Handles both attribute access (``response.message.content``) and
        dict access (``response['message']['content']``) for compatibility
        across ollama library versions.
        """
        # Attribute access (ollama >= 0.4).
        msg = getattr(response, "message", None)
        if msg is not None:
            content = getattr(msg, "content", None)
            if content is not None:
                return str(content)

        # Dict access (older versions / raw API).
        if isinstance(response, dict):
            msg_dict = response.get("message", {})
            if isinstance(msg_dict, dict):
                return str(msg_dict.get("content", ""))

        return ""

    @staticmethod
    def _extract_tool_calls(response: Any) -> list[dict[str, Any]]:
        """Extract tool calls from an Ollama non-streaming response.

        Returns a normalised list of ``{"name": "...", "args": {...}}``.
        """
        result: list[dict[str, Any]] = []

        # Attribute access path.
        msg = getattr(response, "message", None)
        raw_calls = getattr(msg, "tool_calls", None) if msg else None

        # Dict access fallback.
        if raw_calls is None and isinstance(response, dict):
            msg_dict = response.get("message", {})
            if isinstance(msg_dict, dict):
                raw_calls = msg_dict.get("tool_calls")

        if not raw_calls:
            return result

        for call in raw_calls:
            try:
                # Attribute access (ollama model objects).
                func = getattr(call, "function", None)
                if func is not None:
                    name = getattr(func, "name", "")
                    args = getattr(func, "arguments", {})
                    # Arguments may be a dict or a JSON string.
                    if isinstance(args, str):
                        args = json.loads(args)
                    result.append({"name": str(name), "args": dict(args)})
                    continue

                # Dict access fallback.
                if isinstance(call, dict):
                    func_dict = call.get("function", {})
                    if isinstance(func_dict, dict):
                        name = func_dict.get("name", "")
                        args = func_dict.get("arguments", {})
                        if isinstance(args, str):
                            args = json.loads(args)
                        result.append({"name": str(name), "args": dict(args)})

            except (json.JSONDecodeError, TypeError, AttributeError) as exc:
                logger.warning("Failed to parse tool call: %s -- %s", call, exc)

        return result
