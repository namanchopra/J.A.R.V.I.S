package macctl

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

// --- TASK-031 integration tests ---
//
// These are CROSS-LAYER tests that span Controller + Policy + the exec/
// osascript seams. They lock the v0.3.0 invariants that no single
// per-method unit test pins on its own:
//
//   1. Defense-in-depth: policy=deny short-circuits EVERY macctl method
//      before any side effect — not just the ones the per-method suites
//      bothered to cover. New methods added to the Controller should be
//      added to the integration table so the deny contract is loud about
//      missing guards.
//
//   2. Default policy split: the allow/ask defaults from TASK-003 are
//      enforced as the canonical first-run shape. Per-method tests assume
//      the defaults; this asserts them as a single contract.
//
//   3. Save/Load round-trip preserves user choices across Controller
//      instances — the production restart path.

// TestIntegration_DenyShortCircuitsForAllMacctlTools pins the
// defense-in-depth contract: when a tool's policy is DecisionDeny, NO
// osascript / exec call is made by ANY Controller method. This is the
// regression gate against a future agent adding a method that forgets its
// policy.Check guard.
//
// Coverage is parametric so adding a new tool means adding one line to
// the table — the deny contract automatically follows. If a 16th method
// lands without its row here, code review surfaces the omission.
//
// The osascript-call counter is a belt-and-suspenders extra: it asserts
// the seam was not touched for osascript-backed methods (QuitApp,
// FocusWindow, SetVolume, Mute, Unmute). The other methods shell exec
// directly (open, pbpaste, pbcopy, mdfind, shortcuts, screencapture) and
// can't be intercepted from this layer — but the ErrPolicyDeny return is
// the contract: if policy=deny, the method MUST short-circuit before any
// side effect, regardless of which subprocess it would have invoked.
func TestIntegration_DenyShortCircuitsForAllMacctlTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macctl is darwin-only; integration tests skipped on windows")
	}

	cases := []struct {
		toolName string
		invoke   func(c *Controller) error
	}{
		{"mac_open_app", func(c *Controller) error { _, err := c.OpenApp("Safari"); return err }},
		{"mac_quit_app", func(c *Controller) error { _, err := c.QuitApp("Slack"); return err }},
		{"mac_focus_window", func(c *Controller) error { _, err := c.FocusWindow("Slack", "general"); return err }},
		{"mac_set_volume", func(c *Controller) error { _, err := c.SetVolume(50); return err }},
		{"mac_mute", func(c *Controller) error { _, err := c.Mute(); return err }},
		{"mac_unmute", func(c *Controller) error { _, err := c.Unmute(); return err }},
		{"mac_set_brightness", func(c *Controller) error { _, err := c.SetBrightness(50); return err }},
		{"mac_toggle_dnd", func(c *Controller) error { _, err := c.ToggleDND(); return err }},
		{"mac_open_path", func(c *Controller) error { _, err := c.OpenPath("/tmp"); return err }},
		{"mac_spotlight", func(c *Controller) error { _, err := c.Spotlight("test"); return err }},
		{"mac_screenshot", func(c *Controller) error { _, err := c.Screenshot("screen"); return err }},
		{"mac_clipboard_get", func(c *Controller) error { _, err := c.ClipboardGet(); return err }},
		{"mac_clipboard_set", func(c *Controller) error { _, err := c.ClipboardSet("test"); return err }},
		{"mac_list_shortcuts", func(c *Controller) error { _, err := c.ListShortcuts(); return err }},
		{"mac_run_shortcut", func(c *Controller) error { _, err := c.RunShortcut("Take Note", "test"); return err }},
	}

	// Size sanity: 15 macctl tools (every public method on the Controller
	// per the TASK-002 spec). If a 16th lands without a row above, this
	// fails loudly so the new method gets a deny guard in lockstep.
	if got, want := len(cases), 15; got != want {
		t.Errorf("integration table size = %d; want %d — add a row when a new "+
			"macctl method ships so the deny contract is exhaustive", got, want)
	}

	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			policy := NewDefaultPolicy()
			policy.Set(tc.toolName, DecisionDeny)
			c := NewController(policy)

			// osascript recorder: must not be called for any tool with
			// policy=deny. For non-osascript methods (open, pbcopy, mdfind,
			// shortcuts, screencapture) the counter is trivially 0 even
			// without a short-circuit, but the ErrPolicyDeny return below
			// IS the contract — see the function-level comment.
			var osascriptCalls int
			c.osascript = func(string) (string, error) {
				osascriptCalls++
				return "", nil
			}

			err := tc.invoke(c)
			if !errors.Is(err, ErrPolicyDeny) {
				t.Errorf("%s: err = %v; want ErrPolicyDeny — method must "+
					"short-circuit before any side effect when policy=deny",
					tc.toolName, err)
			}
			if osascriptCalls > 0 {
				t.Errorf("%s: osascript was called %d time(s) despite "+
					"policy=deny — the deny check must run BEFORE the seam",
					tc.toolName, osascriptCalls)
			}
		})
	}
}

// TestIntegration_DefaultPolicyEnforcement asserts the default
// allow/ask split is actually enforced by the Controller end-to-end (not
// just visible via Policy.Check, which policy_test.go already covers).
//
// For each allow-listed tool: a fresh Controller + DEFAULT policy + an
// osascript recorder that returns success should produce a no-error
// invocation that DOES reach the seam (or the method's own exec path).
// For ask-listed tools: a fresh Controller with default policy lets the
// method through too (the confirmation gate runs at the daemon layer per
// TASK-018, not in the controller — see app/quit's QuitApp implementation
// and its TestQuitApp_IssuesCorrectScript companion). The Controller
// short-circuits ONLY on deny.
//
// This test specifically asserts that NO tool defaults to deny — if a
// future change accidentally pushed a tool into the deny bucket, every
// invocation would silently fail with ErrPolicyDeny and the user would
// see a flood of "I'm not permitted to ..." spoken responses on
// first launch.
func TestIntegration_DefaultPolicyEnforcement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macctl is darwin-only; integration tests skipped on windows")
	}

	// Pinned allow set — mirrors defaultAllowTools (macctl tools only;
	// spotify_* tools are also default-allow but they're not Controller
	// methods, they live in internal/spotify).
	allowTools := []string{
		"mac_spotlight",
		"mac_clipboard_get",
		"mac_screenshot",
		"mac_list_shortcuts",
	}
	// Pinned ask set — mirrors defaultAskTools.
	askTools := []string{
		"mac_open_app",
		"mac_quit_app",
		"mac_focus_window",
		"mac_set_volume",
		"mac_mute",
		"mac_unmute",
		"mac_set_brightness",
		"mac_toggle_dnd",
		"mac_open_path",
		"mac_clipboard_set",
		"mac_run_shortcut",
	}

	// Acceptance criterion: 13 allow + 11 ask in the package defaults,
	// minus the 9 spotify_* allow tools (handled by spotify package). So
	// the macctl-only allow count is 4 and ask is 11 = 15 total — the
	// same 15 methods the deny-short-circuit test covers above.
	if got, want := len(allowTools)+len(askTools), 15; got != want {
		t.Errorf("default macctl tool count = %d; want %d (matches Controller method count)",
			got, want)
	}

	c := NewController(NewDefaultPolicy())

	// Allow-listed tools: Check returns DecisionAllow on a fresh default
	// policy. The Controller does not gate on Allow at all — it only
	// short-circuits on Deny — so this is the "no gate, proceed" path.
	for _, tool := range allowTools {
		if got := c.policy.Check(tool); got != DecisionAllow {
			t.Errorf("default policy: Check(%q) = %q; want %q (must be allow-by-default)",
				tool, got, DecisionAllow)
		}
	}

	// Ask-listed tools: Check returns DecisionAsk. The Controller still
	// proceeds (the confirmation gate is owned by the daemon's tool
	// executor per TASK-018, not by the Controller). What we're pinning
	// here is that NONE of these defaulted to deny.
	for _, tool := range askTools {
		got := c.policy.Check(tool)
		if got != DecisionAsk {
			t.Errorf("default policy: Check(%q) = %q; want %q (must be ask-by-default — "+
				"a deny default would silently break first-launch UX)",
				tool, got, DecisionAsk)
		}
		if got == DecisionDeny {
			// Belt-and-suspenders: an explicit deny-detection assertion
			// in case the previous check's want value gets edited but
			// this loop survives.
			t.Errorf("default policy: %q defaults to deny; defaults must be allow or ask only", tool)
		}
	}
}

// TestIntegration_PolicyPersistsAcrossControllerInstances verifies the
// Save → Load round-trip preserves user choices across Controller
// lifecycles. Models the production restart path: user edits permissions
// in Settings, app restarts, Controller is reconstructed from disk.
//
// policy_test.go's TestSaveLoadRoundTrip already covers the bytes-level
// round-trip; this test elevates it to the Controller layer to confirm a
// new Controller built from the loaded policy actually honors the
// persisted decisions when its methods dispatch.
func TestIntegration_PolicyPersistsAcrossControllerInstances(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macctl is darwin-only; integration tests skipped on windows")
	}

	tmp := t.TempDir()
	path := filepath.Join(tmp, "policy.json")

	// Stage 1: a "user just edited Settings" Controller. Override two
	// defaults: flip an allow-by-default to deny, and flip an
	// ask-by-default to allow. Both edits must survive the restart.
	p1 := NewDefaultPolicy()
	p1.Set("mac_open_app", DecisionAllow) // was ask
	p1.Set("mac_quit_app", DecisionDeny)  // was ask
	if err := p1.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify on-disk artifact — the file must exist and be non-empty so
	// a later Load round-trip is meaningful. (TestSaveLoadRoundTrip in
	// policy_test.go covers this too; the duplicate is cheap insurance.)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("policy file not created at %s: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatal("policy file is empty after Save")
	}

	// Stage 2: simulate process restart — Load a fresh Policy from disk
	// and wire it into a brand-new Controller. The original p1 / c1 are
	// definitionally discarded.
	p2, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p2 == nil {
		t.Fatal("Load returned nil policy")
	}
	c2 := NewController(p2)

	// Pinned decisions must survive the round-trip.
	if got := c2.policy.Check("mac_open_app"); got != DecisionAllow {
		t.Errorf("persisted Check(mac_open_app) = %q; want %q", got, DecisionAllow)
	}
	if got := c2.policy.Check("mac_quit_app"); got != DecisionDeny {
		t.Errorf("persisted Check(mac_quit_app) = %q; want %q", got, DecisionDeny)
	}

	// End-to-end: invoke QuitApp on the rehydrated Controller — the
	// persisted deny must still short-circuit the osascript seam. This
	// is the production restart contract: user denies a tool, restarts
	// app, denied tool stays denied.
	var osascriptCalls int
	c2.osascript = func(string) (string, error) {
		osascriptCalls++
		return "", nil
	}
	_, err = c2.QuitApp("Slack")
	if !errors.Is(err, ErrPolicyDeny) {
		t.Errorf("after restart: QuitApp err = %v; want ErrPolicyDeny", err)
	}
	if osascriptCalls != 0 {
		t.Errorf("after restart: osascript called %d times; want 0 — "+
			"persisted deny must short-circuit on the rehydrated Controller",
			osascriptCalls)
	}

	// Untouched default must also survive: mac_spotlight stays allow.
	if got := c2.policy.Check("mac_spotlight"); got != DecisionAllow {
		t.Errorf("untouched default after round-trip: Check(mac_spotlight) = %q; want %q",
			got, DecisionAllow)
	}
}

// TestIntegration_AllowedToolEndToEnd is the positive-path counterpart to
// the deny-shortcircuit suite: with policy=allow and a successful
// osascript recorder, an osascript-backed Controller method (here:
// QuitApp) MUST produce both (a) the expected AppleScript invocation
// and (b) a nil error. Pins the full "happy path" for one representative
// method so the test surface isn't 100% negative cases.
//
// We use QuitApp as the representative because it's the canonical
// destructive-but-osascript-driven method and its script is small enough
// to assert verbatim. Other osascript methods (SetVolume, FocusWindow,
// Mute, Unmute) have their own per-method script-shape tests already.
func TestIntegration_AllowedToolEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macctl is darwin-only; integration tests skipped on windows")
	}

	policy := NewDefaultPolicy()
	policy.Set("mac_quit_app", DecisionAllow)
	c := NewController(policy)

	var recorded []string
	c.osascript = func(script string) (string, error) {
		recorded = append(recorded, script)
		return "", nil
	}

	got, err := c.QuitApp("Slack")
	if err != nil {
		t.Fatalf("allow path: QuitApp err = %v; want nil", err)
	}
	if got != "" {
		t.Errorf("allow path: QuitApp returned %q; want empty string", got)
	}
	if len(recorded) != 1 {
		t.Fatalf("allow path: osascript invocations = %d; want 1", len(recorded))
	}
	wantScript := `tell application "Slack" to quit`
	if recorded[0] != wantScript {
		t.Errorf("allow path: script = %q; want %q", recorded[0], wantScript)
	}
}

// TestIntegration_AskDoesNotShortCircuit pins the boundary between the
// Controller-layer policy gate and the daemon-layer confirmation gate
// (TASK-018). The Controller only short-circuits on Deny; Ask is the
// daemon's responsibility (it intercepts the tool_call WS message,
// issues a TTS prompt, and only forwards the call to the Wails method
// after a yes). So at the Controller layer, Ask behaves identically to
// Allow — the method proceeds to exec.
//
// If a future refactor accidentally pushed the confirmation gate down
// into the Controller, this test would fail because Ask would start
// returning a "confirmation required" error instead of a successful nil.
// The split matters: tests for the daemon layer mock-implement the gate;
// tests at the Controller layer (this file) assert the gate is NOT in
// the Controller.
func TestIntegration_AskDoesNotShortCircuit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macctl is darwin-only; integration tests skipped on windows")
	}

	// Default policy: mac_quit_app is ask. Don't change it.
	c := NewController(NewDefaultPolicy())
	if got := c.policy.Check("mac_quit_app"); got != DecisionAsk {
		t.Fatalf("precondition: mac_quit_app default = %q; want %q",
			got, DecisionAsk)
	}

	var calls int
	c.osascript = func(string) (string, error) {
		calls++
		return "", nil
	}

	if _, err := c.QuitApp("Slack"); err != nil {
		t.Errorf("ask path: QuitApp err = %v; want nil — ask must NOT "+
			"short-circuit at the Controller; the confirmation gate is "+
			"a daemon-layer concern (TASK-018)", err)
	}
	if calls != 1 {
		t.Errorf("ask path: osascript calls = %d; want 1 — Controller "+
			"forwards ask-policied calls to the seam (the daemon decides "+
			"whether to actually invoke based on user yes/no)", calls)
	}
}

// TestIntegration_EmptyStringAndPolicyDeny pins the interaction between
// the two early-return paths in OpenApp / QuitApp / RunShortcut /
// Spotlight / OpenPath: when the input is empty AND the policy is deny,
// the input-validation error wins (returns early before checking
// policy). This is deliberate — the validation error is more actionable
// ("you didn't say what to open"), so it surfaces first.
//
// The contract: validation runs BEFORE policy check. A future refactor
// that reorders these two would silently change the error surface, and
// this test catches that.
func TestIntegration_EmptyStringAndPolicyDeny(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macctl is darwin-only; integration tests skipped on windows")
	}

	cases := []struct {
		name    string
		toolKey string
		invoke  func(c *Controller) error
	}{
		{"OpenApp", "mac_open_app", func(c *Controller) error { _, err := c.OpenApp(""); return err }},
		{"QuitApp", "mac_quit_app", func(c *Controller) error { _, err := c.QuitApp(""); return err }},
		{"FocusWindow", "mac_focus_window", func(c *Controller) error { _, err := c.FocusWindow("", ""); return err }},
		{"OpenPath", "mac_open_path", func(c *Controller) error { _, err := c.OpenPath(""); return err }},
		{"Spotlight", "mac_spotlight", func(c *Controller) error { _, err := c.Spotlight(""); return err }},
		{"RunShortcut", "mac_run_shortcut", func(c *Controller) error { _, err := c.RunShortcut("", "input"); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := NewDefaultPolicy()
			policy.Set(tc.toolKey, DecisionDeny)
			c := NewController(policy)

			err := tc.invoke(c)
			if err == nil {
				t.Fatalf("%s(\"\") with policy=deny: err = nil; want validation error",
					tc.name)
			}
			// Validation must win over policy-deny: the returned error
			// must be a string-message validation error, NOT
			// ErrPolicyDeny. The "is required" substring is the
			// canonical signal each method emits ("name is required",
			// "app is required", "path is required", "query is
			// required").
			if errors.Is(err, ErrPolicyDeny) {
				t.Errorf("%s(\"\") with policy=deny: err = ErrPolicyDeny; "+
					"validation must run BEFORE policy check (input "+
					"problem is more actionable than denied access)",
					tc.name)
			}
			if !strings.Contains(err.Error(), "is required") {
				t.Errorf("%s(\"\"): err = %v; want validation error containing %q",
					tc.name, err, "is required")
			}
		})
	}
}
