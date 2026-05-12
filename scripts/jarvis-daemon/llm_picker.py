"""User-pick LLM constructor for the v0.1.5 Connections-panel dropdown.

The Connections panel exposes four options for ``cfg.llmModel``:

* ``"google/gemini-2.5-flash"``     -> Google AI Studio (OpenAI-compat)
* ``"anthropic/claude-haiku-4-5"``  -> Anthropic SDK direct
* ``"openai/gpt-4o-mini"``          -> OpenRouter
* ``"ollama:qwen3:4b"``             -> local Ollama (OpenAI-compat)

Empty / missing / unrecognised values return ``None`` and the daemon falls
back to the legacy key-driven chain (``main._build_llm_provider_chain``).
Bad config NEVER crashes — this is voice software, so degrade gracefully.

This module is intentionally split out of ``main.py`` so the picker can be
unit-tested without dragging in the full pipecat / livekit / torch stack
that ``main`` loads at import time. Tests stub the SDK imports via
``sys.modules`` injection (see ``tests/test_llm_picker.py``).
"""

from __future__ import annotations

import logging
from typing import Any

from config import get_api_key, get_llm_model

logger = logging.getLogger("jarvis-daemon.main")

# Anthropic SDK currently wants the dated model id. The dropdown uses the
# short ``claude-haiku-4-5`` form; map it to the same dated form the legacy
# ``use_anthropic_direct`` branch already ships so the user sees identical
# behaviour. Update this mapping when Anthropic drops a new dated build.
_ANTHROPIC_MODEL_DATE_SUFFIX: dict[str, str] = {
    "claude-haiku-4-5": "claude-haiku-4-5-20251001",
}


def build_user_picked_llm(
    config: dict[str, Any],
    *,
    system_instruction: str,
    anthropic_service_cls: Any,
    chain_state: dict[str, Any],
) -> Any | None:
    """Build the LLM service the user explicitly picked in the UI.

    Args:
        config: Loaded daemon config (``dict[str, Any]``).
        system_instruction: The Jarvis system prompt to pass into the LLM
            service constructor. Injected from ``main`` so this module
            doesn't import the heavy ``pipecat_llm`` module just for a
            constant.
        anthropic_service_cls: The ``AnthropicLLMService`` class to use
            for the anthropic branch. Injected so tests can pass a
            ``MagicMock`` without stubbing the whole pipecat package.
        chain_state: The shared ``_llm_chain_state`` dict from ``main``.
            On success the picker disables runtime failover (the user's
            choice is the source of truth, not a chain) by setting
            ``providers=[]``, ``active_idx=0``, ``service=None``.

    Returns:
        The constructed LLM service instance on success, ``None`` when:

        * ``cfg.llmModel`` is empty / missing / unrecognised
        * the required credential for the picked provider is missing
        * the prefix isn't one of the four supported
    """
    pick = get_llm_model(config)
    if not pick:
        return None

    if pick.startswith("anthropic/"):
        return _build_anthropic(
            pick,
            config,
            system_instruction=system_instruction,
            anthropic_service_cls=anthropic_service_cls,
            chain_state=chain_state,
        )

    # The remaining three providers all speak OpenAI-compat, so they share
    # one OpenAILLMService construction path with provider-specific knobs.
    provider_label: str
    base_url: str
    api_key: str
    model_id: str

    if pick.startswith("google/"):
        google_key = (config.get("googleAPIKey") or "").strip()
        if not google_key:
            logger.warning(
                "llmModel=%r selected but googleAPIKey is unset. "
                "Falling back to key-driven LLM detection.",
                pick,
            )
            return None
        provider_label = "google"
        base_url = "https://generativelanguage.googleapis.com/v1beta/openai/"
        api_key = google_key
        # Google's OpenAI-compat endpoint wants the bare model id, not the
        # "google/" prefix.
        model_id = pick[len("google/"):]

    elif pick.startswith("openai/"):
        jarvis_key = get_api_key(config).strip()
        if not jarvis_key.startswith("sk-or-"):
            logger.warning(
                "llmModel=%r selected but jarvisAPIKey is not an OpenRouter "
                "key (must start with sk-or-). Falling back to key-driven "
                "LLM detection.",
                pick,
            )
            return None
        provider_label = "openrouter"
        base_url = "https://openrouter.ai/api/v1"
        api_key = jarvis_key
        # OpenRouter routes by full slug -- pass the dropdown value as-is.
        model_id = pick

    elif pick.startswith("ollama:"):
        provider_label = "ollama"
        base_url = (
            config.get("ollamaUrl") or "http://localhost:11434/v1"
        ).strip()
        # OpenAI client requires a non-empty key even though Ollama ignores it.
        api_key = "ollama"
        model_id = pick[len("ollama:"):]

    else:
        # Unreachable given get_llm_model validates against VALID_LLM_MODELS,
        # but guard anyway so a future dropdown option without daemon support
        # degrades gracefully instead of raising.
        logger.warning(
            "llmModel=%r has unknown prefix, falling back to key-driven "
            "LLM detection.",
            pick,
        )
        return None

    # Lazy import keeps this module testable without pipecat installed.
    from pipecat.services.openai.llm import OpenAILLMService

    llm = OpenAILLMService(
        api_key=api_key,
        base_url=base_url,
        settings=OpenAILLMService.Settings(
            model=model_id,
            system_instruction=system_instruction,
        ),
    )
    logger.info(
        "LLM (user-pick): %s → %s | source: cfg.llmModel",
        provider_label,
        model_id,
    )
    chain_state["providers"] = []
    chain_state["active_idx"] = 0
    chain_state["service"] = None
    return llm


def _build_anthropic(
    pick: str,
    config: dict[str, Any],
    *,
    system_instruction: str,
    anthropic_service_cls: Any,
    chain_state: dict[str, Any],
) -> Any | None:
    """Construct the Anthropic-direct service for a ``anthropic/`` pick.

    Key resolution order: ``anthropicAPIKey`` -> ``jarvisAPIKey`` (only if
    it starts with ``sk-ant-``). Returns ``None`` if neither yields a key.
    """
    anthropic_key = (config.get("anthropicAPIKey") or "").strip()
    if not anthropic_key:
        jarvis_key = get_api_key(config).strip()
        if jarvis_key.startswith("sk-ant-"):
            anthropic_key = jarvis_key
    if not anthropic_key:
        logger.warning(
            "llmModel=%r selected but no Anthropic key found "
            "(set anthropicAPIKey or a jarvisAPIKey starting with sk-ant-). "
            "Falling back to key-driven LLM detection.",
            pick,
        )
        return None

    # Lazy import so tests don't need the real anthropic SDK.
    from anthropic import AsyncAnthropic

    model_id = pick[len("anthropic/"):]
    model_id = _ANTHROPIC_MODEL_DATE_SUFFIX.get(model_id, model_id)

    llm = anthropic_service_cls(
        api_key=anthropic_key,
        client=AsyncAnthropic(api_key=anthropic_key),
        settings=anthropic_service_cls.Settings(
            model=model_id,
            system_instruction=system_instruction,
        ),
    )
    logger.info(
        "LLM (user-pick): anthropic → %s | source: cfg.llmModel",
        model_id,
    )
    chain_state["providers"] = []
    chain_state["active_idx"] = 0
    chain_state["service"] = None
    return llm
