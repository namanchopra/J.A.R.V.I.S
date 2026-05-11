//go:build darwin

package notify

import (
	"fmt"
	"os/exec"
)

// Send delivers a macOS notification using osascript.
func Send(title, message string) error {
	script := fmt.Sprintf(`display notification %q with title %q`, message, title)
	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify: osascript: %w", err)
	}
	return nil
}
