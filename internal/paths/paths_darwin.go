//go:build darwin

package paths

import (
	"os"
	"path/filepath"
)

// fallbackJarvisHome returns the directory used when os.UserHomeDir() fails on
// macOS. Historically this was "./.jarvis" (current working directory) — the
// app keeps running rather than panicking. Tests that need a deterministic
// path use homeOverride instead of triggering this fallback.
func fallbackJarvisHome() string {
	return filepath.Join(".", ".jarvis")
}

// InstalledPython returns ~/.jarvis/python/bin/python3 if it exists and is a
// regular file (not a directory), else "". The v0.2.0 setup script
// (install-daemon.sh, TASK-004) extracts a portable CPython tree under
// PythonInstallDir(); this helper is the read-side check used by StartJarvis
// (TASK-009) to prefer the user-installed interpreter over the legacy bundled
// one.
//
// IMPORTANT: this is the BASE interpreter only -- it has no site-packages.
// To launch the daemon you almost always want InstalledDaemonVenvPython()
// instead, which resolves to the uv-managed venv that has pipecat + every
// other runtime dep on its sys.path. See app_jarvis.go's launch resolver
// for the correct preference order.
//
// We do NOT verify the execute bit here — install-daemon.sh writes the
// interpreter with 0o755 and shells out to it during phase 2 to materialise
// the venv, so by the time StartJarvis consults this helper, exec-ability has
// already been smoke-tested by the installer itself. Returning the path on a
// non-executable file would let StartJarvis surface the real exec error via
// ErrDaemonLaunchFailed, which is exactly the diagnostic we want.
func InstalledPython() string {
	p := filepath.Join(PythonInstallDir(), "bin", "python3")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}

// InstalledDaemonVenvPython returns ~/.jarvis/jarvis-daemon-env/bin/python
// if it exists and is a regular file, else "". install-daemon.sh phase 2
// (TASK-004) creates this venv via `uv venv` against the InstalledPython
// interpreter and runs `uv pip install -r requirements.txt` into it -- so
// this binary is the ONLY Python that has pipecat, mlx-whisper, vibevoice,
// etc. on its sys.path. StartJarvis MUST prefer this over InstalledPython
// for daemon launches; using the base interpreter triggers a `No module
// named 'pipecat'` crash loop the moment the daemon starts importing.
func InstalledDaemonVenvPython() string {
	p := filepath.Join(DaemonVenvDir(), "bin", "python")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}

// resolveBundledResourcesDir walks up from the current executable to detect a
// macOS .app bundle layout. Inside a bundle, the binary lives at
// "<X>/Jarvis.app/Contents/MacOS/<binary>"; this helper returns
// "<X>/Jarvis.app/Contents/Resources" in that case, and "" otherwise (for
// example when running via `wails dev` or `go run`).
//
// All four exported bundle helpers (BundledResourcesDir, BundledPython,
// BundledDaemonScript, BundledModelsDir) funnel through this routine so the
// detection logic lives in exactly one place.
func resolveBundledResourcesDir() string {
	exe, err := resolveExecutable()
	if err != nil || exe == "" {
		return ""
	}
	// Resolve symlinks so we look at the *real* path. A symlink launcher
	// outside the bundle would otherwise confuse the .app-layout check.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)    // .../Contents/MacOS
	parent := filepath.Dir(dir) // .../Contents
	if filepath.Base(dir) != "MacOS" || filepath.Base(parent) != "Contents" {
		return ""
	}
	return filepath.Join(parent, "Resources")
}

// BundledResourcesDir resolves "<.app>/Contents/Resources" by walking up from
// os.Executable(). Returns "" if the current process is not running from
// inside a macOS .app bundle (i.e. dev mode via `wails dev` or `go run`).
//
// Callers should treat "" as "no bundled assets available" and fall back to
// their dev-mode defaults (e.g., the project's venv, ~/.jarvis/models/).
func BundledResourcesDir() string {
	return resolveBundledResourcesDir()
}

// BundledPython returns the path to the bundled CPython interpreter shipped
// inside the .app, i.e. "<Resources>/python/bin/python3". Returns "" when the
// file does not exist or the process is not running from a .app bundle.
//
// TASK-011 (daemon spawn) prefers this over the dev venv when present.
func BundledPython() string {
	root := resolveBundledResourcesDir()
	if root == "" {
		return ""
	}
	p := filepath.Join(root, "python", "bin", "python3")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	// Mode bits should grant at least one execute bit (owner/group/other).
	// We don't require user-execute specifically because the .app is shared
	// across users on disk in some installations.
	if info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return p
}

// BundledDaemonScript returns the path to the bundled jarvis-daemon entry
// point shipped inside the .app, i.e. "<Resources>/jarvis-daemon/main.py".
// Returns "" when the file does not exist or the process is not running from a
// .app bundle.
//
// TASK-011 (daemon spawn) prefers this over the source-tree path when present.
func BundledDaemonScript() string {
	root := resolveBundledResourcesDir()
	if root == "" {
		return ""
	}
	p := filepath.Join(root, "jarvis-daemon", "main.py")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}

// BundledModelsDir returns the path to the bundled models directory shipped
// inside the .app, i.e. "<Resources>/models". Returns "" when the directory
// does not exist or the process is not running from a .app bundle.
//
// TASK-015 (model paths) prefers this over ~/.jarvis/models/ when present.
func BundledModelsDir() string {
	root := resolveBundledResourcesDir()
	if root == "" {
		return ""
	}
	p := filepath.Join(root, "models")
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return ""
	}
	return p
}
