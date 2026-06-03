package main

// app_overlay_test.go — unit tests for the v0.3.0 overlay window-morph
// bindings declared in app_overlay.go (TASK-004).
//
// External dependencies (the real Wails runtime, the on-disk config file)
// are stubbed via two test seams declared in app_overlay.go:
//   - overlayRuntimeFn  : injects a fakeRuntime in place of prodWindowRuntime
//   - overlayConfigFn   : returns a hand-built *config.Config without
//                         touching ~/.jarvis/config.json
//
// The fakeRuntime is a recorder: every WindowSetSize / WindowSetPosition /
// WindowSetAlwaysOnTop / WindowUnfullscreen / EventsEmit call is appended
// to a calls slice in order, so tests can assert exact sequences. Read-side
// calls (WindowGetSize / WindowGetPosition / WindowIsFullscreen / ScreenGetAll)
// return pre-seeded values.
//
// Note for TASK-005: the same fakeRuntime + installFakeRuntime helper is
// the intended mock surface for the hotkey lifecycle tests. The hotkey
// manager calls into the App's three bindings, which themselves route
// through overlayRuntimeFn -- so swapping the runtime there is sufficient
// to assert hotkey -> binding -> runtime forwarding without bringing up
// a real Wails window.

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/namanchopra/jarvis/internal/config"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

// fakeRuntimeCall is a single record in the fakeRuntime's call log. The
// Name field identifies which runtime method was invoked; the Args slice
// is the argument list in declaration order (or empty for no-arg calls).
// Keeping the args as []interface{} lets one slice hold all call shapes
// without per-method record types.
type fakeRuntimeCall struct {
	Name string
	Args []interface{}
}

// fakeRuntime is the test substitute for prodWindowRuntime. Read-side
// values (curW/curH/curX/curY/isFullscreen/screens/screensErr) are pre-
// seeded by the test; write-side calls and event emissions are appended
// to `calls` under the mutex.
type fakeRuntime struct {
	mu sync.Mutex

	// Pre-seeded read-side values.
	curW, curH   int
	curX, curY   int
	isFullscreen bool
	screens      []wailsruntime.Screen
	screensErr   error

	// Recorded call log (write-side methods + EventsEmit).
	calls []fakeRuntimeCall
}

func (f *fakeRuntime) record(name string, args ...interface{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeRuntimeCall{Name: name, Args: args})
}

func (f *fakeRuntime) snapshot() []fakeRuntimeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeRuntimeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeRuntime) WindowGetSize() (int, int)     { return f.curW, f.curH }
func (f *fakeRuntime) WindowGetPosition() (int, int) { return f.curX, f.curY }

func (f *fakeRuntime) WindowSetSize(width, height int) {
	f.record("WindowSetSize", width, height)
}

func (f *fakeRuntime) WindowSetPosition(x, y int) {
	f.record("WindowSetPosition", x, y)
}

func (f *fakeRuntime) WindowSetAlwaysOnTop(b bool) {
	f.record("WindowSetAlwaysOnTop", b)
}

func (f *fakeRuntime) WindowIsFullscreen() bool { return f.isFullscreen }

func (f *fakeRuntime) WindowUnfullscreen() {
	f.record("WindowUnfullscreen")
}

func (f *fakeRuntime) WindowUnminimise() {
	f.record("WindowUnminimise")
}

func (f *fakeRuntime) WindowShow() {
	f.record("WindowShow")
}

func (f *fakeRuntime) ScreenGetAll() ([]wailsruntime.Screen, error) {
	return f.screens, f.screensErr
}

func (f *fakeRuntime) EventsEmit(eventName string, optionalData ...interface{}) {
	args := append([]interface{}{eventName}, optionalData...)
	f.record("EventsEmit", args...)
}

// installFakeRuntime swaps the package-level overlayRuntimeFn for one that
// returns the given fakeRuntime, restoring the original on t.Cleanup. The
// fake is shared across all App instances during the test, which is fine
// because each test constructs a fresh *App and we never run these tests
// concurrently for the same App.
func installFakeRuntime(t *testing.T, f *fakeRuntime) {
	t.Helper()
	prev := overlayRuntimeFn
	overlayRuntimeFn = func(_ *App) windowRuntime { return f }
	t.Cleanup(func() { overlayRuntimeFn = prev })
}

// installFakeConfig swaps overlayConfigFn for one returning the given
// *config.Config, restoring the original on t.Cleanup. Pass nil to use
// DefaultConfig() (most tests want this).
func installFakeConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	prev := overlayConfigFn
	overlayConfigFn = func() *config.Config { return cfg }
	t.Cleanup(func() { overlayConfigFn = prev })
}

// captureSlog redirects the default slog logger to a buffer for the
// duration of t so warning-message assertions can be made against the
// captured bytes. The previous default is restored on Cleanup. Returns
// the buffer; callers read with buf.String().
//
// Note: slog.Default() is a process-global, so this helper must NOT be
// used by tests running with t.Parallel() against each other. All tests
// in this file run sequentially for that reason (no t.Parallel calls).
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	handler := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// newTestApp returns a fresh *App with the zero-value overlay state. The
// store/scanner/sessionMgr/etc fields are nil because the overlay
// bindings don't touch any of them.
func newTestApp() *App {
	return &App{}
}

// makePrimaryScreen returns a runtime.Screen slice with a single primary
// screen of the given dimensions. We populate only the deprecated
// Width/Height fields because the newer Size struct's type
// (frontend.ScreenSize) lives in an internal package and can't be
// referenced from outside. computeOverlayPosition reads Width/Height as
// the fallback path when Size is zero, so this still exercises the same
// path; the Size-preference branch is covered indirectly by the
// production-only code path.
func makePrimaryScreen(w, h int) []wailsruntime.Screen {
	return []wailsruntime.Screen{
		{
			IsCurrent: true,
			IsPrimary: true,
			Width:     w,
			Height:    h,
		},
	}
}

// findCall returns the first call with the matching name, or false. Use
// findAllCalls when order or count matters.
func findCall(calls []fakeRuntimeCall, name string) (fakeRuntimeCall, bool) {
	for _, c := range calls {
		if c.Name == name {
			return c, true
		}
	}
	return fakeRuntimeCall{}, false
}

// findAllCalls returns every call with the given name preserving order.
func findAllCalls(calls []fakeRuntimeCall, name string) []fakeRuntimeCall {
	out := []fakeRuntimeCall{}
	for _, c := range calls {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestOverlayShowHideRoundTrip exercises the happy path: a Show pins the
// window into the configured corner and emits the overlay event; a Hide
// restores the original geometry exactly and emits the hud event.
func TestOverlayShowHideRoundTrip(t *testing.T) {
	const screenW, screenH = 1920, 1080
	const origW, origH = 1440, 900
	const origX, origY = 100, 100

	f := &fakeRuntime{
		curW:    origW,
		curH:    origH,
		curX:    origX,
		curY:    origY,
		screens: makePrimaryScreen(screenW, screenH),
	}
	installFakeRuntime(t, f)
	installFakeConfig(t, nil) // DefaultConfig → top-right

	a := newTestApp()

	// Show -----------------------------------------------------------------
	if err := a.OverlayShow(); err != nil {
		t.Fatalf("OverlayShow returned err: %v", err)
	}

	calls := f.snapshot()

	setSizes := findAllCalls(calls, "WindowSetSize")
	if len(setSizes) != 1 {
		t.Fatalf("expected exactly 1 WindowSetSize during show, got %d", len(setSizes))
	}
	if setSizes[0].Args[0] != overlayWidth || setSizes[0].Args[1] != overlayHeight {
		t.Errorf("WindowSetSize args = %v, want [%d %d]", setSizes[0].Args, overlayWidth, overlayHeight)
	}

	setPos, ok := findCall(calls, "WindowSetPosition")
	if !ok {
		t.Fatal("expected WindowSetPosition during show, got none")
	}
	wantX := screenW - overlayWidth - overlayGutter
	wantY := overlayGutter
	if setPos.Args[0] != wantX || setPos.Args[1] != wantY {
		t.Errorf("WindowSetPosition (top-right) args = %v, want [%d %d]", setPos.Args, wantX, wantY)
	}

	aot, ok := findCall(calls, "WindowSetAlwaysOnTop")
	if !ok {
		t.Fatal("expected WindowSetAlwaysOnTop(true) during show, got none")
	}
	if aot.Args[0] != true {
		t.Errorf("WindowSetAlwaysOnTop arg = %v, want true", aot.Args[0])
	}

	emit, ok := findCall(calls, "EventsEmit")
	if !ok {
		t.Fatal("expected EventsEmit during show, got none")
	}
	if emit.Args[0] != "overlay:mode" || emit.Args[1] != "overlay" {
		t.Errorf("EventsEmit args = %v, want [overlay:mode overlay]", emit.Args)
	}

	// State should now be saved.
	a.overlayMu.Lock()
	if !a.overlayState.saved {
		a.overlayMu.Unlock()
		t.Fatal("expected overlayState.saved to be true after Show")
	}
	if a.overlayState.w != origW || a.overlayState.h != origH || a.overlayState.x != origX || a.overlayState.y != origY {
		t.Errorf("saved geometry = %+v, want w=%d h=%d x=%d y=%d", a.overlayState, origW, origH, origX, origY)
	}
	a.overlayMu.Unlock()

	// Hide -----------------------------------------------------------------
	// Reset the call log so we can assert just the hide-phase calls.
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()

	if err := a.OverlayHide(); err != nil {
		t.Fatalf("OverlayHide returned err: %v", err)
	}

	calls = f.snapshot()

	aot, ok = findCall(calls, "WindowSetAlwaysOnTop")
	if !ok {
		t.Fatal("expected WindowSetAlwaysOnTop(false) during hide, got none")
	}
	if aot.Args[0] != false {
		t.Errorf("WindowSetAlwaysOnTop arg = %v, want false", aot.Args[0])
	}

	setSize, ok := findCall(calls, "WindowSetSize")
	if !ok {
		t.Fatal("expected WindowSetSize during hide, got none")
	}
	if setSize.Args[0] != origW || setSize.Args[1] != origH {
		t.Errorf("WindowSetSize restore args = %v, want [%d %d]", setSize.Args, origW, origH)
	}

	setPos, ok = findCall(calls, "WindowSetPosition")
	if !ok {
		t.Fatal("expected WindowSetPosition during hide, got none")
	}
	if setPos.Args[0] != origX || setPos.Args[1] != origY {
		t.Errorf("WindowSetPosition restore args = %v, want [%d %d]", setPos.Args, origX, origY)
	}

	emit, ok = findCall(calls, "EventsEmit")
	if !ok {
		t.Fatal("expected EventsEmit during hide, got none")
	}
	if emit.Args[0] != "overlay:mode" || emit.Args[1] != "hud" {
		t.Errorf("EventsEmit hide args = %v, want [overlay:mode hud]", emit.Args)
	}

	// State should now be cleared.
	a.overlayMu.Lock()
	if a.overlayState.saved {
		a.overlayMu.Unlock()
		t.Error("expected overlayState.saved to be false after Hide")
	}
	a.overlayMu.Unlock()
}

// TestOverlayToggleAlternates verifies two consecutive Toggle calls
// produce exactly one show sequence followed by exactly one hide
// sequence, with no extra emissions.
func TestOverlayToggleAlternates(t *testing.T) {
	f := &fakeRuntime{
		curW:    1280,
		curH:    800,
		curX:    50,
		curY:    50,
		screens: makePrimaryScreen(1920, 1080),
	}
	installFakeRuntime(t, f)
	installFakeConfig(t, nil)

	a := newTestApp()

	// First toggle → show.
	if err := a.OverlayToggle(); err != nil {
		t.Fatalf("first OverlayToggle err: %v", err)
	}
	emits := findAllCalls(f.snapshot(), "EventsEmit")
	if len(emits) != 1 || emits[0].Args[1] != "overlay" {
		t.Fatalf("after first toggle, expected one EventsEmit overlay, got %v", emits)
	}

	// Second toggle → hide.
	if err := a.OverlayToggle(); err != nil {
		t.Fatalf("second OverlayToggle err: %v", err)
	}
	emits = findAllCalls(f.snapshot(), "EventsEmit")
	if len(emits) != 2 {
		t.Fatalf("after second toggle, expected two EventsEmits total, got %d", len(emits))
	}
	if emits[0].Args[1] != "overlay" || emits[1].Args[1] != "hud" {
		t.Errorf("toggle emits payloads = [%v %v], want [overlay hud]", emits[0].Args[1], emits[1].Args[1])
	}
}

// TestOverlayHideBeforeShow verifies the failure-case soft no-op: calling
// Hide on a fresh App emits no event, makes no window calls, and logs a
// warning. Most importantly, it does NOT crash.
func TestOverlayHideBeforeShow(t *testing.T) {
	f := &fakeRuntime{
		screens: makePrimaryScreen(1920, 1080),
	}
	installFakeRuntime(t, f)
	installFakeConfig(t, nil)
	logBuf := captureSlog(t)

	a := newTestApp()

	if err := a.OverlayHide(); err != nil {
		t.Fatalf("OverlayHide returned err on fresh App: %v", err)
	}

	calls := f.snapshot()
	if len(calls) != 0 {
		t.Errorf("expected no runtime calls on hide-before-show, got %d: %+v", len(calls), calls)
	}

	if !strings.Contains(logBuf.String(), "no saved geometry") {
		t.Errorf("expected slog warning about 'no saved geometry', got log: %q", logBuf.String())
	}
}

// TestOverlayShowFallsBackForUnknownPosition verifies the defensive
// frontend-mirroring behaviour: an OverlayPosition value not in the known
// set computes the top-right coordinates (the documented default).
func TestOverlayShowFallsBackForUnknownPosition(t *testing.T) {
	const screenW, screenH = 1600, 900

	f := &fakeRuntime{
		curW:    1200,
		curH:    700,
		curX:    200,
		curY:    150,
		screens: makePrimaryScreen(screenW, screenH),
	}
	installFakeRuntime(t, f)

	cfg := config.DefaultConfig()
	cfg.OverlayPosition = "moon" // unknown value
	installFakeConfig(t, cfg)

	a := newTestApp()
	if err := a.OverlayShow(); err != nil {
		t.Fatalf("OverlayShow err: %v", err)
	}

	setPos, ok := findCall(f.snapshot(), "WindowSetPosition")
	if !ok {
		t.Fatal("expected WindowSetPosition")
	}
	wantX := screenW - overlayWidth - overlayGutter
	wantY := overlayGutter
	if setPos.Args[0] != wantX || setPos.Args[1] != wantY {
		t.Errorf("unknown-position fallback args = %v, want [%d %d] (top-right)", setPos.Args, wantX, wantY)
	}
}

// TestOverlayShowUnfullscreensFirst verifies that when the window is in
// macOS fullscreen at Show time, WindowUnfullscreen is called before any
// WindowSetSize / WindowSetPosition. The relative ordering matters
// because macOS silently ignores size changes on fullscreen windows.
func TestOverlayShowUnfullscreensFirst(t *testing.T) {
	f := &fakeRuntime{
		curW:         1440,
		curH:         900,
		curX:         100,
		curY:         100,
		isFullscreen: true,
		screens:      makePrimaryScreen(1920, 1080),
	}
	installFakeRuntime(t, f)
	installFakeConfig(t, nil)

	a := newTestApp()
	if err := a.OverlayShow(); err != nil {
		t.Fatalf("OverlayShow err: %v", err)
	}

	calls := f.snapshot()

	// Find the indexes of the relevant calls; assert ordering.
	unfullscreenIdx := -1
	setSizeIdx := -1
	setPosIdx := -1
	for i, c := range calls {
		switch c.Name {
		case "WindowUnfullscreen":
			if unfullscreenIdx == -1 {
				unfullscreenIdx = i
			}
		case "WindowSetSize":
			if setSizeIdx == -1 {
				setSizeIdx = i
			}
		case "WindowSetPosition":
			if setPosIdx == -1 {
				setPosIdx = i
			}
		}
	}

	if unfullscreenIdx == -1 {
		t.Fatal("expected WindowUnfullscreen during show when starting fullscreen")
	}
	if setSizeIdx == -1 || setPosIdx == -1 {
		t.Fatalf("expected both WindowSetSize and WindowSetPosition during show")
	}
	if unfullscreenIdx > setSizeIdx {
		t.Errorf("WindowUnfullscreen (idx %d) must come BEFORE WindowSetSize (idx %d)", unfullscreenIdx, setSizeIdx)
	}
	if unfullscreenIdx > setPosIdx {
		t.Errorf("WindowUnfullscreen (idx %d) must come BEFORE WindowSetPosition (idx %d)", unfullscreenIdx, setPosIdx)
	}

	// And the wasFullscreen flag should be recorded.
	a.overlayMu.Lock()
	if !a.overlayState.wasFullscreen {
		a.overlayMu.Unlock()
		t.Error("expected overlayState.wasFullscreen = true after Show on a fullscreen window")
	}
	a.overlayMu.Unlock()
}

// TestOverlayShowDoubleNoOp verifies that a second OverlayShow while the
// overlay is already active is a no-op: no WindowSetSize / SetPosition /
// AlwaysOnTop / EventsEmit during the second call (so the saved geometry
// from the first call is preserved untouched, which is what makes the
// eventual Hide actually restore the user's original window).
func TestOverlayShowDoubleNoOp(t *testing.T) {
	f := &fakeRuntime{
		curW:    1200,
		curH:    750,
		curX:    25,
		curY:    25,
		screens: makePrimaryScreen(1920, 1080),
	}
	installFakeRuntime(t, f)
	installFakeConfig(t, nil)

	a := newTestApp()
	if err := a.OverlayShow(); err != nil {
		t.Fatalf("first OverlayShow err: %v", err)
	}
	firstCallCount := len(f.snapshot())

	// Mutate the fake's reported window state so if Show were not a no-op
	// it would over-write the saved geometry with these (wrong) values.
	f.mu.Lock()
	f.curW, f.curH, f.curX, f.curY = 50, 50, 0, 0
	f.mu.Unlock()

	if err := a.OverlayShow(); err != nil {
		t.Fatalf("second OverlayShow err: %v", err)
	}

	if len(f.snapshot()) != firstCallCount {
		t.Errorf("second OverlayShow should be a no-op (no new calls); got %d additional calls",
			len(f.snapshot())-firstCallCount)
	}

	// Saved geometry should still reflect the original Show, not the
	// post-mutation values.
	a.overlayMu.Lock()
	defer a.overlayMu.Unlock()
	if a.overlayState.w != 1200 || a.overlayState.h != 750 || a.overlayState.x != 25 || a.overlayState.y != 25 {
		t.Errorf("saved geometry after double Show = %+v, want w=1200 h=750 x=25 y=25", a.overlayState)
	}
}

// TestComputeOverlayPositionCorners exercises every documented corner
// keyword (and the fallback) using a deterministic screen. This is a
// pure unit test on the helper -- no App needed.
func TestComputeOverlayPositionCorners(t *testing.T) {
	const w, h = 2000, 1200

	cases := []struct {
		corner string
		wantX  int
		wantY  int
	}{
		{"top-left", overlayGutter, overlayGutter},
		{"top-right", w - overlayWidth - overlayGutter, overlayGutter},
		{"bottom-left", overlayGutter, h - overlayHeight - overlayGutter},
		{"bottom-right", w - overlayWidth - overlayGutter, h - overlayHeight - overlayGutter},
		{"last-dragged", w - overlayWidth - overlayGutter, overlayGutter}, // falls back to top-right in v1
		{"garbage", w - overlayWidth - overlayGutter, overlayGutter},      // unknown → top-right
		{"", w - overlayWidth - overlayGutter, overlayGutter},             // empty → top-right
	}

	for _, tc := range cases {
		t.Run(tc.corner, func(t *testing.T) {
			f := &fakeRuntime{screens: makePrimaryScreen(w, h)}
			gotX, gotY := computeOverlayPosition(f, tc.corner)
			if gotX != tc.wantX || gotY != tc.wantY {
				t.Errorf("computeOverlayPosition(%q) = (%d, %d), want (%d, %d)",
					tc.corner, gotX, gotY, tc.wantX, tc.wantY)
			}
		})
	}
}

// TestComputeOverlayPositionScreenError verifies the safe fallback when
// ScreenGetAll errors out -- the helper returns (overlayGutter,
// overlayGutter) rather than panicking or returning negative coordinates.
func TestComputeOverlayPositionScreenError(t *testing.T) {
	f := &fakeRuntime{screensErr: errors.New("simulated screen error")}
	x, y := computeOverlayPosition(f, "top-right")
	if x != overlayGutter || y != overlayGutter {
		t.Errorf("ScreenGetAll-error fallback = (%d, %d), want (%d, %d)",
			x, y, overlayGutter, overlayGutter)
	}
}

// TestComputeOverlayPositionPicksPrimary verifies that when multiple
// screens are present, the one with IsPrimary=true is selected (not
// just screens[0]). This guards the "the user's secondary monitor is
// listed first" case which is common on macOS multi-monitor setups.
func TestComputeOverlayPositionPicksPrimary(t *testing.T) {
	screens := []wailsruntime.Screen{
		{IsPrimary: false, Width: 1024, Height: 768},
		{IsPrimary: true, Width: 2560, Height: 1440},
	}
	f := &fakeRuntime{screens: screens}

	x, y := computeOverlayPosition(f, "top-right")
	wantX := 2560 - overlayWidth - overlayGutter
	wantY := overlayGutter
	if x != wantX || y != wantY {
		t.Errorf("primary-screen selection = (%d, %d), want (%d, %d)", x, y, wantX, wantY)
	}
}
