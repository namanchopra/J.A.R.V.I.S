// Package arch provides a startup guard that prevents Jarvis from running
// on non-arm64 architectures (e.g. when a Universal binary is launched under
// Rosetta 2). The Python daemon relies on Apple MPS, which is unavailable
// under x86_64 emulation, so we fail fast with a clear message rather than
// crash deep inside an MPS call later.
package arch

import (
	"fmt"
	"runtime"
)

// currentGOARCH is a package-level variable rather than a direct reference to
// runtime.GOARCH so that tests can substitute a value without rebuilding under
// a different GOARCH.
var currentGOARCH = runtime.GOARCH

// ErrUnsupportedArch is returned by Check when the host architecture is not
// arm64. The message contains the substring "Jarvis requires Apple Silicon"
// and the detected architecture, as required by the spec.
type ErrUnsupportedArch struct {
	GOARCH string
}

// Error implements the error interface.
func (e *ErrUnsupportedArch) Error() string {
	return fmt.Sprintf("Jarvis requires Apple Silicon (M1 or later). Detected architecture: %s", e.GOARCH)
}

// Check returns nil when the host architecture is arm64 and an
// *ErrUnsupportedArch otherwise. Callers (typically main) should log the
// error and exit non-zero.
func Check() error {
	if currentGOARCH == "arm64" {
		return nil
	}
	return &ErrUnsupportedArch{GOARCH: currentGOARCH}
}
