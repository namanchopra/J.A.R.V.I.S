package macctl

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestNewControllerReturnsNonNil verifies the constructor wires a usable
// Controller. A nil return would mean every caller has to nil-check before
// dispatching a tool — which they don't, because callers (the Wails App,
// the daemon tool bridge) treat NewController as infallible. Pinning the
// non-nil contract here means an accidental future refactor that returns
// nil on a degenerate input will fail loudly.
func TestNewControllerReturnsNonNil(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	if c == nil {
		t.Fatal("NewController(NewDefaultPolicy()) returned nil; expected non-nil *Controller")
	}
	if c.policy == nil {
		t.Error("Controller.policy is nil; constructor must store the policy reference")
	}
	if c.osascript == nil {
		t.Error("Controller.osascript is nil; constructor must wire defaultOsascript")
	}
}

// TestStubsReturnErrNotImplemented is the central contract for TASK-002:
// every public method on *Controller is a stub that returns
// ErrNotImplemented (matched with errors.Is, NOT string compare, so future
// wrapping with fmt.Errorf("OpenApp: %w", err) doesn't break the test).
//
// The "" / nil success check is the deliberate guard from the TASK-002 spec:
// if a future agent accidentally writes `return "ok", nil` instead of a
// real implementation, this test catches it BEFORE the daemon starts
// reporting fake successes to the user. Returning "" + ErrNotImplemented
// is the only acceptable stub shape.
func TestStubsReturnErrNotImplemented(t *testing.T) {
	c := NewController(NewDefaultPolicy())

	// stringStub covers the 14 methods returning (string, error). Each
	// case names the method and supplies a closure that invokes it with
	// representative arguments. The arguments are irrelevant for stub
	// behavior (every code path returns the sentinel) but using
	// realistic values documents the intended signature for readers.
	stringStubs := []struct {
		name string
		call func() (string, error)
	}{
		// --- TASK-011: apps + windows ---
		// OpenApp, QuitApp, FocusWindow are implemented; tests live below.

		// --- TASK-012: audio + display ---
		{"SetVolume", func() (string, error) { return c.SetVolume(50) }},
		{"Mute", func() (string, error) { return c.Mute() }},
		{"Unmute", func() (string, error) { return c.Unmute() }},
		{"SetBrightness", func() (string, error) { return c.SetBrightness(75) }},
		{"ToggleDND", func() (string, error) { return c.ToggleDND() }},

		// --- TASK-013: files + clipboard + screenshots ---
		// OpenPath, Spotlight, Screenshot, ClipboardGet, ClipboardSet are
		// implemented; tests live below.

		// --- TASK-014: shortcuts (RunShortcut returns string; ListShortcuts is tested below) ---
		{"RunShortcut", func() (string, error) { return c.RunShortcut("Take Note", "test") }},
	}

	for _, tc := range stringStubs {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.call()

			// Match via errors.Is so future wrapping (fmt.Errorf("OpenApp: %w", err))
			// doesn't silently break the test. errors.Is also rejects nil.
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("%s: err = %v; want ErrNotImplemented", tc.name, err)
			}

			// The explicit empty-string check from the TASK-002 spec: if a
			// future agent half-implements a stub (e.g. `return "ok", err`)
			// this catches it before users see fake successes. The stub
			// contract is "" + ErrNotImplemented — non-empty strings are a
			// bug regardless of error value.
			if got != "" {
				t.Errorf("%s: returned non-empty string %q before implementation; "+
					"stub contract is \"\" + ErrNotImplemented", tc.name, got)
			}
		})
	}
}

// TestListShortcutsStub is split out because its return shape is
// ([]string, error) rather than (string, error). Same contract: nil slice
// + ErrNotImplemented while the stub stands. TASK-014 will switch the
// success path to []string{} (never nil) per the project's Wails
// serialization convention, but the stub MUST stay nil so this test
// catches accidental "return []string{}, nil" half-implementations.
func TestListShortcutsStub(t *testing.T) {
	c := NewController(NewDefaultPolicy())

	got, err := c.ListShortcuts()

	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("ListShortcuts: err = %v; want ErrNotImplemented", err)
	}
	if got != nil {
		t.Errorf("ListShortcuts: returned non-nil slice %v before implementation; "+
			"stub contract is nil + ErrNotImplemented", got)
	}
}

// TestErrorSentinelsAreDistinct guards against a future refactor that
// accidentally aliases two sentinels to the same underlying error (e.g.
// `var ErrPolicyDeny = ErrNotImplemented`). Callers in TASK-011..014 use
// errors.Is to discriminate between "feature not built", "user denied by
// policy", and "tool missing" to render different spoken responses — so
// the three sentinels MUST be distinguishable.
func TestErrorSentinelsAreDistinct(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrNotImplemented", ErrNotImplemented},
		{"ErrPolicyDeny", ErrPolicyDeny},
		{"ErrWindowNotFound", ErrWindowNotFound},
		{"ErrToolUnavailable", ErrToolUnavailable},
	}

	for i, a := range sentinels {
		if a.err == nil {
			t.Errorf("%s is nil; sentinel errors must be non-nil values", a.name)
			continue
		}
		for j, b := range sentinels {
			if i == j {
				continue
			}
			// errors.Is(a, b) would be true only if a wraps b OR a == b.
			// Distinct sentinels must not satisfy errors.Is in either
			// direction, otherwise the policy/dispatch layer can't tell
			// them apart in a switch.
			if errors.Is(a.err, b.err) {
				t.Errorf("%s and %s collapse under errors.Is; sentinels must be distinct",
					a.name, b.name)
			}
		}
	}
}

// TestOsascriptSeamIsSwappable documents the in-package test seam
// contract: tests CAN swap c.osascript = recorder to capture invocations
// without actually shelling osascript. The stubs themselves don't call
// the seam yet (they short-circuit on ErrNotImplemented), but TASK-011..014
// will rely on this exact swap pattern, and TASK-031 builds an entire
// integration test suite on top of it. If a future refactor renames
// `osascript` to something else, this test fails and the rename gets
// caught before the downstream tests blow up.
func TestOsascriptSeamIsSwappable(t *testing.T) {
	c := NewController(NewDefaultPolicy())

	var calls int
	recorder := func(script string) (string, error) {
		calls++
		return "", nil
	}
	c.osascript = recorder

	// We can't trigger the seam from a stub method (they return early
	// with ErrNotImplemented), so exercise it directly to confirm the
	// field type matches osascriptFn and the closure is callable. If a
	// future change tightens the seam's signature, this call site
	// breaks at compile time — which is the point.
	if _, err := c.osascript("tell application \"Finder\" to activate"); err != nil {
		t.Errorf("swapped osascript invoker returned err = %v; recorder should succeed", err)
	}
	if calls != 1 {
		t.Errorf("osascript recorder calls = %d; want 1", calls)
	}
}

// --- TASK-011 tests: OpenApp / QuitApp / FocusWindow ---

// TestOpenApp_EmptyName pins the input-validation guard. The Wails layer
// validates and trims inputs at the binding boundary, but the Controller
// is the defensive last line — passing an empty string should produce a
// clear error rather than shelling `open -a ""` (which would silently
// pop a "no application named" dialog at the user).
func TestOpenApp_EmptyName(t *testing.T) {
	c := NewController(NewDefaultPolicy())

	got, err := c.OpenApp("")
	if err == nil {
		t.Fatal("OpenApp(\"\") err = nil; want validation error")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("OpenApp(\"\") err = %v; want message containing %q", err, "name is required")
	}
	if got != "" {
		t.Errorf("OpenApp(\"\") returned %q; want empty string on error", got)
	}
}

// TestOpenApp_PolicyDeny is the canonical policy short-circuit guard:
// when policy returns deny, OpenApp must return ErrPolicyDeny BEFORE
// touching osascript (or any other side-effecting subprocess). The
// osascript recorder asserts call count == 0 to pin this — a future
// refactor that reorders policy check after exec.Command would fail.
//
// Note: OpenApp uses exec.Command("open", ...) not osascript, but the
// same invariant holds — the policy check is the first action of the
// method, so even though we can't easily intercept `open` from a test,
// we CAN guarantee the osascript seam wasn't touched. (The exec.Command
// path is unreachable when DecisionDeny short-circuits.)
func TestOpenApp_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_open_app", DecisionDeny)

	var osascriptCalls int
	c.osascript = func(script string) (string, error) {
		osascriptCalls++
		return "", nil
	}

	got, err := c.OpenApp("Safari")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("OpenApp with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if got != "" {
		t.Errorf("OpenApp with policy=deny: returned %q; want empty string", got)
	}
	if osascriptCalls != 0 {
		t.Errorf("OpenApp with policy=deny: osascript called %d times; "+
			"want 0 — policy must short-circuit before any side effect", osascriptCalls)
	}
}

// TestQuitApp_IssuesCorrectScript pins the AppleScript shape. The daemon
// dispatches "quit Slack" by name, and the controller must translate that
// to `tell application "Slack" to quit` — using %q so the app name is
// AppleScript-escaped (handles apps with quotes in their names like
// `My "Special" App`). If a refactor swaps %q for %s the escaping
// regression is caught here.
func TestQuitApp_IssuesCorrectScript(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	// Default policy for mac_quit_app is "ask" — but the controller only
	// short-circuits on deny. Ask still lets the script through (the
	// confirmation gate runs at the daemon layer, not in the controller).
	c.policy.Set("mac_quit_app", DecisionAllow)

	var recorded []string
	c.osascript = func(script string) (string, error) {
		recorded = append(recorded, script)
		return "", nil
	}

	got, err := c.QuitApp("Slack")
	if err != nil {
		t.Fatalf("QuitApp: unexpected err = %v", err)
	}
	if got != "" {
		t.Errorf("QuitApp: returned %q; want empty string", got)
	}
	if len(recorded) != 1 {
		t.Fatalf("QuitApp: osascript invocations = %d; want 1", len(recorded))
	}
	want := `tell application "Slack" to quit`
	if recorded[0] != want {
		t.Errorf("QuitApp: script = %q; want %q", recorded[0], want)
	}
}

// TestQuitApp_PolicyDeny mirrors OpenApp's deny check — QuitApp is the
// flagship destructive tool, so its short-circuit is doubly important.
func TestQuitApp_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_quit_app", DecisionDeny)

	var osascriptCalls int
	c.osascript = func(script string) (string, error) {
		osascriptCalls++
		return "", nil
	}

	_, err := c.QuitApp("Slack")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("QuitApp with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if osascriptCalls != 0 {
		t.Errorf("QuitApp with policy=deny: osascript called %d times; want 0", osascriptCalls)
	}
}

// TestFocusWindow_AppOnly_NoTitle covers the empty-title path: when the
// caller only knows the app name, FocusWindow activates the app and
// returns success — the frontmost window wins. Only ONE osascript call
// is issued (no System Events enumeration), and we don't return
// ErrWindowNotFound.
func TestFocusWindow_AppOnly_NoTitle(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_focus_window", DecisionAllow)

	var recorded []string
	c.osascript = func(script string) (string, error) {
		recorded = append(recorded, script)
		return "", nil
	}

	got, err := c.FocusWindow("Slack", "")
	if err != nil {
		t.Fatalf("FocusWindow(app, \"\"): unexpected err = %v", err)
	}
	if got != "" {
		t.Errorf("FocusWindow(app, \"\"): returned %q; want empty string", got)
	}
	// Only the activate script — no System Events enumeration when title
	// is empty.
	if len(recorded) != 1 {
		t.Fatalf("FocusWindow(app, \"\"): osascript invocations = %d; want 1 (activate only)",
			len(recorded))
	}
	wantActivate := `tell application "Slack" to activate`
	if recorded[0] != wantActivate {
		t.Errorf("FocusWindow(app, \"\"): script[0] = %q; want %q", recorded[0], wantActivate)
	}
}

// TestFocusWindow_NotFound pins the ErrWindowNotFound contract: when the
// System Events enumeration returns output that does NOT contain "ok"
// (because no window matched the title substring), FocusWindow must
// return ErrWindowNotFound — not a generic error, so the daemon's tool
// layer can offer a "did you mean ..." fallback instead of a raw
// AppleScript surface.
func TestFocusWindow_NotFound(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_focus_window", DecisionAllow)

	// Recorder returns empty string for the enumeration call — no "ok"
	// substring → ErrWindowNotFound.
	c.osascript = func(script string) (string, error) {
		return "", nil
	}

	_, err := c.FocusWindow("Slack", "no-such-channel")
	if !errors.Is(err, ErrWindowNotFound) {
		t.Errorf("FocusWindow with no match: err = %v; want ErrWindowNotFound", err)
	}
}

// TestFocusWindow_PolicyDeny pins the deny short-circuit for the third
// TASK-011 method. Same shape as OpenApp/QuitApp: deny → ErrPolicyDeny,
// osascript never touched.
func TestFocusWindow_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_focus_window", DecisionDeny)

	var osascriptCalls int
	c.osascript = func(script string) (string, error) {
		osascriptCalls++
		return "", nil
	}

	_, err := c.FocusWindow("Slack", "general")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("FocusWindow with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if osascriptCalls != 0 {
		t.Errorf("FocusWindow with policy=deny: osascript called %d times; want 0", osascriptCalls)
	}
}

// --- TASK-013 tests: OpenPath / Spotlight / ClipboardGet / ClipboardSet / Screenshot ---
//
// The implementations shell out to `open`, `mdfind`, `pbpaste`, `pbcopy`,
// and `screencapture` — none of which route through the c.osascript test
// seam. We therefore can't intercept their calls from a unit test the way
// the apps/windows tests do. Two practical consequences:
//
//  1. Argument-validation and policy-deny short-circuits ARE unit-testable
//     because they fire BEFORE the exec.Command. Those are pinned below.
//  2. Round-trip / success-path tests require a real Mac with pbcopy etc.
//     on $PATH — they live behind t.Skip in -short mode and behind a
//     LookPath check so the package still passes `go test` on Linux CI.

// TestOpenPath_EmptyPath pins the input-validation guard. The Wails layer
// trims and validates path strings at the binding boundary, but the
// controller is the defensive last line — an empty path must produce a
// clear "path is required" error rather than shelling `open ""` (which
// produces a confusing macOS dialog box at the user).
func TestOpenPath_EmptyPath(t *testing.T) {
	c := NewController(NewDefaultPolicy())

	got, err := c.OpenPath("")
	if err == nil {
		t.Fatal("OpenPath(\"\") err = nil; want validation error")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("OpenPath(\"\") err = %v; want message containing %q", err, "path is required")
	}
	if got != "" {
		t.Errorf("OpenPath(\"\") returned %q; want empty string on error", got)
	}
}

// TestOpenPath_PolicyDeny pins the policy short-circuit: when policy
// denies mac_open_path, OpenPath must return ErrPolicyDeny BEFORE
// shelling `open` so a user-denied tool truly never side-effects. We
// can't intercept the exec.Command from a unit test, but we CAN feed a
// path that would visibly fail if `open` ran (a nonexistent file://) and
// assert we got ErrPolicyDeny instead of the wrapped exec error.
func TestOpenPath_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_open_path", DecisionDeny)

	_, err := c.OpenPath("file:///definitely/not/a/real/path/jarvis-policy-deny")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("OpenPath with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
}

// TestSpotlight_EmptyQuery — same shape as OpenPath_EmptyPath. An empty
// query against mdfind would return either a 0-result success or an
// error depending on platform mood; we reject it up front for predictable
// behaviour.
func TestSpotlight_EmptyQuery(t *testing.T) {
	c := NewController(NewDefaultPolicy())

	got, err := c.Spotlight("")
	if err == nil {
		t.Fatal("Spotlight(\"\") err = nil; want validation error")
	}
	if !strings.Contains(err.Error(), "query is required") {
		t.Errorf("Spotlight(\"\") err = %v; want message containing %q", err, "query is required")
	}
	if got != "" {
		t.Errorf("Spotlight(\"\") returned %q; want empty string on error", got)
	}
}

// TestSpotlight_PolicyDeny mirrors OpenPath_PolicyDeny — even though
// mac_spotlight defaults to allow, an admin who flips it to deny must
// see the short-circuit honoured.
func TestSpotlight_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_spotlight", DecisionDeny)

	_, err := c.Spotlight("README")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("Spotlight with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
}

// TestClipboardGet_PolicyDeny pins the deny path. The default for
// mac_clipboard_get is allow (it's read-only), so the deny path only
// kicks in when a privacy-conscious user has explicitly flipped it.
// Without this test a future refactor that drops the policy check from
// ClipboardGet wouldn't be caught.
func TestClipboardGet_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_clipboard_get", DecisionDeny)

	got, err := c.ClipboardGet()
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("ClipboardGet with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if got != "" {
		t.Errorf("ClipboardGet with policy=deny: returned %q; want empty string", got)
	}
}

// TestClipboardSet_PolicyDeny is the most important test in the TASK-013
// suite: ClipboardSet is destructive (it silently overwrites the user's
// pasteboard), so the deny short-circuit MUST happen before pbcopy gets
// any bytes. Because we can't intercept exec.Command from a unit test,
// the indirect proof is: if pbcopy had run, the host clipboard would
// have changed — but we can't query that without calling ClipboardGet,
// which would itself touch the system. So this test asserts the contract
// at the error-return layer: deny → ErrPolicyDeny → "" string return,
// nothing else.
func TestClipboardSet_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_clipboard_set", DecisionDeny)

	got, err := c.ClipboardSet("this should never reach the pasteboard")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("ClipboardSet with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if got != "" {
		t.Errorf("ClipboardSet with policy=deny: returned %q; want empty string", got)
	}
}

// TestScreenshot_InvalidTarget pins the target-validation guard. Only
// {"screen","window","selection"} are valid; anything else must return
// an error containing "invalid" so the daemon's tool layer can render
// a spoken response listing the accepted values.
//
// We assert the substring "invalid" rather than errors.Is(ErrInvalidArg)
// because the ErrInvalidArg sentinel is owned by TASK-012's macctl.go
// edits (which land in parallel). Substring matching is robust to either
// landing order — both the wrapped-sentinel error and a plain
// fmt.Errorf("...invalid...") satisfy this assertion.
func TestScreenshot_InvalidTarget(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_screenshot", DecisionAllow)

	got, err := c.Screenshot("windows") // plural typo — common LLM mistake
	if err == nil {
		t.Fatal("Screenshot(\"windows\") err = nil; want validation error")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("Screenshot(\"windows\") err = %v; want message containing %q", err, "invalid")
	}
	if got != "" {
		t.Errorf("Screenshot(\"windows\") returned %q; want empty string on error", got)
	}
}

// TestScreenshot_PolicyDeny pins the deny short-circuit. mac_screenshot
// defaults to allow but a paranoid user may flip it to deny.
func TestScreenshot_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_screenshot", DecisionDeny)

	got, err := c.Screenshot("screen")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("Screenshot with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if got != "" {
		t.Errorf("Screenshot with policy=deny: returned %q; want empty string", got)
	}
}

// TestClipboardRoundTrip is the integration path: pbcopy "hello", pbpaste,
// assert "hello". Requires a real Mac with pbcopy/pbpaste on $PATH —
// skipped in short mode and on non-darwin hosts. The default `go test`
// run on a developer's Mac will execute this; CI on Linux will skip it.
//
// Round-trip on darwin honours the TASK-013 acceptance criterion:
//
//	"ClipboardSet("hello") + ClipboardGet() round-trips the exact bytes"
//
// We pick a unique sentinel payload so a stale clipboard from a prior
// test run can't mask a regression.
func TestClipboardRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration only — touches real pasteboard")
	}
	if _, err := exec.LookPath("pbcopy"); err != nil {
		t.Skip("pbcopy not available (non-darwin host)")
	}

	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_clipboard_set", DecisionAllow)
	c.policy.Set("mac_clipboard_get", DecisionAllow)

	want := "jarvis-task013-roundtrip-sentinel"
	if _, err := c.ClipboardSet(want); err != nil {
		t.Fatalf("ClipboardSet: unexpected err = %v", err)
	}
	got, err := c.ClipboardGet()
	if err != nil {
		t.Fatalf("ClipboardGet: unexpected err = %v", err)
	}
	if got != want {
		t.Errorf("clipboard round-trip: got %q; want %q", got, want)
	}
}
