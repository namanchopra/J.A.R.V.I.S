// app_voice.go — Wails bindings for Phase 2 P1 voice surface (TASK-018/019/020).
//
// Exposes two methods to the Settings UI:
//
//   - GetAudioInputDevices(): list of microphone input devices on the host so
//     the Voice tab can render a mic-picker dropdown. Implementation is
//     platform-specific (see app_voice_darwin.go for macOS Core Audio via
//     system_profiler, app_voice_other.go for non-darwin fallback).
//
//   - PreviewVoice(provider, voiceId): asks the running Jarvis Python daemon
//     to synthesize a fixed sample sentence ("Hello, sir. This is what I'll
//     sound like.") through the configured TTS pipeline and play it on the
//     system default output device. Reuses the existing daemon WebSocket
//     channel (api.JarvisDaemonConn) via App.jarvisDaemonConn() — no new IPC
//     transport is introduced. The daemon is expected to auto-cancel any
//     in-flight preview when a new preview_tts message arrives, which gives
//     the UI "interrupt prior playback" semantics for free.
//
// Validation contract:
//
//   - provider must be one of: "vibevoice", "kokoro", "edge", "cartesia".
//   - voiceId must be non-empty (provider-specific; e.g. "en-Carter_man" for
//     vibevoice, "en-GB-RyanNeural" for edge).
//
// Errors are wrapped with the method name per the repo-wide convention
// documented in CLAUDE.md (e.g. "PreviewVoice: daemon not connected").
package main

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/namanchopra/jarvis/internal/api"
)

// AudioDevice describes a microphone input device for the Settings UI
// dropdown. Wails serialises this struct directly to the frontend.
type AudioDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

// previewSampleText is the fixed sentence the daemon synthesises when the
// user presses the Preview button in Settings. Kept as a package-level
// constant so the test suite (and any future i18n surface) can reference
// the same string.
const previewSampleText = "Hello, sir. This is what I'll sound like."

// allowedPreviewProviders is the set of TTS providers the Settings UI is
// allowed to ask the daemon to use for a preview. We validate against this
// list before sending the message so a malformed dropdown value doesn't
// surface a runtime error from inside the Python daemon's TTS plugin
// registry.
var allowedPreviewProviders = map[string]struct{}{
	"vibevoice": {},
	"kokoro":    {},
	"edge":      {},
	"cartesia":  {},
}

// GetAudioInputDevices enumerates available microphones on the host so the
// Voice settings tab can render an input-device dropdown. On macOS it shells
// out to system_profiler SPAudioDataType -json (no cgo); on other platforms
// it returns a single "default" placeholder. Returns nil on enumeration
// failure — callers should fall back to a "Default" entry in the UI.
func (a *App) GetAudioInputDevices() []AudioDevice {
	return enumerateAudioInputs()
}

// PreviewVoice asks the running Jarvis Python daemon to synthesise a fixed
// sample sentence using the supplied TTS provider + voice and play it on
// the default audio output. Pressing Preview again while a prior preview is
// in flight is safe — the daemon auto-cancels prior preview playback when
// it receives a new preview_tts message, so the UI does not need to send a
// separate cancel command.
//
// Returns an error if:
//   - provider is not one of the known TTS providers (defensive UI check)
//   - voiceId is empty
//   - the daemon WebSocket is not connected (daemon not running)
//   - the WebSocket write fails
func (a *App) PreviewVoice(provider, voiceId string) error {
	provider = strings.TrimSpace(provider)
	voiceId = strings.TrimSpace(voiceId)

	if provider == "" {
		return fmt.Errorf("PreviewVoice: provider is required")
	}
	if _, ok := allowedPreviewProviders[provider]; !ok {
		return fmt.Errorf("PreviewVoice: unsupported provider %q", provider)
	}
	if voiceId == "" {
		return fmt.Errorf("PreviewVoice: voiceId is required")
	}

	conn := a.jarvisDaemonConn()
	if conn == nil || !conn.Connected() {
		return fmt.Errorf("PreviewVoice: daemon not connected")
	}

	// Reuse the existing JarvisOutgoing envelope rather than declaring a new
	// IPC schema — the Result field is typed interface{} and is already used
	// to carry arbitrary tool-result payloads (see SendJarvisCommand in
	// app_jarvis.go for the matching "command" type). We tag the message
	// with Type="preview_tts" so the daemon's router can dispatch to its
	// TTS preview handler without colliding with regular text commands.
	msg := api.JarvisOutgoing{
		Type: "preview_tts",
		Text: previewSampleText,
		Result: map[string]interface{}{
			"provider": provider,
			"voiceId":  voiceId,
		},
	}
	if err := conn.Send(msg); err != nil {
		return fmt.Errorf("PreviewVoice: %w", err)
	}

	slog.Debug("voice preview dispatched", "provider", provider, "voiceId", voiceId)
	return nil
}
