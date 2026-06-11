//go:build darwin

package paths

import (
	"os"
	"path/filepath"
	"testing"
)

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

// TestInstalledPythonDarwinShape verifies InstalledPython() returns the Unix
// "bin/python3" layout when the file exists under ~/.jarvis/python/.
// This is a darwin-specific test because the path layout differs from Windows
// (which uses "python.exe" directly under the python install dir).
func TestInstalledPythonDarwinShape(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	tmp := t.TempDir()
	homeOverride = func() string { return tmp }

	// Pre-flight: with no file present, returns "".
	if got := InstalledPython(); got != "" {
		t.Errorf("InstalledPython() with no file = %q; want \"\"", got)
	}

	// Materialise the expected file and re-check.
	dir := filepath.Join(tmp, ".jarvis", "python", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, "python3")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write python3: %v", err)
	}

	if got := InstalledPython(); got != p {
		t.Errorf("InstalledPython() = %q; want %q", got, p)
	}
}

// TestInstalledDaemonVenvPythonDarwinShape verifies the Unix "bin/python"
// shape on macOS. Mirrors TestInstalledDaemonVenvPythonWindowsShape in
// paths_windows_test.go.
func TestInstalledDaemonVenvPythonDarwinShape(t *testing.T) {
	t.Cleanup(func() { homeOverride = nil })
	tmp := t.TempDir()
	homeOverride = func() string { return tmp }

	if got := InstalledDaemonVenvPython(); got != "" {
		t.Errorf("InstalledDaemonVenvPython() with no file = %q; want \"\"", got)
	}

	dir := filepath.Join(tmp, ".jarvis", "jarvis-daemon-env", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, "python")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write python: %v", err)
	}

	if got := InstalledDaemonVenvPython(); got != p {
		t.Errorf("InstalledDaemonVenvPython() = %q; want %q", got, p)
	}
}
