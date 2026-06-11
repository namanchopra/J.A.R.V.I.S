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

// TestSetupV012PathsRespectHomeOverride verifies that the five v0.2.0 setup
// path helpers all anchor under JarvisHome() and pick up homeOverride. The
// helpers are pure string concatenation — none of them touch disk — so we can
// point homeOverride at a path whose parent doesn't exist and still expect
// the right string back with no panic and no error.
func TestSetupV012PathsRespectHomeOverride(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	// Deliberately use a path that does NOT exist on disk. The whole point
	// is to prove these helpers never stat() anything.
	homeOverride = func() string { return "/nonexistent/test-home" }

	jarvisHome := filepath.Join("/nonexistent/test-home", ".jarvis")

	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "SetupSentinelPath",
			got:  SetupSentinelPath("0.2.0"),
			want: filepath.Join(jarvisHome, ".setup-version-0.2.0"),
		},
		{
			name: "PythonInstallDir",
			got:  PythonInstallDir(),
			want: filepath.Join(jarvisHome, "python"),
		},
		{
			name: "DaemonVenvDir",
			got:  DaemonVenvDir(),
			want: filepath.Join(jarvisHome, "jarvis-daemon-env"),
		},
		{
			name: "DaemonSourceDir",
			got:  DaemonSourceDir(),
			want: filepath.Join(jarvisHome, "jarvis-daemon"),
		},
		{
			name: "SetupLogPath",
			got:  SetupLogPath(),
			want: filepath.Join(jarvisHome, "logs", "setup.log"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %q; want %q", tc.name, tc.got, tc.want)
			}
			// Every helper must live under JarvisHome() (sanity).
			if !strings.HasPrefix(tc.got, jarvisHome) {
				t.Errorf("%s = %q; expected prefix %q", tc.name, tc.got, jarvisHome)
			}
		})
	}

	// Bonus: bumping the version string must change the sentinel path so the
	// argument is actually used (guards against a future "oops, I hardcoded
	// it" regression).
	if SetupSentinelPath("0.2.0") == SetupSentinelPath("0.3.0") {
		t.Error("SetupSentinelPath does not vary with its version argument")
	}
}

// TestBundleHelpersProductionFallback verifies that when executableOverride is
// nil (production), the helpers don't panic and return "" because the test
// binary is not running from inside a .app bundle (macOS) or a next-to-exe
// Resources install (Windows).
func TestBundleHelpersProductionFallback(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	executableOverride = nil

	if got := BundledResourcesDir(); got != "" {
		t.Errorf("BundledResourcesDir() production = %q; want \"\" (go test binary is not in a bundle)", got)
	}
	if got := BundledPython(); got != "" {
		t.Errorf("BundledPython() production = %q; want \"\"", got)
	}
	if got := BundledDaemonScript(); got != "" {
		t.Errorf("BundledDaemonScript() production = %q; want \"\"", got)
	}
	if got := BundledModelsDir(); got != "" {
		t.Errorf("BundledModelsDir() production = %q; want \"\"", got)
	}
}
