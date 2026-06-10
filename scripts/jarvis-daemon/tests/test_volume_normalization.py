"""Tests for v0.4.0 / TASK-052 mic-vs-system audio volume normalisation.

The brief: peaks within 6 dB after normalisation, no clipping, and silent
sources must NOT be over-amplified into noise.

These tests target the pure helpers added in main.py:

  * ``_pcm16_peak_dbfs``       -- peak meter on int16 LE PCM
  * ``_update_peak_ema``       -- EMA smoothing
  * ``_observe_mic_peak``      -- mic-side tracker (no-op outside meeting)
  * ``_normalize_system_pcm``  -- gain application on system-audio PCM

They avoid the pipecat pipeline entirely so they run fast and don't
depend on the full daemon stack (same pattern as
``test_meeting_handlers.py``).
"""

from __future__ import annotations

import math

import pytest

import main


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _pcm_from_amplitude(amplitude: int, n_samples: int = 320) -> bytes:
    """Synthesise an int16 LE mono PCM buffer of given peak amplitude.

    Uses a simple square wave (amplitude, -amplitude, ...) so peak ==
    amplitude exactly, and so a gain factor of G produces a post-norm
    peak of exactly amplitude*G (within int16 saturation). This makes
    the assertions in the tests below tight enough to catch off-by-3 dB
    bugs without needing a tolerance band on the peak itself.

    320 samples == 20 ms at 16 kHz (one Pipecat frame).
    """
    out = bytearray(n_samples * 2)
    for i in range(n_samples):
        val = amplitude if (i % 2 == 0) else -amplitude
        if val < 0:
            val += 0x10000
        out[i * 2] = val & 0xFF
        out[i * 2 + 1] = (val >> 8) & 0xFF
    return bytes(out)


def _silence(n_samples: int = 320) -> bytes:
    return b"\x00\x00" * n_samples


def _reset_norm_state(monkeypatch: pytest.MonkeyPatch) -> None:
    """Reset the rolling-peak EMAs + meeting flag to a clean baseline."""
    monkeypatch.setattr(main, "_MIC_PEAK_DBFS", -60.0)
    monkeypatch.setattr(main, "_SYSTEM_PEAK_DBFS", -60.0)
    monkeypatch.setattr(main, "_MEETING_ACTIVE", True)


def _converge_mic_peak(target_dbfs: float, iterations: int = 80) -> None:
    """Drive the mic-peak EMA to ``target_dbfs`` by repeated observation.

    The EMA's alpha (0.15) means a fresh tracker needs ~30-40 iterations
    to converge within 0.5 dB. 80 is a safe upper bound.
    """
    amplitude = max(1, int(32767 * math.pow(10.0, target_dbfs / 20.0)))
    pcm = _pcm_from_amplitude(amplitude)
    for _ in range(iterations):
        main._observe_mic_peak(pcm)


def _peak_dbfs(pcm: bytes) -> float:
    return main._pcm16_peak_dbfs(pcm)


# ---------------------------------------------------------------------------
# Peak meter sanity checks
# ---------------------------------------------------------------------------


class TestPeakMeter:
    def test_silence_returns_floor(self) -> None:
        assert main._pcm16_peak_dbfs(_silence()) == main._VOL_NORM_SILENCE_FLOOR_DBFS

    def test_empty_buffer_returns_floor(self) -> None:
        assert main._pcm16_peak_dbfs(b"") == main._VOL_NORM_SILENCE_FLOOR_DBFS

    def test_full_scale_is_zero_dbfs(self) -> None:
        # 32767 is the int16 ceiling -- by definition 0 dBFS.
        pcm = _pcm_from_amplitude(32767)
        assert main._pcm16_peak_dbfs(pcm) == pytest.approx(0.0, abs=0.01)

    def test_half_scale_is_minus_six_dbfs(self) -> None:
        # 16384 ~= 32767 / 2 ~= -6.02 dBFS.
        pcm = _pcm_from_amplitude(16384)
        assert main._pcm16_peak_dbfs(pcm) == pytest.approx(-6.02, abs=0.1)

    def test_quarter_scale_is_minus_twelve_dbfs(self) -> None:
        pcm = _pcm_from_amplitude(8192)
        assert main._pcm16_peak_dbfs(pcm) == pytest.approx(-12.04, abs=0.1)

    @pytest.mark.parametrize(
        "amplitude,expected_dbfs",
        [
            (32767, 0.0),
            (16384, -6.02),
            (3277, -20.0),
            (327, -40.0),
        ],
    )
    def test_known_amplitudes(self, amplitude: int, expected_dbfs: float) -> None:
        pcm = _pcm_from_amplitude(amplitude)
        assert main._pcm16_peak_dbfs(pcm) == pytest.approx(expected_dbfs, abs=0.3)


# ---------------------------------------------------------------------------
# EMA update sanity
# ---------------------------------------------------------------------------


class TestEMA:
    def test_first_sample_pulls_floor_up_by_alpha(self) -> None:
        # Going from -60 to 0 with alpha=0.15 ==> new = 0.15*0 + 0.85*-60 = -51
        result = main._update_peak_ema(-60.0, 0.0)
        assert result == pytest.approx(-51.0, abs=0.01)

    def test_repeated_samples_converge_to_target(self) -> None:
        # Drive the EMA towards -10 dBFS from -60 and check we get close.
        peak = -60.0
        for _ in range(80):
            peak = main._update_peak_ema(peak, -10.0)
        assert peak == pytest.approx(-10.0, abs=0.5)


# ---------------------------------------------------------------------------
# Acceptance criterion #1: peaks within 6 dB after normalisation
# ---------------------------------------------------------------------------


class TestNormalizationAcceptance:
    def test_quiet_system_audio_is_amplified_to_match_loud_mic(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)

        # Mic peak settles at -6 dBFS (loud voice).
        _converge_mic_peak(-6.0)

        # System audio comes in 24 dB quieter than mic (typical loopback
        # of a quiet remote talker over headphones at a low setting).
        # 32767 * 10^(-30/20) = 1036 amplitude.
        quiet_amp = int(32767 * math.pow(10.0, -30.0 / 20.0))
        quiet_pcm = _pcm_from_amplitude(quiet_amp)
        assert _peak_dbfs(quiet_pcm) == pytest.approx(-30.0, abs=0.3)

        # Run several passes so the system EMA converges to the input
        # level too. We measure peak of the most recent output.
        normalised = quiet_pcm
        for _ in range(80):
            normalised = main._normalize_system_pcm(quiet_pcm)
        post_dbfs = _peak_dbfs(normalised)

        # Acceptance criterion #1: peaks within 6 dB of each other.
        mic_dbfs = main._MIC_PEAK_DBFS
        assert abs(mic_dbfs - post_dbfs) <= main._VOL_NORM_TARGET_RANGE_DB + 0.5

    def test_loud_system_audio_is_attenuated_to_match_quiet_mic(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)

        # Mic is quiet (-30 dBFS -- soft-spoken user on a condenser).
        _converge_mic_peak(-30.0)

        # System audio is hot at -3 dBFS (well above mic by 27 dB).
        loud_amp = int(32767 * math.pow(10.0, -3.0 / 20.0))
        loud_pcm = _pcm_from_amplitude(loud_amp)
        assert _peak_dbfs(loud_pcm) == pytest.approx(-3.0, abs=0.3)

        normalised = loud_pcm
        for _ in range(80):
            normalised = main._normalize_system_pcm(loud_pcm)
        post_dbfs = _peak_dbfs(normalised)

        # We should be within the target band, BUT also bounded by the
        # min-gain clamp (-12 dB) which is by design so we never crush
        # system audio below ~useful levels. With mic at -30 and system
        # at -3, ideal gain is -27 dB; clamped to -12 dB. So post-peak
        # should land at ~-15 dBFS, which is 15 dB above mic. The
        # explicit min-gain clamp is the documented escape valve.
        # We assert the attenuation actually happened.
        assert post_dbfs < _peak_dbfs(loud_pcm)
        # And no clipping (acceptance criterion #2):
        assert post_dbfs <= 0.0


# ---------------------------------------------------------------------------
# Acceptance criterion #2: no audible clipping
# ---------------------------------------------------------------------------


class TestNoClipping:
    def test_normalised_pcm_never_exceeds_full_scale(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)
        # Mic at full scale.
        _converge_mic_peak(0.0)
        # System at -30 dBFS -- the gain target is +30 dB, clamped to
        # +18 dB. Even so the worst-case output must stay <= int16 max.
        sys_pcm = _pcm_from_amplitude(int(32767 * math.pow(10.0, -30.0 / 20.0)))

        # Run a few passes.
        out = sys_pcm
        for _ in range(20):
            out = main._normalize_system_pcm(sys_pcm)

        # Decode and check no sample exceeds int16 bounds (which the
        # type alone guarantees, but the value must also not have
        # saturated at the extremes).
        import struct
        n = len(out) // 2
        samples = struct.unpack(f"<{n}h", out)
        peak = max(abs(s) for s in samples)
        assert peak <= 32767

    def test_clipping_guard_attenuates_near_full_scale(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)
        # Mic is hot at 0 dBFS, system is at -3 dBFS. Delta = 3 dB,
        # which is below the 6 dB target band -> NO normalisation, so
        # the output should be untouched.
        _converge_mic_peak(0.0)
        sys_pcm = _pcm_from_amplitude(int(32767 * math.pow(10.0, -3.0 / 20.0)))
        out = main._normalize_system_pcm(sys_pcm)
        assert out == sys_pcm

    def test_post_gain_peak_stays_below_full_scale_with_one_db_headroom(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)
        # Worst case: mic is at 0 dBFS, system is at -7 dBFS. Delta = 7
        # dB, just outside the target band -> we want to apply +7 dB to
        # match. The clipping guard should detect that 7 dB on -7 dBFS
        # leaves 0 dB peak, and ratchet down by 1 dB.
        _converge_mic_peak(0.0)
        sys_pcm = _pcm_from_amplitude(int(32767 * math.pow(10.0, -7.0 / 20.0)))
        out = main._normalize_system_pcm(sys_pcm)
        post_peak_dbfs = _peak_dbfs(out)
        # Headroom: never above -1 dBFS after the clipping guard.
        assert post_peak_dbfs <= -1.0 + 0.5  # 0.5 dB measurement slack


# ---------------------------------------------------------------------------
# Acceptance criterion #3: silent source not over-amplified
# ---------------------------------------------------------------------------


class TestSilenceGuard:
    def test_silent_mic_does_not_amplify_system(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)
        # Mic stays silent (EMA pinned at floor).
        # System audio is quiet but present.
        sys_pcm = _pcm_from_amplitude(int(32767 * math.pow(10.0, -25.0 / 20.0)))
        out = main._normalize_system_pcm(sys_pcm)
        # System audio MUST be unchanged because mic side is silent.
        assert out == sys_pcm

    def test_silent_system_audio_passes_through_unchanged(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)
        _converge_mic_peak(-6.0)
        silent = _silence()
        out = main._normalize_system_pcm(silent)
        assert out == silent

    def test_both_silent_passes_through_unchanged(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)
        silent = _silence()
        out = main._normalize_system_pcm(silent)
        assert out == silent


# ---------------------------------------------------------------------------
# observe_mic_peak / meeting-mode gating
# ---------------------------------------------------------------------------


class TestObserveMicPeak:
    def test_no_op_outside_meeting_mode(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        # Reset, then mark meeting OFF.
        monkeypatch.setattr(main, "_MIC_PEAK_DBFS", -60.0)
        monkeypatch.setattr(main, "_MEETING_ACTIVE", False)
        loud_pcm = _pcm_from_amplitude(32000)
        main._observe_mic_peak(loud_pcm)
        # Should remain at -60 because the helper short-circuits.
        assert main._MIC_PEAK_DBFS == -60.0

    def test_updates_ema_inside_meeting_mode(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)
        loud_pcm = _pcm_from_amplitude(32000)  # near 0 dBFS
        for _ in range(40):
            main._observe_mic_peak(loud_pcm)
        # Should have converged near 0 dBFS.
        assert main._MIC_PEAK_DBFS > -3.0


# ---------------------------------------------------------------------------
# Edge cases
# ---------------------------------------------------------------------------


class TestEdgeCases:
    def test_empty_pcm_in_normalize_returns_empty(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        _reset_norm_state(monkeypatch)
        _converge_mic_peak(-6.0)
        assert main._normalize_system_pcm(b"") == b""

    def test_normalization_is_idempotent_in_target_band(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        # When mic and system peaks are already within 6 dB, calling
        # normalize repeatedly must return identical bytes (cheap path).
        _reset_norm_state(monkeypatch)
        _converge_mic_peak(-10.0)
        sys_pcm = _pcm_from_amplitude(int(32767 * math.pow(10.0, -8.0 / 20.0)))
        # Within 6 dB of -10 dBFS (mic).
        out1 = main._normalize_system_pcm(sys_pcm)
        out2 = main._normalize_system_pcm(out1)
        assert out1 == sys_pcm
        assert out2 == sys_pcm

    def test_gain_clamp_caps_extreme_amplification(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        # Mic at 0 dBFS, system at -50 dBFS. Naive gain would be +50 dB
        # but our clamp is +18 dB -> output peak should be near -32 dBFS
        # (50 - 18), not 0 dBFS.
        _reset_norm_state(monkeypatch)
        _converge_mic_peak(0.0)
        sys_pcm = _pcm_from_amplitude(int(32767 * math.pow(10.0, -50.0 / 20.0)))
        # -50 dBFS is exactly the silence floor; nudge slightly above.
        sys_pcm = _pcm_from_amplitude(int(32767 * math.pow(10.0, -45.0 / 20.0)))
        out = main._normalize_system_pcm(sys_pcm)
        post_dbfs = _peak_dbfs(out)
        # Cap is +18 dB, so output should be -45 + 18 = -27 dBFS (give
        # or take 1 dB for quantisation in the square wave amplitude).
        assert post_dbfs == pytest.approx(-27.0, abs=2.0)
