//go:build windows

package hotkey

import libhotkey "golang.design/x/hotkey"

// modifierAliases (windows) — maps the cross-platform spec vocabulary onto
// the Windows-only Modifier constants from golang.design/x/hotkey
// (ModAlt / ModCtrl / ModShift / ModWin). The library does not expose
// ModCmd or ModOption on Windows.
//
// Mapping rationale:
//   - alt / option / opt  → ModAlt
//     macOS uses "alt+space" for the overlay toggle; Windows binds the same
//     spec string to the Alt key, which is the visually equivalent key.
//   - ctrl / control      → ModCtrl
//   - shift               → ModShift
//   - cmd / command / meta / super → ModWin
//     macOS uses "cmd" as the primary modifier; the closest Windows
//     equivalent is the Windows key. This keeps cross-platform spec strings
//     portable (e.g. user-supplied "cmd+shift+j" registers as Win+Shift+J on
//     Windows). Note: combos containing ModWin frequently collide with
//     reserved OS shortcuts on Windows and will surface a clean Register
//     error — see the TASK-031 verification tests for the documented
//     failure-case path.
func init() {
	modifierAliases = map[string]libhotkey.Modifier{
		"cmd":     libhotkey.ModWin,
		"command": libhotkey.ModWin,
		"meta":    libhotkey.ModWin,
		"super":   libhotkey.ModWin,
		"win":     libhotkey.ModWin,
		"windows": libhotkey.ModWin,

		"ctrl":    libhotkey.ModCtrl,
		"control": libhotkey.ModCtrl,

		"alt":    libhotkey.ModAlt,
		"option": libhotkey.ModAlt,
		"opt":    libhotkey.ModAlt,

		"shift": libhotkey.ModShift,
	}
}
