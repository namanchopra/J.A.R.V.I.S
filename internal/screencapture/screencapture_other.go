//go:build !darwin && !windows

// screencapture_other.go — no-op fallback for non-Darwin, non-Windows
// builds (i.e. Linux/BSD). Mirrors the convention used by
// internal/notify/notify_other.go and internal/permissions/mic_other.go.
// Meeting mode is currently Mac-only; Windows gets its own stub
// (screencapture_windows.go, TASK-012) en route to the Phase 3 WASAPI
// loopback bridge. On Linux/BSD the Capturer silently does nothing so
// callers don't need build-tag guards at every call site.

package screencapture

type noopCapturer struct{}

func newCapturer() Capturer { return &noopCapturer{} }

func (n *noopCapturer) Start(onAudio AudioCallback) error {
	_ = onAudio
	return ErrUnsupportedPlatform
}

func (n *noopCapturer) Stop() error { return nil }
