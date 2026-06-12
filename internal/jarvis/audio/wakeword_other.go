//go:build !darwin

// wakeword_other.go — wake-word stub for non-macOS platforms.
//
// The Porcupine Go binding is CGO-based and ships Windows native libraries
// for amd64 only; keeping it out of non-darwin builds unblocks both the
// windows/arm64 release build and CGO_ENABLED=0 builds. On these platforms
// wake-word detection is handled by the Pipecat daemon (openWakeWord), so
// the Go fast path simply reports unavailable.

package audio

import (
	"context"
	"fmt"
)

// Start reports that the Go-side wake-word fast path is unavailable on this
// platform. Callers (internal/jarvis ambient mode) treat this as a clean
// "fast path disabled" signal; the daemon's own wake-word pipeline is
// unaffected.
func (w *WakeWordDetector) Start(ctx context.Context, onDetected func()) error {
	return fmt.Errorf(
		"%w: Go-side wake-word detection is not supported on this platform; "+
			"the Jarvis daemon's wake-word pipeline is used instead",
		ErrPorcupineUnavailable,
	)
}
