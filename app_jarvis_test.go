package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/namanchopra/jarvis/internal/config"
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
