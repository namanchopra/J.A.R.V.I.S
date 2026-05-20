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
	// All Controller methods are now implemented in Wave 1a/1b. This test
	// is intentionally a no-op kept for posterity — if a future task adds
	// a stub-back method, re-populate the closure list here.
	_ = NewController(NewDefaultPolicy())

	stringStubs := []struct {
		name string
		call func() (string, error)
	}{
		// All implemented. Add new stubs here when reverting work-in-progress.
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

// --- TASK-014 tests: ListShortcuts / RunShortcut ---

// TestListShortcuts_PolicyDeny pins the deny short-circuit for the
// read-by-default ListShortcuts tool. The default policy allows it, but
// users can opt out via Settings -> Permissions; when they do, the
// shortcuts CLI must NOT be invoked. We can't easily intercept
// exec.Command from a unit test, so we verify the contract by asserting
// the early-return error matches ErrPolicyDeny. The osascript seam is
// unused by ListShortcuts (it shells `shortcuts` directly, not osascript)
// so the recorder count check from the apps/windows suite doesn't apply
// here -- the typed error IS the contract.
func TestListShortcuts_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_list_shortcuts", DecisionDeny)

	got, err := c.ListShortcuts()
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("ListShortcuts with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	// Nil (not []string{}) on deny -- the empty-slice convention applies
	// only on success. A nil return here makes "did we deny?" cheaper to
	// branch on at the caller.
	if got != nil {
		t.Errorf("ListShortcuts with policy=deny: got %v; want nil slice", got)
	}
}

// TestRunShortcut_EmptyName pins the input-validation guard. Like every
// other macctl method that accepts a name argument, an empty value must
// produce a clear validation error rather than shelling `shortcuts run
// ""` (which would either no-op or pop a Shortcuts.app picker at the
// user -- both wrong for a programmatic dispatch path).
func TestRunShortcut_EmptyName(t *testing.T) {
	c := NewController(NewDefaultPolicy())

	got, err := c.RunShortcut("", "input")
	if err == nil {
		t.Fatal("RunShortcut(\"\", _) err = nil; want validation error")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("RunShortcut(\"\", _) err = %v; want message containing %q",
			err, "name is required")
	}
	if got != "" {
		t.Errorf("RunShortcut(\"\", _) returned %q; want empty string on error", got)
	}
}

// TestRunShortcut_PolicyDeny mirrors the deny short-circuit pattern from
// QuitApp / FocusWindow: when policy returns deny, RunShortcut must
// return ErrPolicyDeny BEFORE invoking the shortcuts CLI. The non-empty
// name argument distinguishes this case from TestRunShortcut_EmptyName
// -- we want to confirm a valid name + denied policy still produces the
// policy-deny path (not a validation error).
func TestRunShortcut_PolicyDeny(t *testing.T) {
	c := NewController(NewDefaultPolicy())
	c.policy.Set("mac_run_shortcut", DecisionDeny)

	got, err := c.RunShortcut("Take Note", "test input")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("RunShortcut with policy=deny: err = %v; want ErrPolicyDeny", err)
	}
	if got != "" {
		t.Errorf("RunShortcut with policy=deny: returned %q; want empty string", got)
	}
}
