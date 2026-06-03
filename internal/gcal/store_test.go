package gcal

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/model"
)

// TestLoadConfigMissingFile verifies the documented "first run" contract:
// when the on-disk file does not exist, LoadConfig returns a zero-value
// GCalConfig with a nil error so callers don't have to special-case
// os.ErrNotExist before every read.
func TestLoadConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gcal.json")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(missing): got error %v, want nil", err)
	}
	if !reflect.DeepEqual(cfg, model.GCalConfig{}) {
		t.Errorf("LoadConfig(missing): got %+v, want zero-value GCalConfig", cfg)
	}
}

// TestSaveConfigRoundTrip verifies the basic invariant that Save then Load
// returns an equal struct — guards against JSON-tag drift, time-zone
// truncation, or accidental field omission on either side.
func TestSaveConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gcal.json")

	// time.Time round-trips through JSON only to nanosecond precision in
	// the local zone via RFC3339; use a UTC truncated-to-second value so
	// reflect.DeepEqual is reliable across platforms.
	want := model.GCalConfig{
		AccessToken:  "ya29.a0AfH6SMC-access",
		RefreshToken: "1//0g-refresh",
		ExpiresAt:    time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
		ClientID:     "cid.apps.googleusercontent.com",
		ClientSecret: "GOCSPX-secret",
	}

	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after Save: %v", err)
	}
	// Normalize ExpiresAt to UTC because JSON round-trip preserves the
	// instant but Go may decode into Local — equal instants compare unequal
	// via reflect.DeepEqual due to the *Location pointer.
	got.ExpiresAt = got.ExpiresAt.UTC()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

// TestSaveConfigAtomic is the regression test for the tmp+rename atomic-write
// contract. We force the rename to fail by pre-creating a non-empty directory
// at the destination path — os.Rename refuses to clobber a non-empty directory
// with a regular file on every supported OS. The contract under test:
//
//  1. SaveConfig must return an error.
//  2. The intermediate "<path>.tmp" must NOT remain on disk afterwards —
//     a leftover .tmp would mean a future SaveConfig could collide, and
//     more importantly a half-written tmp on disk is the precise failure
//     mode the atomic-write was designed to prevent leaking.
func TestSaveConfigAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gcal.json")

	// Pre-create a non-empty directory at the destination path. os.Rename
	// fails on a non-empty target directory because it can't replace it
	// with a regular file.
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("setup: mkdir at dest path: %v", err)
	}
	// Put a file inside so the directory is non-empty (on some platforms
	// os.Rename can succeed against an EMPTY target dir; non-empty is
	// universally rejected).
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: write blocker: %v", err)
	}

	cfg := model.GCalConfig{RefreshToken: "should-not-persist"}
	err := SaveConfig(path, cfg)
	if err == nil {
		t.Fatal("SaveConfig: expected error when rename target is a non-empty directory, got nil")
	}

	// The critical assertion: no leftover <path>.tmp.
	tmp := path + ".tmp"
	if _, statErr := os.Stat(tmp); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("leftover tmp file at %s after failed SaveConfig (stat err=%v) — atomic-write contract violated", tmp, statErr)
	}
}

// TestSaveConfigFileMode verifies the documented file/dir permission bits:
// the file holds OAuth refresh tokens so 0o600 (owner-only RW) is required,
// and the parent dir gets 0o700 (owner-only RWX) to defend against drive-by
// reads of the containing directory listing.
//
// Skipped on Windows because Go's os.FileMode bit semantics for permission
// bits aren't enforced on NTFS in the way Unix expects.
func TestSaveConfigFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits not enforced on Windows NTFS")
	}

	dir := t.TempDir()
	// Place the file under a NESTED subdirectory so the SaveConfig path
	// actually exercises os.MkdirAll(0o700) — the t.TempDir() itself was
	// created with the test runner's umask, which can vary.
	subDir := filepath.Join(dir, "gcal-mode-sub")
	path := filepath.Join(subDir, "gcal.json")

	cfg := model.GCalConfig{RefreshToken: "tok"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// File mode: 0o600.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode: got %#o, want %#o", got, 0o600)
	}

	// Dir mode: 0o700.
	di, err := os.Stat(subDir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("dir mode: got %#o, want %#o", got, 0o700)
	}
}
