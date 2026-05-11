package terminal

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const osascriptTimeout = 5 * time.Second

// runAppleScript executes an AppleScript snippet via osascript and returns the
// trimmed stdout. It enforces a 5-second timeout.
func runAppleScript(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), osascriptTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("osascript: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// escapeAppleScript sanitizes a string for safe embedding inside an
// AppleScript double-quoted string literal. It escapes backslashes first,
// then double-quotes.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
