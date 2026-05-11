"""Intent router -- classify user utterances and dispatch to local or cloud LLM.

When a final transcript arrives from STT, we need to decide whether to send it
to Qwen (local via Ollama, fast ~0.5s) or Claude (cloud via OpenRouter, smart
~2s).  Simple commands go local; complex queries go cloud.

The routing is pure keyword matching -- no ML, no network calls.  Target
latency is <5ms per call.

Usage::

    from router import route, LLMTarget

    result = route("approve all sessions")
    assert result.target == LLMTarget.LOCAL

    result = route("explain why auth-service failed")
    assert result.target == LLMTarget.CLOUD
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass
from enum import Enum
from typing import Final

logger: Final = logging.getLogger("jarvis-daemon.router")

# ---------------------------------------------------------------------------
# Public types
# ---------------------------------------------------------------------------


class LLMTarget(Enum):
    """Dispatch target for a user utterance."""

    LOCAL = "local"  # Qwen via Ollama -- fast, simple commands
    CLOUD = "cloud"  # Claude via OpenRouter -- complex reasoning


@dataclass(frozen=True, slots=True)
class RoutingResult:
    """The outcome of intent classification."""

    target: LLMTarget
    text: str  # Cleaned text to send to LLM
    reason: str  # Why this target was chosen (for debug logging)


# ---------------------------------------------------------------------------
# Trigger-word stripping
# ---------------------------------------------------------------------------

# Prefixes to strip from the beginning of the utterance before routing.
# Order matters: longer phrases first so "hey jarvis" is matched before "jarvis".
_TRIGGER_PREFIXES: Final[tuple[str, ...]] = (
    "hey jarvis",
    "jarvis",
    "could you",
    "can you",
    "please",
)

# Compiled pattern: match any trigger prefix at the start of the string,
# optionally followed by a comma or whitespace.
_TRIGGER_RE: Final[re.Pattern[str]] = re.compile(
    r"^(?:" + "|".join(re.escape(p) for p in _TRIGGER_PREFIXES) + r")[\s,]*",
    re.IGNORECASE,
)


def _strip_triggers(text: str) -> str:
    """Remove trigger-word prefixes from *text*.

    Applies the pattern repeatedly so stacked prefixes like
    ``"hey jarvis please approve"`` collapse to ``"approve"``.
    """
    prev = ""
    while text != prev:
        prev = text
        text = _TRIGGER_RE.sub("", text).strip()
    return text


# ---------------------------------------------------------------------------
# Keyword patterns
# ---------------------------------------------------------------------------

# LOCAL: simple commands, status checks, greetings, navigation.
_LOCAL_KEYWORDS: Final[tuple[str, ...]] = (
    # Approval actions
    "approve",
    "deny",
    "reject",
    # Focus / navigation
    "focus",
    "switch to",
    "open",
    "navigate",
    "show me",
    "go to",
    # Status queries
    "status",
    "what's running",
    "whats running",
    "how many",
    "how much",
    # Session control
    "stop",
    "resume",
    "launch",
    "restart",
    # Git operations
    "stage",
    "commit",
    "push",
    # Greetings
    "hello",
    "hey",
    "good morning",
    "good evening",
    "good night",
    "good afternoon",
)

# CLOUD: complex reasoning, analysis, drafting, planning.
_CLOUD_KEYWORDS: Final[tuple[str, ...]] = (
    "explain",
    "why",
    "analyze",
    "analyse",
    "describe",
    "compare",
    "draft",
    "write",
    "compose",
    "summarize",
    "summarise",
    "what if",
    "how should",
    "could you",
    "help me",
    "plan",
    "suggest",
    "recommend",
    "review",
)

# Word-count thresholds.
_SHORT_UTTERANCE_MAX_WORDS: Final[int] = 10
_LONG_UTTERANCE_MIN_WORDS: Final[int] = 20

# Question words that prevent short utterances from being classified LOCAL.
_QUESTION_WORDS: Final[frozenset[str]] = frozenset({
    "why",
    "how",
    "explain",
    "what",
    "describe",
})


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _contains_any(text_lower: str, keywords: tuple[str, ...]) -> str | None:
    """Return the first keyword found in *text_lower*, or ``None``."""
    for kw in keywords:
        if kw in text_lower:
            return kw
    return None


def _word_count(text: str) -> int:
    """Return the number of whitespace-delimited words in *text*."""
    return len(text.split())


def _has_question_word(text_lower: str) -> bool:
    """Return ``True`` if any leading word in *text_lower* is a question word."""
    first_word = text_lower.split()[0] if text_lower.split() else ""
    return first_word in _QUESTION_WORDS


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

def route(text: str) -> RoutingResult:
    """Classify *text* and return a ``RoutingResult``.

    Routing priority (first match wins):
      1. CLOUD keywords found -> CLOUD
      2. LOCAL keywords found -> LOCAL
      3. Long utterance (>20 words) -> CLOUD
      4. Short utterance (<10 words, no question words) -> LOCAL
      5. Default -> CLOUD (safer fallback)

    The returned ``RoutingResult.text`` has trigger-word prefixes stripped.
    """
    # -- Normalize --------------------------------------------------------
    cleaned = _strip_triggers(text.strip())
    if not cleaned:
        # Empty after stripping -- treat as a greeting.
        return RoutingResult(
            target=LLMTarget.LOCAL,
            text=cleaned or text.strip(),
            reason="empty utterance after trigger stripping",
        )

    text_lower = cleaned.lower()
    words = _word_count(cleaned)

    # -- 1. Check CLOUD keywords first (they indicate complexity) ---------
    cloud_match = _contains_any(text_lower, _CLOUD_KEYWORDS)
    if cloud_match is not None:
        result = RoutingResult(
            target=LLMTarget.CLOUD,
            text=cleaned,
            reason=f"cloud keyword: {cloud_match!r}",
        )
        logger.debug("route -> %s (%s)", result.target.value, result.reason)
        return result

    # -- 2. Check LOCAL keywords ------------------------------------------
    local_match = _contains_any(text_lower, _LOCAL_KEYWORDS)
    if local_match is not None:
        result = RoutingResult(
            target=LLMTarget.LOCAL,
            text=cleaned,
            reason=f"local keyword: {local_match!r}",
        )
        logger.debug("route -> %s (%s)", result.target.value, result.reason)
        return result

    # -- 3. Long utterance -> CLOUD (likely needs reasoning) --------------
    if words > _LONG_UTTERANCE_MIN_WORDS:
        result = RoutingResult(
            target=LLMTarget.CLOUD,
            text=cleaned,
            reason=f"long utterance ({words} words)",
        )
        logger.debug("route -> %s (%s)", result.target.value, result.reason)
        return result

    # -- 4. Short utterance without question words -> LOCAL ---------------
    if words < _SHORT_UTTERANCE_MAX_WORDS and not _has_question_word(text_lower):
        result = RoutingResult(
            target=LLMTarget.LOCAL,
            text=cleaned,
            reason=f"short utterance ({words} words, no question words)",
        )
        logger.debug("route -> %s (%s)", result.target.value, result.reason)
        return result

    # -- 5. Default: CLOUD (safer -- Claude handles anything) -------------
    result = RoutingResult(
        target=LLMTarget.CLOUD,
        text=cleaned,
        reason="default fallback",
    )
    logger.debug("route -> %s (%s)", result.target.value, result.reason)
    return result
