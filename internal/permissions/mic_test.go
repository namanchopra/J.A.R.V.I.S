package permissions

import (
	"runtime"
	"testing"
)

// validStatuses is the closed set of strings MicStatus is allowed to return.
var validStatuses = map[string]bool{
	"granted":        true,
	"denied":         true,
	"not_determined": true,
	"restricted":     true,
}

// TestMicStatusReturnsValidString asserts MicStatus always returns one of the
// four documented values regardless of platform. On non-darwin the stub
// returns "not_determined"; on darwin we accept any of the four because the
// answer depends on the dev machine's TCC database.
func TestMicStatusReturnsValidString(t *testing.T) {
	got := MicStatus()
	if !validStatuses[got] {
		t.Fatalf("MicStatus() = %q; want one of granted|denied|not_determined|restricted", got)
	}
}

// TestMicStatusStubOnNonDarwin pins the stub behaviour so we don't accidentally
// change it later. Skipped on darwin where MicStatus reads real TCC state and
// on windows where MicStatus reads the CapabilityAccessManager registry.
func TestMicStatusStubOnNonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin uses the real AVFoundation implementation")
	}
	if runtime.GOOS == "windows" {
		t.Skip("windows uses the real CapabilityAccessManager registry implementation")
	}
	if got := MicStatus(); got != "not_determined" {
		t.Fatalf("non-darwin MicStatus() = %q; want \"not_determined\"", got)
	}
}

// TestRequestMicDoesNotPanic ensures RequestMic is callable. On darwin this
// may surface the system prompt to the test runner — that's intentional and
// acceptable for a manual `go test` invocation. We do not assert any status
// change because the prompt is interactive.
func TestRequestMicDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RequestMic panicked: %v", r)
		}
	}()
	RequestMic()
}
