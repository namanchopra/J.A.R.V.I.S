//go:build windows

// screencapture_windows_common.go — Windows declarations shared between the
// CGO WASAPI implementation (screencapture_windows.go) and the !cgo stub
// (screencapture_windows_nocgo.go). Lives in its own cgo-free file so the
// windows-tagged test file can reference these symbols under either build
// mode (the arm64 release build and Mac-side cross-compile sanity both run
// with CGO_ENABLED=0).

package screencapture

import "errors"

// ErrNoPlaybackDevice is returned by Start when no default audio render
// endpoint is available (headless server, all devices disabled, etc.).
// Distinct from a transient capture failure so the meeting UI can surface
// the right CTA ("plug in or enable a playback device, then retry").
var ErrNoPlaybackDevice = errors.New("screencapture: no default playback device available for WASAPI loopback")
