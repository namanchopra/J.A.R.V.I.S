//go:build !darwin && !windows

package hotkey

import libhotkey "golang.design/x/hotkey"

// modifierAliases (linux/other) — maps the cross-platform spec vocabulary
// onto X11's Mod1..Mod5 modifier groups. golang.design/x/hotkey only exports
// ModCtrl, ModShift, and Mod1..Mod5 on Linux (X11 modifier indices); ModAlt
// and ModCmd do not exist.
//
// Mapping rationale (X11 conventions on most desktops):
//   - alt / option / opt           → Mod1 (typically Alt_L / Alt_R)
//   - cmd / command / meta / super → Mod4 (typically Super_L, the "Windows" key)
//   - ctrl / control               → ModCtrl
//   - shift                        → ModShift
//
// Jarvis does not officially target Linux for v0.4.0; this file exists so the
// hotkey package compiles cleanly on Linux runners (CI lint passes) and the
// rest of the codebase keeps its `go vet ./...` invariant.
func init() {
	modifierAliases = map[string]libhotkey.Modifier{
		"cmd":     libhotkey.Mod4,
		"command": libhotkey.Mod4,
		"meta":    libhotkey.Mod4,
		"super":   libhotkey.Mod4,
		"win":     libhotkey.Mod4,
		"windows": libhotkey.Mod4,

		"ctrl":    libhotkey.ModCtrl,
		"control": libhotkey.ModCtrl,

		"alt":    libhotkey.Mod1,
		"option": libhotkey.Mod1,
		"opt":    libhotkey.Mod1,

		"shift": libhotkey.ModShift,
	}
}
