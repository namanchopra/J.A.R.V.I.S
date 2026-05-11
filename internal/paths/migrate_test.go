package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestHome configures homeOverride to point JarvisHome/LegacyHome under
// t.TempDir() so the test runs against an isolated filesystem.
// Returns the test home directory; the caller can construct the legacy/new paths via:
//
//	filepath.Join(testHome, ".awm") and filepath.Join(testHome, ".jarvis")
func setupTestHome(t *testing.T) string {
	t.Helper()
	testHome := t.TempDir()
	homeOverride = func() string { return testHome }
	t.Cleanup(func() { homeOverride = nil })
	return testHome
}

// TestMigrateNoOpWhenTargetExists verifies that when ~/.jarvis/ already exists,
// MigrateLegacyHome returns nil without touching ~/.awm/.
func TestMigrateNoOpWhenTargetExists(t *testing.T) {
	testHome := setupTestHome(t)
	awm := filepath.Join(testHome, ".awm")
	jarvis := filepath.Join(testHome, ".jarvis")

	// Pre-populate both — target exists takes precedence.
	if err := os.MkdirAll(awm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(jarvis, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(awm, "marker"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jarvis, "marker"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyHome(); err != nil {
		t.Fatalf("MigrateLegacyHome() returned error on no-op case: %v", err)
	}

	// Verify ~/.awm is untouched (still a directory, not a symlink).
	info, err := os.Lstat(awm)
	if err != nil {
		t.Fatalf("stat legacy: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("MigrateLegacyHome should NOT have replaced ~/.awm with a symlink when target already exists")
	}

	// Verify ~/.awm/marker still has legacy content.
	got, err := os.ReadFile(filepath.Join(awm, "marker"))
	if err != nil {
		t.Fatalf("read legacy marker: %v", err)
	}
	if string(got) != "legacy" {
		t.Errorf("legacy marker = %q; want %q (untouched)", got, "legacy")
	}

	// Verify ~/.jarvis/marker still has its original content (not overwritten).
	gotJarvis, err := os.ReadFile(filepath.Join(jarvis, "marker"))
	if err != nil {
		t.Fatalf("read jarvis marker: %v", err)
	}
	if string(gotJarvis) != "new" {
		t.Errorf("jarvis marker = %q; want %q (untouched)", gotJarvis, "new")
	}
}

// TestMigrateNoOpWhenSourceMissing verifies that on a fresh install (no ~/.awm/),
// MigrateLegacyHome returns nil and creates nothing.
func TestMigrateNoOpWhenSourceMissing(t *testing.T) {
	testHome := setupTestHome(t)
	awm := filepath.Join(testHome, ".awm")
	jarvis := filepath.Join(testHome, ".jarvis")

	// Neither exists — true fresh install.
	if err := MigrateLegacyHome(); err != nil {
		t.Fatalf("MigrateLegacyHome() returned error for fresh install: %v", err)
	}

	if _, err := os.Stat(awm); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected ~/.awm to remain absent; got err=%v", err)
	}
	if _, err := os.Stat(jarvis); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected ~/.jarvis to remain absent; got err=%v", err)
	}
}

// TestMigrateHappyPath verifies the full migration path: copy contents, then
// replace ~/.awm/ with a symlink to ~/.jarvis/.
func TestMigrateHappyPath(t *testing.T) {
	testHome := setupTestHome(t)
	awm := filepath.Join(testHome, ".awm")
	jarvis := filepath.Join(testHome, ".jarvis")

	// Pre-populate ~/.awm/ with realistic files.
	if err := os.MkdirAll(filepath.Join(awm, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(awm, "config.json"), []byte(`{"key":"value"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(awm, "awm.db"), []byte("sqlite-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(awm, "logs", "session-1.log"), []byte("log line"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyHome(); err != nil {
		t.Fatalf("MigrateLegacyHome() failed: %v", err)
	}

	// ~/.jarvis/ should contain the data.
	for _, p := range []string{"config.json", "awm.db", filepath.Join("logs", "session-1.log")} {
		if _, err := os.Stat(filepath.Join(jarvis, p)); err != nil {
			t.Errorf("expected ~/.jarvis/%s to exist; err=%v", p, err)
		}
	}

	// Verify migrated content matches.
	gotCfg, err := os.ReadFile(filepath.Join(jarvis, "config.json"))
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if string(gotCfg) != `{"key":"value"}` {
		t.Errorf("migrated config.json = %q; want %q", gotCfg, `{"key":"value"}`)
	}
	gotDB, err := os.ReadFile(filepath.Join(jarvis, "awm.db"))
	if err != nil {
		t.Fatalf("read migrated db: %v", err)
	}
	if string(gotDB) != "sqlite-data" {
		t.Errorf("migrated awm.db = %q; want %q", gotDB, "sqlite-data")
	}

	// ~/.awm should now be a symlink to ~/.jarvis.
	info, err := os.Lstat(awm)
	if err != nil {
		t.Fatalf("lstat legacy: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("~/.awm should be a symlink after migration; got mode=%v", info.Mode())
	}
	target, err := os.Readlink(awm)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != jarvis {
		t.Errorf("~/.awm symlink target = %q; want %q", target, jarvis)
	}

	// Reading via symlink should yield the migrated data.
	got, err := os.ReadFile(filepath.Join(awm, "config.json"))
	if err != nil {
		t.Fatalf("read via symlink: %v", err)
	}
	if string(got) != `{"key":"value"}` {
		t.Errorf("config.json via symlink = %q; want %q", got, `{"key":"value"}`)
	}

	// Reading the nested log file via symlink should also work.
	gotLog, err := os.ReadFile(filepath.Join(awm, "logs", "session-1.log"))
	if err != nil {
		t.Fatalf("read nested file via symlink: %v", err)
	}
	if string(gotLog) != "log line" {
		t.Errorf("logs/session-1.log via symlink = %q; want %q", gotLog, "log line")
	}
}

// TestMigrateFailureLeavesSourceIntact verifies that when migration fails (because
// the destination is unwritable), the source ~/.awm/ is unchanged and any partial
// destination state has been cleaned up.
func TestMigrateFailureLeavesSourceIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission test would not fail")
	}
	testHome := setupTestHome(t)
	awm := filepath.Join(testHome, ".awm")
	jarvis := filepath.Join(testHome, ".jarvis")

	// Source has a real file.
	if err := os.MkdirAll(awm, 0o755); err != nil {
		t.Fatal(err)
	}
	srcContent := []byte("important user data")
	if err := os.WriteFile(filepath.Join(awm, "config.json"), srcContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Make testHome unwritable so creating ~/.jarvis fails.
	if err := os.Chmod(testHome, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(testHome, 0o755) }) // restore for tempdir cleanup

	err := MigrateLegacyHome()
	if err == nil {
		t.Fatal("expected MigrateLegacyHome() to return error when destination is unwritable")
	}
	if !strings.Contains(err.Error(), "MigrateLegacyHome") {
		t.Errorf("error not wrapped with MigrateLegacyHome prefix: %v", err)
	}

	// ~/.awm should still be a directory (not symlink) with the original file intact.
	info, lerr := os.Lstat(awm)
	if lerr != nil {
		t.Fatalf("lstat legacy after failure: %v", lerr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("MigrateLegacyHome should NOT have replaced ~/.awm with a symlink when migration failed")
	}
	if !info.IsDir() {
		t.Errorf("~/.awm should still be a directory; got mode=%v", info.Mode())
	}
	got, rerr := os.ReadFile(filepath.Join(awm, "config.json"))
	if rerr != nil {
		t.Fatalf("read original after failure: %v", rerr)
	}
	if string(got) != string(srcContent) {
		t.Errorf("source data corrupted after failed migration: got %q; want %q", got, srcContent)
	}

	// ~/.jarvis should be absent or empty (cleanup via os.RemoveAll on failure path).
	if jinfo, jerr := os.Stat(jarvis); jerr == nil {
		if !jinfo.IsDir() {
			t.Errorf("~/.jarvis exists but is not a directory: mode=%v", jinfo.Mode())
		} else {
			entries, derr := os.ReadDir(jarvis)
			if derr != nil {
				t.Fatalf("readdir jarvis: %v", derr)
			}
			if len(entries) != 0 {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("~/.jarvis should be empty after failed migration; got entries: %v", names)
			}
		}
	} else if !errors.Is(jerr, os.ErrNotExist) {
		t.Errorf("unexpected error checking ~/.jarvis after failure: %v", jerr)
	}
}
