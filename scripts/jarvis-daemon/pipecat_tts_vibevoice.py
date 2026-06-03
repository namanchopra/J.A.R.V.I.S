"""Pipecat TTS processor using Microsoft VibeVoice-Realtime-0.5B (local, free, ~300ms TTFB).

Drop-in replacement for KokoroTTSService / CartesiaTTSService. Runs the 0.5B
parameter VibeVoice model locally on Apple Silicon (MPS) or CUDA.

Model auto-downloads from HuggingFace on first use.

Requires: pip install vibevoice (from git clone + pip install -e .[streamingtts])
"""
from __future__ import annotations

import array
import asyncio
import logging
import math
import os
import re
import threading
from collections.abc import Callable, Coroutine
from typing import Any, Final

import numpy as np

from pipecat.frames.frames import (
    EndFrame,
    Frame,
    LLMFullResponseEndFrame,
    LLMFullResponseStartFrame,
    TextFrame,
    TTSAudioRawFrame,
    TTSSpeakFrame,
    TTSStartedFrame,
    TTSStoppedFrame,
)
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor

logger: Final = logging.getLogger("jarvis-daemon.pipecat_tts_vibevoice")

SAMPLE_RATE: Final[int] = 24_000
_SENTENCE_END = re.compile(r'[.!?;]\s*$')


def _resolve_model_dir() -> str | None:
    """Return the directory where Jarvis bundled models live.

    Priority:
      1. ``JARVIS_BUNDLED_MODELS_DIR`` env var (set by the Go side in a
         production .app bundle to ``<Resources>/models``).
      2. ``~/.jarvis/models`` (dev-mode default after Phase 1 migration).
      3. ``None`` — caller falls back to the HuggingFace cache / network.
    """
    bundled = os.environ.get("JARVIS_BUNDLED_MODELS_DIR")
    if bundled and os.path.isdir(bundled):
        return bundled
    home_jarvis = os.path.expanduser("~/.jarvis/models")
    if os.path.isdir(home_jarvis):
        return home_jarvis
    return None


def _float32_to_pcm_s16le(samples: np.ndarray) -> bytes:
    """Convert float32 numpy audio samples to PCM s16le bytes."""
    clipped = np.clip(samples, -1.0, 1.0)
    pcm_int16 = (clipped * 32767).astype(np.int16)
    return pcm_int16.tobytes()


class VibeVoiceTTSService(FrameProcessor):
    """Pipecat TTS using Microsoft VibeVoice-Realtime-0.5B (local, free, ~300ms TTFB).

    Runs the 0.5B param model locally. Streams audio chunks as they're generated.
    Falls back to Edge TTS on failure.
    """

    def __init__(
        self,
        model_path: str = "microsoft/VibeVoice-Realtime-0.5B",
        voice: str = "en-Carter_man",
        device: str | None = None,
        inference_steps: int = 5,
        cfg_scale: float = 1.5,
        **kwargs,
    ):
        super().__init__(**kwargs)
        self._model_path = model_path
        self._voice = voice
        self._inference_steps = inference_steps
        self._cfg_scale = cfg_scale
        self._text_buffer: str = ""
        self._in_response = False
        self._service = None  # Lazy-loaded StreamingTTSService
        self._service_lock = asyncio.Lock()
        self._consecutive_failures = 0

        # Auto-detect device: MPS for Apple Silicon, CUDA if available, else CPU
        if device is None:
            import torch
            if torch.backends.mps.is_available():
                self._device = "mps"
            elif torch.cuda.is_available():
                self._device = "cuda"
            else:
                self._device = "cpu"
        else:
            self._device = device

        # Audio level callback for orb animation — set externally by main.py.
        self._audio_send_fn: Callable[[float], Coroutine[Any, Any, None]] | None = None
        # Mobile TTS callback — sends raw PCM chunks to mobile clients via WS.
        self._mobile_tts_fn: Callable[[bytes], Coroutine[Any, Any, None]] | None = None
        self._audio_chunk_counter: int = 0
        # Expose the output sample rate so the Router/mobile broadcast wiring
        # in main.py can pick the right ``sampleRate`` for the WS message.
        # VibeVoice outputs at 24000 Hz; without this attribute the wiring
        # falls back to its 16000 default and the phone's WAV header is wrong,
        # making playback sound slowed-down and droning.
        self._sample_rate: int = SAMPLE_RATE

    async def prewarm(self) -> None:
        """Eagerly load the VibeVoice model in the background.

        Call this once at pipeline init so the model is ready before the
        first (interruptible) TTS request. Without prewarm the model is
        lazy-loaded inside _synthesize(); if the user starts speaking
        mid-load the executor task is cancelled and the next request has
        to start the load from scratch.
        """
        try:
            await self._get_service()
        except Exception as e:
            logger.warning("VibeVoice prewarm failed (will retry on first TTS): %s", e)

    async def _get_service(self):
        """Lazy-load the VibeVoice model (downloads from HF on first use)."""
        if self._service is not None:
            return self._service

        async with self._service_lock:
            if self._service is not None:
                return self._service

            # Route the (potentially huge) first-run download through the
            # model_status reporter so the HUD's first-run overlay can show
            # progress. ensure_model is a no-op when the cache is already
            # warm — subsequent restarts pay zero overhead here.
            try:
                import model_status
                await model_status.ensure_model("vibevoice")
            except Exception:
                logger.debug("model_status.ensure_model('vibevoice') failed; "
                             "falling through to from_pretrained", exc_info=True)

            from vibevoice.modular.modeling_vibevoice_streaming_inference import (
                VibeVoiceStreamingForConditionalGenerationInference,
            )
            from vibevoice.processor.vibevoice_streaming_processor import (
                VibeVoiceStreamingProcessor,
            )

            # Prefer a locally-bundled snapshot (production .app or
            # ~/.jarvis/models/vibevoice) so we never hit the network on a
            # fresh DMG install. Falls through to the HF repo id when no
            # local snapshot is present (wails dev / first run).
            #
            # IMPORTANT: only override when the directory actually contains
            # the model weights. Dev-mode users may have a partial layout
            # where ~/.jarvis/models/vibevoice/voices/ exists (voice presets)
            # but the main weights live in the HuggingFace cache. Pointing
            # from_pretrained at a weights-less directory raises
            # "Error no file named model.safetensors found in directory ..."
            # and falls back to Edge TTS.
            resolved_dir = _resolve_model_dir()
            if resolved_dir:
                local_vibevoice = os.path.join(resolved_dir, "vibevoice")
                has_weights = (
                    os.path.isfile(os.path.join(local_vibevoice, "model.safetensors"))
                    or os.path.isfile(os.path.join(local_vibevoice, "pytorch_model.bin"))
                )
                if os.path.isdir(local_vibevoice) and has_weights:
                    logger.info(
                        "Using bundled VibeVoice snapshot at %s",
                        local_vibevoice,
                    )
                    self._model_path = local_vibevoice
                elif os.path.isdir(local_vibevoice):
                    logger.info(
                        "Local %s exists but is missing model weights — "
                        "loading model from HuggingFace cache (voices/ "
                        "still read from local).",
                        local_vibevoice,
                    )

            logger.info("Loading VibeVoice model from %s (device=%s)...", self._model_path, self._device)

            # Import the StreamingTTSService from the demo code pattern
            # We inline the essential loading logic to avoid depending on the demo module
            import torch

            processor = await asyncio.get_event_loop().run_in_executor(
                None,
                lambda: VibeVoiceStreamingProcessor.from_pretrained(self._model_path),
            )

            if self._device == "mps":
                load_dtype = torch.float32
                attn_impl = "sdpa"
                device_map = None
            elif self._device == "cuda":
                load_dtype = torch.bfloat16
                attn_impl = "flash_attention_2"
                device_map = "cuda"
            else:
                load_dtype = torch.float32
                attn_impl = "sdpa"
                device_map = "cpu"

            def _load_model():
                try:
                    model = VibeVoiceStreamingForConditionalGenerationInference.from_pretrained(
                        self._model_path,
                        torch_dtype=load_dtype,
                        device_map=device_map,
                        attn_implementation=attn_impl,
                    )
                except Exception:
                    if attn_impl == "flash_attention_2":
                        logger.warning("Flash attention failed, falling back to SDPA")
                        model = VibeVoiceStreamingForConditionalGenerationInference.from_pretrained(
                            self._model_path,
                            torch_dtype=load_dtype,
                            device_map=device_map or self._device,
                            attn_implementation="sdpa",
                        )
                    else:
                        raise

                if self._device == "mps" and device_map is None:
                    model.to("mps")

                model.eval()
                model.model.noise_scheduler = model.model.noise_scheduler.from_config(
                    model.model.noise_scheduler.config,
                    algorithm_type="sde-dpmsolver++",
                    beta_schedule="squaredcos_cap_v2",
                )
                model.set_ddpm_inference_steps(num_steps=self._inference_steps)
                return model

            model = await asyncio.get_event_loop().run_in_executor(None, _load_model)

            # Load voice preset
            voice_preset = await self._load_voice_preset(self._voice)

            self._service = _VibeVoiceState(
                model=model,
                processor=processor,
                voice_preset=voice_preset,
                device=self._device,
                cfg_scale=self._cfg_scale,
                inference_steps=self._inference_steps,
            )
            logger.info("VibeVoice model loaded (device=%s, voice=%s)", self._device, self._voice)
            return self._service

    async def _load_voice_preset(self, voice_name: str):
        """Load a voice preset .pt file.

        Resolution order:
          1. Bundled location (JARVIS_BUNDLED_MODELS_DIR/vibevoice/voices/...)
          2. Dev-mode location (~/.jarvis/models/vibevoice/voices/...)
          3. Source-tree sidecar (scripts/jarvis-daemon/vibevoice_voices/...)
          4. HuggingFace cache (hf_hub_download)
        """
        import torch

        possible_paths: list[str] = []

        # 1. Bundled / dev-mode local directory chosen by _resolve_model_dir.
        # In a signed .app this is read-only -- the runtime download below
        # cannot write here, which is why path #2 exists.
        resolved_dir = _resolve_model_dir()
        if resolved_dir:
            possible_paths.append(
                os.path.join(resolved_dir, "vibevoice", "voices", f"{voice_name}.pt")
            )

        # 2. ALWAYS check ~/.jarvis/models too, regardless of whether
        # JARVIS_BUNDLED_MODELS_DIR shortened _resolve_model_dir to the
        # bundled path. Voice presets downloaded post-install (by the
        # runtime download below, or manually pre-staged by the user)
        # land here -- and without this fallback they would never be
        # found again on a signed-bundle install.
        home_jarvis_models = os.path.expanduser("~/.jarvis/models")
        home_jarvis_path = os.path.join(
            home_jarvis_models, "vibevoice", "voices", f"{voice_name}.pt"
        )
        if home_jarvis_path not in possible_paths:
            possible_paths.append(home_jarvis_path)

        # 3. Source-tree sidecar (legacy support for users who cloned the
        #    VibeVoice repo and copied voices next to this script).
        possible_paths.append(
            os.path.join(os.path.dirname(__file__), "vibevoice_voices", f"{voice_name}.pt")
        )

        # 4. Runtime download (GitHub raw, NOT HuggingFace).
        #
        # The microsoft/VibeVoice-Realtime-0.5B HF repo contains ONLY model
        # weights (model.safetensors + config). Voice presets ship from the
        # microsoft/VibeVoice GitHub repo at:
        #   demo/voices/streaming_model/{voice_name}.pt
        # v0.2.7 and earlier tried HF Hub for the .pt and failed silently
        # (the file simply doesn't exist there), leaving the user stuck.
        #
        # Target ~/.jarvis/models/ unconditionally so the download succeeds
        # even on signed-bundle installs where resolved_dir is read-only.
        github_voices_dir = os.path.join(home_jarvis_models, "vibevoice", "voices")
        github_target = os.path.join(github_voices_dir, f"{voice_name}.pt")
        if not os.path.exists(github_target):
            try:
                import urllib.request
                os.makedirs(github_voices_dir, exist_ok=True)
                url = (
                    "https://raw.githubusercontent.com/microsoft/VibeVoice/"
                    f"main/demo/voices/streaming_model/{voice_name}.pt"
                )
                logger.info("Downloading voice preset %s from %s", voice_name, url)
                tmp_target = github_target + ".tmp"
                await asyncio.get_event_loop().run_in_executor(
                    None,
                    lambda: urllib.request.urlretrieve(url, tmp_target),
                )
                os.replace(tmp_target, github_target)
                logger.info("Voice preset saved to %s", github_target)
            except Exception as e:
                logger.warning(
                    "voice preset download failed (%s); will fall through to other paths", e
                )

        device = self._device if self._device != "cpu" else "cpu"
        for path in possible_paths:
            if path and os.path.exists(path):
                logger.info("Loading voice preset from %s", path)
                return await asyncio.get_event_loop().run_in_executor(
                    None,
                    lambda p=path: torch.load(p, map_location=device, weights_only=False),
                )

        raise FileNotFoundError(
            f"Voice preset '{voice_name}' not found. "
            f"Searched: {possible_paths}. "
            f"Run: bash demo/download_experimental_voices.sh in the VibeVoice repo."
        )

    def _maybe_send_mobile_tts(self, pcm_chunk: bytes) -> None:
        """Forward raw PCM audio to mobile clients via the WS bridge."""
        if self._mobile_tts_fn is None:
            return
        try:
            asyncio.create_task(self._mobile_tts_fn(pcm_chunk))
        except Exception:
            pass

    def _maybe_send_audio_level(self, pcm_chunk: bytes) -> None:
        """Compute RMS of a PCM s16le chunk and fire the audio level callback."""
        if self._audio_send_fn is None:
            return

        self._audio_chunk_counter += 1
        if self._audio_chunk_counter % 3 != 0:
            return

        try:
            samples = array.array("h", pcm_chunk)
            if len(samples) == 0:
                return
            mean_sq = sum(s * s for s in samples) / len(samples)
            rms = math.sqrt(mean_sq) / 32768.0
            level = min(1.0, rms * 8.0)
        except Exception:
            return

        logger.info("audio_level: %.3f (chunk #%d)", level, self._audio_chunk_counter)
        asyncio.create_task(self._audio_send_fn(level))

    async def process_frame(self, frame: Frame, direction: FrameDirection):
        await super().process_frame(frame, direction)

        if isinstance(frame, TTSSpeakFrame):
            text = frame.text if hasattr(frame, "text") else str(frame)
            if text.strip():
                await self._synthesize(text.strip())
            return

        if isinstance(frame, LLMFullResponseStartFrame):
            self._in_response = True
            self._text_buffer = ""
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, TextFrame):
            text = frame.text if hasattr(frame, "text") else ""
            self._text_buffer += text
            if _SENTENCE_END.search(self._text_buffer):
                chunk = self._text_buffer.strip()
                self._text_buffer = ""
                if chunk:
                    await self._synthesize(chunk)
            # Forward the TextFrame so the downstream assistant_aggregator
            # can append the response text to the LLM context. Without this,
            # only tool_calls appear in the assistant history and Jarvis
            # has no memory of what it actually said.
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, LLMFullResponseEndFrame):
            self._in_response = False
            if self._text_buffer.strip():
                await self._synthesize(self._text_buffer.strip())
                self._text_buffer = ""
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, EndFrame):
            await self.push_frame(frame, direction)
            return

        await self.push_frame(frame, direction)

    async def _synthesize(self, text: str):
        """Synthesize text to audio via VibeVoice.

        No silent fallback. If VibeVoice fails, the error is logged and
        propagated as missing audio; the user sees the failure rather than
        silently switching to a cloud TTS with a different voice.
        """
        logger.info("TTS synthesizing: %s", text[:60])
        self._audio_chunk_counter = 0
        await self.push_frame(TTSStartedFrame())

        try:
            await self._synthesize_vibevoice(text)
            self._consecutive_failures = 0
        except Exception:
            self._consecutive_failures += 1
            logger.exception(
                "VibeVoice TTS failed (attempt %d) for: %s",
                self._consecutive_failures, text[:60],
            )
        finally:
            await self.push_frame(TTSStoppedFrame())
            if self._audio_send_fn is not None:
                asyncio.create_task(self._audio_send_fn(0.0))

    async def _synthesize_vibevoice(self, text: str):
        """Generate audio using VibeVoice with streaming."""
        import copy
        import torch

        state = await self._get_service()
        chunk_size = SAMPLE_RATE * 2 // 50  # 20ms of 16-bit mono PCM

        # Prepare inputs
        text_clean = text.replace("\u2019", "'")
        inputs = state.processor.process_input_with_cached_prompt(
            text=text_clean,
            cached_prompt=state.voice_preset,
            padding=True,
            return_tensors="pt",
            return_attention_mask=True,
        )
        device = state.device if state.device != "cpu" else "cpu"
        for k, v in inputs.items():
            if torch.is_tensor(v):
                inputs[k] = v.to(device)

        # Use the streaming approach: run generation in a thread,
        # pull audio chunks from the AudioStreamer
        from vibevoice.modular.streamer import AudioStreamer

        audio_streamer = AudioStreamer(batch_size=1, stop_signal=None, timeout=None)
        errors: list = []
        stop_event = threading.Event()

        def _generate():
            try:
                state.model.generate(
                    **inputs,
                    max_new_tokens=None,
                    cfg_scale=state.cfg_scale,
                    tokenizer=state.processor.tokenizer,
                    generation_config={"do_sample": False},
                    audio_streamer=audio_streamer,
                    stop_check_fn=stop_event.is_set,
                    verbose=False,
                    refresh_negative=True,
                    all_prefilled_outputs=copy.deepcopy(state.voice_preset),
                )
            except Exception as exc:
                errors.append(exc)
                audio_streamer.end()

        thread = threading.Thread(target=_generate, daemon=True)
        thread.start()

        try:
            stream = audio_streamer.get_stream(0)
            sentinel = object()

            while True:
                # Pull chunks from the generation thread
                audio_chunk = await asyncio.get_event_loop().run_in_executor(
                    None, lambda: next(stream, sentinel)
                )
                if audio_chunk is sentinel:
                    break

                # Convert to numpy float32 if tensor
                if torch.is_tensor(audio_chunk):
                    audio_chunk = audio_chunk.detach().cpu().to(torch.float32).numpy()
                else:
                    audio_chunk = np.asarray(audio_chunk, dtype=np.float32)

                if audio_chunk.ndim > 1:
                    audio_chunk = audio_chunk.reshape(-1)

                # Normalize
                peak = np.max(np.abs(audio_chunk)) if audio_chunk.size else 0.0
                if peak > 1.0:
                    audio_chunk = audio_chunk / peak

                # Convert to PCM s16le
                pcm_data = _float32_to_pcm_s16le(audio_chunk)

                # Push in 20ms chunks
                for i in range(0, len(pcm_data), chunk_size):
                    chunk = pcm_data[i:i + chunk_size]
                    await self.push_frame(TTSAudioRawFrame(
                        audio=chunk,
                        sample_rate=SAMPLE_RATE,
                        num_channels=1,
                    ))
                    self._maybe_send_audio_level(chunk)
                    self._maybe_send_mobile_tts(chunk)
        finally:
            stop_event.set()
            audio_streamer.end()
            thread.join(timeout=5.0)
            if errors:
                raise errors[0]

class _VibeVoiceState:
    """Holds loaded model, processor, and voice preset."""
    __slots__ = ("model", "processor", "voice_preset", "device", "cfg_scale", "inference_steps")

    def __init__(self, model, processor, voice_preset, device, cfg_scale, inference_steps):
        self.model = model
        self.processor = processor
        self.voice_preset = voice_preset
        self.device = device
        self.cfg_scale = cfg_scale
        self.inference_steps = inference_steps
