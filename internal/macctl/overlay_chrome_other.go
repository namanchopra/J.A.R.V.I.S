//go:build !darwin

// overlay_chrome_other.go — no-op fallback for non-Darwin builds. The
// overlay-chrome toggle is Mac-only by design; on Linux/Windows the
// SetMainWindowFrameless call silently does nothing so callers don't
// need build-tag guards at every site.

package macctl

// SetMainWindowFrameless is a no-op on non-Darwin builds.
func SetMainWindowFrameless(frameless bool) {}
