package macctl

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ListShortcuts returns the names of every user-installed Shortcut in
// Shortcuts.app. Shells `shortcuts list --output-format json`.
//
// Different macOS versions emit different JSON shapes:
//   - Newer (Ventura+): an array of objects with a "name" key.
//   - Older / minimal: a bare array of strings.
//   - Some hosts: newline-delimited names with no JSON at all.
//
// We try each shape in order; the first one that parses non-empty wins.
// Read-only by policy default ("allow") -- the daemon-side dispatcher
// gates this on policy.Check("mac_list_shortcuts") anyway.
//
// Per project convention, returns []string{} (never nil) on the empty
// case so the Wails serializer renders JSON `[]` instead of `null`.
func (c *Controller) ListShortcuts() ([]string, error) {
	if d := c.policy.Check("mac_list_shortcuts"); d == DecisionDeny {
		return nil, ErrPolicyDeny
	}
	out, err := exec.Command("shortcuts", "list", "--output-format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("ListShortcuts: %w", err)
	}
	// Shape 1: array of objects with a "name" key (modern macOS).
	var asObjects []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &asObjects); err == nil && len(asObjects) > 0 {
		names := make([]string, len(asObjects))
		for i, o := range asObjects {
			names[i] = o.Name
		}
		return names, nil
	}
	// Shape 2: bare array of strings.
	var asStrings []string
	if err := json.Unmarshal(out, &asStrings); err == nil && len(asStrings) > 0 {
		return asStrings, nil
	}
	// Shape 3: newline-delimited names (some hosts ignore --output-format).
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return []string{}, nil
	}
	return strings.Split(raw, "\n"), nil
}

// builtinShortcuts maps lowercased common shortcut names to native
// osascript commands. Lets voice commands like "lock my screen" or
// "sleep the mac" work without the user having to import a `.shortcut`
// file into Shortcuts.app first. The system prompt advertises these
// names, so they need to actually do something.
//
// Keys are lowercased and stripped of punctuation for matching; values
// are osascript snippets that don't need stdin input.
var builtinShortcuts = map[string]string{
	"lock screen":          `tell application "System Events" to keystroke "q" using {control down, command down}`,
	"lock":                 `tell application "System Events" to keystroke "q" using {control down, command down}`,
	"sleep":                `tell application "System Events" to sleep`,
	"sleep mac":            `tell application "System Events" to sleep`,
	"open downloads":       `tell application "Finder" to open folder "Downloads" of (path to home folder)`,
	"downloads":            `tell application "Finder" to open folder "Downloads" of (path to home folder)`,
	"empty trash":          `tell application "Finder" to empty trash`,
	"show desktop":         `tell application "System Events" to key code 103`,
	"mission control":      `tell application "System Events" to key code 160`,
	"toggle do not disturb": `tell application "System Events" to keystroke "d" using {option down, command down}`,
	"toggle dnd":           `tell application "System Events" to keystroke "d" using {option down, command down}`,
}

// RunShortcut runs the named Shortcut, optionally piping `input` to its
// stdin. Returns the shortcut's stdout (trimmed).
//
// Resolution order:
//  1. If `name` matches a built-in (Lock Screen, Sleep, etc.), run the
//     equivalent osascript. This avoids the user having to import a
//     `.shortcut` file into Shortcuts.app before basic system actions
//     become voice-controllable.
//  2. Otherwise shell `shortcuts run <name>` and let Shortcuts.app
//     resolve it against the user's installed library.
//
// When input is empty we omit --input-path entirely; passing "" + stdin
// would make the shortcut read an empty payload, which is subtly
// different from "no input at all" for some Shortcuts (e.g. "Take Note"
// would create an empty note rather than prompting).
//
// Destructive -- gated on policy.Check("mac_run_shortcut"); DecisionDeny
// short-circuits before the shortcuts CLI is invoked.
func (c *Controller) RunShortcut(name, input string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("RunShortcut: name is required")
	}
	if d := c.policy.Check("mac_run_shortcut"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	// Built-in path: voice-friendly shortcuts that don't require the
	// user to have anything imported into Shortcuts.app. Input is
	// ignored here since these are parameterless system actions.
	if script, ok := builtinShortcuts[strings.ToLower(strings.TrimSpace(name))]; ok {
		out, err := exec.Command("osascript", "-e", script).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("RunShortcut(%q) builtin: %s: %w",
				name, strings.TrimSpace(string(out)), err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	args := []string{"run", name}
	if input != "" {
		// `--input-path -` tells `shortcuts` to read the input from stdin.
		// This works for both small (single-line) and large (multi-KB)
		// inputs without command-line length limits.
		args = append(args, "--input-path", "-")
	}
	cmd := exec.Command("shortcuts", args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("RunShortcut(%q): %s: %w",
			name, strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}
