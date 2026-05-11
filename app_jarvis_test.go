package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/namanchopra/jarvis/internal/paths"
)

// TestOpenDaemonLog_HappyPath asserts that when ~/.jarvis/logs/daemon.log
// exists, OpenDaemonLog returns nil. We can't actually verify that the
// macOS `open` command did anything (it dispatches via Launch Services to a
// GUI editor) — this test exercises the binding plumbing: path resolution,
// existence check, and command invocation. The `open` binary itself is a
// no-op when handed a regular file under headless test runners.
func TestOpenDaemonLog_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	// paths.JarvisHome() consults os.UserHomeDir() which reads $HOME on
	// darwin/linux. Redirect $HOME at the temp dir so paths.DataPath
	// resolves underneath it without touching the real ~/.jarvis.
	t.Setenv("HOME", tmp)

	logPath := paths.DataPath("logs", "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir logs dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("[jarvis-daemon] test log line\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	a := &App{}
	if err := a.OpenDaemonLog(); err != nil {
		t.Fatalf("OpenDaemonLog() = %v; want nil", err)
	}
}

// TestOpenDaemonLog_MissingFile asserts that when the daemon log does not
// exist (e.g. the daemon has never been launched), OpenDaemonLog returns a
// wrapped error whose message contains "OpenDaemonLog:" per the repo-wide
// error-wrapping convention.
func TestOpenDaemonLog_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Intentionally do NOT create the log file.

	a := &App{}
	err := a.OpenDaemonLog()
	if err == nil {
		t.Fatalf("OpenDaemonLog() with missing log = nil; want error")
	}
	if !strings.Contains(err.Error(), "OpenDaemonLog:") {
		t.Errorf("error message = %q; expected to contain %q", err.Error(), "OpenDaemonLog:")
	}
}
