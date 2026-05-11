package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestJarvisHomeHappyPath verifies that JarvisHome() returns $HOME/.jarvis
// when os.UserHomeDir() succeeds.
func TestJarvisHomeHappyPath(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	homeOverride = func() string { return "/users/test" }

	got := JarvisHome()
	want := filepath.Join("/users/test", ".jarvis")
	if got != want {
		t.Errorf("JarvisHome() = %q; want %q", got, want)
	}
}

// TestJarvisHomeFallback verifies that JarvisHome() returns "./.jarvis" when
// os.UserHomeDir() fails (simulated by homeOverride returning ".").
func TestJarvisHomeFallback(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	homeOverride = func() string { return "." }

	got := JarvisHome()
	want := filepath.Join(".", ".jarvis")
	if got != want {
		t.Errorf("JarvisHome() fallback = %q; want %q", got, want)
	}
}

// TestLegacyHome verifies LegacyHome() returns $HOME/.awm.
func TestLegacyHome(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	homeOverride = func() string { return "/users/test" }

	got := LegacyHome()
	want := filepath.Join("/users/test", ".awm")
	if got != want {
		t.Errorf("LegacyHome() = %q; want %q", got, want)
	}
}

// TestConfigPath verifies ConfigPath() == JarvisHome()/config.json.
func TestConfigPath(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	homeOverride = func() string { return "/users/test" }

	got := ConfigPath()
	want := filepath.Join("/users/test", ".jarvis", "config.json")
	if got != want {
		t.Errorf("ConfigPath() = %q; want %q", got, want)
	}
}

// TestSubDirsLiveUnderJarvisHome verifies that LogsDir, WorkspacesDir,
// ModelsDir, RecordingsDir all live under JarvisHome().
func TestSubDirsLiveUnderJarvisHome(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	homeOverride = func() string { return "/users/test" }

	prefix := JarvisHome()
	cases := map[string]string{
		"LogsDir":       LogsDir(),
		"WorkspacesDir": WorkspacesDir(),
		"ModelsDir":     ModelsDir(),
		"RecordingsDir": RecordingsDir(),
	}
	for name, got := range cases {
		if !strings.HasPrefix(got, prefix+string(filepath.Separator)) && got != prefix {
			t.Errorf("%s() = %q; expected to start with %q", name, got, prefix)
		}
	}
}

// TestDataPathJoinsParts verifies that DataPath joins variadic parts onto
// JarvisHome() correctly.
func TestDataPathJoinsParts(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	homeOverride = func() string { return "/users/test" }

	got := DataPath("a", "b", "c.txt")
	want := filepath.Join("/users/test", ".jarvis", "a", "b", "c.txt")
	if got != want {
		t.Errorf("DataPath(...) = %q; want %q", got, want)
	}

	// Empty variadic - should return JarvisHome itself
	gotEmpty := DataPath()
	wantEmpty := JarvisHome()
	if gotEmpty != wantEmpty {
		t.Errorf("DataPath() = %q; want %q (= JarvisHome())", gotEmpty, wantEmpty)
	}
}

// TestRealHomeIsNotTouched verifies that running these tests does NOT touch
// the real $HOME directory. (Sanity check - tests don't use t.TempDir() for
// file ops because they're all pure path calculation, no I/O.)
func TestRealHomeIsNotTouched(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	// This test simply asserts that homeOverride is nil-safe (production state).
	homeOverride = nil

	got := JarvisHome()
	// Should NOT error. Should NOT panic. Should return SOMETHING reasonable.
	if got == "" {
		t.Error("JarvisHome() returned empty string with no override")
	}
	// Should end with .jarvis
	if !strings.HasSuffix(got, ".jarvis") {
		t.Errorf("JarvisHome() = %q; expected suffix '.jarvis'", got)
	}
}
