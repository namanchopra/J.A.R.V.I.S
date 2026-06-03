//go:build !darwin

// screencapture_other.go — no-op fallback for non-Darwin builds. Mirrors
// the convention used by internal/macctl/overlay_chrome_other.go.
// Meeting mode is Mac-only by design; on Linux/Windows the Capturer
// silently does nothing so callers don't need build-tag guards at every
// call site.

package screencapture

type noopCapturer struct{}

func newCapturer() Capturer { return &noopCapturer{} }

func (n *noopCapturer) Start(onAudio AudioCallback) error {
	_ = onAudio
	return ErrUnsupportedPlatform
}

func (n *noopCapturer) Stop() error { return nil }
