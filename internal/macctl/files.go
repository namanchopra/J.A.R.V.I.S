package macctl

import (
	"fmt"
	"os/exec"
	"strings"
)

// OpenPath shells `open <path>` to launch a filesystem path or URL via
// LaunchServices. The same command works uniformly for both — `open
// /Users/x/foo.pdf` opens the PDF in Preview, `open https://example.com`
// opens the URL in the default browser, and `open file:///nonexistent`
// produces a non-zero exit with a stderr message ("The file ... does not
// exist") that we surface verbatim in the wrapped error.
//
// CombinedOutput is used (rather than Output) so the stderr text gets
// folded into the error message. Without that, the only signal a caller
// gets on a missing path is exit code 1, which is the "silent success"
// trap the TASK-013 acceptance criteria explicitly call out.
//
// Policy gate: mac_open_path defaults to ask.
func (c *Controller) OpenPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("OpenPath: path is required")
	}
	if d := c.policy.Check("mac_open_path"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	out, err := exec.Command("open", path).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("OpenPath(%q): %s: %w", path, strings.TrimSpace(string(out)), err)
	}
	return "", nil
}

// Spotlight runs `mdfind <query>` and returns up to 20 matching absolute
// paths joined by '\n'. Read-only — never modifies user state. The 20-hit
// cap is chosen to match the spoken-response budget: the daemon's TTS
// layer caps responses around ~30s and 20 file paths is a reasonable
// "say the first batch, ask the user to narrow" boundary.
//
// Bare `Output()` (not CombinedOutput) is used because mdfind's stderr
// is reserved for genuine errors (broken index, malformed query); we
// don't want it appended to the result when the query simply has no
// hits (which returns "" + exit 0).
//
// Policy gate: mac_spotlight defaults to allow.
func (c *Controller) Spotlight(query string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("Spotlight: query is required")
	}
	if d := c.policy.Check("mac_spotlight"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	out, err := exec.Command("mdfind", query).Output()
	if err != nil {
		return "", fmt.Errorf("Spotlight(%q): %w", query, err)
	}
	// TrimSpace removes the trailing '\n' mdfind emits; Split on a
	// fully-empty result would otherwise yield [""] (one empty string)
	// which we'd then return as the literal "" — confusing for callers
	// expecting an empty-string success indicator.
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 20 {
		lines = lines[:20]
	}
	return strings.Join(lines, "\n"), nil
}
