//go:build darwin

// screencapture_darwin_test.go — smoke tests for the real darwin Capturer.
//
// These tests run on a Mac and assume macOS 13+. They are tolerant of CI
// runners without Screen Recording permission: the audio-delivery test
// skips cleanly if permission is denied so a denied runner doesn't fail
// the suite. The idempotent-Stop and nil-callback tests have no permission
// dependency and always run.

package screencapture

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestStart_OnDarwinWithPermission_DeliversAudio is best-run manually with
// Screen Recording permission granted and some audio source playing (any
// browser, a music app, etc — system audio coverage means anything counts).
// Skips on permission denial so unattended CI runs don't fail.
func TestStart_OnDarwinWithPermission_DeliversAudio(t *testing.T) {
	c := New()
	var frames int32
	err := c.Start(func(pcm []byte) {
		atomic.AddInt32(&frames, 1)
		_ = pcm
	})
	if errors.Is(err, ErrPermissionDenied) {
		t.Skip("Screen Recording permission denied; skipping (manual-run test)")
	}
	if errors.Is(err, ErrUnsupportedOS) {
		t.Skipf("macOS < 13.0; skipping (got %v)", err)
	}
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	// 1.5s of capture. Even with no acoustic energy the audio path
	// delivers silence frames at ~10/s on macOS 13+, so >= 1 frame is
	// a conservative liveness check.
	time.Sleep(1500 * time.Millisecond)
	got := atomic.LoadInt32(&frames)
	if got < 1 {
		t.Errorf("expected at least 1 audio frame in 1.5s, got %d", got)
	}
}

// TestStop_Idempotent guarantees the Capturer interface contract:
// calling Stop on a fresh capturer or twice in a row must return nil
// rather than blowing up. Required for any consumer that wants a
// defer-style teardown.
func TestStop_Idempotent(t *testing.T) {
	c := New()
	if err := c.Stop(); err != nil {
		t.Fatalf("first Stop on fresh capturer: %v", err)
	}
	if err := c.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestStart_NilCallback_ReturnsError confirms the input-validation rail
// at the top of Start. Without this, a nil callback would crash the
// process when goAudioCallback dereferences it.
func TestStart_NilCallback_ReturnsError(t *testing.T) {
	c := New()
	err := c.Start(nil)
	if err == nil {
		t.Fatal("expected error for nil callback")
	}
}

// TestStart_DoubleStart_ReturnsError verifies the active-capture guard:
// a second Start without an intervening Stop must error rather than
// silently leak the previous cgo.Handle or stomp the static SCStream.
func TestStart_DoubleStart_ReturnsError(t *testing.T) {
	c := New()
	err := c.Start(func(_ []byte) {})
	if errors.Is(err, ErrPermissionDenied) {
		t.Skip("permission denied; double-start test needs first Start to succeed")
	}
	if errors.Is(err, ErrUnsupportedOS) {
		t.Skipf("macOS < 13.0; skipping (got %v)", err)
	}
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	err = c.Start(func(_ []byte) {})
	if err == nil {
		t.Fatal("expected error on second Start without intervening Stop")
	}
}
