"""Tests for the platform-aware STT backend selection in ``pipecat_stt``.

Covers the TASK-037 (v0.4.0 Windows port) acceptance criteria:

  1. On Windows / non-Apple-Silicon: ``_load_backend`` skips the
     mlx-whisper attempt entirely and lands on faster-whisper.
  2. On darwin/arm64: mlx-whisper is still attempted first (so STT
     latency on M2 is unchanged from the pre-port behaviour).
  3. ``_try_load_faster_whisper`` prefers CUDA when available and
     falls back to CPU when CUDA initialisation fails -- this is the
     "no CUDA available, CPU fallback works" path in AC #3.

The tests deliberately do **not** import ``pipecat`` or
``faster_whisper`` -- both are heavyweight and not always installed in
the local pytest environment. Instead we monkeypatch the module-level
helpers and stub the import sites inside ``_try_load_faster_whisper`` /
``_try_load_mlx_whisper`` so the logic can be exercised without the
real ML runtimes.

These tests intentionally mirror the sync-pytest style used elsewhere
in the daemon test suite (no ``pytest-asyncio`` dep; we only test
synchronous helpers and the synchronous ``_load_backend`` path).
"""

from __future__ import annotations

import sys
import types
from typing import Any

import pytest

import pipecat_stt


# ---------------------------------------------------------------------------
# _is_apple_silicon
# ---------------------------------------------------------------------------


def test_is_apple_silicon_true_on_darwin_arm64(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """darwin + arm64 -> True (MLX runtime is available)."""
    monkeypatch.setattr(pipecat_stt.sys, "platform", "darwin")
    monkeypatch.setattr(pipecat_stt.platform, "machine", lambda: "arm64")
    assert pipecat_stt._is_apple_silicon() is True


def test_is_apple_silicon_true_on_darwin_aarch64(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Some POSIX uname implementations report ``aarch64`` -- still Apple Silicon."""
    monkeypatch.setattr(pipecat_stt.sys, "platform", "darwin")
    monkeypatch.setattr(pipecat_stt.platform, "machine", lambda: "aarch64")
    assert pipecat_stt._is_apple_silicon() is True


def test_is_apple_silicon_false_on_darwin_x86_64(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Intel Mac -> False (MLX has no x86_64 build)."""
    monkeypatch.setattr(pipecat_stt.sys, "platform", "darwin")
    monkeypatch.setattr(pipecat_stt.platform, "machine", lambda: "x86_64")
    assert pipecat_stt._is_apple_silicon() is False


def test_is_apple_silicon_false_on_windows_amd64(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Windows amd64 -> False (must land on faster-whisper)."""
    monkeypatch.setattr(pipecat_stt.sys, "platform", "win32")
    monkeypatch.setattr(pipecat_stt.platform, "machine", lambda: "AMD64")
    assert pipecat_stt._is_apple_silicon() is False


def test_is_apple_silicon_false_on_windows_arm64(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Windows arm64 (Snapdragon) -> False -- MLX is darwin-only even on ARM."""
    monkeypatch.setattr(pipecat_stt.sys, "platform", "win32")
    monkeypatch.setattr(pipecat_stt.platform, "machine", lambda: "ARM64")
    assert pipecat_stt._is_apple_silicon() is False


def test_is_apple_silicon_false_on_linux(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Linux is never Apple Silicon."""
    monkeypatch.setattr(pipecat_stt.sys, "platform", "linux")
    monkeypatch.setattr(pipecat_stt.platform, "machine", lambda: "x86_64")
    assert pipecat_stt._is_apple_silicon() is False


# ---------------------------------------------------------------------------
# _detect_cuda -- must never raise on any platform
# ---------------------------------------------------------------------------


def test_detect_cuda_returns_false_when_torch_missing(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Missing ``torch`` is treated as "no CUDA" -- never an exception.

    The daemon ships with torch on every platform (vibevoice depends on
    it) but the function still guards the import to keep the helper
    safe in degraded environments. We force the import to fail and
    assert the helper returns False instead of raising.
    """

    real_import = __builtins__["__import__"] if isinstance(__builtins__, dict) else __builtins__.__import__

    def _fake_import(name: str, *args: Any, **kwargs: Any) -> Any:
        if name == "torch":
            raise ImportError("torch is not installed")
        return real_import(name, *args, **kwargs)

    monkeypatch.setattr("builtins.__import__", _fake_import)
    # Make sure any cached torch module is gone for the duration of the test.
    monkeypatch.delitem(sys.modules, "torch", raising=False)

    assert pipecat_stt._detect_cuda() is False


def test_detect_cuda_returns_false_when_torch_cuda_check_raises(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """If ``torch.cuda.is_available()`` itself raises, treat as no CUDA.

    Real-world scenario: an NVIDIA driver on Windows is mid-update and
    NVML returns an error code. We must not crash the STT load -- just
    fall back to CPU.
    """

    fake_torch = types.ModuleType("torch")
    fake_cuda = types.ModuleType("torch.cuda")

    def _raises() -> bool:
        raise RuntimeError("NVML init failed")

    fake_cuda.is_available = _raises  # type: ignore[attr-defined]
    fake_torch.cuda = fake_cuda  # type: ignore[attr-defined]

    monkeypatch.setitem(sys.modules, "torch", fake_torch)
    monkeypatch.setitem(sys.modules, "torch.cuda", fake_cuda)

    assert pipecat_stt._detect_cuda() is False


def test_detect_cuda_returns_true_when_torch_says_yes(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Happy path: torch reports CUDA visible -> we say True."""
    fake_torch = types.ModuleType("torch")
    fake_cuda = types.ModuleType("torch.cuda")
    fake_cuda.is_available = lambda: True  # type: ignore[attr-defined]
    fake_torch.cuda = fake_cuda  # type: ignore[attr-defined]

    monkeypatch.setitem(sys.modules, "torch", fake_torch)
    monkeypatch.setitem(sys.modules, "torch.cuda", fake_cuda)

    assert pipecat_stt._detect_cuda() is True


# ---------------------------------------------------------------------------
# _load_backend -- platform-aware ordering
# ---------------------------------------------------------------------------


def _make_stt() -> pipecat_stt.LocalWhisperSTT:
    """Construct a ``LocalWhisperSTT`` without touching the Pipecat base class.

    ``FrameProcessor.__init__`` (Pipecat 1.x) requires an active event
    loop and registers the processor with the global pipeline registry.
    The platform-detection tests don't exercise that machinery, so we
    bypass ``__init__`` and seed the only attributes ``_load_backend``
    reads. This keeps the test surface narrow and the test runtime tiny.
    """
    obj = pipecat_stt.LocalWhisperSTT.__new__(pipecat_stt.LocalWhisperSTT)
    obj._model_name = "small.en"
    return obj


def test_load_backend_skips_mlx_on_windows(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """On Windows, ``_load_backend`` must not even attempt mlx-whisper.

    Mirrors TASK-037 AC #1: Windows STT must land on faster-whisper
    cleanly. We force the platform helper to report non-Apple-Silicon,
    stub Parakeet as unavailable (typical for users without NeMo
    installed), and verify ``_try_load_mlx_whisper`` is never called
    while ``_try_load_faster_whisper`` is.
    """
    monkeypatch.setattr(pipecat_stt, "_is_apple_silicon", lambda: False)

    mlx_call_count = {"n": 0}
    fw_call_count = {"n": 0}

    def _fake_parakeet(self: Any) -> None:
        return None

    def _fake_mlx(self: Any) -> None:
        mlx_call_count["n"] += 1
        return ("mlx", None)  # If reached, would *succeed* -- so we'd notice.

    def _fake_fw(self: Any) -> tuple[str, Any]:
        fw_call_count["n"] += 1
        return ("faster-whisper", object())

    monkeypatch.setattr(
        pipecat_stt.LocalWhisperSTT, "_try_load_parakeet", _fake_parakeet
    )
    monkeypatch.setattr(
        pipecat_stt.LocalWhisperSTT, "_try_load_mlx_whisper", _fake_mlx
    )
    monkeypatch.setattr(
        pipecat_stt.LocalWhisperSTT, "_try_load_faster_whisper", _fake_fw
    )

    stt = _make_stt()
    result = stt._load_backend()

    assert mlx_call_count["n"] == 0, "mlx-whisper must be skipped on non-Apple-Silicon"
    assert fw_call_count["n"] == 1, "faster-whisper must be the active backend"
    assert result is not None
    assert result[0] == "faster-whisper"


def test_load_backend_tries_mlx_on_apple_silicon(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """On darwin/arm64, mlx-whisper must still be tried first (after Parakeet).

    Guards against regressions on the M2 latency-unchanged criterion.
    """
    monkeypatch.setattr(pipecat_stt, "_is_apple_silicon", lambda: True)

    mlx_call_count = {"n": 0}
    fw_call_count = {"n": 0}

    def _fake_parakeet(self: Any) -> None:
        return None

    def _fake_mlx(self: Any) -> tuple[str, Any]:
        mlx_call_count["n"] += 1
        return ("mlx", None)

    def _fake_fw(self: Any) -> tuple[str, Any]:
        fw_call_count["n"] += 1
        return ("faster-whisper", object())

    monkeypatch.setattr(
        pipecat_stt.LocalWhisperSTT, "_try_load_parakeet", _fake_parakeet
    )
    monkeypatch.setattr(
        pipecat_stt.LocalWhisperSTT, "_try_load_mlx_whisper", _fake_mlx
    )
    monkeypatch.setattr(
        pipecat_stt.LocalWhisperSTT, "_try_load_faster_whisper", _fake_fw
    )

    stt = _make_stt()
    result = stt._load_backend()

    assert mlx_call_count["n"] == 1, "mlx-whisper must be attempted on darwin/arm64"
    assert fw_call_count["n"] == 0, "faster-whisper must not be reached when mlx succeeds"
    assert result is not None
    assert result[0] == "mlx"


# ---------------------------------------------------------------------------
# _try_load_faster_whisper -- CUDA preferred, CPU fallback
# ---------------------------------------------------------------------------


def _install_fake_faster_whisper(
    monkeypatch: pytest.MonkeyPatch,
    *,
    cuda_construct: Any,
    cpu_construct: Any,
) -> dict[str, list[dict[str, Any]]]:
    """Install a fake ``faster_whisper`` module that records constructor args.

    ``cuda_construct`` is invoked when ``WhisperModel(device="cuda", ...)``
    is called; ``cpu_construct`` is invoked for ``device="cpu"``. Either
    can raise to simulate a failed CUDA load. Returns a dict capturing
    the call history so the test can assert on the order of attempts.
    """
    call_log: dict[str, list[dict[str, Any]]] = {"calls": []}

    class _FakeWhisperModel:
        def __init__(self, model_name: str, **kwargs: Any) -> None:
            call_log["calls"].append({"model_name": model_name, **kwargs})
            device = kwargs.get("device")
            if device == "cuda":
                result = cuda_construct() if callable(cuda_construct) else cuda_construct
            else:
                result = cpu_construct() if callable(cpu_construct) else cpu_construct
            if isinstance(result, Exception):
                raise result

    fake_module = types.ModuleType("faster_whisper")
    fake_module.WhisperModel = _FakeWhisperModel  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "faster_whisper", fake_module)
    return call_log


def test_faster_whisper_loads_cpu_when_no_cuda(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """No CUDA visible -> single CPU load attempt with int8 compute type.

    This is the headline Windows-without-GPU path TASK-037 AC #3 calls
    out. We must construct the model with ``device="cpu"`` and
    ``compute_type="int8"`` exactly once.
    """
    monkeypatch.setattr(pipecat_stt, "_detect_cuda", lambda: False)
    log = _install_fake_faster_whisper(
        monkeypatch, cuda_construct=None, cpu_construct=None,
    )

    stt = _make_stt()
    result = stt._try_load_faster_whisper()

    assert result is not None
    assert result[0] == "faster-whisper"
    assert log["calls"] == [
        {"model_name": "small.en", "device": "cpu", "compute_type": "int8"},
    ]


def test_faster_whisper_prefers_cuda_when_available(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """CUDA visible + load OK -> single CUDA load with float16 compute type."""
    monkeypatch.setattr(pipecat_stt, "_detect_cuda", lambda: True)
    log = _install_fake_faster_whisper(
        monkeypatch, cuda_construct=None, cpu_construct=None,
    )

    stt = _make_stt()
    result = stt._try_load_faster_whisper()

    assert result is not None
    assert result[0] == "faster-whisper"
    assert log["calls"] == [
        {"model_name": "small.en", "device": "cuda", "compute_type": "float16"},
    ]


def test_faster_whisper_falls_back_to_cpu_when_cuda_load_fails(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """CUDA visible but load fails (OOM / driver mismatch) -> CPU fallback.

    Direct test for TASK-037 AC #3 "no CUDA available, CPU fallback
    works". A user with a buggy CUDA stack must still get a working STT.
    """
    monkeypatch.setattr(pipecat_stt, "_detect_cuda", lambda: True)
    log = _install_fake_faster_whisper(
        monkeypatch,
        cuda_construct=RuntimeError("CUDA out of memory"),
        cpu_construct=None,
    )

    stt = _make_stt()
    result = stt._try_load_faster_whisper()

    assert result is not None
    assert result[0] == "faster-whisper"
    # Two attempts: CUDA failed, CPU succeeded.
    assert len(log["calls"]) == 2
    assert log["calls"][0]["device"] == "cuda"
    assert log["calls"][1]["device"] == "cpu"
    assert log["calls"][1]["compute_type"] == "int8"


def test_faster_whisper_returns_none_when_both_attempts_fail(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Both CUDA and CPU loads fail -> backend reports unavailable.

    The caller in ``_load_backend`` treats a ``None`` return as "this
    backend isn't usable, try the next one (or fail closed)".
    """
    monkeypatch.setattr(pipecat_stt, "_detect_cuda", lambda: True)
    log = _install_fake_faster_whisper(
        monkeypatch,
        cuda_construct=RuntimeError("CUDA unavailable"),
        cpu_construct=RuntimeError("Disk full -- can't write cache"),
    )

    stt = _make_stt()
    result = stt._try_load_faster_whisper()

    assert result is None
    assert len(log["calls"]) == 2
    assert log["calls"][0]["device"] == "cuda"
    assert log["calls"][1]["device"] == "cpu"


def test_faster_whisper_returns_none_when_import_fails(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """faster_whisper not installed at all -> backend reports unavailable."""

    real_import = __builtins__["__import__"] if isinstance(__builtins__, dict) else __builtins__.__import__

    def _fake_import(name: str, *args: Any, **kwargs: Any) -> Any:
        if name == "faster_whisper":
            raise ImportError("faster_whisper is not installed")
        return real_import(name, *args, **kwargs)

    monkeypatch.setattr("builtins.__import__", _fake_import)
    monkeypatch.delitem(sys.modules, "faster_whisper", raising=False)

    stt = _make_stt()
    result = stt._try_load_faster_whisper()

    assert result is None
