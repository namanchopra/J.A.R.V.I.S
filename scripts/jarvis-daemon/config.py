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
from typing import Any

logger = logging.getLogger("jarvis-daemon")

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
