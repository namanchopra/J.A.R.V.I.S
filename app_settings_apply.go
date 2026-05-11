package main

import (
	"fmt"

	"github.com/namanchopra/jarvis/internal/config"
)

// SaveConfig persists the Jarvis configuration to ~/.jarvis/config.json
// and reports whether the change requires restarting the Python daemon.
//
// Sequence:
//  1. Load the previously persisted config (best-effort — on read error
//     we treat "old" as the zero Config so every field appears to have
//     changed, which is the safe default).
//  2. Compute DaemonRestartNeeded by comparing the previous and new
//     daemon-relevant fields.
//  3. Persist the new config.
//  4. Return the flag to the caller. SaveConfig never actually restarts
//     the daemon itself — RestartJarvis() is the only restart trigger,
//     called explicitly by the frontend after the user confirms.
func (a *App) SaveConfig(cfg config.Config) (config.SaveResult, error) {
	// Step 1: best-effort load of the previous on-disk config so we can
	// diff. If the file is missing or corrupt, config.Load returns the
	// defaults — that's a fine baseline for a first-run save.
	prev, err := config.Load()
	if err != nil {
		// Don't fail the save just because we couldn't read the old
		// file; assume zero values and let the restart-needed check err
		// on the side of "yes, restart".
		prev = &config.Config{}
	}

	restartNeeded := daemonRestartNeeded(*prev, cfg)

	// Step 2: persist.
	if err := config.Save(&cfg); err != nil {
		return config.SaveResult{}, fmt.Errorf("SaveConfig: %w", err)
	}

	return config.SaveResult{DaemonRestartNeeded: restartNeeded}, nil
}

// DaemonRestartNeeded is exported as a Wails binding so the frontend can
// preview "would saving this config require a restart?" without actually
// writing to disk. Delegates to the internal helper for the real logic.
//
// Returns true if any of the fields the Python daemon reads at startup
// differ between old and next.
func (a *App) DaemonRestartNeeded(old, next config.Config) bool {
	return daemonRestartNeeded(old, next)
}

// daemonRestartNeeded compares two configs and returns true if any field
// that the Python daemon reads at startup has changed. The set is
// deliberately conservative: anything the daemon caches at boot belongs
// here; anything the daemon never reads (notifications, scan interval,
// terminal preferences, project roots, mobile API port/token) does NOT.
//
// Fields that trigger a restart (per v0.1.2 spec):
//   - Voice settings: TtsProvider, SttModel, VoicePreset, MicInputDevice,
//     WakeWordEnabled
//   - API keys consumed by the daemon: GoogleAPIKey, AnthropicAPIKey,
//     CartesiaAPIKey, JarvisAPIKey, JarvisElevenLabsKey,
//     JarvisPicovoiceKey
//   - Agent invocation: DefaultAgent, DefaultCommand
//   - Voice transport: UseLiveKitTransport, LiveKitURL, LiveKitAPIKey,
//     LiveKitAPISecret, LiveKitRoomName
func daemonRestartNeeded(old, next config.Config) bool {
	// Voice / STT / TTS configuration.
	if old.TtsProvider != next.TtsProvider {
		return true
	}
	if old.SttModel != next.SttModel {
		return true
	}
	if old.VoicePreset != next.VoicePreset {
		return true
	}
	if old.MicInputDevice != next.MicInputDevice {
		return true
	}
	if !boolPtrEqual(old.WakeWordEnabled, next.WakeWordEnabled) {
		return true
	}

	// API keys read by the daemon at startup.
	if old.GoogleAPIKey != next.GoogleAPIKey {
		return true
	}
	if old.AnthropicAPIKey != next.AnthropicAPIKey {
		return true
	}
	if old.CartesiaAPIKey != next.CartesiaAPIKey {
		return true
	}
	if old.JarvisAPIKey != next.JarvisAPIKey {
		return true
	}
	if old.JarvisElevenLabsKey != next.JarvisElevenLabsKey {
		return true
	}
	if old.JarvisPicovoiceKey != next.JarvisPicovoiceKey {
		return true
	}

	// Agent invocation defaults the daemon uses when spawning sessions.
	if old.DefaultAgent != next.DefaultAgent {
		return true
	}
	if old.DefaultCommand != next.DefaultCommand {
		return true
	}

	// Voice transport — LiveKit vs LocalAudio is baked in at daemon boot.
	if old.UseLiveKitTransport != next.UseLiveKitTransport {
		return true
	}
	if old.LiveKitURL != next.LiveKitURL {
		return true
	}
	if old.LiveKitAPIKey != next.LiveKitAPIKey {
		return true
	}
	if old.LiveKitAPISecret != next.LiveKitAPISecret {
		return true
	}
	if old.LiveKitRoomName != next.LiveKitRoomName {
		return true
	}

	return false
}

// boolPtrEqual compares two *bool values treating nil ("unset") as a
// distinct state from a non-nil pointer to false ("explicitly disabled").
// Returns true if both are nil OR both are non-nil with the same bool.
func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
