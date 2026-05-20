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

// RunShortcut runs the named Shortcut, optionally piping `input` to its
// stdin. Returns the shortcut's stdout (trimmed).
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
