// Package spotify provides drivers for controlling Spotify. The AppleScript
// driver in applescript.go talks to the local Spotify.app on macOS via
// osascript and works without any OAuth setup. The Web API client (TASK-008)
// lives in sibling files and is composed on top for search/metadata
// operations.
package spotify

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ErrInvalidArg is returned when a caller passes an argument outside the
// allowed range (e.g. a volume percentage outside 0-100, or an empty URI).
// We surface this without invoking osascript so a caller bug is caught at
// the boundary rather than producing an opaque "exit status 1" later.
var ErrInvalidArg = errors.New("spotify: invalid argument")

// osascriptFn is the test seam used by *AppleScript. Production uses
// defaultOsascript; tests inject a recorder closure to capture the script.
//
// The returned string is the trimmed combined output (stdout+stderr) of
// osascript. On non-zero exit the error must be non-nil; Spotify's
// user-facing diagnostics like "Spotify got an error: Can't play track"
// land in stderr, so callers fold the output text into the wrapped error
// message to preserve it for surface-level pattern matching.
type osascriptFn func(script string) (string, error)

// defaultOsascript shells `osascript -e <script>` and returns the combined
// output. Combining stdout+stderr keeps Spotify's diagnostic text visible
// on error paths — without it, "Can't play track" disappears into stderr
// and the caller only sees "exit status 1".
func defaultOsascript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	trimmed := strings.TrimRight(string(out), "\n")
	if err != nil {
		return trimmed, fmt.Errorf("osascript: %w: %s", err, trimmed)
	}
	return trimmed, nil
}

// AppleScript drives the local Spotify.app via osascript. It is the
// non-OAuth half of this package — every method here works as long as the
// Spotify desktop client is installed and running, with no network. The
// Web API half (TASK-008) handles search and metadata that AppleScript
// can't reach.
//
// Methods are safe to call concurrently: each call shells a fresh
// osascript subprocess.
type AppleScript struct {
	osascript osascriptFn
}

// NewAppleScript returns a driver wired to the real osascript binary.
func NewAppleScript() *AppleScript {
	return &AppleScript{osascript: defaultOsascript}
}

// Track is the subset of Spotify's "current track" metadata Jarvis cares
// about. A zero-valued Track signals "Spotify isn't playing anything (or
// isn't running)" — see CurrentTrack for the not-running contract.
type Track struct {
	Name            string `json:"name"`
	Artist          string `json:"artist"`
	URI             string `json:"uri"`
	PositionSeconds int    `json:"positionSeconds"`
}

// Play starts playback of the given Spotify URI (e.g. "spotify:track:...").
// If the Spotify client rejects the URI, its diagnostic ("Can't play
// track") bubbles up via the returned error so the caller can surface it
// to the user verbatim.
func (a *AppleScript) Play(uri string) error {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return fmt.Errorf("Play: %w: uri is required", ErrInvalidArg)
	}
	// AppleScript string literals don't escape embedded quotes or
	// backslashes. Spotify URIs are restricted to [A-Za-z0-9:_-], so any
	// stray quote/backslash is a sign of injection — reject defensively.
	if strings.ContainsAny(uri, `"\`) {
		return fmt.Errorf("Play: %w: uri contains forbidden characters", ErrInvalidArg)
	}
	script := fmt.Sprintf(`tell application "Spotify" to play track %q`, uri)
	if _, err := a.osascript(script); err != nil {
		return fmt.Errorf("Play: %w", err)
	}
	return nil
}

// Pause pauses Spotify playback. No-op if already paused.
func (a *AppleScript) Pause() error {
	if _, err := a.osascript(`tell application "Spotify" to pause`); err != nil {
		return fmt.Errorf("Pause: %w", err)
	}
	return nil
}

// Resume resumes Spotify playback. AppleScript uses "play" with no
// arguments to continue the current track — there is no separate "resume"
// verb in Spotify's scripting dictionary.
func (a *AppleScript) Resume() error {
	if _, err := a.osascript(`tell application "Spotify" to play`); err != nil {
		return fmt.Errorf("Resume: %w", err)
	}
	return nil
}

// Next skips to the next track in Spotify's current queue.
func (a *AppleScript) Next() error {
	if _, err := a.osascript(`tell application "Spotify" to next track`); err != nil {
		return fmt.Errorf("Next: %w", err)
	}
	return nil
}

// Previous goes to the previous track. Behaviour matches the desktop
// client: first invocation rewinds to the start of the current track,
// second invocation jumps to the prior track.
func (a *AppleScript) Previous() error {
	if _, err := a.osascript(`tell application "Spotify" to previous track`); err != nil {
		return fmt.Errorf("Previous: %w", err)
	}
	return nil
}

// SetVolume sets Spotify's internal sound volume on a 0-100 scale.
// Out-of-range values return ErrInvalidArg without invoking osascript —
// we reject rather than clamp so callers don't silently end up at a
// different volume than they asked for.
func (a *AppleScript) SetVolume(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("SetVolume: %w: pct=%d not in 0..100", ErrInvalidArg, pct)
	}
	script := fmt.Sprintf(`tell application "Spotify" to set sound volume to %d`, pct)
	if _, err := a.osascript(script); err != nil {
		return fmt.Errorf("SetVolume: %w", err)
	}
	return nil
}

// CurrentTrack returns metadata about the track currently loaded in
// Spotify. When Spotify is not running, returns a zero-valued Track and a
// nil error — "not running" is a normal idle state, not an exceptional
// failure, so callers like the voice daemon can speak "Nothing's playing"
// without first inspecting an error.
//
// Any other osascript failure (script syntax error, parse failure, etc.)
// is returned as a wrapped error.
func (a *AppleScript) CurrentTrack() (Track, error) {
	const script = `tell application "Spotify"
	set t to current track
	return (name of t) & "|" & (artist of t) & "|" & (spotify url of t) & "|" & (player position as integer)
end tell`
	out, err := a.osascript(script)
	if err != nil {
		// Spotify's AppleScript dictionary returns "Spotify got an error"
		// when the app isn't running and we ask it to look at a current
		// track. Treat that signature as a soft "not running" state.
		if isSpotifyNotRunning(out) || isSpotifyNotRunning(err.Error()) {
			return Track{}, nil
		}
		return Track{}, fmt.Errorf("CurrentTrack: %w", err)
	}
	track, parseErr := parseCurrentTrack(out)
	if parseErr != nil {
		return Track{}, fmt.Errorf("CurrentTrack: %w", parseErr)
	}
	return track, nil
}

// isSpotifyNotRunning reports whether an osascript output blob carries the
// "Spotify is not running" signature.
func isSpotifyNotRunning(s string) bool {
	return strings.Contains(s, "Spotify got an error")
}

// parseCurrentTrack parses the pipe-delimited output of the CurrentTrack
// AppleScript snippet. Empty input produces a zero-valued Track without
// error (Spotify returned no track).
func parseCurrentTrack(out string) (Track, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return Track{}, nil
	}
	parts := strings.Split(out, "|")
	if len(parts) != 4 {
		return Track{}, fmt.Errorf("unexpected current-track format: %q", out)
	}
	pos, err := strconv.Atoi(strings.TrimSpace(parts[3]))
	if err != nil {
		return Track{}, fmt.Errorf("parse position %q: %w", parts[3], err)
	}
	return Track{
		Name:            strings.TrimSpace(parts[0]),
		Artist:          strings.TrimSpace(parts[1]),
		URI:             strings.TrimSpace(parts[2]),
		PositionSeconds: pos,
	}, nil
}
