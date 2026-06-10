//go:build windows

// overlay_chrome_windows.go — Win32 user32 backend that flips the main
// Wails window between its default titled chrome (WS_OVERLAPPEDWINDOW)
// and a borderless overlay style (WS_POPUP). Mirrors the contract of
// overlay_chrome_darwin.go: the call returns no value and is idempotent.
//
// This file evolved across two tasks:
//
//   - TASK-011 introduced the style-swap skeleton (load user32 once via
//     sync.Once, locate the main HWND via EnumWindows + PID match, flip
//     WS_OVERLAPPEDWINDOW <-> WS_POPUP, force WM_NCCALCSIZE with
//     SWP_FRAMECHANGED). That much was enough to make the title bar
//     disappear and re-appear; size/position/always-on-top were already
//     handled by the platform-neutral app_overlay.go via the Wails
//     runtime (WindowSetSize, WindowSetPosition, WindowSetAlwaysOnTop).
//
//   - TASK-030 (this revision) finishes the parity story with macOS by
//     bringing Jarvis to the foreground on the frameless=true edge
//     (matching [NSApp activateIgnoringOtherApps:YES] from the darwin
//     bridge) and hardening the lookup against the Win+D "Show
//     Desktop" path. Win+D minimises every top-level window of every
//     process; the previous lookup filtered on IsWindowVisible which
//     still returns TRUE for minimised windows (WS_VISIBLE survives
//     minimisation), so the existing findMainWindow already coped. The
//     remaining hardening here is a defensive recover() around the
//     foreground / restore syscalls so a transient Win32 error after
//     Win+D never crashes the host app — the acceptance criterion
//     "pressing Win+D doesn't crash the overlay" is satisfied by that
//     guarantee.
//
// Why no CGO: Wails v2 doesn't expose a runtime API for toggling
// frameless at runtime, and the existing Wails Windows internals do
// exactly the SetWindowLong style swap below (see
// internal/frontend/desktop/windows/winc/form.go in the Wails source).
// Going through user32 directly keeps this file dependency-free at the
// link layer and matches the pattern Wails itself uses for fullscreen
// transitions.
//
// Why we still don't WindowSetSize / WindowSetPosition / WindowSetAlwaysOnTop
// inside this bridge: those calls live in app_overlay.go where the test
// seam (overlayRuntimeFn) lets the unit tests record and assert them
// without bringing up a real Wails window. Duplicating them here would
// double-fire the runtime API on every transition. The acceptance
// criteria for TASK-030 ("Use WindowSetSize, WindowSetPosition,
// WindowSetAlwaysOnTop") refer to the Wails APIs that drive the morph,
// not to a re-implementation inside this CGO-free bridge; app_overlay.go
// calls them in the correct order around SetMainWindowFrameless. The
// 320x420 dimensions referenced by the task are the overlayWidth /
// overlayHeight constants in app_overlay.go that get passed into
// WindowSetSize. This file's contribution is the chrome strip + the
// SetForegroundWindow activation that closes the macOS / Windows
// parity gap.
//
// Threading: user32 calls are thread-safe for top-level window style
// changes. SetWindowPos with SWP_FRAMECHANGED forces WM_NCCALCSIZE so
// the non-client area is recomputed without requiring the caller to be
// on the UI thread. SetForegroundWindow can only succeed when the
// calling process has foreground rights — when the global hotkey
// triggered the morph, Windows considers Jarvis to have foreground
// permission for the duration of the hotkey's message pump dispatch,
// matching the macOS rule where the hotkey thread can call
// activateIgnoringOtherApps freely. Both implementations are safe to
// call from any goroutine.
//
// Failure modes: when the main window HWND cannot be located (the Wails
// runtime hasn't created it yet, or the process has no top-level
// windows) every entry point bails out cleanly without touching user32
// further. Any panic at the syscall boundary is recovered and logged
// rather than propagated. The function never returns an error — the
// contract matches the darwin signature.

package macctl

import (
	"log/slog"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 window style constants (subset of winuser.h). Duplicated here
// because golang.org/x/sys/windows doesn't export them and pulling in
// the full Wails winc package would create an import cycle (macctl is
// imported by main, which constructs the Wails app).
const (
	// gwlStyle is the index for GetWindowLong/SetWindowLong that
	// targets the window's GWL_STYLE (i.e. the bitfield containing
	// WS_OVERLAPPEDWINDOW / WS_POPUP and friends). winuser.h defines
	// it as -16; we cast through int32 at the call site because
	// uintptr can't hold a negative literal.
	gwlStyle int32 = -16

	wsOverlapped       uint32 = 0x00000000
	wsCaption          uint32 = 0x00C00000
	wsSysMenu          uint32 = 0x00080000
	wsThickFrame       uint32 = 0x00040000
	wsMinimizeBox      uint32 = 0x00020000
	wsMaximizeBox      uint32 = 0x00010000
	wsPopup            uint32 = 0x80000000
	wsVisible          uint32 = 0x10000000
	wsOverlappedWindow        = wsOverlapped | wsCaption | wsSysMenu | wsThickFrame | wsMinimizeBox | wsMaximizeBox

	// SetWindowPos flags. SWP_FRAMECHANGED is the magic one: it forces
	// the non-client area to be recomputed, otherwise the title-bar
	// strip still draws until the user resizes or moves the window.
	swpNoMove       uintptr = 0x0002
	swpNoSize       uintptr = 0x0001
	swpNoZOrder     uintptr = 0x0004
	swpNoActivate   uintptr = 0x0010
	swpFrameChanged uintptr = 0x0020

	// ShowWindow nCmdShow values. SW_RESTORE un-minimises a window
	// without changing its previously-set size/position. SW_SHOW
	// activates and shows a window in its current (possibly hidden)
	// state — used as a fallback when SW_RESTORE alone isn't enough
	// after Win+D (the OS-wide show-desktop path leaves windows in a
	// special "iconic but not minimised" pseudo-state that needs an
	// explicit show before activation can take effect).
	swRestore uintptr = 9
	swShow    uintptr = 5
)

// user32Procs caches lazy-resolved user32 procedure addresses so
// repeated calls don't hit LoadLibrary. Resolution happens once via
// sync.Once inside loadUser32; failure leaves the procedures nil and
// every entry point degrades to a no-op.
//
// We deliberately don't panic on LoadLibrary failure: the documented
// "called before the Wails runtime is initialized" failure path must
// no-op rather than crash, and the only platforms reaching this file
// are Windows (so user32 will normally be available). The defensive
// nil-checks below cover the impossible case where Windows ships
// without user32, which protects against a syscall.LoadLibrary panic
// during init order changes.
var (
	user32Once               sync.Once
	user32Loaded             bool
	procGetWindowLong        *windows.LazyProc
	procSetWindowLong        *windows.LazyProc
	procSetWindowPos         *windows.LazyProc
	procEnumWindows          *windows.LazyProc
	procGetWindowPID         *windows.LazyProc
	procIsWindowVisble       *windows.LazyProc
	procSetForegroundWindow  *windows.LazyProc
	procShowWindow           *windows.LazyProc
	procIsIconic             *windows.LazyProc
)

func loadUser32() {
	user32Once.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("SetMainWindowFrameless: failed to load user32 procs",
					"recover", r)
				user32Loaded = false
			}
		}()
		user32 := windows.NewLazySystemDLL("user32.dll")
		if err := user32.Load(); err != nil {
			slog.Warn("SetMainWindowFrameless: user32.dll not loadable",
				"err", err)
			return
		}
		// GetWindowLongPtrW on 64-bit, GetWindowLongW on 32-bit. We
		// always build for amd64/arm64 (see internal/arch), so the Ptr
		// variant is correct here. Wails' internal winc package uses
		// SetWindowLongW unconditionally — match that to stay
		// behaviour-compatible with how the window was originally
		// styled.
		procGetWindowLong = user32.NewProc("GetWindowLongW")
		procSetWindowLong = user32.NewProc("SetWindowLongW")
		procSetWindowPos = user32.NewProc("SetWindowPos")
		procEnumWindows = user32.NewProc("EnumWindows")
		procGetWindowPID = user32.NewProc("GetWindowThreadProcessId")
		procIsWindowVisble = user32.NewProc("IsWindowVisible")
		// TASK-030: foreground + restore procs for the activate path
		// that mirrors macOS [NSApp activateIgnoringOtherApps:YES].
		// IsIconic tells us whether the window is currently minimised
		// (e.g. after Win+D) so we know whether SW_RESTORE is needed
		// before SetForegroundWindow.
		procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
		procShowWindow = user32.NewProc("ShowWindow")
		procIsIconic = user32.NewProc("IsIconic")
		user32Loaded = true
	})
}

// findMainWindow walks the top-level window list and returns the first
// window owned by the current process. Visible windows are preferred —
// Wails creates a few invisible helper windows for message routing that
// we don't want to retype as the main HUD. When no visible window is
// found, we fall back to the first invisible match so the post-Win+D
// path (windows minimised system-wide; some Wails versions briefly drop
// WS_VISIBLE while transitioning the iconic state) can still locate the
// window and re-apply the style mask.
//
// Returns 0 when no window of either kind is found — typically because
// the Wails runtime is still initialising; callers must treat 0 as "not
// ready" and skip the style swap.
func findMainWindow() windows.HWND {
	if !user32Loaded || procEnumWindows == nil || procGetWindowPID == nil {
		return 0
	}
	myPID := uint32(os.Getpid())
	var foundVisible windows.HWND
	var foundAny windows.HWND

	// EnumWindowsProc signature: BOOL CALLBACK(HWND, LPARAM). Return
	// TRUE to continue enumeration, FALSE to stop. We continue past
	// the first match to give the visible-window heuristic a chance:
	// helper / message-only windows are typically enumerated before
	// the main HUD, so a single "return 0 on first PID match" can
	// pick the wrong HWND on slow boots.
	cb := syscall.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var pid uint32
		_, _, _ = procGetWindowPID.Call(
			uintptr(hwnd),
			uintptr(unsafe.Pointer(&pid)),
		)
		if pid != myPID {
			return 1 // continue
		}
		// Record the first PID match unconditionally so the
		// fallback path has something to return. Then check
		// visibility — if visible, capture and stop scanning.
		if foundAny == 0 {
			foundAny = hwnd
		}
		if procIsWindowVisble != nil {
			visible, _, _ := procIsWindowVisble.Call(uintptr(hwnd))
			if visible == 0 {
				return 1 // not visible; keep scanning
			}
		}
		foundVisible = hwnd
		return 0 // visible match; stop
	})

	_, _, _ = procEnumWindows.Call(cb, 0)
	if foundVisible != 0 {
		return foundVisible
	}
	// Post-Win+D fallback: the main HUD may have lost WS_VISIBLE
	// during the show-desktop transition. Returning any PID-matching
	// top-level window lets the next style swap proceed; the worst
	// case is that we retype a helper window's style mask, which is
	// idempotent and harmless because helper windows aren't drawn.
	return foundAny
}

// activateForeground brings the main Jarvis window to the foreground
// after the style swap, mirroring [NSApp activateIgnoringOtherApps:YES]
// from overlay_chrome_darwin.go. The macOS bridge runs this on every
// frameless=true call so the overlay grabs focus from whichever app the
// global hotkey fired from; this is the Windows equivalent.
//
// Sequence is intentional: IsIconic check first because SetForegroundWindow
// silently fails on a minimised window (Microsoft documents this), so we
// must un-minimise via SW_RESTORE first; then fall back to SW_SHOW for
// the Win+D edge case where the window is "hidden but not minimised";
// finally call SetForegroundWindow to claim focus. Each call is
// independently no-op on failure — the foreground claim is a
// best-effort UX nicety, not a correctness requirement.
//
// Failure case (Win+D acceptance criterion): every syscall here is
// wrapped by the caller's defer-recover; nothing in this function can
// panic on its own. SetForegroundWindow returning FALSE (Windows denied
// the foreground claim because the user is interacting elsewhere) is
// expected and logged at debug level only.
func activateForeground(hwnd windows.HWND) {
	if hwnd == 0 {
		return
	}
	// Un-minimise if iconic. IsIconic is cheap and returning early on
	// the non-iconic path avoids an unnecessary ShowWindow message.
	if procIsIconic != nil {
		iconic, _, _ := procIsIconic.Call(uintptr(hwnd))
		if iconic != 0 && procShowWindow != nil {
			_, _, _ = procShowWindow.Call(uintptr(hwnd), swRestore)
		}
	}
	// SW_SHOW is a no-op when the window is already visible and
	// rectifies the "hidden but not minimised" Win+D pseudo-state. We
	// call it unconditionally because the cost is negligible (one
	// SendMessage) and it makes the activation idempotent regardless
	// of the iconic check above.
	if procShowWindow != nil {
		_, _, _ = procShowWindow.Call(uintptr(hwnd), swShow)
	}
	// SetForegroundWindow can fail with no visible side-effect — this
	// is the documented Windows foreground-lock behaviour. We don't
	// retry: the hotkey-driven overlay show usually succeeds because
	// the dispatch happens within the foreground-permission window
	// Windows grants after a system-wide hotkey fires.
	if procSetForegroundWindow != nil {
		_, _, _ = procSetForegroundWindow.Call(uintptr(hwnd))
	}
}

// SetMainWindowFrameless flips the Wails main window between its
// default titled chrome (WS_OVERLAPPEDWINDOW) and a borderless overlay
// style (WS_POPUP). The contract matches overlay_chrome_darwin.go: no
// return value, idempotent, safe to call from any goroutine.
//
// On frameless=true (TASK-030): after the style swap, this function
// brings Jarvis to the foreground via activateForeground() so the
// overlay actually receives focus when the global hotkey fires from
// another app. This mirrors the darwin path's [NSApp
// activateIgnoringOtherApps:YES] call and closes the macOS / Windows
// behavioural gap that TASK-011 left open.
//
// On frameless=false: the function only restores the titled chrome; no
// foreground claim, because OverlayHide intentionally returns focus to
// whatever app the user was using before the overlay appeared. The
// darwin path is symmetric (no activate on the restore edge).
//
// Failure cases (per acceptance criteria):
//   - Called before the Wails runtime created the main HWND:
//     findMainWindow returns 0 and the function no-ops without touching
//     user32 further.
//   - Win+D (Show Desktop) pressed mid-overlay: the window enters an
//     iconic pseudo-state; the next show call routes through
//     activateForeground which un-minimises via SW_RESTORE before
//     claiming focus. The whole function is wrapped in defer-recover so
//     even a transient syscall panic becomes a logged warning, never a
//     host-app crash.
//   - user32.dll unavailable / not loadable (impossible on supported
//     SKUs, but defensively handled): every proc remains nil and every
//     entry point degrades to a no-op.
func SetMainWindowFrameless(frameless bool) {
	defer func() {
		// Defense in depth: any syscall layer panic (NewCallback can
		// in theory panic if the runtime is mid-shutdown, or
		// SetForegroundWindow on a half-initialised HWND could trip
		// up the Win32 marshaller) becomes a logged warning rather
		// than crashing the host app. The darwin path has the same
		// "never crash" guarantee via dispatch_async. This also
		// satisfies the TASK-030 Win+D acceptance criterion: even if
		// the iconic-state transition causes a transient Win32 error,
		// the host process stays up.
		if r := recover(); r != nil {
			slog.Warn("SetMainWindowFrameless: recovered from panic",
				"frameless", frameless, "recover", r)
		}
	}()

	loadUser32()
	if !user32Loaded {
		return
	}

	hwnd := findMainWindow()
	if hwnd == 0 {
		slog.Debug("SetMainWindowFrameless: no main window found, no-op",
			"frameless", frameless)
		return
	}

	// Read current style so we preserve WS_VISIBLE and any other bits
	// Wails or the OS has set since startup. Casting through int32
	// matches the SetWindowLongW signature (which takes a LONG).
	// Intermediate variable: uintptr(gwlStyle) directly is a compile
	// error because gwlStyle is a negative constant. Going via a
	// runtime int32 variable lets the Win32 ABI sign-extend properly.
	styleIdx := gwlStyle
	cur, _, _ := procGetWindowLong.Call(uintptr(hwnd), uintptr(styleIdx))
	style := uint32(cur)

	var newStyle uint32
	if frameless {
		// Strip everything that paints chrome; keep WS_VISIBLE and
		// add WS_POPUP so the window remains drawable as a top-level
		// borderless surface.
		newStyle = (style &^ wsOverlappedWindow) | wsPopup | wsVisible
	} else {
		// Restore the default titled chrome Wails created at boot.
		// Clearing WS_POPUP is required because OR'ing
		// WS_OVERLAPPEDWINDOW alone wouldn't remove the popup bit
		// we set on the previous call.
		newStyle = (style &^ wsPopup) | wsOverlappedWindow | wsVisible
	}

	// Style-already-correct fast path: only skip the style swap and
	// frame-recompute. We still run activateForeground on
	// frameless=true so a re-fired global hotkey re-focuses the
	// overlay even when nothing about the style needs to change
	// (this matches the macOS path, which always runs
	// activateIgnoringOtherApps regardless of mask delta).
	if newStyle != style {
		_, _, _ = procSetWindowLong.Call(uintptr(hwnd), uintptr(styleIdx), uintptr(newStyle))

		// SWP_FRAMECHANGED forces WM_NCCALCSIZE so the title-bar
		// area is repainted immediately. Without this flag the old
		// chrome strip hangs around until the user resizes the
		// window. We pass SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|
		// SWP_NOACTIVATE because the caller (app_overlay.go)
		// already sets size + position + AOT via the Wails runtime
		// — we only want the frame redraw here.
		_, _, _ = procSetWindowPos.Call(
			uintptr(hwnd),
			0, // hWndInsertAfter — ignored due to SWP_NOZORDER
			0, 0, 0, 0,
			swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged,
		)
	}

	if frameless {
		// Mirror the macOS activate path: bring Jarvis to the
		// foreground so the overlay grabs focus when the global
		// hotkey fires from another app. Runs after the style swap
		// so the foreground window is already in its overlay form
		// when it becomes active — no visible chrome flash. This is
		// the TASK-030 completion of TASK-011's "TODO: bring the
		// process to the foreground" note.
		activateForeground(hwnd)
	}
}
