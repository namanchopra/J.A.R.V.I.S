//go:build windows

// displaycontroller_windows.go is the Windows backend for the
// DisplayController interface declared in displaycontroller.go. It
// implements SetBrightness via WMI's WmiSetBrightness method on the
// root\WMI namespace, invoked through PowerShell so we don't need a
// cgo / direct-COM dependency just to flip a backlight. ToggleDND
// (TASK-024) flips the global toast-notifications setting at
// HKCU\Software\Microsoft\Windows\CurrentVersion\PushNotifications
// (ToastEnabled DWORD) — the same key the Settings → Notifications
// "Notifications" master switch writes — which is the most reliable
// public surface for Focus-Assist-style suppression across Win10 and
// Win11. The Win11-only Focus Sessions API (QuietHoursProfile COM
// surface) is intentionally not used because it's undocumented and
// has shipped breaking changes in three Win11 feature updates so
// far; the toast-enabled registry value, by contrast, has been
// stable since Windows 8.
//
// Why PowerShell and not direct WMI COM:
//
//   - The plan (TASK-023) explicitly allows "WMI WmiSetBrightness via
//     PowerShell or direct syscall to monitor.dll" — PowerShell is
//     significantly simpler and avoids pulling in another cgo / go-ole
//     surface area for a single brightness call.
//   - The PowerShell one-liner mirrors the macOS reference backend's
//     "shell out to the third-party `brightness` CLI" pattern in
//     internal/macctl/display.go, which keeps the two platforms'
//     failure-mode taxonomies symmetric (LookPath miss -> typed tool
//     unavailable error, exec failure -> wrapped exec error).
//   - PowerShell.exe ships with every supported Windows SKU (it is a
//     Component Object Model / OS component, not an optional install),
//     so the "tool unavailable" branch is genuinely a last-resort
//     diagnostic — typically only reached on a stripped Nano Server or
//     a misconfigured PATH.
//
// External-monitor caveat: WmiMonitorBrightnessMethods only enumerates
// integrated panels (laptop displays / all-in-one PCs). Desktops with a
// standalone monitor get back an empty collection, which surfaces as a
// "No instance(s) available" PowerShell error — we detect that and
// return a stable sentinel so callers can render the canonical "only
// the built-in display supports brightness control" message rather than
// leaking raw PowerShell stderr.
//
// Headless caveat: when the system has no display at all (Server Core,
// CI runner, kiosk in a closed state) the same empty-collection error
// fires; we treat it the same way — typed error, not panic. The
// acceptance criterion "headless returns error not panic" is satisfied
// because every failure mode below funnels through fmt.Errorf rather
// than through a nil-dereference / index-out-of-range path.

package syscontrol

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// ErrExternalMonitorBrightness is returned by SetBrightness when the
// system has no integrated panel — typically a desktop PC driving one
// or more external monitors over HDMI/DisplayPort/DVI. WmiSetBrightness
// only works on panels that expose the WmiMonitorBrightnessMethods
// instance (laptop LCDs, all-in-one PCs, some tablets); external
// monitors must be controlled via DDC/CI which is a different API
// surface entirely and outside the scope of this task.
//
// Distinct from ErrUnsupportedPlatform so callers can render a
// specific "external monitor brightness is not supported" message
// rather than the generic platform-fallback copy.
var ErrExternalMonitorBrightness = errors.New("syscontrol: brightness control only supported on integrated displays (external monitors require DDC/CI)")

// dndRegistryPath is the HKCU subkey that holds the global toast-
// enabled flag. The "ToastEnabled" DWORD under this path is what
// Settings → System → Notifications writes when the user flips the
// master switch, which makes it the canonical public surface for
// suppressing notifications system-wide. The key has existed since
// Windows 8 and is preserved across Win10 → Win11 upgrades, so a
// single code path covers every supported SKU.
//
// Kept as a package var so tests can repoint the toggle at a scratch
// subkey (e.g. HKCU\Software\Jarvis\Test\PushNotifications) without
// mutating the real user setting. Production code never reassigns
// this — only tests do, guarded by t.Cleanup.
var dndRegistryPath = `Software\Microsoft\Windows\CurrentVersion\PushNotifications`

// dndRegistryValueName is the DWORD value under dndRegistryPath that
// gates whether toast notifications appear. 0 == suppressed (DND
// on), 1 == normal (DND off). Stored as a var (not const) for the
// same test-seam reason as dndRegistryPath above.
var dndRegistryValueName = "ToastEnabled"

// dndOpenKeyFn is the test seam for opening the HKCU subkey that
// holds the toast-enabled flag. Production wires through
// registry.OpenKey with READ|SET_VALUE access; tests substitute a
// fake that records the access mask and returns a stub key so the
// branch coverage can exercise "key missing", "value missing",
// "permission denied", and the happy-path toggle without touching
// the real registry.
var dndOpenKeyFn = func(path string, access uint32) (registryKey, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, access)
	if err != nil {
		return nil, err
	}
	return realRegistryKey{k}, nil
}

// dndCreateKeyFn is the test seam for creating the HKCU subkey when
// it's missing (rare — the key ships with the OS — but possible on
// stripped LTSC images or after aggressive privacy-tool cleanups).
// We split create from open so the happy path (key present) doesn't
// pay the CreateKeyEx round-trip; only the recovery branch hits it.
var dndCreateKeyFn = func(path string) (registryKey, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil, err
	}
	return realRegistryKey{k}, nil
}

// registryKey is the minimal interface our ToggleDND logic needs
// from a registry handle. Defining it here (rather than depending
// directly on registry.Key) lets tests swap in a memory-backed fake
// without reflecting on the concrete type.
type registryKey interface {
	GetIntegerValue(name string) (uint64, uint32, error)
	SetDWordValue(name string, value uint32) error
	Close() error
}

// realRegistryKey adapts a real registry.Key to the registryKey
// interface. Trivial pass-through; exists only so tests can supply a
// fake that doesn't import the windows/registry package directly.
type realRegistryKey struct{ k registry.Key }

func (r realRegistryKey) GetIntegerValue(name string) (uint64, uint32, error) {
	return r.k.GetIntegerValue(name)
}

func (r realRegistryKey) SetDWordValue(name string, value uint32) error {
	return r.k.SetDWordValue(name, value)
}

func (r realRegistryKey) Close() error { return r.k.Close() }

// powershellLookPathFn is the test seam for locating powershell.exe.
// Production uses exec.LookPath; tests substitute a stub to simulate a
// PATH where PowerShell is missing without mutating the host environment.
// Mirrors the lookPathFn pattern in internal/macctl/display.go.
var powershellLookPathFn = exec.LookPath

// powershellRunFn is the test seam for shelling out to PowerShell.
// Production wires through exec.Command's CombinedOutput so stderr
// (which is where PowerShell writes the "No instance(s) available"
// diagnostic we care about) is captured alongside stdout. Tests
// substitute a recorder that returns canned (stdout, exitErr) pairs
// to exercise each branch (success, no instance, generic failure)
// without invoking PowerShell.
//
// Signature accepts the full argv so tests can assert on the exact
// command line issued (the WMI one-liner is non-trivial and a typo
// would silently no-op against the wrong namespace / class).
var powershellRunFn = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// noInstanceMarkers are substrings that appear in PowerShell's stderr
// when WmiMonitorBrightnessMethods returns an empty collection — either
// because the system is headless or because every connected display is
// external. We match on lowercased substrings rather than exact strings
// because PowerShell localises some of these messages and the surface
// differs between Win10 and Win11 PowerShell 5.1 vs PowerShell 7.
//
// Kept as a package var (not a const) so tests can reuse the same
// matcher without duplicating the list.
var noInstanceMarkers = []string{
	"no instance",          // EN-US: "No instance(s) available."
	"not supported",        // Some SKUs emit "WMI: Not supported"
	"invalidoperation",     // PowerShell 7 wraps it as InvalidOperation
	"empty pipe",           // pipeline emptied by Where-Object -- shouldn't fire today but future-proof
	"wmimonitorbrightness", // class-name echo present in most failure modes
}

// WindowsDisplayController is the Windows backend for DisplayController.
// It is a zero-config value type — no policy reference, no cached COM
// handles — because brightness control on Windows is a one-shot PS
// invocation with no setup cost. The struct exists primarily so the
// interface satisfaction is explicit (compile-time checked below) and
// so future state (e.g. a cached PowerShell argv for kiosk hardening)
// has somewhere to land without changing the public API.
type WindowsDisplayController struct{}

// NewWindowsDisplayController returns a ready-to-use Windows
// DisplayController. No error path because there's no eager
// initialisation: the PowerShell invocation happens lazily inside
// SetBrightness, and any environment problem (missing powershell.exe,
// no integrated panel) is reported at call time as a typed error.
func NewWindowsDisplayController() *WindowsDisplayController {
	return &WindowsDisplayController{}
}

// Compile-time assertion that *WindowsDisplayController satisfies the
// DisplayController interface. Mirrors the macctl var-asserts in
// macctl.go so a future refactor that drifts either side fails to
// compile rather than at runtime.
var _ DisplayController = (*WindowsDisplayController)(nil)

// SetBrightness sets the primary integrated display's backlight to pct
// (0..100) by invoking WmiSetBrightness on the
// root\WMI:WmiMonitorBrightnessMethods instance through PowerShell.
//
// The one-liner we run is:
//
//	(Get-CimInstance -Namespace root/WMI -ClassName WmiMonitorBrightnessMethods).WmiSetBrightness(1, <pct>)
//
// where the first argument (timeout, in seconds) is fixed at 1 — the
// transition is instantaneous on every panel we have hardware for, and
// passing 0 sometimes degrades to "use the previous timeout" on older
// drivers, so 1 is the conservative floor.
//
// Failure modes mapped to typed errors:
//
//   - pct outside 0..100              -> wrapped ErrInvalidArg-style message
//   - powershell.exe not on PATH       -> wrapped ErrUnsupportedPlatform (genuinely
//     an unsupported host: every supported
//     Windows SKU ships PowerShell)
//   - WMI returns "no instance"        -> ErrExternalMonitorBrightness
//   - any other PowerShell failure     -> wrapped exec error with combined output
//
// The status string return is reserved for human-readable context
// (currently always ""), matching the DisplayController interface
// signature so the macOS and Windows backends are interchangeable from
// the caller's perspective.
func (c *WindowsDisplayController) SetBrightness(pct int) (string, error) {
	if pct < 0 || pct > 100 {
		return "", fmt.Errorf("SetBrightness(%d): pct must be in 0..100", pct)
	}

	psPath, err := powershellLookPathFn("powershell")
	if err != nil {
		// PowerShell is part of the Windows base install on every
		// supported SKU, so its absence is effectively "not a supported
		// host" (Nano Server, stripped container image, hostile PATH).
		// We surface ErrUnsupportedPlatform so callers can render the
		// canonical platform-fallback copy instead of a generic exec
		// error — same treatment a stripped macOS host would get if
		// `osascript` were missing.
		return "", fmt.Errorf("SetBrightness: powershell.exe not found on PATH: %w", ErrUnsupportedPlatform)
	}

	// -NoProfile keeps invocation latency predictable (profile scripts
	// can add hundreds of ms on managed hosts). -NonInteractive guards
	// against any cmdlet that might prompt for input on an unattended
	// daemon. -Command takes the WMI one-liner verbatim; we deliberately
	// embed pct via Sprintf rather than via -ArgumentList so the literal
	// substitution shows up in process audit logs as a single self-
	// contained command line (easier to grep in incident response).
	psScript := fmt.Sprintf(
		`(Get-CimInstance -Namespace root/WMI -ClassName WmiMonitorBrightnessMethods).WmiSetBrightness(1, %d)`,
		pct,
	)
	out, runErr := powershellRunFn(
		psPath,
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		psScript,
	)
	if runErr != nil {
		combined := strings.ToLower(string(out))
		// External monitor / headless: WmiMonitorBrightnessMethods has
		// no instance to call against, so WmiSetBrightness raises a
		// "no instance(s) available" diagnostic. Map to the dedicated
		// sentinel so the daemon can render the canonical message.
		// We probe the lowercased combined output against several
		// localisation-robust markers because the exact wording varies
		// between PowerShell 5.1 and 7 / Win10 and Win11.
		for _, marker := range noInstanceMarkers {
			if strings.Contains(combined, marker) {
				return "", fmt.Errorf("SetBrightness(%d): %w", pct, ErrExternalMonitorBrightness)
			}
		}
		// Generic PowerShell / WMI failure — bubble the combined output
		// up wrapped so log scrapers can still pull the original
		// diagnostic out, but errors.Is callers see the wrapped error
		// rather than the raw exit-code.
		return "", fmt.Errorf("SetBrightness(%d): %s: %w", pct, strings.TrimSpace(string(out)), runErr)
	}

	return "", nil
}

// ToggleDND flips the system "do not disturb" state on Windows by
// inverting the ToastEnabled DWORD at
// HKCU\Software\Microsoft\Windows\CurrentVersion\PushNotifications.
//
// Why this key (and not the Win11 Focus Sessions COM surface):
//
//   - ToastEnabled is the master toggle written by Settings → System →
//     Notifications. Flipping it to 0 immediately suppresses every
//     toast (the same effect the user sees when they slide the master
//     switch off), which is exactly the "do not disturb" semantic the
//     interface contract requires.
//   - The key has existed unchanged since Windows 8 — both Win10 and
//     Win11, every SKU we ship to, expose the same DWORD with the
//     same semantics. The Win11-only QuietHoursProfile / Focus
//     Sessions COM surface, by contrast, has shipped breaking
//     changes in three feature updates and is still undocumented.
//   - HKCU access requires no elevation, so the daemon (running as the
//     interactive user) can flip the bit without an UAC prompt.
//
// Semantics: this is a *toggle*, not a setter, mirroring the macOS
// ToggleDND signature on internal/macctl.Controller — the interface
// (displaycontroller.go) intentionally takes no argument so the two
// platforms stay interchangeable. We read the current value, invert
// it, and write it back. If the value is missing entirely (DND was
// never touched on this profile) we treat it as the default
// (notifications enabled, value == 1) and flip to 0.
//
// Failure modes mapped to typed errors:
//
//   - subkey missing (rare; LTSC + privacy-cleanup combo) -> we create
//     it, defaulting the unread value to 1 (notifications enabled),
//     and flip from there. The key-creation branch never returns
//     ErrUnsupportedPlatform because the OS itself supports it; only
//     a permission error (e.g. mandatory-integrity-level lockdown on
//     a managed kiosk) bubbles up wrapped.
//   - value-name missing under existing key -> default to 1 then
//     toggle, same path as subkey-missing. We do not error here
//     because that's the documented OEM default (the value is
//     written lazily on first user interaction).
//   - permission denied on either OpenKey or SetDWordValue -> wrapped
//     error preserving the underlying syscall error so callers see
//     "Access is denied." in logs, with no panic and no partial
//     write.
//   - any other registry error -> wrapped with method name so
//     errors.Is / errors.As still works against the original.
//
// The status string return follows the same convention as
// SetBrightness above — currently always "" — so callers can rely on
// "" meaning "the operation completed; no extra context to surface".
func (c *WindowsDisplayController) ToggleDND() (string, error) {
	// Try to open the key with combined read + write access in a
	// single syscall — the common case (key present from a clean
	// OS install) takes exactly one round-trip. If the key is
	// genuinely missing we fall through to the create branch
	// below; any *other* error (permission denied, integrity
	// lockdown) is fatal and bubbles up wrapped.
	k, err := dndOpenKeyFn(dndRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		// registry.ErrNotExist signals "subkey doesn't exist" — we
		// can recover by creating it. Anything else (especially
		// access-denied on a managed host) is genuinely fatal.
		if !errors.Is(err, registry.ErrNotExist) {
			return "", fmt.Errorf("ToggleDND: open %s: %w", dndRegistryPath, err)
		}
		// Subkey missing — create it. This is the "recovered from a
		// stripped LTSC image" branch.
		k, err = dndCreateKeyFn(dndRegistryPath)
		if err != nil {
			return "", fmt.Errorf("ToggleDND: create %s: %w", dndRegistryPath, err)
		}
	}
	defer func() {
		// Best-effort close: there's nothing useful we can do if
		// closing a registry handle fails (the value has already
		// been written by then), and the kernel will reap the
		// handle when the process exits regardless. We log
		// nothing here for the same reason the macOS backend
		// doesn't log osascript exit codes — the deferred close is
		// plumbing, not policy.
		_ = k.Close()
	}()

	// Read the current value. registry.ErrNotExist on the *value*
	// (as opposed to the *key*) is the documented default state for
	// a profile that has never touched the master switch — treat it
	// as 1 (notifications enabled) so the toggle flips to 0 (DND
	// on), which is the intuitive first-press behaviour.
	var current uint64 = 1
	if val, _, getErr := k.GetIntegerValue(dndRegistryValueName); getErr == nil {
		current = val
	} else if !errors.Is(getErr, registry.ErrNotExist) {
		// A *real* error reading the value (corrupt hive, locked
		// key, IO error) — bubble up rather than silently defaulting,
		// because silently defaulting would mask hive corruption that
		// callers would otherwise see and remediate.
		return "", fmt.Errorf("ToggleDND: read %s\\%s: %w",
			dndRegistryPath, dndRegistryValueName, getErr)
	}

	// Toggle: any non-zero value (typically 1) means "notifications
	// on", so we flip to 0 (DND on). 0 flips back to 1.
	var next uint32 = 1
	if current != 0 {
		next = 0
	}

	if setErr := k.SetDWordValue(dndRegistryValueName, next); setErr != nil {
		return "", fmt.Errorf("ToggleDND: write %s\\%s=%d: %w",
			dndRegistryPath, dndRegistryValueName, next, setErr)
	}

	return "", nil
}
