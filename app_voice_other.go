//go:build !darwin && !windows

// app_voice_other.go — non-macOS, non-Windows fallback for
// GetAudioInputDevices.
//
// On Linux (and any other future port target) we don't yet have a native
// audio-device enumeration path. We return a single "Default" entry so the
// Settings dropdown has at least one option to render and the daemon falls
// back to the OS default input device.
//
// macOS uses app_voice_darwin.go (Core Audio via system_profiler).
// Windows uses app_voice_windows.go (MMDevice / IMMDeviceEnumerator).
package main

func enumerateAudioInputs() []AudioDevice {
	return []AudioDevice{
		{ID: "default", Name: "Default", IsDefault: true},
	}
}
