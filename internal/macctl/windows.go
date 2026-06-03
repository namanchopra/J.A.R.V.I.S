package macctl

import (
	"fmt"
	"strings"
)

// FocusWindow brings a window of `app` matching `title` substring to the
// front via System Events. Returns ErrWindowNotFound if no match.
func (c *Controller) FocusWindow(app, title string) (string, error) {
	if app == "" {
		return "", fmt.Errorf("FocusWindow: app is required")
	}
	if d := c.policy.Check("mac_focus_window"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	// Two-step: (1) activate the app, (2) raise the matching window via
	// System Events. If title is empty, just activates the app — the
	// frontmost window wins.
	activateScript := fmt.Sprintf(`tell application %q to activate`, app)
	if _, err := c.osascript(activateScript); err != nil {
		return "", fmt.Errorf("FocusWindow: activate %q: %w", app, err)
	}
	if title == "" {
		return "", nil
	}
	// Find a window whose name contains `title`.
	script := fmt.Sprintf(`
tell application "System Events"
  tell process %q
    set theWindows to every window
    repeat with w in theWindows
      if name of w contains %q then
        set value of attribute "AXMain" of w to true
        return "ok"
      end if
    end repeat
  end tell
end tell`, app, title)
	out, err := c.osascript(script)
	if err != nil {
		return "", fmt.Errorf("FocusWindow: %w", err)
	}
	if !strings.Contains(out, "ok") {
		return "", ErrWindowNotFound
	}
	return "", nil
}
