// Package audio provides microphone capture for the Jarvis voice companion.
// It wraps PortAudio to deliver raw PCM audio at 16 kHz mono -- the sample
// rate required by both Porcupine (wake word) and Whisper (speech-to-text).
//
// The PortAudio-backed implementation is macOS-only (capture_darwin.go).
// On Windows and other platforms the Pipecat daemon owns the microphone via
// PyAudio, so MicCapture is a stub (capture_other.go) whose Start() returns
// a descriptive error — callers treat that as "ambient fast path
// unavailable" and the daemon-driven voice loop is unaffected. This split
// also keeps the gordonklaus/portaudio CGO dependency (which needs
// portaudio-2.0.pc via pkg-config) out of Windows builds entirely.
package audio

import "errors"

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	// sampleRate is the capture sample rate in Hz. Both Porcupine and Whisper
	// require 16 kHz mono input.
	sampleRate = 16000

	// framesPerBuffer is the number of samples per PortAudio callback
	// invocation. 512 frames at 16 kHz gives ~32 ms of audio per callback,
	// which is a good balance between latency and overhead.
	framesPerBuffer = 512

	// ringBufferSize is the capacity of the internal sample channel. At 512
	// frames per callback, 64 slots hold ~2 seconds of audio, providing
	// sufficient headroom for brief consumer stalls without blocking the
	// PortAudio callback thread.
	ringBufferSize = 64
)

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ErrCaptureStopped is returned by ReadChunk and ReadChunkFloat when the
// capture has been stopped and no more audio will be produced.
var ErrCaptureStopped = errors.New("audio: capture is stopped")
