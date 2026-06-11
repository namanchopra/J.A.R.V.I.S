package syscontrol

// DisplayController controls per-display tunables: backlight brightness
// and the system "do not disturb" / Focus mode. Both have notoriously
// private OS APIs, so implementations typically shell out to a
// third-party CLI (macOS: `brightness` for the backlight, `shortcuts`
// for Focus) or hit a platform-specific syscall (Windows: WMI's
// WmiSetBrightness, Focus Assist registry key — see TASK-023, TASK-024).
//
// Implementations gate destructive operations through their own policy
// layer (see internal/macctl/policy.go for the macOS reference) before
// performing any side effect. Range validation happens before the
// policy gate so callers get the more actionable error.
type DisplayController interface {
	// SetBrightness sets the primary display's backlight to pct
	// (0..100). Values outside that range MUST be rejected with a
	// wrapped error (see internal/macctl.ErrInvalidArg) before any
	// side effect. When the required external dependency is missing
	// (macOS: the `brightness` CLI; Windows: a WMI-capable monitor),
	// implementations return a typed "tool unavailable" error (see
	// internal/macctl.ErrToolUnavailable) wrapped with an install
	// hint so the daemon can render a remediation message.
	SetBrightness(pct int) (string, error)

	// ToggleDND flips the system "do not disturb" / Focus state.
	// macOS routes through the `shortcuts` CLI's "Set Focus" entry;
	// Windows TASK-024 toggles the Focus Assist registry key.
	// Implementations MUST return a wrapped error when the
	// underlying dependency is absent rather than panicking — this
	// is purely a quality-of-life tool and degrading gracefully is
	// the right behaviour.
	ToggleDND() (string, error)
}
