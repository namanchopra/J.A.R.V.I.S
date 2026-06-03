"""Tests for ``active_client.py`` -- active interlocutor tracking + decay.

The module holds process-wide state, so each test resets it via the
``reset_active_client`` fixture before mutating.  All assertions drive
the clock via the explicit ``now`` argument; no monkeypatching of
``time.monotonic`` is required.
"""

from __future__ import annotations

import pytest

import active_client
from active_client import (
    MOBILE_GRACE_SECONDS,
    get_active,
    get_last_mobile_activity_at,
    reset_for_tests,
    set_mac_active,
    set_mobile_active,
)


@pytest.fixture(autouse=True)
def reset_active_client() -> None:
    """Reset module state before and after every test."""
    reset_for_tests()
    yield
    reset_for_tests()


# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------


def test_default_active_is_mac() -> None:
    assert get_active(now=0.0) == "mac"


def test_default_last_mobile_activity_is_none() -> None:
    assert get_last_mobile_activity_at() is None


# ---------------------------------------------------------------------------
# set_mobile_active
# ---------------------------------------------------------------------------


def test_set_mobile_active_flips_to_mobile() -> None:
    set_mobile_active(now=100.0)
    assert get_active(now=100.0) == "mobile"


def test_set_mobile_active_records_timestamp() -> None:
    set_mobile_active(now=42.0)
    assert get_last_mobile_activity_at() == 42.0


def test_set_mobile_active_refreshes_grace_window() -> None:
    set_mobile_active(now=0.0)
    # 30 s into the grace window -- still mobile.
    assert get_active(now=30.0) == "mobile"
    # Refresh -- timer restarts from t=50.
    set_mobile_active(now=50.0)
    # 50 s after the second hit -- still inside the (60 s) grace window
    # because the timer reset on the refresh.
    assert get_active(now=100.0) == "mobile"


# ---------------------------------------------------------------------------
# set_mac_active
# ---------------------------------------------------------------------------


def test_set_mac_active_flips_back_to_mac() -> None:
    set_mobile_active(now=0.0)
    assert get_active(now=10.0) == "mobile"
    set_mac_active(now=15.0)
    assert get_active(now=15.0) == "mac"


def test_set_mac_active_clears_mobile_timestamp() -> None:
    set_mobile_active(now=0.0)
    set_mac_active(now=5.0)
    assert get_last_mobile_activity_at() is None


# ---------------------------------------------------------------------------
# Decay
# ---------------------------------------------------------------------------


def test_mobile_decays_to_mac_after_grace_window() -> None:
    set_mobile_active(now=0.0)
    # Just inside the grace window -- still mobile.
    assert get_active(now=MOBILE_GRACE_SECONDS - 0.001) == "mobile"
    # The boundary itself (<=) is still mobile.
    assert get_active(now=MOBILE_GRACE_SECONDS) == "mobile"


def test_mobile_decays_strictly_after_grace_window() -> None:
    set_mobile_active(now=0.0)
    # Strictly past the window -- should decay.
    assert get_active(now=MOBILE_GRACE_SECONDS + 0.001) == "mac"


def test_decay_clears_mobile_timestamp() -> None:
    set_mobile_active(now=0.0)
    # Trigger the decay path.
    get_active(now=MOBILE_GRACE_SECONDS + 1.0)
    assert get_last_mobile_activity_at() is None


def test_decay_is_permanent_until_set_mobile_active() -> None:
    set_mobile_active(now=0.0)
    # Decay.
    assert get_active(now=MOBILE_GRACE_SECONDS + 5.0) == "mac"
    # Subsequent calls keep returning mac even if ``now`` rolls back -- the
    # decay is a one-way state transition; only set_mobile_active can revive
    # the mobile state.
    assert get_active(now=MOBILE_GRACE_SECONDS + 6.0) == "mac"
    assert get_active(now=0.5) == "mac"


def test_set_mobile_active_after_decay_revives_mobile() -> None:
    set_mobile_active(now=0.0)
    # Decay to mac.
    get_active(now=MOBILE_GRACE_SECONDS + 1.0)
    assert get_active(now=MOBILE_GRACE_SECONDS + 2.0) == "mac"
    # Phone sends a new chunk -- back to mobile.
    set_mobile_active(now=MOBILE_GRACE_SECONDS + 2.0)
    assert get_active(now=MOBILE_GRACE_SECONDS + 5.0) == "mobile"


# ---------------------------------------------------------------------------
# Defensive paths
# ---------------------------------------------------------------------------


def test_get_active_defaults_now_to_wallclock(monkeypatch: pytest.MonkeyPatch) -> None:
    """When ``now`` is omitted, ``time.monotonic`` drives the comparison."""
    fake_clock = {"t": 0.0}

    def _fake_monotonic() -> float:
        return fake_clock["t"]

    monkeypatch.setattr(active_client.time, "monotonic", _fake_monotonic)

    fake_clock["t"] = 10.0
    set_mobile_active()
    assert get_last_mobile_activity_at() == 10.0
    fake_clock["t"] = 15.0
    assert get_active() == "mobile"
    fake_clock["t"] = 10.0 + MOBILE_GRACE_SECONDS + 0.1
    assert get_active() == "mac"


def test_reset_for_tests_clears_state() -> None:
    set_mobile_active(now=0.0)
    reset_for_tests()
    assert get_active(now=0.0) == "mac"
    assert get_last_mobile_activity_at() is None
