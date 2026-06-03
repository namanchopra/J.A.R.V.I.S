package main

// app_hotkey.go — v0.3.0 TASK-005: Wails bindings for the global PTT hotkey.
//
// Three bindings:
//   - RebindOverlayHotkey(spec) — unregister current binding, register the
//     new spec, persist to config. Emits "overlay:hotkey_error" on any
//     failure so the Settings panel can show its "grant Accessibility" CTA.
//   - OverlayPTTPress  — invoked by the hotkey on key-down. Shows the
//     overlay (TASK-004 binding) and notifies the daemon that the PTT
//     gate is opening (TASK-006 helper). Lenient: a missing daemon
//     connection still shows the overlay (UI handles the disconnected
//     state on its own).
//   - OverlayPTTRelease — invoked on key-up. Notifies the daemon to close
//     the PTT gate. Per design we do NOT auto-hide the overlay; the user
//     closes it manually (TASK-008 close button or OverlayToggle).
//
// Wiring: main.go's OnStartup constructs a hotkey.Manager, stores it on
// the App, and calls Register with closures that swallow + log the
// errors from OverlayPTTPress / OverlayPTTRelease. The hotkey package's
// callbacks are typed `func()` (no error) so the wiring layer absorbs the
// error-returning binding shape there. That keeps the hotkey package's
// surface clean and matches the design from TASK-002's notes.

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/namanchopra/jarvis/internal/config"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// RebindOverlayHotkey unregisters the current global hotkey and registers a
// new one. The new spec is persisted to ~/.jarvis/config.json so the
// binding survives a restart.
//
// Failure paths (all emit "overlay:hotkey_error" on the Wails event bus
// before returning the wrapped error):
//   - empty spec — rejected up front
//   - manager not initialised (startup never ran the hotkey wiring)
//   - hotkey.Manager.Register failure (parse error or OS denial)
//   - config load / save failure
//
// On success the manager is now armed with the new spec and the config
// file has been written. No event is emitted on the happy path — the
// Settings panel observes success by the absence of an error response on
// the Wails JS call.
func (a *App) RebindOverlayHotkey(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		err := fmt.Errorf("RebindOverlayHotkey: spec is empty")
		a.emitHotkeyError(err)
		return err
	}
	if a.hotkeyManager == nil {
		err := fmt.Errorf("RebindOverlayHotkey: hotkey manager not initialised")
		a.emitHotkeyError(err)
		return err
	}

	// Register first so a parse / OS-denial error doesn't corrupt config
	// with a spec the OS will reject. The Manager's Register is idempotent
	// (it Unregisters the prior binding before swapping in the new one),
	// so a failure here leaves the previous spec still active.
	if err := a.hotkeyManager.Register(
		spec,
		a.hotkeyPressCallback(),
		a.hotkeyReleaseCallback(),
	); err != nil {
		slog.Warn("RebindOverlayHotkey: register failed", "spec", spec, "err", err)
		a.emitHotkeyError(err)
		return fmt.Errorf("RebindOverlayHotkey: %w", err)
	}

	// Persist. Load → mutate → save. If save fails the in-memory binding
	// is still the new one (Register has already succeeded) but the next
	// app start will use the old spec — surface the error so the user
	// knows the change wasn't durable.
	cfg, err := config.Load()
	if err != nil {
		wrapped := fmt.Errorf("RebindOverlayHotkey: load config: %w", err)
		a.emitHotkeyError(wrapped)
		return wrapped
	}
	cfg.OverlayHotkey = spec
	if err := config.Save(cfg); err != nil {
		wrapped := fmt.Errorf("RebindOverlayHotkey: save config: %w", err)
		a.emitHotkeyError(wrapped)
		return wrapped
	}
	slog.Info("RebindOverlayHotkey: rebound", "spec", spec)
	return nil
}

// OverlayPTTPress is invoked by the global hotkey on key-down. It shows
// the overlay window and tells the daemon to open its STT gate.
//
// Lenient on missing daemon: the overlay is still shown so the user has
// visible feedback that the hotkey worked; the disconnected-daemon path
// is the UI's problem to communicate (the overlay renders the "idle"
// orb state when no daemon is connected — see TASK-008).
//
// Errors from OverlayShow are propagated (a window-morph failure is
// genuinely worth surfacing). Errors from SendPTTActive are propagated
// too: the overlay is open but the audio gate is closed, which is a
// state worth seeing in logs.
func (a *App) OverlayPTTPress() error {
	if err := a.OverlayShow(); err != nil {
		return fmt.Errorf("OverlayPTTPress: show: %w", err)
	}
	conn := a.jarvisDaemonConn()
	if conn == nil || !conn.Connected() {
		slog.Warn("OverlayPTTPress: daemon not connected — overlay shown but no audio")
		return nil // soft path; the overlay UI handles the disconnected state
	}
	if err := conn.SendPTTActive(); err != nil {
		return fmt.Errorf("OverlayPTTPress: send ptt_active: %w", err)
	}
	return nil
}

// OverlayPTTRelease is invoked on key-up. Notifies the daemon to close
// the PTT gate. Does NOT auto-hide the overlay — manual close per design.
//
// Lenient on missing daemon: a key-up with no connected daemon is a no-op
// rather than an error. This matches OverlayPTTPress's soft path: if the
// daemon was offline at press-time, there's nothing to release at up-time.
func (a *App) OverlayPTTRelease() error {
	conn := a.jarvisDaemonConn()
	if conn == nil || !conn.Connected() {
		// Matches the lenient OverlayPTTPress behaviour. The daemon being
		// offline at key-up time is symmetric with it being offline at
		// key-down time; there is no extra error to report.
		return nil
	}
	if err := conn.SendPTTRelease(); err != nil {
		return fmt.Errorf("OverlayPTTRelease: send ptt_release: %w", err)
	}
	return nil
}

// hotkeyPressCallback returns the closure the global hotkey fires on
// key-down. After the second-pass UX revision the global hotkey TOGGLES
// the overlay (open if hidden, hide if open) rather than driving PTT.
// PTT is now bound to Space inside the overlay window — see
// frontend/src/views/OverlayView.tsx for that keydown handler. Separation
// keeps "open the overlay" and "talk to Jarvis" as distinct gestures.
//
// The closure captures `a` so it follows the App's lifetime — when the
// App is GC'd (i.e. on process shutdown after main.go's OnShutdown
// closes the manager), no dangling references remain.
func (a *App) hotkeyPressCallback() func() {
	return func() {
		if err := a.OverlayToggle(); err != nil {
			slog.Error("hotkey press handler (toggle)", "err", err)
		}
	}
}

// hotkeyReleaseCallback is a deliberate no-op after the UX revision: the
// global hotkey is a single-edge toggle, not a hold-to-talk binding. The
// hotkey package's Manager always invokes both press and release callbacks
// per cycle, so we still pass one — it just does nothing. A long hold
// therefore opens (or closes) the overlay once on key-down and the
// release does nothing observable.
func (a *App) hotkeyReleaseCallback() func() {
	return func() {}
}

// hotkeyPTTPressCallback returns the closure fired on the PTT hotkey's
// key-down event (default ctrl+space). It calls OverlayPTTPress which
// shows the overlay (if hidden, for visual feedback) AND sends the
// ptt_active control frame to the daemon. Unlike the toggle hotkey
// above, this is the canonical "start talking" gesture and works from
// any app — the user doesn't need to focus the overlay window first.
func (a *App) hotkeyPTTPressCallback() func() {
	return func() {
		if err := a.OverlayPTTPress(); err != nil {
			slog.Error("PTT hotkey press handler", "err", err)
		}
	}
}

// hotkeyPTTReleaseCallback fires on key-up of the PTT hotkey. Sends
// ptt_release to the daemon, which finalises the transcription and
// triggers the LLM turn. The overlay stays visible after release so the
// user can see Jarvis's speaking-state animation while it responds.
func (a *App) hotkeyPTTReleaseCallback() func() {
	return func() {
		if err := a.OverlayPTTRelease(); err != nil {
			slog.Error("PTT hotkey release handler", "err", err)
		}
	}
}

// emitHotkeyError publishes "overlay:hotkey_error" on the Wails event bus
// so the Settings panel (TASK-009) can surface the failure (e.g. the
// "grant Accessibility access" CTA). Safe to call before a.ctx has been
// assigned: a nil context falls through to the runtime as a no-op rather
// than panicking, but we guard explicitly to keep the slog trail clean.
func (a *App) emitHotkeyError(err error) {
	if a == nil || a.ctx == nil || err == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "overlay:hotkey_error", err.Error())
}
