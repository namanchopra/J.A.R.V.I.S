"""Model download orchestration and progress reporting for the Jarvis daemon.

On a fresh DMG install the heavy model weights (VibeVoice TTS ~1.9 GB,
Whisper STT ~460 MB, Kokoro ~360 MB) are not bundled. The first wake-word
triggered lazy load used to silently hang for 5-10 minutes while
``from_pretrained`` / ``snapshot_download`` ran behind the scenes.

This module owns three concerns:

1. **Cache detection.** ``is_cached(name)`` asks ``huggingface_hub`` whether
   a known canonical file from the repo is already on disk. No reinvention
   of cache-path math.

2. **Pre-fetch orchestration.** ``prefetch_models()`` runs as a background
   asyncio task at daemon startup. It builds the pending list, emits a
   ``model_setup`` event, then downloads each missing model serially while
   streaming ``model_download/progress`` events to the HUD.

3. **Progress reporting.** ``ProgressTqdm`` is a tqdm subclass passed via
   ``snapshot_download(tqdm_class=...)``. It hooks ``update()`` to compute
   pct + speed + ETA. Emission is throttled to ~2 events/sec to avoid
   spamming the WS. The Kokoro path uses ``urllib`` (no tqdm), so it has a
   small ``urlretrieve`` reporthook helper that produces the same events.

The module is self-contained: callers wire it in via ``set_event_sink()``
(once, from main.py) and call the public ``ensure_*()`` helpers from the
TTS / STT lazy-load paths so an in-flight pre-fetch is honoured.
"""

from __future__ import annotations

import asyncio
import logging
import os
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any, Final

from tqdm.auto import tqdm

logger: Final = logging.getLogger("jarvis-daemon.model_status")

# ---------------------------------------------------------------------------
# Event sink
# ---------------------------------------------------------------------------

# A coroutine of the same shape main.py's ``_ws_send`` uses: takes a JSON-able
# dict, returns nothing. Stored at module scope because the TTS / STT modules
# call ``emit_*`` directly without holding a reference to the WS object.
EventSink = Callable[[dict[str, Any]], Awaitable[None]]

_event_sink: EventSink | None = None
_loop: asyncio.AbstractEventLoop | None = None


def set_event_sink(send_fn: EventSink, loop: asyncio.AbstractEventLoop) -> None:
    """Register the WS send callback. Called once from voice_session()."""
    global _event_sink, _loop
    _event_sink = send_fn
    _loop = loop


def _emit_sync(payload: dict[str, Any]) -> None:
    """Send a payload from any thread. No-op if no sink is configured.

    Used by ``ProgressTqdm.update()`` which runs inside the executor thread
    that ``snapshot_download`` spawns for the download workers.
    """
    if _event_sink is None or _loop is None:
        return
    try:
        asyncio.run_coroutine_threadsafe(_event_sink(payload), _loop)
    except Exception:
        logger.debug("Failed to schedule WS emit from thread", exc_info=True)


async def _emit(payload: dict[str, Any]) -> None:
    """Send a payload from the event loop thread. No-op if no sink."""
    if _event_sink is None:
        return
    try:
        await _event_sink(payload)
    except Exception:
        logger.debug("WS emit failed", exc_info=True)


# ---------------------------------------------------------------------------
# Model registry
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class ModelSpec:
    """Static metadata for a model the daemon may download."""
    name: str                 # short id used in events ("vibevoice", "whisper", "kokoro")
    repo_id: str              # HF repo id, or "" for non-HF sources (Kokoro)
    # A canonical file that is guaranteed to exist after a successful
    # snapshot_download. Used for the cache-presence probe.
    cache_probe_file: str
    approx_size_bytes: int    # for UI ETA & first-run banner
    # ``allow_patterns`` passed to snapshot_download. Trims the ~5 GB
    # safetensors/onnx forks so we only pull what we actually load.
    allow_patterns: tuple[str, ...] = ()


# Repo-pinned canonical files. Picked because they are always present in the
# repo's ``main`` revision and have a small enough chance of being renamed.
_MODELS: Final[dict[str, ModelSpec]] = {
    "vibevoice": ModelSpec(
        name="vibevoice",
        repo_id="microsoft/VibeVoice-Realtime-0.5B",
        cache_probe_file="config.json",
        approx_size_bytes=1_900_000_000,
        allow_patterns=("*.safetensors", "*.json", "*.txt", "tokenizer*"),
    ),
    "whisper": ModelSpec(
        name="whisper",
        repo_id="mlx-community/whisper-small.en-mlx",
        cache_probe_file="config.json",
        approx_size_bytes=460_000_000,
        allow_patterns=("*.safetensors", "*.json", "*.txt", "tokenizer*", "*.tiktoken"),
    ),
    # Kokoro is hosted on GitHub Releases, not HF. We special-case the
    # download in ``_download_kokoro`` but still report it through the same
    # event surface so the HUD treats it uniformly.
    "kokoro": ModelSpec(
        name="kokoro",
        repo_id="",
        cache_probe_file="",
        approx_size_bytes=360_000_000,
    ),
}


# Local file paths for Kokoro (mirrors pipecat_tts_kokoro.py constants).
_KOKORO_DIR: Final[str] = os.path.expanduser("~/.awm/models/kokoro")
_KOKORO_MODEL_PATH: Final[str] = os.path.join(_KOKORO_DIR, "kokoro-v1.0.onnx")
_KOKORO_VOICES_PATH: Final[str] = os.path.join(_KOKORO_DIR, "voices-v1.0.bin")
_KOKORO_MODEL_URL: Final[str] = (
    "https://github.com/thewh1teagle/kokoro-onnx/releases/download/"
    "model-files-v1.0/kokoro-v1.0.onnx"
)
_KOKORO_VOICES_URL: Final[str] = (
    "https://github.com/thewh1teagle/kokoro-onnx/releases/download/"
    "model-files-v1.0/voices-v1.0.bin"
)


# ---------------------------------------------------------------------------
# Cache presence
# ---------------------------------------------------------------------------


def _bundled_dir_has_model(name: str) -> bool:
    """Return True if a locally-bundled snapshot exists for ``name``.

    Mirrors the resolution logic in ``pipecat_tts_vibevoice._resolve_model_dir``
    and ``pipecat_stt._resolve_model_dir`` so a user with prebaked models
    (production .app or hand-populated ~/.jarvis/models) skips the download.
    """
    bundled = os.environ.get("JARVIS_BUNDLED_MODELS_DIR")
    candidates: list[str] = []
    if bundled and os.path.isdir(bundled):
        candidates.append(bundled)
    home_jarvis = os.path.expanduser("~/.jarvis/models")
    if os.path.isdir(home_jarvis):
        candidates.append(home_jarvis)

    if name == "vibevoice":
        for root in candidates:
            vv = os.path.join(root, "vibevoice")
            if os.path.isfile(os.path.join(vv, "model.safetensors")) or \
               os.path.isfile(os.path.join(vv, "pytorch_model.bin")):
                return True
    elif name == "whisper":
        for root in candidates:
            for sub in ("whisper-small", "whisper-small-mlx", "whisper-small.en-mlx"):
                p = os.path.join(root, sub)
                if os.path.isdir(p) and (
                    os.path.isfile(os.path.join(p, "weights.npz")) or
                    os.path.isfile(os.path.join(p, "model.safetensors"))
                ):
                    return True
    elif name == "kokoro":
        return os.path.isfile(_KOKORO_MODEL_PATH) and os.path.isfile(_KOKORO_VOICES_PATH)
    return False


def is_cached(name: str) -> bool:
    """Return True if the model is already on disk.

    Checks both the bundled directory (production .app / hand-populated dev)
    AND the HuggingFace cache (default first-run location). Kokoro is plain
    file existence on its GitHub-Releases mirror.
    """
    spec = _MODELS.get(name)
    if spec is None:
        return False

    if _bundled_dir_has_model(name):
        return True

    if name == "kokoro":
        # Already covered by _bundled_dir_has_model, but the function
        # contract is "exhaustive cache check" — keep the explicit branch.
        return os.path.isfile(_KOKORO_MODEL_PATH) and os.path.isfile(_KOKORO_VOICES_PATH)

    try:
        from huggingface_hub import try_to_load_from_cache
        from huggingface_hub.constants import HF_HUB_CACHE
    except ImportError:
        logger.debug("huggingface_hub not installed; assuming model not cached")
        return False

    cached = try_to_load_from_cache(
        repo_id=spec.repo_id,
        filename=spec.cache_probe_file,
        cache_dir=HF_HUB_CACHE,
    )
    # try_to_load_from_cache returns:
    #   * the absolute file path if cached
    #   * _CACHED_NO_EXIST sentinel if HF knows the file doesn't exist (revoked)
    #   * None if not cached
    # We treat anything that's a string as "cached".
    return isinstance(cached, str) and os.path.isfile(cached)


# ---------------------------------------------------------------------------
# Progress reporting
# ---------------------------------------------------------------------------


@dataclass
class _DownloadState:
    """Shared progress state for a single in-flight download.

    ``snapshot_download`` instantiates one tqdm per file, but the HUD wants
    aggregate bytes/pct for the whole model. We share a single state object
    across all tqdm instances for that model so totals add up correctly.
    """
    model: str
    total_bytes: int                         # sum of every file's total
    downloaded_bytes: int = 0
    started_at: float = field(default_factory=time.monotonic)
    last_emit: float = 0.0
    # Per-tqdm last value so we can compute deltas without races.
    tqdm_last: dict[int, int] = field(default_factory=dict)


_THROTTLE_SECONDS: Final[float] = 0.5  # ~2 events/sec per the spec


class ProgressTqdm(tqdm):
    """tqdm subclass that streams ``model_download/progress`` events.

    Passed to ``snapshot_download(tqdm_class=ProgressTqdm)``. HF creates one
    instance per file. We attach a shared ``_DownloadState`` via the class-
    level ``_state`` attribute (set by ``_with_progress_state``) so all
    instances aggregate into the same total.

    Why subclass instead of polling disk size? snapshot_download streams
    chunks straight from urllib to ProgressTqdm.update(n) -- we get exact
    byte counts without any disk-walk overhead, and total_bytes is known
    upfront (HF passes total= when constructing the bar).
    """

    _state: _DownloadState | None = None

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        state = type(self)._state
        if state is not None:
            # Fold this file's total into the aggregate. tqdm may pass
            # total=None when the size is unknown (e.g. compressed transfer
            # without Content-Length); skip those.
            if isinstance(self.total, int) and self.total > 0:
                state.total_bytes += self.total

    def update(self, n: int = 1) -> bool | None:
        ret = super().update(n)
        state = type(self)._state
        if state is None:
            return ret

        # tqdm.update(n) is a delta, but the state aggregates absolute bytes.
        # Track per-instance last n to be safe against weird internal calls.
        key = id(self)
        last = state.tqdm_last.get(key, 0)
        state.tqdm_last[key] = self.n
        delta = max(0, self.n - last)
        state.downloaded_bytes += delta

        now = time.monotonic()
        if now - state.last_emit < _THROTTLE_SECONDS:
            return ret
        state.last_emit = now

        _emit_sync(_build_progress_payload(state))
        return ret


def _build_progress_payload(state: _DownloadState) -> dict[str, Any]:
    """Construct a ``model_download/progress`` event from shared state."""
    total = state.total_bytes
    done = min(state.downloaded_bytes, total) if total > 0 else state.downloaded_bytes
    pct = int((done / total) * 100) if total > 0 else 0

    elapsed = max(0.001, time.monotonic() - state.started_at)
    speed = int(done / elapsed) if elapsed > 0 else 0

    payload: dict[str, Any] = {
        "type": "model_download",
        "model": state.model,
        "state": "progress",
        "total_bytes": total,
        "downloaded_bytes": done,
        "pct": min(100, max(0, pct)),
    }
    if speed > 0:
        payload["speed_bytes_per_sec"] = speed
        remaining = max(0, total - done)
        if remaining > 0:
            payload["eta_seconds"] = int(remaining / speed)
    return payload


# ---------------------------------------------------------------------------
# Public download helpers
# ---------------------------------------------------------------------------

# Tracks in-flight downloads so retry / second-callers can wait instead of
# starting a duplicate. asyncio.Event signals "this model is done (success
# or error)" — callers await it before falling through to lazy-load.
_inflight: dict[str, asyncio.Event] = {}
_inflight_lock = asyncio.Lock()


async def _emit_started(name: str) -> None:
    spec = _MODELS[name]
    await _emit({
        "type": "model_download",
        "model": name,
        "state": "started",
        "total_bytes": spec.approx_size_bytes,
    })


async def _emit_done(name: str) -> None:
    await _emit({
        "type": "model_download",
        "model": name,
        "state": "done",
    })


async def _emit_error(name: str, error: str) -> None:
    await _emit({
        "type": "model_download",
        "model": name,
        "state": "error",
        "error": error,
    })


async def _emit_setup(pending: list[str]) -> None:
    payload: dict[str, Any] = {
        "type": "model_setup",
        "state": "downloading" if pending else "ready",
        "models_pending": [
            {"name": n, "approx_size_bytes": _MODELS[n].approx_size_bytes}
            for n in pending
        ],
    }
    # Cache the latest payload so a late-mounting HUD client (the WS
    # connection from the React side races daemon startup by ~1-2s and
    # can miss the first model_setup emission) can re-pull it via
    # `request_model_setup`. Without this cache, a fresh DMG install
    # silently skipped the FirstRunDownloadOverlay because the
    # downloading state arrived before the HUD had subscribed.
    global _latest_setup_payload
    _latest_setup_payload = payload
    await _emit(payload)


# Module-level cache of the latest model_setup event. Populated by
# `_emit_setup` on every state transition; consumed by
# `handle_request_setup_message` when the HUD asks for a fresh copy.
_latest_setup_payload: dict[str, Any] | None = None


async def handle_request_setup_message(data: dict[str, Any]) -> None:
    """Re-emit the cached ``model_setup`` event for a late-mounting HUD.

    Mirrors the ``request_pipeline_status`` pattern from v0.1.5 so the
    FirstRunDownloadOverlay can request the current download state on
    mount instead of relying on having been subscribed at the moment
    the daemon first emitted it. ``data`` is unused — kept in the
    signature so the message dispatcher in ``main.py`` can route every
    inbound message uniformly through ``handler(data)``.
    """
    del data  # explicitly unused — see docstring
    if _latest_setup_payload is None:
        # Prefetch hasn't started yet (daemon barely up) — emit a
        # synthetic "ready" so the HUD doesn't sit with no state.
        # `prefetch_models` will overwrite this within seconds with the
        # real pending list if there's actually a download to do.
        await _emit({
            "type": "model_setup",
            "state": "ready",
            "models_pending": [],
        })
        return
    await _emit(_latest_setup_payload)


def _download_hf_snapshot(name: str) -> None:
    """Synchronous worker that runs ``snapshot_download`` with progress.

    Invoked via ``loop.run_in_executor`` from ``ensure_model`` / ``prefetch``.
    Must not touch the event loop directly. Progress events fire through
    ``_emit_sync`` (which uses ``run_coroutine_threadsafe``).
    """
    from huggingface_hub import snapshot_download

    spec = _MODELS[name]
    state = _DownloadState(model=name, total_bytes=0)

    # The ProgressTqdm class holds state on the type itself. snapshot_download
    # is not concurrent across calls (we serialize downloads), so a class-level
    # state pointer is safe -- but reset it in a try/finally to keep the
    # contract obvious.
    ProgressTqdm._state = state
    try:
        kwargs: dict[str, Any] = {
            "repo_id": spec.repo_id,
            "tqdm_class": ProgressTqdm,
        }
        if spec.allow_patterns:
            kwargs["allow_patterns"] = list(spec.allow_patterns)
        snapshot_download(**kwargs)
    finally:
        ProgressTqdm._state = None


def _download_kokoro() -> None:
    """Synchronous Kokoro download with a urlretrieve reporthook.

    Kokoro lives on GitHub releases (not HF) so we can't reuse ProgressTqdm.
    The shape of the events emitted is identical to the HF path -- the HUD
    has no idea which source backs which model.
    """
    import urllib.request

    os.makedirs(_KOKORO_DIR, exist_ok=True)

    files = [
        (_KOKORO_MODEL_PATH, _KOKORO_MODEL_URL),
        (_KOKORO_VOICES_PATH, _KOKORO_VOICES_URL),
    ]
    # Pre-probe Content-Length for an honest total. If the HEAD fails (CDN
    # quirk), fall back to the static approx size from the spec.
    total = 0
    sizes: list[int] = []
    for _, url in files:
        size = 0
        try:
            req = urllib.request.Request(url, method="HEAD")
            with urllib.request.urlopen(req, timeout=10) as resp:
                size = int(resp.headers.get("Content-Length", "0") or 0)
        except Exception:
            size = 0
        sizes.append(size)
        total += size
    if total == 0:
        total = _MODELS["kokoro"].approx_size_bytes

    state = _DownloadState(model="kokoro", total_bytes=total)
    downloaded_accum = 0

    for (path, url), size in zip(files, sizes, strict=False):
        if os.path.isfile(path):
            # Already on disk from a previous partial run; count its size
            # toward the aggregate so the bar starts at the right offset.
            try:
                downloaded_accum += os.path.getsize(path)
            except OSError:
                pass
            continue

        def _reporthook(block_num: int, block_size: int, total_size: int,
                        _path: str = path) -> None:
            # urlretrieve passes (block_num, block_size, total_size).
            # We re-aggregate using cumulative bytes from this file plus
            # the running ``downloaded_accum`` from previously-finished files.
            file_done = block_num * block_size
            state.downloaded_bytes = downloaded_accum + file_done
            now = time.monotonic()
            if now - state.last_emit < _THROTTLE_SECONDS:
                return
            state.last_emit = now
            _emit_sync(_build_progress_payload(state))

        urllib.request.urlretrieve(url, path, reporthook=_reporthook)
        # Roll the per-file total into the running accumulator before the
        # next file starts so the next file's reporthook offsets correctly.
        try:
            downloaded_accum += os.path.getsize(path)
        except OSError:
            downloaded_accum += size


async def ensure_model(name: str, *, force: bool = False) -> None:
    """Make sure model ``name`` is on disk. Idempotent + de-duplicating.

    Callable from any code path (lazy load, prefetch task, retry handler).
    If the model is already cached, returns immediately. If a download is
    already running for this model, awaits its completion instead of
    starting a duplicate.

    ``force=True`` skips the cache check and forces a re-download (used by
    the HUD's retry button when a previous attempt errored out).
    """
    if name not in _MODELS:
        raise ValueError(f"unknown model: {name}")

    if not force and is_cached(name):
        logger.debug("model %s already cached, skipping download", name)
        return

    # De-dupe concurrent calls for the same model.
    async with _inflight_lock:
        existing = _inflight.get(name)
        if existing is not None:
            done_event = existing
        else:
            done_event = asyncio.Event()
            _inflight[name] = done_event
            owner = True
        if existing is not None:
            owner = False

    if not owner:
        logger.debug("model %s already downloading; awaiting completion", name)
        await done_event.wait()
        return

    try:
        await _emit_started(name)
        logger.info("Downloading model: %s", name)
        loop = asyncio.get_running_loop()
        if name == "kokoro":
            await loop.run_in_executor(None, _download_kokoro)
        else:
            await loop.run_in_executor(None, _download_hf_snapshot, name)
        await _emit_done(name)
        logger.info("Model downloaded: %s", name)
    except Exception as exc:
        msg = f"{type(exc).__name__}: {exc}"
        logger.exception("Model download failed: %s", name)
        await _emit_error(name, msg)
    finally:
        async with _inflight_lock:
            _inflight.pop(name, None)
        done_event.set()


def required_models_for(config: dict[str, Any]) -> list[str]:
    """Return the ordered list of models the daemon will load.

    Mirrors the TTS-selection logic in main.create_pipeline_components so
    the prefetch list matches what actually gets lazy-loaded. STT is always
    Whisper (the Parakeet path is best-effort with its own internal cache).
    """
    tts_provider = str(config.get("ttsProvider", "")).lower()

    models: list[str] = []
    if tts_provider == "kokoro":
        models.append("kokoro")
    elif tts_provider == "cartesia":
        # Cloud TTS, no local weights needed.
        pass
    else:
        # Default / "vibevoice" path. We don't pre-emptively download
        # Kokoro because the auto-fallback only fires when vibevoice import
        # fails -- and in a shipped DMG vibevoice is always installed.
        models.append("vibevoice")

    models.append("whisper")
    return models


async def prefetch_models(config: dict[str, Any]) -> None:
    """Background task: download every required model that isn't cached.

    Emits ``model_setup state=downloading`` with the pending list, then
    downloads serially. Emits ``model_setup state=ready`` when all are done
    (or finished with errors; the HUD treats per-model error events as the
    actionable signal).
    """
    required = required_models_for(config)
    pending = [m for m in required if not is_cached(m)]

    if not pending:
        await _emit_setup([])
        logger.info("Model prefetch: all required models already cached")
        return

    logger.info(
        "Model prefetch: %d pending (%s)",
        len(pending),
        ", ".join(pending),
    )
    await _emit_setup(pending)

    for name in pending:
        await ensure_model(name)

    # Always emit ready at the end -- errored downloads still show as
    # ready=true on the setup event, but the per-model error events tell the
    # HUD which models need a retry button.
    await _emit_setup([])
    logger.info("Model prefetch complete")


async def handle_retry_message(data: dict[str, Any]) -> None:
    """Handle ``{"type": "retry_model_download", "model": "..."}`` from HUD."""
    name = data.get("model")
    if not isinstance(name, str) or name not in _MODELS:
        logger.warning("retry_model_download: unknown model %r", name)
        return
    logger.info("Retrying model download: %s", name)
    await ensure_model(name, force=True)
    # Re-check the full pending set so the HUD's overlay updates correctly
    # if this was the last one outstanding.
    await _emit_setup([m for m in _MODELS if not is_cached(m)])
