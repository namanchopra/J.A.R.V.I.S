package hotkey

import (
	"fmt"
	"strings"

	libhotkey "golang.design/x/hotkey"
)

// modifierAliases maps every accepted spelling of a modifier (lowercased) to
// the underlying library Modifier constant.
var modifierAliases = map[string]libhotkey.Modifier{
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

// namedKeys covers non-letter, non-digit keys. Letters and digits are handled
// programmatically below.
var namedKeys = map[string]libhotkey.Key{
	"space":  libhotkey.KeySpace,
	"return": libhotkey.KeyReturn,
	"enter":  libhotkey.KeyReturn,
	"tab":    libhotkey.KeyTab,
	"escape": libhotkey.KeyEscape,
	"esc":    libhotkey.KeyEscape,
	"left":   libhotkey.KeyLeft,
	"right":  libhotkey.KeyRight,
	"up":     libhotkey.KeyUp,
	"down":   libhotkey.KeyDown,

	"f1":  libhotkey.KeyF1,
	"f2":  libhotkey.KeyF2,
	"f3":  libhotkey.KeyF3,
	"f4":  libhotkey.KeyF4,
	"f5":  libhotkey.KeyF5,
	"f6":  libhotkey.KeyF6,
	"f7":  libhotkey.KeyF7,
	"f8":  libhotkey.KeyF8,
	"f9":  libhotkey.KeyF9,
	"f10": libhotkey.KeyF10,
	"f11": libhotkey.KeyF11,
	"f12": libhotkey.KeyF12,
}

// letterKeys maps "a".."z" to the corresponding KeyA..KeyZ constants.
var letterKeys = map[string]libhotkey.Key{
	"a": libhotkey.KeyA, "b": libhotkey.KeyB, "c": libhotkey.KeyC, "d": libhotkey.KeyD,
	"e": libhotkey.KeyE, "f": libhotkey.KeyF, "g": libhotkey.KeyG, "h": libhotkey.KeyH,
	"i": libhotkey.KeyI, "j": libhotkey.KeyJ, "k": libhotkey.KeyK, "l": libhotkey.KeyL,
	"m": libhotkey.KeyM, "n": libhotkey.KeyN, "o": libhotkey.KeyO, "p": libhotkey.KeyP,
	"q": libhotkey.KeyQ, "r": libhotkey.KeyR, "s": libhotkey.KeyS, "t": libhotkey.KeyT,
	"u": libhotkey.KeyU, "v": libhotkey.KeyV, "w": libhotkey.KeyW, "x": libhotkey.KeyX,
	"y": libhotkey.KeyY, "z": libhotkey.KeyZ,
}

// digitKeys maps "0".."9" to Key0..Key9.
var digitKeys = map[string]libhotkey.Key{
	"0": libhotkey.Key0, "1": libhotkey.Key1, "2": libhotkey.Key2, "3": libhotkey.Key3,
	"4": libhotkey.Key4, "5": libhotkey.Key5, "6": libhotkey.Key6, "7": libhotkey.Key7,
	"8": libhotkey.Key8, "9": libhotkey.Key9,
}

// Parse converts a hotkey spec like "alt+space" or "cmd+shift+j" into the
// library's Modifier slice and Key.
//
// Rules:
//   - Tokens are separated by '+', case-insensitive, whitespace trimmed.
//   - Zero or more modifier tokens, followed by exactly one key token at the end.
//   - Duplicate modifiers are rejected.
//   - Unknown tokens produce an error mentioning the unknown token verbatim.
//   - Empty spec produces an error mentioning "empty hotkey spec".
func Parse(spec string) ([]libhotkey.Modifier, libhotkey.Key, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return nil, 0, fmt.Errorf("hotkey: empty hotkey spec")
	}

	rawTokens := strings.Split(trimmed, "+")
	tokens := make([]string, 0, len(rawTokens))
	for _, t := range rawTokens {
		tok := strings.ToLower(strings.TrimSpace(t))
		if tok == "" {
			return nil, 0, fmt.Errorf("hotkey: empty token in spec %q", spec)
		}
		tokens = append(tokens, tok)
	}

	// Last token must be the key; everything before it is a modifier.
	keyTok := tokens[len(tokens)-1]
	modToks := tokens[:len(tokens)-1]

	// Resolve modifiers, rejecting duplicates.
	mods := make([]libhotkey.Modifier, 0, len(modToks))
	seen := make(map[libhotkey.Modifier]struct{}, len(modToks))
	for _, mt := range modToks {
		mod, ok := modifierAliases[mt]
		if !ok {
			// If it appears in any key map, the user put the key before a modifier.
			if _, isKey := lookupKey(mt); isKey {
				return nil, 0, fmt.Errorf("hotkey: %q is a key, expected modifier in spec %q", mt, spec)
			}
			return nil, 0, fmt.Errorf("hotkey: unknown token %q in spec %q", mt, spec)
		}
		if _, dup := seen[mod]; dup {
			return nil, 0, fmt.Errorf("hotkey: duplicate modifier %q in spec %q", mt, spec)
		}
		seen[mod] = struct{}{}
		mods = append(mods, mod)
	}

	// Resolve the key.
	key, ok := lookupKey(keyTok)
	if !ok {
		// If it's actually a modifier name, give a clearer message.
		if _, isMod := modifierAliases[keyTok]; isMod {
			return nil, 0, fmt.Errorf("hotkey: %q is a modifier, expected key in spec %q", keyTok, spec)
		}
		return nil, 0, fmt.Errorf("hotkey: unknown token %q in spec %q", keyTok, spec)
	}

	return mods, key, nil
}

// lookupKey resolves a lowercased token to a library Key.
func lookupKey(tok string) (libhotkey.Key, bool) {
	if k, ok := namedKeys[tok]; ok {
		return k, true
	}
	if k, ok := letterKeys[tok]; ok {
		return k, true
	}
	if k, ok := digitKeys[tok]; ok {
		return k, true
	}
	return 0, false
}
