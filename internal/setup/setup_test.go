package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
)

// TestSetupStateJSONRoundTrip asserts that marshalling a fully-populated
// SetupState and unmarshalling it back yields an equal value. This guards
// against future refactors silently renaming a JSON tag that the React
// SetupScreen depends on.
func TestSetupStateJSONRoundTrip(t *testing.T) {
	original := SetupState{
		Complete:       false,
		Phase:          PhaseVibeVoice,
		PhaseProgress:  42,
		PhaseDoneCount: 2,
		SetupVersion:   SetupExpectedVersion,
		LastError:      "network timeout",
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SetupState
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got != original {
		t.Errorf("round-trip mismatch\n  got:  %+v\n  want: %+v", got, original)
	}

	// Sanity-check that the JSON contains the expected wire keys (camelCase,
	// matching the TS SetupStateEvent shape).
	str := string(raw)
	for _, key := range []string{
		`"complete"`,
		`"phase"`,
		`"phaseProgress"`,
		`"phaseDoneCount"`,
		`"setupVersion"`,
		`"lastError"`,
	} {
		if !strings.Contains(str, key) {
			t.Errorf("JSON missing key %s: %s", key, str)
		}
	}
}

// TestSetupStateOmitEmpty asserts that when Phase is the zero value, the
// JSON output omits the "phase" and "lastError" keys. The React layer
// distinguishes "no current phase" from "empty-string phase" so the omit
// behaviour is part of the contract.
func TestSetupStateOmitEmpty(t *testing.T) {
	st := SetupState{
		Complete:     true,
		SetupVersion: SetupExpectedVersion,
	}

	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	str := string(raw)

	if strings.Contains(str, `"phase"`) {
		t.Errorf("expected zero Phase to be omitted; got: %s", str)
	}
	if strings.Contains(str, `"lastError"`) {
		t.Errorf("expected empty LastError to be omitted; got: %s", str)
	}
	// Non-omitempty fields should still be present even when at the zero value.
	for _, key := range []string{`"complete"`, `"phaseProgress"`, `"phaseDoneCount"`, `"setupVersion"`} {
		if !strings.Contains(str, key) {
			t.Errorf("expected key %s to be present even when zero; got: %s", key, str)
		}
	}
}

// TestSetupPhaseConstants pins the four SetupPhase string values. If a future
// refactor mistypes one of these, this test breaks loudly instead of letting
// the React SetupScreen render an "unknown phase" placeholder at runtime.
func TestSetupPhaseConstants(t *testing.T) {
	cases := map[SetupPhase]string{
		PhasePython:    "python_install",
		PhaseVenv:      "venv_install",
		PhaseVibeVoice: "vibevoice_download",
		PhaseWhisper:   "whisper_download",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("phase constant = %q; want %q", string(got), want)
		}
	}
}

// TestSetupExpectedVersion pins the sentinel version string so a bump becomes
// an explicit code review event (the bump is intentional in lockstep with a
// major install-flow change — see SetupExpectedVersion docs).
func TestSetupExpectedVersion(t *testing.T) {
	if SetupExpectedVersion != "0.2.0" {
		t.Errorf("SetupExpectedVersion = %q; want %q", SetupExpectedVersion, "0.2.0")
	}
}

// TestSentinelErrorsAreDistinct asserts the two exported sentinel errors are
// not aliases of each other and have non-empty messages. Callers in TASK-009
// and TASK-012 distinguish them with errors.Is, so identity matters.
func TestSentinelErrorsAreDistinct(t *testing.T) {
	if ErrSetupRequired == nil || ErrDaemonLaunchFailed == nil {
		t.Fatal("sentinel errors must not be nil")
	}
	if errors.Is(ErrSetupRequired, ErrDaemonLaunchFailed) {
		t.Error("ErrSetupRequired must not match ErrDaemonLaunchFailed")
	}
	if errors.Is(ErrDaemonLaunchFailed, ErrSetupRequired) {
		t.Error("ErrDaemonLaunchFailed must not match ErrSetupRequired")
	}
	if ErrSetupRequired.Error() == "" || ErrDaemonLaunchFailed.Error() == "" {
		t.Error("sentinel errors must have non-empty messages")
	}
}

// ---------------------------------------------------------------------------
// TASK-008: sentinel write + read + version comparison
// ---------------------------------------------------------------------------

// setupTestHome redirects $HOME (which paths.JarvisHome consults via
// os.UserHomeDir) to t.TempDir() and creates a bundled requirements.txt fake
// with the supplied contents. Returns (testHome, requirementsPath).
//
// The pattern matches the convention established in app_jarvis_test.go: rather
// than reaching into paths.homeOverride (package-private), tests flip the
// $HOME env var and rely on os.UserHomeDir to pick it up.
func setupTestHome(t *testing.T, requirementsContent string) (string, string) {
	t.Helper()
	// Jarvis only targets darwin/linux in v0.2.0. On windows $HOME is not
	// consulted by os.UserHomeDir, so the redirection trick used here doesn't
	// hold — skip rather than produce false confidence.
	if runtime.GOOS == "windows" {
		t.Skip("setup-on-launch is darwin-only; sentinel tests skipped on windows")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Materialize a fake bundled requirements.txt outside the .jarvis dir so
	// tests can hash a stable file. Use a sibling path under tmp to keep it
	// isolated from any home-rooted scanning.
	reqPath := filepath.Join(tmp, "requirements.txt")
	if err := os.WriteFile(reqPath, []byte(requirementsContent), 0o644); err != nil {
		t.Fatalf("write fake requirements.txt: %v", err)
	}
	return tmp, reqPath
}

// hashFileForTest mirrors the in-package hashFile so tests can compute the
// expected sha without exporting the private helper. See hashFile in setup.go
// for the production implementation that this checks.
func hashFileForTest(t *testing.T, path string) string {
	t.Helper()
	sum, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile(%s): %v", path, err)
	}
	return sum
}

// TestWriteAndReadSentinelRoundTrip exercises the happy path: WriteSentinel
// followed by ReadSentinel should yield a struct whose four user-visible
// fields equal the input. Timestamp is compared truncated to seconds since
// the wire format is RFC 3339 (second precision).
func TestWriteAndReadSentinelRoundTrip(t *testing.T) {
	_, reqPath := setupTestHome(t, "requests==2.32.0\nnumpy==2.0.0\n")
	sum := hashFileForTest(t, reqPath)

	now := time.Now().UTC().Truncate(time.Second)
	in := SentinelData{
		Version:            SetupExpectedVersion,
		Timestamp:          now,
		RequirementsSHA256: sum,
		PythonPBSTag:       "20260510",
	}
	if err := WriteSentinel(in); err != nil {
		t.Fatalf("WriteSentinel: %v", err)
	}

	// Sentinel file should exist at the expected path.
	if _, err := os.Stat(paths.SetupSentinelPath(SetupExpectedVersion)); err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}

	got, err := ReadSentinel(reqPath)
	if err != nil {
		t.Fatalf("ReadSentinel: %v", err)
	}
	if got.Version != in.Version {
		t.Errorf("Version: got %q want %q", got.Version, in.Version)
	}
	if !got.Timestamp.Equal(in.Timestamp) {
		t.Errorf("Timestamp: got %v want %v", got.Timestamp, in.Timestamp)
	}
	if !strings.EqualFold(got.RequirementsSHA256, in.RequirementsSHA256) {
		t.Errorf("RequirementsSHA256: got %q want %q", got.RequirementsSHA256, in.RequirementsSHA256)
	}
	if got.PythonPBSTag != in.PythonPBSTag {
		t.Errorf("PythonPBSTag: got %q want %q", got.PythonPBSTag, in.PythonPBSTag)
	}
}

// TestReadSentinel_MissingFile asserts ReadSentinel returns ErrSetupRequired
// (wrapped) when ~/.jarvis/.setup-version-<version> does not exist. This is
// the most common path — every fresh install hits it on first launch.
func TestReadSentinel_MissingFile(t *testing.T) {
	_, reqPath := setupTestHome(t, "requests==2.32.0\n")
	// Intentionally do NOT call WriteSentinel.

	_, err := ReadSentinel(reqPath)
	if err == nil {
		t.Fatalf("ReadSentinel with missing file = nil; want ErrSetupRequired")
	}
	if !errors.Is(err, ErrSetupRequired) {
		t.Errorf("err = %v; want errors.Is(err, ErrSetupRequired) == true", err)
	}
}

// TestReadSentinel_VersionMismatch asserts that a sentinel reporting an older
// Jarvis version (e.g. 0.1.0) does not unlock the current expected version.
// We hand-craft a file at the expected path with stale `version:` metadata
// inside — this simulates a future where SetupExpectedVersion is bumped and
// the user still has a previous-generation sentinel on disk.
func TestReadSentinel_VersionMismatch(t *testing.T) {
	_, reqPath := setupTestHome(t, "requests==2.32.0\n")
	sum := hashFileForTest(t, reqPath)

	path := paths.SetupSentinelPath(SetupExpectedVersion)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	contents := "version: 0.1.0\n" +
		"timestamp: 2026-01-01T00:00:00Z\n" +
		"requirements_sha256: " + sum + "\n" +
		"python_pbs_tag: 20260101\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write stale sentinel: %v", err)
	}

	_, err := ReadSentinel(reqPath)
	if err == nil {
		t.Fatalf("ReadSentinel with stale version = nil; want ErrSetupRequired")
	}
	if !errors.Is(err, ErrSetupRequired) {
		t.Errorf("err = %v; want errors.Is(err, ErrSetupRequired) == true", err)
	}
}

// TestReadSentinel_SHAMismatch asserts that a sentinel recording sha=X does
// not unlock setup when the on-disk bundled requirements.txt has sha=Y. This
// is the path that re-triggers SetupScreen after a Jarvis upgrade bumps the
// Python deps without bumping SetupExpectedVersion.
func TestReadSentinel_SHAMismatch(t *testing.T) {
	tmp, reqPath := setupTestHome(t, "old-deps==1.0.0\n")
	oldSum := hashFileForTest(t, reqPath)

	// Write a sentinel that records the OLD requirements sha.
	if err := WriteSentinel(SentinelData{
		Version:            SetupExpectedVersion,
		Timestamp:          time.Now().UTC().Truncate(time.Second),
		RequirementsSHA256: oldSum,
		PythonPBSTag:       "20260510",
	}); err != nil {
		t.Fatalf("WriteSentinel: %v", err)
	}

	// Now create a *different* bundled requirements file (sha=Y).
	newReq := filepath.Join(tmp, "requirements-new.txt")
	if err := os.WriteFile(newReq, []byte("new-deps==2.0.0\n"), 0o644); err != nil {
		t.Fatalf("write new requirements: %v", err)
	}
	// Sanity check that the hashes really differ — otherwise the test is moot.
	newSum := hashFileForTest(t, newReq)
	if newSum == oldSum {
		t.Fatalf("test setup error: new and old requirements hash to the same value")
	}

	_, err := ReadSentinel(newReq)
	if err == nil {
		t.Fatalf("ReadSentinel with sha mismatch = nil; want ErrSetupRequired")
	}
	if !errors.Is(err, ErrSetupRequired) {
		t.Errorf("err = %v; want errors.Is(err, ErrSetupRequired) == true", err)
	}
}

// TestReadSentinel_MalformedFile asserts ReadSentinel does not panic on random
// bytes and returns ErrSetupRequired so the SetupScreen surfaces cleanly. The
// "random bytes" case exercises both the no-recognized-keys branch in
// parseSentinel and the bufio.Scanner buffer handling.
func TestReadSentinel_MalformedFile(t *testing.T) {
	_, reqPath := setupTestHome(t, "requests==2.32.0\n")

	path := paths.SetupSentinelPath(SetupExpectedVersion)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pile of garbage with no `key: value` pairs and no recognized keys.
	garbage := []byte("\x00\x01\x02not a valid sentinel\nrandom garbage line\n!!!!!\n")
	if err := os.WriteFile(path, garbage, 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ReadSentinel panicked on malformed file: %v", r)
		}
	}()

	_, err := ReadSentinel(reqPath)
	if err == nil {
		t.Fatalf("ReadSentinel with garbage = nil; want ErrSetupRequired")
	}
	if !errors.Is(err, ErrSetupRequired) {
		t.Errorf("err = %v; want errors.Is(err, ErrSetupRequired) == true", err)
	}
}

// TestIsSetupComplete_HappyPath verifies that a valid sentinel pointing at an
// existing requirements.txt with matching sha causes IsSetupComplete to return
// true. This is the green-path StartJarvis case.
//
// It also exercises the "bundled file is missing" branch: IsSetupComplete must
// collapse to false rather than letting the I/O error bubble up to callers.
func TestIsSetupComplete_HappyPath(t *testing.T) {
	_, reqPath := setupTestHome(t, "requests==2.32.0\nflask==3.0.0\n")
	sum := hashFileForTest(t, reqPath)

	if err := WriteSentinel(SentinelData{
		Version:            SetupExpectedVersion,
		Timestamp:          time.Now().UTC().Truncate(time.Second),
		RequirementsSHA256: sum,
		PythonPBSTag:       "20260510",
	}); err != nil {
		t.Fatalf("WriteSentinel: %v", err)
	}

	if !IsSetupComplete(reqPath) {
		t.Errorf("IsSetupComplete = false; want true (sentinel was just written)")
	}

	// And the missing-bundled-requirements case must collapse to false.
	if IsSetupComplete(filepath.Join(t.TempDir(), "does-not-exist.txt")) {
		t.Errorf("IsSetupComplete = true for missing bundled requirements; want false")
	}
}

// TestWriteSentinel_AtomicOnError injects a write failure by pre-creating
// ~/.jarvis as a regular file (not a directory). WriteSentinel's internal
// MkdirAll then fails, so neither the .tmp file nor the final sentinel
// materializes — satisfying the atomic-or-nothing contract.
//
// We deliberately avoid chmod-based permission tricks: chmod 0 on darwin
// behaves differently than linux for root/non-root test runners, and the
// "file-where-directory-expected" trick is portable.
func TestWriteSentinel_AtomicOnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("setup-on-launch is darwin-only; atomic-write test skipped on windows")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	jarvisDir := filepath.Join(tmp, ".jarvis")
	if err := os.WriteFile(jarvisDir, []byte("not-a-directory"), 0o644); err != nil {
		t.Fatalf("pre-create .jarvis as file: %v", err)
	}

	err := WriteSentinel(SentinelData{
		Version:            SetupExpectedVersion,
		Timestamp:          time.Now().UTC().Truncate(time.Second),
		RequirementsSHA256: "deadbeef",
		PythonPBSTag:       "20260510",
	})
	if err == nil {
		t.Fatalf("WriteSentinel against file-where-dir-expected = nil; want error")
	}

	// Target sentinel must not exist on disk after a failed write. We can't
	// rely on os.IsNotExist alone here because os.Stat against a path whose
	// parent is a regular file returns ENOTDIR ("not a directory"), not
	// ENOENT — but the file is still "not there" for our purposes. Treat any
	// non-nil stat error as proof of absence.
	target := paths.SetupSentinelPath(SetupExpectedVersion)
	if info, statErr := os.Stat(target); statErr == nil {
		t.Errorf("target %s exists after failed WriteSentinel; info = %+v", target, info)
	}
	// And the .tmp sibling must not be left behind.
	if info, statErr := os.Stat(target + ".tmp"); statErr == nil {
		t.Errorf("tmp %s.tmp exists after failed WriteSentinel; info = %+v", target, info)
	}
}

// TestWriteSentinel_AtomicRenameLeavesNoTmp is a stronger guarantee on the
// success path: after a clean WriteSentinel, no `.tmp` sibling should linger.
// Refactors have historically broken this (e.g. by accidentally copying
// instead of renaming) and the bug is silent at the user level.
func TestWriteSentinel_AtomicRenameLeavesNoTmp(t *testing.T) {
	_, reqPath := setupTestHome(t, "requests==2.32.0\n")
	sum := hashFileForTest(t, reqPath)

	if err := WriteSentinel(SentinelData{
		Version:            SetupExpectedVersion,
		Timestamp:          time.Now().UTC().Truncate(time.Second),
		RequirementsSHA256: sum,
		PythonPBSTag:       "20260510",
	}); err != nil {
		t.Fatalf("WriteSentinel: %v", err)
	}

	target := paths.SetupSentinelPath(SetupExpectedVersion)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("sentinel target missing after WriteSentinel: %v", err)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp lingered after successful rename; stat err = %v", err)
	}
}

// TestParseSentinel_IgnoresUnknownKeysAndComments asserts the forward-compat
// guarantee: a file with unknown keys, blank lines, and `#` comments must
// still parse cleanly as long as at least one recognized key is present. This
// lets a future v0.3.0 add fields without breaking v0.2.0 readers (the
// sentinel ages forward, the reader doesn't).
func TestParseSentinel_IgnoresUnknownKeysAndComments(t *testing.T) {
	_, reqPath := setupTestHome(t, "requests==2.32.0\n")
	sum := hashFileForTest(t, reqPath)

	path := paths.SetupSentinelPath(SetupExpectedVersion)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	contents := "# top-level comment\n" +
		"\n" +
		"version: " + SetupExpectedVersion + "\n" +
		"unknown_future_key: some-value\n" +
		"timestamp: 2026-05-12T10:30:00Z\n" +
		"# mid-file comment\n" +
		"requirements_sha256: " + sum + "\n" +
		"python_pbs_tag: 20260510\n" +
		"another_unknown: x\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	got, err := ReadSentinel(reqPath)
	if err != nil {
		t.Fatalf("ReadSentinel: %v", err)
	}
	if got.Version != SetupExpectedVersion {
		t.Errorf("Version = %q; want %q", got.Version, SetupExpectedVersion)
	}
	if got.PythonPBSTag != "20260510" {
		t.Errorf("PythonPBSTag = %q; want %q", got.PythonPBSTag, "20260510")
	}
}
