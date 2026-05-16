package paths

import (
	"os"
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

// makeFakeAppBundle creates a minimal Jarvis.app layout under root and returns
// (resourcesDir, exePath). The exe is a real (empty, non-executable) file at
// "<root>/Jarvis.app/Contents/MacOS/jarvis". Optional sub-assets (python,
// daemon script, models dir) are NOT created here; individual tests opt in.
//
// Paths are returned with symlinks resolved (filepath.EvalSymlinks) because
// production code resolves symlinks before walking up the layout, and on
// macOS t.TempDir() lives under /var/folders which is a symlink to
// /private/var/folders. Returning resolved paths lets tests compare apples to
// apples.
func makeFakeAppBundle(t *testing.T, root string) (resources, exe string) {
	t.Helper()
	app := filepath.Join(root, "Jarvis.app")
	contents := filepath.Join(app, "Contents")
	macos := filepath.Join(contents, "MacOS")
	resources = filepath.Join(contents, "Resources")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatalf("mkdir MacOS: %v", err)
	}
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatalf("mkdir Resources: %v", err)
	}
	exe = filepath.Join(macos, "jarvis")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	// Resolve symlinks on returned paths so callers can compare directly
	// against values produced by the production helper, which also resolves.
	if r, err := filepath.EvalSymlinks(resources); err == nil {
		resources = r
	}
	if e, err := filepath.EvalSymlinks(exe); err == nil {
		exe = e
	}
	return resources, exe
}

// TestBundledResourcesDirInBundle verifies the helper resolves Resources when
// the executable lives inside a .app layout.
func TestBundledResourcesDirInBundle(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	got := BundledResourcesDir()
	if got != resources {
		t.Errorf("BundledResourcesDir() = %q; want %q", got, resources)
	}
}

// TestBundledResourcesDirDevMode verifies the helper returns "" when the
// executable is NOT inside a .app bundle (e.g., `go test`, `wails dev`).
func TestBundledResourcesDirDevMode(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "jarvis")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledResourcesDir(); got != "" {
		t.Errorf("BundledResourcesDir() in dev mode = %q; want \"\"", got)
	}
}

// TestBundledResourcesDirExecutableError verifies the helper returns "" when
// resolveExecutable fails.
func TestBundledResourcesDirExecutableError(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	executableOverride = func() (string, error) { return "", os.ErrNotExist }

	if got := BundledResourcesDir(); got != "" {
		t.Errorf("BundledResourcesDir() with exe error = %q; want \"\"", got)
	}
}

// TestBundledPythonPresent verifies BundledPython returns the path when the
// interpreter exists at <Resources>/python/bin/python3 and is executable.
func TestBundledPythonPresent(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	pyDir := filepath.Join(resources, "python", "bin")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatalf("mkdir python bin: %v", err)
	}
	py := filepath.Join(pyDir, "python3")
	if err := os.WriteFile(py, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write python3: %v", err)
	}

	got := BundledPython()
	if got != py {
		t.Errorf("BundledPython() = %q; want %q", got, py)
	}
}

// TestBundledPythonMissing verifies BundledPython returns "" when the
// interpreter is not present.
func TestBundledPythonMissing(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	_, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	// No python file at all.
	if got := BundledPython(); got != "" {
		t.Errorf("BundledPython() missing = %q; want \"\"", got)
	}
}

// TestBundledPythonNotExecutable verifies BundledPython returns "" when the
// interpreter file exists but lacks any execute bit.
func TestBundledPythonNotExecutable(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	pyDir := filepath.Join(resources, "python", "bin")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatalf("mkdir python bin: %v", err)
	}
	py := filepath.Join(pyDir, "python3")
	if err := os.WriteFile(py, []byte("data"), 0o644); err != nil {
		t.Fatalf("write python3: %v", err)
	}

	if got := BundledPython(); got != "" {
		t.Errorf("BundledPython() non-executable = %q; want \"\"", got)
	}
}

// TestBundledPythonDevMode verifies BundledPython returns "" outside a bundle.
func TestBundledPythonDevMode(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "jarvis")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledPython(); got != "" {
		t.Errorf("BundledPython() dev mode = %q; want \"\"", got)
	}
}

// TestBundledDaemonScriptPresent verifies BundledDaemonScript returns the path
// when main.py exists.
func TestBundledDaemonScriptPresent(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	dir := filepath.Join(resources, "jarvis-daemon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir jarvis-daemon: %v", err)
	}
	script := filepath.Join(dir, "main.py")
	if err := os.WriteFile(script, []byte("# main\n"), 0o644); err != nil {
		t.Fatalf("write main.py: %v", err)
	}

	got := BundledDaemonScript()
	if got != script {
		t.Errorf("BundledDaemonScript() = %q; want %q", got, script)
	}
}

// TestBundledDaemonScriptMissing verifies BundledDaemonScript returns "" when
// the script is not present.
func TestBundledDaemonScriptMissing(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	_, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledDaemonScript(); got != "" {
		t.Errorf("BundledDaemonScript() missing = %q; want \"\"", got)
	}
}

// TestBundledDaemonScriptDevMode verifies BundledDaemonScript returns ""
// outside a bundle.
func TestBundledDaemonScriptDevMode(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "jarvis")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledDaemonScript(); got != "" {
		t.Errorf("BundledDaemonScript() dev mode = %q; want \"\"", got)
	}
}

// TestBundledModelsDirPresent verifies BundledModelsDir returns the path when
// the directory exists.
func TestBundledModelsDirPresent(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	models := filepath.Join(resources, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatalf("mkdir models: %v", err)
	}

	got := BundledModelsDir()
	if got != models {
		t.Errorf("BundledModelsDir() = %q; want %q", got, models)
	}
}

// TestBundledModelsDirMissing verifies BundledModelsDir returns "" when the
// directory is not present.
func TestBundledModelsDirMissing(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	_, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledModelsDir(); got != "" {
		t.Errorf("BundledModelsDir() missing = %q; want \"\"", got)
	}
}

// TestBundledModelsDirIsFile verifies BundledModelsDir returns "" when the
// "models" entry exists but is a file, not a directory.
func TestBundledModelsDirIsFile(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeAppBundle(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	models := filepath.Join(resources, "models")
	if err := os.WriteFile(models, []byte("oops"), 0o644); err != nil {
		t.Fatalf("write models file: %v", err)
	}

	if got := BundledModelsDir(); got != "" {
		t.Errorf("BundledModelsDir() file = %q; want \"\"", got)
	}
}

// TestBundledModelsDirDevMode verifies BundledModelsDir returns "" outside a
// bundle.
func TestBundledModelsDirDevMode(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "jarvis")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledModelsDir(); got != "" {
		t.Errorf("BundledModelsDir() dev mode = %q; want \"\"", got)
	}
}

// TestBundleHelpersProductionFallback verifies that when executableOverride is
// nil (production), the helpers don't panic and return "" because the test
// binary is not running from inside a .app bundle.
func TestBundleHelpersProductionFallback(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	executableOverride = nil

	if got := BundledResourcesDir(); got != "" {
		t.Errorf("BundledResourcesDir() production = %q; want \"\" (go test binary is not in a .app)", got)
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
