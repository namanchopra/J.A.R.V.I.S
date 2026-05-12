"""Pipeline-status payload builder for the v0.1.5 HUD pipeline indicator.

The HUD subscribes to ``pipeline_status`` events over the existing
``/ws/jarvis`` bridge so it can render which TTS / STT / LLM is actually
active (post-fallback) without polling. The schema is fixed and shared
with the React HUD agent — see ``CLAUDE.md`` v0.1.5 notes.

This module is intentionally split out of ``main.py`` for the same reason
``llm_picker.py`` is: the builder is a pure function over already-resolved
choices + the daemon config, so we can unit-test it without dragging in
the full pipecat / livekit / torch stack ``main`` loads at import time.

Schema (FIXED — coordinate with the HUD before changing):

    {
      "type": "pipeline_status",
      "tts": {"provider": str, "voice": str},
      "stt": {"model": str},
      "llm": {"provider": str, "model": str, "source": str},
      "wake_word": {"enabled": bool, "sensitivity": float},
    }

``llm.source`` is ``"user-pick"`` when ``cfg.llmModel`` was a recognised
value and the picker produced a service; otherwise ``"key-detected"``
(legacy key-driven chain or Anthropic-direct).
"""

from __future__ import annotations

import logging
from typing import Any, Final

logger = logging.getLogger("jarvis-daemon.main")

# Mapping from the ``cfg.llmModel`` prefix the user picked in the
# Connections panel dropdown to the provider label the HUD renders.
# Mirrors ``llm_picker.build_user_picked_llm`` branches.
_USER_PICK_PROVIDER_BY_PREFIX: Final[dict[str, str]] = {
    "google/": "google",
    "anthropic/": "anthropic",
    "openai/": "openrouter",
    "ollama:": "ollama",
}

# Mapping from the legacy chain ``provider_chain[0]["name"]`` strings used
# by ``_build_llm_provider_chain`` to the canonical lowercase provider
# label the HUD expects. Anything not in this map falls through to a
# lowercased best-effort label.
_CHAIN_NAME_TO_LABEL: Final[dict[str, str]] = {
    "OpenRouter": "openrouter",
    "Google AI Studio": "google",
    "Ollama (local)": "ollama",
}


def resolve_user_pick_llm(llm_model_pick: str) -> tuple[str, str] | None:
    """Resolve a ``cfg.llmModel`` value into ``(provider, model_id)``.

    Returns ``None`` when ``llm_model_pick`` is empty or has no recognised
    prefix. The model id matches what ``llm_picker`` actually sends to the
    SDK (post-prefix for google/anthropic/ollama, full slug for openrouter).
    The Anthropic dated-suffix mapping deliberately is NOT applied here —
    the HUD wants to show the user-facing pick, not the SDK-internal id.

    This is a pure helper so tests don't need to import ``llm_picker``.
    """
    if not llm_model_pick:
        return None
    if llm_model_pick.startswith("google/"):
        return "google", llm_model_pick[len("google/"):]
    if llm_model_pick.startswith("anthropic/"):
        return "anthropic", llm_model_pick[len("anthropic/"):]
    if llm_model_pick.startswith("openai/"):
        # OpenRouter routes by the vendor-prefixed slug; preserve as-is so
        # the HUD shows what the user actually picked.
        return "openrouter", llm_model_pick
    if llm_model_pick.startswith("ollama:"):
        return "ollama", llm_model_pick[len("ollama:"):]
    return None


def resolve_chain_provider_label(chain_name: str) -> str:
    """Map a legacy chain ``name`` string to the HUD's canonical label.

    Falls back to ``chain_name.lower().split()[0]`` for unknown names so a
    future provider added to ``_build_llm_provider_chain`` still surfaces
    something readable instead of an empty string.
    """
    if chain_name in _CHAIN_NAME_TO_LABEL:
        return _CHAIN_NAME_TO_LABEL[chain_name]
    return chain_name.lower().split()[0] if chain_name else ""


def build_pipeline_status(
    *,
    tts_provider: str,
    tts_voice: str | None,
    stt_model: str,
    llm_provider: str,
    llm_model: str,
    llm_source: str,
    wake_word_enabled: bool,
    wake_word_sensitivity: float,
) -> dict[str, Any]:
    """Assemble the ``pipeline_status`` event payload.

    All callers go through this so the schema stays in one place. The
    returned dict is JSON-serialisable as-is (no datetimes, no sets) and
    contains no secrets — only the resolved provider / model / voice
    identifiers the HUD needs to render the pipeline indicator.

    Args:
        tts_provider: Resolved TTS provider — ``"vibevoice"`` / ``"kokoro"``
            / ``"cartesia"``. This is the POST-FALLBACK value (e.g. when
            the user requested cartesia but no key was set and the daemon
            fell back to vibevoice, this is ``"vibevoice"``).
        tts_voice: Resolved voice id (provider-specific). ``None`` is
            tolerated and surfaces as the empty string so the HUD always
            sees a string.
        stt_model: Resolved STT model id from ``cfg.sttModel``.
        llm_provider: Lowercase provider label.
        llm_model: Model string actually passed to the LLM service.
        llm_source: ``"user-pick"`` when the user's dropdown choice was
            honoured; ``"key-detected"`` when the legacy key-driven chain
            (or Anthropic-direct) ran.
        wake_word_enabled: Mirrors ``get_wake_word_enabled(cfg)``.
        wake_word_sensitivity: Float in ``[0.3, 0.8]`` clamped at the call
            site; this helper preserves the value as-is.
    """
    return {
        "type": "pipeline_status",
        "tts": {
            "provider": tts_provider,
            "voice": tts_voice or "",
        },
        "stt": {
            "model": stt_model,
        },
        "llm": {
            "provider": llm_provider,
            "model": llm_model,
            "source": llm_source,
        },
        "wake_word": {
            "enabled": bool(wake_word_enabled),
            "sensitivity": float(wake_word_sensitivity),
        },
    }
