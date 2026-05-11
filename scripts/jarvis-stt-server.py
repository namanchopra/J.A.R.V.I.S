#!/usr/bin/env python3
"""
Jarvis Fast STT Server — faster-whisper on Apple Silicon.

Runs as a long-lived sidecar process. Communicates via stdin/stdout:
  - Receives: a line with the path to a WAV file (16kHz mono)
  - Responds: a line with the transcription text (or empty line if no speech)

The model stays loaded in memory between requests (no cold start per turn).
This cuts STT from 2-5s (whisper-cli cold) to ~0.3-0.5s.

Usage:
  python3 scripts/jarvis-stt-server.py [--model base.en]

Install deps:
  pip3 install faster-whisper
"""

import sys
import os
import argparse
import logging

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [jarvis-stt] %(message)s",
    stream=sys.stderr,
)
log = logging.getLogger("jarvis-stt")


def main():
    parser = argparse.ArgumentParser(description="Jarvis Fast STT Server")
    parser.add_argument(
        "--model",
        default="base.en",
        help="Whisper model size: tiny.en, base.en, small.en (default: base.en)",
    )
    parser.add_argument(
        "--device",
        default="auto",
        help="Device: auto, cpu, cuda (default: auto)",
    )
    parser.add_argument(
        "--compute-type",
        default="int8",
        help="Compute type: int8, float16, float32 (default: int8)",
    )
    args = parser.parse_args()

    # Import here so startup errors are clear
    try:
        from faster_whisper import WhisperModel
    except ImportError:
        log.error("faster-whisper not installed. Run: pip3 install faster-whisper")
        sys.exit(1)

    log.info(f"Loading model '{args.model}' (device={args.device}, compute={args.compute_type})...")

    model = WhisperModel(
        args.model,
        device=args.device,
        compute_type=args.compute_type,
    )

    log.info("Model loaded. Listening for WAV paths on stdin...")

    # Signal ready to the Go parent process
    print("READY", flush=True)

    for line in sys.stdin:
        wav_path = line.strip()
        if not wav_path:
            continue

        if not os.path.isfile(wav_path):
            log.warning(f"File not found: {wav_path}")
            print("", flush=True)
            continue

        try:
            segments, info = model.transcribe(
                wav_path,
                language="en",
                beam_size=1,           # faster, slightly less accurate
                best_of=1,
                temperature=0.0,       # greedy decoding = fastest
                condition_on_previous_text=False,
                vad_filter=True,       # skip silence segments
                vad_parameters=dict(
                    min_silence_duration_ms=300,
                    speech_pad_ms=200,
                ),
            )

            # Collect all segments into text
            text = " ".join(seg.text.strip() for seg in segments if seg.text.strip())
            text = text.strip()

            if text:
                log.info(f"Transcribed ({info.duration:.1f}s audio): {text[:80]}")
            else:
                log.info(f"No speech detected ({info.duration:.1f}s audio)")

            print(text, flush=True)

        except Exception as e:
            log.error(f"Transcription error: {e}")
            print("", flush=True)


if __name__ == "__main__":
    main()
