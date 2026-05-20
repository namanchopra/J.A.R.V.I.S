package macctl

import (
	"fmt"
)

// SetVolume sets the system output volume to pct (0..100). Issues
// `osascript -e "set volume output volume <pct>"` which is the
// publicly-documented recipe (`man osascript`, Apple's AppleScript
// Language Guide). Values outside 0..100 are rejected with
// ErrInvalidArg before any side effect -- a stray voice misfire
// ("Set volume to a hundred and fifty") shouldn't reach osascript.
//
// Policy is checked under the canonical name "mac_set_volume"; a
// DecisionDeny short-circuits before the osascript invocation so the
// deny invariant holds for the audit log.
func (c *Controller) SetVolume(pct int) (string, error) {
	// Validate first so a bad input is rejected even when policy=deny
	// would also block it -- surfaces the more actionable error to the
	// caller ("out of range" beats "denied").
	if pct < 0 || pct > 100 {
		return "", fmt.Errorf("SetVolume(%d): %w: pct must be in 0..100", pct, ErrInvalidArg)
	}
	if d := c.policy.Check("mac_set_volume"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	script := fmt.Sprintf("set volume output volume %d", pct)
	if _, err := c.osascript(script); err != nil {
		return "", fmt.Errorf("SetVolume(%d): %w", pct, err)
	}
	return "", nil
}

// Mute sets the output-muted property to true. Idempotent -- muting a
// muted device is a no-op at the AppleScript layer.
//
// Policy name: "mac_mute". Deny short-circuits before osascript.
func (c *Controller) Mute() (string, error) {
	if d := c.policy.Check("mac_mute"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	script := "set volume with output muted"
	if _, err := c.osascript(script); err != nil {
		return "", fmt.Errorf("Mute: %w", err)
	}
	return "", nil
}

// Unmute clears the output-muted property. Counterpart to Mute. Like
// Mute, idempotent -- unmuting a non-muted device is a no-op.
//
// Policy name: "mac_unmute". Deny short-circuits before osascript.
func (c *Controller) Unmute() (string, error) {
	if d := c.policy.Check("mac_unmute"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	script := "set volume without output muted"
	if _, err := c.osascript(script); err != nil {
		return "", fmt.Errorf("Unmute: %w", err)
	}
	return "", nil
}
