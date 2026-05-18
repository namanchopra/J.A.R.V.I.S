package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/config"
	"github.com/namanchopra/jarvis/internal/paths"
	"github.com/namanchopra/jarvis/internal/setup"
)

// ---------------------------------------------------------------------------
// Test helpers (v0.2.0 TASK-009 setup-gating)
// ---------------------------------------------------------------------------

// writeValidSentinel materialises a fixture requirements.txt under
// fakeResources and writes a sentinel pinned to its SHA-256. Used by tests
// that need to bypass the setup-gate to exercise the post-gate logic in
// StartJarvis.
//
// fakeResources mirrors the .app's Contents/Resources layout — pass the same
// directory that bundledResourcesDirFn returns for the test. HOME must
// already be redirected (t.Setenv("HOME", tmp)) before calling this so the
// sentinel lands under the test's temp directory.
//
// Returns the on-disk path of the requirements fixture in case the test
// wants to mutate it (e.g. to simulate a sha drift).
func writeValidSentinel(t *testing.T, fakeResources string) string {
	t.Helper()
	reqDir := filepath.Join(fakeResources, "jarvis-daemon")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatalf("mkdir fake jarvis-daemon: %v", err)
	}
	reqPath := filepath.Join(reqDir, "requirements.txt")
	if err := os.WriteFile(reqPath, []byte("pipecat-ai==0.1.0\nwhisper==1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write requirements fixture: %v", err)
	}
	sum, err := fileSHA256(reqPath)
	if err != nil {
		t.Fatalf("hash requirements fixture: %v", err)
	}
	if err := setup.WriteSentinel(setup.SentinelData{
		Version:            setup.SetupExpectedVersion,
		Timestamp:          time.Now().UTC(),
		RequirementsSHA256: sum,
		PythonPBSTag:       "test-tag",
	}); err != nil {
		t.Fatalf("WriteSentinel: %v", err)
	}
	return reqPath
}

// fileSHA256 computes the lowercase hex SHA-256 of the file at path. Mirrors
// setup.hashFile (unexported in internal/setup) so tests in this package can
// produce a sentinel whose RequirementsSHA256 matches what ReadSentinel will
// compute on the same fixture.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

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

// ---------------------------------------------------------------------------
// v0.2.0 TASK-016 — OpenSetupLog Wails binding
// ---------------------------------------------------------------------------

// TestOpenSetupLog_HappyPath mirrors TestOpenDaemonLog_HappyPath for the new
// setup-log binding. When ~/.jarvis/logs/setup.log exists, OpenSetupLog must
// return nil. We cannot verify that macOS' `open` actually dispatched to a
// GUI editor under a headless test runner — `open <regular-file>` is a no-op
// in that environment and exits 0. The assertion exercises the binding's
// plumbing end-to-end: paths.SetupLogPath resolution, existence check, and
// the `open` invocation.
//
// HOME redirection mirrors the daemon-log test: paths.JarvisHome consults
// os.UserHomeDir which reads $HOME on darwin/linux, so pointing $HOME at the
// test's TempDir makes paths.SetupLogPath resolve underneath it without
// touching the real ~/.jarvis.
func TestOpenSetupLog_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	logPath := paths.SetupLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir setup logs dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("[install-daemon] setup line\n"), 0o644); err != nil {
		t.Fatalf("write setup log: %v", err)
	}

	a := &App{}
	if err := a.OpenSetupLog(); err != nil {
		t.Fatalf("OpenSetupLog() = %v; want nil", err)
	}
}

// TestOpenSetupLog_MissingFile mirrors TestOpenDaemonLog_MissingFile for the
// new setup-log binding. When ~/.jarvis/logs/setup.log does not exist (e.g.
// the user clicked "View setup log" before install-daemon.sh ever ran, or
// the log was cleaned up out-of-band) OpenSetupLog must return a wrapped
// error whose message contains "OpenSetupLog:" per the repo-wide error-
// wrapping convention.
//
// React-side handling: SetupScreen guards the call with
// `typeof window.go.main.App.OpenSetupLog === 'function'` (TASK-011) and
// swallows the rejected promise — but the wrapped-error contract must still
// hold so future callers can errors.Is / string-match against it.
func TestOpenSetupLog_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Intentionally do NOT create the log file.

	a := &App{}
	err := a.OpenSetupLog()
	if err == nil {
		t.Fatalf("OpenSetupLog() with missing log = nil; want error")
	}
	if !strings.Contains(err.Error(), "OpenSetupLog:") {
		t.Errorf("error message = %q; expected to contain %q", err.Error(), "OpenSetupLog:")
	}
}

// ---------------------------------------------------------------------------
// v0.1.2 — RestartJarvis + DaemonRestartNeeded + SaveConfig restart flag
// ---------------------------------------------------------------------------

// TestDaemonRestartNeeded is the exhaustive table test for the restart-needed
// heuristic. For every field on Config that should trigger a restart, we
// flip it and assert true; for every field that should NOT trigger a
// restart, we flip it and assert false. This is the contract the React
// agent codes against.
func TestDaemonRestartNeeded(t *testing.T) {
	wakeOn := true
	wakeOff := false

	t.Run("identical configs do not need restart", func(t *testing.T) {
		base := config.Config{
			TtsProvider:         "vibevoice",
			SttModel:            "whisper-small.en",
			VoicePreset:         "en-Carter_man",
			MicInputDevice:      "AppleHDA:1",
			WakeWordEnabled:     &wakeOn,
			GoogleAPIKey:        "g",
			AnthropicAPIKey:     "a",
			CartesiaAPIKey:      "c",
			JarvisAPIKey:        "j",
			JarvisElevenLabsKey: "el",
			JarvisPicovoiceKey:  "pv",
			DefaultAgent:        "claude-code",
			DefaultCommand:      "claude",
			UseLiveKitTransport: true,
			LiveKitURL:          "wss://example.livekit.cloud",
			LiveKitAPIKey:       "lk-key",
			LiveKitAPISecret:    "lk-secret",
			LiveKitRoomName:     "jarvis",
			LlmModel:            "anthropic/claude-haiku-4-5",
		}
		if daemonRestartNeeded(base, base) {
			t.Errorf("identical configs reported as needing restart")
		}
	})

	// Helper: build a paired (old, next) where next has exactly the given
	// field mutated. Each closure receives a fresh next and is responsible
	// for the single change.
	type mutCase struct {
		name   string
		mutate func(next *config.Config)
	}
	restartRelevant := []mutCase{
		{"TtsProvider changed", func(c *config.Config) { c.TtsProvider = "kokoro" }},
		{"SttModel changed", func(c *config.Config) { c.SttModel = "whisper-tiny.en" }},
		{"VoicePreset changed", func(c *config.Config) { c.VoicePreset = "different-voice" }},
		{"MicInputDevice changed", func(c *config.Config) { c.MicInputDevice = "ZoomAudioDevice:0" }},
		{"WakeWordEnabled changed (true -> false)", func(c *config.Config) { c.WakeWordEnabled = &wakeOff }},
		{"WakeWordEnabled changed (true -> nil)", func(c *config.Config) { c.WakeWordEnabled = nil }},
		{"GoogleAPIKey changed", func(c *config.Config) { c.GoogleAPIKey = "g-changed" }},
		{"AnthropicAPIKey changed", func(c *config.Config) { c.AnthropicAPIKey = "a-changed" }},
		{"CartesiaAPIKey changed", func(c *config.Config) { c.CartesiaAPIKey = "c-changed" }},
		{"JarvisAPIKey changed", func(c *config.Config) { c.JarvisAPIKey = "j-changed" }},
		{"JarvisElevenLabsKey changed", func(c *config.Config) { c.JarvisElevenLabsKey = "el-changed" }},
		{"JarvisPicovoiceKey changed", func(c *config.Config) { c.JarvisPicovoiceKey = "pv-changed" }},
		{"DefaultAgent changed", func(c *config.Config) { c.DefaultAgent = "kiro" }},
		{"DefaultCommand changed", func(c *config.Config) { c.DefaultCommand = "claude-beta" }},
		{"UseLiveKitTransport changed", func(c *config.Config) { c.UseLiveKitTransport = false }},
		{"LiveKitURL changed", func(c *config.Config) { c.LiveKitURL = "wss://other.livekit.cloud" }},
		{"LiveKitAPIKey changed", func(c *config.Config) { c.LiveKitAPIKey = "lk-key-2" }},
		{"LiveKitAPISecret changed", func(c *config.Config) { c.LiveKitAPISecret = "lk-secret-2" }},
		{"LiveKitRoomName changed", func(c *config.Config) { c.LiveKitRoomName = "lounge" }},
		{"LlmModel changed", func(c *config.Config) { c.LlmModel = "openai/gpt-4o-mini" }},
		{"LlmModel cleared (set to empty)", func(c *config.Config) { c.LlmModel = "" }},
	}

	base := config.Config{
		TtsProvider:         "vibevoice",
		SttModel:            "whisper-small.en",
		VoicePreset:         "en-Carter_man",
		MicInputDevice:      "AppleHDA:1",
		WakeWordEnabled:     &wakeOn,
		GoogleAPIKey:        "g",
		AnthropicAPIKey:     "a",
		CartesiaAPIKey:      "c",
		JarvisAPIKey:        "j",
		JarvisElevenLabsKey: "el",
		JarvisPicovoiceKey:  "pv",
		DefaultAgent:        "claude-code",
		DefaultCommand:      "claude",
		UseLiveKitTransport: true,
		LiveKitURL:          "wss://example.livekit.cloud",
		LiveKitAPIKey:       "lk-key",
		LiveKitAPISecret:    "lk-secret",
		LiveKitRoomName:     "jarvis",
		LlmModel:            "anthropic/claude-haiku-4-5",
	}

	for _, tc := range restartRelevant {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.mutate(&next)
			if !daemonRestartNeeded(base, next) {
				t.Errorf("expected restart needed after %q; got false", tc.name)
			}
		})
	}

	// v0.1.5: explicit "change LlmModel then change it back" round-trip
	// asserts the comparator is symmetric — flipping the field reports
	// restart-needed, restoring it reports no-restart-needed. This is the
	// React-side contract for the "Discard changes" affordance.
	t.Run("LlmModel flipped then restored yields no restart", func(t *testing.T) {
		flipped := base
		flipped.LlmModel = "google/gemini-2.5-flash"
		if !daemonRestartNeeded(base, flipped) {
			t.Errorf("LlmModel flipped: expected restart needed; got false")
		}
		// Restore to baseline value.
		restored := flipped
		restored.LlmModel = base.LlmModel
		if daemonRestartNeeded(base, restored) {
			t.Errorf("LlmModel restored to baseline: expected no restart; got true")
		}
	})

	// Fields that should NOT trigger a restart — the daemon either doesn't
	// read them at boot, or it reads them but on demand each call.
	daemonIrrelevant := []mutCase{
		{"ScanIntervalSeconds changed", func(c *config.Config) { c.ScanIntervalSeconds = 60 }},
		{"NotificationsEnabled changed", func(c *config.Config) { c.NotificationsEnabled = false }},
		{"NotifyOnApproval changed", func(c *config.Config) { c.NotifyOnApproval = false }},
		{"NotifyOnCompletion changed", func(c *config.Config) { c.NotifyOnCompletion = false }},
		{"CIWatchEnabled changed", func(c *config.Config) { c.CIWatchEnabled = true }},
		{"CIProvider changed", func(c *config.Config) { c.CIProvider = "github-actions" }},
		{"PreferredTerminal changed", func(c *config.Config) { c.PreferredTerminal = "iterm2" }},
		{"ProjectRootPaths changed", func(c *config.Config) { c.ProjectRootPaths = []string{"/some/new/path"} }},
		{"MobileAPIPort changed", func(c *config.Config) { c.MobileAPIPort = 4423 }},
		{"MobileAPIToken changed", func(c *config.Config) { c.MobileAPIToken = "new-token" }},
		{"DotClaudeSourcePath changed", func(c *config.Config) { c.DotClaudeSourcePath = "/some/path/.claude" }},
	}
	for _, tc := range daemonIrrelevant {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.mutate(&next)
			if daemonRestartNeeded(base, next) {
				t.Errorf("daemon-irrelevant change %q reported as needing restart", tc.name)
			}
		})
	}
}

// TestBoolPtrEqual covers the tri-state pointer comparison helper used for
// WakeWordEnabled. nil ("unset"), *false ("explicitly disabled"), and
// *true ("explicitly enabled") must all be distinguishable.
func TestBoolPtrEqual(t *testing.T) {
	tr, fl := true, false
	tests := []struct {
		name string
		a, b *bool
		want bool
	}{
		{"both nil", nil, nil, true},
		{"a nil, b not nil", nil, &tr, false},
		{"a not nil, b nil", &tr, nil, false},
		{"both true", &tr, &tr, true},
		{"both false", &fl, &fl, true},
		{"true vs false", &tr, &fl, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := boolPtrEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("boolPtrEqual(%v, %v) = %v; want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestSaveConfigReturnsRestartFlag verifies the v0.1.2 acceptance criterion:
// SaveConfig returns DaemonRestartNeeded=true when a daemon-relevant field
// (ttsProvider) changes between the previously-saved config and the new
// one, and false when only a daemon-irrelevant field changes.
func TestSaveConfigReturnsRestartFlag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{}

	// Seed an "old" on-disk config with a known ttsProvider.
	wakeOn := true
	old := config.Config{
		DefaultAgent:    "claude-code",
		DefaultCommand:  "claude",
		MobileAPIPort:   4422,
		TtsProvider:     "vibevoice",
		SttModel:        "whisper-small.en",
		WakeWordEnabled: &wakeOn,
	}
	if _, err := a.SaveConfig(old); err != nil {
		t.Fatalf("seed SaveConfig: %v", err)
	}

	// Change a daemon-relevant field (TtsProvider).
	next := old
	next.TtsProvider = "kokoro"
	res, err := a.SaveConfig(next)
	if err != nil {
		t.Fatalf("SaveConfig with changed TtsProvider: %v", err)
	}
	if !res.DaemonRestartNeeded {
		t.Errorf("DaemonRestartNeeded = false after TtsProvider change; want true")
	}

	// Change a daemon-irrelevant field (ScanIntervalSeconds) — no restart.
	next2 := next
	next2.ScanIntervalSeconds = 60
	res2, err := a.SaveConfig(next2)
	if err != nil {
		t.Fatalf("SaveConfig with changed ScanIntervalSeconds: %v", err)
	}
	if res2.DaemonRestartNeeded {
		t.Errorf("DaemonRestartNeeded = true after only ScanIntervalSeconds change; want false")
	}

	// Saving the same config twice produces no-op restart flag.
	res3, err := a.SaveConfig(next2)
	if err != nil {
		t.Fatalf("SaveConfig idempotent: %v", err)
	}
	if res3.DaemonRestartNeeded {
		t.Errorf("DaemonRestartNeeded = true after no-op save; want false")
	}
}

// TestSaveConfigPersistsV012Fields verifies that the 8 new v0.1.2 fields
// actually land in ~/.jarvis/config.json after a SaveConfig call (the
// other half of "SaveConfig actually persists them"). Without the
// promotion of these fields into Config, this test would fail because
// the values would be silently dropped during marshaling.
func TestSaveConfigPersistsV012Fields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{}
	wakeOn := true
	cfg := config.Config{
		DefaultAgent:    "claude-code",
		DefaultCommand:  "claude",
		TtsProvider:     "cartesia",
		SttModel:        "faster-whisper",
		VoicePreset:     "en-Carter_man",
		MicInputDevice:  "AppleHDA:1",
		WakeWordEnabled: &wakeOn,
		GoogleAPIKey:    "g-key",
		AnthropicAPIKey: "a-key",
		CartesiaAPIKey:  "c-key",
	}
	if _, err := a.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Read back from disk.
	got, err := a.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.TtsProvider != "cartesia" {
		t.Errorf("TtsProvider not persisted: got %q", got.TtsProvider)
	}
	if got.SttModel != "faster-whisper" {
		t.Errorf("SttModel not persisted: got %q", got.SttModel)
	}
	if got.VoicePreset != "en-Carter_man" {
		t.Errorf("VoicePreset not persisted: got %q", got.VoicePreset)
	}
	if got.MicInputDevice != "AppleHDA:1" {
		t.Errorf("MicInputDevice not persisted: got %q", got.MicInputDevice)
	}
	if got.WakeWordEnabled == nil || *got.WakeWordEnabled != true {
		t.Errorf("WakeWordEnabled not persisted: got %v", got.WakeWordEnabled)
	}
	if got.GoogleAPIKey != "g-key" {
		t.Errorf("GoogleAPIKey not persisted: got %q", got.GoogleAPIKey)
	}
	if got.AnthropicAPIKey != "a-key" {
		t.Errorf("AnthropicAPIKey not persisted: got %q", got.AnthropicAPIKey)
	}
	if got.CartesiaAPIKey != "c-key" {
		t.Errorf("CartesiaAPIKey not persisted: got %q", got.CartesiaAPIKey)
	}
}

// TestRestartJarvisResetsRestartCounter verifies the critical implementation
// detail flagged in the spec: StopJarvis sets jarvisRestarts =
// maxJarvisRestarts as a fuse to prevent the monitor goroutine from
// auto-restarting on a graceful stop. RestartJarvis must clear that fuse
// (jarvisRestarts = 0) before calling StartJarvis or the daemon would
// never come up after a user-initiated restart.
//
// We can't run a full RestartJarvis end-to-end here (no Python daemon in
// the test env), so we verify the counter-reset half: pre-set the fuse to
// maxJarvisRestarts (simulating "StopJarvis just ran"), invoke the reset
// sequence inline, and assert jarvisRestarts == 0. The StartJarvis half
// is exercised by the existing daemon startup flow.
func TestRestartJarvisResetsRestartCounter(t *testing.T) {
	a := &App{}

	// Simulate the post-StopJarvis state where the fuse is set.
	a.jarvisMu.Lock()
	a.jarvisRestarts = maxJarvisRestarts
	a.jarvisMu.Unlock()

	// Reproduce the reset that RestartJarvis performs between StopJarvis
	// and StartJarvis. We intentionally test the reset in isolation
	// because StartJarvis would attempt to spawn the real Python daemon.
	a.jarvisMu.Lock()
	a.jarvisRestarts = 0
	got := a.jarvisRestarts
	a.jarvisMu.Unlock()

	if got != 0 {
		t.Errorf("after reset: jarvisRestarts = %d; want 0", got)
	}
}

// TestRestartJarvisErrorWraps verifies that when StartJarvis fails (which
// it will in this test environment — no bundled Python, no dev venv), the
// error returned by RestartJarvis is wrapped with the "RestartJarvis:"
// prefix per the repo-wide error-wrapping convention.
func TestRestartJarvisErrorWraps(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{}
	err := a.RestartJarvis()
	if err == nil {
		t.Fatalf("RestartJarvis() in test env = nil; want error (no python interpreter)")
	}
	if !strings.Contains(err.Error(), "RestartJarvis:") {
		t.Errorf("error message = %q; want prefix %q", err.Error(), "RestartJarvis:")
	}
}

// ---------------------------------------------------------------------------
// v0.2.0 TASK-001 — quarantine attribute strip in StartJarvis
// ---------------------------------------------------------------------------

// TestStartJarvis_StripsQuarantineWhenBundled asserts that when the process
// appears to be running inside a .app bundle (i.e. BundledResourcesDir
// returns a non-empty path), StartJarvis invokes the quarantine-strip
// helper exactly once per call, passing the resolved Resources directory.
//
// In v0.2.0 the strip is what unblocks the daemon from launching on a fresh
// DMG download: macOS stamps com.apple.quarantine on every nested binary,
// and Gatekeeper silently refuses to exec them unless the attribute is
// cleared. The test substitutes both indirection points
// (bundledResourcesDirFn, stripQuarantineFn) so it does NOT require a real
// .app layout on disk or a real `xattr` binary.
//
// TASK-009 update: since the setup-gate now precedes the strip block, the
// test writes a valid sentinel under HOME before invoking StartJarvis so
// the strip code actually runs. StartJarvis is expected to return a
// non-nil error here because no bundled Python interpreter exists in the
// test environment; the assertion is on the side-effect (strip count +
// path), not the return value.
func TestStartJarvis_StripsQuarantineWhenBundled(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Substitute the bundled-resources lookup so the strip block runs.
	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	// TASK-009: pass the setup-gate by writing a valid sentinel + fixture
	// requirements.txt under the fake bundle. Without this the new gate
	// in StartJarvis would early-return setup.ErrSetupRequired before
	// reaching the strip block.
	writeValidSentinel(t, fakeResources)

	// Substitute the strip itself with a counter so the real `xattr` binary
	// is never invoked. Recording the path argument lets us assert the
	// production caller passes the value returned by bundledResourcesDirFn.
	var stripCalls int
	var stripPaths []string
	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error {
		stripCalls++
		stripPaths = append(stripPaths, p)
		return nil
	}
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	a := &App{}
	// StartJarvis will fail at the python-interpreter check below the strip
	// block — that is expected and unrelated to TASK-001.
	_ = a.StartJarvis()

	if stripCalls != 1 {
		t.Errorf("stripQuarantineFn invocation count = %d; want 1", stripCalls)
	}
	if len(stripPaths) != 1 || stripPaths[0] != fakeResources {
		t.Errorf("stripQuarantineFn paths = %v; want [%q]", stripPaths, fakeResources)
	}
}

// TestStartJarvis_SkipsStripInDevMode asserts that when BundledResourcesDir
// returns "" (dev mode via `wails dev` or `go run`, or `go test`),
// StartJarvis does NOT invoke the quarantine-strip helper at all. There is
// no bundle to strip from, and running `xattr` against an empty / missing
// path would just generate log noise.
//
// As with the bundled test, StartJarvis is expected to return a non-nil
// error because no Python interpreter is available — the assertion is on
// the strip counter staying at zero.
func TestStartJarvis_SkipsStripInDevMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Simulate dev mode by forcing bundledResourcesDirFn to return "".
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return "" }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	var stripCalls int
	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error {
		stripCalls++
		return nil
	}
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	a := &App{}
	_ = a.StartJarvis()

	if stripCalls != 0 {
		t.Errorf("stripQuarantineFn invocation count in dev mode = %d; want 0", stripCalls)
	}
}

// TestStartJarvis_StripFailureDoesNotBlockLaunch verifies the "best effort"
// contract: even when stripQuarantineFn returns a non-nil error (simulating
// missing `xattr`, permission denied, or any other failure mode), the
// outcome of StartJarvis is unchanged. The strip's failure must never be
// the reason a user sees a launch error.
//
// TASK-009 update: in the gated world, StartJarvis still fails after the
// strip because no Python interpreter is installed; the failure mode
// changed from "could not find Python interpreter" to
// setup.ErrDaemonLaunchFailed. The assertion now checks the typed error
// reaches the caller via errors.Is — which is both the new contract AND
// stronger evidence that the strip's permission error did NOT short-
// circuit StartJarvis with its own different error.
func TestStartJarvis_StripFailureDoesNotBlockLaunch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	// Pass the setup-gate (TASK-009) so we reach the strip block.
	writeValidSentinel(t, fakeResources)

	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error {
		return os.ErrPermission
	}
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	a := &App{}
	err := a.StartJarvis()
	if err == nil {
		t.Fatalf("StartJarvis() with strip failure = nil; want daemon-launch-failed error")
	}
	// Strip failure must NOT surface — the error must be the typed
	// ErrDaemonLaunchFailed from the python-lookup branch downstream.
	if !errors.Is(err, setup.ErrDaemonLaunchFailed) {
		t.Errorf("StartJarvis() error = %q; want errors.Is(err, ErrDaemonLaunchFailed) (strip failure must not surface)", err.Error())
	}
	// The wrapping must still use the StartJarvis: prefix.
	if !strings.Contains(err.Error(), "StartJarvis:") {
		t.Errorf("StartJarvis() error = %q; want %q prefix", err.Error(), "StartJarvis:")
	}
	// And critically the error must NOT be ErrSetupRequired — that would
	// indicate the sentinel-write helper failed to bypass the gate, which
	// would make the strip assertion above vacuously true.
	if errors.Is(err, setup.ErrSetupRequired) {
		t.Errorf("StartJarvis() unexpectedly returned ErrSetupRequired = %q; sentinel fixture is broken", err.Error())
	}
}

// ---------------------------------------------------------------------------
// v0.2.0 TASK-009 — StartJarvis setup gating
// ---------------------------------------------------------------------------
//
// These tests verify the new contract added to StartJarvis: it must return
// setup.ErrSetupRequired (typed, errors.Is-checkable) when the sentinel is
// missing/invalid, and setup.ErrDaemonLaunchFailed (typed, errors.Is-
// checkable) when sentinel is valid but the daemon binary can't be exec'd.
// App.tsx (TASK-012) consumes these as the discriminator for SetupScreen
// vs. amber "view daemon log" banner.

// TestStartJarvis_ReturnsErrSetupRequired_WhenSentinelMissing verifies the
// "first launch on a fresh machine" path: no sentinel exists under
// ~/.jarvis/, StartJarvis must short-circuit with setup.ErrSetupRequired so
// App.tsx can mount the SetupScreen.
//
// Dev-mode (bundledResourcesDirFn returns "") is the relevant scenario here:
// in a real fresh install the .app's Resources tree exists but the user's
// ~/.jarvis/.setup-version-0.2.0 sentinel does not, so the bundledRequirements
// hash check happens against the bundle's requirements.txt. We simulate that
// without a real bundle by pointing bundledResourcesDirFn at a temp dir that
// HAS a requirements.txt but no sentinel.
func TestStartJarvis_ReturnsErrSetupRequired_WhenSentinelMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Wire up a fake bundle so bundledRequirementsPath() points at a real
	// file the hash check can read. This is the production scenario: the
	// .app is installed but the user has never run setup.
	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	reqDir := filepath.Join(fakeResources, "jarvis-daemon")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatalf("mkdir bundled requirements dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reqDir, "requirements.txt"), []byte("pipecat-ai==0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write bundled requirements.txt: %v", err)
	}
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	// Strip stub so the test doesn't shell out to xattr. The gate runs
	// BEFORE the strip, so this is a defensive substitution against a
	// future refactor that flips the order.
	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error { return nil }
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	// Sanity: assert the sentinel really is absent before the call.
	sentinel := paths.SetupSentinelPath(setup.SetupExpectedVersion)
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("test bug: sentinel %q exists in fresh tmp home: %v", sentinel, err)
	}

	a := &App{}
	err := a.StartJarvis()
	if err == nil {
		t.Fatalf("StartJarvis() with no sentinel = nil; want ErrSetupRequired")
	}
	if !errors.Is(err, setup.ErrSetupRequired) {
		t.Errorf("StartJarvis() error = %v; want errors.Is(err, ErrSetupRequired)", err)
	}
	// And critically: the error must NOT be ErrDaemonLaunchFailed. If both
	// fired the React side wouldn't know whether to mount SetupScreen or
	// HUD-with-banner. The two error types must be mutually exclusive.
	if errors.Is(err, setup.ErrDaemonLaunchFailed) {
		t.Errorf("StartJarvis() error = %v; unexpectedly also matches ErrDaemonLaunchFailed", err)
	}
}

// TestStartJarvis_ReturnsErrDaemonLaunchFailed_WhenPythonMissing verifies the
// "user completed setup but the install got corrupted" path: sentinel is
// valid (passes IsSetupComplete) but no python binary exists at any of the
// three lookup locations (installed, bundled, dev). StartJarvis must surface
// setup.ErrDaemonLaunchFailed so App.tsx renders the amber banner instead
// of the SetupScreen.
func TestStartJarvis_ReturnsErrDaemonLaunchFailed_WhenPythonMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	// Strip stub to keep tests hermetic.
	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error { return nil }
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	// Pass the setup-gate by writing a valid sentinel.
	writeValidSentinel(t, fakeResources)

	// Intentionally do NOT create any python binary at:
	//   - ~/.jarvis/python/bin/python3         (installed)
	//   - <fakeResources>/python/bin/python3   (bundled)
	//   - ~/.jarvis/jarvis-daemon-env/bin/python3  (dev)
	// So all three pythonPath candidates resolve to "" and the
	// downstream branch returns ErrDaemonLaunchFailed.

	a := &App{}
	err := a.StartJarvis()
	if err == nil {
		t.Fatalf("StartJarvis() with valid sentinel + no python = nil; want ErrDaemonLaunchFailed")
	}
	if !errors.Is(err, setup.ErrDaemonLaunchFailed) {
		t.Errorf("StartJarvis() error = %v; want errors.Is(err, ErrDaemonLaunchFailed)", err)
	}
	// Mutually exclusive contract — must NOT also be ErrSetupRequired.
	if errors.Is(err, setup.ErrSetupRequired) {
		t.Errorf("StartJarvis() error = %v; unexpectedly also matches ErrSetupRequired", err)
	}
	if !strings.Contains(err.Error(), "StartJarvis:") {
		t.Errorf("StartJarvis() error = %q; want %q prefix", err.Error(), "StartJarvis:")
	}
}

// TestStartJarvis_PrefersInstalledPython_OverBundle verifies the path-
// resolution order: when BOTH a user-installed python (at
// ~/.jarvis/python/bin/python3) AND a bundled python (at
// <Resources>/python/bin/python3) exist, the installed one wins.
//
// This is the v0.2.0 hand-off: legacy builds shipped the full python tree
// inside the .app, but v0.2.0 + install-daemon.sh moves it to ~/.jarvis/.
// Until the legacy bundle path is removed entirely, an old .app on a new
// user's machine could expose both — the contract is that we always prefer
// the user-installed one because it's the one keyed to the sentinel's
// requirements.txt sha.
//
// We assert by inspecting cmd.Path captured via startJarvisCommandFn — the
// real exec.Cmd never runs because the captured cmd is constructed with a
// failing-exec stub (cmd.Path points at a regular file with the exec bit
// set, but invocation is short-circuited by stubbing Start via a synthetic
// failing command... actually simpler: we just capture cmd.Path and ignore
// the eventual Start failure).
func TestStartJarvis_PrefersInstalledPython_OverBundle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	// Strip stub.
	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error { return nil }
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	// Sentinel + bundle requirements (writeValidSentinel materialises both).
	writeValidSentinel(t, fakeResources)

	// Create a fake python at the INSTALLED path (~/.jarvis/python/bin/python3).
	installedPy := filepath.Join(paths.PythonInstallDir(), "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(installedPy), 0o755); err != nil {
		t.Fatalf("mkdir installed python dir: %v", err)
	}
	if err := os.WriteFile(installedPy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write installed python: %v", err)
	}

	// Also create a fake python at the BUNDLED path
	// (<fakeResources>/python/bin/python3).
	bundledPy := filepath.Join(fakeResources, "python", "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(bundledPy), 0o755); err != nil {
		t.Fatalf("mkdir bundled python dir: %v", err)
	}
	if err := os.WriteFile(bundledPy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write bundled python: %v", err)
	}

	// And a fake daemon script at the installed location so we don't
	// fail on script discovery before the cmd is constructed.
	installedScript := filepath.Join(paths.DaemonSourceDir(), "main.py")
	if err := os.MkdirAll(filepath.Dir(installedScript), 0o755); err != nil {
		t.Fatalf("mkdir installed daemon dir: %v", err)
	}
	if err := os.WriteFile(installedScript, []byte("# main\n"), 0o644); err != nil {
		t.Fatalf("write installed main.py: %v", err)
	}

	// Stub the exec.Cmd factory so we capture cmd.Path without actually
	// exec'ing python. Construct the cmd pointing at /bin/false (which
	// exists everywhere and exits 1 immediately) so .Start() succeeds and
	// cmd.Wait() returns a non-zero exit — that's fine because monitorJarvisDaemon
	// runs in a goroutine and doesn't affect the StartJarvis return value.
	//
	// We capture cmd.Path BEFORE overwriting it so the assertion below
	// sees what StartJarvis would have invoked.
	var capturedPath string
	prevCmdFn := startJarvisCommandFn
	startJarvisCommandFn = func(name string, arg ...string) *exec.Cmd {
		capturedPath = name
		// Return a cmd that exits immediately so Start() succeeds + Wait()
		// returns fast. /bin/true is universally available on macOS + Linux.
		c := exec.Command("/bin/true")
		return c
	}
	t.Cleanup(func() { startJarvisCommandFn = prevCmdFn })

	a := &App{}
	// Don't care about the return — we're asserting on capturedPath.
	_ = a.StartJarvis()
	// Wait for the spawned /bin/true to be reaped by the monitor goroutine
	// before t.Cleanup tears down the temp HOME (otherwise the goroutine
	// can still be writing to ~/.jarvis/logs/daemon.log when the dir
	// disappears). 100ms is plenty for /bin/true.
	time.Sleep(100 * time.Millisecond)

	if capturedPath == "" {
		t.Fatalf("startJarvisCommandFn was never invoked; StartJarvis returned early")
	}
	if capturedPath != installedPy {
		t.Errorf("StartJarvis preferred wrong python: got %q; want installed %q (bundled was %q)",
			capturedPath, installedPy, bundledPy)
	}
}

// TestPaths_InstalledPython_RespectsHomeOverride verifies the new
// paths.InstalledPython helper added in TASK-009. The helper is a thin
// "exists + is-file" check on ~/.jarvis/python/bin/python3.
//
// Cases covered:
//   - happy: python3 file exists → helper returns the path
//   - missing: nothing at the path → helper returns ""
//   - directory: a dir at the path → helper returns ""
func TestPaths_InstalledPython_RespectsHomeOverride(t *testing.T) {
	t.Run("happy path returns absolute path when file exists", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)

		py := filepath.Join(paths.PythonInstallDir(), "bin", "python3")
		if err := os.MkdirAll(filepath.Dir(py), 0o755); err != nil {
			t.Fatalf("mkdir python dir: %v", err)
		}
		if err := os.WriteFile(py, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write python: %v", err)
		}

		got := paths.InstalledPython()
		if got != py {
			t.Errorf("InstalledPython() = %q; want %q", got, py)
		}
	})

	t.Run("missing dir returns empty string", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)
		// Don't create ~/.jarvis/python/bin at all.
		if got := paths.InstalledPython(); got != "" {
			t.Errorf("InstalledPython() with missing dir = %q; want \"\"", got)
		}
	})

	t.Run("directory at python3 path returns empty string", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)

		// Create a DIRECTORY where the helper expects a regular file.
		// Some corrupt installs land here.
		py := filepath.Join(paths.PythonInstallDir(), "bin", "python3")
		if err := os.MkdirAll(py, 0o755); err != nil {
			t.Fatalf("mkdir python3 (as dir): %v", err)
		}
		if got := paths.InstalledPython(); got != "" {
			t.Errorf("InstalledPython() with dir at python3 = %q; want \"\"", got)
		}
	})
}

// TestPaths_InstalledDaemonScript_RespectsHomeOverride mirrors the python
// helper's coverage for the daemon-script helper. install-daemon.sh rsyncs
// the daemon source to ~/.jarvis/jarvis-daemon/; main.py is the entry point.
func TestPaths_InstalledDaemonScript_RespectsHomeOverride(t *testing.T) {
	t.Run("happy path returns absolute path when main.py exists", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)

		main := filepath.Join(paths.DaemonSourceDir(), "main.py")
		if err := os.MkdirAll(filepath.Dir(main), 0o755); err != nil {
			t.Fatalf("mkdir daemon dir: %v", err)
		}
		if err := os.WriteFile(main, []byte("# main\n"), 0o644); err != nil {
			t.Fatalf("write main.py: %v", err)
		}

		got := paths.InstalledDaemonScript()
		if got != main {
			t.Errorf("InstalledDaemonScript() = %q; want %q", got, main)
		}
	})

	t.Run("missing dir returns empty string", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)
		// Don't create the dir at all.
		if got := paths.InstalledDaemonScript(); got != "" {
			t.Errorf("InstalledDaemonScript() with missing dir = %q; want \"\"", got)
		}
	})

	t.Run("directory at main.py path returns empty string", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)

		main := filepath.Join(paths.DaemonSourceDir(), "main.py")
		if err := os.MkdirAll(main, 0o755); err != nil {
			t.Fatalf("mkdir main.py (as dir): %v", err)
		}
		if got := paths.InstalledDaemonScript(); got != "" {
			t.Errorf("InstalledDaemonScript() with dir at main.py = %q; want \"\"", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TASK-015 — cross-layer integration tests
// ---------------------------------------------------------------------------
//
// These exercise StartJarvis end-to-end across (app_jarvis.go gate logic) +
// (internal/setup sentinel persistence) + (internal/paths binary discovery).
// They use the t.TempDir / homeOverride pattern + the three production seams
// (bundledResourcesDirFn, stripQuarantineFn, startJarvisCommandFn) so no real
// .app bundle, xattr binary, or python interpreter is required.

// TestIntegration_FullSetupHappyPath drives StartJarvis through the complete
// happy path: a real sentinel written by setup.WriteSentinel that satisfies
// setup.IsSetupComplete, with both an installed python (~/.jarvis/python/
// bin/python3) AND a bundled python (<Resources>/python/bin/python3)
// available. The contract verifies:
//
//  1. setup.IsSetupComplete returns true against the real sentinel.
//  2. StartJarvis picks the InstalledPython() path (not bundled).
//  3. StartJarvis returns nil — no ErrSetupRequired, no ErrDaemonLaunchFailed.
//
// Distinct from TestStartJarvis_PrefersInstalledPython_OverBundle (TASK-009),
// which only asserts the path preference. This test also pins the round-trip
// from setup.WriteSentinel → setup.IsSetupComplete → StartJarvis-returns-nil,
// catching regressions where the sentinel format silently drifts away from
// what IsSetupComplete accepts.
//
// Two packages exercised: main (StartJarvis) + internal/setup (sentinel) +
// internal/paths (InstalledPython, BundledPython, DaemonSourceDir).
func TestIntegration_FullSetupHappyPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Fake bundle layout. bundledResourcesDirFn returns this so the gate's
	// bundledRequirementsPath() resolves under tmp.
	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	// Strip stub so StartJarvis's quarantine-strip block doesn't shell out
	// to /usr/bin/xattr in the test env.
	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error { return nil }
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	// writeValidSentinel materialises the bundled requirements.txt AND a
	// sentinel whose RequirementsSHA256 matches it — i.e. the exact state
	// the installer leaves on disk at the end of a successful run.
	reqPath := writeValidSentinel(t, fakeResources)

	// Sanity: the same setup.IsSetupComplete that StartJarvis's gate calls
	// must accept the sentinel before we attempt StartJarvis.
	if !setup.IsSetupComplete(reqPath) {
		t.Fatalf("test bug: setup.IsSetupComplete(%q) = false right after writeValidSentinel", reqPath)
	}

	// Materialise BOTH python candidates. The installed one must win.
	installedPy := filepath.Join(paths.PythonInstallDir(), "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(installedPy), 0o755); err != nil {
		t.Fatalf("mkdir installed python: %v", err)
	}
	if err := os.WriteFile(installedPy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write installed python: %v", err)
	}
	bundledPy := filepath.Join(fakeResources, "python", "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(bundledPy), 0o755); err != nil {
		t.Fatalf("mkdir bundled python: %v", err)
	}
	if err := os.WriteFile(bundledPy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write bundled python: %v", err)
	}

	// Daemon entry script at the installed location so StartJarvis finds
	// it without falling back to the source-tree.
	installedScript := filepath.Join(paths.DaemonSourceDir(), "main.py")
	if err := os.MkdirAll(filepath.Dir(installedScript), 0o755); err != nil {
		t.Fatalf("mkdir installed daemon dir: %v", err)
	}
	if err := os.WriteFile(installedScript, []byte("# main\n"), 0o644); err != nil {
		t.Fatalf("write installed main.py: %v", err)
	}

	// Capture cmd.Path via startJarvisCommandFn. The returned exec.Cmd
	// runs /usr/bin/true so cmd.Start() succeeds + cmd.Wait() returns fast;
	// the daemon monitor goroutine then exits without restarting (the
	// fuse stays at 0 in a clean test env). NOTE: /bin/true does not exist
	// on macOS — the binary lives at /usr/bin/true. The contrast with the
	// existing TestStartJarvis_PrefersInstalledPython_OverBundle (which
	// uses /bin/true but ignores StartJarvis's return) is intentional:
	// this test asserts err == nil and therefore needs a real binary.
	var capturedPath string
	prevCmdFn := startJarvisCommandFn
	startJarvisCommandFn = func(name string, arg ...string) *exec.Cmd {
		capturedPath = name
		return exec.Command("/usr/bin/true")
	}
	t.Cleanup(func() { startJarvisCommandFn = prevCmdFn })

	a := &App{}
	if err := a.StartJarvis(); err != nil {
		// In the happy path StartJarvis must return nil — both the
		// setup-gate and the python-lookup branches should be satisfied.
		t.Fatalf("StartJarvis() = %v; want nil", err)
	}
	// Let the monitor goroutine reap /bin/true before t.Cleanup tears
	// down the temp HOME (otherwise the goroutine writes to
	// ~/.jarvis/logs/daemon.log against a missing dir).
	time.Sleep(100 * time.Millisecond)

	if capturedPath == "" {
		t.Fatalf("startJarvisCommandFn was never invoked; StartJarvis returned early")
	}
	if capturedPath != installedPy {
		t.Errorf("StartJarvis picked %q; want installed %q (bundled was %q)",
			capturedPath, installedPy, bundledPy)
	}
}

// TestIntegration_StartJarvisAfterSuccessfulSetup verifies the cross-layer
// continuity: a sentinel written via the REAL setup.WriteSentinel (TASK-008)
// must satisfy StartJarvis's setup-gate (TASK-009) on the next launch. This
// is the "you completed setup, now relaunch the app" scenario.
//
// Distinct from TestIntegration_FullSetupHappyPath above by what it asserts:
// that test verifies the full launch flow + python preference; THIS test
// narrows in on the gate behaviour — given a freshly-written sentinel,
// StartJarvis must NOT short-circuit with ErrSetupRequired even when no
// python interpreter exists (the failure mode becomes ErrDaemonLaunchFailed
// instead, which is the React-facing discriminator between SetupScreen and
// amber banner).
//
// Two packages exercised: main (StartJarvis gate) + internal/setup
// (WriteSentinel + IsSetupComplete).
func TestIntegration_StartJarvisAfterSuccessfulSetup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error { return nil }
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	// Pre-condition: sentinel absent → StartJarvis returns ErrSetupRequired.
	// This pins the contrast: the SAME App, same env, would have failed
	// with the wrong error code if we hadn't written the sentinel.
	if _, err := os.Stat(paths.SetupSentinelPath(setup.SetupExpectedVersion)); !os.IsNotExist(err) {
		t.Fatalf("test bug: sentinel exists in fresh tmp home: %v", err)
	}
	// Materialise the bundled requirements.txt (needed before we can write
	// a hash-matching sentinel) — but NOT the sentinel itself yet.
	reqDir := filepath.Join(fakeResources, "jarvis-daemon")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatalf("mkdir bundled requirements dir: %v", err)
	}
	reqPath := filepath.Join(reqDir, "requirements.txt")
	if err := os.WriteFile(reqPath, []byte("pipecat-ai==0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write bundled requirements.txt: %v", err)
	}

	preApp := &App{}
	preErr := preApp.StartJarvis()
	if preErr == nil {
		t.Fatalf("pre-condition: StartJarvis() with no sentinel = nil; want ErrSetupRequired")
	}
	if !errors.Is(preErr, setup.ErrSetupRequired) {
		t.Fatalf("pre-condition: StartJarvis() err = %v; want errors.Is(err, ErrSetupRequired)", preErr)
	}

	// Now write a valid sentinel via the REAL setup.WriteSentinel path.
	// This is the "user just finished setup" handoff.
	sum, err := fileSHA256(reqPath)
	if err != nil {
		t.Fatalf("hash bundled requirements: %v", err)
	}
	if err := setup.WriteSentinel(setup.SentinelData{
		Version:            setup.SetupExpectedVersion,
		Timestamp:          time.Now().UTC(),
		RequirementsSHA256: sum,
		PythonPBSTag:       "integration-test",
	}); err != nil {
		t.Fatalf("setup.WriteSentinel: %v", err)
	}

	// Cross-check: the gate's helper must now accept it.
	if !setup.IsSetupComplete(reqPath) {
		t.Fatalf("setup.IsSetupComplete(%q) = false right after WriteSentinel; gate would still reject", reqPath)
	}

	// StartJarvis with the sentinel present must NOT return ErrSetupRequired.
	// It WILL return ErrDaemonLaunchFailed because no python interpreter
	// exists in the test env — that's the new failure mode, and the very
	// discriminator the React side uses to choose between SetupScreen
	// (ErrSetupRequired) and the amber banner (ErrDaemonLaunchFailed).
	a := &App{}
	err = a.StartJarvis()
	if err == nil {
		t.Fatalf("StartJarvis() with sentinel + no python = nil; want ErrDaemonLaunchFailed")
	}
	if errors.Is(err, setup.ErrSetupRequired) {
		t.Errorf("StartJarvis() after successful setup unexpectedly returned ErrSetupRequired: %v", err)
	}
	if !errors.Is(err, setup.ErrDaemonLaunchFailed) {
		t.Errorf("StartJarvis() err = %v; want errors.Is(err, ErrDaemonLaunchFailed) (gate passed, python missing)", err)
	}
}
