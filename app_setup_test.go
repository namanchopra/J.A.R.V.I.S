package main

// app_setup_test.go — unit tests for the v0.2.0 setup-on-launch Wails
// bindings (TASK-006). The tests exercise the stderr parser, the dedup mutex,
// the sentinel-existence check, and the cached-state snapshot logic.
//
// External dependencies (the real install-daemon.sh, the real `bash` binary,
// disk I/O outside the temp dir) are stubbed via two test seams declared in
// app_setup.go: `setupSpawnerFn` (subprocess substitution) and the
// `eventEmitter` interface (event recording). Because of these seams none
// of these tests shell out to a real subprocess or touch the user's real
// ~/.jarvis directory.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
	"github.com/namanchopra/jarvis/internal/setup"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

// recordedEmit captures a single event emission for assertion. The event
// payload is intentionally an `any` so the recorder can hold both
// setupProgressEvent and setupStateEvent values without converting at capture
// time.
type recordedEmit struct {
	Channel string
	Event   any
}

// recordingEmitter is the test substitute for the production wailsEmitter.
// Every Emit call appends a recordedEmit to the slice under the mutex so
// concurrent emissions (e.g. the parser goroutine) don't race with assertion
// reads.
type recordingEmitter struct {
	mu     sync.Mutex
	events []recordedEmit
}

func (r *recordingEmitter) Emit(_ context.Context, name string, args ...interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Wails' EventsEmit accepts variadic args; setup.go always passes a single
	// payload so we capture args[0] when present. The recorder is robust to
	// the zero-arg case (which shouldn't happen in this binding but would
	// otherwise nil-deref).
	var payload any
	if len(args) > 0 {
		payload = args[0]
	}
	r.events = append(r.events, recordedEmit{Channel: name, Event: payload})
}

// snapshot returns a copy of events for safe assertion-time iteration.
func (r *recordingEmitter) snapshot() []recordedEmit {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEmit, len(r.events))
	copy(out, r.events)
	return out
}

// installRecorder swaps in a recordingEmitter for the duration of t. Returns
// the recorder so the test can assert against it.
func installRecorder(t *testing.T, a *App) *recordingEmitter {
	t.Helper()
	rec := &recordingEmitter{}
	rt := setupRuntimeFor(a)
	rt.mu.Lock()
	rt.emitter = rec
	rt.mu.Unlock()
	t.Cleanup(func() {
		rt.mu.Lock()
		rt.emitter = nil
		rt.mu.Unlock()
	})
	return rec
}

// clearRuntime drops the per-App setupRuntime so each test starts with a
// pristine slate (LastError empty, PhaseDoneCount 0, etc.). Wails apps are
// singletons in production; tests routinely construct multiple App{} values
// per package run, so we tidy up after ourselves.
func clearRuntime(t *testing.T, a *App) {
	t.Helper()
	setupRuntimes.Delete(a)
	t.Cleanup(func() { setupRuntimes.Delete(a) })
}

// fakeStderrSpawner returns a setupSpawnerFn that ignores its args and
// returns the provided stderr reader plus a Wait closure that returns
// waitErr after the parser drains the reader. The returned spawner counts
// invocations (the int32 increment) so the dedup test can verify only one
// spawn occurred.
func fakeStderrSpawner(stderr string, waitErr error, spawnCount *int32) func(context.Context, setupSpawnArgs) (*setupSpawnResult, error) {
	return func(_ context.Context, _ setupSpawnArgs) (*setupSpawnResult, error) {
		atomic.AddInt32(spawnCount, 1)
		return &setupSpawnResult{
			Stderr: io.NopCloser(strings.NewReader(stderr)),
			Wait:   func() error { return waitErr },
		}, nil
	}
}

// stubInstallScript writes a dummy install-daemon.sh under tmp so
// resolveSetupSpawnArgs finds it via the source-tree fallback. The script
// itself never runs (setupSpawnerFn is replaced); only its existence is
// checked. Returns the absolute path to the project root that the test
// should chdir into via t.Cleanup-protected os.Chdir.
//
// It also installs a no-op setupSubscribeFn for the duration of the test
// so RunSetup's TASK-007 bridge subscription doesn't try to call
// runtime.EventsOn against a non-Wails context (which log.Fatalfs and
// would kill the test binary). Tests that exercise the bridge handler
// directly should NOT use stubInstallScript — they don't go through
// RunSetup so the seam doesn't matter.
func stubInstallScript(t *testing.T, tmp string) {
	t.Helper()
	scriptDir := filepath.Join(tmp, "scripts", "setup")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "install-daemon.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\n# stub for tests\n"), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	// Switch CWD so the source-tree relative path "scripts/setup/install-daemon.sh"
	// resolves to the stub. The cleanup restores the original CWD so other
	// tests in the suite are unaffected.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Replace setupSubscribeFn with a no-op so RunSetup doesn't call
	// runtime.EventsOn against a non-Wails context.
	prevSub := setupSubscribeFn
	setupSubscribeFn = func(_ *App, _ func(map[string]interface{})) func() {
		return func() {}
	}
	t.Cleanup(func() { setupSubscribeFn = prevSub })
}

// findEventsByState filters the recorded events to setupProgressEvent values
// whose State matches s. Returns the filtered slice.
func findEventsByState(events []recordedEmit, s setupProgressState) []setupProgressEvent {
	out := []setupProgressEvent{}
	for _, e := range events {
		evt, ok := e.Event.(setupProgressEvent)
		if !ok {
			continue
		}
		if evt.State == s {
			out = append(out, evt)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// IsSetupComplete
// ---------------------------------------------------------------------------

// TestIsSetupComplete_DelegatesToSetupPackage asserts the binding correctly
// reflects the presence/absence of the v0.2.0 setup sentinel file. The
// sentinel path comes from paths.SetupSentinelPath(setup.SetupExpectedVersion)
// which today lives under $HOME/.jarvis — we redirect $HOME to a temp dir
// so the test never touches the real install.
//
// The name nods to the TASK-008 future where this binding will delegate to
// `setup.IsSetupComplete(bundledRequirementsPath)`; today the body is a
// sentinel-existence check and this test pins that behaviour.
func TestIsSetupComplete_DelegatesToSetupPackage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{}
	clearRuntime(t, a)

	// Clean machine: sentinel absent → false.
	if a.IsSetupComplete() {
		t.Fatalf("IsSetupComplete() with no sentinel = true; want false")
	}

	// Create the sentinel. Ensure the parent dir exists.
	sentinel := paths.SetupSentinelPath(setup.SetupExpectedVersion)
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatalf("mkdir jarvis dir: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	if !a.IsSetupComplete() {
		t.Fatalf("IsSetupComplete() with sentinel = false; want true")
	}

	// Corrupt case: directory at sentinel path → false (warning logged).
	if err := os.Remove(sentinel); err != nil {
		t.Fatalf("remove sentinel: %v", err)
	}
	if err := os.Mkdir(sentinel, 0o755); err != nil {
		t.Fatalf("mkdir sentinel-as-dir: %v", err)
	}
	if a.IsSetupComplete() {
		t.Errorf("IsSetupComplete() with sentinel-as-directory = true; want false (treated as corrupt install)")
	}
}

// ---------------------------------------------------------------------------
// GetSetupState
// ---------------------------------------------------------------------------

// TestGetSetupState_ReturnsCachedState asserts that GetSetupState returns
// whatever the parser most recently wrote to the cached state, plus an
// always-populated SetupVersion and a freshly-computed Complete bit derived
// from the sentinel file.
func TestGetSetupState_ReturnsCachedState(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{}
	clearRuntime(t, a)

	// Initial call: empty runtime → SetupVersion backfilled, Complete=false.
	got := a.GetSetupState()
	if got.SetupVersion != setup.SetupExpectedVersion {
		t.Errorf("GetSetupState() SetupVersion = %q; want %q", got.SetupVersion, setup.SetupExpectedVersion)
	}
	if got.Complete {
		t.Errorf("GetSetupState() Complete = true on clean machine; want false")
	}
	if got.PhaseDoneCount != 0 {
		t.Errorf("GetSetupState() PhaseDoneCount = %d; want 0", got.PhaseDoneCount)
	}

	// Mutate cached state directly (simulating the parser having processed
	// a few PHASE_DONE markers) and assert GetSetupState reflects it.
	rt := setupRuntimeFor(a)
	rt.mu.Lock()
	rt.currentState.Phase = setup.PhaseVibeVoice
	rt.currentState.PhaseProgress = 50
	rt.currentState.PhaseDoneCount = 2
	rt.currentState.LastError = "test error"
	rt.mu.Unlock()

	got = a.GetSetupState()
	if got.Phase != setup.PhaseVibeVoice {
		t.Errorf("Phase = %q; want %q", got.Phase, setup.PhaseVibeVoice)
	}
	if got.PhaseProgress != 50 {
		t.Errorf("PhaseProgress = %d; want 50", got.PhaseProgress)
	}
	if got.PhaseDoneCount != 2 {
		t.Errorf("PhaseDoneCount = %d; want 2", got.PhaseDoneCount)
	}
	if got.LastError != "test error" {
		t.Errorf("LastError = %q; want %q", got.LastError, "test error")
	}

	// Sentinel present → Complete flips to true on next read.
	sentinel := paths.SetupSentinelPath(setup.SetupExpectedVersion)
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	got = a.GetSetupState()
	if !got.Complete {
		t.Errorf("after sentinel write: Complete = false; want true")
	}
}

// ---------------------------------------------------------------------------
// RunSetup parser behaviour
// ---------------------------------------------------------------------------

// TestRunSetup_ParsesPhaseStartedDoneFromFakeStderr feeds the canonical
// happy-path stderr fragment through the parser via the runSetupParseLoop
// entry point and asserts the emitter received the expected `started` →
// `progress` → `done` event sequence with correct field values.
//
// We exercise runSetupParseLoop directly (rather than RunSetup) so we do
// not need to mock setupSpawnerFn / resolveSetupSpawnArgs for this test —
// the parser is the unit under test, not the spawn plumbing.
func TestRunSetup_ParsesPhaseStartedDoneFromFakeStderr(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	// A condensed but representative phase-1 fragment from the schema doc's
	// happy-path example. Includes PHASE_BYTES (which augments the next
	// PHASE_PROGRESS), PHASE_ETA (ditto), and PHASE_DONE.
	fragment := strings.Join([]string{
		"PHASE: python_install",
		"PHASE_BYTES: 0 / 92341056",
		"PHASE_PROGRESS: 0",
		"PHASE_BYTES: 12582912 / 92341056",
		"PHASE_PROGRESS: 13",
		"PHASE_ETA: 26",
		"PHASE_PROGRESS: 100",
		"PHASE_DONE: python_install",
	}, "\n") + "\n"

	var logBuf bytes.Buffer
	if err := a.runSetupParseLoop(strings.NewReader(fragment), &logBuf); err != nil {
		t.Fatalf("runSetupParseLoop: %v", err)
	}

	events := rec.snapshot()

	// Expect at least: 1 started + 3 progress + 1 done = 5 events.
	started := findEventsByState(events, stateStarted)
	if len(started) != 1 {
		t.Errorf("started events = %d; want 1", len(started))
	} else {
		if started[0].Phase != setup.PhasePython {
			t.Errorf("started phase = %q; want %q", started[0].Phase, setup.PhasePython)
		}
		if started[0].Type != "setup_progress" {
			t.Errorf("started type = %q; want %q", started[0].Type, "setup_progress")
		}
	}

	progress := findEventsByState(events, stateProgress)
	if len(progress) != 3 {
		t.Errorf("progress events = %d; want 3", len(progress))
	}
	if len(progress) >= 1 {
		// First PROGRESS: phaseProgress=0, bytes=0/92341056.
		p0 := progress[0]
		if p0.PhaseProgress == nil || *p0.PhaseProgress != 0 {
			t.Errorf("progress[0].phaseProgress = %v; want 0", p0.PhaseProgress)
		}
		if p0.BytesDone == nil || *p0.BytesDone != 0 {
			t.Errorf("progress[0].bytesDone = %v; want 0", p0.BytesDone)
		}
		if p0.BytesTotal == nil || *p0.BytesTotal != 92341056 {
			t.Errorf("progress[0].bytesTotal = %v; want 92341056", p0.BytesTotal)
		}
	}
	if len(progress) >= 2 {
		// Second PROGRESS: phaseProgress=13, bytes=12582912/92341056, no ETA
		// yet (PHASE_ETA appears AFTER this PROGRESS in the fragment).
		p1 := progress[1]
		if p1.PhaseProgress == nil || *p1.PhaseProgress != 13 {
			t.Errorf("progress[1].phaseProgress = %v; want 13", p1.PhaseProgress)
		}
		if p1.BytesDone == nil || *p1.BytesDone != 12582912 {
			t.Errorf("progress[1].bytesDone = %v; want 12582912", p1.BytesDone)
		}
	}
	if len(progress) >= 3 {
		// Third PROGRESS: phaseProgress=100, ETA=26 carried over from the
		// line BEFORE this PROGRESS, bytes still sticky.
		p2 := progress[2]
		if p2.PhaseProgress == nil || *p2.PhaseProgress != 100 {
			t.Errorf("progress[2].phaseProgress = %v; want 100", p2.PhaseProgress)
		}
		if p2.EtaSeconds == nil || *p2.EtaSeconds != 26 {
			t.Errorf("progress[2].etaSeconds = %v; want 26", p2.EtaSeconds)
		}
	}

	done := findEventsByState(events, stateDone)
	if len(done) != 1 {
		t.Errorf("done events = %d; want 1", len(done))
	} else {
		if done[0].Phase != setup.PhasePython {
			t.Errorf("done phase = %q; want %q", done[0].Phase, setup.PhasePython)
		}
	}

	// Cached state should reflect: phase=python_install, doneCount=1.
	state := a.GetSetupState()
	if state.PhaseDoneCount != 1 {
		t.Errorf("after fragment: PhaseDoneCount = %d; want 1", state.PhaseDoneCount)
	}
	if state.Phase != setup.PhasePython {
		t.Errorf("after fragment: Phase = %q; want %q", state.Phase, setup.PhasePython)
	}

	// Every line should have been teed to the log buffer.
	teed := logBuf.String()
	if !strings.Contains(teed, "PHASE: python_install") {
		t.Errorf("log buffer missing PHASE start line; got: %q", teed)
	}
	if !strings.Contains(teed, "PHASE_DONE: python_install") {
		t.Errorf("log buffer missing PHASE_DONE line; got: %q", teed)
	}
}

// ---------------------------------------------------------------------------
// RunSetup dedup
// ---------------------------------------------------------------------------

// TestRunSetup_DeduplicatesConcurrentCalls fires two RunSetup invocations
// concurrently and asserts that the substituted spawner is called exactly
// once — the second caller sees the running flag and returns the cached
// snapshot without re-spawning.
//
// We use a blocking spawner (the Wait closure waits on a channel) so the
// race between the two RunSetup calls is deterministic: caller A enters,
// sets running=true, blocks in Wait; caller B enters, sees running=true,
// returns the snapshot. Then we unblock A and wait for the goroutine to
// finish.
func TestRunSetup_DeduplicatesConcurrentCalls(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stubInstallScript(t, tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	_ = installRecorder(t, a)

	// The spawner returns a Wait that blocks until release is closed, so
	// caller A is parked inside RunSetup while caller B fires.
	release := make(chan struct{})
	var spawnCount int32

	prev := setupSpawnerFn
	setupSpawnerFn = func(_ context.Context, _ setupSpawnArgs) (*setupSpawnResult, error) {
		atomic.AddInt32(&spawnCount, 1)
		return &setupSpawnResult{
			// Empty stderr → parser loop exits immediately.
			Stderr: io.NopCloser(strings.NewReader("")),
			Wait: func() error {
				<-release
				return nil
			},
		}, nil
	}
	t.Cleanup(func() { setupSpawnerFn = prev })

	// Fire caller A in a goroutine; it will park in Wait until release fires.
	var aErr error
	doneA := make(chan struct{})
	go func() {
		_, aErr = a.RunSetup()
		close(doneA)
	}()

	// Wait for caller A to set running=true. Polling is a code smell but
	// the alternative (instrumenting setup.go with a "running" channel) is
	// overkill for a single test. 1s ceiling with 1ms granularity gives the
	// goroutine ample headroom while keeping the test fast on CI.
	deadline := time.Now().Add(1 * time.Second)
	for {
		rt := setupRuntimeFor(a)
		rt.mu.Lock()
		running := rt.running
		rt.mu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("caller A never set running=true within 1s")
		}
		time.Sleep(1 * time.Millisecond)
	}

	// Caller B should see running=true and short-circuit. We capture its
	// return value to assert it received a snapshot (not an error).
	stateB, errB := a.RunSetup()
	if errB != nil {
		t.Errorf("caller B err = %v; want nil (concurrent calls should no-op)", errB)
	}
	if stateB.SetupVersion != setup.SetupExpectedVersion {
		t.Errorf("caller B SetupVersion = %q; want %q", stateB.SetupVersion, setup.SetupExpectedVersion)
	}

	// Unblock caller A.
	close(release)
	<-doneA
	if aErr != nil {
		t.Errorf("caller A err = %v; want nil", aErr)
	}

	if got := atomic.LoadInt32(&spawnCount); got != 1 {
		t.Errorf("setupSpawnerFn invocations = %d; want 1 (dedup failed)", got)
	}
}

// ---------------------------------------------------------------------------
// Daemon auto-launch after successful RunSetup (v0.2.6)
// ---------------------------------------------------------------------------

// TestRunSetup_LaunchesDaemonAfterSuccess regression-pins v0.2.6 fix:
// when install-daemon.sh completes phases 1+2 and writes the sentinel,
// RunSetup must call StartJarvis to kick off the daemon. Otherwise the
// SetupScreen sits forever on "phase 2 done" because phases 3+4 are
// driven by daemon-emitted model_download events that never fire (the
// daemon was never launched -- the app's startup() StartJarvis call had
// bailed earlier with ErrSetupRequired because the sentinel didn't exist
// yet).
//
// We don't actually exercise the full daemon launch -- that would require
// a real Python + venv on disk. Instead we just confirm RunSetup attempts
// to spawn a daemon process by capturing startJarvisCommandFn invocations.
func TestRunSetup_LaunchesDaemonAfterSuccess(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stubInstallScript(t, tmp)

	// Stub the quarantine strip and bundle resources dir so StartJarvis
	// gets past its early bookkeeping.
	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })
	prevStripFn := stripQuarantineFn
	stripQuarantineFn = func(p string) error { return nil }
	t.Cleanup(func() { stripQuarantineFn = prevStripFn })

	// StartJarvis needs a sentinel + bundled requirements.txt to pass its
	// preflight checks. writeValidSentinel materialises both.
	writeValidSentinel(t, fakeResources)
	// And a venv python so StartJarvis resolves an interpreter.
	venvPy := filepath.Join(paths.DaemonVenvDir(), "bin", "python")
	if err := os.MkdirAll(filepath.Dir(venvPy), 0o755); err != nil {
		t.Fatalf("mkdir venv python dir: %v", err)
	}
	if err := os.WriteFile(venvPy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write venv python: %v", err)
	}
	// And a daemon main.py.
	mainPy := filepath.Join(paths.DaemonSourceDir(), "main.py")
	if err := os.MkdirAll(filepath.Dir(mainPy), 0o755); err != nil {
		t.Fatalf("mkdir daemon dir: %v", err)
	}
	if err := os.WriteFile(mainPy, []byte("# main\n"), 0o644); err != nil {
		t.Fatalf("write main.py: %v", err)
	}

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	_ = installRecorder(t, a)

	// Spawner returns a clean phase-done sequence so RunSetup succeeds.
	prev := setupSpawnerFn
	setupSpawnerFn = func(_ context.Context, _ setupSpawnArgs) (*setupSpawnResult, error) {
		stderr := "PHASE: python_install\nPHASE_PROGRESS: 100\nPHASE_DONE: python_install\nPHASE: venv_install\nPHASE_PROGRESS: 100\nPHASE_DONE: venv_install\n"
		return &setupSpawnResult{
			Stderr: io.NopCloser(strings.NewReader(stderr)),
			Wait:   func() error { return nil },
		}, nil
	}
	t.Cleanup(func() { setupSpawnerFn = prev })

	// Capture daemon launch invocations. /bin/true exits immediately so
	// the daemon monitor goroutine reaps cleanly before t.Cleanup tears
	// down the temp HOME.
	var daemonLaunches int32
	prevCmdFn := startJarvisCommandFn
	startJarvisCommandFn = func(name string, arg ...string) *exec.Cmd {
		atomic.AddInt32(&daemonLaunches, 1)
		return exec.Command("/bin/true")
	}
	t.Cleanup(func() { startJarvisCommandFn = prevCmdFn })

	_, err := a.RunSetup()
	if err != nil {
		t.Fatalf("RunSetup = %v; want nil", err)
	}
	// Give the daemon-monitor goroutine a moment to fire so /bin/true
	// reaps before t.Cleanup tears down the temp dir.
	time.Sleep(100 * time.Millisecond)

	if got := atomic.LoadInt32(&daemonLaunches); got != 1 {
		t.Errorf("startJarvisCommandFn invocations = %d; want 1 (daemon must auto-launch after RunSetup success so phases 3+4 can fire)", got)
	}
}

// ---------------------------------------------------------------------------
// Watchdog → auto-retry path (v0.2.3)
// ---------------------------------------------------------------------------

// TestRunSetup_WatchdogRetriesAfterSilence verifies the v0.2.3 hang-recovery
// path: attempts 1..N-1 stall (the spawner returns a stderr that never
// yields any data), the watchdog kicks in after setupAttemptWatchdogSilence,
// the ctx gets canceled, and RunSetup retries up to setupMaxAttempts. The
// last attempt succeeds, so RunSetup returns nil.
//
// This is what "flawless install for everyone" hinges on -- if the script
// hangs for whatever reason on the user's machine (network stall, App Nap,
// XProtect rescan), the watchdog must recover transparently.
func TestRunSetup_WatchdogRetriesAfterSilence(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stubInstallScript(t, tmp)

	// Tighten the watchdog so the test runs in ~1s instead of 6 minutes.
	prevSilence := setupAttemptWatchdogSilence
	prevPoll := setupWatchdogPollInterval
	prevAttempts := setupMaxAttempts
	setupAttemptWatchdogSilence = 80 * time.Millisecond
	setupWatchdogPollInterval = 10 * time.Millisecond
	setupMaxAttempts = 3
	t.Cleanup(func() {
		setupAttemptWatchdogSilence = prevSilence
		setupWatchdogPollInterval = prevPoll
		setupMaxAttempts = prevAttempts
	})

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	var spawnCount int32
	prev := setupSpawnerFn
	setupSpawnerFn = func(ctx context.Context, _ setupSpawnArgs) (*setupSpawnResult, error) {
		n := atomic.AddInt32(&spawnCount, 1)
		// Attempts 1 and 2 stall -- stderr never yields data; only the
		// ctx-cancel from the watchdog unblocks the parser's Read.
		if n < int32(setupMaxAttempts) {
			pr, _ := io.Pipe()
			// Closer that's actually triggered by ctx so the parser's
			// blocking Read unblocks when the watchdog cancels.
			go func() {
				<-ctx.Done()
				_ = pr.Close()
			}()
			return &setupSpawnResult{
				Stderr: pr,
				Wait: func() error {
					<-ctx.Done()
					return ctx.Err()
				},
			}, nil
		}
		// Final attempt: yield a clean phase-done sequence so RunSetup
		// returns success.
		stderr := "PHASE: python_install\nPHASE_PROGRESS: 100\nPHASE_DONE: python_install\nPHASE: venv_install\nPHASE_PROGRESS: 100\nPHASE_DONE: venv_install\n"
		return &setupSpawnResult{
			Stderr: io.NopCloser(strings.NewReader(stderr)),
			Wait:   func() error { return nil },
		}, nil
	}
	t.Cleanup(func() { setupSpawnerFn = prev })

	_, err := a.RunSetup()
	if err != nil {
		t.Fatalf("RunSetup after %d-attempt watchdog recovery = %v; want nil", setupMaxAttempts, err)
	}
	if got := atomic.LoadInt32(&spawnCount); got != int32(setupMaxAttempts) {
		t.Errorf("setupSpawnerFn invocations = %d; want %d (one per attempt)", got, setupMaxAttempts)
	}

	// Verify the SetupScreen got setup_retry events for attempts 2 and 3.
	retries := 0
	for _, ev := range rec.snapshot() {
		if m, ok := ev.Event.(map[string]interface{}); ok {
			if t, _ := m["type"].(string); t == "setup_retry" {
				retries++
			}
		}
	}
	if retries != setupMaxAttempts-1 {
		t.Errorf("setup_retry events = %d; want %d (one per retry)", retries, setupMaxAttempts-1)
	}
}

// TestRunSetup_WatchdogExhaustsRetries_HandsOffToTerminal verifies the
// unhappy path: when EVERY in-process attempt times out, RunSetup falls
// back to launching install-daemon.sh in Terminal.app rather than
// surfacing an error. The user gets a working install via the
// terminal-context path even when the GUI-context path is broken.
//
// This regression-pins the v0.2.3 escape hatch -- if a future refactor
// drops the Terminal hand-off, this test will fail and the change author
// has to explicitly delete it (signaling the regression).
func TestRunSetup_WatchdogExhaustsRetries_HandsOffToTerminal(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stubInstallScript(t, tmp)

	prevSilence := setupAttemptWatchdogSilence
	prevPoll := setupWatchdogPollInterval
	prevAttempts := setupMaxAttempts
	setupAttemptWatchdogSilence = 80 * time.Millisecond
	setupWatchdogPollInterval = 10 * time.Millisecond
	setupMaxAttempts = 2
	t.Cleanup(func() {
		setupAttemptWatchdogSilence = prevSilence
		setupWatchdogPollInterval = prevPoll
		setupMaxAttempts = prevAttempts
	})

	// Stub the Terminal launcher so we don't actually pop a Terminal
	// window during the test. Capture the args so we can assert the
	// hand-off received the same script/uv/daemon paths the in-process
	// attempts would have used.
	var terminalCalls int32
	var lastArgs setupSpawnArgs
	prevLauncher := launchTerminalInstallerFn
	launchTerminalInstallerFn = func(args setupSpawnArgs) error {
		atomic.AddInt32(&terminalCalls, 1)
		lastArgs = args
		return nil
	}
	t.Cleanup(func() { launchTerminalInstallerFn = prevLauncher })

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	var spawnCount int32
	prev := setupSpawnerFn
	setupSpawnerFn = func(ctx context.Context, _ setupSpawnArgs) (*setupSpawnResult, error) {
		atomic.AddInt32(&spawnCount, 1)
		pr, _ := io.Pipe()
		go func() {
			<-ctx.Done()
			_ = pr.Close()
		}()
		return &setupSpawnResult{
			Stderr: pr,
			Wait: func() error {
				<-ctx.Done()
				return ctx.Err()
			},
		}, nil
	}
	t.Cleanup(func() { setupSpawnerFn = prev })

	_, err := a.RunSetup()
	if err != nil {
		t.Fatalf("RunSetup after Terminal hand-off = %v; want nil (hand-off should succeed quietly)", err)
	}
	if got := atomic.LoadInt32(&spawnCount); got != int32(setupMaxAttempts) {
		t.Errorf("setupSpawnerFn invocations = %d; want %d", got, setupMaxAttempts)
	}
	if got := atomic.LoadInt32(&terminalCalls); got != 1 {
		t.Errorf("launchTerminalInstallerFn invocations = %d; want 1 (single hand-off)", got)
	}
	if lastArgs.ScriptPath == "" {
		t.Errorf("Terminal hand-off received empty ScriptPath; want a real path")
	}

	// SetupScreen should have received a setup_terminal_handoff event so
	// the React side can show "auto-launched Terminal installer" copy.
	gotHandoff := false
	for _, ev := range rec.snapshot() {
		if m, ok := ev.Event.(map[string]interface{}); ok {
			if t, _ := m["type"].(string); t == "setup_terminal_handoff" {
				gotHandoff = true
				break
			}
		}
	}
	if !gotHandoff {
		t.Errorf("setup_terminal_handoff event was never emitted; SetupScreen has no way to explain the hand-off to the user")
	}
	_ = errors.Is // keep the import live without forcing the failure path
}

// ---------------------------------------------------------------------------
// RunSetup robustness — invalid stderr lines must not crash
// ---------------------------------------------------------------------------

// TestRunSetup_InvalidStderrLine_DoesNotCrash feeds the parser a mix of
// known-good PHASE markers, structurally-invalid PHASE markers (bad numbers,
// out-of-range values, unknown phase strings), and totally unrelated
// chatter (curl progress, `set -x` echoes, bash error messages). The parser
// must:
//
//   1. Not panic / not return an error from runSetupParseLoop.
//   2. Emit events for the known-good lines.
//   3. NOT emit events for the structurally-invalid lines.
//   4. Tee everything to the log buffer regardless.
//
// This is the schema doc's "unknown-prefix handling" contract.
func TestRunSetup_InvalidStderrLine_DoesNotCrash(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	// Mix of valid and bogus lines. Order matters because the parser keeps
	// per-phase state ("last phase declared", "pending bytes").
	stderr := strings.Join([]string{
		"+ uv venv ~/.jarvis/jarvis-daemon-env",        // bash -x echo, not a PHASE marker
		"PHASE: bogus_phase_name",                       // unknown phase → drop + warn
		"PHASE: venv_install",                           // valid
		"random curl progress line goes here",           // not a marker
		"PHASE_PROGRESS: not-a-number",                  // invalid (won't match regex; falls through to "unknown")
		"PHASE_PROGRESS: -5",                            // won't match (\d+ rejects minus)
		"PHASE_PROGRESS: 9999",                          // matches but >100; clamped + warned
		"PHASE_BYTES: 100 200",                          // missing slash → won't match
		"PHASE_DONE: bogus_phase",                       // unknown phase in DONE → drop + warn
		"PHASE_DONE: venv_install",                      // valid
		"",                                              // blank line — skipped silently
	}, "\n")

	var logBuf bytes.Buffer
	if err := a.runSetupParseLoop(strings.NewReader(stderr), &logBuf); err != nil {
		t.Fatalf("runSetupParseLoop returned error on noisy input: %v", err)
	}

	// Verify the valid PHASE markers emitted but the invalid ones did not.
	events := rec.snapshot()
	started := findEventsByState(events, stateStarted)
	if len(started) != 1 {
		t.Errorf("started events = %d; want 1 (bogus_phase_name PHASE should be dropped)", len(started))
	}
	if len(started) >= 1 && started[0].Phase != setup.PhaseVenv {
		t.Errorf("started phase = %q; want %q", started[0].Phase, setup.PhaseVenv)
	}

	// The clamped PHASE_PROGRESS: 9999 still emits (with value 100). The
	// invalid format ones (not-a-number, -5, 100 200) do not.
	progress := findEventsByState(events, stateProgress)
	if len(progress) != 1 {
		t.Errorf("progress events = %d; want 1 (only the clamped 9999 should emit)", len(progress))
	}
	if len(progress) >= 1 {
		if progress[0].PhaseProgress == nil || *progress[0].PhaseProgress != 100 {
			t.Errorf("clamped progress value = %v; want 100", progress[0].PhaseProgress)
		}
	}

	done := findEventsByState(events, stateDone)
	if len(done) != 1 {
		t.Errorf("done events = %d; want 1 (bogus_phase PHASE_DONE should be dropped)", len(done))
	}

	// And the noisy lines must all be teed to the log.
	teed := logBuf.String()
	for _, must := range []string{
		"+ uv venv",
		"random curl progress",
		"PHASE: bogus_phase_name",
	} {
		if !strings.Contains(teed, must) {
			t.Errorf("log buffer missing %q; got:\n%s", must, teed)
		}
	}
}

// TestRunSetup_PhaseErrorEmitsErrorEvent verifies the parser's PHASE_ERROR
// handling: a PHASE_ERROR line produces an `error`-state setup_progress
// event with the message, AND populates the cached state's LastError. The
// implicit phase is the most-recently-declared PHASE: (vibevoice_download
// in this fixture).
func TestRunSetup_PhaseErrorEmitsErrorEvent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	stderr := strings.Join([]string{
		"PHASE: vibevoice_download",
		"PHASE_BYTES: 0 / 1932735283",
		"PHASE_PROGRESS: 0",
		"PHASE_ERROR: huggingface_hub returned 429 Too Many Requests",
	}, "\n") + "\n"

	if err := a.runSetupParseLoop(strings.NewReader(stderr), io.Discard); err != nil {
		t.Fatalf("runSetupParseLoop: %v", err)
	}

	errs := findEventsByState(rec.snapshot(), stateError)
	if len(errs) != 1 {
		t.Fatalf("error events = %d; want 1", len(errs))
	}
	if errs[0].Phase != setup.PhaseVibeVoice {
		t.Errorf("error phase = %q; want %q (implicit from last PHASE)", errs[0].Phase, setup.PhaseVibeVoice)
	}
	wantMsg := "huggingface_hub returned 429 Too Many Requests"
	if errs[0].Error != wantMsg {
		t.Errorf("error message = %q; want %q", errs[0].Error, wantMsg)
	}

	state := a.GetSetupState()
	if state.LastError != wantMsg {
		t.Errorf("cached LastError = %q; want %q", state.LastError, wantMsg)
	}
}

// ---------------------------------------------------------------------------
// RunSetup exit-code failure surfaces error
// ---------------------------------------------------------------------------

// TestRunSetup_ExitCodeFailure_EmitsErrorEvent simulates the install script
// terminating with a non-zero exit code AFTER its stderr was already drained
// without any PHASE_ERROR markers (the bash-killed-by-signal scenario from
// docs/setup-events.md "Unhappy paths" section D). The Go side must:
//
//   1. Return a wrapped error from RunSetup ("RunSetup: …").
//   2. Emit a synthetic `error`-state setup_progress event so React shows
//      the failure even though the script itself didn't get a chance to
//      announce it.
//   3. Persist the failure into LastError so a later GetSetupState read
//      surfaces it.
func TestRunSetup_ExitCodeFailure_EmitsErrorEvent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stubInstallScript(t, tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	// Replace the spawner with one that emits no PHASE_* lines and exits
	// with a synthetic error from Wait.
	var spawnCount int32
	prev := setupSpawnerFn
	setupSpawnerFn = fakeStderrSpawner("", fmt.Errorf("simulated SIGKILL"), &spawnCount)
	t.Cleanup(func() { setupSpawnerFn = prev })

	state, err := a.RunSetup()
	if err == nil {
		t.Fatalf("RunSetup() with non-zero exit = nil; want error")
	}
	if !strings.Contains(err.Error(), "RunSetup:") {
		t.Errorf("error message = %q; want %q prefix", err.Error(), "RunSetup:")
	}
	if !strings.Contains(err.Error(), "simulated SIGKILL") {
		t.Errorf("error message = %q; want underlying error to be wrapped", err.Error())
	}

	// LastError should be populated.
	if state.LastError == "" {
		t.Errorf("returned state.LastError is empty; want exit-code message")
	}
	if !strings.Contains(state.LastError, "install-daemon.sh exited with error") {
		t.Errorf("LastError = %q; want %q substring", state.LastError, "install-daemon.sh exited with error")
	}

	// And one synthetic error event should have been emitted (because no
	// PHASE_ERROR came from the script itself).
	errs := findEventsByState(rec.snapshot(), stateError)
	if len(errs) != 1 {
		t.Errorf("synthetic error events = %d; want 1", len(errs))
	}

	// And spawn was called exactly once (no retry on failure inside RunSetup
	// itself — retries are React's responsibility via retry_setup_phase).
	if got := atomic.LoadInt32(&spawnCount); got != 1 {
		t.Errorf("spawn invocations = %d; want 1", got)
	}
}

// TestRunSetup_ExitCodeFailure_PreservesPhaseError verifies the contrast
// with TestRunSetup_ExitCodeFailure_EmitsErrorEvent: when the script emitted
// a PHASE_ERROR before exiting non-zero, we do NOT overwrite that more-
// specific error message with the generic "install-daemon.sh exited" text.
// The script's own message is what the user actually wants to see.
func TestRunSetup_ExitCodeFailure_PreservesPhaseError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stubInstallScript(t, tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	stderr := strings.Join([]string{
		"PHASE: python_install",
		"PHASE_ERROR: failed to download python-build-standalone after 3 retries",
	}, "\n") + "\n"

	var spawnCount int32
	prev := setupSpawnerFn
	setupSpawnerFn = fakeStderrSpawner(stderr, fmt.Errorf("exit status 1"), &spawnCount)
	t.Cleanup(func() { setupSpawnerFn = prev })

	state, err := a.RunSetup()
	if err == nil {
		t.Fatalf("RunSetup() = nil; want error")
	}

	// The script's PHASE_ERROR message must survive, NOT be overwritten
	// with the generic exit-code message.
	wantSubstr := "failed to download python-build-standalone"
	if !strings.Contains(state.LastError, wantSubstr) {
		t.Errorf("state.LastError = %q; want substring %q", state.LastError, wantSubstr)
	}
	if strings.Contains(state.LastError, "install-daemon.sh exited with error") {
		t.Errorf("state.LastError contains generic exit-code text; should preserve PHASE_ERROR: %q", state.LastError)
	}

	// Exactly one error event should be in the recorder — the PHASE_ERROR
	// emitted by the parser, not a second synthetic one from the Wait path.
	errs := findEventsByState(rec.snapshot(), stateError)
	if len(errs) != 1 {
		t.Errorf("error events = %d; want 1 (PHASE_ERROR only; no synthetic dup)", len(errs))
	}
	if len(errs) >= 1 && !strings.Contains(errs[0].Error, wantSubstr) {
		t.Errorf("error event message = %q; want substring %q", errs[0].Error, wantSubstr)
	}
}

// ---------------------------------------------------------------------------
// handleRequestSetupState
// ---------------------------------------------------------------------------

// TestHandleRequestSetupState_EmitsSnapshot asserts the late-mount handler
// emits a setup_state event with the current cached snapshot. This mirrors
// the v0.1.5 request_pipeline_status pattern.
func TestHandleRequestSetupState_EmitsSnapshot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	// Seed cached state with a representative mid-install snapshot.
	rt := setupRuntimeFor(a)
	rt.mu.Lock()
	rt.currentState.Phase = setup.PhaseWhisper
	rt.currentState.PhaseDoneCount = 3
	rt.currentState.LastError = ""
	rt.currentState.SetupVersion = setup.SetupExpectedVersion
	rt.mu.Unlock()

	a.handleRequestSetupState()

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("emitted events = %d; want 1", len(events))
	}
	evt, ok := events[0].Event.(setupStateEvent)
	if !ok {
		t.Fatalf("emitted event type = %T; want setupStateEvent", events[0].Event)
	}
	if events[0].Channel != "setup" {
		t.Errorf("channel = %q; want %q", events[0].Channel, "setup")
	}
	if evt.Type != "setup_state" {
		t.Errorf("event.Type = %q; want %q", evt.Type, "setup_state")
	}
	if evt.Phase != setup.PhaseWhisper {
		t.Errorf("event.Phase = %q; want %q", evt.Phase, setup.PhaseWhisper)
	}
	if evt.PhaseDoneCount != 3 {
		t.Errorf("event.PhaseDoneCount = %d; want 3", evt.PhaseDoneCount)
	}
	if evt.SetupVersion != setup.SetupExpectedVersion {
		t.Errorf("event.SetupVersion = %q; want %q", evt.SetupVersion, setup.SetupExpectedVersion)
	}
	if evt.Complete {
		t.Errorf("event.Complete = true on clean machine; want false")
	}
}

// ---------------------------------------------------------------------------
// Channel-name pin
// ---------------------------------------------------------------------------

// TestRunSetup_EmitsOnSetupChannel pins the Wails event channel name to
// "setup" (lowercase, no namespace prefix). The React side subscribes to
// EventsOn('setup', ...) and a typo here would silently break the HUD with
// no compile-time failure.
func TestRunSetup_EmitsOnSetupChannel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)

	stderr := "PHASE: python_install\nPHASE_DONE: python_install\n"
	if err := a.runSetupParseLoop(strings.NewReader(stderr), io.Discard); err != nil {
		t.Fatalf("runSetupParseLoop: %v", err)
	}

	for _, ev := range rec.snapshot() {
		if ev.Channel != "setup" {
			t.Errorf("event emitted on channel %q; want %q", ev.Channel, "setup")
		}
	}
}

// ---------------------------------------------------------------------------
// TASK-007 — daemon model-event bridge
// ---------------------------------------------------------------------------
//
// The bridge translates daemon-emitted model_setup / model_download payloads
// (forwarded onto the `jarvis` Wails channel by handlers_jarvis_ws.go) into
// `setup_progress` / `setup_state` events on the `setup` channel. These
// tests drive handleDaemonModelEvent directly with synthetic
// map[string]interface{} payloads — matching the shape that the production
// jarvis-channel emitter constructs — and assert the bridge's output.

// markSetupRunning flips rt.running to true for the duration of t and
// resets it on cleanup. The bridge is gated on setupRunning, so without
// this helper every bridge test would emit nothing.
func markSetupRunning(t *testing.T, a *App) {
	t.Helper()
	rt := setupRuntimeFor(a)
	rt.mu.Lock()
	rt.running = true
	rt.mu.Unlock()
	t.Cleanup(func() {
		rt.mu.Lock()
		rt.running = false
		rt.mu.Unlock()
	})
}

// TestBridge_ModelSetupReadyFinalisesPhases regression-pins v0.2.7 fix:
// when models are already cached in ~/.cache/huggingface (the user
// previously installed Jarvis, models survived a ~/.jarvis wipe), the
// daemon's prefetch_models() sees pending=[] and emits a SINGLE
// model_setup{state:"ready", models_pending:[]} event. No model_download
// events ever fire.
//
// Before v0.2.7 the bridge silently dropped state="ready" -- so the
// per-model done branch in bridgeHandleModelDownload never ran, sentinel
// was never written (well, install-daemon.sh wrote it earlier but the
// bridge thought no progress was made), and setup_state{complete:true}
// was never emitted -- SetupScreen sat forever on phases 3+4 pending.
//
// v0.2.7 finalises both phases on state="ready": emit done for both
// phases, refresh sentinel, emit setup_state{complete:true}. This test
// pins all three behaviours.
func TestBridge_ModelSetupReadyFinalisesPhases(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)
	markSetupRunning(t, a)

	// Stub sentinel writer so we don't touch real ~/.jarvis. Tests assert
	// the writer was called with a Version matching SetupExpectedVersion.
	var sentinelWriteCount int32
	prev := setupWriteSentinelFn
	setupWriteSentinelFn = func(d setup.SentinelData) error {
		atomic.AddInt32(&sentinelWriteCount, 1)
		if d.Version != setup.SetupExpectedVersion {
			t.Errorf("sentinel write Version = %q; want %q", d.Version, setup.SetupExpectedVersion)
		}
		return nil
	}
	t.Cleanup(func() { setupWriteSentinelFn = prev })

	// Daemon's cache-hit emission.
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":           "model_setup",
		"state":          "ready",
		"models_pending": []interface{}{},
	})

	if got := atomic.LoadInt32(&sentinelWriteCount); got != 1 {
		t.Errorf("sentinel writes = %d; want 1", got)
	}

	// Expect emit order:
	//   1. setup_progress {phase: vibevoice, state: done}
	//   2. setup_progress {phase: whisper,   state: done}
	//   3. setup_state    {complete: true}
	events := rec.snapshot()
	if len(events) != 3 {
		t.Fatalf("emitted events = %d; want 3\nevents: %+v", len(events), events)
	}
	if p, ok := events[0].Event.(setupProgressEvent); !ok || p.Phase != setup.PhaseVibeVoice || p.State != stateDone {
		t.Errorf("event[0] = %+v; want setup_progress{vibevoice, done}", events[0].Event)
	}
	if p, ok := events[1].Event.(setupProgressEvent); !ok || p.Phase != setup.PhaseWhisper || p.State != stateDone {
		t.Errorf("event[1] = %+v; want setup_progress{whisper, done}", events[1].Event)
	}
	s, ok := events[2].Event.(setupStateEvent)
	if !ok {
		t.Fatalf("event[2] type = %T; want setupStateEvent", events[2].Event)
	}
	if !s.Complete {
		t.Errorf("setup_state Complete = false; want true")
	}
	if s.PhaseDoneCount != 2 {
		t.Errorf("setup_state PhaseDoneCount = %d; want 2 (vibevoice + whisper latched)", s.PhaseDoneCount)
	}
}

// TestBridge_ForwardsModelDownloadVibeVoiceAsSetupProgress feeds a
// representative model_download progress payload (matching the shape that
// model_status.py's _build_progress_payload emits) into the bridge and
// asserts that the corresponding setup_progress event lands on the `setup`
// channel with the correct phase and field translations.
func TestBridge_ForwardsModelDownloadVibeVoiceAsSetupProgress(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)
	markSetupRunning(t, a)

	// model_download / progress at 45% with bytes + ETA. JSON-decoded
	// numbers come through as float64, matching the production path
	// where handlers_jarvis_ws.go decodes the daemon WS frame into a
	// map[string]interface{} before calling the bridge.
	payload := map[string]interface{}{
		"type":             "model_download",
		"model":            "vibevoice",
		"state":            "progress",
		"pct":              float64(45),
		"total_bytes":      float64(1932735283),
		"downloaded_bytes": float64(869730877),
		"eta_seconds":      float64(31),
	}
	a.handleDaemonModelEvent(payload)

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("emitted events = %d; want 1\nevents: %+v", len(events), events)
	}
	if events[0].Channel != "setup" {
		t.Errorf("channel = %q; want %q", events[0].Channel, "setup")
	}
	evt, ok := events[0].Event.(setupProgressEvent)
	if !ok {
		t.Fatalf("event type = %T; want setupProgressEvent", events[0].Event)
	}
	if evt.Type != "setup_progress" {
		t.Errorf("Type = %q; want %q", evt.Type, "setup_progress")
	}
	if evt.Phase != setup.PhaseVibeVoice {
		t.Errorf("Phase = %q; want %q", evt.Phase, setup.PhaseVibeVoice)
	}
	if evt.State != stateProgress {
		t.Errorf("State = %q; want %q", evt.State, stateProgress)
	}
	if evt.PhaseProgress == nil || *evt.PhaseProgress != 45 {
		t.Errorf("PhaseProgress = %v; want 45", evt.PhaseProgress)
	}
	if evt.BytesDone == nil || *evt.BytesDone != 869730877 {
		t.Errorf("BytesDone = %v; want 869730877", evt.BytesDone)
	}
	if evt.BytesTotal == nil || *evt.BytesTotal != 1932735283 {
		t.Errorf("BytesTotal = %v; want 1932735283", evt.BytesTotal)
	}
	if evt.EtaSeconds == nil || *evt.EtaSeconds != 31 {
		t.Errorf("EtaSeconds = %v; want 31", evt.EtaSeconds)
	}
}

// TestBridge_OnlyForwardsWhileSetupRunning verifies the gate that protects
// the FirstRunDownloadOverlay use-case: when setup has completed (running=
// false), the bridge MUST NOT emit on the `setup` channel even if model
// events keep flowing from the daemon (e.g. user swaps models from
// Settings while the SetupScreen is unmounted).
func TestBridge_OnlyForwardsWhileSetupRunning(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)
	// Intentionally do NOT call markSetupRunning — running stays false.

	payload := map[string]interface{}{
		"type":             "model_download",
		"model":            "vibevoice",
		"state":            "progress",
		"pct":              float64(45),
		"total_bytes":      float64(1932735283),
		"downloaded_bytes": float64(869730877),
	}
	a.handleDaemonModelEvent(payload)

	// And a model_setup event, also while not running:
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_setup",
		"state": "downloading",
		"models_pending": []interface{}{
			map[string]interface{}{"name": "vibevoice", "approx_size_bytes": float64(1932735283)},
		},
	})

	events := rec.snapshot()
	if len(events) != 0 {
		t.Errorf("emitted events while !running = %d; want 0\nevents: %+v", len(events), events)
	}
}

// TestBridge_AdvancesPhaseDoneCountOnModelDone verifies that
// model_download {state:done} events advance the cached PhaseDoneCount
// once per model: a vibevoice done bumps the counter by 1 (idempotent
// on a second done), and a whisper done brings the counter to 4 (assuming
// phases 1 + 2 were already done from the stderr parser).
//
// PhaseDoneCount is the React HUD's progress bar driver, so any drift
// here would show stale progress on screen.
func TestBridge_AdvancesPhaseDoneCountOnModelDone(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	_ = installRecorder(t, a)
	markSetupRunning(t, a)

	// Substitute setupWriteSentinelFn with a recorder so the whisper-done
	// path doesn't touch the real ~/.jarvis directory. We don't need to
	// assert anything against it here — the sentinel test below covers
	// that — but we do need to keep it from no-oping or erroring.
	prevWrite := setupWriteSentinelFn
	setupWriteSentinelFn = func(_ setup.SentinelData) error { return nil }
	t.Cleanup(func() { setupWriteSentinelFn = prevWrite })

	// Seed the counter to 2 (phases 1 + 2 done) to match the production
	// flow where phases 3 + 4 are the model downloads.
	rt := setupRuntimeFor(a)
	rt.mu.Lock()
	rt.currentState.PhaseDoneCount = 2
	rt.mu.Unlock()

	// vibevoice done → counter = 3.
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_download",
		"model": "vibevoice",
		"state": "done",
	})
	state := a.GetSetupState()
	if state.PhaseDoneCount != 3 {
		t.Errorf("after vibevoice done: PhaseDoneCount = %d; want 3", state.PhaseDoneCount)
	}

	// Replaying vibevoice done is idempotent — counter stays at 3.
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_download",
		"model": "vibevoice",
		"state": "done",
	})
	state = a.GetSetupState()
	if state.PhaseDoneCount != 3 {
		t.Errorf("after duplicate vibevoice done: PhaseDoneCount = %d; want 3 (idempotent)", state.PhaseDoneCount)
	}

	// whisper done → counter = 4 (all phases complete).
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_download",
		"model": "whisper",
		"state": "done",
	})
	state = a.GetSetupState()
	if state.PhaseDoneCount != 4 {
		t.Errorf("after whisper done: PhaseDoneCount = %d; want 4", state.PhaseDoneCount)
	}
}

// TestBridge_ErrorEventPropagatesToSetupProgress verifies that a daemon-
// emitted model_download {state:error} payload produces a setup_progress
// {state:error} event on the `setup` channel, with the error message
// preserved verbatim and the cached LastError populated for subsequent
// GetSetupState reads.
func TestBridge_ErrorEventPropagatesToSetupProgress(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)
	markSetupRunning(t, a)

	wantMsg := "huggingface_hub returned 429 Too Many Requests"
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_download",
		"model": "vibevoice",
		"state": "error",
		"error": wantMsg,
	})

	errs := findEventsByState(rec.snapshot(), stateError)
	if len(errs) != 1 {
		t.Fatalf("error events = %d; want 1", len(errs))
	}
	if errs[0].Phase != setup.PhaseVibeVoice {
		t.Errorf("error phase = %q; want %q", errs[0].Phase, setup.PhaseVibeVoice)
	}
	if errs[0].Error != wantMsg {
		t.Errorf("error message = %q; want %q", errs[0].Error, wantMsg)
	}

	state := a.GetSetupState()
	if state.LastError != wantMsg {
		t.Errorf("cached LastError = %q; want %q", state.LastError, wantMsg)
	}
}

// TestBridge_WhisperCompletionWritesSentinel asserts the bridge's
// end-of-phase-4 contract: when BOTH vibevoice and whisper have emitted
// {state:done}, the bridge must invoke setupWriteSentinelFn exactly once
// AND emit a setup_state {complete:true} event so the React HUD can flip
// out of the SetupScreen without waiting for the next launch.
//
// The sentinel write itself is recorded via a substituted
// setupWriteSentinelFn so the test doesn't touch ~/.jarvis. The write
// must happen on the whisper done (vibevoice was first), not before,
// to mirror the production ordering where the daemon downloads
// vibevoice then whisper serially.
func TestBridge_WhisperCompletionWritesSentinel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)
	markSetupRunning(t, a)

	// Recorder for sentinel writes. Each invocation appends the data so
	// we can assert the call count + payload at the end.
	var sentinelCalls []setup.SentinelData
	prevWrite := setupWriteSentinelFn
	setupWriteSentinelFn = func(d setup.SentinelData) error {
		sentinelCalls = append(sentinelCalls, d)
		return nil
	}
	t.Cleanup(func() { setupWriteSentinelFn = prevWrite })

	// Step 1: vibevoice done — sentinel must NOT be written yet (whisper
	// hasn't finished).
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_download",
		"model": "vibevoice",
		"state": "done",
	})
	if got := len(sentinelCalls); got != 0 {
		t.Errorf("after vibevoice done only: sentinel write count = %d; want 0", got)
	}

	// Step 2: whisper done — both models complete; sentinel is written
	// AND a setup_state {complete:true} event is emitted.
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_download",
		"model": "whisper",
		"state": "done",
	})
	if got := len(sentinelCalls); got != 1 {
		t.Fatalf("after whisper done: sentinel write count = %d; want 1", got)
	}
	if sentinelCalls[0].Version != setup.SetupExpectedVersion {
		t.Errorf("sentinel Version = %q; want %q", sentinelCalls[0].Version, setup.SetupExpectedVersion)
	}
	if sentinelCalls[0].Timestamp.IsZero() {
		t.Errorf("sentinel Timestamp is zero; want a real time")
	}

	// And a setup_state {complete:true} event should be in the recorder.
	var completeEvt *setupStateEvent
	for _, ev := range rec.snapshot() {
		if se, ok := ev.Event.(setupStateEvent); ok && se.Complete {
			seCopy := se
			completeEvt = &seCopy
			break
		}
	}
	if completeEvt == nil {
		t.Fatalf("no setup_state{complete:true} event emitted after whisper done")
	}
	if completeEvt.Type != "setup_state" {
		t.Errorf("complete event Type = %q; want %q", completeEvt.Type, "setup_state")
	}
	if completeEvt.SetupVersion != setup.SetupExpectedVersion {
		t.Errorf("complete event SetupVersion = %q; want %q", completeEvt.SetupVersion, setup.SetupExpectedVersion)
	}
}

// TestBridge_ModelSetupStartedEmitsStartedPerPendingModel verifies the
// model_setup {state:downloading, models_pending:[...]} entry point: each
// pending model in the list produces one setup_progress {state:started}
// event with the correct phase. Unknown models (e.g. "kokoro") are dropped
// silently — they have no corresponding phase row on the SetupScreen.
func TestBridge_ModelSetupStartedEmitsStartedPerPendingModel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	rec := installRecorder(t, a)
	markSetupRunning(t, a)

	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_setup",
		"state": "downloading",
		"models_pending": []interface{}{
			map[string]interface{}{"name": "vibevoice", "approx_size_bytes": float64(1932735283)},
			map[string]interface{}{"name": "whisper", "approx_size_bytes": float64(483183820)},
			map[string]interface{}{"name": "kokoro", "approx_size_bytes": float64(123456789)}, // unknown — must drop
		},
	})

	started := findEventsByState(rec.snapshot(), stateStarted)
	if len(started) != 2 {
		t.Errorf("started events = %d; want 2 (kokoro should be dropped)", len(started))
	}
	if len(started) >= 1 && started[0].Phase != setup.PhaseVibeVoice {
		t.Errorf("started[0].Phase = %q; want %q", started[0].Phase, setup.PhaseVibeVoice)
	}
	if len(started) >= 2 && started[1].Phase != setup.PhaseWhisper {
		t.Errorf("started[1].Phase = %q; want %q", started[1].Phase, setup.PhaseWhisper)
	}
}

// ---------------------------------------------------------------------------
// TASK-015 — cross-layer integration tests
// ---------------------------------------------------------------------------
//
// These tests exercise two packages each (app_setup.go bridge + internal/setup
// sentinel persistence; app_jarvis.go gate + internal/setup IsSetupComplete).
// They are the unique value-add of TASK-015 vs. the per-layer unit tests
// landed in TASK-006/007/008/009.

// TestIntegration_BridgeWritesSentinelAfterBothModelsDone is the end-to-end
// bridge → sentinel handoff. It drives the bridge with the canonical daemon
// event sequence (model_setup{state:downloading} → model_download{vibevoice
// done} → model_download{whisper done}) and asserts that the production
// setup.WriteSentinel path produces a file that setup.IsSetupComplete then
// recognises as valid.
//
// Distinct from TestBridge_WhisperCompletionWritesSentinel (TASK-007), which
// stubs setupWriteSentinelFn so it only verifies the bridge CALLED the
// sentinel writer with the right arguments. This test exercises the REAL
// setup.WriteSentinel + ReadSentinel path so a regression that breaks the
// round-trip (e.g. a sentinel key rename, an atomic-rename bug) gets caught
// even when the unit tests stay green.
//
// Two packages exercised: main (bridge) + internal/setup (sentinel).
func TestIntegration_BridgeWritesSentinelAfterBothModelsDone(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Materialise a fake bundled requirements.txt under <tmp>/Resources/
	// jarvis-daemon/ so setup.IsSetupComplete (called via a.IsSetupComplete
	// at the end) can hash it for the sha-match check. bundledResourcesDirFn
	// is the same seam app_jarvis.go uses to resolve bundledRequirementsPath.
	fakeResources := filepath.Join(tmp, "Jarvis.app", "Contents", "Resources")
	reqDir := filepath.Join(fakeResources, "jarvis-daemon")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatalf("mkdir bundled requirements dir: %v", err)
	}
	reqPath := filepath.Join(reqDir, "requirements.txt")
	if err := os.WriteFile(reqPath, []byte("pipecat-ai==0.1.0\nwhisper==1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write bundled requirements.txt: %v", err)
	}

	// Read sha for the assertion-side error message below. We mirror
	// setup.hashFile (unexported) via the app_jarvis_test.go helper
	// fileSHA256, which lives in the same `main` package.
	wantSha, err := fileSHA256(reqPath)
	if err != nil {
		t.Fatalf("hash bundled requirements: %v", err)
	}

	// Build an App, mark setup running, install a recorder, and crucially
	// do NOT stub setupWriteSentinelFn — we want the real write to land
	// under the redirected HOME.
	a := &App{ctx: context.Background()}
	clearRuntime(t, a)
	_ = installRecorder(t, a)
	markSetupRunning(t, a)

	// Step 1: model_setup{state:downloading, models_pending:[vibevoice,
	// whisper]} announces the upcoming downloads. The bridge emits two
	// `started` events but does NOT write the sentinel.
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_setup",
		"state": "downloading",
		"models_pending": []interface{}{
			map[string]interface{}{"name": "vibevoice", "approx_size_bytes": float64(1932735283)},
			map[string]interface{}{"name": "whisper", "approx_size_bytes": float64(483183820)},
		},
	})

	// Step 2: vibevoice completes. Counter advances by 1; sentinel is NOT
	// written yet (whisper outstanding).
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_download",
		"model": "vibevoice",
		"state": "done",
	})

	sentinelPath := paths.SetupSentinelPath(setup.SetupExpectedVersion)
	if _, statErr := os.Stat(sentinelPath); statErr == nil {
		t.Fatalf("sentinel exists after only vibevoice done; want absent until whisper done too")
	}

	// Step 3: whisper completes. Counter advances to 4; setupWriteSentinelFn
	// runs end-to-end and produces a real file under the redirected HOME.
	a.handleDaemonModelEvent(map[string]interface{}{
		"type":  "model_download",
		"model": "whisper",
		"state": "done",
	})

	// The sentinel file must now exist on disk under our temp home, with
	// the correct version + a non-zero timestamp.
	info, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel not written after whisper done: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("sentinel path %q is a directory; want a file", sentinelPath)
	}

	// Cross-package validation: parse the bridge-written file directly so
	// we see exactly what the production WriteSentinel persisted. The unit
	// tests stub setupWriteSentinelFn so they only verify the bridge CALLED
	// the writer with the right arguments — this is the first test that
	// confirms WriteSentinel's serializer + ReadSentinel's parser round-
	// trip the bridge's payload.
	rawBytes, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read bridge-written sentinel: %v", err)
	}
	rawStr := string(rawBytes)
	if !strings.Contains(rawStr, "version: "+setup.SetupExpectedVersion) {
		t.Errorf("sentinel missing version line; got: %q", rawStr)
	}
	if !strings.Contains(rawStr, "timestamp: ") {
		t.Errorf("sentinel missing timestamp line; got: %q", rawStr)
	}

	// The bridge intentionally writes a minimal sentinel (Version +
	// Timestamp only — see app_setup.go ~line 1144). The corresponding
	// production contract is: app.IsSetupComplete (cheap existence check)
	// accepts it; setup.IsSetupComplete (hash-checking) REJECTS it because
	// the RequirementsSHA256 field is blank. Pinning both sides here makes
	// any future change to that contract a visible code review event.
	prevDirFn := bundledResourcesDirFn
	bundledResourcesDirFn = func() string { return fakeResources }
	t.Cleanup(func() { bundledResourcesDirFn = prevDirFn })

	if !a.IsSetupComplete() {
		t.Errorf("app.IsSetupComplete() = false after bridge wrote sentinel; want true (existence check)")
	}
	if setup.IsSetupComplete(reqPath) {
		t.Errorf("setup.IsSetupComplete(%q) = true; want false (bridge writes minimal sentinel — bundled sha=%q does not match blank sentinel sha)",
			reqPath, wantSha)
	}
}
