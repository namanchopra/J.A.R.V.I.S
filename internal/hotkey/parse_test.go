package hotkey

import (
	"sort"
	"strings"
	"testing"

	libhotkey "golang.design/x/hotkey"
)

// sortedMods returns a sorted copy of mods so set-equality tests are stable.
func sortedMods(mods []libhotkey.Modifier) []libhotkey.Modifier {
	out := make([]libhotkey.Modifier, len(mods))
	copy(out, mods)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func modsEqual(a, b []libhotkey.Modifier) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := sortedMods(a), sortedMods(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func TestParse_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		spec     string
		wantMods []libhotkey.Modifier
		wantKey  libhotkey.Key
	}{
		{
			name:     "alt+space",
			spec:     "alt+space",
			wantMods: []libhotkey.Modifier{libhotkey.ModOption},
			wantKey:  libhotkey.KeySpace,
		},
		{
			name:     "cmd+shift+j",
			spec:     "cmd+shift+j",
			wantMods: []libhotkey.Modifier{libhotkey.ModCmd, libhotkey.ModShift},
			wantKey:  libhotkey.KeyJ,
		},
		{
			name:     "uppercase ALT+SPACE matches alt+space",
			spec:     "ALT+SPACE",
			wantMods: []libhotkey.Modifier{libhotkey.ModOption},
			wantKey:  libhotkey.KeySpace,
		},
		{
			name:     "bare space with no modifier",
			spec:     "space",
			wantMods: []libhotkey.Modifier{},
			wantKey:  libhotkey.KeySpace,
		},
		{
			name:     "whitespace around tokens",
			spec:     "  cmd  +   shift  +  j  ",
			wantMods: []libhotkey.Modifier{libhotkey.ModCmd, libhotkey.ModShift},
			wantKey:  libhotkey.KeyJ,
		},
		{
			name:     "option alias maps to ModOption",
			spec:     "option+a",
			wantMods: []libhotkey.Modifier{libhotkey.ModOption},
			wantKey:  libhotkey.KeyA,
		},
		{
			name:     "control alias maps to ModCtrl",
			spec:     "control+1",
			wantMods: []libhotkey.Modifier{libhotkey.ModCtrl},
			wantKey:  libhotkey.Key1,
		},
		{
			name:     "command alias maps to ModCmd",
			spec:     "command+return",
			wantMods: []libhotkey.Modifier{libhotkey.ModCmd},
			wantKey:  libhotkey.KeyReturn,
		},
		{
			name:     "enter alias maps to KeyReturn",
			spec:     "cmd+enter",
			wantMods: []libhotkey.Modifier{libhotkey.ModCmd},
			wantKey:  libhotkey.KeyReturn,
		},
		{
			name:     "esc alias maps to KeyEscape",
			spec:     "esc",
			wantMods: []libhotkey.Modifier{},
			wantKey:  libhotkey.KeyEscape,
		},
		{
			name:     "f12 function key",
			spec:     "ctrl+f12",
			wantMods: []libhotkey.Modifier{libhotkey.ModCtrl},
			wantKey:  libhotkey.KeyF12,
		},
		{
			name:     "all four modifiers + letter",
			spec:     "cmd+ctrl+alt+shift+z",
			wantMods: []libhotkey.Modifier{libhotkey.ModCmd, libhotkey.ModCtrl, libhotkey.ModOption, libhotkey.ModShift},
			wantKey:  libhotkey.KeyZ,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotMods, gotKey, err := Parse(tc.spec)
			if err != nil {
				t.Fatalf("Parse(%q) returned unexpected error: %v", tc.spec, err)
			}
			if !modsEqual(gotMods, tc.wantMods) {
				t.Errorf("Parse(%q) mods = %v, want %v (order-insensitive)", tc.spec, gotMods, tc.wantMods)
			}
			if gotKey != tc.wantKey {
				t.Errorf("Parse(%q) key = %v, want %v", tc.spec, gotKey, tc.wantKey)
			}
		})
	}
}

func TestParse_Failure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		spec      string
		wantInErr string
	}{
		{
			name:      "empty spec",
			spec:      "",
			wantInErr: "empty",
		},
		{
			name:      "whitespace-only spec",
			spec:      "   ",
			wantInErr: "empty",
		},
		{
			name:      "garbage token",
			spec:      "garbage",
			wantInErr: "garbage",
		},
		{
			name:      "duplicate modifier",
			spec:      "cmd+cmd+j",
			wantInErr: "duplicate",
		},
		{
			name:      "duplicate modifier via alias",
			spec:      "cmd+command+j",
			wantInErr: "duplicate",
		},
		{
			name:      "modifier-only spec (no key)",
			spec:      "cmd+shift",
			wantInErr: "shift",
		},
		{
			name:      "empty token between plusses",
			spec:      "cmd++j",
			wantInErr: "empty token",
		},
		{
			name:      "unknown trailing token",
			spec:      "cmd+xyzkey",
			wantInErr: "xyzkey",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := Parse(tc.spec)
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", tc.spec)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("Parse(%q) error = %q, want it to contain %q", tc.spec, err.Error(), tc.wantInErr)
			}
		})
	}
}

// TestParse_GarbageMentionsTokenVerbatim is the explicit failure-case criterion
// from the task brief: Parse("garbage") must return a non-nil error mentioning
// "garbage" verbatim.
func TestParse_GarbageMentionsTokenVerbatim(t *testing.T) {
	t.Parallel()
	_, _, err := Parse("garbage")
	if err == nil {
		t.Fatal(`Parse("garbage") expected error, got nil`)
	}
	if !strings.Contains(err.Error(), "garbage") {
		t.Errorf(`Parse("garbage") error = %q, want it to mention "garbage"`, err.Error())
	}
}

// TestParse_AltSpaceShape verifies the brief's primary success-case criterion:
// exactly one Option modifier + KeySpace.
func TestParse_AltSpaceShape(t *testing.T) {
	t.Parallel()
	mods, key, err := Parse("alt+space")
	if err != nil {
		t.Fatalf(`Parse("alt+space") unexpected error: %v`, err)
	}
	if len(mods) != 1 || mods[0] != libhotkey.ModOption {
		t.Errorf(`Parse("alt+space") mods = %v, want [ModOption]`, mods)
	}
	if key != libhotkey.KeySpace {
		t.Errorf(`Parse("alt+space") key = %v, want KeySpace`, key)
	}
}
