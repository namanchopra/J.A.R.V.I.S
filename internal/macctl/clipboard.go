package macctl

import (
	"fmt"
	"os/exec"
	"strings"
)

// ClipboardGet shells `pbpaste` and returns the current clipboard text
// verbatim — no trimming, no normalization. The daemon's spoken-response
// layer handles "empty clipboard" framing, so we faithfully return ""
// (with nil error) when nothing is on the pasteboard rather than synthesising
// a placeholder string.
//
// Policy gate: mac_clipboard_get defaults to allow (read-only).
func (c *Controller) ClipboardGet() (string, error) {
	if d := c.policy.Check("mac_clipboard_get"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", fmt.Errorf("ClipboardGet: %w", err)
	}
	return string(out), nil
}

// ClipboardSet shells `pbcopy` and writes text to the clipboard via stdin.
// `pbcopy` reads its payload from stdin (it has no string-argument mode),
// so we hand it a strings.NewReader and call Run — no Output/CombinedOutput
// needed since pbcopy produces no stdout on success.
//
// Destructive: silently overwrites whatever the user had previously copied.
// Policy gate: mac_clipboard_set defaults to ask.
func (c *Controller) ClipboardSet(text string) (string, error) {
	if d := c.policy.Check("mac_clipboard_set"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ClipboardSet: %w", err)
	}
	return "", nil
}
