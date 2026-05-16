package main

// app_setup.go implements the v0.2.0 setup-on-launch Wails bindings (TASK-006).
//
// The three exported methods (IsSetupComplete, RunSetup, GetSetupState) front
// the install flow that runs before the daemon can start. The contract that
// React + bash + the daemon bridge all read from lives in
// docs/setup-events.md — this file is the Go producer end of that contract.
//
// Boundaries (deliberate, see plan task descriptions):
//   - This file does NOT spawn the daemon, talk to the daemon, or modify the
//     existing app_jarvis.go StartJarvis logic — that's TASK-009.
//   - This file does NOT write the setup-complete sentinel file or verify a
//     requirements-sha; setup.IsSetupComplete (TASK-008) owns that. The
//     binding below delegates to that package-level helper once it lands.
//   - TASK-007 now lives here too: a daemon-event subscription bridges
//     model_setup / model_download events (already forwarded to the
//     `jarvis` Wails channel by internal/api/handlers_jarvis_ws.go) into
//     the same `setup` channel + setupParser state machine, but ONLY
//     while setupRunning == true. After setup completes the subscription
//     is torn down and model events flow through to the FirstRunDownloadOverlay
//     on the `jarvis` channel as before.
//   - This file does NOT implement OpenSetupLog (TASK-016).
//
// State location note: ideally the runtime state below (setupMu, setupRunning,
// setupCurrentState, setupEmitter) would be plain fields on App. To avoid
// touching app.go while parallel agents (TASK-004 / TASK-008 / TASK-010) are
// editing this same checkout, we instead keep the state in a per-App
// registry keyed by *App identity. Functionally identical; localised to this
// file. A follow-up refactor can hoist these into App once the wave is in.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/namanchopra/jarvis/internal/paths"
	"github.com/namanchopra/jarvis/internal/setup"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ---------------------------------------------------------------------------
// Event types
// ---------------------------------------------------------------------------
//
// These mirror the canonical TypeScript shapes declared in
// docs/setup-events.md ("Go types (canonical)" section). They would
// idiomatically live in internal/setup/setup.go alongside SetupState, but
// keeping them here for TASK-006 lets us land the bindings without
// modifying the foundation package — which other v0.2.0 wave-2 agents
// (TASK-008 in particular) may already be editing. A follow-up refactor
// can hoist them into internal/setup once the wave is in.

// setupProgressState is the inner discriminator (`state` field) on a
// setupProgressEvent. The four values match the schema doc's TS enum
// SetupProgressState.
type setupProgressState string

const (
	stateStarted  setupProgressState = "started"
	stateProgress setupProgressState = "progress"
	stateDone     setupProgressState = "done"
	stateError    setupProgressState = "error"
)

// setupProgressEvent mirrors the TS SetupProgressEvent shape. JSON tags
// MUST match the TS field names exactly — Wails serialises the struct via
// encoding/json and React reads it as `unknown` then type-guards.
//
// Pointer fields are used for phaseProgress / bytesDone / bytesTotal /
// etaSeconds so `omitempty` distinguishes "not set" from a real zero.
type setupProgressEvent struct {
	Type          string             `json:"type"` // always "setup_progress"
	Phase         setup.SetupPhase   `json:"phase"`
	State         setupProgressState `json:"state"`
	PhaseProgress *int               `json:"phaseProgress,omitempty"`
	BytesDone     *int64             `json:"bytesDone,omitempty"`
	BytesTotal    *int64             `json:"bytesTotal,omitempty"`
	EtaSeconds    *int               `json:"etaSeconds,omitempty"`
	Message       string             `json:"message,omitempty"`
	Error         string             `json:"error,omitempty"`
}

// setupStateEvent mirrors the TS SetupStateEvent shape — a snapshot of the
// cached state, emitted in response to request_setup_state or at the moment
// the sentinel is written.
type setupStateEvent struct {
	Type           string           `json:"type"` // always "setup_state"
	Complete       bool             `json:"complete"`
	Phase          setup.SetupPhase `json:"phase,omitempty"`
	PhaseDoneCount int              `json:"phaseDoneCount"`
	SetupVersion   string           `json:"setupVersion"`
	LastError      string           `json:"lastError,omitempty"`
}

// ---------------------------------------------------------------------------
// Per-App setup runtime (the would-be App-struct fields)
// ---------------------------------------------------------------------------

// setupRuntime carries the per-App mutable state that the setup bindings need.
// One instance is lazily created per *App via setupRuntimeFor.
type setupRuntime struct {
	mu           sync.Mutex
	running      bool
	currentState setup.SetupState
	emitter      eventEmitter // nil → wailsEmitter via setupRuntime.emit

	// bridgeCancel is the unsubscribe closure returned by the daemon-event
	// subscriber when RunSetup starts. RunSetup's defer invokes it on exit
	// so model events stop flowing to the `setup` channel once setup ends.
	// nil when no subscription is active (tests that don't exercise the
	// bridge, or production calls that fail before reaching the subscribe
	// step).
	bridgeCancel func()

	// vibeVoiceDone / whisperDone latch true the first time the bridge sees
	// a model_download {state:done} for the corresponding model. Used to
	// trigger the sentinel write at the moment both phase-3 and phase-4
	// downloads have completed (the daemon's own `model_setup state=ready`
	// event also signals "all done" but we can't rely on its ordering vs.
	// the per-model done events arriving). Reset when RunSetup begins a
	// fresh run so re-installs work.
	vibeVoiceDone bool
	whisperDone   bool
}

// setupRuntimes holds one setupRuntime per *App. App is a Wails-bound
// singleton in production but tests construct multiple App{} values, so we
// key by pointer identity.
var setupRuntimes sync.Map // map[*App]*setupRuntime

// setupRuntimeFor lazily returns the runtime for `a`, creating it on first
// use. Always non-nil.
func setupRuntimeFor(a *App) *setupRuntime {
	if v, ok := setupRuntimes.Load(a); ok {
		return v.(*setupRuntime)
	}
	rt := &setupRuntime{}
	// LoadOrStore guards against the unlikely race of two callers reaching
	// this branch concurrently for the same App.
	actual, _ := setupRuntimes.LoadOrStore(a, rt)
	return actual.(*setupRuntime)
}

// emit chooses the active emitter (test-injected or production) and forwards
// to it. Centralises the wails default-fallback so callers don't need to
// nil-check at every call site.
func (rt *setupRuntime) emit(ctx context.Context, name string, args ...interface{}) {
	rt.mu.Lock()
	em := rt.emitter
	rt.mu.Unlock()
	if em == nil {
		em = wailsEmitter{}
	}
	em.Emit(ctx, name, args...)
}

// ---------------------------------------------------------------------------
// Event emission abstraction (test seam)
// ---------------------------------------------------------------------------
//
// Production code wraps runtime.EventsEmit in a wailsEmitter so tests can
// inject a recorder instead. Without the seam tests would have to spin up a
// real Wails runtime context just to assert "did the parser emit the event we
// expected?".

type eventEmitter interface {
	Emit(ctx context.Context, name string, args ...interface{})
}

// wailsEmitter is the production emitter: it forwards directly to Wails'
// runtime.EventsEmit.
type wailsEmitter struct{}

func (wailsEmitter) Emit(ctx context.Context, name string, args ...interface{}) {
	runtime.EventsEmit(ctx, name, args...)
}

// ---------------------------------------------------------------------------
// Daemon-event subscription seam (TASK-007 bridge)
// ---------------------------------------------------------------------------
//
// In production we subscribe to the existing `jarvis` Wails channel via
// runtime.EventsOn. That channel is populated by handlers_jarvis_ws.go
// (every model_setup / model_download payload received from the daemon
// is re-emitted verbatim there since v0.1.1). The subscriber returns an
// unsubscribe closure that RunSetup invokes on exit.
//
// Tests cannot call runtime.EventsOn — it requires a fully-initialised
// Wails ctx that the test harness doesn't construct — so we tunnel the
// subscription behind a function variable. Tests replace the variable
// with a no-op that returns a cancel-tracking closure and instead drive
// events synthetically by calling handleDaemonModelEvent directly.

// setupSubscribeFn is the indirection point for daemon-event subscription.
// Production code (defaultSetupSubscriber) calls runtime.EventsOn against
// the App's Wails ctx. Tests substitute a no-op that doesn't subscribe.
//
// The handler signature accepts a `map[string]interface{}` because that's
// what the existing forwarder in handlers_jarvis_ws.go emits — every
// model_setup / model_download payload is decoded into a generic map
// before being passed to JarvisEventEmitter. Reusing the same shape keeps
// the bridge's parser identical between production and test paths.
//
// Returns an unsubscribe closure; the closure must be idempotent so a
// double-invoke (e.g. defer + explicit teardown on error) is safe.
var setupSubscribeFn = defaultSetupSubscriber

// defaultSetupSubscriber registers a handler on the production Wails
// `jarvis` channel and returns the cancel closure from runtime.EventsOn.
//
// The wrapped callback type-asserts each event payload to
// map[string]interface{} — the only shape handlers_jarvis_ws.go forwards
// for model events. Anything else (state_change, transcript, tool_call,
// etc.) is silently ignored: this bridge only cares about model phases.
func defaultSetupSubscriber(a *App, handler func(map[string]interface{})) (cancel func()) {
	// Default to a no-op cancel so the named return is always safe even
	// if EventsOn panics before returning a real unsubscribe closure.
	cancel = func() {}
	if a == nil || a.ctx == nil {
		// Defensive: a nil ctx means startup() hasn't run. There is
		// nothing to subscribe to; return a no-op cancel so callers
		// don't have to nil-check.
		return cancel
	}
	// runtime.EventsOn panics with "invalid context" when called against
	// a non-Wails context (e.g. context.Background() in tests). Recover
	// so the bridge degrades to a no-op subscription in those scenarios
	// rather than crashing RunSetup; tests that need to exercise the
	// bridge replace setupSubscribeFn entirely.
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("setup: runtime.EventsOn unavailable; bridge inactive", "recover", fmt.Sprintf("%v", r))
			cancel = func() {}
		}
	}()
	cancel = runtime.EventsOn(a.ctx, "jarvis", func(args ...interface{}) {
		if len(args) == 0 {
			return
		}
		payload, ok := args[0].(map[string]interface{})
		if !ok {
			// Non-map payloads come from other emitters on the jarvis
			// channel (e.g. JarvisEvent structs from app_jarvis.go).
			// They don't carry model_* events, so we drop silently.
			return
		}
		handler(payload)
	})
	return cancel
}

// setupWriteSentinelFn is the seam for the end-of-phase-4 sentinel write.
// Production code calls setup.WriteSentinel directly. Tests substitute a
// recorder so they can assert "did the bridge attempt a write?" without
// touching the real ~/.jarvis directory.
//
// Note: this seam is intentionally minimal — it does NOT cover the SHA-256
// hashing of the bundled requirements.txt. The bridge writes a sentinel
// with an empty requirements_sha256 because the production pipeline that
// writes the *authoritative* sentinel lives elsewhere (TASK-009's daemon
// boot path). The bridge's write is a best-effort "we got to whisper_done,
// nothing else exploded" marker that the React layer uses to flip out of
// the SetupScreen immediately, without waiting for the next launch's
// IsSetupComplete-via-ReadSentinel verification.
var setupWriteSentinelFn = setup.WriteSentinel

// nowFn is the wall-clock indirection point. The bridge stamps every
// sentinel write with nowFn().UTC(); tests that want to assert a stable
// timestamp can override it. Defaults to time.Now.
var nowFn = time.Now

// ---------------------------------------------------------------------------
// Path resolution indirection for the install script
// ---------------------------------------------------------------------------
//
// In production we resolve the script + uv binary + daemon source dir from
// the bundled .app Resources. In `wails dev` and `go test` runs there is no
// .app, so we fall back to source-tree relative paths. Tests substitute
// setupSpawnerFn to bypass spawning entirely and feed a canned stderr stream
// to the parser.

// setupSpawnArgs collects the inputs the install script needs. Resolved at the
// top of RunSetup and passed to setupSpawnerFn so tests can inspect them
// without re-resolving paths.
type setupSpawnArgs struct {
	ScriptPath       string // scripts/setup/install-daemon.sh
	UvPath           string // bundled uv binary
	DaemonSourcePath string // jarvis-daemon source tree to copy from
	LogPath          string // ~/.jarvis/logs/setup.log
}

// setupSpawnResult is what the spawner hands back to RunSetup. In production
// stderr is a pipe from a real subprocess and Wait blocks on cmd.Wait().
// In tests stderr is a *bytes.Reader wrapped in an io.NopCloser and Wait
// returns nil after the parser drains it.
type setupSpawnResult struct {
	Stderr io.ReadCloser
	Wait   func() error
}

// setupSpawnerFn is the indirection point. Production code (defaultSetupSpawner)
// exec's the install script; tests replace it with a function that returns a
// pre-built stderr reader and a synthetic Wait result.
//
// The signature returns ReadCloser so the parser goroutine can rely on a
// single Close() call regardless of whether stderr is a real OS pipe or a
// test-only in-memory buffer.
var setupSpawnerFn = defaultSetupSpawner

// defaultSetupSpawner is the production implementation. It exec's
// `bash <scriptPath> <uvPath> <daemonSourcePath>`, captures stderr via
// StderrPipe, and returns a wait closure that blocks on cmd.Wait().
//
// stdout is intentionally left default (inherits the parent's, i.e. the
// app's launcher stdout) — the schema doc declares stderr the canonical
// channel for PHASE_* markers, and the script's own chatty output on stdout
// is consumed by whatever is hosting the Wails process (typically /dev/null
// in production .app launches).
func defaultSetupSpawner(ctx context.Context, args setupSpawnArgs) (*setupSpawnResult, error) {
	cmd := exec.CommandContext(ctx, "bash", args.ScriptPath, args.UvPath, args.DaemonSourcePath)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("StderrPipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	return &setupSpawnResult{
		Stderr: stderr,
		Wait:   cmd.Wait,
	}, nil
}

// resolveSetupSpawnArgs picks the script + uv + daemon-source paths from
// either the .app bundle (production) or the source tree (dev / test). Errors
// only when the install script itself cannot be located. uv and daemon-source
// missing are deferred to the script's own checks (which emit PHASE_ERROR).
func resolveSetupSpawnArgs() (setupSpawnArgs, error) {
	// Script: prefer bundled <Resources>/scripts/setup/install-daemon.sh,
	// fall back to source tree.
	scriptPath := ""
	if res := paths.BundledResourcesDir(); res != "" {
		candidate := filepath.Join(res, "scripts", "setup", "install-daemon.sh")
		if _, err := os.Stat(candidate); err == nil {
			scriptPath = candidate
		}
	}
	if scriptPath == "" {
		// Source-tree fallback for `wails dev` runs. The CWD at dev-time is
		// the project root, so a relative path is sufficient. Tests do not
		// reach this branch — they substitute setupSpawnerFn entirely.
		candidates := []string{
			"scripts/setup/install-daemon.sh",
			"../scripts/setup/install-daemon.sh",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				scriptPath = c
				break
			}
		}
	}
	if scriptPath == "" {
		return setupSpawnArgs{}, fmt.Errorf("install-daemon.sh not found (tried bundled <Resources>/scripts/setup/install-daemon.sh and source-tree paths)")
	}

	// uv binary: bundled <Resources>/uv (the .app ships a pinned uv), or
	// whatever `uv` resolves to on $PATH for dev.
	uvPath := ""
	if res := paths.BundledResourcesDir(); res != "" {
		candidate := filepath.Join(res, "uv")
		if _, err := os.Stat(candidate); err == nil {
			uvPath = candidate
		}
	}
	if uvPath == "" {
		if found, err := exec.LookPath("uv"); err == nil {
			uvPath = found
		}
	}

	// Daemon source dir: bundled <Resources>/jarvis-daemon, or source tree.
	daemonSource := ""
	if res := paths.BundledResourcesDir(); res != "" {
		candidate := filepath.Join(res, "jarvis-daemon")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			daemonSource = candidate
		}
	}
	if daemonSource == "" {
		candidates := []string{
			"scripts/jarvis-daemon",
			"../scripts/jarvis-daemon",
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				daemonSource = c
				break
			}
		}
	}

	return setupSpawnArgs{
		ScriptPath:       scriptPath,
		UvPath:           uvPath,
		DaemonSourcePath: daemonSource,
		LogPath:          paths.SetupLogPath(),
	}, nil
}

// ---------------------------------------------------------------------------
// Stderr line parser
// ---------------------------------------------------------------------------
//
// The regexes below match the formats declared canonical in
// docs/setup-events.md ("install-daemon.sh stderr format" section). Any line
// that doesn't match a known prefix is teed to setup.log + slog.Warn'd, but
// does NOT emit an event — that's the schema doc's "unknown-prefix handling"
// contract.

var (
	rePhaseStart    = regexp.MustCompile(`^PHASE:\s+(\w+)\s*$`)
	rePhaseProgress = regexp.MustCompile(`^PHASE_PROGRESS:\s+(\d+)\s*$`)
	rePhaseBytes    = regexp.MustCompile(`^PHASE_BYTES:\s+(\d+)\s*/\s*(\d+)\s*$`)
	rePhaseETA      = regexp.MustCompile(`^PHASE_ETA:\s+(\d+)\s*$`)
	rePhaseDone     = regexp.MustCompile(`^PHASE_DONE:\s+(\w+)\s*$`)
	rePhaseError    = regexp.MustCompile(`^PHASE_ERROR:\s+(.+)$`)
)

// validPhase returns true iff s matches one of the four canonical phase
// strings. The schema doc declares any other value an error to be logged-and-
// dropped (NOT emitted).
func validPhase(s string) bool {
	switch setup.SetupPhase(s) {
	case setup.PhasePython, setup.PhaseVenv, setup.PhaseVibeVoice, setup.PhaseWhisper:
		return true
	}
	return false
}

// setupParser is the per-RunSetup parser state. It tracks the last phase
// declared via PHASE: (used as the implicit phase for PHASE_ERROR which
// doesn't repeat the phase name), and pending bytes/eta augmentations that
// attach to the next PHASE_PROGRESS emission.
//
// State is intentionally local to a single RunSetup call — concurrent runs
// are de-duplicated at the App level (setupRuntime.running), so we never
// have two parsers active at once.
type setupParser struct {
	lastPhase     setup.SetupPhase
	pendingBytesD *int64
	pendingBytesT *int64
	pendingETA    *int

	emit  func(setupProgressEvent)
	state *setup.SetupState // shared with rt.currentState; mutex held by caller
}

// handleLine routes a single trimmed stderr line. Returns true if the line
// was a recognised PHASE_* marker (whether or not it emitted an event);
// false otherwise so the caller can log-and-tee unrecognised lines.
func (p *setupParser) handleLine(line string) bool {
	if m := rePhaseStart.FindStringSubmatch(line); m != nil {
		ph := m[1]
		if !validPhase(ph) {
			slog.Warn("setup: unrecognised phase", "phase", ph)
			return true
		}
		p.lastPhase = setup.SetupPhase(ph)
		p.state.Phase = p.lastPhase
		p.state.PhaseProgress = 0
		p.state.LastError = ""
		// Reset pending bytes/eta on phase start — they belong to the
		// previous phase's tail.
		p.pendingBytesD, p.pendingBytesT, p.pendingETA = nil, nil, nil
		p.emit(setupProgressEvent{
			Type:  "setup_progress",
			Phase: p.lastPhase,
			State: stateStarted,
		})
		return true
	}

	if m := rePhaseBytes.FindStringSubmatch(line); m != nil {
		done, err1 := strconv.ParseInt(m[1], 10, 64)
		total, err2 := strconv.ParseInt(m[2], 10, 64)
		if err1 != nil || err2 != nil || total < 0 || done < 0 {
			slog.Warn("setup: invalid PHASE_BYTES", "line", line)
			return true
		}
		p.pendingBytesD = &done
		p.pendingBytesT = &total
		return true
	}

	if m := rePhaseETA.FindStringSubmatch(line); m != nil {
		eta, err := strconv.Atoi(m[1])
		if err != nil || eta < 0 {
			slog.Warn("setup: invalid PHASE_ETA", "line", line)
			return true
		}
		p.pendingETA = &eta
		return true
	}

	if m := rePhaseProgress.FindStringSubmatch(line); m != nil {
		v, err := strconv.Atoi(m[1])
		if err != nil {
			slog.Warn("setup: invalid PHASE_PROGRESS", "line", line)
			return true
		}
		// Clamp + warn per schema doc ("Values >100 or <0 are clamped at
		// the boundary AND logged at slog.Warn — the script is buggy if
		// this happens").
		if v < 0 {
			slog.Warn("setup: PHASE_PROGRESS < 0; clamped", "value", v)
			v = 0
		}
		if v > 100 {
			slog.Warn("setup: PHASE_PROGRESS > 100; clamped", "value", v)
			v = 100
		}
		if p.lastPhase == "" {
			// No PHASE: declared yet. Without a phase the React side would
			// reject the event (phase is required). Drop + warn.
			slog.Warn("setup: PHASE_PROGRESS before PHASE:", "value", v)
			return true
		}
		p.state.PhaseProgress = v
		vv := v
		evt := setupProgressEvent{
			Type:          "setup_progress",
			Phase:         p.lastPhase,
			State:         stateProgress,
			PhaseProgress: &vv,
		}
		// Attach pending bytes/eta. Per the schema doc, bytesDone and
		// bytesTotal are jointly optional — we only attach them as a pair.
		if p.pendingBytesD != nil && p.pendingBytesT != nil {
			bd := *p.pendingBytesD
			bt := *p.pendingBytesT
			evt.BytesDone = &bd
			evt.BytesTotal = &bt
		}
		if p.pendingETA != nil {
			e := *p.pendingETA
			evt.EtaSeconds = &e
		}
		// Clear ETA after emit (it's a momentary signal). Bytes stay
		// "sticky" so subsequent PROGRESS lines without a fresh PHASE_BYTES
		// still carry the most-recent byte counts to the UI.
		p.pendingETA = nil
		p.emit(evt)
		return true
	}

	if m := rePhaseDone.FindStringSubmatch(line); m != nil {
		ph := m[1]
		if !validPhase(ph) {
			slog.Warn("setup: unrecognised phase in PHASE_DONE", "phase", ph)
			return true
		}
		p.state.Phase = setup.SetupPhase(ph)
		p.state.PhaseProgress = 100
		p.state.PhaseDoneCount++
		// Reset pending bytes/eta on phase done.
		p.pendingBytesD, p.pendingBytesT, p.pendingETA = nil, nil, nil
		p.emit(setupProgressEvent{
			Type:  "setup_progress",
			Phase: setup.SetupPhase(ph),
			State: stateDone,
		})
		return true
	}

	if m := rePhaseError.FindStringSubmatch(line); m != nil {
		msg := strings.TrimSpace(m[1])
		// The schema doc says PHASE_ERROR's phase is implicit (the most-
		// recently-declared PHASE:). If no phase has been declared yet, we
		// still emit but with an empty phase string — the React type guard
		// will drop it, which is the correct behaviour (a truly orphan
		// error from the script is a script bug, not a user-actionable
		// problem).
		ph := p.lastPhase
		p.state.LastError = msg
		p.emit(setupProgressEvent{
			Type:  "setup_progress",
			Phase: ph,
			State: stateError,
			Error: msg,
		})
		return true
	}

	return false
}

// ---------------------------------------------------------------------------
// Wails bindings
// ---------------------------------------------------------------------------

// IsSetupComplete returns true iff the v0.2.0 setup sentinel exists at
// ~/.jarvis/.setup-version-0.2.0 AND its contents satisfy the validity check
// owned by internal/setup (TASK-008). Today the package-level helper is not
// yet merged, so this binding falls back to a minimal existence check on the
// sentinel path. Once TASK-008 lands, replace the body with a single
// `return setup.IsSetupComplete(bundledRequirementsPath)` call.
//
// The binding is intentionally cheap (no disk I/O beyond a single Stat) so
// App.tsx can call it on every render without measurable cost.
func (a *App) IsSetupComplete() bool {
	// TODO(TASK-008): swap this for `return setup.IsSetupComplete(...)` once
	// that helper is merged. Until then, an existence check matches the
	// MVP contract — the sentinel is only written at the END of phase 4 by
	// the orchestrator, so its presence is a strong signal that all four
	// phases completed at least once.
	sentinel := paths.SetupSentinelPath(setup.SetupExpectedVersion)
	info, err := os.Stat(sentinel)
	if err != nil {
		return false
	}
	if info.IsDir() {
		// A directory at the sentinel path is a corrupt install; treat as
		// "not complete" so the SetupScreen runs again.
		slog.Warn("setup: sentinel path is a directory", "path", sentinel)
		return false
	}
	return true
}

// RunSetup spawns scripts/setup/install-daemon.sh, parses its stderr line-by-
// line for PHASE_* markers, and emits one `setup_progress` event per
// recognised line on the Wails `setup` channel. Returns the final cached
// SetupState plus any error.
//
// Concurrency: a second call while a run is in flight returns the current
// cached state immediately without re-spawning (the dedup mutex flow
// declared in the schema doc).
//
// Behaviour summary:
//   - Stderr is teed to ~/.jarvis/logs/setup.log (every line, recognised or
//     not) so post-hoc debugging works without keeping a terminal open.
//   - Unrecognised stderr lines are slog.Warn'd but do not crash.
//   - install-daemon.sh exiting non-zero produces an `error`-state
//     setup_progress event (using the most-recent phase) AND a wrapped
//     error return value.
//   - The sentinel write at the end of phase 4 is NOT done here — TASK-008
//     owns it via setup.IsSetupComplete; once that lands the post-Wait
//     branch below should snapshot the final state and emit a
//     setup_state{complete:true} event.
func (a *App) RunSetup() (setup.SetupState, error) {
	rt := setupRuntimeFor(a)

	// De-dup: if a run is already in flight, return the snapshot we have.
	rt.mu.Lock()
	if rt.running {
		state := rt.currentState
		if state.SetupVersion == "" {
			state.SetupVersion = setup.SetupExpectedVersion
		}
		rt.mu.Unlock()
		slog.Info("RunSetup: already in flight; returning cached state")
		return state, nil
	}
	rt.running = true
	// Reset the per-run portion of cached state but keep the version stamp
	// so consumers always see it populated.
	rt.currentState = setup.SetupState{
		SetupVersion: setup.SetupExpectedVersion,
	}
	// Reset the per-run model-phase latches so a re-install starts clean.
	rt.vibeVoiceDone = false
	rt.whisperDone = false
	rt.mu.Unlock()

	// Always clear the running flag on exit AND tear down the daemon-event
	// subscription so model events post-setup stop being re-emitted on the
	// `setup` channel — they continue to flow on the `jarvis` channel to
	// whoever else is listening (the FirstRunDownloadOverlay).
	defer func() {
		rt.mu.Lock()
		rt.running = false
		cancel := rt.bridgeCancel
		rt.bridgeCancel = nil
		rt.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}()

	args, err := resolveSetupSpawnArgs()
	if err != nil {
		wrapped := fmt.Errorf("RunSetup: %w", err)
		a.setSetupError(wrapped.Error())
		a.emitErrorEvent(wrapped.Error())
		return a.snapshotSetupState(), wrapped
	}

	// Ensure the log directory exists; OpenFile will fail otherwise.
	if mkErr := os.MkdirAll(filepath.Dir(args.LogPath), 0o755); mkErr != nil {
		slog.Warn("RunSetup: could not create log dir", "path", filepath.Dir(args.LogPath), "err", mkErr)
	}
	// Truncate-on-open so the file always reflects only the current run —
	// same convention as jarvisLogWriter in app_jarvis.go.
	logFile, logErr := os.OpenFile(args.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if logErr != nil {
		slog.Warn("RunSetup: could not open setup.log; continuing without tee", "path", args.LogPath, "err", logErr)
		logFile = nil
	}
	defer func() {
		if logFile != nil {
			_ = logFile.Close()
		}
	}()

	ctx := a.ctx
	if ctx == nil {
		// Tests construct App{} directly without going through startup(),
		// so a.ctx may be nil. Fall back to Background so exec.CommandContext
		// + the parser goroutine still work.
		ctx = context.Background()
	}

	spawn, spawnErr := setupSpawnerFn(ctx, args)
	if spawnErr != nil {
		wrapped := fmt.Errorf("RunSetup: spawn: %w", spawnErr)
		a.setSetupError(wrapped.Error())
		a.emitErrorEvent(wrapped.Error())
		return a.snapshotSetupState(), wrapped
	}

	// Bridge: subscribe to the `jarvis` Wails channel so daemon-emitted
	// model_setup / model_download events for phases 3 and 4 get re-emitted
	// on the `setup` channel as setup_progress events. The subscription is
	// torn down in the deferred cleanup above so it only runs for the
	// duration of this RunSetup call. The handler itself is also gated on
	// rt.running, providing defence in depth against a late event arriving
	// after the unsubscribe (Wails dispatches synchronously today but the
	// guarantee isn't documented).
	bridgeCancel := setupSubscribeFn(a, a.handleDaemonModelEvent)
	rt.mu.Lock()
	rt.bridgeCancel = bridgeCancel
	rt.mu.Unlock()

	// Drain stderr in this goroutine (no need to background it — RunSetup
	// is itself running on a goroutine spawned by the Wails frontend call).
	if drainErr := a.runSetupParseLoop(spawn.Stderr, logFile); drainErr != nil {
		slog.Warn("RunSetup: stderr drain ended with error", "err", drainErr)
	}
	// Always close stderr so the subprocess' write end can finalise.
	_ = spawn.Stderr.Close()

	// Wait for the subprocess to fully exit so we can surface its exit code.
	waitErr := spawn.Wait()
	if waitErr != nil {
		msg := fmt.Sprintf("install-daemon.sh exited with error: %v", waitErr)
		// Only emit a synthetic error event if no PHASE_ERROR was already
		// emitted by the script itself (which would have populated
		// rt.currentState.LastError before we got here). If the script
		// crashed before emitting PHASE_ERROR (signal kill, hard segfault),
		// we surface our synthetic message; otherwise the script's own
		// PHASE_ERROR takes precedence and we leave the cached LastError
		// alone so the React side sees the original phase-specific text.
		current := a.snapshotSetupState()
		if current.LastError == "" {
			a.setSetupError(msg)
			a.emitErrorEvent(msg)
		}
		return a.snapshotSetupState(), fmt.Errorf("RunSetup: %w", waitErr)
	}

	return a.snapshotSetupState(), nil
}

// runSetupParseLoop drains stderr line by line, dispatching to the parser
// and teeing every line to logFile. Returns the scanner's terminal error
// (nil on clean EOF).
//
// Split out from RunSetup so tests can invoke it directly with an io.Reader
// of canned stderr — see TestRunSetup_ParsesPhase*.
//
// Concurrency model:
//   - Parser state mutations and rt.currentState updates happen under
//     rt.mu (held only across handleLine).
//   - Event emission happens AFTER the mutex is released, so a Wails emit
//     path that turns out to be slow (e.g. recorder lock contention in
//     tests) doesn't extend the critical section.
//   - To keep handleLine simple, we capture each emitted event into a small
//     per-line buffer inside the parser's emit closure, then drain the
//     buffer once handleLine returns and rt.mu is unlocked.
func (a *App) runSetupParseLoop(stderr io.Reader, logFile io.Writer) error {
	rt := setupRuntimeFor(a)

	// Snapshot the emitter pointer once per call. Tests that swap the
	// emitter mid-run are not a supported scenario (the recorder is
	// installed before runSetupParseLoop starts and cleared at t.Cleanup).
	rt.mu.Lock()
	em := rt.emitter
	rt.mu.Unlock()
	if em == nil {
		em = wailsEmitter{}
	}

	// pendingEmits is the per-line buffer the parser writes to; the loop
	// drains it after releasing the mutex.
	var pendingEmits []setupProgressEvent
	parser := &setupParser{
		state: &rt.currentState,
		emit: func(evt setupProgressEvent) {
			pendingEmits = append(pendingEmits, evt)
		},
	}

	// The default bufio.Scanner buffer is 64 KiB — too small for some pip
	// install lines that include very long resolved-deps printouts. Grow
	// the buffer ceiling to 1 MiB; lines longer than that are truncated and
	// teed verbatim with a slog.Warn (no parser intent could match a 1 MiB
	// PHASE_* marker anyway).
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	for scanner.Scan() {
		raw := scanner.Bytes()
		// Tee the raw bytes (with a trailing newline restored) to setup.log
		// BEFORE we touch parser state. This way even a panic in handleLine
		// can't lose the line from the persistent log.
		if logFile != nil {
			_, _ = logFile.Write(raw)
			_, _ = logFile.Write([]byte{'\n'})
		}
		line := strings.TrimRight(string(raw), " \t")
		if line == "" {
			continue
		}
		// Mutate parser state + state ptr under the mutex so concurrent
		// GetSetupState calls always read a coherent snapshot. Emit happens
		// after the unlock so the emitter call path (which may take its
		// own locks — e.g. the test recorder) doesn't extend our critical
		// section.
		pendingEmits = pendingEmits[:0]
		rt.mu.Lock()
		recognised := parser.handleLine(line)
		rt.mu.Unlock()
		for _, evt := range pendingEmits {
			em.Emit(a.ctx, "setup", evt)
		}
		if !recognised {
			slog.Warn("setup: unrecognised stderr", "line", line)
		}
	}
	return scanner.Err()
}

// GetSetupState returns the cached snapshot. Safe for the React HUD to call
// at any time (including before any RunSetup has been kicked off — in that
// case Complete is computed on the fly from the sentinel file).
//
// Mirrors v0.1.5's GetPipelineStatus binding pattern: returns a value, never
// nil; callers don't have to handle a missing-state case.
func (a *App) GetSetupState() setup.SetupState {
	rt := setupRuntimeFor(a)
	rt.mu.Lock()
	state := rt.currentState
	rt.mu.Unlock()

	// On the very first call before RunSetup has populated state, the
	// version stamp is empty. Backfill it so React always sees the field.
	if state.SetupVersion == "" {
		state.SetupVersion = setup.SetupExpectedVersion
	}
	// Complete is recomputed on every read so React reflects sentinel
	// changes immediately (e.g. a successful end-of-phase-4 write that
	// happens after the last RunSetup return).
	state.Complete = a.IsSetupComplete()
	return state
}

// handleRequestSetupState is the daemon-WS / command-dispatcher hook
// referenced by docs/setup-events.md's "React to Go" section. It mirrors the
// v0.1.5 request_pipeline_status pattern: a late-mounting React HUD fires
// `{type:'request_setup_state'}` via sendJarvisCommand, and Go responds by
// emitting a fresh `setup_state` event on the `setup` channel.
//
// Wiring this into the actual command dispatcher is intentionally deferred
// (the existing dispatcher in app_jarvis.go SendJarvisCommand forwards to
// the daemon; setup messages need a Go-side intercept that lands with the
// TASK-007 / TASK-010 work). Today this method is callable directly from
// tests, and from any future router that wants to forward the React command.
func (a *App) handleRequestSetupState() {
	state := a.GetSetupState()
	evt := setupStateEvent{
		Type:           "setup_state",
		Complete:       state.Complete,
		Phase:          state.Phase,
		PhaseDoneCount: state.PhaseDoneCount,
		SetupVersion:   state.SetupVersion,
		LastError:      state.LastError,
	}
	rt := setupRuntimeFor(a)
	rt.emit(a.ctx, "setup", evt)
}

// ---------------------------------------------------------------------------
// Daemon model-event bridge (TASK-007)
// ---------------------------------------------------------------------------
//
// handleDaemonModelEvent is the callback the `jarvis`-channel subscriber
// invokes for every payload the daemon forwards (state_change, transcript,
// model_setup, model_download, ...). The bridge only consumes model_setup
// and model_download — everything else falls through silently.
//
// Gating: this handler is a no-op unless setupRunning == true. That gate
// has two reasons:
//  1. Subscription cleanup in RunSetup's defer is best-effort — Wails'
//     EventsOff is documented to remove the listener but a race against
//     an in-flight emit isn't ruled out, so we belt-and-brace by checking
//     the flag.
//  2. The same daemon channel feeds the FirstRunDownloadOverlay (mid-
//     session model swaps via Settings). Those events must NOT double-
//     render on the SetupScreen channel; the gate guarantees the
//     SetupScreen receives bridge events only during the initial install.
//
// Mapping (daemon payload -> setup_progress on the `setup` channel):
//
//	model_setup {state:"downloading", models_pending:[{name},...]}
//	  -> one setup_progress {state:started, phase:<mapped>} per pending model.
//	     `ready` state is ignored — the per-model done events drive the
//	     sentinel write because they're strictly more reliable.
//	model_download {model, state:"started",   total_bytes}
//	  -> setup_progress {state:started,  phase:<mapped>}
//	model_download {model, state:"progress", pct, total_bytes,
//	                downloaded_bytes, eta_seconds}
//	  -> setup_progress {state:progress, phase:<mapped>, phaseProgress,
//	                     bytesDone, bytesTotal, etaSeconds}
//	model_download {model, state:"done"}
//	  -> setup_progress {state:done,     phase:<mapped>}; advance
//	     phaseDoneCount; if BOTH vibevoice and whisper have now reported
//	     done, also write the sentinel + emit setup_state {complete:true}.
//	model_download {model, state:"error",   error}
//	  -> setup_progress {state:error,    phase:<mapped>, error}
//
// Model -> phase mapping:
//
//	"vibevoice" -> PhaseVibeVoice
//	"whisper"   -> PhaseWhisper
//	anything else (e.g. "kokoro" if added later) -> silently dropped, since
//	  the SetupScreen only knows the four canonical phases.
func (a *App) handleDaemonModelEvent(payload map[string]interface{}) {
	if payload == nil {
		return
	}
	rt := setupRuntimeFor(a)

	// Gate on setupRunning. Capture emitter under the same lock so the
	// "is setup running?" check and the choice of emitter are consistent
	// with the snapshot tests assert against.
	rt.mu.Lock()
	if !rt.running {
		rt.mu.Unlock()
		return
	}
	em := rt.emitter
	rt.mu.Unlock()
	if em == nil {
		em = wailsEmitter{}
	}

	evtType, _ := payload["type"].(string)
	switch evtType {
	case "model_setup":
		a.bridgeHandleModelSetup(payload, em)
	case "model_download":
		a.bridgeHandleModelDownload(payload, em)
	default:
		// Not a setup-relevant event (state_change, transcript, ...).
		// Drop silently — we only mirror the model phases.
	}
}

// bridgeHandleModelSetup processes a `model_setup` event from the daemon.
// State `downloading` with a non-empty models_pending list emits a
// `started` setup_progress event for each pending model the bridge
// recognises. State `ready` is a no-op here (the per-model `done` events
// drive sentinel-write because they're strictly more reliable — they
// don't depend on the daemon batching the final ready emit relative to
// the last per-model done).
func (a *App) bridgeHandleModelSetup(payload map[string]interface{}, em eventEmitter) {
	state, _ := payload["state"].(string)
	if state != "downloading" {
		return
	}
	pendingRaw, _ := payload["models_pending"].([]interface{})
	for _, entry := range pendingRaw {
		obj, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := obj["name"].(string)
		phase, ok := modelNameToPhase(name)
		if !ok {
			// Unknown model — silently skip. The SetupScreen only
			// recognises the four canonical phases.
			continue
		}
		// Update cached state under the lock so a concurrent
		// GetSetupState sees the phase advance.
		rt := setupRuntimeFor(a)
		rt.mu.Lock()
		rt.currentState.Phase = phase
		rt.currentState.PhaseProgress = 0
		rt.currentState.LastError = ""
		rt.mu.Unlock()
		em.Emit(a.ctx, "setup", setupProgressEvent{
			Type:  "setup_progress",
			Phase: phase,
			State: stateStarted,
		})
	}
}

// bridgeHandleModelDownload processes a `model_download` event and emits
// the corresponding setup_progress. See handleDaemonModelEvent's docstring
// for the full mapping table.
func (a *App) bridgeHandleModelDownload(payload map[string]interface{}, em eventEmitter) {
	model, _ := payload["model"].(string)
	phase, ok := modelNameToPhase(model)
	if !ok {
		// Unknown model — silently drop (kokoro, etc.).
		return
	}
	state, _ := payload["state"].(string)

	rt := setupRuntimeFor(a)

	switch state {
	case "started":
		rt.mu.Lock()
		rt.currentState.Phase = phase
		rt.currentState.PhaseProgress = 0
		rt.currentState.LastError = ""
		rt.mu.Unlock()
		em.Emit(a.ctx, "setup", setupProgressEvent{
			Type:  "setup_progress",
			Phase: phase,
			State: stateStarted,
		})

	case "progress":
		// pct is the daemon's already-clamped 0..100 percentage; mirror
		// the stderr parser's clamp-and-warn behaviour for defence in
		// depth in case a future daemon version slips a bad value.
		pct := int(coerceNumber(payload["pct"]))
		if pct < 0 {
			slog.Warn("bridge: model_download pct < 0; clamped", "value", pct, "model", model)
			pct = 0
		}
		if pct > 100 {
			slog.Warn("bridge: model_download pct > 100; clamped", "value", pct, "model", model)
			pct = 100
		}
		rt.mu.Lock()
		rt.currentState.Phase = phase
		rt.currentState.PhaseProgress = pct
		rt.mu.Unlock()

		pctCopy := pct
		evt := setupProgressEvent{
			Type:          "setup_progress",
			Phase:         phase,
			State:         stateProgress,
			PhaseProgress: &pctCopy,
		}
		// bytes_done / bytes_total are emitted as a pair when both are
		// present and non-negative, matching the stderr parser's
		// behaviour.
		if v, hasD := payload["downloaded_bytes"]; hasD {
			if t, hasT := payload["total_bytes"]; hasT {
				bd := int64(coerceNumber(v))
				bt := int64(coerceNumber(t))
				if bd >= 0 && bt >= 0 {
					evt.BytesDone = &bd
					evt.BytesTotal = &bt
				}
			}
		}
		if v, has := payload["eta_seconds"]; has {
			eta := int(coerceNumber(v))
			if eta >= 0 {
				evt.EtaSeconds = &eta
			}
		}
		em.Emit(a.ctx, "setup", evt)

	case "done":
		// Advance per-phase latch + counter, then emit. Sentinel-write
		// happens after BOTH models have completed.
		writeSentinel := false
		rt.mu.Lock()
		rt.currentState.Phase = phase
		rt.currentState.PhaseProgress = 100
		switch model {
		case "vibevoice":
			if !rt.vibeVoiceDone {
				rt.vibeVoiceDone = true
				rt.currentState.PhaseDoneCount++
			}
		case "whisper":
			if !rt.whisperDone {
				rt.whisperDone = true
				rt.currentState.PhaseDoneCount++
			}
		}
		if rt.vibeVoiceDone && rt.whisperDone {
			writeSentinel = true
		}
		stateCopy := rt.currentState
		rt.mu.Unlock()

		em.Emit(a.ctx, "setup", setupProgressEvent{
			Type:  "setup_progress",
			Phase: phase,
			State: stateDone,
		})

		if writeSentinel {
			// Best-effort sentinel write. The bridge is the optimistic
			// "we made it through phase 4 cleanly" sentinel; the next
			// launch will still re-verify via setup.ReadSentinel against
			// the bundled requirements.txt. A write failure is logged
			// but does NOT block the setup_state emission — the React
			// HUD still flips out of the SetupScreen based on the event.
			data := setup.SentinelData{
				Version:   setup.SetupExpectedVersion,
				Timestamp: nowFn().UTC(),
			}
			if err := setupWriteSentinelFn(data); err != nil {
				slog.Warn("bridge: WriteSentinel failed (HUD still flips via setup_state)", "err", err)
			}
			// Emit setup_state {complete:true} so the React HUD swaps
			// out of the SetupScreen immediately, without waiting for
			// the next launch's IsSetupComplete to fire.
			em.Emit(a.ctx, "setup", setupStateEvent{
				Type:           "setup_state",
				Complete:       true,
				Phase:          stateCopy.Phase,
				PhaseDoneCount: stateCopy.PhaseDoneCount,
				SetupVersion:   setup.SetupExpectedVersion,
			})
		}

	case "error":
		errMsg, _ := payload["error"].(string)
		rt.mu.Lock()
		rt.currentState.Phase = phase
		rt.currentState.LastError = errMsg
		rt.mu.Unlock()
		em.Emit(a.ctx, "setup", setupProgressEvent{
			Type:  "setup_progress",
			Phase: phase,
			State: stateError,
			Error: errMsg,
		})

	default:
		// Unknown / missing state — drop silently. The daemon's contract
		// only defines started/progress/done/error so anything else is
		// a future addition we should ignore for forward compatibility.
	}
}

// modelNameToPhase maps the daemon's short model id (used in event payloads)
// to the SetupPhase enum. Returns false for any name the SetupScreen has no
// row for (e.g. "kokoro" if it ever appears in models_pending).
func modelNameToPhase(name string) (setup.SetupPhase, bool) {
	switch name {
	case "vibevoice":
		return setup.PhaseVibeVoice, true
	case "whisper":
		return setup.PhaseWhisper, true
	}
	return "", false
}

// coerceNumber accepts any JSON-decoded numeric value (float64 from
// encoding/json, or the rare int when callers construct payloads
// directly in tests) and returns a float64 for arithmetic. Returns 0 for
// nil / non-numeric values so the caller can apply its own range checks
// without nil-deref.
func coerceNumber(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// setSetupError records msg as the LastError on the cached state under the
// mutex. Surfacing the error to subscribers is the caller's job (via
// emitErrorEvent); this helper only updates the snapshot that
// GetSetupState / handleRequestSetupState pull from.
func (a *App) setSetupError(msg string) {
	rt := setupRuntimeFor(a)
	rt.mu.Lock()
	rt.currentState.LastError = msg
	rt.mu.Unlock()
}

// snapshotSetupState returns a value copy of the cached state under the
// mutex. Used by RunSetup's exit paths to compose a return value without
// holding the lock across the return.
func (a *App) snapshotSetupState() setup.SetupState {
	rt := setupRuntimeFor(a)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	state := rt.currentState
	if state.SetupVersion == "" {
		state.SetupVersion = setup.SetupExpectedVersion
	}
	return state
}

// emitErrorEvent is the "no phase started, but something blew up at the Go
// layer" fallback emitter. It fires an `error`-state setup_progress event
// using the most-recently-declared phase (empty string if none) so the React
// HUD has at least one event to render its error banner against.
//
// The React type guard will drop events with an empty phase — that's the
// intended behaviour for truly orphan errors (no phase started → no phase
// row to attach the banner to). The cached state still carries LastError so
// a follow-up GetSetupState call surfaces the failure.
func (a *App) emitErrorEvent(msg string) {
	rt := setupRuntimeFor(a)
	// Read phase + emitter under a single lock to keep the operation
	// race-free. Emit happens after unlock so a slow emitter doesn't
	// extend the critical section.
	rt.mu.Lock()
	phase := rt.currentState.Phase
	em := rt.emitter
	rt.mu.Unlock()
	if em == nil {
		em = wailsEmitter{}
	}
	em.Emit(a.ctx, "setup", setupProgressEvent{
		Type:  "setup_progress",
		Phase: phase,
		State: stateError,
		Error: msg,
	})
}
