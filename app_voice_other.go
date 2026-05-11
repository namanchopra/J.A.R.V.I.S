//go:build !darwin

// app_voice_other.go — non-macOS fallback for GetAudioInputDevices.
//
// On Linux/Windows we don't currently enumerate Core Audio (no equivalent
// shell command, and cgo bindings to ALSA / WASAPI are out of scope for
// Phase 2 P1). We return a single "Default" entry so the Settings dropdown
// has at least one option to render and the daemon falls back to the OS
// default input device.
package main

func enumerateAudioInputs() []AudioDevice {
	return []AudioDevice{
		{ID: "default", Name: "Default", IsDefault: true},
	}
}
