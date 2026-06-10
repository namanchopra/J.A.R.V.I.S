//go:build windows

package paths

import (
	"os"
	"path/filepath"
)

// fallbackJarvisHome returns the directory used when os.UserHomeDir() fails on
// Windows (e.g. %USERPROFILE% is unset or unreadable). We use C:\jarvis-home
// rather than a relative path so the app has a stable, writable location even
// when launched from a path the user can't write back to (e.g. Program Files).
// Tests that need a deterministic path use homeOverride instead of triggering
// this fallback.
func fallbackJarvisHome() string {
	return `C:\jarvis-home`
}

// InstalledPython returns the user-installed portable CPython interpreter
// at ~/.jarvis/python/python.exe if it exists and is a regular file, else "".
// install-daemon.ps1 phase 1 (TASK-004) extracts python-build-standalone's
// Windows tarball under PythonInstallDir(); the layout places python.exe at
// the install root (no bin/ subdirectory, in contrast to Unix builds).
//
// IMPORTANT: this is the BASE interpreter only -- it has no site-packages.
// To launch the daemon you almost always want InstalledDaemonVenvPython()
// instead, which resolves to the uv-managed venv that has pipecat + every
// other runtime dep on its sys.path. See app_jarvis.go's launch resolver
// for the correct preference order.
//
// We do NOT verify the execute bit here — on Windows the FS doesn't carry the
// same execute-permission semantics as Unix, and the installer has already
// shelled out to the interpreter during phase 2 to materialise the venv. If
// the file is somehow non-executable, StartJarvis will surface the real exec
// error via ErrDaemonLaunchFailed, which is exactly the diagnostic we want.
func InstalledPython() string {
	p := filepath.Join(PythonInstallDir(), "python.exe")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}

// InstalledDaemonVenvPython returns the user-installed daemon-venv interpreter
// at ~/.jarvis/jarvis-daemon-env/Scripts/python.exe if it exists and is a
// regular file, else "". install-daemon.ps1 phase 2 (TASK-004) creates this
// venv via `uv venv` against the InstalledPython interpreter and runs
// `uv pip install -r requirements.txt` into it -- so this binary is the ONLY
// Python that has pipecat, faster-whisper, vibevoice, etc. on its sys.path.
// StartJarvis MUST prefer this over InstalledPython for daemon launches;
// using the base interpreter triggers a `No module named 'pipecat'` crash
// loop the moment the daemon starts importing.
//
// Note the `Scripts` subdirectory (instead of Unix's `bin`): that is the
// venv layout produced by both `python -m venv` and `uv venv` on Windows.
func InstalledDaemonVenvPython() string {
	p := filepath.Join(DaemonVenvDir(), "Scripts", "python.exe")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}

// resolveBundledResourcesDir walks up from the current executable to detect
// the Windows next-to-exe Resources layout produced by build/scripts/post-
// build.ps1 (TASK-055). Inside a Windows install the binary lives at
// "<install>\jarvis.exe" and Resources sits sibling: "<install>\Resources\".
// This helper returns the Resources directory in that case, and "" otherwise
// (e.g. when running via `wails dev` or `go run` where no Resources sibling
// exists).
//
// All four exported bundle helpers (BundledResourcesDir, BundledPython,
// BundledDaemonScript, BundledModelsDir) funnel through this routine so the
// detection logic lives in exactly one place.
func resolveBundledResourcesDir() string {
	exe, err := resolveExecutable()
	if err != nil || exe == "" {
		return ""
	}
	// Resolve symlinks/junctions so we look at the *real* path. A launcher
	// outside the install dir would otherwise confuse the layout check.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe) // <install>
	resources := filepath.Join(dir, "Resources")
	info, err := os.Stat(resources)
	if err != nil || !info.IsDir() {
		return ""
	}
	return resources
}

// BundledResourcesDir resolves "<install>\Resources" by walking up from
// os.Executable(). Returns "" if no Resources directory sits next to the
// executable (i.e. dev mode via `wails dev` or `go run`).
//
// Callers should treat "" as "no bundled assets available" and fall back to
// their dev-mode defaults (e.g., the project's venv, ~/.jarvis/models/).
func BundledResourcesDir() string {
	return resolveBundledResourcesDir()
}

// BundledPython returns the path to the bundled CPython interpreter shipped
// in the Windows install, i.e. "<Resources>\python\python.exe". Returns ""
// when the file does not exist or the process is not running from an install
// that has a Resources sibling.
//
// TASK-011 (daemon spawn) prefers this over the dev venv when present.
//
// We do not verify execute permissions on Windows — the NTFS file system does
// not carry the same execute-bit semantics as Unix, and the installer has
// already shelled out to this interpreter to materialise the venv.
func BundledPython() string {
	root := resolveBundledResourcesDir()
	if root == "" {
		return ""
	}
	p := filepath.Join(root, "python", "python.exe")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}

// BundledDaemonScript returns the path to the bundled jarvis-daemon entry
// point shipped in the Windows install, i.e. "<Resources>\jarvis-daemon\main.py".
// Returns "" when the file does not exist or the process is not running from
// an install that has a Resources sibling.
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
// in the Windows install, i.e. "<Resources>\models". Returns "" when the
// directory does not exist or the process is not running from an install that
// has a Resources sibling.
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
