//go:build darwin

// screencapture_darwin.go — Go side of the macOS ScreenCaptureKit bridge.
// The ObjC implementation lives in screencapture_darwin.m; this file only
// holds:
//
//   * the cgo preamble with framework link flags + forward declarations
//     of the C entrypoints (sck_check_version / sck_start / sck_stop)
//   * the darwinCapturer struct implementing the Capturer interface
//   * goAudioCallback — the //export'd bridge that fires from the SCK
//     serial dispatch queue and forwards PCM bytes to the user callback
//
// Why split: keeping @implementation in a .m file ensures the ObjC class
// only exists in one translation unit. When the implementation was in the
// cgo preamble of this file, the preamble got compiled twice (once for
// the package binary, once for the test binary), causing duplicate-symbol
// link errors for `_OBJC_CLASS_$_JarvisSCKAudioSink`. Splitting into a .m
// avoids that and matches the standard cgo pattern for non-trivial ObjC.
//
// Threading: the SCStream delegate fires on a private serial dispatch
// queue ("com.jarvis.sck.audio"), so goAudioCallback runs on a NON-Go-
// main goroutine. Callers must do their own marshalling.
//
// Pipeline shape (full detail in screencapture_darwin.m):
//
//	SCStream (48 kHz stereo float32, native SCK output)
//	  -> SCStreamOutput delegate
//	      -> AVAudioConverter (cached per stream session)
//	          -> 16 kHz mono int16 PCM (matches CanonicalAudioFormat)
//	              -> goAudioCallback -> user AudioCallback
//
// Permission model: SCStream triggers macOS's single "Screen Recording"
// prompt covering both video and audio. A user who has denied permission
// sees SCStreamErrorUserDeclined (-3801) from getShareableContent, which
// we map to ErrPermissionDenied.

package screencapture

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework Cocoa -framework ScreenCaptureKit -framework CoreMedia -framework AVFoundation -framework CoreAudio -mmacosx-version-min=13.0

#include <stdint.h>

// Entrypoints implemented in screencapture_darwin.m. We forward-declare
// here so cgo knows the signatures; the actual definitions live in the
// .m file which the cgo build system compiles alongside this .go file.
int sck_check_version(void);
int sck_start(uintptr_t handle);
void sck_stop(void);
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime/cgo"
	"sync"
	"unsafe"
)

// darwinCapturer is the real ScreenCaptureKit-backed Capturer. Only one
// active capture is permitted at a time (matches the singleton globals on
// the ObjC side). The mutex serialises Start/Stop transitions; the audio
// callback fires on the SCK dispatch queue without taking this mutex.
type darwinCapturer struct {
	mu     sync.Mutex
	handle cgo.Handle
	active bool
}

func newCapturer() Capturer { return &darwinCapturer{} }

// Start spins up SCStream and begins delivering PCM frames to onAudio.
// Returns ErrUnsupportedOS on macOS < 13, ErrPermissionDenied when Screen
// Recording is denied, or a wrapped error on any other SCK failure.
//
// The onAudio callback fires on a serial dispatch queue created on the C
// side ("com.jarvis.sck.audio") — NOT the Go main goroutine. Callers must
// do their own marshalling if they need main-thread access.
func (d *darwinCapturer) Start(onAudio AudioCallback) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active {
		return errors.New("screencapture: already active")
	}
	if onAudio == nil {
		return errors.New("screencapture: nil callback")
	}
	// Cheap pre-check so we can return ErrUnsupportedOS without burning
	// time on SCShareableContent. The C side also checks (defence-in-depth).
	if C.sck_check_version() == 0 {
		return ErrUnsupportedOS
	}

	h := cgo.NewHandle(onAudio)
	rc := C.sck_start(C.uintptr_t(h))
	switch rc {
	case 0:
		d.handle = h
		d.active = true
		return nil
	case 1:
		h.Delete()
		return ErrUnsupportedOS
	case 2:
		h.Delete()
		return ErrPermissionDenied
	default:
		h.Delete()
		return fmt.Errorf("screencapture: SCK start failed (rc=%d)", int(rc))
	}
}

// Stop halts the SCStream and releases the cgo.Handle. Idempotent: a
// second call on a stopped Capturer returns nil. The onAudio callback may
// still fire briefly after Stop returns because the ObjC delegate dispatch
// is asynchronous — Go-side consumers should treat the callback as "may
// fire one more time after Stop" and tolerate it. For meeting mode this is
// fine: the daemon ignores system_audio frames once the meeting is stopped.
func (d *darwinCapturer) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.active {
		return nil
	}
	C.sck_stop()
	d.handle.Delete()
	d.handle = 0
	d.active = false
	return nil
}

// goAudioCallback is the C-callable bridge into the Go AudioCallback. It
// fires on the SCK serial dispatch queue, NOT the Go main goroutine.
//
// The pcm pointer references memory owned by the ObjC AVAudioPCMBuffer; we
// must copy out before returning because the buffer is recycled on the
// next sample. We also recover() from any panic in the user callback —
// letting a Go panic unwind into ObjC would be a bad day.
//
// Signature note: parameters are named handle/pcm/length and the pcm
// pointer is non-const to match cgo's emitted prototype in _cgo_export.h.
// A mismatch (e.g. `const uint8_t *`) produces "conflicting types" errors.
//
//export goAudioCallback
func goAudioCallback(handle C.uintptr_t, pcm *C.uint8_t, length C.int) {
	n := int(length)
	if n <= 0 || pcm == nil {
		return
	}
	h := cgo.Handle(handle)
	v := h.Value()
	cb, ok := v.(AudioCallback)
	if !ok || cb == nil {
		return
	}
	// Copy out of the ObjC-owned buffer before the SCK delegate recycles it.
	out := make([]byte, n)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(pcm)), n))

	defer func() {
		// Don't let user-callback panics unwind into ObjC — the runtime
		// can't safely recover from that. Swallow + carry on; the next
		// frame will deliver normally.
		_ = recover()
	}()
	cb(out)
}
