package main

// app_overlay.go — v0.3.0 TASK-004: window-morph Wails bindings.
//
// The Wails app is a single-window process. To produce an "always-on-top
// overlay" surface without writing a CGO bridge for a second NSWindow, we
// morph the existing main window: shrink it to an 180x180 always-on-top
// box anchored to a screen corner on OverlayShow, then restore the saved
// geometry on OverlayHide. The React side swaps its rendered tree on the
// "overlay:mode" event ("overlay" vs "hud") emitted at the end of each
// transition (TASK-008).
//
// Boundaries:
//   - This file does NOT register a global hotkey -- TASK-005 (internal/hotkey)
//     calls into these bindings on key press / release.
//   - This file does NOT touch the frontend -- the React mode-switch in
//     App.tsx is TASK-008.
//   - "last-dragged" position persistence is deferred; OverlayShow currently
//     falls back to "top-right" for that value (see comment below).
//   - We deliberately do NOT set NSWindowCollectionBehaviorCanJoinAllSpaces;
//     multi-space visibility was explicitly out-of-scope per the plan.
//
// State location: the saved geometry lives on *App (overlayMu + overlayState
// fields, defined in app.go). The struct itself, overlayGeometry, is local
// to this file because no other package reads it -- it's a pure
// implementation detail of the show/hide round-trip.
//
// Runtime abstraction: the bindings call through a windowRuntime interface
// rather than directly into github.com/wailsapp/wails/v2/pkg/runtime so the
// unit tests can substitute a recorder. The production implementation is
// prodWindowRuntime which forwards each call to the matching runtime.Window*
// function with the App's ctx. The package-level overlayRuntimeFn variable
// is the swap point -- tests overwrite it in t.Cleanup-protected setUp helpers.

import (
	"fmt"
	"log/slog"

	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/macctl"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// overlayWidth / overlayHeight are the logical-pixel dimensions of the
// morphed overlay window. Portrait by design now: the layout stacks an
// orb on top, a state line, and a row of controls (PTT / mute / interrupt)
// at the bottom. Iterated 180x180 -> 280x280 -> 320x420 after first-pass
// UX feedback (orb too small + needs in-overlay controls + personality).
const (
	overlayWidth  = 320
	overlayHeight = 420
)

// overlayGutter is the inset (in logical pixels) from each screen edge.
// 20 px is enough to clear the macOS menu bar at the top and matches the
// gutter used by other native overlay widgets (e.g. Raycast quicklook).
const overlayGutter = 20

// overlayGeometry is the snapshot captured by OverlayShow so OverlayHide
// can restore the main window exactly. The zero value (saved == false)
// is the documented "no prior show" sentinel: OverlayHide treats it as a
// soft no-op and OverlayToggle treats it as "overlay not active, show it".
//
// wasFullscreen records whether the window was in macOS fullscreen at
// the moment of OverlayShow. We can't simply WindowSetSize on a fullscreen
// window (the size change is silently ignored on macOS), so OverlayShow
// calls WindowUnfullscreen first and stores the flag here. OverlayHide
// does NOT automatically restore fullscreen -- restoring fullscreen would
// nuke the user's overlay-era app layout. The flag exists for diagnostics
// and future "remember fullscreen on hide" toggles (not in v1).
type overlayGeometry struct {
	saved         bool
	x, y          int
	w, h          int
	wasFullscreen bool
}

// windowRuntime is the test seam for the Wails runtime calls used by the
// overlay bindings. Production code uses prodWindowRuntime (which forwards
// each call to runtime.Window*); tests use a fake recorder that captures
// calls for assertion. Keep this interface small -- only the methods we
// actually use, so the test fake stays trivially mockable.
//
// Note: ScreenGetAll returns a slice of runtime.Screen for both production
// and tests; defining our own Screen type would force a conversion at the
// boundary for no real benefit. The test fake just returns a hand-crafted
// []runtime.Screen.
type windowRuntime interface {
	WindowGetSize() (int, int)
	WindowGetPosition() (int, int)
	WindowSetSize(width, height int)
	WindowSetPosition(x, y int)
	WindowSetAlwaysOnTop(b bool)
	WindowIsFullscreen() bool
	WindowUnfullscreen()
	WindowUnminimise()
	WindowShow()
	ScreenGetAll() ([]wailsruntime.Screen, error)
	EventsEmit(eventName string, optionalData ...interface{})
}

// prodWindowRuntime is the production windowRuntime backed by Wails'
// real runtime package. Bound to the App's ctx at construction time so
// the bindings don't have to thread it through each call.
type prodWindowRuntime struct {
	a *App
}

func (p prodWindowRuntime) WindowGetSize() (int, int) {
	return wailsruntime.WindowGetSize(p.a.ctx)
}

func (p prodWindowRuntime) WindowGetPosition() (int, int) {
	return wailsruntime.WindowGetPosition(p.a.ctx)
}

func (p prodWindowRuntime) WindowSetSize(width, height int) {
	wailsruntime.WindowSetSize(p.a.ctx, width, height)
}

func (p prodWindowRuntime) WindowSetPosition(x, y int) {
	wailsruntime.WindowSetPosition(p.a.ctx, x, y)
}

func (p prodWindowRuntime) WindowSetAlwaysOnTop(b bool) {
	wailsruntime.WindowSetAlwaysOnTop(p.a.ctx, b)
}

func (p prodWindowRuntime) WindowIsFullscreen() bool {
	return wailsruntime.WindowIsFullscreen(p.a.ctx)
}

func (p prodWindowRuntime) WindowUnfullscreen() {
	wailsruntime.WindowUnfullscreen(p.a.ctx)
}

func (p prodWindowRuntime) WindowUnminimise() {
	wailsruntime.WindowUnminimise(p.a.ctx)
}

func (p prodWindowRuntime) WindowShow() {
	wailsruntime.WindowShow(p.a.ctx)
}

func (p prodWindowRuntime) ScreenGetAll() ([]wailsruntime.Screen, error) {
	return wailsruntime.ScreenGetAll(p.a.ctx)
}

func (p prodWindowRuntime) EventsEmit(eventName string, optionalData ...interface{}) {
	wailsruntime.EventsEmit(p.a.ctx, eventName, optionalData...)
}

// overlayRuntimeFn returns the windowRuntime to use for a given App. In
// production this is always prodWindowRuntime{a}. Tests overwrite the
// package-level variable inside a t.Cleanup-protected helper so the
// fake-recorder runtime is used for the duration of one test only.
// Mirrors the seam pattern used by setupSpawnerFn / setupSubscribeFn
// elsewhere in this package.
var overlayRuntimeFn = func(a *App) windowRuntime {
	return prodWindowRuntime{a: a}
}

// overlayConfigFn returns the current Config used to look up
// OverlayPosition. Indirected so tests can supply a Config without
// touching the on-disk file or the package-level config singleton.
// Production reads from config.Get() which serves the in-memory cached
// copy populated by Load() at startup -- avoids a disk re-read on every
// OverlayShow call.
var overlayConfigFn = func() *config.Config {
	return config.Get()
}

// ---------------------------------------------------------------------------
// Bindings
// ---------------------------------------------------------------------------

// OverlayShow morphs the main Wails window into an overlay-sized,
// always-on-top window anchored to the configured screen corner.
//
// The current window size + position + fullscreen flag are saved on the
// App (mutex-protected) so OverlayHide can restore them exactly. If the
// overlay is already active (i.e. a prior OverlayShow has populated the
// saved geometry and no OverlayHide has fired in between), this method
// logs a debug message and no-ops -- double-save would lose the original
// geometry and brick the restore path.
//
// Emits the "overlay:mode" Wails event with payload "overlay" once the
// window transition is complete so the React side can swap to
// <OverlayView /> (TASK-008).
//
// Returns nil on success. Returns nil on the "already active" no-op
// branch too (idempotent from the caller's perspective). The only error
// path is reserved for future config-load failures; the v1 implementation
// always succeeds because overlayConfigFn never errors (it returns the
// cached Config or DefaultConfig).
func (a *App) OverlayShow() error {
	// ----- Phase 1: under the lock, check + record state -----
	a.overlayMu.Lock()
	if a.overlayState.saved {
		a.overlayMu.Unlock()
		slog.Debug("OverlayShow: overlay already active, no-op")
		return nil
	}
	a.overlayMu.Unlock()

	rt := overlayRuntimeFn(a)

	// Read the current window state *before* mutating it. WindowIsFullscreen
	// is queried first because the subsequent WindowGetSize/WindowGetPosition
	// would otherwise report the fullscreen-era dimensions which we wouldn't
	// want to restore to.
	wasFullscreen := rt.WindowIsFullscreen()
	if wasFullscreen {
		// macOS silently ignores WindowSetSize while fullscreen, so the
		// morph itself wouldn't take effect. Unfullscreen first; the
		// resulting (un-fullscreened) geometry is what we capture.
		rt.WindowUnfullscreen()
	}

	curW, curH := rt.WindowGetSize()
	curX, curY := rt.WindowGetPosition()

	// Compute target overlay position from configured corner + primary
	// screen bounds. Failures here fall back to (overlayGutter, overlayGutter)
	// i.e. top-left as the safest visible position -- never block the show.
	pos := overlayConfigFn().OverlayPosition
	targetX, targetY := computeOverlayPosition(rt, pos)

	// ----- Phase 2: persist saved geometry under the lock -----
	a.overlayMu.Lock()
	a.overlayState = overlayGeometry{
		saved:         true,
		x:             curX,
		y:             curY,
		w:             curW,
		h:             curH,
		wasFullscreen: wasFullscreen,
	}
	a.overlayMu.Unlock()

	// ----- Phase 3: apply the morph (lock released; runtime calls can be slow) -----
	// Strip Mac chrome FIRST so the resulting size doesn't include the
	// title-bar height that would otherwise eat into the orb area.
	// SetMainWindowFrameless also calls activateIgnoringOtherApps which
	// brings Jarvis to the foreground -- required when the global hotkey
	// fires from another app and the previously-focused app would
	// otherwise keep keyboard focus.
	macctl.SetMainWindowFrameless(true)
	rt.WindowSetSize(overlayWidth, overlayHeight)
	rt.WindowSetPosition(targetX, targetY)
	rt.WindowSetAlwaysOnTop(true)
	rt.WindowUnminimise()
	rt.WindowShow()
	rt.EventsEmit("overlay:mode", "overlay")

	slog.Info("OverlayShow: morphed to overlay",
		"from", fmt.Sprintf("%dx%d@%d,%d", curW, curH, curX, curY),
		"to", fmt.Sprintf("%dx%d@%d,%d", overlayWidth, overlayHeight, targetX, targetY),
		"corner", pos,
		"wasFullscreen", wasFullscreen,
	)
	return nil
}

// OverlayHide restores the saved window geometry, unsets always-on-top,
// and emits "overlay:mode" with payload "hud".
//
// Failure case: if OverlayShow has never been called (saved geometry is
// the zero value), this method logs a warning via slog.Warn and returns
// nil without emitting any event or making any runtime calls. This is
// the documented "double-hide" / "hide-before-show" safety branch --
// crashing here would be a bad UX if the user holds the hotkey twice
// in rapid succession and the second release races the first.
func (a *App) OverlayHide() error {
	// Snapshot + clear the saved geometry under the lock, then release the
	// lock before any runtime calls.
	a.overlayMu.Lock()
	state := a.overlayState
	if !state.saved {
		a.overlayMu.Unlock()
		slog.Warn("OverlayHide: no saved geometry (OverlayShow was never called), no-op")
		return nil
	}
	// Clear before the runtime calls so a concurrent OverlayShow sees an
	// empty slot and proceeds (rather than no-op'ing because we hadn't
	// cleared yet). Restoration is independent of the saved-flag now that
	// we've snapshotted into `state`.
	a.overlayState = overlayGeometry{}
	a.overlayMu.Unlock()

	rt := overlayRuntimeFn(a)
	rt.WindowSetAlwaysOnTop(false)
	// Restore the Mac chrome BEFORE resizing back so the title-bar area
	// is accounted for in the final geometry. SetMainWindowFrameless(false)
	// also un-activates the special foreground state from OverlayShow.
	macctl.SetMainWindowFrameless(false)
	rt.WindowSetSize(state.w, state.h)
	rt.WindowSetPosition(state.x, state.y)
	rt.EventsEmit("overlay:mode", "hud")

	slog.Info("OverlayHide: restored HUD",
		"to", fmt.Sprintf("%dx%d@%d,%d", state.w, state.h, state.x, state.y),
	)
	return nil
}

// OverlayToggle flips between overlay and HUD based on whether the saved
// geometry is currently populated. Equivalent to OverlayShow when no
// overlay is active, OverlayHide when one is. Useful for the future
// Settings UI "open overlay" button (TASK-009) and for keyboard users
// who'd rather tap a hotkey than press-and-hold.
func (a *App) OverlayToggle() error {
	a.overlayMu.Lock()
	active := a.overlayState.saved
	a.overlayMu.Unlock()

	if active {
		return a.OverlayHide()
	}
	return a.OverlayShow()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// computeOverlayPosition returns the (x, y) coordinates for an
// overlayDimension x overlayDimension box anchored to the screen corner
// indicated by `corner`. The reference screen is the primary screen
// reported by runtime.ScreenGetAll (falling back to screens[0] if no
// screen has IsPrimary=true). If the runtime returns an error or no
// screens at all, we fall back to (overlayGutter, overlayGutter) --
// always a visible top-left position that won't strand the overlay
// off-screen.
//
// Unknown corner values fall through to "top-right" to mirror the
// frontend's behaviour per the plan ("Unknown values are tolerated on
// load; the frontend treats them as 'top-right'."). "last-dragged" also
// falls back to top-right in v1; persisting drag positions is deferred.
func computeOverlayPosition(rt windowRuntime, corner string) (int, int) {
	screens, err := rt.ScreenGetAll()
	if err != nil || len(screens) == 0 {
		slog.Warn("computeOverlayPosition: ScreenGetAll failed, using safe fallback",
			"err", err, "screensReturned", len(screens))
		return overlayGutter, overlayGutter
	}

	// Pick the primary screen if any reports IsPrimary, otherwise screens[0].
	// Some platforms / Wails versions don't reliably populate IsPrimary, so
	// this fallback is load-bearing -- not just defensive.
	screen := screens[0]
	for _, s := range screens {
		if s.IsPrimary {
			screen = s
			break
		}
	}

	// Pull width / height. Wails' Screen has both deprecated Width/Height
	// and a newer Size struct; prefer Size when populated to be forward-
	// compatible, fall back to Width/Height otherwise.
	w, h := screen.Width, screen.Height
	if screen.Size.Width > 0 {
		w = screen.Size.Width
	}
	if screen.Size.Height > 0 {
		h = screen.Size.Height
	}

	// Defensive: if neither field is populated, return the safe fallback.
	if w <= 0 || h <= 0 {
		slog.Warn("computeOverlayPosition: screen has no usable size, using safe fallback",
			"width", w, "height", h)
		return overlayGutter, overlayGutter
	}

	switch corner {
	case "top-left":
		return overlayGutter, overlayGutter
	case "bottom-right":
		return w - overlayWidth - overlayGutter, h - overlayHeight - overlayGutter
	case "bottom-left":
		return overlayGutter, h - overlayHeight - overlayGutter
	case "last-dragged":
		// Deferred: see file-level comment. Fall through to top-right so
		// the user gets a sensible visible position rather than a no-op.
		fallthrough
	case "top-right":
		fallthrough
	default:
		return w - overlayWidth - overlayGutter, overlayGutter
	}
}
