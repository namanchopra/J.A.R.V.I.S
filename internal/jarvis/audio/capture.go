// Package audio provides microphone capture for the Jarvis voice companion.
// It wraps PortAudio to deliver raw PCM audio at 16 kHz mono -- the sample
// rate required by both Porcupine (wake word) and Whisper (speech-to-text).
package audio

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/gordonklaus/portaudio"
)

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

// ---------------------------------------------------------------------------
// MicCapture
// ---------------------------------------------------------------------------

// MicCapture captures audio from the default system input device and makes
// it available as a stream of int16 or float32 PCM samples. It is safe for
// concurrent use; Start and Stop serialise via a mutex, and ReadChunk /
// ReadChunkFloat may be called from any goroutine.
type MicCapture struct {
	mu      sync.Mutex
	stream  *portaudio.Stream
	running bool

	// chunks receives slices of int16 samples produced by the PortAudio
	// callback. It acts as a ring buffer: if the consumer falls behind, the
	// oldest unread chunk is dropped so the callback never blocks the audio
	// thread.
	chunks chan []int16

	// done is closed when Stop is called. ReadChunk uses it to unblock
	// and return ErrCaptureStopped.
	done chan struct{}
}

// NewMicCapture creates a MicCapture ready to be started. No system
// resources are acquired until Start is called.
func NewMicCapture() *MicCapture {
	return &MicCapture{}
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Start opens the default input device at 16 kHz, mono, int16 and begins
// streaming audio into the internal ring buffer. It returns a descriptive
// error if PortAudio cannot initialise, no input device is available, or
// microphone access is denied by the operating system.
func (mc *MicCapture) Start() error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.running {
		return nil // already running, idempotent
	}

	if err := portaudio.Initialize(); err != nil {
		return fmt.Errorf("audio: failed to initialise PortAudio: %w", classifyError(err))
	}

	mc.chunks = make(chan []int16, ringBufferSize)
	mc.done = make(chan struct{})

	// callback is invoked by the PortAudio audio thread. It copies the
	// incoming samples into a freshly allocated slice and sends it on the
	// chunks channel. If the channel is full (consumer too slow), the oldest
	// chunk is drained to make room so the callback never blocks.
	callback := func(in []int16) {
		buf := make([]int16, len(in))
		copy(buf, in)

		select {
		case mc.chunks <- buf:
		default:
			// Drop the oldest chunk to make room.
			select {
			case <-mc.chunks:
			default:
			}
			// Best-effort re-send; if it still fails we drop the new chunk
			// rather than blocking the audio thread.
			select {
			case mc.chunks <- buf:
			default:
			}
		}
	}

	stream, err := portaudio.OpenDefaultStream(
		1,               // input channels (mono)
		0,               // output channels (none)
		float64(sampleRate),
		framesPerBuffer,
		callback,
	)
	if err != nil {
		portaudio.Terminate()
		return fmt.Errorf("audio: failed to open default input stream: %w", classifyError(err))
	}

	if err := stream.Start(); err != nil {
		stream.Close()
		portaudio.Terminate()
		return fmt.Errorf("audio: failed to start audio stream: %w", classifyError(err))
	}

	mc.stream = stream
	mc.running = true

	slog.Info("mic capture started",
		"sample_rate", sampleRate,
		"frames_per_buffer", framesPerBuffer,
	)
	return nil
}

// Stop closes the PortAudio stream and terminates the PortAudio subsystem.
// It is safe to call multiple times or on a MicCapture that was never started.
func (mc *MicCapture) Stop() error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if !mc.running {
		return nil
	}

	mc.running = false

	// Signal readers that no more data is coming.
	close(mc.done)

	var errs []string

	if mc.stream != nil {
		if err := mc.stream.Stop(); err != nil {
			errs = append(errs, fmt.Sprintf("stream stop: %v", err))
		}
		if err := mc.stream.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("stream close: %v", err))
		}
		mc.stream = nil
	}

	if err := portaudio.Terminate(); err != nil {
		errs = append(errs, fmt.Sprintf("terminate: %v", err))
	}

	if len(errs) > 0 {
		slog.Warn("mic capture stopped with errors", "errors", strings.Join(errs, "; "))
		return fmt.Errorf("audio: stop encountered errors: %s", strings.Join(errs, "; "))
	}

	slog.Info("mic capture stopped")
	return nil
}

// IsRunning reports whether the capture stream is currently active.
func (mc *MicCapture) IsRunning() bool {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return mc.running
}

// Drain discards all buffered audio chunks. Call this after TTS playback
// to clear any audio that was captured while Jarvis was speaking (echo/reverb).
func (mc *MicCapture) Drain() {
	for {
		select {
		case <-mc.chunks:
			// discard
		default:
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// ReadChunk blocks until samples int16 values are available and returns them.
// It returns ErrCaptureStopped if the capture is stopped before enough
// samples have been collected.
func (mc *MicCapture) ReadChunk(samples int) ([]int16, error) {
	if samples <= 0 {
		return nil, fmt.Errorf("audio: samples must be positive, got %d", samples)
	}

	result := make([]int16, 0, samples)

	for len(result) < samples {
		select {
		case chunk, ok := <-mc.chunks:
			if !ok {
				// Channel was closed (should not happen, but defensive).
				return nil, ErrCaptureStopped
			}

			remaining := samples - len(result)
			if len(chunk) <= remaining {
				result = append(result, chunk...)
			} else {
				result = append(result, chunk[:remaining]...)
				// Put the leftover samples back. Because we may lose a few
				// samples here if the channel is full, this is acceptable
				// for audio streaming where perfect sample-level continuity
				// is not critical.
				leftover := chunk[remaining:]
				select {
				case mc.chunks <- leftover:
				default:
					slog.Debug("dropped leftover samples", "count", len(leftover))
				}
			}

		case <-mc.done:
			if len(result) > 0 {
				return result, ErrCaptureStopped
			}
			return nil, ErrCaptureStopped
		}
	}

	return result, nil
}

// ReadChunkFloat blocks until samples float32 values are available and
// returns them in the [-1.0, 1.0] range. This is the format required by
// Whisper for inference. Conversion: float32(sample) / 32768.0.
func (mc *MicCapture) ReadChunkFloat(samples int) ([]float32, error) {
	raw, err := mc.ReadChunk(samples)
	if err != nil {
		return nil, err
	}

	out := make([]float32, len(raw))
	for i, s := range raw {
		out[i] = float32(s) / 32768.0
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// classifyError inspects a PortAudio error message and wraps it with
// user-friendly guidance when the root cause is identifiable (e.g.
// microphone permission denied on macOS).
func classifyError(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()

	// macOS Core Audio returns this when microphone access has not been
	// granted. The phrasing varies slightly across OS versions but always
	// contains a reference to permissions or "not authorized".
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "permission") ||
		strings.Contains(lower, "not authorized") ||
		strings.Contains(lower, "denied") {
		return fmt.Errorf(
			"%w -- grant microphone access in System Settings > Privacy & Security > Microphone",
			err,
		)
	}

	// "no default input device" or similar.
	if strings.Contains(lower, "no default") ||
		strings.Contains(lower, "no input") ||
		strings.Contains(lower, "device") && strings.Contains(lower, "not found") {
		return fmt.Errorf(
			"%w -- no input device found; connect a microphone and try again",
			err,
		)
	}

	return err
}
