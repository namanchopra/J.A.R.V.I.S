package macctl

import (
	"errors"
	"os/exec"
)

// ErrNotImplemented is returned by stub methods until their real
// implementation lands. Pinning the error type lets tests distinguish
// "feature not built yet" from "real failure" without parsing strings.
//
// TASK-011..014 will replace each stub below with a real osascript /
// exec invocation; until then this sentinel is the canonical signal
// that a Controller method has not yet been wired up.
var ErrNotImplemented = errors.New("macctl: not implemented")

// ErrPolicyDeny is returned when a tool's policy decision is deny.
// The caller MUST surface this as a user-facing message rather than
// a generic failure (the daemon's tool executor knows how to render it
// into a spoken response like "I'm not permitted to quit apps").
var ErrPolicyDeny = errors.New("macctl: tool denied by policy")

// ErrWindowNotFound is returned by FocusWindow when no window matches
// the (app, title) pair. Distinguished from a generic osascript failure
// so the caller can offer a "did you mean..." style fallback instead of
// surfacing a raw AppleScript error.
var ErrWindowNotFound = errors.New("macctl: window not found")

// ErrToolUnavailable is returned when a required external tool
// (e.g. the optional `brightness` CLI consumed by SetBrightness) is
// not installed on the host. The Controller degrades gracefully — it
// returns this typed error so callers can prompt the user to install
// the missing dependency rather than reporting a generic exec failure.
var ErrToolUnavailable = errors.New("macctl: required tool unavailable")

// ErrInvalidArg is returned when a method receives an argument that
// fails its preconditions (e.g. SetVolume(-1) or SetBrightness(101)).
// Distinguishing this from a generic error lets the daemon's tool
// layer render a precise spoken response ("That volume is out of
// range — try 0 to 100") instead of a generic "something went wrong".
//
// Mirrors the ErrInvalidQuery sentinel pattern from
// internal/spotify/errors.go — same rationale: validation failures
// should never reach the side-effecting layer.
var ErrInvalidArg = errors.New("macctl: invalid argument")

// osascriptFn is a test seam — production passes a closure that shells
// exec.Command("osascript", "-e", script). Tests substitute a recorder
// so they can assert which scripts get issued without actually invoking
// the real osascript binary (which would mutate the host's state).
//
// Kept unexported so callers outside the package cannot override it;
// in-package tests swap c.osascript = recorder directly on the struct.
type osascriptFn func(script string) (string, error)

// defaultOsascript is the production osascript invoker. Shells out to
// /usr/bin/osascript with a -e flag; returns the stdout as a string.
// Errors from exec.Command are passed through verbatim — TASK-011..014
// will add context-wrapping (fmt.Errorf("OpenApp: %w", err)) once they
// inspect the failure mode for each tool.
func defaultOsascript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Controller is the public façade for system-wide Mac control — app
// launch/focus, audio/display tunables, clipboard I/O, screenshots,
// and macOS Shortcuts invocation. Each method is a tool the Jarvis
// daemon's LLM can call via the daemon ⇄ Wails tool bridge.
//
// The Controller does NOT enforce policy itself; it holds a reference
// to a *Policy so subsequent tasks (TASK-011..014) can gate each
// invocation through controller.policy.Check(toolName) and return
// ErrPolicyDeny when denied.
//
// All 15 method stubs below return (zero, ErrNotImplemented). Real
// implementations land in TASK-011..014.
type Controller struct {
	// policy is the persisted per-tool permission map. Nil is rejected
	// by NewController; methods may assume policy != nil.
	policy *Policy

	// osascript is the test seam described above. Production callers
	// receive defaultOsascript via NewController; tests overwrite it
	// in-place with a recorder.
	osascript osascriptFn
}

// NewController returns a Controller wired with the production osascript
// invoker and the given policy. The policy argument is required — a nil
// policy would silently allow every destructive tool, so callers must
// pass NewDefaultPolicy() (or a Load()-restored *Policy) explicitly.
//
// Returning a pointer keeps the test seam mutable from in-package tests:
//
//	c := NewController(NewDefaultPolicy())
//	c.osascript = recorder
func NewController(policy *Policy) *Controller {
	return &Controller{
		policy:    policy,
		osascript: defaultOsascript,
	}
}

// --- Apps + windows (TASK-011) ---
// Implementations live in apps.go (OpenApp, QuitApp) and windows.go
// (FocusWindow).

// --- Audio + display (TASK-012) ---
// Implementations live in audio.go (SetVolume, Mute, Unmute) and
// display.go (SetBrightness, ToggleDND).

// --- Files + clipboard + screenshots (TASK-013) ---

// OpenPath shells `open <path>`. Handles both filesystem paths and URLs
// (the `open` command treats both uniformly). TASK-013 will surface a
// clear error if the path doesn't exist rather than letting `open`
// silently succeed.
func (c *Controller) OpenPath(path string) (string, error) { return "", ErrNotImplemented }

// Spotlight runs `mdfind <query>` and returns up to 20 matching paths,
// newline-separated. Read-only by policy default.
func (c *Controller) Spotlight(query string) (string, error) { return "", ErrNotImplemented }

// Screenshot shells `screencapture` with the appropriate flag for the
// target. Valid targets: "screen" (full), "window" (interactive window
// picker), "selection" (drag rectangle). TASK-013 will write the output
// to ~/.jarvis/screenshots/<timestamp>.png and return the file path.
func (c *Controller) Screenshot(target string) (string, error) { return "", ErrNotImplemented }

// ClipboardGet shells `pbpaste` and returns the current clipboard text.
// Returns "" + nil when the clipboard is empty (not an error condition).
func (c *Controller) ClipboardGet() (string, error) { return "", ErrNotImplemented }

// ClipboardSet shells `pbcopy` and writes text to the clipboard.
// Destructive (overwrites whatever the user had copied); gated on
// policy.Check("mac_clipboard_set").
func (c *Controller) ClipboardSet(text string) (string, error) { return "", ErrNotImplemented }

// --- Shortcuts (TASK-014) ---

// ListShortcuts shells `shortcuts list --output-format json` and returns
// the array of Shortcut names installed on the host. Read-only by policy
// default. Returns nil + ErrNotImplemented from the stub; TASK-014 will
// return ([]string{}, nil) for the empty case (never nil) to match the
// project's Wails serialization convention.
func (c *Controller) ListShortcuts() ([]string, error) { return nil, ErrNotImplemented }

// RunShortcut shells `shortcuts run "Name"` and pipes input to the
// shortcut's stdin (for inputs >1KB; smaller inputs use --input). Returns
// the shortcut's stdout. Destructive; gated on policy.Check("mac_run_shortcut").
func (c *Controller) RunShortcut(name, input string) (string, error) {
	return "", ErrNotImplemented
}
