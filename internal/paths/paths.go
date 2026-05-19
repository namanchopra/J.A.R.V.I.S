// Package paths provides canonical filesystem path helpers for the Jarvis
// data directory (~/.jarvis) and its sub-directories.
//
// All Jarvis runtime data lives under JarvisHome() which defaults to
// "$HOME/.jarvis". Legacy installations stored data under "$HOME/.awm";
// see migrate.go (TASK-018) for the migration shim that one-shot copies
// legacy data into the new location and symlinks for backward compat.
//
// On rare environments where os.UserHomeDir() fails (e.g., no $HOME set),
// helpers fall back to "./.jarvis" (current directory) so the app can
// continue running rather than panicking.
package paths

import (
	"os"
	"path/filepath"
)

// homeOverride lets tests redirect JarvisHome/LegacyHome to a temporary
// directory without touching the real $HOME. Production callers leave it nil.
var homeOverride func() string

// executableOverride lets tests simulate a bundled .app layout without
// rebuilding the binary. When nil (production), os.Executable is used.
// Tests set this to a function that returns a fake exe path such as
// "/tmp/Jarvis.app/Contents/MacOS/jarvis" to exercise the bundle helpers.
var executableOverride func() (string, error)

func resolveHome() string {
	if homeOverride != nil {
		return homeOverride()
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return home
}

// resolveExecutable returns the current executable path, honoring
// executableOverride when set (used by tests). Production callers fall through
// to os.Executable.
func resolveExecutable() (string, error) {
	if executableOverride != nil {
		return executableOverride()
	}
	return os.Executable()
}

// JarvisHome returns the absolute path to the Jarvis data directory,
// typically "$HOME/.jarvis". Falls back to "./.jarvis" if $HOME is unavailable.
func JarvisHome() string {
	return filepath.Join(resolveHome(), ".jarvis")
}

// LegacyHome returns the legacy data directory ("$HOME/.awm") used by older
// installs. Only the migration shim should reference this.
func LegacyHome() string {
	return filepath.Join(resolveHome(), ".awm")
}

// ConfigPath returns the absolute path to the Jarvis config file.
func ConfigPath() string {
	return filepath.Join(JarvisHome(), "config.json")
}

// DataPath joins variadic path components onto JarvisHome().
//   DataPath("foo", "bar") => "$HOME/.jarvis/foo/bar"
func DataPath(parts ...string) string {
	return filepath.Join(append([]string{JarvisHome()}, parts...)...)
}

// LogsDir returns the directory for session output logs.
func LogsDir() string {
	return filepath.Join(JarvisHome(), "logs")
}

// WorkspacesDir returns the base directory for virtual monorepo workspaces.
func WorkspacesDir() string {
	return filepath.Join(JarvisHome(), "workspaces")
}

// ModelsDir returns the directory for downloaded AI model files (Whisper, etc.).
func ModelsDir() string {
	return filepath.Join(JarvisHome(), "models")
}

// RecordingsDir returns the directory for session recordings (.jsonl).
func RecordingsDir() string {
	return filepath.Join(JarvisHome(), "recordings")
}

// ---------------------------------------------------------------------------
// v0.2.0 setup-on-launch paths
// ---------------------------------------------------------------------------
//
// The helpers below point at the user-installed setup tree that
// install-daemon.sh (TASK-004) populates on first launch. They are pure
// string concatenation — none of them touch disk — so they are safe to call
// before the directories exist (which is the whole point: they tell the
// installer where to write).
//
// SetupSentinelPath takes the expected version as an argument rather than
// importing setup.SetupExpectedVersion, which would introduce an import
// cycle (setup may eventually want to use these helpers itself). Callers
// pass `setup.SetupExpectedVersion`.

// SetupSentinelPath returns the absolute path to the setup-version sentinel
// file at ~/.jarvis/.setup-version-<version>. The file's existence + valid
// contents are checked by setup.IsSetupComplete (TASK-008) to decide whether
// to mount SetupScreen.
//
// Callers should pass setup.SetupExpectedVersion as `version`; the parameter
// exists to avoid an import cycle between internal/paths and internal/setup.
func SetupSentinelPath(version string) string {
	return filepath.Join(JarvisHome(), ".setup-version-"+version)
}

// PythonInstallDir returns the absolute path to the user-installed portable
// CPython tree: ~/.jarvis/python/. install-daemon.sh writes here in phase 1.
func PythonInstallDir() string {
	return filepath.Join(JarvisHome(), "python")
}

// DaemonVenvDir returns the absolute path to the user-installed daemon
// virtualenv: ~/.jarvis/jarvis-daemon-env/. install-daemon.sh creates this
// with `uv venv` in phase 2.
func DaemonVenvDir() string {
	return filepath.Join(JarvisHome(), "jarvis-daemon-env")
}

// DaemonSourceDir returns the absolute path where install-daemon.sh copies the
// daemon's Python source from the .app bundle's Resources/jarvis-daemon/.
// Lives at ~/.jarvis/jarvis-daemon/.
func DaemonSourceDir() string {
	return filepath.Join(JarvisHome(), "jarvis-daemon")
}

// SetupLogPath returns the path to the setup orchestrator log:
// ~/.jarvis/logs/setup.log. install-daemon.sh tees its stderr here; the
// SetupScreen footer's "View setup log" link opens it via the OpenSetupLog
// Wails binding (TASK-016).
func SetupLogPath() string {
	return filepath.Join(JarvisHome(), "logs", "setup.log")
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

// InstalledDaemonScript returns ~/.jarvis/jarvis-daemon/main.py if it exists,
// else "". The v0.2.0 setup script (install-daemon.sh, TASK-004) rsyncs the
// daemon Python source from the .app bundle into DaemonSourceDir() so the
// running app can edit / rotate the daemon source without re-signing the
// bundle. StartJarvis (TASK-009) prefers this path over the legacy bundled
// daemon script.
func InstalledDaemonScript() string {
	p := filepath.Join(DaemonSourceDir(), "main.py")
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
