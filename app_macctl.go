package main

// ---------------------------------------------------------------------------
// macctl Wails bindings — TASK-015 (v0.3.0).
//
// Exposes the 15 system-control methods on *macctl.Controller to the
// frontend + Python daemon via auto-generated Wails bindings, plus two
// helpers (GetMacctlPolicy / SetMacctlPolicy) that back the Settings UI's
// Permissions panel (TASK-017).
//
// Method naming convention: every binding is prefixed with `Mac` so the
// `wails generate module` output groups them cleanly under `App.Mac*` in
// the TypeScript namespace, and so the Python daemon's tool layer can map
// snake_case tool names (`mac_open_app`) to camelCase Wails methods
// (`MacOpenApp`) with a deterministic transform.
//
// All methods route through App.macctl(), a lazy singleton accessor that
// constructs the Controller on first use. Lazy construction means a Wails
// process that never invokes a mac_* tool never reads ~/.jarvis/policy.json
// -- both a perf win and a startup-resilience win (a malformed policy file
// can't crash the boot path).
//
// Errors from the Controller are returned verbatim so the Wails serializer
// surfaces them as JS exceptions on the frontend; the Python daemon side
// extracts the error string and renders friendly spoken messages there.
// ---------------------------------------------------------------------------

import (
	"fmt"

	"github.com/namanchopra/jarvis/internal/macctl"
)

// macctl returns the App's lazily-initialised *macctl.Controller.
//
// Constructed once per process via sync.Once so concurrent Wails handler
// goroutines all observe the same singleton (and the same in-memory policy
// state, which itself is RWMutex-guarded inside *macctl.Policy).
//
// On first call we attempt to load the persisted policy from disk; any I/O
// or parse failure falls through to NewDefaultPolicy() so the Controller is
// always non-nil. We deliberately do NOT log the load failure here — Load
// only errors on truly malformed JSON (missing-file returns defaults), and
// the macctl.Load implementation already surfaces a wrapped error that the
// Settings UI can present if the user opens it. Logging from a lazy getter
// would spam the daemon log on every process start.
func (a *App) macctl() *macctl.Controller {
	a.macctlOnce.Do(func() {
		policy, err := macctl.Load(macctl.PolicyPath())
		if err != nil {
			// Malformed policy.json on disk -- fall back to safe defaults
			// rather than refusing to construct a Controller. The user will
			// see the broken file in Settings -> Permissions and can repair
			// or reset it from there.
			policy = macctl.NewDefaultPolicy()
		}
		a.macctlController = macctl.NewController(policy)
	})
	return a.macctlController
}

// ---------------------------------------------------------------------------
// Apps + windows
// ---------------------------------------------------------------------------

// MacOpenApp launches (or brings to foreground) the macOS app named `name`.
// Shells out to `open -a <name>` via the Controller. Returns the
// Controller's status string (empty on success) and any error verbatim.
//
// Policy gate: mac_open_app (defaults to ask). A DecisionDeny short-circuits
// before any side effect and returns macctl.ErrPolicyDeny.
func (a *App) MacOpenApp(name string) (string, error) {
	return a.macctl().OpenApp(name)
}

// MacQuitApp tells the named app to quit gracefully via AppleScript
// (`tell application <name> to quit`). Policy gate: mac_quit_app.
func (a *App) MacQuitApp(name string) (string, error) {
	return a.macctl().QuitApp(name)
}

// MacFocusWindow brings a specific window of `app` whose title contains
// `title` to the foreground. Returns macctl.ErrWindowNotFound when no
// matching window is found. Policy gate: mac_focus_window.
func (a *App) MacFocusWindow(app, title string) (string, error) {
	return a.macctl().FocusWindow(app, title)
}

// ---------------------------------------------------------------------------
// Audio + display
// ---------------------------------------------------------------------------

// MacSetVolume sets the system output volume to pct (0..100). Validates
// the range before calling osascript -- out-of-range pcts return
// macctl.ErrInvalidArg. Policy gate: mac_set_volume.
func (a *App) MacSetVolume(pct int) (string, error) {
	return a.macctl().SetVolume(pct)
}

// MacMute sets the output-muted property to true. Idempotent. Policy
// gate: mac_mute.
func (a *App) MacMute() (string, error) {
	return a.macctl().Mute()
}

// MacUnmute clears the output-muted property. Idempotent. Policy gate:
// mac_unmute.
func (a *App) MacUnmute() (string, error) {
	return a.macctl().Unmute()
}

// MacSetBrightness sets the display brightness to pct (0..100). May
// return macctl.ErrToolUnavailable if the underlying `brightness` CLI is
// not installed. Policy gate: mac_set_brightness.
func (a *App) MacSetBrightness(pct int) (string, error) {
	return a.macctl().SetBrightness(pct)
}

// MacToggleDND toggles macOS Do Not Disturb via the bundled Shortcuts.app
// "Toggle Focus" recipe (private-API-free). Policy gate: mac_toggle_dnd.
func (a *App) MacToggleDND() (string, error) {
	return a.macctl().ToggleDND()
}

// ---------------------------------------------------------------------------
// Files + spotlight + screenshots
// ---------------------------------------------------------------------------

// MacOpenPath opens a filesystem path or URL via `open <path>`. Policy
// gate: mac_open_path.
func (a *App) MacOpenPath(path string) (string, error) {
	return a.macctl().OpenPath(path)
}

// MacSpotlight runs a Spotlight (`mdfind`) query and returns up to ~20
// matching paths joined by newlines. Read-only. Policy gate:
// mac_spotlight (defaults to allow).
func (a *App) MacSpotlight(query string) (string, error) {
	return a.macctl().Spotlight(query)
}

// MacScreenshot captures a screenshot via `screencapture`. `target` must
// be one of "screen" | "window" | "selection". Writes the PNG to
// ~/.jarvis/screenshots/<unix>.png and returns the absolute path. Policy
// gate: mac_screenshot (defaults to allow).
func (a *App) MacScreenshot(target string) (string, error) {
	return a.macctl().Screenshot(target)
}

// ---------------------------------------------------------------------------
// Clipboard
// ---------------------------------------------------------------------------

// MacClipboardGet returns the current clipboard text via `pbpaste`.
// Returns "" + nil when the clipboard is empty. Policy gate:
// mac_clipboard_get (defaults to allow).
func (a *App) MacClipboardGet() (string, error) {
	return a.macctl().ClipboardGet()
}

// MacClipboardSet writes `text` to the clipboard via `pbcopy`. Destructive
// (silently overwrites the user's previous clipboard). Policy gate:
// mac_clipboard_set.
func (a *App) MacClipboardSet(text string) (string, error) {
	return a.macctl().ClipboardSet(text)
}

// ---------------------------------------------------------------------------
// Shortcuts
// ---------------------------------------------------------------------------

// MacListShortcuts returns the names of every user-installed macOS
// Shortcut via `shortcuts list --output-format json`. Read-only. Policy
// gate: mac_list_shortcuts (defaults to allow).
//
// Returns []string{} (never nil) on the empty case so the Wails JSON
// serializer emits `[]` rather than `null` to the frontend.
func (a *App) MacListShortcuts() ([]string, error) {
	return a.macctl().ListShortcuts()
}

// MacRunShortcut runs the named Shortcut, optionally piping `input` to
// its stdin. Returns the shortcut's stdout (trimmed). Policy gate:
// mac_run_shortcut.
func (a *App) MacRunShortcut(name, input string) (string, error) {
	return a.macctl().RunShortcut(name, input)
}

// ---------------------------------------------------------------------------
// Policy management (Settings UI -- TASK-017)
// ---------------------------------------------------------------------------

// GetMacctlPolicy returns the current per-tool permission decisions as a
// plain map[string]string for trivial Wails serialization. The keys are
// canonical tool names (e.g. "mac_open_app", "spotify_pause") and the
// values are the wire-format decision strings ("allow" | "ask" | "deny").
//
// Snapshots the policy under its internal RWMutex so a concurrent Set
// from another goroutine cannot interleave with the marshalling. Returns
// an empty (but non-nil) map when no decisions are configured -- the
// Settings UI relies on the non-nil contract to render an empty table
// rather than crash on a null prop.
func (a *App) GetMacctlPolicy() map[string]string {
	policy := a.macctl().Policy()
	if policy == nil {
		return map[string]string{}
	}
	// Snapshot under the policy's read lock to avoid racing a concurrent
	// Set from another goroutine. The Policy.Snapshot helper acquires the
	// same RLock that Save() uses, so the returned map is a safe,
	// independently-mutable copy of the in-memory state.
	snap := policy.Snapshot()
	out := make(map[string]string, len(snap))
	for tool, decision := range snap {
		out[tool] = string(decision)
	}
	return out
}

// SetMacctlPolicy mutates a single tool's permission decision and
// persists the full policy to disk via macctl.Policy.Save.
//
// `decision` must be one of "allow" | "ask" | "deny" -- invalid values
// are rejected with an error rather than silently coerced, because the
// Settings UI calls this from a typed segmented control where an invalid
// value indicates a frontend bug we want to surface, not paper over.
//
// Persistence is synchronous so the Settings UI can rely on "Save"
// completing before navigating away -- callers do not have to coordinate
// an additional flush.
func (a *App) SetMacctlPolicy(tool, decision string) error {
	if tool == "" {
		return fmt.Errorf("SetMacctlPolicy: tool is required")
	}
	d := macctl.Decision(decision)
	if !d.IsValid() {
		return fmt.Errorf("SetMacctlPolicy: invalid decision %q (must be allow|ask|deny)", decision)
	}
	policy := a.macctl().Policy()
	if policy == nil {
		return fmt.Errorf("SetMacctlPolicy: policy unavailable")
	}
	policy.Set(tool, d)
	if err := policy.Save(macctl.PolicyPath()); err != nil {
		return fmt.Errorf("SetMacctlPolicy: %w", err)
	}
	return nil
}
