package macctl

import (
	"fmt"
	"os/exec"
	"strings"
)

// OpenApp activates the app named `name`. Uses `open -a <Name>`, which
// is more reliable than osascript for launching apps not yet running.
func (c *Controller) OpenApp(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("OpenApp: name is required")
	}
	if d := c.policy.Check("mac_open_app"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	out, err := exec.Command("open", "-a", name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("OpenApp(%q): %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return "", nil
}

// QuitApp tells the app to quit gracefully.
func (c *Controller) QuitApp(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("QuitApp: name is required")
	}
	if d := c.policy.Check("mac_quit_app"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	script := fmt.Sprintf(`tell application %q to quit`, name)
	if _, err := c.osascript(script); err != nil {
		return "", fmt.Errorf("QuitApp(%q): %w", name, err)
	}
	return "", nil
}
