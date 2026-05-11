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
