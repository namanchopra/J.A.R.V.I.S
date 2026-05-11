"""Pytest config for the jarvis-daemon test suite.

Adds the daemon source directory to ``sys.path`` so tests can import
``model_status`` (and friends) without a package install. Mirrors the
runtime layout where the daemon is launched with the source dir on the
path via ``python scripts/jarvis-daemon/main.py``.
"""

from __future__ import annotations

import sys
from pathlib import Path

_DAEMON_DIR = Path(__file__).resolve().parent.parent
if str(_DAEMON_DIR) not in sys.path:
    sys.path.insert(0, str(_DAEMON_DIR))
