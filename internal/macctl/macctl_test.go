package macctl

import (
	"errors"
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
		{"OpenApp", func() (string, error) { return c.OpenApp("Safari") }},
		{"QuitApp", func() (string, error) { return c.QuitApp("Safari") }},
		{"FocusWindow", func() (string, error) { return c.FocusWindow("Slack", "general") }},

		// --- TASK-012: audio + display ---
		{"SetVolume", func() (string, error) { return c.SetVolume(50) }},
		{"Mute", func() (string, error) { return c.Mute() }},
		{"Unmute", func() (string, error) { return c.Unmute() }},
		{"SetBrightness", func() (string, error) { return c.SetBrightness(75) }},
		{"ToggleDND", func() (string, error) { return c.ToggleDND() }},

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
