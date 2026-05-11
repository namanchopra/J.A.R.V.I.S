"""Tests for the v0.1.2 config accessor helpers.

The frontend promotes 8 settings from React ``useState`` into
``~/.jarvis/config.json``. This file pins down the daemon-side helpers that
read those settings and ensures the defaults preserve v0.1.1 behaviour when
no key is set.

Covered helpers (defined in ``config.py``):

* ``get_tts_provider`` — ``"vibevoice"`` | ``"kokoro"`` | ``"cartesia"``
* ``get_stt_model``    — ``"whisper-small.en"`` | ``"whisper-tiny.en"`` | ``"faster-whisper"``
* ``get_voice_preset`` — free-form, ``None`` when unset
* ``get_mic_device``   — free-form device name, ``None`` when unset
* ``get_wake_word_enabled`` — bool, defaults ``True``
* ``get_google_api_key`` / ``get_anthropic_api_key`` / ``get_cartesia_api_key``
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any

import pytest

import config


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def empty_cfg() -> dict[str, Any]:
    """A config dict with no v0.1.2 keys set."""
    return {}


@pytest.fixture
def temp_jarvis_home(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """Point ``Path.home()`` at a temp dir so ``load_config()`` reads from
    ``<tmp>/.jarvis/config.json`` instead of the user's real home.
    """
    monkeypatch.setattr(Path, "home", lambda: tmp_path)
    return tmp_path


def _write_config(home: Path, payload: dict[str, Any]) -> Path:
    cfg_dir = home / ".jarvis"
    cfg_dir.mkdir(parents=True, exist_ok=True)
    cfg_path = cfg_dir / "config.json"
    cfg_path.write_text(json.dumps(payload), encoding="utf-8")
    return cfg_path


# ---------------------------------------------------------------------------
# TTS provider
# ---------------------------------------------------------------------------


def test_get_tts_provider_defaults_to_vibevoice(empty_cfg: dict[str, Any]) -> None:
    """Empty config -> ``vibevoice`` to preserve v0.1.1 behaviour."""
    assert config.get_tts_provider(empty_cfg) == "vibevoice"


def test_get_tts_provider_respects_explicit_value() -> None:
    """User-set provider wins over the default."""
    assert config.get_tts_provider({"ttsProvider": "kokoro"}) == "kokoro"
    assert config.get_tts_provider({"ttsProvider": "cartesia"}) == "cartesia"
    assert config.get_tts_provider({"ttsProvider": "vibevoice"}) == "vibevoice"


def test_get_tts_provider_falls_back_on_unknown_value(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Per spec: bad values silently fall back with a warning log."""
    with caplog.at_level("WARNING"):
        assert config.get_tts_provider({"ttsProvider": "bogusengine"}) == "vibevoice"
    assert any("bogusengine" in rec.message for rec in caplog.records)


def test_get_tts_provider_case_insensitive_and_trimmed() -> None:
    """Accept ``"Kokoro"`` / ``"  cartesia  "`` from a hand-edited config."""
    assert config.get_tts_provider({"ttsProvider": "Kokoro"}) == "kokoro"
    assert config.get_tts_provider({"ttsProvider": "  cartesia  "}) == "cartesia"


def test_get_tts_provider_blank_string_uses_default() -> None:
    """Empty / whitespace string is treated as "unset", not as an error."""
    assert config.get_tts_provider({"ttsProvider": ""}) == "vibevoice"
    assert config.get_tts_provider({"ttsProvider": "   "}) == "vibevoice"


# ---------------------------------------------------------------------------
# STT model
# ---------------------------------------------------------------------------


def test_get_stt_model_defaults() -> None:
    """Empty config -> ``whisper-small.en``."""
    assert config.get_stt_model({}) == "whisper-small.en"


def test_get_stt_model_explicit_tiny() -> None:
    """The daemon must NOT quietly upgrade tiny.en to small.en (req #4)."""
    assert (
        config.get_stt_model({"sttModel": "whisper-tiny.en"}) == "whisper-tiny.en"
    )


def test_get_stt_model_explicit_faster_whisper() -> None:
    assert (
        config.get_stt_model({"sttModel": "faster-whisper"}) == "faster-whisper"
    )


def test_get_stt_model_unknown_falls_back(
    caplog: pytest.LogCaptureFixture,
) -> None:
    with caplog.at_level("WARNING"):
        assert (
            config.get_stt_model({"sttModel": "whisper-huge.en"})
            == "whisper-small.en"
        )
    assert any("whisper-huge.en" in rec.message for rec in caplog.records)


# ---------------------------------------------------------------------------
# Wake word
# ---------------------------------------------------------------------------


def test_get_wake_word_enabled_default_true() -> None:
    """v0.1.1 always inserted the WakeWordGate — the default must stay True."""
    assert config.get_wake_word_enabled({}) is True


def test_get_wake_word_enabled_explicit_false() -> None:
    assert config.get_wake_word_enabled({"wakeWordEnabled": False}) is False


def test_get_wake_word_enabled_string_coercion() -> None:
    """Tolerate hand-edited string booleans without crashing."""
    assert config.get_wake_word_enabled({"wakeWordEnabled": "false"}) is False
    assert config.get_wake_word_enabled({"wakeWordEnabled": "true"}) is True
    assert config.get_wake_word_enabled({"wakeWordEnabled": "0"}) is False
    assert config.get_wake_word_enabled({"wakeWordEnabled": "1"}) is True


# ---------------------------------------------------------------------------
# Voice preset
# ---------------------------------------------------------------------------


def test_get_voice_preset_returns_none_when_unset() -> None:
    """Without a provider arg, unset ``voicePreset`` returns ``None`` so
    callers can keep their own per-provider defaults.
    """
    assert config.get_voice_preset({}) is None


def test_get_voice_preset_explicit_value_wins() -> None:
    assert config.get_voice_preset({"voicePreset": "en-Alice_woman"}) == "en-Alice_woman"


def test_get_voice_preset_provider_default_when_unset() -> None:
    """When the caller passes ``provider=...`` and the user hasn't set a
    voice, return the bundled default for that provider.
    """
    assert config.get_voice_preset({}, provider="vibevoice") == "en-Carter_man"
    assert config.get_voice_preset({}, provider="kokoro") == "af_sarah"
    # cartesia default is a UUID — just check it's non-empty
    cartesia_default = config.get_voice_preset({}, provider="cartesia")
    assert isinstance(cartesia_default, str) and cartesia_default


def test_get_voice_preset_explicit_wins_over_provider_default() -> None:
    """Even with ``provider=kokoro``, an explicit ``voicePreset`` wins."""
    got = config.get_voice_preset({"voicePreset": "custom"}, provider="kokoro")
    assert got == "custom"


# ---------------------------------------------------------------------------
# Mic device
# ---------------------------------------------------------------------------


def test_get_mic_device_returns_none_when_unset() -> None:
    assert config.get_mic_device({}) is None
    assert config.get_mic_device({"micInputDevice": ""}) is None
    assert config.get_mic_device({"micInputDevice": "   "}) is None


def test_get_mic_device_returns_trimmed_value() -> None:
    assert (
        config.get_mic_device({"micInputDevice": " MacBook Pro Microphone "})
        == "MacBook Pro Microphone"
    )


# ---------------------------------------------------------------------------
# API keys
# ---------------------------------------------------------------------------


def test_api_key_helpers_round_trip(
    temp_jarvis_home: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Write google / anthropic / cartesia keys to disk, read them back.

    Exercises the full ``load_config -> get_*_api_key`` path so any future
    refactor that splits config layers (env vs file vs defaults) catches
    regressions here.
    """
    # Clear the env override path so the Cartesia helper reads from config.
    monkeypatch.delenv("CARTESIA_API_KEY", raising=False)
    _write_config(
        temp_jarvis_home,
        {
            "googleAPIKey": "AIza-google-test-key",
            "anthropicAPIKey": "sk-ant-test-key",
            "cartesiaAPIKey": "cartesia-test-key",
        },
    )
    cfg = config.load_config()
    assert config.get_google_api_key(cfg) == "AIza-google-test-key"
    assert config.get_anthropic_api_key(cfg) == "sk-ant-test-key"
    assert config.get_cartesia_api_key(cfg) == "cartesia-test-key"


def test_api_key_helpers_return_empty_when_unset() -> None:
    """Helpers must NEVER return ``None`` — empty string is the contract."""
    assert config.get_google_api_key({}) == ""
    assert config.get_anthropic_api_key({}) == ""
    # Cartesia helper checks env vars; clear that path before asserting.
    saved = os.environ.pop("CARTESIA_API_KEY", None)
    try:
        assert config.get_cartesia_api_key({}) == ""
    finally:
        if saved is not None:
            os.environ["CARTESIA_API_KEY"] = saved


def test_cartesia_api_key_falls_back_to_env(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """``cartesiaAPIKey`` unset in config -> read ``CARTESIA_API_KEY`` env var."""
    monkeypatch.setenv("CARTESIA_API_KEY", "from-env-key")
    assert config.get_cartesia_api_key({}) == "from-env-key"


def test_cartesia_config_wins_over_env(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """When both sources are set, config takes precedence."""
    monkeypatch.setenv("CARTESIA_API_KEY", "from-env-key")
    assert (
        config.get_cartesia_api_key({"cartesiaAPIKey": "from-config"})
        == "from-config"
    )


# ---------------------------------------------------------------------------
# load_config() integration — the new v0.1.2 keys aren't required, the
# file load path must keep returning defaults when they're absent.
# ---------------------------------------------------------------------------


def test_load_config_without_v012_keys_uses_helper_defaults(
    temp_jarvis_home: Path,
) -> None:
    """A v0.1.1-shaped config file (no v0.1.2 keys) -> helpers all return
    their documented defaults.
    """
    _write_config(temp_jarvis_home, {"jarvisVoice": "Daniel"})  # legacy-only
    cfg = config.load_config()
    assert config.get_tts_provider(cfg) == "vibevoice"
    assert config.get_stt_model(cfg) == "whisper-small.en"
    assert config.get_wake_word_enabled(cfg) is True
    assert config.get_voice_preset(cfg) is None
    assert config.get_mic_device(cfg) is None
