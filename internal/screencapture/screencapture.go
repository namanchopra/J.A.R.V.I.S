// Package screencapture wraps macOS ScreenCaptureKit to deliver system
// audio frames into the Jarvis pipeline. On macOS 13+, the darwin build
// targets SCStream via a CGO+ObjC bridge (see screencapture_darwin.go,
// implemented by TASK-004). On other platforms or older macOS, a no-op
// stub returns ErrUnsupportedPlatform / ErrUnsupportedOS at Start time
// so the rest of the app can continue running with meeting mode degraded.
//
// Capturer is the small, stable interface dependent packages program
// against. TASK-005's app_meeting.go consumes it; the daemon never
// imports this package directly — PCM frames are base64-encoded and sent
// over the existing daemon WS.
//
// Lifecycle: construct via New(), call Start(onAudio) with a callback
// that receives PCM frames, call Stop() to halt. onAudio is invoked off
// the Go main goroutine; callers must do their own marshalling.
package screencapture

import "errors"

// ErrUnsupportedPlatform is returned by the non-darwin stub on Start.
// Mirrors the convention used by internal/macctl on non-darwin builds.
var ErrUnsupportedPlatform = errors.New("screencapture: platform not supported (darwin only)")

// ErrUnsupportedOS is returned by the darwin impl when running on macOS
// versions older than 13.0. Declared here so callers can compare with
// errors.Is without importing platform-specific paths.
var ErrUnsupportedOS = errors.New("screencapture: macOS 13.0 or newer required")

// ErrPermissionDenied is returned by the darwin impl when Screen Recording
// permission has not been granted in System Settings. Callers should
// surface a permission CTA (see TASK-015 for the UI wiring) rather than
// treat this as a fatal startup error.
var ErrPermissionDenied = errors.New("screencapture: screen recording permission denied")

// AudioFormat documents the PCM format produced by the darwin Capturer.
// Pinned here so consumers know what to expect without inspecting the
// implementation. TASK-004 converts SCK's native 48 kHz stereo float
// into this canonical shape before invoking the callback.
type AudioFormat struct {
	SampleRateHz  int // 16000
	Channels      int // 1 (mono)
	BitsPerSample int // 16
}

// CanonicalAudioFormat is the format the Capturer guarantees. Callers
// that need a different shape must convert on their side; the Capturer
// itself does not accept a format parameter to keep the surface tiny.
var CanonicalAudioFormat = AudioFormat{SampleRateHz: 16000, Channels: 1, BitsPerSample: 16}

// AudioCallback receives a chunk of PCM bytes in CanonicalAudioFormat.
// Called from a non-main goroutine. Implementations must not block for
// long — buffer or hand off to a channel if expensive work is needed.
type AudioCallback func(pcm []byte)

// Capturer is the small, stable interface this package exports.
// Two methods only. Adding more would force every consumer to update
// its mocks; TASK-004 must work within this surface.
type Capturer interface {
	// Start begins delivering PCM frames to onAudio. Returns
	// ErrUnsupportedPlatform on non-darwin, ErrUnsupportedOS on macOS
	// < 13, ErrPermissionDenied when Screen Recording is denied, or a
	// wrapped error for other SCK failures. Idempotent only across
	// start/stop cycles — calling Start twice without an intervening
	// Stop is implementation-defined and should return an error.
	Start(onAudio AudioCallback) error

	// Stop halts frame delivery. Idempotent: a second call is a no-op
	// returning nil. The onAudio callback is guaranteed not to fire
	// after Stop returns.
	Stop() error
}

// New constructs a platform-appropriate Capturer. The actual concrete
// type is selected by build tag (screencapture_darwin.go vs
// screencapture_other.go). Always returns a non-nil Capturer; callers
// handle platform support by checking the error from Start.
func New() Capturer {
	return newCapturer()
}
