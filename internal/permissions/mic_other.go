//go:build !darwin && !windows

// Package permissions exposes microphone permission helpers. On platforms
// without a real implementation (Linux today; future POSIX targets) these
// stubs let the rest of the codebase compile and test. darwin uses the
// AVFoundation TCC bridge in mic_darwin.go; windows reads the
// CapabilityAccessManager consent store in mic_windows.go.
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
