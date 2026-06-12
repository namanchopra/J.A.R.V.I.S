//go:build windows

// screencapture_windows.go — Go side of the Windows WASAPI loopback
// audio-capture bridge. Replaces the Phase 1 stub with the real
// IAudioClient + AUDCLNT_STREAMFLAGS_LOOPBACK implementation. The C
// implementation lives in screencapture_windows.c; this file only holds:
//
//   * the cgo preamble linking against ole32/oleaut32 (no separate
//     Windows SDK import library is required — MMDevAPI / AudioClient
//     are exposed as plain COM CLSIDs and IIDs)
//   * the windowsCapturer struct implementing the Capturer interface
//   * goWindowsAudioCallback — the //export'd bridge that fires from
//     the WASAPI capture worker thread and forwards PCM bytes to the
//     user callback
//
// Why split go/c: keeping the WASAPI/COM code in a .c file avoids cgo
// preamble duplication (the preamble would be compiled twice — once for
// the package binary, once for the test binary — risking duplicate
// symbol issues if any internal helpers escape).
//
// Threading: the WASAPI capture loop runs on a dedicated C-side worker
// thread (CreateThread); goWindowsAudioCallback fires from that thread,
// NOT the Go main goroutine. Callers must do their own marshalling.
//
// Pipeline shape (full detail in screencapture_windows.c):
//
//	IAudioClient (loopback, native mix format e.g. 48 kHz stereo float32)
//	  -> IAudioCaptureClient::GetBuffer (worker loop)
//	      -> downmix stereo→mono + decimate to 16 kHz int16 (TASK-041 basic;
//	         TASK-042 will replace decimation with proper linear interp)
//	          -> goWindowsAudioCallback -> user AudioCallback
//
// Permission model: Windows has no system-audio-capture permission
// equivalent to macOS Screen Recording — WASAPI loopback works for any
// process. The Windows-specific UX note: when no application is producing
// audio, WASAPI loopback emits silent buffers (per Microsoft's docs);
// this is expected behaviour, not a failure mode. See TASK-050 for the
// AUDCLNT_E_DEVICE_INVALIDATED handling path.

package screencapture

/*
#cgo windows CFLAGS: -D_WIN32_WINNT=0x0A00 -DCOBJMACROS -DINITGUID
#cgo windows LDFLAGS: -lole32 -loleaut32 -lwinmm -lksuser -luuid

#include <stdint.h>

// Entrypoints implemented in screencapture_windows.c. Forward-declared
// here so cgo knows the signatures; the actual definitions live in the
// .c file which the cgo build system compiles alongside this .go file.
//
// wasapi_start returns:
//   0 = success
//   1 = no default playback (render) endpoint available
//   2 = device activate / initialize failed (COM/WASAPI generic)
//   3 = capture client / worker thread failed to start
//   4 = already active (defensive — Go side also guards)
int wasapi_start(uintptr_t handle);
void wasapi_stop(void);
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime/cgo"
	"sync"
	"unsafe"
)

// ErrNoPlaybackDevice lives in screencapture_windows_common.go so it is
// visible under both cgo and !cgo builds — the windows-tagged test file
// references it in either mode.

// windowsCapturer is the real WASAPI-backed Capturer. Only one active
// capture is permitted at a time (matches the singleton globals on the
// C side). The mutex serialises Start/Stop transitions; the audio
// callback fires on the WASAPI worker thread without taking this mutex.
type windowsCapturer struct {
	mu     sync.Mutex
	handle cgo.Handle
	active bool
}

func newCapturer() Capturer { return &windowsCapturer{} }

// Start begins WASAPI loopback capture on the default render endpoint and
// delivers PCM frames to onAudio. Returns ErrNoPlaybackDevice when no
// default render endpoint is available, or a wrapped error on any other
// WASAPI/COM failure.
//
// The onAudio callback fires on the WASAPI capture worker thread (NOT the
// Go main goroutine). Callers must do their own marshalling if they need
// main-thread access. Frames arrive in CanonicalAudioFormat (16 kHz mono
// int16).
func (w *windowsCapturer) Start(onAudio AudioCallback) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.active {
		return errors.New("screencapture: already active")
	}
	if onAudio == nil {
		return errors.New("screencapture: nil callback")
	}

	h := cgo.NewHandle(onAudio)
	rc := C.wasapi_start(C.uintptr_t(h))
	switch rc {
	case 0:
		w.handle = h
		w.active = true
		return nil
	case 1:
		h.Delete()
		return ErrNoPlaybackDevice
	case 2:
		h.Delete()
		return fmt.Errorf("screencapture: WASAPI device activate/initialize failed (rc=%d)", int(rc))
	case 3:
		h.Delete()
		return fmt.Errorf("screencapture: WASAPI capture client start failed (rc=%d)", int(rc))
	case 4:
		h.Delete()
		return errors.New("screencapture: WASAPI loopback already active (C side)")
	default:
		h.Delete()
		return fmt.Errorf("screencapture: WASAPI start failed (rc=%d)", int(rc))
	}
}

// Stop halts the WASAPI capture worker thread and releases the cgo.Handle.
// Idempotent: a second call on a stopped Capturer returns nil. The onAudio
// callback may still fire briefly after Stop returns because the worker
// thread teardown is asynchronous — Go-side consumers should treat the
// callback as "may fire one more time after Stop" and tolerate it. For
// meeting mode this is fine: the daemon ignores system_audio frames once
// the meeting is stopped.
func (w *windowsCapturer) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		return nil
	}
	C.wasapi_stop()
	w.handle.Delete()
	w.handle = 0
	w.active = false
	return nil
}

// goWindowsAudioCallback is the C-callable bridge into the Go
// AudioCallback. It fires on the WASAPI capture worker thread, NOT the Go
// main goroutine.
//
// The pcm pointer references memory owned by the C-side conversion
// buffer; we must copy out before returning because the buffer is reused
// on the next capture iteration. We also recover() from any panic in the
// user callback — letting a Go panic unwind into C would be a bad day.
//
// Signature note: parameters are named handle/pcm/length and the pcm
// pointer is non-const to match cgo's emitted prototype in _cgo_export.h.
// A mismatch (e.g. `const uint8_t *`) produces "conflicting types" errors.
//
//export goWindowsAudioCallback
func goWindowsAudioCallback(handle C.uintptr_t, pcm *C.uint8_t, length C.int) {
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
	// Copy out of the C-owned buffer before the worker thread reuses it.
	out := make([]byte, n)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(pcm)), n))

	defer func() {
		// Don't let user-callback panics unwind into C — the runtime
		// can't safely recover from that. Swallow + carry on; the next
		// frame will deliver normally.
		_ = recover()
	}()
	cb(out)
}
