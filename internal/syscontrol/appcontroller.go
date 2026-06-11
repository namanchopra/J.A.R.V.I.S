// Package syscontrol defines platform-agnostic interfaces for system-wide
// host control — app launch/focus, audio/display tunables, clipboard I/O,
// and screenshots — that the Jarvis daemon's LLM can invoke as tools via
// the daemon ⇄ Wails tool bridge.
//
// The package itself is interface-only: it pulls in NO platform-specific
// implementations (no AppleScript, no PowerShell, no Win32). The macOS
// backend lives in github.com/namanchopra/jarvis/internal/macctl and its
// *macctl.Controller satisfies every interface declared here (compile-time
// asserted in macctl_iface_darwin.go). Future Windows / Linux backends will
// live in sibling packages (e.g. internal/syscontrol/<impl>_windows.go) or
// in dedicated packages (the Phase 2 plan creates *_windows.go files
// directly in this package per TASK-020..TASK-029).
//
// The decoupling lets callers depend on the small Controller interfaces
// they actually use rather than on the full *macctl.Controller surface,
// and lets Phase 2's Windows port supply per-platform implementations
// without modifying macctl's macOS-only AppleScript code.
//
// On a platform without an implementation, callers should surface
// ErrUnsupportedPlatform rather than panicking. Each concrete backend
// returns this sentinel (wrapped with the method name) from any method
// that has no platform equivalent — see for instance TASK-012's screen
// capture stub on Windows for the wrap-with-context pattern.
package syscontrol

import "errors"

// ErrUnsupportedPlatform is the canonical sentinel for "this system
// control feature has no implementation on the current GOOS". Returned
// (wrapped via fmt.Errorf("MethodName: %w", ErrUnsupportedPlatform)) by
// platform stubs so callers can distinguish "feature not built yet on
// this OS" from a genuine failure on a supported OS without parsing
// error strings.
//
// Use errors.Is(err, syscontrol.ErrUnsupportedPlatform) at the call
// site to detect this case and route to a user-facing "not yet
// available on Windows" / "not yet available on Linux" message
// instead of crashing or surfacing a generic exec error.
var ErrUnsupportedPlatform = errors.New("syscontrol: not supported on this platform")

// AppController controls application launch, termination, and window
// focus. Implementations gate destructive operations through their own
// policy layer (see internal/macctl/policy.go for the macOS reference)
// before performing any side effect.
//
// All methods return a status string (typically empty on success) plus
// an error. The status-string return is preserved to match the existing
// *macctl.Controller signatures so a one-line delegation suffices on
// macOS — and so a future Windows / Linux implementation can surface
// additional context (e.g. "fell back to cmd.exe because Windows
// Terminal is not installed") without changing the interface.
type AppController interface {
	// OpenApp activates (or launches) the named application. On macOS
	// this shells `open -a <name>`; on Windows TASK-020 shells
	// `Start-Process`. An empty name MUST be rejected with a wrapped
	// error rather than silently no-oping — a stray voice misfire
	// shouldn't launch a random app.
	OpenApp(name string) (string, error)

	// QuitApp asks the named application to terminate gracefully. On
	// macOS this is `tell application <name> to quit`; on Windows
	// TASK-020 uses `Stop-Process`. Quitting a non-running app MUST
	// return a non-nil error (not silently succeed) so the daemon
	// can surface "the app wasn't running" to the user.
	QuitApp(name string) (string, error)

	// FocusWindow brings a specific window of `app` whose title
	// contains `title` to the foreground. Implementations MUST return
	// a sentinel "not found" error (see internal/macctl.ErrWindowNotFound
	// for the macOS analogue) when no window matches, so callers can
	// offer a "did you mean ..." style fallback rather than surfacing
	// a raw OS error. An empty title means "activate the app and let
	// its frontmost window win".
	FocusWindow(app, title string) (string, error)
}
