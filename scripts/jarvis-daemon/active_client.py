"""Active interlocutor tracking for per-client voice persona routing.

The Jarvis daemon serves two clients concurrently:

* The Mac (local mic + speaker) -- the original "Jarvis" persona, British male
  voice synthesised by VibeVoice.
* The phone (mobile WS bridge) -- the "Friday" persona, British female voice
  synthesised by macOS ``say -v Serena``.

Only one of them is the active interlocutor at any given moment.  This module
holds a tiny piece of process-wide state that the TTS router, the LLM persona
overlay, the greeting fork, and the transport gates consult to decide where
audio should be played, which voice should speak, and which persona should
frame the reply.

Decay rule
~~~~~~~~~~
A turn that originated from the phone keeps "mobile" active for
``MOBILE_GRACE_SECONDS`` after the last mobile audio chunk arrives.  Once that
window expires (no further mobile activity), ``get_active`` reports ``"mac"``
again so the next Mac mic turn uses VibeVoice instead of Friday's voice.

Threading model
~~~~~~~~~~~~~~~
All mutators / readers run on the asyncio event loop.  The state is plain
module-level globals; no locks needed.  Functions accept an explicit ``now``
so unit tests can drive the clock without monkey-patching ``time.monotonic``.
"""

from __future__ import annotations

import time
from typing import Final, Literal

# ---------------------------------------------------------------------------
# Public types
# ---------------------------------------------------------------------------

ActiveClient = Literal["mac", "mobile"]

# After this many seconds of no mobile activity, the active client decays
# back to "mac" so the next local-mic turn uses the Jarvis (VibeVoice) voice.
MOBILE_GRACE_SECONDS: Final[float] = 60.0

# ---------------------------------------------------------------------------
# Module-level state (singleton)
# ---------------------------------------------------------------------------

# Most recent client to take a turn.  Defaults to "mac" so cold-start (Mac
# mic + speaker) keeps the existing Jarvis behaviour.
_active: ActiveClient = "mac"

# Wall-clock timestamp (``time.monotonic`` style) of the last mobile audio
# chunk or keepalive signal.  ``None`` when the phone has never been seen
# (cold start) or after a decay-to-mac.
_last_mobile_activity_at: float | None = None


# ---------------------------------------------------------------------------
# Mutators
# ---------------------------------------------------------------------------


def set_mobile_active(now: float | None = None) -> None:
    """Mark the phone as the currently active interlocutor.

    Called whenever the Go bridge forwards a ``mobile_active`` control
    frame (mobile audio_start / audio_chunk) to the daemon.  Refreshes
    ``_last_mobile_activity_at`` so the grace window restarts.
    """
    global _active, _last_mobile_activity_at
    _active = "mobile"
    _last_mobile_activity_at = now if now is not None else time.monotonic()


def set_mac_active(now: float | None = None) -> None:
    """Mark the Mac as the currently active interlocutor.

    Called when the local mic's VAD fires (UserStartedSpeakingFrame on
    Mac).  Also explicitly clears the mobile timestamp so any in-flight
    grace window is cancelled -- the Mac speaker should respond
    immediately, even if a phone turn happened ~10 seconds ago.
    """
    global _active, _last_mobile_activity_at
    _active = "mac"
    _last_mobile_activity_at = None
    # ``now`` accepted for symmetry with set_mobile_active; not stored.
    del now


# ---------------------------------------------------------------------------
# Readers
# ---------------------------------------------------------------------------


def get_active(now: float | None = None) -> ActiveClient:
    """Return the current active client, applying the mobile->mac decay.

    The transition rule is:

      * If ``_active`` is ``"mac"`` -- return ``"mac"``.
      * If ``_active`` is ``"mobile"`` AND the last mobile activity is
        within ``MOBILE_GRACE_SECONDS`` -- return ``"mobile"``.
      * Otherwise -- decay to ``"mac"`` (mutate state) and return
        ``"mac"``.

    ``now`` is the timestamp used for the comparison.  When ``None`` we
    fall back to ``time.monotonic()``.  Tests pass an explicit clock so
    they can drive the decay deterministically.
    """
    global _active, _last_mobile_activity_at

    if _active == "mac":
        return "mac"

    # _active == "mobile"
    if _last_mobile_activity_at is None:
        # Shouldn't happen in practice (set_mobile_active always stores
        # a timestamp) but defensive: treat as decayed.
        _active = "mac"
        return "mac"

    current = now if now is not None else time.monotonic()
    if current - _last_mobile_activity_at <= MOBILE_GRACE_SECONDS:
        return "mobile"

    # Grace expired -- decay back to Mac.
    _active = "mac"
    _last_mobile_activity_at = None
    return "mac"


def get_last_mobile_activity_at() -> float | None:
    """Return the timestamp of the most recent mobile activity, or ``None``."""
    return _last_mobile_activity_at


def reset_for_tests() -> None:
    """Reset module state back to defaults.  Test-only.

    Pytest fixtures call this in setup/teardown so individual test cases
    don't leak state into each other.  Avoid calling at runtime -- it
    bypasses the normal mobile->mac decay semantics.
    """
    global _active, _last_mobile_activity_at
    _active = "mac"
    _last_mobile_activity_at = None
