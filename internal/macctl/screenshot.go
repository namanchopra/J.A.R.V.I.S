package macctl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
)

// Screenshot shells `screencapture` and writes a PNG to
// ~/.jarvis/screenshots/<unix>.png, returning the absolute path. `target`
// selects the capture mode:
//
//	"screen"    — entire main display (non-interactive).
//	"window"    — interactive window picker (click a window to capture).
//	"selection" — interactive crosshair-drag rectangle.
//
// The two interactive modes can be cancelled by the user (Esc), in which
// case screencapture exits 0 with no file written. We post-check the
// output path via os.Stat to distinguish that "user cancelled" outcome
// from a genuine success — returning a clear error instead of a phantom
// path that the daemon would try to upload.
//
// Flag rationale:
//
//	-x    — silent: suppress the camera shutter sound. The daemon spoken
//	        response provides feedback; the system sound is just noise.
//	-W    — interactive window selection (used together with -o for "window"
//	        target). The -W flag picks the window the user clicks.
//	-o    — no window shadow. Cleaner crops for downstream OCR / vision.
//	-s    — interactive selection (drag a rectangle).
//
// Policy gate: mac_screenshot defaults to allow (read-only — the
// resulting file lives under the user's own ~/.jarvis tree, the screen
// content is the user's own work, and the interactive modes require an
// explicit user gesture before any pixels are captured).
//
// Invalid-target validation uses a substring-matchable "invalid" error
// message rather than referencing the ErrInvalidArg sentinel directly.
// Reason: ErrInvalidArg is added to macctl.go by TASK-012's parallel work
// and the landing order is non-deterministic. The substring-match contract
// is robust to either landing order — the daemon's tool layer matches on
// "invalid" anyway, and TASK-012 may rewrap the same message later without
// breaking the assertion. If you're cleaning up after both TASK-012 and
// TASK-013 land, feel free to switch this to fmt.Errorf("...: %w", ErrInvalidArg).
func (c *Controller) Screenshot(target string) (string, error) {
	if d := c.policy.Check("mac_screenshot"); d == DecisionDeny {
		return "", ErrPolicyDeny
	}
	dir := paths.DataPath("screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("Screenshot: mkdir %s: %w", dir, err)
	}
	// Unix-second granularity is sufficient — a user voicing back-to-back
	// "screenshot" commands would have to fire two within a single second
	// for the collision to matter, and the second one would simply overwrite
	// the first PNG (a behaviour we'd want anyway since the older one is
	// definitionally stale at that point).
	out := filepath.Join(dir, fmt.Sprintf("%d.png", time.Now().Unix()))

	var args []string
	switch target {
	case "screen":
		args = []string{"-x", out}
	case "window":
		args = []string{"-Wo", "-x", out}
	case "selection":
		args = []string{"-s", "-x", out}
	default:
		return "", fmt.Errorf("Screenshot: invalid target %q: must be screen|window|selection", target)
	}

	if err := exec.Command("screencapture", args...).Run(); err != nil {
		// `screencapture` exits non-zero with no useful stderr when the
		// host app lacks Screen Recording entitlement (TCC denies the
		// frame grab silently). The exec error is just "exit status 1".
		// Probe for the missing-permission case by checking whether the
		// output file was actually written — TCC denial produces a
		// nonzero exit AND no file. A genuine syscall failure usually
		// also produces no file, but the user-facing fix is the same:
		// point them at System Settings.
		info, statErr := os.Stat(out)
		if statErr != nil || info.Size() == 0 {
			return "", fmt.Errorf("Screenshot(%s): screen recording permission likely missing — enable it for the host app in System Settings > Privacy & Security > Screen Recording: %w", target, err)
		}
		return "", fmt.Errorf("Screenshot(%s): %w", target, err)
	}

	// User-cancellation check. screencapture exits 0 even when the user
	// presses Esc out of "window"/"selection" — the only signal is that
	// no file was written. A bare ENOENT here would be misleading
	// ("screenshot tool broken?"), so we surface a human-grokkable error
	// that the daemon can translate to "I didn't capture anything — let
	// me know if you want to try again."
	info, statErr := os.Stat(out)
	if statErr != nil || info.Size() == 0 {
		return "", fmt.Errorf("Screenshot(%s): no file written (user cancelled?)", target)
	}
	return out, nil
}
