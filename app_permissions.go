package main

import "github.com/namanchopra/jarvis/internal/permissions"

// GetMicPermissionStatus returns the current microphone authorization state.
// One of: "granted", "denied", "not_determined", "restricted".
//
// This binding is a Wails-exposed method on App; the frontend can call it to
// decide whether to show an onboarding step that requests permission.
func (a *App) GetMicPermissionStatus() string {
	return permissions.MicStatus()
}

// RequestMicPermission triggers the OS-level microphone permission dialog.
// Returns immediately; the caller should poll GetMicPermissionStatus to learn
// the user's decision. Calling this when status is already "granted" or
// "denied" is a no-op from the user's perspective (no second prompt).
func (a *App) RequestMicPermission() {
	permissions.RequestMic()
}
