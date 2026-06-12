// wakeword.go — shared surface of the wake-word detector.
//
// The Porcupine-backed detection loop is macOS-only (wakeword_darwin.go):
// the Picovoice Go binding is CGO-based and ships Windows native libraries
// for amd64 only, which breaks both the windows/arm64 release build and any
// CGO_ENABLED=0 build. On non-darwin platforms Start returns
// ErrPorcupineUnavailable (wakeword_other.go) — wake-word detection there is
// handled by the Pipecat daemon's openWakeWord pipeline, not the Go fast
// path.

package audio

import (
	"errors"
	"sync"
)

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ErrPorcupineUnavailable indicates that the Porcupine engine could not be
// initialised. Common causes include missing native library files for the
// current platform, an invalid or expired access key, or a corrupt/missing
// keyword model file.
var ErrPorcupineUnavailable = errors.New("audio: porcupine engine unavailable")

// ---------------------------------------------------------------------------
// WakeWordDetector
// ---------------------------------------------------------------------------

// WakeWordDetector listens to a MicCapture audio stream and invokes a
// callback whenever the configured wake word is detected. Detection is
// powered by the Picovoice Porcupine on-device wake word engine.
//
// By default the built-in "Jarvis" keyword is used. When modelPath is set
// to a custom .ppn file, that keyword model is used instead.
//
// The detector does not own the MicCapture — the caller is responsible for
// starting and stopping the mic independently.
type WakeWordDetector struct {
	accessKey   string
	modelPath   string
	sensitivity float32
	mic         *MicCapture
	mu          sync.Mutex
}

// NewWakeWordDetector creates a WakeWordDetector configured with the given
// Picovoice access key, optional custom keyword model path, sensitivity,
// and a shared MicCapture instance.
//
// If sensitivity is <= 0 it defaults to 0.5. Sensitivity values closer to
// 1.0 increase the detection rate at the cost of more false positives;
// values closer to 0.0 reduce false positives but may miss wake words
// spoken quietly or in noisy environments.
func NewWakeWordDetector(accessKey string, modelPath string, sensitivity float32, mic *MicCapture) *WakeWordDetector {
	if sensitivity <= 0 {
		sensitivity = 0.5
	}
	return &WakeWordDetector{
		accessKey:   accessKey,
		modelPath:   modelPath,
		sensitivity: sensitivity,
		mic:         mic,
	}
}

// Stop is a no-op provided for API symmetry with other subsystems. The
// detection loop is cancelled via the context passed to Start.
func (w *WakeWordDetector) Stop() {
	// Cancellation is handled by the ctx passed to Start.
}
