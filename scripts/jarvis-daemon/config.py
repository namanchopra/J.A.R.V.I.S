"""Configuration loader for the Jarvis voice daemon.

Reads ~/.awm/config.json and exposes jarvis-relevant fields with safe defaults.
Supports both the new ``jarvis*`` key prefix and the legacy ``dex*`` prefix
for backward compatibility with installs that haven't been migrated yet.
All functions handle missing keys and missing files gracefully.
"""

from __future__ import annotations

import json
import logging
from pathlib import Path
from typing import Any, Final

logger = logging.getLogger("jarvis-daemon.config")

# ---------------------------------------------------------------------------
# v0.1.2 voice-config defaults
# ---------------------------------------------------------------------------
# These keep the daemon behaving exactly like v0.1.1 when no new keys are set.
# Per-provider defaults are looked up in ``_VOICE_PRESET_DEFAULTS`` below.

_DEFAULT_TTS_PROVIDER: Final[str] = "vibevoice"
_DEFAULT_STT_MODEL: Final[str] = "whisper-small.en"
_DEFAULT_WAKE_WORD_ENABLED: Final[bool] = True

# Valid choices — anything outside these silently falls back to the default
# (per task spec: "bad values silently fall back to defaults with a warning log").
_VALID_TTS_PROVIDERS: Final[frozenset[str]] = frozenset(
    {"vibevoice", "kokoro", "cartesia"}
)
_VALID_STT_MODELS: Final[frozenset[str]] = frozenset(
    {"whisper-small.en", "whisper-tiny.en", "faster-whisper"}
)

# v0.1.5 LLM picker — the four options exposed in the Connections panel
# dropdown. Empty string means "preserve key-driven detection" (the legacy
# behaviour every v0.1.0--v0.1.4 user already gets). Anything outside this
# set logs a warning and falls back to the legacy chain — never crash on
# bad config (this is voice software).
VALID_LLM_MODELS: Final[frozenset[str]] = frozenset(
    {
        "google/gemini-2.5-flash",
        "anthropic/claude-haiku-4-5",
        "openai/gpt-4o-mini",
        "ollama:qwen3:4b",
    }
)

# Per-provider default voice preset. Returned only when the user has NOT set
# ``voicePreset`` in config (so the daemon keeps its legacy hardcoded voices).
_VOICE_PRESET_DEFAULTS: Final[dict[str, str]] = {
    "vibevoice": "en-Carter_man",
    "kokoro": "af_sarah",
    "cartesia": "1463a4e1-56a1-4b41-b257-728d56e93605",
}

# Default config values for jarvis-relevant fields.
_DEFAULTS: dict[str, Any] = {
    "mobileAPIPort": 4422,
    "mobileAPIToken": "",
    "jarvisEnabled": False,
    "jarvisProvider": "cli",
    "jarvisAPIKey": "",
    "jarvisVoice": "Daniel",
    "jarvisAmbientEnabled": True,
    "jarvisVerbosity": "concise",
    "jarvisPicovoiceKey": "",
    "jarvisWakeWordModel": "",
    "jarvisWakeSensitivity": 0.5,
    "jarvisElevenLabsKey": "",
    "jarvisElevenLabsVoice": "",
}


def _config_path() -> Path:
    """Return the path to the Jarvis config file.

    Prefers ``~/.jarvis/config.json`` (new location). Falls back to
    ``~/.awm/config.json`` (legacy) only if the new path does not exist —
    this matters during the Phase 1 → Phase 2 transition and for installs
    where the migration shim hasn't run yet.
    """
    new = Path.home() / ".jarvis" / "config.json"
    legacy = Path.home() / ".awm" / "config.json"
    if new.exists():
        return new
    if legacy.exists():
        return legacy
    return new  # default to new path so a fresh install writes there


def load_config() -> dict[str, Any]:
    """Load configuration from ~/.awm/config.json.

    Reads ``jarvis*`` keys preferentially, falls back to ``dex*`` keys for
    backward compat with installs that haven't been migrated yet. Always
    returns a dict with ``jarvis*`` keys populated. Missing keys fall back
    to defaults. If the file doesn't exist or is malformed, returns a dict
    of pure defaults.
    """
    path = _config_path()
    config: dict[str, Any] = dict(_DEFAULTS)

    if not path.exists():
        logger.warning("Config file not found at %s, using defaults", path)
        return config

    try:
        raw = path.read_text(encoding="utf-8")
        parsed = json.loads(raw)
        if not isinstance(parsed, dict):
            logger.warning("Config file is not a JSON object, using defaults")
            return config

        # Backward-compat: read legacy dex* keys into jarvis* slots.
        # If both are present, jarvis* wins (config.update below overlays parsed).
        _LEGACY_KEY_MAP = {
            "jarvisEnabled":         "dexEnabled",
            "jarvisProvider":        "dexProvider",
            "jarvisAPIKey":          "dexAPIKey",
            "jarvisVoice":           "dexVoice",
            "jarvisAmbientEnabled":  "dexAmbientEnabled",
            "jarvisVerbosity":       "dexVerbosity",
            "jarvisPicovoiceKey":    "dexPicovoiceKey",
            "jarvisWakeWordModel":   "dexWakeWordModel",
            "jarvisWakeSensitivity": "dexWakeSensitivity",
            "jarvisElevenLabsKey":   "dexElevenLabsKey",
            "jarvisElevenLabsVoice": "dexElevenLabsVoice",
        }
        for new_key, legacy_key in _LEGACY_KEY_MAP.items():
            if new_key not in parsed and legacy_key in parsed:
                config[new_key] = parsed[legacy_key]

        # Overlay parsed values onto defaults (new jarvis* keys take precedence
        # over both defaults AND any legacy values copied above).
        config.update(parsed)
    except (json.JSONDecodeError, OSError) as exc:
        logger.warning("Failed to read config: %s, using defaults", exc)

    return config


def get_api_key(config: dict[str, Any] | None = None) -> str:
    """Return the API key from config.

    Reads ``jarvisAPIKey`` first, falls back to ``dexAPIKey`` for legacy
    configs. If no config dict is provided, loads from disk.
    """
    if config is None:
        config = load_config()
    key = config.get("jarvisAPIKey", "")
    if not key:
        key = config.get("dexAPIKey", "")
    return str(key)


def get_ws_url(config: dict[str, Any] | None = None) -> str:
    """Return the WebSocket URL for connecting to the Go app.

    Format: ws://localhost:{port}/ws/jarvis
    Port comes from mobileAPIPort in config (default 4422).
    """
    if config is None:
        config = load_config()
    port = config.get("mobileAPIPort", 4422)
    try:
        port = int(port)
    except (TypeError, ValueError):
        port = 4422
    return f"ws://localhost:{port}/ws/jarvis"


def get_auth_token(config: dict[str, Any] | None = None) -> str:
    """Return the mobileAPIToken for WebSocket authentication."""
    if config is None:
        config = load_config()
    return str(config.get("mobileAPIToken", ""))


# ---------------------------------------------------------------------------
# v0.1.2 accessors — TTS / STT / voice / mic / wake word
# ---------------------------------------------------------------------------
# Each accessor is a pure function that takes the already-loaded config dict.
# Unknown / blank values silently fall back to the default and emit a
# debug-level note (the task spec forbids hard validation here).


def get_tts_provider(config: dict[str, Any] | None = None) -> str:
    """Return the configured TTS provider.

    One of ``"vibevoice"``, ``"kokoro"``, ``"cartesia"``. Falls back to
    ``"vibevoice"`` when the key is missing, blank, or unrecognised.
    """
    if config is None:
        config = load_config()
    raw = config.get("ttsProvider")
    if not isinstance(raw, str) or not raw.strip():
        return _DEFAULT_TTS_PROVIDER
    value = raw.strip().lower()
    if value not in _VALID_TTS_PROVIDERS:
        logger.warning(
            "Unknown ttsProvider=%r, falling back to %s",
            raw,
            _DEFAULT_TTS_PROVIDER,
        )
        return _DEFAULT_TTS_PROVIDER
    return value


def get_stt_model(config: dict[str, Any] | None = None) -> str:
    """Return the configured STT model identifier.

    One of ``"whisper-small.en"``, ``"whisper-tiny.en"``, ``"faster-whisper"``.
    Falls back to ``"whisper-small.en"`` when missing or unrecognised.
    """
    if config is None:
        config = load_config()
    raw = config.get("sttModel")
    if not isinstance(raw, str) or not raw.strip():
        return _DEFAULT_STT_MODEL
    value = raw.strip()
    if value not in _VALID_STT_MODELS:
        logger.warning(
            "Unknown sttModel=%r, falling back to %s",
            raw,
            _DEFAULT_STT_MODEL,
        )
        return _DEFAULT_STT_MODEL
    return value


def get_voice_preset(
    config: dict[str, Any] | None = None,
    provider: str | None = None,
) -> str | None:
    """Return the configured voice preset, or ``None`` if unset.

    When ``provider`` is given and ``voicePreset`` is unset, returns the
    provider's bundled default (so callers can plug it directly into the TTS
    constructor without an extra ``or`` chain). When ``provider`` is ``None``
    and the user hasn't set ``voicePreset``, returns ``None`` — callers
    should then keep their existing per-provider defaults.
    """
    if config is None:
        config = load_config()
    raw = config.get("voicePreset")
    if isinstance(raw, str) and raw.strip():
        return raw.strip()
    if provider is None:
        return None
    return _VOICE_PRESET_DEFAULTS.get(provider)


def get_mic_device(config: dict[str, Any] | None = None) -> str | None:
    """Return the user-selected microphone device name, or ``None`` if unset.

    The returned value is a free-form string (e.g. ``"MacBook Pro Microphone"``)
    that the daemon maps to a PyAudio device index at startup. ``None`` /
    empty string means "use the OS default device".
    """
    if config is None:
        config = load_config()
    raw = config.get("micInputDevice")
    if not isinstance(raw, str):
        return None
    raw = raw.strip()
    return raw or None


def get_wake_word_enabled(config: dict[str, Any] | None = None) -> bool:
    """Return whether wake-word gating is enabled.

    Defaults to ``True`` to preserve legacy behaviour (mic feeds through the
    WakeWordGate). When set to ``False``, ``main.py`` will skip inserting the
    gate entirely so the mic feeds STT directly (always-listening mode).
    """
    if config is None:
        config = load_config()
    raw = config.get("wakeWordEnabled")
    if raw is None:
        return _DEFAULT_WAKE_WORD_ENABLED
    if isinstance(raw, bool):
        return raw
    # Permissive coercion — JSON booleans round-trip cleanly but tolerate the
    # occasional ``"true"`` / ``"false"`` string a hand-edited config might ship.
    if isinstance(raw, str):
        return raw.strip().lower() not in {"false", "0", "no", "off", ""}
    return bool(raw)


# ---------------------------------------------------------------------------
# v0.1.2 accessors — additive API keys
# ---------------------------------------------------------------------------
# These are intentionally additive: existing ``jarvis*`` / ``dex*`` keys keep
# working unchanged. ``main.py`` exports the values to env vars so SDK clients
# that read from the environment (Anthropic, Google AI Studio) pick them up.


def get_google_api_key(config: dict[str, Any] | None = None) -> str:
    """Return the Google AI Studio API key from config (``""`` if unset)."""
    if config is None:
        config = load_config()
    return str(config.get("googleAPIKey") or "").strip()


def get_anthropic_api_key(config: dict[str, Any] | None = None) -> str:
    """Return the direct Anthropic API key from config (``""`` if unset).

    Distinct from ``jarvisAPIKey`` / ``dexAPIKey`` which may hold an
    OpenRouter ``sk-or-`` key or other provider token. This helper only
    looks at the dedicated ``anthropicAPIKey`` slot.
    """
    if config is None:
        config = load_config()
    return str(config.get("anthropicAPIKey") or "").strip()


def get_llm_model(config: dict[str, Any] | None = None) -> str | None:
    """Return the user's explicit LLM model pick, or ``None`` to defer.

    The v0.1.5 Connections panel exposes a four-option dropdown bound to
    ``llmModel`` in config:

    * ``"google/gemini-2.5-flash"``     -- Google Generative AI (OpenAI-compat)
    * ``"anthropic/claude-haiku-4-5"``  -- Anthropic SDK direct
    * ``"openai/gpt-4o-mini"``          -- OpenRouter
    * ``"ollama:qwen3:4b"``             -- local Ollama

    Contract:
    * Returns the validated value when it matches one of ``VALID_LLM_MODELS``.
    * Returns ``None`` when the key is missing, blank, or holds a value
      outside ``VALID_LLM_MODELS``. Callers must fall back to legacy
      key-driven detection in that case (never crash on bad config).
    * Unknown values emit a single WARNING-level log so daemon.log makes
      the misconfiguration obvious without surfacing a UI error.
    """
    if config is None:
        config = load_config()
    raw = config.get("llmModel")
    if not isinstance(raw, str):
        return None
    value = raw.strip()
    if not value:
        return None
    if value not in VALID_LLM_MODELS:
        logger.warning(
            "Unknown llmModel=%r, falling back to key-driven LLM detection",
            raw,
        )
        return None
    return value


def get_cartesia_api_key(config: dict[str, Any] | None = None) -> str:
    """Return the Cartesia TTS API key.

    Checks ``cartesiaAPIKey`` in config first, then falls back to the
    ``CARTESIA_API_KEY`` env var so power users running the daemon from a
    shell can override without editing config. Returns ``""`` if neither
    source has a value.
    """
    import os

    if config is None:
        config = load_config()
    from_config = str(config.get("cartesiaAPIKey") or "").strip()
    if from_config:
        return from_config
    return os.environ.get("CARTESIA_API_KEY", "").strip()
