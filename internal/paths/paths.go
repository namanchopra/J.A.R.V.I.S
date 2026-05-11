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
