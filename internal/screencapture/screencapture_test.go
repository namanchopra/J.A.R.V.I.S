//go:build !darwin

package screencapture

import (
	"errors"
	"testing"
)

func TestNew_ReturnsNonNilCapturer(t *testing.T) {
	c := New()
	if c == nil {
		t.Fatal("New() returned nil; expected non-nil Capturer")
	}
}

func TestStart_OnNonDarwin_ReturnsUnsupportedPlatform(t *testing.T) {
	c := New()
	err := c.Start(func(_ []byte) {})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
	}
}

func TestStop_OnNonDarwin_Idempotent(t *testing.T) {
	c := New()
	if err := c.Stop(); err != nil {
		t.Fatalf("first Stop returned err: %v", err)
	}
	if err := c.Stop(); err != nil {
		t.Fatalf("second Stop returned err: %v", err)
	}
}

func TestStart_NilCallback_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Start(nil) panicked: %v", r)
		}
	}()
	c := New()
	_ = c.Start(nil) // should return error not panic
}

func TestCanonicalAudioFormat_IsDocumentedShape(t *testing.T) {
	f := CanonicalAudioFormat
	if f.SampleRateHz != 16000 || f.Channels != 1 || f.BitsPerSample != 16 {
		t.Errorf("CanonicalAudioFormat changed: %+v", f)
	}
}
