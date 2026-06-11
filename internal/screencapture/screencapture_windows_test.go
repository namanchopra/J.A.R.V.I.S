//go:build windows

// screencapture_windows_test.go — integration smoke test for the real
// Windows WASAPI loopback Capturer (TASK-041 / TASK-049).
//
// What this test verifies:
//   * Start → audio worker thread fires ≥ N callbacks within a bounded
//     time window. WASAPI loopback delivers silent buffers even when no
//     application is producing audio (per Microsoft docs), so a fresh
//     headless box still produces frames — callback count is the
//     liveness signal, not byte content.
//   * Stop is idempotent and tears the worker thread down cleanly.
//
// Why we skip rather than fail on ErrNoPlaybackDevice:
//   * GitHub Actions Windows runners are headless and frequently have
//     no default render endpoint at all. Failing the suite there would
//     turn a real CI signal into noise. Local devs with a real audio
//     stack still get full coverage.
//
// Threading note:
//   * goWindowsAudioCallback fires on the WASAPI worker thread, NOT the
//     test goroutine. We use sync/atomic for the frame counter to avoid
//     a race detector complaint when this is run under `go test -race`.

package screencapture

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestStart_OnWindows_DeliversAudioFrames is the TASK-049 integration
// rail: open WASAPI loopback, run for up to 5s, assert we get at least
// 10 callback invocations. Even on a silent system, WASAPI emits silence
// frames at the device period (~10ms default), so 10 callbacks in 5s is
// a very conservative liveness floor — a healthy device delivers
// hundreds.
//
// Skips when no default playback device exists (headless CI), so the
// test stays green on runners without audio hardware while still
// catching regressions on dev boxes and properly-provisioned CI agents.
func TestStart_OnWindows_DeliversAudioFrames(t *testing.T) {
	c := New()
	var frames int32

	err := c.Start(func(pcm []byte) {
		atomic.AddInt32(&frames, 1)
		_ = pcm
	})
	if errors.Is(err, ErrNoPlaybackDevice) {
		t.Skip("no default playback device available; skipping WASAPI integration test (headless runner?)")
	}
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	// Poll for ≥10 callbacks for up to 5s. On a healthy device this
	// completes in well under a second; the deadline only fires on a
	// broken capture worker. The acceptance criterion is "≥10 callbacks
	// in <10s" — we run with a 5s budget to leave headroom for the test
	// harness + teardown without blowing the criterion.
	const wantFrames = int32(10)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&frames) >= wantFrames {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got := atomic.LoadInt32(&frames)
	if got < wantFrames {
		t.Errorf("expected at least %d audio frames within 5s, got %d", wantFrames, got)
	}
}

// TestStart_NilCallback_OnWindows_ReturnsError confirms the
// input-validation rail at the top of windowsCapturer.Start. A nil
// callback would crash goWindowsAudioCallback when the worker thread
// dereferences the cgo.Handle, so the Go-side guard is load-bearing.
func TestStart_NilCallback_OnWindows_ReturnsError(t *testing.T) {
	c := New()
	if err := c.Start(nil); err == nil {
		t.Fatal("expected error for nil callback on Windows Capturer")
	}
}

// TestStop_Idempotent_OnWindows guarantees the Capturer interface
// contract on Windows: calling Stop on a fresh capturer or twice in a
// row must return nil rather than blowing up (e.g. double-Delete of the
// cgo.Handle would panic). Required for any consumer that wants a
// defer-style teardown.
func TestStop_Idempotent_OnWindows(t *testing.T) {
	c := New()
	if err := c.Stop(); err != nil {
		t.Fatalf("first Stop on fresh capturer: %v", err)
	}
	if err := c.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestStart_DoubleStart_OnWindows_ReturnsError verifies the
// active-capture guard: a second Start without an intervening Stop must
// error rather than silently leak the previous cgo.Handle or stomp the
// singleton C-side state.
func TestStart_DoubleStart_OnWindows_ReturnsError(t *testing.T) {
	c := New()
	err := c.Start(func(_ []byte) {})
	if errors.Is(err, ErrNoPlaybackDevice) {
		t.Skip("no default playback device available; double-start test needs first Start to succeed")
	}
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	if err := c.Start(func(_ []byte) {}); err == nil {
		t.Fatal("expected error on second Start without intervening Stop")
	}
}
