package syscontrol

// AudioController controls the system playback device's volume and mute
// state. The reference macOS implementation (internal/macctl/audio.go)
// routes through `osascript`; TASK-022's Windows backend uses the
// IAudioEndpointVolume COM interface via go-ole.
//
// Implementations gate destructive operations through their own policy
// layer (see internal/macctl/policy.go for the macOS reference) before
// touching the audio endpoint. Validation (range checks etc.) happens
// before the policy gate so callers get the more actionable error.
type AudioController interface {
	// SetVolume sets the default playback device's volume to pct
	// (0..100). Values outside that range MUST be rejected with a
	// wrapped error (see internal/macctl.ErrInvalidArg for the
	// reference sentinel) before any side effect — a stray voice
	// misfire ("set volume to a hundred and fifty") should never
	// reach the OS.
	SetVolume(pct int) (string, error)

	// Mute mutes the default playback device. Idempotent — muting
	// a muted device is a no-op at the OS layer; implementations
	// MUST NOT surface that as an error.
	Mute() (string, error)

	// Unmute clears the muted flag on the default playback device.
	// Counterpart to Mute; equally idempotent.
	Unmute() (string, error)
}
