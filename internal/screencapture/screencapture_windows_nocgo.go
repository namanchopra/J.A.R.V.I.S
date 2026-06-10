//go:build windows && !cgo

// screencapture_windows_nocgo.go — fallback constructor used when the
// Windows build is compiled without CGO. The real WASAPI loopback bridge
// (screencapture_windows.go + screencapture_windows.c) requires a C
// toolchain, which is present on the production CI runner (windows-2022,
// MSVC/MinGW) but not when a macOS contributor sanity-checks the tree
// with `GOOS=windows go build` (Go disables CGO by default when cross-
// compiling). Without this file the cross-platform screencapture.go
// references an undefined newCapturer symbol.
//
// Behaviour: the no-CGO build silently returns a stub Capturer whose
// Start() reports ErrUnsupportedPlatform. Meeting mode is not expected
// to work in this configuration — the production Windows binary is
// always built with CGO enabled by .github/workflows/release-windows.yml
// (TASK-018, TASK-040).

package screencapture

type windowsNoCgoCapturer struct{}

func newCapturer() Capturer { return &windowsNoCgoCapturer{} }

func (w *windowsNoCgoCapturer) Start(onAudio AudioCallback) error {
	_ = onAudio
	return ErrUnsupportedPlatform
}

func (w *windowsNoCgoCapturer) Stop() error { return nil }
