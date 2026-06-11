//go:build windows

package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeFakeWindowsInstall creates a minimal "<install>\jarvis.exe" layout under
// root and returns (resourcesDir, exePath). The exe is a real (empty) file at
// "<root>\jarvis.exe". The "<root>\Resources" directory is created. Optional
// sub-assets (python, daemon script, models dir) are NOT created here;
// individual tests opt in.
//
// Paths are returned with symlinks/junctions resolved (filepath.EvalSymlinks)
// because production code resolves symlinks before walking up the layout.
// Returning resolved paths lets tests compare apples to apples.
func makeFakeWindowsInstall(t *testing.T, root string) (resources, exe string) {
	t.Helper()
	resources = filepath.Join(root, "Resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatalf("mkdir Resources: %v", err)
	}
	exe = filepath.Join(root, "jarvis.exe")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	if r, err := filepath.EvalSymlinks(resources); err == nil {
		resources = r
	}
	if e, err := filepath.EvalSymlinks(exe); err == nil {
		exe = e
	}
	return resources, exe
}

// TestInstalledDaemonVenvPythonWindowsShape verifies that the Windows venv
// interpreter lives under "Scripts\python.exe" (NOT "bin/python"), which is
// the layout produced by `uv venv` and `python -m venv` on Windows.
//
// This is the headline acceptance criterion from TASK-002:
//   "InstalledDaemonVenvPython returns <home>\jarvis-daemon-env\Scripts\python.exe"
func TestInstalledDaemonVenvPythonWindowsShape(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	tmp := t.TempDir()
	homeOverride = func() string { return tmp }

	// Pre-flight: with no file present, returns "".
	if got := InstalledDaemonVenvPython(); got != "" {
		t.Errorf("InstalledDaemonVenvPython() with no file = %q; want \"\"", got)
	}

	// Materialise the expected venv interpreter and re-check.
	dir := filepath.Join(tmp, ".jarvis", "jarvis-daemon-env", "Scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := filepath.Join(dir, "python.exe")
	if err := os.WriteFile(want, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("write python.exe: %v", err)
	}

	if got := InstalledDaemonVenvPython(); got != want {
		t.Errorf("InstalledDaemonVenvPython() = %q; want %q", got, want)
	}

	// Sanity: the path component "Scripts" must appear (not "bin").
	got := InstalledDaemonVenvPython()
	if !strings.Contains(got, string(filepath.Separator)+"Scripts"+string(filepath.Separator)) {
		t.Errorf("InstalledDaemonVenvPython() = %q; expected to contain '\\Scripts\\'", got)
	}
}

// TestInstalledPythonWindowsShape verifies that the base Python interpreter
// lives at "<install>\python.exe" (no bin/ subdirectory, in contrast to the
// Unix python-build-standalone layout which uses "bin/python3").
func TestInstalledPythonWindowsShape(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	tmp := t.TempDir()
	homeOverride = func() string { return tmp }

	if got := InstalledPython(); got != "" {
		t.Errorf("InstalledPython() with no file = %q; want \"\"", got)
	}

	dir := filepath.Join(tmp, ".jarvis", "python")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := filepath.Join(dir, "python.exe")
	if err := os.WriteFile(want, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("write python.exe: %v", err)
	}

	if got := InstalledPython(); got != want {
		t.Errorf("InstalledPython() = %q; want %q", got, want)
	}
}

// TestJarvisHomeWindowsShape verifies that JarvisHome() on Windows resolves
// to "<userprofile>\.jarvis" when a home is available via homeOverride. This
// covers the happy-path acceptance criterion: JarvisHome() returns
// "C:\Users\<user>\.jarvis" on Windows.
func TestJarvisHomeWindowsShape(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	homeOverride = func() string { return `C:\Users\test` }

	got := JarvisHome()
	want := filepath.Join(`C:\Users\test`, ".jarvis")
	if got != want {
		t.Errorf("JarvisHome() = %q; want %q", got, want)
	}
}

// TestJarvisHomeWindowsFallback verifies the failure-case acceptance criterion
// from TASK-002:
//   "Non-existent %USERPROFILE% (failure case): returns the fallback
//    C:\jarvis-home and logs a warning"
//
// We trigger the fallback by clearing every home-locator env var that
// os.UserHomeDir consults on Windows (USERPROFILE, plus HOMEDRIVE+HOMEPATH).
// homeOverride is left nil so the real fallback code path executes.
func TestJarvisHomeWindowsFallback(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	homeOverride = nil

	// Clear every var that os.UserHomeDir looks at on Windows. We restore them
	// via t.Setenv (which cleans up automatically after the test).
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	got := JarvisHome()
	want := `C:\jarvis-home`
	if got != want {
		t.Errorf("JarvisHome() fallback = %q; want %q", got, want)
	}
}

// TestBundledResourcesDirWindowsInBundle verifies that the Windows bundle
// resolver locates the "Resources" sibling directory next to jarvis.exe.
func TestBundledResourcesDirWindowsInBundle(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeWindowsInstall(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	got := BundledResourcesDir()
	if got != resources {
		t.Errorf("BundledResourcesDir() = %q; want %q", got, resources)
	}
}

// TestBundledResourcesDirWindowsDevMode verifies the helper returns "" when no
// Resources directory sits next to the executable (i.e., `go run` / `wails
// dev`).
func TestBundledResourcesDirWindowsDevMode(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "jarvis.exe")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledResourcesDir(); got != "" {
		t.Errorf("BundledResourcesDir() dev mode = %q; want \"\"", got)
	}
}

// TestBundledResourcesDirWindowsExecutableError verifies the helper returns ""
// when resolveExecutable fails.
func TestBundledResourcesDirWindowsExecutableError(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	executableOverride = func() (string, error) { return "", os.ErrNotExist }

	if got := BundledResourcesDir(); got != "" {
		t.Errorf("BundledResourcesDir() with exe error = %q; want \"\"", got)
	}
}

// TestBundledPythonWindowsPresent verifies BundledPython returns the path when
// <Resources>\python\python.exe exists.
func TestBundledPythonWindowsPresent(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeWindowsInstall(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	pyDir := filepath.Join(resources, "python")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatalf("mkdir python: %v", err)
	}
	py := filepath.Join(pyDir, "python.exe")
	if err := os.WriteFile(py, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("write python.exe: %v", err)
	}

	got := BundledPython()
	if got != py {
		t.Errorf("BundledPython() = %q; want %q", got, py)
	}
}

// TestBundledPythonWindowsMissing verifies BundledPython returns "" when the
// interpreter is not present.
func TestBundledPythonWindowsMissing(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	_, exe := makeFakeWindowsInstall(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledPython(); got != "" {
		t.Errorf("BundledPython() missing = %q; want \"\"", got)
	}
}

// TestBundledDaemonScriptWindowsPresent verifies BundledDaemonScript returns
// the path when <Resources>\jarvis-daemon\main.py exists.
func TestBundledDaemonScriptWindowsPresent(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeWindowsInstall(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	dir := filepath.Join(resources, "jarvis-daemon")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir jarvis-daemon: %v", err)
	}
	script := filepath.Join(dir, "main.py")
	if err := os.WriteFile(script, []byte("# main\r\n"), 0o644); err != nil {
		t.Fatalf("write main.py: %v", err)
	}

	got := BundledDaemonScript()
	if got != script {
		t.Errorf("BundledDaemonScript() = %q; want %q", got, script)
	}
}

// TestBundledDaemonScriptWindowsMissing verifies BundledDaemonScript returns
// "" when the script is not present.
func TestBundledDaemonScriptWindowsMissing(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	_, exe := makeFakeWindowsInstall(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	if got := BundledDaemonScript(); got != "" {
		t.Errorf("BundledDaemonScript() missing = %q; want \"\"", got)
	}
}

// TestBundledModelsDirWindowsPresent verifies BundledModelsDir returns the
// path when the directory exists next to the exe.
func TestBundledModelsDirWindowsPresent(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeWindowsInstall(t, tmp)
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

// TestBundledModelsDirWindowsIsFile verifies BundledModelsDir returns "" when
// the "models" entry exists but is a file, not a directory.
func TestBundledModelsDirWindowsIsFile(t *testing.T) {
	t.Cleanup(func() { executableOverride = nil })
	tmp := t.TempDir()
	resources, exe := makeFakeWindowsInstall(t, tmp)
	executableOverride = func() (string, error) { return exe, nil }

	models := filepath.Join(resources, "models")
	if err := os.WriteFile(models, []byte("oops"), 0o644); err != nil {
		t.Fatalf("write models file: %v", err)
	}

	if got := BundledModelsDir(); got != "" {
		t.Errorf("BundledModelsDir() file = %q; want \"\"", got)
	}
}

// TestFallbackJarvisHomeIsAbsolute verifies the Windows fallback is an
// absolute path that the app can write to without depending on $CWD.
func TestFallbackJarvisHomeIsAbsolute(t *testing.T) {
	got := fallbackJarvisHome()
	if !filepath.IsAbs(got) {
		t.Errorf("fallbackJarvisHome() = %q; want an absolute path", got)
	}
	if got != `C:\jarvis-home` {
		t.Errorf("fallbackJarvisHome() = %q; want %q", got, `C:\jarvis-home`)
	}
}
