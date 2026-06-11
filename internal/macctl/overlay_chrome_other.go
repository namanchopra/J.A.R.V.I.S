//go:build !darwin && !windows

// overlay_chrome_other.go — no-op fallback for non-Darwin, non-Windows
// builds (typically Linux). The overlay-chrome toggle is implemented per
// platform; on platforms without a backend the SetMainWindowFrameless call
// silently does nothing so callers don't need build-tag guards at every
// site. Windows has its own backend in overlay_chrome_windows.go; Darwin
// uses the Cocoa bridge in overlay_chrome_darwin.go.

package macctl

// SetMainWindowFrameless is a no-op on platforms without a frameless
// toggle backend.
func SetMainWindowFrameless(frameless bool) {}
