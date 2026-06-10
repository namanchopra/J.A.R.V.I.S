//go:build windows

// Package permissions exposes Windows microphone permission checks by reading
// the CapabilityAccessManager consent store in the registry. Windows does not
// expose a synchronous "request prompt" API for desktop apps the way macOS
// AVFoundation does — the user must toggle access via Settings → Privacy →
// Microphone — so RequestMic is a no-op and we surface a deep-link from the UI.
package permissions

import (
	"golang.org/x/sys/windows/registry"
)

// consentStoreMicPath is the per-user registry key that holds the global
// "Microphone access for this device" toggle. On Windows 10 1803+ and all of
// Windows 11 this key exists by default; on older SKUs (Win7, Win8.1, some
// LTSB / Server variants) it is absent and we report "not_determined".
const consentStoreMicPath = `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\microphone`

// nonPackagedSubKey holds the consent value for Win32 / "unpackaged" apps.
// Jarvis is an unpackaged Wails build (no MSIX identity), so this is the
// subkey that governs its mic access. If the subkey is missing we fall back
// to the parent key's Value entry.
const nonPackagedSubKey = `NonPackaged`

// consentValueName is the REG_SZ entry under either key whose contents are
// "Allow" or "Deny". Any other contents are treated as not_determined so a
// future Microsoft-added third value can't be silently mis-mapped to denied.
const consentValueName = `Value`

// MicStatus reports the current microphone authorization for Jarvis on Windows
// by reading the CapabilityAccessManager consent store. The mapping is:
//
//	"Allow" -> "granted"
//	"Deny"  -> "denied"
//	missing key / unreadable value / unsupported SKU -> "not_determined"
//
// We never return "restricted" on Windows — that state is darwin-only (MDM /
// parental controls); the closest Windows analogue is a group-policy-denied
// registry value, which already maps to "denied" via the consent store.
func MicStatus() string {
	// Prefer the NonPackaged subkey because Jarvis is an unpackaged Win32 app.
	// If it's missing fall back to the parent mic key (which carries the
	// global Microphone access toggle). Both paths are non-fatal: any error
	// surfaces as "not_determined" so the UI re-prompts via the OS Settings
	// deep-link rather than treating an unreadable registry as a hard denial.
	if status, ok := readConsentValue(consentStoreMicPath + `\` + nonPackagedSubKey); ok {
		return status
	}
	if status, ok := readConsentValue(consentStoreMicPath); ok {
		return status
	}
	return "not_determined"
}

// readConsentValue opens the supplied HKCU subkey read-only, reads the "Value"
// REG_SZ entry and maps it to one of the documented MicStatus return values.
// The second return reports whether the read produced a definitive
// granted/denied answer; a false ok means the caller should try a fallback
// key (or default to not_determined). We swallow all errors here on purpose:
// missing key, missing value, wrong value type, and access-denied are all
// expected failure modes on older / locked-down SKUs and must never panic.
func readConsentValue(subKeyPath string) (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, subKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()

	value, _, err := key.GetStringValue(consentValueName)
	if err != nil {
		return "", false
	}

	switch value {
	case "Allow":
		return "granted", true
	case "Deny":
		return "denied", true
	default:
		// Unknown value - treat as inconclusive so the caller falls back to
		// the parent key. If both keys yield unknowns, MicStatus returns
		// "not_determined" which prompts the UI to surface the Settings link.
		return "", false
	}
}

// RequestMic is a no-op on Windows. Unlike macOS AVFoundation, Windows has no
// programmatic prompt for desktop / Win32 apps to request mic access — the
// user must toggle it manually in Settings → Privacy → Microphone. The Wails
// frontend opens that page via the ms-settings:privacy-microphone deep link
// (see TASK-032). Keeping this function present mirrors the darwin signature
// so callers can stay platform-agnostic.
func RequestMic() {
	// Intentionally empty.
}
