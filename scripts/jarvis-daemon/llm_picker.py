"""User-pick LLM constructor for the Connections-panel dropdown.

The Connections panel exposes four options for ``cfg.llmModel``:

* ``"google/gemini-2.5-flash"``     -> OpenRouter
* ``"anthropic/claude-haiku-4-5"``  -> OpenRouter
* ``"openai/gpt-4o-mini"``          -> OpenRouter
* ``"ollama:qwen3:4b"``             -> local Ollama (OpenAI-compat)

OpenRouter is the source of truth for cloud LLM routing (since v0.1.6).
The previous Google-direct and Anthropic-direct branches were removed —
one OpenRouter key now unlocks every cloud model in the dropdown.

Empty / missing / unrecognised values return ``None`` and the daemon falls
back to the legacy key-driven chain (``main._build_llm_provider_chain``).
A user picking a cloud model without an ``sk-or-`` key in ``jarvisAPIKey``
also returns ``None`` with a warning so the daemon stays up and the
legacy auto-detect path takes over (which still honours ``sk-ant-`` direct
keys for users who haven't migrated yet).

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


def build_user_picked_llm(
    config: dict[str, Any],
    *,
    system_instruction: str,
    anthropic_service_cls: Any,  # kept in signature for callsite compat; unused
    chain_state: dict[str, Any],
) -> Any | None:
    """Build the LLM service the user explicitly picked in the UI.

    Args:
        config: Loaded daemon config (``dict[str, Any]``).
        system_instruction: The Jarvis system prompt to pass into the LLM
            service constructor. Injected from ``main`` so this module
            doesn't import the heavy ``pipecat_llm`` module just for a
            constant.
        anthropic_service_cls: Retained in the signature so ``main.py``
            doesn't need to change its call site. Unused since v0.1.6 —
            OpenRouter is the only cloud route.
        chain_state: The shared ``_llm_chain_state`` dict from ``main``.
            On success the picker disables runtime failover (the user's
            choice is the source of truth, not a chain) by setting
            ``providers=[]``, ``active_idx=0``, ``service=None``.

    Returns:
        The constructed LLM service instance on success, ``None`` when:

        * ``cfg.llmModel`` is empty / missing / unrecognised
        * the user picked a cloud model but ``jarvisAPIKey`` isn't an
          OpenRouter key (``sk-or-...``)
        * the prefix isn't one of the four supported
    """
    del anthropic_service_cls  # explicitly unused — see docstring

    pick = get_llm_model(config)
    if not pick:
        return None

    provider_label: str
    base_url: str
    api_key: str
    model_id: str

    if pick.startswith("ollama:"):
        # Local Ollama stays local — OpenRouter doesn't proxy local models.
        provider_label = "ollama"
        base_url = (
            config.get("ollamaUrl") or "http://localhost:11434/v1"
        ).strip()
        # OpenAI client requires a non-empty key even though Ollama ignores it.
        api_key = "ollama"
        model_id = pick[len("ollama:"):]

    elif (
        pick.startswith("google/")
        or pick.startswith("anthropic/")
        or pick.startswith("openai/")
    ):
        # All cloud picks route through OpenRouter. One key, one billing
        # surface, one auth path. The previous Google-direct and
        # Anthropic-direct branches were removed in v0.1.6 per
        # "OpenRouter is the source of truth".
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
        # OpenRouter routes by full slug — pass the dropdown value as-is.
        # google/gemini-2.5-flash, anthropic/claude-haiku-4-5, and
        # openai/gpt-4o-mini are all valid OpenRouter model ids.
        model_id = pick

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
