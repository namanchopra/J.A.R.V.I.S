//go:build !darwin

// Package permissions exposes microphone permission helpers. On non-darwin
// platforms there is no TCC-equivalent we care about for Phase 2, so these
// stubs let the rest of the codebase compile and test on Linux/Windows while
// the real implementation lives in mic_darwin.go.
package permissions

// MicStatus always returns "not_determined" on non-darwin platforms.
// Frontend callers should treat that as "the app cannot determine mic state
// here; assume the user grants access via the OS-level mechanism if any".
func MicStatus() string {
	return "not_determined"
}

// RequestMic is a no-op on non-darwin platforms.
func RequestMic() {
	// Intentionally empty: no system-level prompt to trigger off-darwin.
}
