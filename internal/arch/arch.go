// Package arch provides a startup guard that prevents Jarvis from running
// on architectures the daemon cannot support.
//
// On macOS the Python daemon relies on Apple MPS, which is unavailable under
// x86_64 emulation, so only darwin/arm64 is accepted there. On Windows both
// amd64 (x64) and arm64 are supported because the daemon falls back to CPU
// or CUDA, neither of which requires Apple Silicon. Callers should invoke
// Check() early in main and bail out with a clear message rather than crash
// deeper in the stack later.
package arch

import (
	"fmt"
	"runtime"
	"strings"
)

// currentGOARCH and currentGOOS are package-level variables rather than direct
// references to runtime.GOARCH / runtime.GOOS so that tests can substitute
// values without rebuilding under a different target.
var (
	currentGOARCH = runtime.GOARCH
	currentGOOS   = runtime.GOOS
)

// darwinValidArchitectures lists the GOARCH values Jarvis accepts on macOS.
// Apple Silicon (arm64) is the only supported target — Rosetta-emulated
// x86_64 will fail deep inside an MPS call.
var darwinValidArchitectures = []string{"arm64"}

// windowsValidArchitectures lists the GOARCH values Jarvis accepts on Windows.
// Both Intel/AMD x64 (amd64) and ARM64 (Snapdragon) are supported; the daemon
// uses CPU or CUDA on Windows, so Apple Silicon is not required.
var windowsValidArchitectures = []string{"amd64", "arm64"}

// ErrUnsupportedArch is returned by Check when the host platform/architecture
// combination is not supported. It carries both the detected GOOS and GOARCH
// so callers can produce a useful diagnostic.
type ErrUnsupportedArch struct {
	GOOS   string
	GOARCH string
}

// Error implements the error interface. The message is tailored to the
// detected GOOS so users get an actionable hint about what is expected:
//   - darwin: "Jarvis requires Apple Silicon (M1 or later). Detected architecture: <arch>"
//   - windows: "Jarvis on Windows requires x64 or arm64. Detected architecture: <arch>"
//   - other:   "Jarvis does not support <goos>/<goarch>"
func (e *ErrUnsupportedArch) Error() string {
	switch e.GOOS {
	case "darwin":
		return fmt.Sprintf("Jarvis requires Apple Silicon (M1 or later). Detected architecture: %s", e.GOARCH)
	case "windows":
		return fmt.Sprintf("Jarvis on Windows requires x64 or arm64. Detected architecture: %s", e.GOARCH)
	default:
		return fmt.Sprintf("Jarvis does not support %s/%s", e.GOOS, e.GOARCH)
	}
}

// Check returns nil when the host platform/architecture combination is
// supported and an *ErrUnsupportedArch otherwise. Callers (typically main)
// should log the error and exit non-zero.
//
// Supported combinations:
//   - darwin/arm64
//   - windows/amd64
//   - windows/arm64
func Check() error {
	switch currentGOOS {
	case "darwin":
		if containsArch(darwinValidArchitectures, currentGOARCH) {
			return nil
		}
	case "windows":
		if containsArch(windowsValidArchitectures, currentGOARCH) {
			return nil
		}
	}
	return &ErrUnsupportedArch{GOOS: currentGOOS, GOARCH: currentGOARCH}
}

// containsArch reports whether the given GOARCH appears in the list. The
// comparison is case-insensitive defensively — runtime.GOARCH is always lower
// case but tests may inject odd values.
func containsArch(list []string, goarch string) bool {
	for _, a := range list {
		if strings.EqualFold(a, goarch) {
			return true
		}
	}
	return false
}
