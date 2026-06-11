// Package paths provides canonical filesystem path helpers for the Jarvis
// data directory (~/.jarvis) and its sub-directories.
//
// All Jarvis runtime data lives under JarvisHome() which defaults to
// "$HOME/.jarvis" on macOS and "%USERPROFILE%\.jarvis" on Windows. Legacy
// installations stored data under "$HOME/.awm"; see migrate.go (TASK-018) for
// the migration shim that one-shot copies legacy data into the new location
// and symlinks for backward compat.
//
// On rare environments where os.UserHomeDir() fails (e.g., no $HOME or no
// %USERPROFILE% set), helpers fall back to a platform-specific safe directory
// (see fallbackJarvisHome in paths_darwin.go and paths_windows.go) so the app
// can continue running rather than panicking.
//
// Platform-specific behaviour (interpreter layout, bundle resolution, fallback
// home) lives in paths_darwin.go and paths_windows.go. This file holds the
// cross-platform helpers that both build tags depend on.
package paths

import (
	"log/slog"
	"os"
	"path/filepath"
)

// homeOverride lets tests redirect JarvisHome/LegacyHome to a temporary
// directory without touching the real $HOME. Production callers leave it nil.
var homeOverride func() string

// executableOverride lets tests simulate a bundled .app (macOS) or
// next-to-exe Resources (Windows) layout without rebuilding the binary. When
// nil (production), os.Executable is used. Tests set this to a function that
// returns a fake exe path such as "/tmp/Jarvis.app/Contents/MacOS/jarvis" to
// exercise the bundle helpers.
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
// typically "$HOME/.jarvis" on macOS and "%USERPROFILE%\.jarvis" on Windows.
// Falls back to a platform-specific safe directory (see fallbackJarvisHome)
// when os.UserHomeDir() fails AND no homeOverride is set.
func JarvisHome() string {
	if homeOverride != nil {
		return filepath.Join(homeOverride(), ".jarvis")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		slog.Warn("paths: os.UserHomeDir failed; using platform fallback", "err", err)
		return fallbackJarvisHome()
	}
	return filepath.Join(home, ".jarvis")
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

// InstalledDaemonScript returns ~/.jarvis/jarvis-daemon/main.py if it exists,
// else "". The v0.2.0 setup script (install-daemon.sh / install-daemon.ps1,
// TASK-004) rsyncs/copies the daemon Python source from the bundled
// Resources/jarvis-daemon/ into DaemonSourceDir() so the running app can edit /
// rotate the daemon source without re-signing the bundle. StartJarvis
// (TASK-009) prefers this path over the legacy bundled daemon script.
//
// The script file name (main.py) is identical across platforms, so this helper
// is cross-platform. Platform differences live only in InstalledPython /
// InstalledDaemonVenvPython (where the interpreter layout differs between
// Unix's bin/ and Windows's Scripts/).
func InstalledDaemonScript() string {
	p := filepath.Join(DaemonSourceDir(), "main.py")
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}
