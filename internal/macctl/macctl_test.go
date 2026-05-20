package macctl

import (
	"errors"
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
		// SetVolume, Mute, Unmute, SetBrightness, ToggleDND are implemented;
		// tests live below.

		// --- TASK-013: files + clipboard + screenshots ---
		{"OpenPath", func() (string, error) { return c.OpenPath("/tmp") }},
		{"Spotlight", func() (string, error) { return c.Spotlight("README") }},
		{"Screenshot", func() (string, error) { return c.Screenshot("screen") }},
		{"ClipboardGet", func() (string, error) { return c.ClipboardGet() }},
		{"ClipboardSet", func() (string, error) { return c.ClipboardSet("hello") }},

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
		{"ErrInvalidArg", ErrInvalidArg},
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

// --- TASK-012 tests: SetVolume / Mute / Unmute / SetBrightness / ToggleDND ---

// TestSetVolume_RejectsOutOfRange pins the input validation guard. The
// daemon's LLM may hallucinate a percentage outside 0..100 (e.g. "Set
// volume to 150" -> 150), and the controller is the defensive boundary.
// Both -1 and 101 must return ErrInvalidArg AND NOT touch osascript --
// the recorder pins the no-side-effect invariant.
func TestSetVolume_RejectsOutOfRange(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_set_volume", DecisionAllow)

	cases := []struct {
		name string
		pct  int
	}{
		{"negative", -1},
		{"too large", 101},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var osascriptCalls int
			c.osascript = func(script string) (string, error) {
				osascriptCalls++
				return "", nil
			}
			got, err := c.SetVolume(tc.pct)
			if !errors.Is(err, ErrInvalidArg) {
				t.Errorf("SetVolume(%d): err = %v; want ErrInvalidArg", tc.pct, err)
			}
			if got != "" {
				t.Errorf("SetVolume(%d): returned %q; want empty string on error", tc.pct, got)
			}
			if osascriptCalls != 0 {
				t.Errorf("SetVolume(%d): osascript called %d times; "+
					"validation must short-circuit before any side effect", tc.pct, osascriptCalls)
			}
		})
	}
}

// TestSetVolume_IssuesCorrectScript pins the AppleScript shape: the
// daemon dispatches "set volume to 50" and the controller must emit
// `set volume output volume 50` -- the publicly-documented recipe. If
// a future refactor swaps the script (e.g. via the deprecated `osascript
// -e "set output volume of (get volume settings) to 50"`) this test
// fails and forces a conscious change.
func TestSetVolume_IssuesCorrectScript(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_set_volume", DecisionAllow)

	var recorded []string
	c.osascript = func(script string) (string, error) {
		recorded = append(recorded, script)
		return "", nil
	}

	got, err := c.SetVolume(50)
	if err != nil {
		t.Fatalf("SetVolume(50): unexpected err = %v", err)
	}
	if got != "" {
		t.Errorf("SetVolume(50): returned %q; want empty string", got)
	}
	if len(recorded) != 1 {
		t.Fatalf("SetVolume(50): osascript invocations = %d; want 1", len(recorded))
	}
	want := "set volume output volume 50"
	if recorded[0] != want {
		t.Errorf("SetVolume(50): script = %q; want %q", recorded[0], want)
	}
}

// TestSetVolume_PolicyDeny pins the deny short-circuit for the audio
// controls. Same shape as OpenApp/QuitApp's policy guards -- deny must
// return ErrPolicyDeny without touching osascript.
func TestSetVolume_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_set_volume", DecisionDeny)

	var osascriptCalls int
	c.osascript = func(script string) (string, error) {
		osascriptCalls++
		return "", nil
	}

	_, err := c.SetVolume(50)
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("SetVolume with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if osascriptCalls != 0 {
		t.Errorf("SetVolume with policy=deny: osascript called %d times; want 0", osascriptCalls)
	}
}

// TestMute_IssuesCorrectScript pins the canonical Mute AppleScript:
// `set volume with output muted`. The "with output muted" idiom is the
// AppleScript shorthand for setting the boolean property -- swapping to
// the verbose `set volume settings to ...` form would still work but
// breaks the symmetry with Unmute, so we pin the exact form.
func TestMute_IssuesCorrectScript(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_mute", DecisionAllow)

	var recorded []string
	c.osascript = func(script string) (string, error) {
		recorded = append(recorded, script)
		return "", nil
	}

	got, err := c.Mute()
	if err != nil {
		t.Fatalf("Mute: unexpected err = %v", err)
	}
	if got != "" {
		t.Errorf("Mute: returned %q; want empty string", got)
	}
	if len(recorded) != 1 {
		t.Fatalf("Mute: osascript invocations = %d; want 1", len(recorded))
	}
	want := "set volume with output muted"
	if recorded[0] != want {
		t.Errorf("Mute: script = %q; want %q", recorded[0], want)
	}
}

// TestUnmute_IssuesCorrectScript pins Unmute's counterpart shape:
// `set volume without output muted`. Symmetric with Mute's recipe.
func TestUnmute_IssuesCorrectScript(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_unmute", DecisionAllow)

	var recorded []string
	c.osascript = func(script string) (string, error) {
		recorded = append(recorded, script)
		return "", nil
	}

	got, err := c.Unmute()
	if err != nil {
		t.Fatalf("Unmute: unexpected err = %v", err)
	}
	if got != "" {
		t.Errorf("Unmute: returned %q; want empty string", got)
	}
	if len(recorded) != 1 {
		t.Fatalf("Unmute: osascript invocations = %d; want 1", len(recorded))
	}
	want := "set volume without output muted"
	if recorded[0] != want {
		t.Errorf("Unmute: script = %q; want %q", recorded[0], want)
	}
}

// TestSetBrightness_ToolMissing pins the ErrToolUnavailable path: when
// the `brightness` CLI is not on PATH, SetBrightness must return a
// wrapped ErrToolUnavailable (so the daemon's tool layer can render the
// install hint) WITHOUT shelling out. We monkey-patch the package-level
// lookPathFn seam to simulate the missing CLI deterministically -- the
// alternative (renaming the real binary or mutating PATH) would be both
// flaky and host-mutating.
func TestSetBrightness_ToolMissing(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_set_brightness", DecisionAllow)

	// Save + restore the seam so other tests in the package see the real
	// lookPathFn. t.Cleanup runs after the test even on failure/skip.
	originalLookPath := lookPathFn
	t.Cleanup(func() { lookPathFn = originalLookPath })

	lookPathFn = func(file string) (string, error) {
		// Simulate "command not found" for `brightness` specifically;
		// any other lookups (none in this code path) hit the real impl.
		if file == "brightness" {
			return "", errors.New("exec: \"brightness\": executable file not found in $PATH")
		}
		return originalLookPath(file)
	}

	// Also stub runCmdFn so an accidental escape past the LookPath
	// guard surfaces loudly via the call counter rather than silently
	// mutating the host's brightness.
	originalRunCmd := runCmdFn
	t.Cleanup(func() { runCmdFn = originalRunCmd })
	var runCmdCalls int
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		runCmdCalls++
		return nil, nil
	}

	got, err := c.SetBrightness(50)
	if !errors.Is(err, ErrToolUnavailable) {
		t.Errorf("SetBrightness with brightness missing: err = %v; want ErrToolUnavailable", err)
	}
	if got != "" {
		t.Errorf("SetBrightness with brightness missing: returned %q; want empty string", got)
	}
	if runCmdCalls != 0 {
		t.Errorf("SetBrightness with brightness missing: runCmd called %d times; "+
			"LookPath failure must short-circuit before exec", runCmdCalls)
	}
	// Pin the install hint substring so the daemon's tool layer can rely
	// on it for the spoken response. If a future refactor drops the
	// "brew install brightness" hint this test fails -- forcing a
	// conscious change to the user-facing copy.
	if !strings.Contains(err.Error(), "brew install brightness") {
		t.Errorf("SetBrightness err = %v; want message containing %q to guide user",
			err, "brew install brightness")
	}
}

// TestSetBrightness_RejectsOutOfRange mirrors the volume validation
// guard -- same rationale, same boundary. Pinned because the brightness
// CLI accepts 0.0..1.0 floats; an unvalidated int could be rescaled to
// 1.5 and rejected by the CLI with a confusing message.
func TestSetBrightness_RejectsOutOfRange(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_set_brightness", DecisionAllow)

	originalLookPath := lookPathFn
	t.Cleanup(func() { lookPathFn = originalLookPath })
	var lookPathCalls int
	lookPathFn = func(file string) (string, error) {
		lookPathCalls++
		return "/opt/homebrew/bin/brightness", nil
	}

	originalRunCmd := runCmdFn
	t.Cleanup(func() { runCmdFn = originalRunCmd })
	var runCmdCalls int
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		runCmdCalls++
		return nil, nil
	}

	cases := []struct {
		name string
		pct  int
	}{
		{"negative", -1},
		{"too large", 101},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookPathCalls = 0
			runCmdCalls = 0
			_, err := c.SetBrightness(tc.pct)
			if !errors.Is(err, ErrInvalidArg) {
				t.Errorf("SetBrightness(%d): err = %v; want ErrInvalidArg", tc.pct, err)
			}
			if lookPathCalls != 0 {
				t.Errorf("SetBrightness(%d): lookPath called %d times; "+
					"validation must short-circuit first", tc.pct, lookPathCalls)
			}
			if runCmdCalls != 0 {
				t.Errorf("SetBrightness(%d): runCmd called %d times; want 0",
					tc.pct, runCmdCalls)
			}
		})
	}
}

// TestSetBrightness_PolicyDeny pins the deny short-circuit for the
// brightness controller. Deny must beat both the LookPath guard and
// the exec call.
func TestSetBrightness_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_set_brightness", DecisionDeny)

	originalLookPath := lookPathFn
	t.Cleanup(func() { lookPathFn = originalLookPath })
	var lookPathCalls int
	lookPathFn = func(file string) (string, error) {
		lookPathCalls++
		return "/opt/homebrew/bin/brightness", nil
	}

	originalRunCmd := runCmdFn
	t.Cleanup(func() { runCmdFn = originalRunCmd })
	var runCmdCalls int
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		runCmdCalls++
		return nil, nil
	}

	_, err := c.SetBrightness(50)
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("SetBrightness with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if lookPathCalls != 0 {
		t.Errorf("SetBrightness with policy=deny: lookPath called %d times; want 0",
			lookPathCalls)
	}
	if runCmdCalls != 0 {
		t.Errorf("SetBrightness with policy=deny: runCmd called %d times; want 0",
			runCmdCalls)
	}
}

// TestToggleDND_PolicyDeny pins the deny short-circuit for the Focus
// toggle. Same invariant: deny -> ErrPolicyDeny without touching the
// `shortcuts` CLI.
func TestToggleDND_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_toggle_dnd", DecisionDeny)

	originalLookPath := lookPathFn
	t.Cleanup(func() { lookPathFn = originalLookPath })
	var lookPathCalls int
	lookPathFn = func(file string) (string, error) {
		lookPathCalls++
		return "/usr/bin/shortcuts", nil
	}

	originalRunCmd := runCmdFn
	t.Cleanup(func() { runCmdFn = originalRunCmd })
	var runCmdCalls int
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		runCmdCalls++
		return nil, nil
	}

	_, err := c.ToggleDND()
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("ToggleDND with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if lookPathCalls != 0 {
		t.Errorf("ToggleDND with policy=deny: lookPath called %d times; want 0",
			lookPathCalls)
	}
	if runCmdCalls != 0 {
		t.Errorf("ToggleDND with policy=deny: runCmd called %d times; want 0",
			runCmdCalls)
	}
}

// TestToggleDND_IssuesCorrectArgv pins the `shortcuts run "Set Focus"`
// invocation. Apple's stable public path for toggling Focus from the
// command line is the Shortcuts.app "Set Focus" action -- pinning the
// argv documents that contract and catches a future "let's just shell
// `defaults write` again" regression.
func TestToggleDND_IssuesCorrectArgv(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_toggle_dnd", DecisionAllow)

	originalLookPath := lookPathFn
	t.Cleanup(func() { lookPathFn = originalLookPath })
	lookPathFn = func(file string) (string, error) {
		return "/usr/bin/shortcuts", nil
	}

	originalRunCmd := runCmdFn
	t.Cleanup(func() { runCmdFn = originalRunCmd })
	var recorded [][]string
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		recorded = append(recorded, append([]string{name}, args...))
		return nil, nil
	}

	got, err := c.ToggleDND()
	if err != nil {
		t.Fatalf("ToggleDND: unexpected err = %v", err)
	}
	if got != "" {
		t.Errorf("ToggleDND: returned %q; want empty string", got)
	}
	if len(recorded) != 1 {
		t.Fatalf("ToggleDND: runCmd invocations = %d; want 1", len(recorded))
	}
	want := []string{"shortcuts", "run", "Set Focus"}
	if len(recorded[0]) != len(want) {
		t.Fatalf("ToggleDND: argv length = %d; want %d (argv=%v)",
			len(recorded[0]), len(want), recorded[0])
	}
	for i, arg := range want {
		if recorded[0][i] != arg {
			t.Errorf("ToggleDND: argv[%d] = %q; want %q", i, recorded[0][i], arg)
		}
	}
}
