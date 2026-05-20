package macctl

import (
	"fmt"
	"os/exec"
	"strings"
)

// lookPathFn is a test seam mirroring osascriptFn: production uses
// exec.LookPath, tests can swap in a stub to simulate the `brightness`
// CLI being absent without mutating the host's PATH. Kept as a package
// variable rather than a Controller field because LookPath is a static
// process-wide concept -- every Controller in a process sees the same
// PATH -- and tests can guard the swap with t.Cleanup.
var lookPathFn = exec.LookPath

// runCmdFn is the test seam for shelling out to non-osascript CLIs
// (`brightness`, `shortcuts`). Production wires through exec.Command's
// CombinedOutput; tests substitute a recorder so they can assert which
// argv was issued and pin the deny short-circuit.
var runCmdFn = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// SetBrightness sets the display brightness to pct (0..100). The macOS
// public APIs for brightness are private/locked-down, so we shell out
// to the third-party `brightness` CLI (homebrew's `brightness` formula:
// `brew install brightness`). The CLI accepts a float 0.0..1.0, so we
// rescale the integer percent.
//
// If `brightness` is not installed we return ErrToolUnavailable wrapped
// with a clear install hint so the daemon's tool layer can render it
// into a spoken response like "Install the brightness CLI via brew
// install brightness." Avoids the user staring at a generic "command
// not found".
//
// Policy name: "mac_set_brightness". Deny short-circuits before the
// LookPath/exec invocation.
func (c *Controller) SetBrightness(pct int) (string, error) {
	if pct < 0 || pct > 100 {
		return "", fmt.Errorf("SetBrightness(%d): %w: pct must be in 0..100", pct, ErrInvalidArg)
	}
	if d := c.policy.Check("mac_set_brightness"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	if _, err := lookPathFn("brightness"); err != nil {
		return "", fmt.Errorf("SetBrightness: %w: install via `brew install brightness`",
			ErrToolUnavailable)
	}
	// brightness expects a float 0.0..1.0. Format with 2 decimal places
	// -- finer precision is below the perceptual threshold and noisier
	// in logs.
	level := float64(pct) / 100.0
	arg := fmt.Sprintf("%.2f", level)
	out, err := runCmdFn("brightness", arg)
	if err != nil {
		return "", fmt.Errorf("SetBrightness(%d): %s: %w", pct,
			strings.TrimSpace(string(out)), err)
	}
	return "", nil
}

// ToggleDND toggles macOS Do Not Disturb / Focus. macOS Monterey+
// removed the public `defaults write` recipe (private API surface), so
// the only safe public path is the Shortcuts.app "Set Focus" action,
// invoked via the `shortcuts` CLI.
//
// The user must have a shortcut named "Set Focus" installed (Apple
// ships one in the gallery, and TASK-016 bundles one as a fallback).
// If the shortcut doesn't exist `shortcuts run` returns a non-zero exit
// code with a message in stderr -- we bubble that up wrapped so the
// daemon can surface "Add the Set Focus shortcut from the gallery".
//
// Policy name: "mac_toggle_dnd". Deny short-circuits before exec.
func (c *Controller) ToggleDND() (string, error) {
	if d := c.policy.Check("mac_toggle_dnd"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	if _, err := lookPathFn("shortcuts"); err != nil {
		return "", fmt.Errorf("ToggleDND: %w: `shortcuts` CLI is required (macOS Monterey+)",
			ErrToolUnavailable)
	}
	out, err := runCmdFn("shortcuts", "run", "Set Focus")
	if err != nil {
		return "", fmt.Errorf("ToggleDND: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return "", nil
}
