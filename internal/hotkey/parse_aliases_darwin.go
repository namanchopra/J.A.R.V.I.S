//go:build darwin

package hotkey

import libhotkey "golang.design/x/hotkey"

// modifierAliases (darwin) — preserves the original macOS-specific spec
// vocabulary: cmd/command/meta/super → ModCmd, alt/option/opt → ModOption.
// These two darwin-only constants are why this map has to live in a
// platform-tagged file (see parse.go for the cross-platform rationale).
func init() {
	modifierAliases = map[string]libhotkey.Modifier{
		"cmd":     libhotkey.ModCmd,
		"command": libhotkey.ModCmd,
		"meta":    libhotkey.ModCmd,
		"super":   libhotkey.ModCmd,

		"ctrl":    libhotkey.ModCtrl,
		"control": libhotkey.ModCtrl,

		"alt":    libhotkey.ModOption,
		"option": libhotkey.ModOption,
		"opt":    libhotkey.ModOption,

		"shift": libhotkey.ModShift,
	}
}
