"""Text-to-speech engine using edge-tts with streaming sentence support.

Speaks Jarvis's responses via Microsoft Edge neural voices (free, no API key).
Supports sentence-by-sentence streaming: as the LLM generates sentences, each
is queued and spoken in order.  Mutes the mic during speech to prevent echo
feedback, then unmutes after a short delay to let reverb fade.

Audio playback uses ``afplay`` (macOS built-in).

Requires edge-tts:  pip install edge-tts>=6.1
"""

from __future__ import annotations

import asyncio
import logging
import os
import re
import tempfile
from typing import TYPE_CHECKING, Final

if TYPE_CHECKING:
    from mic import MicStream

logger: Final = logging.getLogger("jarvis-daemon.tts")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------
VOICE: Final[str] = "en-GB-RyanNeural"
"""Default voice -- British male for Jarvis-like cadence."""

UNMUTE_DELAY: Final[float] = 1.5
"""Seconds after speech finishes before unmuting the mic (reverb fade)."""


# ---------------------------------------------------------------------------
# Sentence splitting
# ---------------------------------------------------------------------------
_SENTENCE_RE: Final[re.Pattern[str]] = re.compile(r"(?<=[.!?])\s+")


def split_sentences(text: str) -> list[str]:
    """Split *text* on sentence boundaries (``.``, ``!``, ``?``) for streaming TTS.

    Returns a list of stripped, non-empty sentences.

    >>> split_sentences("Hello there. How are you? Fine!")
    ['Hello there.', 'How are you?', 'Fine!']
    >>> split_sentences("   ")
    []
    """
    sentences = _SENTENCE_RE.split(text)
    return [s.strip() for s in sentences if s.strip()]


# ---------------------------------------------------------------------------
# TTSEngine
# ---------------------------------------------------------------------------
class TTSEngine:
    """Text-to-speech using edge-tts with streaming sentence support.

    Speaks text via Microsoft Edge neural voices.  Supports sentence-by-sentence
    streaming: as the LLM generates sentences, each is queued and spoken in
    order.  Mutes the mic during speech to prevent echo.

    Usage::

        tts = TTSEngine(mic_stream=mic)
        await tts.start()

        # Immediate (blocking until done):
        await tts.speak("Good evening, sir.")

        # Streaming (non-blocking, queued):
        for sentence in split_sentences(llm_output):
            await tts.speak_sentence(sentence)

        # Interrupt current speech:
        await tts.interrupt()

        await tts.stop()
    """

    def __init__(
        self,
        voice: str = VOICE,
        mic_stream: MicStream | None = None,
    ) -> None:
        """Initialise the TTS engine.

        Args:
            voice: Microsoft Edge neural voice identifier.
            mic_stream: Optional ``MicStream`` instance to mute during speech.
        """
        self._voice = voice
        self._mic = mic_stream
        self._speaking = False
        self._cancel_event = asyncio.Event()
        self._queue: asyncio.Queue[str] = asyncio.Queue()
        self._task: asyncio.Task[None] | None = None
        self._current_proc: asyncio.subprocess.Process | None = None

    # -- public properties ---------------------------------------------------

    @property
    def speaking(self) -> bool:
        """Whether audio is currently being played."""
        return self._speaking

    # -- lifecycle -----------------------------------------------------------

    async def start(self) -> None:
        """Start the TTS consumer loop (processes queued sentences).

        The consumer runs as a background task, pulling sentences from the
        queue and speaking them one at a time.
        """
        self._validate_edge_tts()
        self._cancel_event.clear()
        self._task = asyncio.create_task(self._consumer_loop())
        logger.info("TTS engine started (voice=%s)", self._voice)

    async def stop(self) -> None:
        """Stop the consumer and cancel any current speech."""
        self._cancel_event.set()

        # Kill any in-flight afplay process.
        await self._kill_current_proc()

        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None

        # Drain leftover queue items.
        self._drain_queue()
        self._speaking = False
        logger.info("TTS engine stopped")

    # -- public speech methods -----------------------------------------------

    async def speak(self, text: str) -> None:
        """Speak *text* immediately (blocks until playback finishes).

        This bypasses the sentence queue -- use for one-shot utterances.
        """
        text = text.strip()
        if not text:
            return
        await self._stream_speak(text)

    async def speak_sentence(self, sentence: str) -> None:
        """Queue a sentence for sequential playback (non-blocking).

        Sentences are played in FIFO order by the consumer loop.
        """
        sentence = sentence.strip()
        if not sentence:
            return
        await self._queue.put(sentence)

    async def interrupt(self) -> None:
        """Stop current speech immediately and drain the queue.

        The consumer loop remains running -- new sentences queued after
        ``interrupt()`` will be played normally.
        """
        # Kill the afplay process if it is still running.
        await self._kill_current_proc()

        # Drain all pending sentences.
        self._drain_queue()

        self._speaking = False
        logger.debug("TTS interrupted")

    # -- internal: consumer loop ---------------------------------------------

    async def _consumer_loop(self) -> None:
        """Process queued sentences using streaming TTS playback."""
        while not self._cancel_event.is_set():
            try:
                sentence = await asyncio.wait_for(
                    self._queue.get(), timeout=0.5,
                )
            except asyncio.TimeoutError:
                continue
            except asyncio.CancelledError:
                break

            await self._stream_speak(sentence)

    # -- internal: streaming TTS (no file I/O) --------------------------------

    async def _stream_speak(self, text: str) -> None:
        """Stream edge-tts audio directly to ffplay — no temp files.

        Edge-tts yields audio chunks as they're synthesized. We pipe them
        straight into ffplay's stdin, so playback starts within ~200ms
        of the first chunk arriving. No waiting for full synthesis.
        """
        if self._mic is not None:
            self._mic.muted = True

        self._speaking = True

        try:
            import edge_tts  # noqa: PLC0415

            if self._cancel_event.is_set():
                return

            # Start ffplay reading from stdin pipe. -nodisp hides the window.
            # -autoexit closes when input ends. -loglevel quiet suppresses output.
            proc = await asyncio.create_subprocess_exec(
                "ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet", "-i", "pipe:0",
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.DEVNULL,
                stderr=asyncio.subprocess.DEVNULL,
            )
            self._current_proc = proc

            # Stream audio chunks from edge-tts directly into ffplay.
            communicate = edge_tts.Communicate(text, self._voice)
            async for chunk in communicate.stream():
                if chunk["type"] == "audio" and proc.stdin is not None:
                    try:
                        proc.stdin.write(chunk["data"])
                        await proc.stdin.drain()
                    except (BrokenPipeError, ConnectionResetError):
                        break
                if self._cancel_event.is_set():
                    break

            # Close stdin to signal end of audio.
            if proc.stdin is not None:
                try:
                    proc.stdin.close()
                    await proc.stdin.wait_closed()
                except Exception:
                    pass

            await proc.wait()
            self._current_proc = None

        except asyncio.CancelledError:
            raise
        except FileNotFoundError:
            # ffplay not installed — fall back to file-based approach.
            logger.warning("ffplay not found, falling back to file-based TTS")
            await self._file_speak(text)
        except Exception:
            logger.exception("Streaming TTS failed: %s", text[:60])
        finally:
            self._speaking = False

            # Unmute mic after delay.
            if self._mic is not None:
                try:
                    await asyncio.sleep(UNMUTE_DELAY)
                except asyncio.CancelledError:
                    self._mic.muted = False
                    raise
                self._mic.muted = False

    async def _file_speak(self, text: str) -> None:
        """Fallback: generate MP3 file then play with afplay."""
        try:
            import edge_tts  # noqa: PLC0415
            import tempfile as _tf

            communicate = edge_tts.Communicate(text, self._voice)
            tmp = _tf.NamedTemporaryFile(suffix=".mp3", delete=False)
            tmp_path = tmp.name
            tmp.close()
            await communicate.save(tmp_path)

            proc = await asyncio.create_subprocess_exec(
                "afplay", tmp_path,
                stdout=asyncio.subprocess.DEVNULL,
                stderr=asyncio.subprocess.DEVNULL,
            )
            self._current_proc = proc
            await proc.wait()
            self._current_proc = None
            os.unlink(tmp_path)
        except Exception:
            logger.exception("File-based TTS failed: %s", text[:60])

    # -- internal: helpers ---------------------------------------------------

    async def _kill_current_proc(self) -> None:
        """Kill the currently running ``afplay`` process, if any."""
        proc = self._current_proc
        if proc is not None and proc.returncode is None:
            try:
                proc.kill()
                await proc.wait()
            except (OSError, ProcessLookupError):
                pass
            self._current_proc = None

    def _drain_queue(self) -> None:
        """Discard all pending sentences in the queue."""
        while not self._queue.empty():
            try:
                self._queue.get_nowait()
            except asyncio.QueueEmpty:
                break

    @staticmethod
    def _validate_edge_tts() -> None:
        """Check that edge-tts is importable.  Raises ``RuntimeError`` if not."""
        try:
            import edge_tts  # noqa: F401, PLC0415
        except ImportError as exc:
            raise RuntimeError(
                "edge-tts is not installed. Install it with: "
                "pip install edge-tts>=6.1"
            ) from exc
