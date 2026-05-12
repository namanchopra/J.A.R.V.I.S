"""Tests for the v0.1.5 LLM picker.

The Connections panel's "LLM Model" dropdown writes one of four literal
values to ``cfg.llmModel``; the daemon must honor that pick exactly,
falling back to the legacy key-driven chain only when the value is empty,
unrecognised, or the required credential is missing.

These tests pin down:

* ``config.get_llm_model`` -- the pure helper that validates and returns
  the user's pick (or ``None``).
* ``llm_picker.build_user_picked_llm`` -- the constructor that turns a
  pick into a Pipecat LLM service. The function takes the OpenAI / Anthropic
  service classes via dependency injection so tests can pass ``MagicMock``s
  without dragging in the real pipecat / anthropic SDKs (which are heavy
  and intentionally NOT a test-time dependency for unit tests).
"""

from __future__ import annotations

import sys
import types
from typing import Any
from unittest.mock import MagicMock

import pytest

import config
import llm_picker


# ---------------------------------------------------------------------------
# Test helpers
# ---------------------------------------------------------------------------


def _install_fake_pipecat_openai(monkeypatch: pytest.MonkeyPatch) -> MagicMock:
    """Install a fake ``pipecat.services.openai.llm.OpenAILLMService`` module.

    Returns the MagicMock standing in for the service class so callers can
    assert on construction kwargs. The real pipecat package is heavy
    (LiveKit, torch, etc.) so we never import it in unit tests; the picker
    does ``from pipecat.services.openai.llm import OpenAILLMService`` lazily,
    so monkey-patching ``sys.modules`` for that path is enough.
    """
    fake_service = MagicMock(name="OpenAILLMService")
    fake_service.Settings = MagicMock(name="OpenAILLMService.Settings")

    mod_llm = types.ModuleType("pipecat.services.openai.llm")
    mod_llm.OpenAILLMService = fake_service  # type: ignore[attr-defined]

    # Provide minimal stubs for each intermediate package so the import
    # machinery doesn't fail walking the chain.
    for name in ("pipecat", "pipecat.services", "pipecat.services.openai"):
        if name not in sys.modules:
            monkeypatch.setitem(sys.modules, name, types.ModuleType(name))
    monkeypatch.setitem(sys.modules, "pipecat.services.openai.llm", mod_llm)
    return fake_service


def _install_fake_anthropic(monkeypatch: pytest.MonkeyPatch) -> MagicMock:
    """Install a fake ``anthropic.AsyncAnthropic`` for the picker's lazy import."""
    fake_async = MagicMock(name="AsyncAnthropic")
    mod = types.ModuleType("anthropic")
    mod.AsyncAnthropic = fake_async  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "anthropic", mod)
    return fake_async


def _fake_anthropic_service_cls() -> MagicMock:
    """Return a MagicMock standing in for ``AnthropicLLMService``.

    Pre-wires the ``.Settings`` attribute the picker also calls so tests
    can introspect both construction kwargs and Settings kwargs cleanly.
    """
    cls = MagicMock(name="AnthropicLLMService")
    cls.Settings = MagicMock(name="AnthropicLLMService.Settings")
    return cls


@pytest.fixture
def chain_state() -> dict[str, Any]:
    """A fresh ``_llm_chain_state`` dict the picker mutates on success."""
    return {"providers": [], "active_idx": 0, "service": None}


SYS_PROMPT = "You are Jarvis."


# ---------------------------------------------------------------------------
# config.get_llm_model -- pure helper
# ---------------------------------------------------------------------------


def test_get_llm_model_empty_returns_none() -> None:
    """No key, blank string, whitespace-only string -> ``None`` (legacy path)."""
    assert config.get_llm_model({}) is None
    assert config.get_llm_model({"llmModel": ""}) is None
    assert config.get_llm_model({"llmModel": "   "}) is None
    # Non-string types must not crash -- the helper just returns None.
    assert config.get_llm_model({"llmModel": None}) is None
    assert config.get_llm_model({"llmModel": 42}) is None  # type: ignore[dict-item]


def test_get_llm_model_returns_explicit_value() -> None:
    """All four dropdown values round-trip through the helper unchanged."""
    for value in config.VALID_LLM_MODELS:
        assert config.get_llm_model({"llmModel": value}) == value


def test_get_llm_model_trims_whitespace_around_valid_value() -> None:
    """Hand-edited config with stray whitespace must still match a valid pick."""
    assert (
        config.get_llm_model({"llmModel": "  anthropic/claude-haiku-4-5  "})
        == "anthropic/claude-haiku-4-5"
    )


def test_unknown_llm_model_falls_back(caplog: pytest.LogCaptureFixture) -> None:
    """Invented value not in VALID_LLM_MODELS -> ``None`` + WARNING log.

    The daemon must NEVER crash on bad config (voice software invariant).
    Returning ``None`` lets ``main`` fall through to the legacy chain.
    """
    bogus = "anthropic/claude-7-opus-fake"
    assert bogus not in config.VALID_LLM_MODELS  # sanity check
    with caplog.at_level("WARNING"):
        assert config.get_llm_model({"llmModel": bogus}) is None
    assert any(bogus in rec.message for rec in caplog.records), (
        "expected a WARNING log mentioning the bad model so daemon.log "
        "surfaces the misconfiguration"
    )


# ---------------------------------------------------------------------------
# llm_picker.build_user_picked_llm -- the constructor side
# ---------------------------------------------------------------------------
# Each branch checks: (1) the picker actually built something, (2) the
# right SDK / OpenAI-compat endpoint got the call, (3) the model id passed
# through correctly (post-prefix where the provider doesn't want the vendor
# prefix, full-slug for OpenRouter).


def test_google_prefix_routes_to_openrouter(
    monkeypatch: pytest.MonkeyPatch,
    chain_state: dict[str, Any],
) -> None:
    """v0.1.6: ``google/...`` routes through OpenRouter (was Google-direct in
    v0.1.5). The full slug ``google/gemini-2.5-flash`` is passed unchanged —
    OpenRouter resolves it server-side.
    """
    fake_openai = _install_fake_pipecat_openai(monkeypatch)

    cfg: dict[str, Any] = {
        "llmModel": "google/gemini-2.5-flash",
        "jarvisAPIKey": "sk-or-v1-openrouter-test",
    }
    result = llm_picker.build_user_picked_llm(
        cfg,
        system_instruction=SYS_PROMPT,
        anthropic_service_cls=_fake_anthropic_service_cls(),
        chain_state=chain_state,
    )
    assert result is not None, "picker should have built a service"

    fake_openai.assert_called_once()
    kwargs = fake_openai.call_args.kwargs
    assert kwargs["base_url"] == "https://openrouter.ai/api/v1", (
        "v0.1.6: every cloud pick is routed through OpenRouter"
    )
    assert kwargs["api_key"] == "sk-or-v1-openrouter-test"
    fake_openai.Settings.assert_called_once()
    settings_kwargs = fake_openai.Settings.call_args.kwargs
    assert settings_kwargs["model"] == "google/gemini-2.5-flash", (
        "OpenRouter routes by full slug — pass the dropdown value as-is"
    )
    assert settings_kwargs["system_instruction"] == SYS_PROMPT
    # Successful pick disables the failover chain.
    assert chain_state["providers"] == []
    assert chain_state["service"] is None


def test_google_prefix_falls_back_when_no_openrouter_key(
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
    chain_state: dict[str, Any],
) -> None:
    """v0.1.6: ``google/...`` requires an OpenRouter ``sk-or-`` key.
    A bare googleAPIKey no longer satisfies the pick (Google-direct path
    removed). Missing key -> ``None`` + WARNING -> legacy chain takes over.
    """
    _install_fake_pipecat_openai(monkeypatch)

    with caplog.at_level("WARNING"):
        result = llm_picker.build_user_picked_llm(
            {
                "llmModel": "google/gemini-2.5-flash",
                # A Google-direct key is no longer accepted for the user-pick
                # path. Legacy auto-detect still honours it.
                "googleAPIKey": "AIza-google-test",
            },
            system_instruction=SYS_PROMPT,
            anthropic_service_cls=_fake_anthropic_service_cls(),
            chain_state=chain_state,
        )
    assert result is None
    assert any("sk-or-" in rec.message for rec in caplog.records)


def test_anthropic_prefix_routes_to_openrouter(
    monkeypatch: pytest.MonkeyPatch,
    chain_state: dict[str, Any],
) -> None:
    """v0.1.6: ``anthropic/...`` routes through OpenRouter (was Anthropic-direct
    in v0.1.5). The full slug ``anthropic/claude-haiku-4-5`` is passed as-is —
    OpenRouter knows how to dispatch it.
    """
    fake_openai = _install_fake_pipecat_openai(monkeypatch)

    cfg: dict[str, Any] = {
        "llmModel": "anthropic/claude-haiku-4-5",
        "jarvisAPIKey": "sk-or-v1-openrouter-test",
    }
    result = llm_picker.build_user_picked_llm(
        cfg,
        system_instruction=SYS_PROMPT,
        anthropic_service_cls=_fake_anthropic_service_cls(),
        chain_state=chain_state,
    )
    assert result is not None

    fake_openai.assert_called_once()
    kwargs = fake_openai.call_args.kwargs
    assert kwargs["base_url"] == "https://openrouter.ai/api/v1"
    assert kwargs["api_key"] == "sk-or-v1-openrouter-test"
    fake_openai.Settings.assert_called_once()
    settings_kwargs = fake_openai.Settings.call_args.kwargs
    # OpenRouter resolves the model from the full slug; no date suffix needed.
    assert settings_kwargs["model"] == "anthropic/claude-haiku-4-5"
    assert settings_kwargs["system_instruction"] == SYS_PROMPT


def test_anthropic_prefix_falls_back_when_no_openrouter_key(
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
    chain_state: dict[str, Any],
) -> None:
    """v0.1.6: ``anthropic/...`` with only an Anthropic-direct ``sk-ant-`` key
    no longer satisfies the user-pick path. The picker returns ``None`` and
    the legacy auto-detect chain (which still honours sk-ant- direct) takes
    over.
    """
    _install_fake_pipecat_openai(monkeypatch)
    with caplog.at_level("WARNING"):
        result = llm_picker.build_user_picked_llm(
            {
                "llmModel": "anthropic/claude-haiku-4-5",
                # Even an sk-ant- jarvisAPIKey no longer satisfies the user-pick
                # path — OpenRouter is the source of truth for explicit picks.
                "jarvisAPIKey": "sk-ant-direct-key",
            },
            system_instruction=SYS_PROMPT,
            anthropic_service_cls=_fake_anthropic_service_cls(),
            chain_state=chain_state,
        )
    assert result is None
    assert any("sk-or-" in rec.message for rec in caplog.records)


def test_openai_prefix_routes_to_openrouter(
    monkeypatch: pytest.MonkeyPatch,
    chain_state: dict[str, Any],
) -> None:
    """``openai/...`` -> OpenRouter (sk-or- key required). The full slug
    ``openai/gpt-4o-mini`` goes through unchanged -- OpenRouter routes by
    the vendor-prefixed slug, NOT by the bare model id.
    """
    fake_openai = _install_fake_pipecat_openai(monkeypatch)

    cfg: dict[str, Any] = {
        "llmModel": "openai/gpt-4o-mini",
        "jarvisAPIKey": "sk-or-v1-openrouter-test",
    }
    result = llm_picker.build_user_picked_llm(
        cfg,
        system_instruction=SYS_PROMPT,
        anthropic_service_cls=_fake_anthropic_service_cls(),
        chain_state=chain_state,
    )
    assert result is not None

    kwargs = fake_openai.call_args.kwargs
    assert kwargs["base_url"] == "https://openrouter.ai/api/v1"
    assert kwargs["api_key"] == "sk-or-v1-openrouter-test"
    # OpenRouter wants the full prefixed slug for routing.
    assert (
        fake_openai.Settings.call_args.kwargs["model"] == "openai/gpt-4o-mini"
    )


def test_openai_prefix_falls_back_when_key_not_openrouter(
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
    chain_state: dict[str, Any],
) -> None:
    """``openai/...`` requires an ``sk-or-`` jarvisAPIKey. A non-OpenRouter
    key (or no key) -> ``None`` + WARNING.
    """
    _install_fake_pipecat_openai(monkeypatch)

    with caplog.at_level("WARNING"):
        result = llm_picker.build_user_picked_llm(
            {
                "llmModel": "openai/gpt-4o-mini",
                "jarvisAPIKey": "sk-ant-not-openrouter",
            },
            system_instruction=SYS_PROMPT,
            anthropic_service_cls=_fake_anthropic_service_cls(),
            chain_state=chain_state,
        )
    assert result is None
    assert any("sk-or-" in rec.message for rec in caplog.records)


def test_ollama_prefix_routes_to_localhost(
    monkeypatch: pytest.MonkeyPatch,
    chain_state: dict[str, Any],
) -> None:
    """``ollama:...`` -> OpenAILLMService against the local Ollama daemon.
    Model id is the post-prefix portion (``qwen3:4b``).
    """
    fake_openai = _install_fake_pipecat_openai(monkeypatch)

    cfg: dict[str, Any] = {"llmModel": "ollama:qwen3:4b"}
    result = llm_picker.build_user_picked_llm(
        cfg,
        system_instruction=SYS_PROMPT,
        anthropic_service_cls=_fake_anthropic_service_cls(),
        chain_state=chain_state,
    )
    assert result is not None

    kwargs = fake_openai.call_args.kwargs
    assert "localhost" in kwargs["base_url"], (
        "Ollama branch must default to localhost without contacting the network"
    )
    assert kwargs["base_url"] == "http://localhost:11434/v1"
    # OpenAI client requires a non-empty key; Ollama ignores it.
    assert kwargs["api_key"] == "ollama"
    assert fake_openai.Settings.call_args.kwargs["model"] == "qwen3:4b"


def test_ollama_prefix_respects_ollama_url_override(
    monkeypatch: pytest.MonkeyPatch,
    chain_state: dict[str, Any],
) -> None:
    """A user with Ollama on a non-default host can set ``ollamaUrl``; the
    picker must respect it instead of hardcoding localhost.
    """
    fake_openai = _install_fake_pipecat_openai(monkeypatch)

    cfg: dict[str, Any] = {
        "llmModel": "ollama:qwen3:4b",
        "ollamaUrl": "http://192.168.1.5:11434/v1",
    }
    result = llm_picker.build_user_picked_llm(
        cfg,
        system_instruction=SYS_PROMPT,
        anthropic_service_cls=_fake_anthropic_service_cls(),
        chain_state=chain_state,
    )
    assert result is not None
    assert (
        fake_openai.call_args.kwargs["base_url"] == "http://192.168.1.5:11434/v1"
    )


def test_empty_llm_model_returns_none_no_construction(
    monkeypatch: pytest.MonkeyPatch,
    chain_state: dict[str, Any],
) -> None:
    """``llmModel=""`` -> picker returns ``None`` and constructs NOTHING.
    This is the v0.1.4-compatible path -- every user who hasn't touched the
    dropdown must see identical behaviour to v0.1.4.
    """
    fake_openai = _install_fake_pipecat_openai(monkeypatch)
    fake_anth = _fake_anthropic_service_cls()

    assert llm_picker.build_user_picked_llm(
        {},
        system_instruction=SYS_PROMPT,
        anthropic_service_cls=fake_anth,
        chain_state=chain_state,
    ) is None
    assert llm_picker.build_user_picked_llm(
        {"llmModel": ""},
        system_instruction=SYS_PROMPT,
        anthropic_service_cls=fake_anth,
        chain_state=chain_state,
    ) is None

    fake_openai.assert_not_called()
    fake_anth.assert_not_called()
