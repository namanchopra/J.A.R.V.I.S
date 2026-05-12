"""Tests for the v0.1.5 pipeline-status payload builder.

The daemon emits a single ``pipeline_status`` event over the existing
``/ws/jarvis`` bridge after every pipeline build so the HUD can render
the resolved TTS / STT / LLM indicator without polling. The schema is
fixed and shared with the React HUD agent; if these tests change, the
HUD must change too.

These tests cover the pure builder in ``pipeline_status.py`` -- they
deliberately avoid importing ``main`` so the test suite stays fast and
doesn't drag in pipecat / livekit / torch. The same pattern that
``test_llm_picker.py`` uses for stubbing SDK imports is preserved here.
"""

from __future__ import annotations

from typing import Any

import pytest

import pipeline_status


# ---------------------------------------------------------------------------
# Test helpers
# ---------------------------------------------------------------------------


def _build_with_defaults(**overrides: Any) -> dict[str, Any]:
    """Construct a payload with sensible defaults so tests can override one
    field at a time without restating the full kwargs each time.
    """
    defaults: dict[str, Any] = {
        "tts_provider": "vibevoice",
        "tts_voice": "en-Carter_man",
        "stt_model": "whisper-small.en",
        "llm_provider": "openrouter",
        "llm_model": "openai/gpt-4o-mini",
        "llm_source": "user-pick",
        "wake_word_enabled": True,
        "wake_word_sensitivity": 0.5,
    }
    defaults.update(overrides)
    return pipeline_status.build_pipeline_status(**defaults)


def _simulate_pipeline_emit(
    cfg: dict[str, Any],
    *,
    tts_choice: str,
    voice_choice: str | None,
    stt_choice: str,
    user_pick_succeeded: bool,
    fallback_llm_provider: str = "openrouter",
    fallback_llm_model: str = "google/gemini-2.5-flash",
) -> dict[str, Any]:
    """Mirror the post-build emit path in ``main.create_pipeline_components``.

    Translates ``cfg`` + the three resolved voice-config choices + the LLM
    picker outcome into the same payload the daemon ships over the WS, so
    the tests can pin down the exact shape without spinning up the
    pipecat pipeline. Keep this aligned with the wiring in
    ``main.create_pipeline_components`` (see the "v0.1.5 pipeline-status"
    block right after the voice-config one-liner log).
    """
    if user_pick_succeeded:
        pick = cfg.get("llmModel", "")
        resolved = pipeline_status.resolve_user_pick_llm(pick)
        # ``user_pick_succeeded`` is the test's assertion that the picker
        # returned non-None, so the resolver must too -- guard with assert
        # to surface mis-specified test inputs immediately.
        assert resolved is not None, (
            f"Test asked for user_pick_succeeded but llmModel={pick!r} "
            "is not a recognised picker prefix."
        )
        llm_provider, llm_model = resolved
        llm_source = "user-pick"
    else:
        llm_provider = fallback_llm_provider
        llm_model = fallback_llm_model
        llm_source = "key-detected"

    return pipeline_status.build_pipeline_status(
        tts_provider=tts_choice,
        tts_voice=voice_choice,
        stt_model=stt_choice,
        llm_provider=llm_provider,
        llm_model=llm_model,
        llm_source=llm_source,
        wake_word_enabled=bool(cfg.get("wakeWordEnabled", True)),
        wake_word_sensitivity=float(cfg.get("jarvisWakeSensitivity", 0.5)),
    )


# ---------------------------------------------------------------------------
# Required tests from the v0.1.5 spec
# ---------------------------------------------------------------------------


def test_pipeline_status_payload_shape() -> None:
    """The four top-level keys and the right nested shape are present.

    The HUD wires to this exact schema; missing keys or extra nesting
    levels would break the indicator rendering.
    """
    payload = _build_with_defaults()

    # Top-level shape.
    assert payload["type"] == "pipeline_status"
    assert set(payload.keys()) == {"type", "tts", "stt", "llm", "wake_word"}

    # tts: provider + voice (both strings).
    assert isinstance(payload["tts"], dict)
    assert set(payload["tts"].keys()) == {"provider", "voice"}
    assert isinstance(payload["tts"]["provider"], str)
    assert isinstance(payload["tts"]["voice"], str)

    # stt: model (string).
    assert isinstance(payload["stt"], dict)
    assert set(payload["stt"].keys()) == {"model"}
    assert isinstance(payload["stt"]["model"], str)

    # llm: provider + model + source (all strings).
    assert isinstance(payload["llm"], dict)
    assert set(payload["llm"].keys()) == {"provider", "model", "source"}
    assert isinstance(payload["llm"]["provider"], str)
    assert isinstance(payload["llm"]["model"], str)
    assert payload["llm"]["source"] in {"user-pick", "key-detected"}

    # wake_word: enabled (bool) + sensitivity (float).
    assert isinstance(payload["wake_word"], dict)
    assert set(payload["wake_word"].keys()) == {"enabled", "sensitivity"}
    assert isinstance(payload["wake_word"]["enabled"], bool)
    assert isinstance(payload["wake_word"]["sensitivity"], float)


def test_pipeline_status_source_user_pick() -> None:
    """``cfg.llmModel`` recognised by the picker -> ``source="user-pick"``.

    Simulates the v0.1.5 happy path: user opened the Connections panel,
    picked Anthropic, the daemon honoured it. The provider label must be
    ``"anthropic"`` and the model must be the post-prefix id the HUD
    renders (not the dated SDK suffix -- that's an Anthropic-SDK-internal
    detail the HUD doesn't need to see).
    """
    cfg = {
        "llmModel": "anthropic/claude-haiku-4-5",
        "anthropicAPIKey": "sk-ant-test",
    }
    payload = _simulate_pipeline_emit(
        cfg,
        tts_choice="vibevoice",
        voice_choice="en-Carter_man",
        stt_choice="whisper-small.en",
        user_pick_succeeded=True,
    )
    assert payload["llm"]["source"] == "user-pick"
    assert payload["llm"]["provider"] == "anthropic"
    assert payload["llm"]["model"] == "claude-haiku-4-5"


def test_pipeline_status_source_key_detected() -> None:
    """Empty ``cfg.llmModel`` -> legacy chain ran -> ``source="key-detected"``.

    This is the v0.1.4-compatible path. Every user who hasn't touched the
    new dropdown must see ``source="key-detected"`` so the HUD can render
    the "auto-selected from API keys" label instead of "user pick".
    """
    cfg = {
        "llmModel": "",
        "jarvisAPIKey": "sk-or-v1-openrouter",
    }
    payload = _simulate_pipeline_emit(
        cfg,
        tts_choice="vibevoice",
        voice_choice="en-Carter_man",
        stt_choice="whisper-small.en",
        user_pick_succeeded=False,
        fallback_llm_provider="openrouter",
        fallback_llm_model="google/gemini-2.5-flash",
    )
    assert payload["llm"]["source"] == "key-detected"
    assert payload["llm"]["provider"] == "openrouter"
    assert payload["llm"]["model"] == "google/gemini-2.5-flash"


def test_pipeline_status_resolved_provider_after_fallback() -> None:
    """User picked cartesia but no cartesiaAPIKey -> daemon falls back to
    vibevoice. The payload must reflect the POST-FALLBACK provider, not
    the user's original request, so the HUD can show "cartesia requested
    but unavailable -- running vibevoice".

    The TTS resolution itself happens inside ``_build_tts_service`` (which
    returns the resolved provider as its second tuple element); this test
    pins down the payload contract that consumes that resolved value.
    """
    # Simulate ``_build_tts_service`` having returned the fallback choice
    # for a user that picked cartesia. The daemon's contract is: the
    # second return value of ``_build_tts_service`` is what's emitted, NOT
    # the original ``cfg.ttsProvider``.
    cfg = {
        "ttsProvider": "cartesia",  # user's REQUEST
        "cartesiaAPIKey": "",       # but no key -> fallback fires
        "llmModel": "",
    }
    # ``tts_choice`` is what ``_build_tts_service`` actually returned --
    # i.e. the fallback, not "cartesia".
    payload = _simulate_pipeline_emit(
        cfg,
        tts_choice="vibevoice",          # post-fallback
        voice_choice="en-Carter_man",
        stt_choice="whisper-small.en",
        user_pick_succeeded=False,
    )
    assert payload["tts"]["provider"] == "vibevoice", (
        "payload must reflect the resolved (post-fallback) provider, not "
        "the user's original cfg.ttsProvider request"
    )
    assert payload["tts"]["provider"] != cfg["ttsProvider"]


# ---------------------------------------------------------------------------
# Additional edge-case coverage (still pure, no main.py import)
# ---------------------------------------------------------------------------


def test_pipeline_status_none_voice_becomes_empty_string() -> None:
    """Cartesia / Kokoro / VibeVoice can all return ``None`` for the voice
    when the user hasn't set ``voicePreset`` and there's no legacy override.
    The HUD wants a string everywhere, so the builder normalises to ``""``.
    """
    payload = _build_with_defaults(tts_voice=None)
    assert payload["tts"]["voice"] == ""
    assert isinstance(payload["tts"]["voice"], str)


def test_pipeline_status_wake_word_disabled() -> None:
    """``wakeWordEnabled=false`` round-trips through the payload as a bool."""
    payload = _build_with_defaults(wake_word_enabled=False)
    assert payload["wake_word"]["enabled"] is False


def test_pipeline_status_wake_sensitivity_preserves_float() -> None:
    """Sensitivity is preserved verbatim as a float; the HUD does the
    0.3-0.8 clamping at render time so the daemon doesn't silently lie
    about what's in config.
    """
    payload = _build_with_defaults(wake_word_sensitivity=0.7)
    assert payload["wake_word"]["sensitivity"] == 0.7
    # Even when an int sneaks through ``config.get`` we want a float in
    # the JSON so the HUD schema check stays simple.
    payload = _build_with_defaults(wake_word_sensitivity=1)
    assert isinstance(payload["wake_word"]["sensitivity"], float)


def test_pipeline_status_serialises_to_json() -> None:
    """The payload must be JSON-serialisable as-is (no datetimes / sets)
    because ``main`` ships it via ``json.dumps(payload)`` directly."""
    import json

    payload = _build_with_defaults()
    raw = json.dumps(payload)
    roundtrip = json.loads(raw)
    assert roundtrip == payload


# ---------------------------------------------------------------------------
# Resolver helpers (used by the live emit path in ``main``)
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "pick,expected",
    [
        ("google/gemini-2.5-flash", ("google", "gemini-2.5-flash")),
        ("anthropic/claude-haiku-4-5", ("anthropic", "claude-haiku-4-5")),
        ("openai/gpt-4o-mini", ("openrouter", "openai/gpt-4o-mini")),
        ("ollama:qwen3:4b", ("ollama", "qwen3:4b")),
    ],
)
def test_resolve_user_pick_llm_recognised_prefixes(
    pick: str, expected: tuple[str, str]
) -> None:
    """Each of the four supported dropdown values maps to the canonical
    (provider, model) tuple the HUD renders. OpenRouter is the only one
    that keeps the vendor prefix in the model id (it routes by full slug).
    """
    assert pipeline_status.resolve_user_pick_llm(pick) == expected


def test_resolve_user_pick_llm_unknown_returns_none() -> None:
    """Empty / unknown picks return None so callers can fall back."""
    assert pipeline_status.resolve_user_pick_llm("") is None
    assert pipeline_status.resolve_user_pick_llm("bogus") is None
    assert pipeline_status.resolve_user_pick_llm("anthropic-but-no-slash") is None


@pytest.mark.parametrize(
    "chain_name,expected",
    [
        ("OpenRouter", "openrouter"),
        ("Google AI Studio", "google"),
        ("Ollama (local)", "ollama"),
    ],
)
def test_resolve_chain_provider_label_known(chain_name: str, expected: str) -> None:
    """The legacy ``_build_llm_provider_chain`` names map to the HUD's
    canonical lowercase labels."""
    assert pipeline_status.resolve_chain_provider_label(chain_name) == expected


def test_resolve_chain_provider_label_unknown_lowercases() -> None:
    """A future provider name we haven't taught the resolver about still
    surfaces something readable -- the first word lowercased. Empty input
    returns ''.
    """
    assert pipeline_status.resolve_chain_provider_label("") == ""
    assert pipeline_status.resolve_chain_provider_label("FutureProvider X") == (
        "futureprovider"
    )
