//go:build !darwin

// capture_other.go — MicCapture stub for non-macOS platforms.
//
// On Windows (and Linux/BSD) the Pipecat daemon owns the microphone via
// PyAudio; the Go-side ambient fast path (wake word + interrupt detection)
// is a macOS-only optimisation. This stub keeps the package compiling
// without the gordonklaus/portaudio CGO dependency, which requires
// portaudio-2.0.pc via pkg-config and broke `wails build` on the Windows
// release runners.
//
// Contract with consumers (vad.go, wakeword.go, stt.go, fast_stt.go and
// internal/jarvis/jarvis.go): Start() returns a descriptive error, which the
// ambient-mode bootstrap treats as "fast path unavailable" and surfaces as a
// wrapped error without affecting the daemon-driven voice loop. ReadChunk /
// ReadChunkFloat return ErrCaptureStopped, which every read loop in this
// package already treats as a clean shutdown signal.

package audio

import "errors"

// MicCapture is a non-functional placeholder on this platform. See the
// package documentation for the platform split rationale.
type MicCapture struct{}

// NewMicCapture creates a MicCapture ready to be started. On this platform
// Start always fails; construction is kept cheap and side-effect-free so
// callers can build the object graph unconditionally.
func NewMicCapture() *MicCapture {
	return &MicCapture{}
}

// Start always returns an error on this platform: voice capture is handled
// by the Pipecat daemon (PyAudio), not the Go process.
func (mc *MicCapture) Start() error {
	return errors.New(
		"audio: Go-side mic capture (ambient fast path) is not supported on this platform; " +
			"voice capture is handled by the Jarvis daemon",
	)
}

// Stop is a no-op on this platform. Safe to call any number of times.
func (mc *MicCapture) Stop() error { return nil }

// IsRunning always reports false on this platform.
func (mc *MicCapture) IsRunning() bool { return false }

// Drain is a no-op on this platform.
func (mc *MicCapture) Drain() {}

// ReadChunk returns ErrCaptureStopped on this platform — the same signal the
// package's read loops already interpret as a clean end-of-stream.
func (mc *MicCapture) ReadChunk(samples int) ([]int16, error) {
	return nil, ErrCaptureStopped
}

// ReadChunkFloat returns ErrCaptureStopped on this platform.
func (mc *MicCapture) ReadChunkFloat(samples int) ([]float32, error) {
	return nil, ErrCaptureStopped
}
