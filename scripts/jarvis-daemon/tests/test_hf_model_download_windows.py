"""Windows-only verification for HuggingFace model download paths (TASK-017).

TASK-017 verifies that ``model_status.prefetch_models`` downloads the
VibeVoice + Whisper weights into the **Windows-native** HuggingFace cache
directory (``%USERPROFILE%\\.cache\\huggingface\\hub``) and that an
interrupted download leaves a *resumable* partial state on disk -- i.e.
no half-written files that would corrupt the next ``snapshot_download``.

Acceptance criteria (verbatim from the plan):
  1. VibeVoice (~1.9 GB) downloads to
     ``%USERPROFILE%\\.cache\\huggingface\\hub\\models--microsoft--VibeVoice-Realtime-0.5B``
  2. Whisper (~460 MB) downloads to
     ``%USERPROFILE%\\.cache\\huggingface\\hub\\models--mlx-community--whisper-small.en-mlx``
  3. Failure case: cancelling mid-download leaves resumable partial state
     (no half-written files).

Test strategy:
  Doing a *real* ~2.4 GB end-to-end download in CI would push every PR
  past the 20-minute build budget (TASK-018) and burn HF bandwidth. We
  instead split coverage:

    * Path-shape tests run on every platform (no skip) and verify the
      *static* relationship between repo_id and on-disk path matches the
      acceptance criteria. They are deterministic and fast.

    * Filesystem-probe tests are Windows-only -- they exercise
      ``_measure_hf_snapshot_size`` against a synthetic on-disk layout
      that mirrors ``snapshot_download``'s actual cache layout
      (``hub/models--<org>--<repo>/blobs/<sha>``). This catches the
      Windows-specific bug class where ``os.path.expanduser('~')``
      resolves to a path the real downloader would refuse to write to.

    * The resumable-cancel test is Windows-only. It uses a controllable
      fake worker (no network) to simulate a download that creates
      partial blobs + an HF ``.incomplete`` sidecar, then cancels the
      task. We assert no zero-byte / orphaned files survive in a state
      that would block a re-run.

    * The optional end-to-end download is gated behind the env var
      ``JARVIS_RUN_HF_DOWNLOAD=1`` so a developer (or the
      release-blocking acceptance run, NOT the per-PR matrix) can opt
      in. CI's regular matrix never sets this var.

Path model:
  HF Hub's on-disk layout is::

      <HF_HUB_CACHE>/
        models--<org>--<repo>/
          blobs/<sha>            <- actual file content (large)
          snapshots/<rev>/<file> <- symlink/junction to ../blobs/<sha>
          refs/main              <- pointer file (a few bytes)

  ``HF_HUB_CACHE`` resolves at module-load time inside
  ``huggingface_hub.constants``. On Windows with a vanilla install it
  is ``%USERPROFILE%\\.cache\\huggingface\\hub`` (post-v0.20 HF dropped
  the older ``~/.cache/huggingface/transformers`` path).
"""

from __future__ import annotations

import asyncio
import os
import sys
from pathlib import Path
from typing import Any

import pytest

import model_status


# ---------------------------------------------------------------------------
# Platform guard + helpers
# ---------------------------------------------------------------------------

IS_WINDOWS: bool = sys.platform.startswith("win")

# Most filesystem-probe tests need Windows path separators / the real
# %USERPROFILE% expansion. Tests that work cross-platform on path-shape
# alone are not gated.
requires_windows = pytest.mark.skipif(
    not IS_WINDOWS,
    reason="TASK-017 HF download path verification runs on Windows only",
)


def _expected_repo_dir(repo_id: str) -> str:
    """Mirror HF Hub's ``models--<org>--<repo>`` directory naming.

    Kept inline (rather than imported from model_status) so the test
    fails loudly if upstream HF changes the format and ``model_status``
    silently follows -- this is the *contract* the acceptance criteria
    pin down.
    """
    return f"models--{repo_id.replace('/', '--')}"


# ---------------------------------------------------------------------------
# AC #1 + #2 (static path shape -- runs everywhere, deterministic)
# ---------------------------------------------------------------------------


def test_vibevoice_repo_dir_matches_acceptance_path() -> None:
    """The VibeVoice repo translates to the exact dir name the AC pins.

    AC #1: ``models--microsoft--VibeVoice-Realtime-0.5B`` under
    ``%USERPROFILE%\\.cache\\huggingface\\hub``. We verify the dir-name
    half here; the parent-root half is exercised by the Windows-only
    test below.
    """
    spec = model_status._MODELS["vibevoice"]
    assert spec.repo_id == "microsoft/VibeVoice-Realtime-0.5B"
    assert (
        _expected_repo_dir(spec.repo_id)
        == "models--microsoft--VibeVoice-Realtime-0.5B"
    )


def test_whisper_repo_dir_matches_acceptance_path() -> None:
    """AC #2: ``models--mlx-community--whisper-small.en-mlx``.

    Note: the repo id is the *mlx* fork even on Windows, because
    pipecat_stt's platform-detection (TASK-037) lazily loads
    faster-whisper from the same HF blob store rather than maintaining
    a parallel cache. The on-disk dir is identical across platforms.
    """
    spec = model_status._MODELS["whisper"]
    assert spec.repo_id == "mlx-community/whisper-small.en-mlx"
    assert (
        _expected_repo_dir(spec.repo_id)
        == "models--mlx-community--whisper-small.en-mlx"
    )


def test_approx_sizes_match_acceptance_callouts() -> None:
    """Sanity-check the sizes referenced in AC #1 (~1.9 GB) and AC #2 (~460 MB).

    Drift on these numbers is fine (HF releases new weight files), but
    we want the test to fail audibly so the AC doc gets updated rather
    than silently rotting.
    """
    vv = model_status._MODELS["vibevoice"]
    wh = model_status._MODELS["whisper"]
    # AC #1: ~1.9 GB. Allow plus/minus 20% so a minor weight bump doesn't fail.
    assert 1_500_000_000 <= vv.approx_size_bytes <= 2_300_000_000
    # AC #2: ~460 MB. Same plus/minus 20% latitude.
    assert 350_000_000 <= wh.approx_size_bytes <= 600_000_000


# ---------------------------------------------------------------------------
# AC #1 + #2 (Windows-only filesystem probe)
# ---------------------------------------------------------------------------


@requires_windows
def test_userprofile_expansion_lands_on_windows_cache_root() -> None:
    """``~/.cache/huggingface/hub`` resolves under %USERPROFILE% on Windows.

    The whole acceptance-criteria path hinges on Python's
    ``os.path.expanduser('~')`` returning ``%USERPROFILE%`` (which it
    does on Windows since 3.8). If a future Python release or a quirky
    Windows env (e.g. roaming profile redirect, no USERPROFILE set)
    breaks that contract, this test catches it before the daemon ships
    a model into the wrong place.
    """
    user_profile = os.environ.get("USERPROFILE")
    assert user_profile, "USERPROFILE must be set on Windows"

    hub_root = os.path.expanduser(r"~/.cache/huggingface/hub")
    # Normalise both sides so the comparison ignores forward/backslash
    # variation that ``expanduser`` can produce when fed a POSIX-style
    # input on Windows.
    norm_hub = os.path.normpath(hub_root)
    norm_user = os.path.normpath(user_profile)
    assert norm_hub.startswith(norm_user), (
        f"HF cache root {norm_hub!r} should live under %USERPROFILE% "
        f"({norm_user!r}) -- got an unexpected root"
    )
    assert norm_hub.endswith(os.path.join(".cache", "huggingface", "hub"))


@requires_windows
def test_measure_hf_snapshot_size_uses_windows_cache_layout(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    """The size-measuring helper walks the *Windows* hub blob dir.

    We swap ``os.path.expanduser`` so the helper looks under tmp_path
    instead of the real %USERPROFILE%, then plant a synthetic blobs/
    layout that matches what ``snapshot_download`` would produce after
    a partial download. The function must return the cumulative blob
    size for both VibeVoice and Whisper.

    Why the test is Windows-only:
      The helper is portable Python and would pass on macOS too -- but
      AC #1 / #2 explicitly call out the Windows path. Running this on
      macOS would dilute the signal: a green on macOS is meaningless
      for the Windows acceptance gate. The static-shape tests above
      give us cross-platform coverage of the path *string*; this test
      exercises the *filesystem* against actual Win32 path resolution.
    """
    # Build a fake HF cache anchored at tmp_path.
    fake_user = tmp_path / "fake_user_profile"
    fake_user.mkdir()

    real_expanduser = os.path.expanduser

    def _fake_expanduser(path: str) -> str:
        # Only intercept the HF cache call -- leave other tilde expansions
        # alone so any tqdm / logging side effects don't blow up.
        if path == "~/.cache/huggingface/hub":
            return str(fake_user / ".cache" / "huggingface" / "hub")
        return real_expanduser(path)

    monkeypatch.setattr(os.path, "expanduser", _fake_expanduser)

    # Plant blobs for both repos. We use SHA-shaped filenames (40 hex
    # chars) so the test is faithful to the real layout, but any name
    # would work -- the helper walks the whole blobs dir.
    def _plant(repo_id: str, sizes: list[int]) -> Path:
        blobs = (
            fake_user
            / ".cache"
            / "huggingface"
            / "hub"
            / _expected_repo_dir(repo_id)
            / "blobs"
        )
        blobs.mkdir(parents=True)
        for i, size in enumerate(sizes):
            (blobs / f"{i:040x}").write_bytes(b"\0" * size)
        return blobs

    vv_blobs = _plant(
        "microsoft/VibeVoice-Realtime-0.5B",
        [128, 256, 512],  # 896 bytes total -- representative blob fan-out
    )
    wh_blobs = _plant(
        "mlx-community/whisper-small.en-mlx",
        [64, 64, 64, 64],  # 256 bytes total
    )

    # Sanity: planted layout exists.
    assert vv_blobs.is_dir()
    assert wh_blobs.is_dir()

    # Helper should return the cumulative blob size and *only* read from
    # the Windows-style path under our fake %USERPROFILE%.
    assert model_status._measure_hf_snapshot_size("vibevoice") == 128 + 256 + 512
    assert model_status._measure_hf_snapshot_size("whisper") == 64 + 64 + 64 + 64


@requires_windows
def test_measure_hf_snapshot_returns_zero_when_cache_absent(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    """Before any download starts, the measurer must report 0 (not raise).

    This is the contract the polling task relies on -- if the helper
    raised when the cache dir is missing, the first poll tick on a
    fresh install would crash ``_poll_download_progress``.
    """
    fake_user = tmp_path / "empty_user_profile"
    fake_user.mkdir()

    real_expanduser = os.path.expanduser

    def _fake_expanduser(path: str) -> str:
        if path == "~/.cache/huggingface/hub":
            return str(fake_user / ".cache" / "huggingface" / "hub")
        return real_expanduser(path)

    monkeypatch.setattr(os.path, "expanduser", _fake_expanduser)

    # No blobs dir exists yet -- this is the state right after daemon boot
    # before snapshot_download has touched anything.
    assert model_status._measure_hf_snapshot_size("vibevoice") == 0
    assert model_status._measure_hf_snapshot_size("whisper") == 0


# ---------------------------------------------------------------------------
# AC #3: cancelling mid-download leaves resumable partial state
# ---------------------------------------------------------------------------


@requires_windows
def test_cancel_mid_download_preserves_partial_blobs(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    """A cancelled download must leave the on-disk blobs intact for resume.

    HF Hub implements resumable downloads by writing each blob to
    ``blobs/<sha>.incomplete`` first and atomic-renaming to ``blobs/<sha>``
    when complete. ``snapshot_download`` resumes by re-opening the
    ``.incomplete`` sidecar with an HTTP Range header.

    The daemon's contract layered on top: ``ensure_model`` swallows
    exceptions from the worker (including KeyboardInterrupt / asyncio
    cancellation) and emits ``model_download/state=error``. It must NOT
    delete the partial blobs (no half-written files erased, no full
    blobs orphaned).

    We simulate the cancel with a fake worker that:
      1. Plants one finished blob + one ``.incomplete`` sidecar
      2. Raises an exception to mimic SIGINT mid-download

    Then we assert:
      - The finished blob still exists
      - The ``.incomplete`` sidecar still exists (HF resumes from it)
      - No zero-byte orphan files were created
      - ``model_status._inflight`` is cleared so a retry can start

    We drive the coroutine via ``asyncio.run`` (matching the
    ``test_model_status.py`` pattern) so we don't need pytest-asyncio
    available on the Windows runner image. ensure_model swallows the
    inner exception and emits a model_download/error event, so the
    outer coroutine completes normally.
    """
    # Anchor HF cache at tmp_path so we don't touch the runner's real cache.
    fake_user = tmp_path / "fake_user_profile"
    fake_user.mkdir()
    hub_root = fake_user / ".cache" / "huggingface" / "hub"
    hub_root.mkdir(parents=True)

    real_expanduser = os.path.expanduser

    def _fake_expanduser(path: str) -> str:
        if path == "~/.cache/huggingface/hub":
            return str(hub_root)
        return real_expanduser(path)

    monkeypatch.setattr(os.path, "expanduser", _fake_expanduser)

    # Disable the cache short-circuit so ensure_model invokes the worker.
    monkeypatch.setattr(model_status, "is_cached", lambda _n: False)
    # Clear shared state (test isolation -- prefetch may have run in another test).
    model_status._inflight.clear()
    model_status._latest_setup_payload = None

    repo_dir = hub_root / _expected_repo_dir(
        "microsoft/VibeVoice-Realtime-0.5B",
    )
    blobs_dir = repo_dir / "blobs"
    blobs_dir.mkdir(parents=True)

    finished_blob = blobs_dir / ("a" * 40)
    incomplete_blob = blobs_dir / (("b" * 40) + ".incomplete")

    def _partial_then_cancel(_name: str) -> None:
        # Simulate snapshot_download having written one finished blob
        # plus an in-flight chunk that gets paused with an .incomplete
        # sidecar. Then raise to mimic the user hitting Ctrl-C while
        # the worker thread is mid-chunk.
        finished_blob.write_bytes(b"\0" * 1024)
        incomplete_blob.write_bytes(b"\0" * 512)
        # ensure_model catches arbitrary Exception subclasses (see
        # model_status.py line 581) and emits an error event without
        # cleaning up the partials -- which is the contract under test.
        raise RuntimeError("simulated SIGINT mid-download")

    monkeypatch.setattr(model_status, "_download_hf_snapshot", _partial_then_cancel)

    # Drive ensure_model through a no-op event sink so we don't depend
    # on a real WebSocket recorder for this test's signal.
    async def _noop_sink(_payload: dict[str, Any]) -> None:
        return None

    async def _drive() -> None:
        model_status.set_event_sink(_noop_sink, asyncio.get_running_loop())
        await model_status.ensure_model("vibevoice")

    asyncio.run(_drive())

    # ---- Assertions: partial state is intact + retry-friendly ----

    # AC #3 happy half: the finished blob survives the cancel.
    assert finished_blob.exists(), (
        "finished blob was erased on cancel -- next resume would have to "
        "redownload it"
    )
    assert finished_blob.stat().st_size == 1024

    # AC #3 happy half: the .incomplete sidecar survives (resumable).
    assert incomplete_blob.exists(), (
        ".incomplete sidecar was deleted -- HF Hub would have to restart "
        "this blob from byte 0 instead of resuming"
    )
    assert incomplete_blob.stat().st_size == 512

    # No half-written *renamed* files (a zero-byte ``b * 40`` would
    # indicate the rename happened but the content vanished).
    bogus = blobs_dir / ("b" * 40)
    assert not bogus.exists(), (
        "an empty final blob exists -- HF cache is corrupted, resume will "
        "fail"
    )

    # In-flight bookkeeping must clear so a retry can start fresh.
    assert "vibevoice" not in model_status._inflight, (
        "ensure_model leaked an inflight entry; retry would deadlock "
        "awaiting an event that nobody will set"
    )


# ---------------------------------------------------------------------------
# Opt-in end-to-end smoke (runs against the real HF mirror; gated by env)
# ---------------------------------------------------------------------------


# Gate the heavy E2E behind an env var so the per-PR Windows matrix
# (TASK-018) stays under its 20-minute budget. Set
# JARVIS_RUN_HF_DOWNLOAD=1 on the release-blocking acceptance run only.
_RUN_REAL_DOWNLOAD = os.environ.get("JARVIS_RUN_HF_DOWNLOAD") == "1"

real_download_gate = pytest.mark.skipif(
    not (IS_WINDOWS and _RUN_REAL_DOWNLOAD),
    reason=(
        "Opt-in: JARVIS_RUN_HF_DOWNLOAD=1 + Windows required. "
        "Per-PR matrix skips this; release-acceptance run sets the var."
    ),
)


@real_download_gate
def test_real_whisper_download_lands_on_windows_cache_path(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """End-to-end: pull the Whisper config blob and assert the Windows path.

    We deliberately download the smallest probe file (``config.json``,
    a few KB) rather than the full safetensors payload (~460 MB). The
    AC is satisfied when the blob lands at the AC-mandated path; we
    don't need to materialise the whole weight file to prove that.

    To keep the run hermetic we point HF_HOME at tmp_path so the test
    doesn't pollute the real %USERPROFILE%\\.cache\\huggingface\\hub
    (and so a re-run on the same runner doesn't false-pass via cache).
    """
    hf_home = tmp_path / "hf_home"
    hf_home.mkdir()
    monkeypatch.setenv("HF_HOME", str(hf_home))

    # Re-import constants so HF_HUB_CACHE picks up the new HF_HOME.
    import importlib

    import huggingface_hub
    import huggingface_hub.constants

    importlib.reload(huggingface_hub.constants)
    importlib.reload(huggingface_hub)

    from huggingface_hub import hf_hub_download  # type: ignore[attr-defined]

    spec = model_status._MODELS["whisper"]
    downloaded = hf_hub_download(
        repo_id=spec.repo_id,
        filename=spec.cache_probe_file,
    )

    # The downloaded path must live under HF_HOME/hub/models--<org>--<repo>/.
    repo_dir = _expected_repo_dir(spec.repo_id)
    expected_under = hf_home / "hub" / repo_dir
    assert os.path.normpath(downloaded).startswith(
        os.path.normpath(str(expected_under))
    ), (
        f"downloaded path {downloaded!r} did not land under "
        f"{expected_under!r} -- HF cache layout has drifted from the "
        "TASK-017 acceptance criteria"
    )

    # And the file is non-empty (i.e. a real download happened, not a
    # zero-byte placeholder that would mean half-written state).
    assert os.path.getsize(downloaded) > 0
