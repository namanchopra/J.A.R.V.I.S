# v0.2.0 Setup Events Schema (Wails channel: `setup`)

> Status: contract for the v0.2.0 setup-on-launch flow.
> Consumers: Go (`app_setup.go`, TASK-006), React (`use-setup-state.ts`, TASK-010 + `SetupScreen.tsx`, TASK-011), bash (`scripts/setup/install-daemon.sh`, TASK-004), daemon bridge (TASK-007).
> Source of truth for field names, enum values, and parse formats. Downstream tasks build to this document — if you need to change a field name, update this doc first, then notify every consumer task.

## Why a separate channel

Pre-v0.2.0 lifecycle events (`pipeline_status`, `model_setup`, `model_download`, `state_change`, `audio_level`, etc.) flow on the existing Wails `jarvis` channel, which is the daemon-WS passthrough wired in `internal/api/handlers_jarvis_ws.go`. Those events come **from** the Python daemon and are forwarded by Go.

Setup events are different:

- They originate in **Go**, not the daemon (the daemon doesn't exist yet during phases 1 and 2 — it's literally what we're installing).
- They are emitted via Wails `EventsEmit('setup', ...)` directly from `app_setup.go`. They do NOT travel through the daemon WS handler. Do not add a `setup_progress` / `setup_state` case to `handlers_jarvis_ws.go` — that handler is for events that the daemon produces over the WS bridge.
- For phases 3 and 4, the daemon's existing `model_download` / `model_setup` events ARE used as input — but the bridge in TASK-007 re-emits them as `setup_progress` on the `setup` channel rather than the React HUD subscribing to two channels.

Channel name: **`setup`** (lower-case, no namespace prefix).

## Payloads — Go to React

Both payloads are JSON objects emitted via `runtime.EventsEmit(ctx, "setup", payload)`. React subscribes via `EventsOn('setup', handler)` in `useSetupState` (TASK-010).

Each payload's `type` field is the discriminator. React MUST switch on `type` before reading any other field, and MUST reject (console.warn + drop) any event whose `type` is not in the documented enum below.

### `setup_progress`

Emitted by Go on every recognised stderr line from `install-daemon.sh` (phases 1 + 2) and on every `model_setup` / `model_download` event re-emitted by the bridge (phases 3 + 4). One emission per stderr line — no batching, no coalescing — so the React side can render live byte counts.

```ts
interface SetupProgressEvent {
  type: 'setup_progress'
  phase: SetupPhase
  state: 'started' | 'progress' | 'done' | 'error'
  phaseProgress?: number   // integer 0..100, only when state === 'progress'
  bytesDone?: number       // integer bytes, only for download phases
  bytesTotal?: number      // integer bytes, only for download phases
  etaSeconds?: number      // integer seconds remaining; download phases only
  message?: string         // single-line human-readable text for the log viewer
  error?: string           // single-line message, REQUIRED when state === 'error'
}
```

Field rules:

| Field            | Type        | Optional | Range / enum                                              | When set                                                                                          |
| ---------------- | ----------- | -------- | --------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `type`           | string      | no       | always `"setup_progress"`                                 | every emission                                                                                    |
| `phase`          | SetupPhase  | no       | enum below; unknown values are rejected                   | every emission                                                                                    |
| `state`          | string      | no       | `started` / `progress` / `done` / `error`                 | every emission                                                                                    |
| `phaseProgress`  | int         | yes      | 0..100 inclusive; never negative; never > 100             | when `state === 'progress'`. Omitted for `started` / `done` / `error`.                            |
| `bytesDone`      | int         | yes      | >= 0; <= `bytesTotal` when both set                       | only for `vibevoice_download` / `whisper_download` phases. Indeterminate downloads omit it.       |
| `bytesTotal`     | int         | yes      | > 0 when set; same constraint as `bytesDone`              | same as `bytesDone`. MUST be set together with `bytesDone` or both omitted.                       |
| `etaSeconds`     | int         | yes      | >= 0                                                      | when a useful ETA is available. Phase 1's curl-based download may omit during the first 2s.       |
| `message`        | string      | yes      | single line, no embedded `\n`                             | for noteworthy log lines (start of phase, end of phase, retry attempts). Free-form, UI-renderable. |
| `error`          | string      | yes      | single line, no embedded `\n`                             | REQUIRED when `state === 'error'`; absent otherwise.                                              |

Discriminated-union semantics: a consumer should treat `state` as the inner discriminator after `type`. e.g. `state === 'error'` implies `error` is set; `state === 'progress'` implies `phaseProgress` is set.

### `setup_state`

Emitted by Go on three occasions only:

1. When React fires `request_setup_state` (the React side's on-mount refresh, TASK-010).
2. When the sentinel file is written at the end of phase 4 (so the gate in `App.tsx` (TASK-012) can flip to the HUD without re-asking via the binding).
3. When `IsSetupComplete()` flips from true to false at runtime (only realistic case: a future v0.2.1 dep bump invalidates the sentinel; not expected during a single boot).

```ts
interface SetupStateEvent {
  type: 'setup_state'
  complete: boolean
  phase?: SetupPhase       // most-recently-active phase; absent if setup hasn't started yet
  phaseDoneCount: number   // integer 0..4
  setupVersion: string     // e.g. "0.2.0"
  lastError?: string       // present when the last setup attempt failed
}
```

Field rules:

| Field             | Type       | Optional | Range / enum                                                         |
| ----------------- | ---------- | -------- | -------------------------------------------------------------------- |
| `type`            | string     | no       | always `"setup_state"`                                               |
| `complete`        | boolean    | no       | `true` iff sentinel exists and validates (see TASK-008)              |
| `phase`           | SetupPhase | yes      | omitted when no setup attempt has run yet on this boot               |
| `phaseDoneCount`  | int        | no       | 0..4 inclusive; monotonic-nondecreasing within a single run          |
| `setupVersion`    | string     | no       | semver-like; current value `"0.2.0"`                                 |
| `lastError`       | string     | yes      | last `PHASE_ERROR` message; cleared when `complete` becomes `true`   |

`setup_state` is a snapshot. It is NOT emitted on every phase tick — that's what `setup_progress` is for. Consumers that need live progress subscribe to both event types and use `setup_progress` for the per-phase progress bar, `setup_state` for the gate decision.

## Payloads — React to Go

React sends these via the existing `sendJarvisCommand(JSON.stringify({...}))` helper. The Go side routes them on `type` inside the `App` struct's command-dispatcher; setup-related types are handled by `app_setup.go` (TASK-006), not the daemon WS handler (which would have nowhere to forward them).

### `request_setup_state`

Fired once by `useSetupState` (TASK-010) on mount, mirroring how `usePipelineStatus` fires `request_pipeline_status`. Late-mounting React (Wails initialised after Go already emitted its first event) needs this to repopulate.

```ts
{ type: 'request_setup_state' }
```

Go's response: emit a `setup_state` event with the current cached state (from the `setupCurrentState` mutex-protected field in TASK-006). No payload comes back through the helper's return value — the response is event-only, fire-and-forget on the React side.

### `retry_setup_phase`

Fired by the SetupScreen's RETRY button (TASK-011) when an in-progress phase errored. The Go side re-spawns `install-daemon.sh` (which is idempotent + resumable via per-phase sentinel files; see TASK-004 acceptance criteria).

```ts
{
  type: 'retry_setup_phase'
  phase: SetupPhase   // the phase the user clicked retry on; one of the four enum values
}
```

Go's response:

- If a setup run is already in-flight (the `setupRunning` mutex flag from TASK-006 is set), drop the request and emit a `setup_progress` event with the current in-flight phase's state so React stays in sync. Do NOT spawn a second `install-daemon.sh`.
- Otherwise re-spawn `install-daemon.sh`. Because every phase checks its sentinel before running, this is safe even when the user retries from phase 3 — phases 1 and 2 will no-op and only the failing phase + later phases will actually execute.
- The `phase` field is currently advisory (the script picks up where its sentinels say it left off). Future versions may use it to force re-run of a specific phase; do not rely on that semantic yet.

## Canonical phases

The phases run STRICTLY in this order. They are never interleaved, never reordered, and never skipped on a clean install. (On a resumed install where a phase already completed, that phase is a no-op but its `PHASE_DONE` is still emitted by the script so progress reporting stays consistent.)

| # | String value          | Stage                                       | Approx duration (home broadband) | Bytes downloaded |
| - | --------------------- | ------------------------------------------- | -------------------------------- | ---------------- |
| 1 | `python_install`      | Fetch + verify python-build-standalone arm64 | ~30s                             | ~90 MB           |
| 2 | `venv_install`        | `uv venv` + `uv pip install -r requirements.txt` | ~1 min                       | varies (cached wheels common) |
| 3 | `vibevoice_download`  | HF: `microsoft/VibeVoice-Realtime-0.5B`     | ~5–7 min                         | ~1.9 GB          |
| 4 | `whisper_download`    | HF: `mlx-community/whisper-small.en-mlx`    | ~2–3 min                         | ~460 MB          |

Ordering invariants:

- For a single setup run, `phaseDoneCount` increases monotonically from 0 to 4. It never decrements within a run.
- Phases 1 and 2 are emitted by `install-daemon.sh` (TASK-004) via the stderr format below.
- Phases 3 and 4 are emitted by the daemon's `model_status.prefetch_models` and forwarded by the TASK-007 bridge from the `jarvis` channel to the `setup` channel. The daemon doesn't even start running until phase 2 completes.
- An error in phase N halts the flow. Phases N+1..4 are NOT auto-started. The React SetupScreen renders the inline error + retry; the retry path re-spawns `install-daemon.sh` which picks up at the failing phase.

## install-daemon.sh stderr format

`install-daemon.sh` writes structured progress markers to **stderr** (so stdout stays free for the script's own logging / verbose `set -x` output if a maintainer enables it). Go reads stderr line-by-line and parses each line against the prefixes below.

All prefixes are at the **start of the line**. Trailing whitespace is trimmed before matching. Multi-line PHASE_* markers are not allowed — every marker fits on one line.

| Prefix            | Format                                  | Emits                                                              |
| ----------------- | --------------------------------------- | ------------------------------------------------------------------ |
| `PHASE:`          | `PHASE: <phase>`                        | `setup_progress { phase, state: 'started' }`                       |
| `PHASE_PROGRESS:` | `PHASE_PROGRESS: <int 0..100>`          | `setup_progress { phase, state: 'progress', phaseProgress: N }`    |
| `PHASE_BYTES:`    | `PHASE_BYTES: <done> / <total>`         | augments the next `setup_progress` with `bytesDone` + `bytesTotal` |
| `PHASE_ETA:`      | `PHASE_ETA: <seconds>`                  | augments the next `setup_progress` with `etaSeconds`               |
| `PHASE_DONE:`     | `PHASE_DONE: <phase>`                   | `setup_progress { phase, state: 'done' }` + advance `phaseDoneCount` |
| `PHASE_ERROR:`    | `PHASE_ERROR: <single-line message>`    | `setup_progress { phase, state: 'error', error: msg }` + halt      |

Parser hints (TASK-006 uses these; they live here so the contract is canonical):

```
^PHASE:\s+(python_install|venv_install|vibevoice_download|whisper_download)\s*$
^PHASE_PROGRESS:\s+(\d{1,3})\s*$
^PHASE_BYTES:\s+(\d+)\s+/\s+(\d+)\s*$
^PHASE_ETA:\s+(\d+)\s*$
^PHASE_DONE:\s+(python_install|venv_install|vibevoice_download|whisper_download)\s*$
^PHASE_ERROR:\s+(.+)$
```

Format notes:

- `<phase>` MUST be one of the four canonical phase strings. Unknown phase strings are rejected by Go (`slog.Warn("setup: unrecognised phase", "phase", x)` + drop) and are NOT emitted to React.
- `PHASE_PROGRESS` is an integer 0..100. Decimals are not allowed. Values >100 or <0 are clamped at the boundary AND logged at `slog.Warn` (the script is buggy if this happens).
- `PHASE_BYTES` uses a literal `space slash space` separator — `<done> / <total>`. Both are integer bytes (no `MB` / `KB` suffixes). The slash separator was chosen to make the line trivially distinguishable from `PHASE:` lines under any regex.
- `PHASE_ETA` is integer seconds. If indeterminate, the script simply does not emit `PHASE_ETA` for that tick.
- `PHASE_ERROR` text MUST NOT contain embedded newlines. If the underlying error has a multi-line message (e.g. a curl error block), the script joins on `; ` before emitting.

Unknown-prefix handling:

- Any stderr line that does NOT match one of these prefixes is passed through to `~/.jarvis/logs/setup.log` verbatim and logged at `slog.Warn("setup: unrecognised stderr line", "line", line)`. It does NOT cause an event emission. This is how the parser tolerates `set -x` debug output, curl `-#` progress bars, uv's own progress output, etc. without crashing.
- Phases 3 and 4 do NOT come from the script's stderr — the script exits at the end of phase 2 and the daemon starts. The TASK-007 bridge then watches daemon WS events.

## TypeScript types (canonical)

These are the exact types `frontend/src/lib/use-setup-state.ts` (TASK-010) must export. Other React modules import from there; the strings below are the source of truth.

```ts
export type SetupPhase =
  | 'python_install'
  | 'venv_install'
  | 'vibevoice_download'
  | 'whisper_download'

export type SetupProgressState = 'started' | 'progress' | 'done' | 'error'

export interface SetupProgressEvent {
  type: 'setup_progress'
  phase: SetupPhase
  state: SetupProgressState
  phaseProgress?: number   // integer 0..100
  bytesDone?: number       // integer bytes
  bytesTotal?: number      // integer bytes
  etaSeconds?: number      // integer seconds
  message?: string         // single-line, no '\n'
  error?: string           // single-line, no '\n'; required iff state === 'error'
}

export interface SetupStateEvent {
  type: 'setup_state'
  complete: boolean
  phase?: SetupPhase
  phaseDoneCount: number   // integer 0..4
  setupVersion: string     // e.g. '0.2.0'
  lastError?: string
}

export interface RequestSetupStateCommand {
  type: 'request_setup_state'
}

export interface RetrySetupPhaseCommand {
  type: 'retry_setup_phase'
  phase: SetupPhase
}
```

Type guards (TASK-010 must implement these and reject malformed events without crashing):

- `isSetupProgressEvent(v: unknown): v is SetupProgressEvent`
- `isSetupStateEvent(v: unknown): v is SetupStateEvent`

Guards reject the event on any of: missing `type`, wrong `type` literal, unknown `phase` enum, unknown `state` enum, wrong field types, `phaseProgress` out of range, `error` missing when `state === 'error'`. On rejection: `console.warn('useSetupState: dropped malformed event', v)` + return; do not update component state.

## Go types (canonical)

These are the exact types `internal/setup/setup.go` (TASK-002) must export. The `app_setup.go` parser (TASK-006) constructs these and feeds them to `runtime.EventsEmit(ctx, "setup", e)`. The JSON tags MUST match the TS field names exactly — Wails serialises the struct via `encoding/json` and React reads it as `unknown` then type-guards.

```go
package setup

type SetupPhase string

const (
    PhasePython    SetupPhase = "python_install"
    PhaseVenv      SetupPhase = "venv_install"
    PhaseVibeVoice SetupPhase = "vibevoice_download"
    PhaseWhisper   SetupPhase = "whisper_download"
)

type SetupProgressState string

const (
    StateStarted  SetupProgressState = "started"
    StateProgress SetupProgressState = "progress"
    StateDone     SetupProgressState = "done"
    StateError    SetupProgressState = "error"
)

// SetupProgressEvent mirrors the TS SetupProgressEvent shape.
// JSON tags MUST match the TS field names exactly.
type SetupProgressEvent struct {
    Type          string             `json:"type"`           // always "setup_progress"
    Phase         SetupPhase         `json:"phase"`
    State         SetupProgressState `json:"state"`
    PhaseProgress *int               `json:"phaseProgress,omitempty"` // 0..100
    BytesDone     *int64             `json:"bytesDone,omitempty"`
    BytesTotal    *int64             `json:"bytesTotal,omitempty"`
    EtaSeconds    *int               `json:"etaSeconds,omitempty"`
    Message       string             `json:"message,omitempty"`
    Error         string             `json:"error,omitempty"`
}

// SetupStateEvent mirrors the TS SetupStateEvent shape.
type SetupStateEvent struct {
    Type           string     `json:"type"`            // always "setup_state"
    Complete       bool       `json:"complete"`
    Phase          SetupPhase `json:"phase,omitempty"`
    PhaseDoneCount int        `json:"phaseDoneCount"`
    SetupVersion   string     `json:"setupVersion"`
    LastError      string     `json:"lastError,omitempty"`
}
```

Notes on the Go shape:

- Pointer fields (`*int`, `*int64`) are used for `phaseProgress` / `bytesDone` / `bytesTotal` / `etaSeconds` so `omitempty` distinguishes "not set" from a real zero. A `phaseProgress` of 0 is meaningful (start of a phase); a missing `phaseProgress` is also meaningful (state is not `progress`).
- `Phase` on `SetupStateEvent` is `omitempty` — the zero value `SetupPhase("")` serialises to absent rather than to `"phase":""`, which the React type guard would reject.
- The `Type` field is a plain `string` (not a typed enum) because Go can't constrain a string to a single literal at compile time; the constructor functions in `app_setup.go` always set it to the right value.

## Behavioural contracts

These invariants are enforced by the producer (Go) and may be RELIED UPON by the consumer (React) without defensive checks. Violations are bugs in the producer.

1. `phaseDoneCount` is monotonic-nondecreasing within a single setup run. It only resets when a new run starts (e.g. user clicked retry after a hard failure).
2. The four canonical phase values are the ONLY valid `phase` strings. Go MUST reject any other string parsed from stderr (`slog.Warn` + drop). React MUST reject any other string in a received event (console.warn + drop).
3. `bytesDone` and `bytesTotal` are jointly optional: either BOTH set (in the same event) or BOTH absent. A missing pair means "progress is indeterminate" — UI renders an indeterminate spinner, not a 0% bar.
4. `state === 'error'` in any phase halts the flow. The producer MUST NOT emit `setup_progress` for any later phase until the user retries. The producer MUST set `error` on this event.
5. `setup_state.complete === true` is emitted exactly once per setup run — at the moment the sentinel file is written (end of phase 4). Subsequent boots emit it once on receipt of `request_setup_state`.
6. The `type` discriminator is the FIRST field checked on every event. Consumers MUST switch on `type` before reading any other field. Unknown `type` values (anything not in `{setup_progress, setup_state}`) are logged-and-dropped, not crashed-on.
7. The Go side MUST tee every stderr line — recognised or not — to `~/.jarvis/logs/setup.log` for post-hoc debugging. This is independent of event emission.
8. Concurrent `RunSetup` calls are de-duplicated by Go via the `setupMu` mutex + `setupRunning` flag (TASK-006). React MAY fire `retry_setup_phase` while a run is already in flight; Go responds with the current in-flight state, not a second spawn.

## Happy path — full stderr sequence

A successful end-to-end install of v0.2.0 writes approximately this sequence to `install-daemon.sh` stderr (interleaved tool output is omitted; only the PHASE_* markers Go parses are shown). Phases 3 and 4 are emitted by the daemon, not the script — they're shown here so the full event timeline is visible.

```
# Phase 1 — python-build-standalone fetch
PHASE: python_install
PHASE_BYTES: 0 / 92341056
PHASE_PROGRESS: 0
PHASE_BYTES: 12582912 / 92341056
PHASE_PROGRESS: 13
PHASE_ETA: 26
PHASE_BYTES: 41943040 / 92341056
PHASE_PROGRESS: 45
PHASE_ETA: 14
PHASE_BYTES: 88080384 / 92341056
PHASE_PROGRESS: 95
PHASE_ETA: 1
PHASE_PROGRESS: 100
PHASE_DONE: python_install

# Phase 2 — uv venv + uv pip install
PHASE: venv_install
PHASE_PROGRESS: 5
PHASE_PROGRESS: 23
PHASE_PROGRESS: 47
PHASE_PROGRESS: 68
PHASE_PROGRESS: 89
PHASE_PROGRESS: 100
PHASE_DONE: venv_install

# install-daemon.sh exits here; the daemon launches and emits the rest
# over the daemon WS, which the TASK-007 bridge converts to setup_progress
# events on the `setup` channel.

# Phase 3 — VibeVoice via daemon bridge
PHASE: vibevoice_download                          (bridge-synthesized from model_setup)
PHASE_BYTES: 0 / 1932735283
PHASE_PROGRESS: 0
PHASE_BYTES: 419430400 / 1932735283
PHASE_PROGRESS: 22
PHASE_ETA: 287
PHASE_BYTES: 1610612736 / 1932735283
PHASE_PROGRESS: 83
PHASE_ETA: 41
PHASE_PROGRESS: 100
PHASE_DONE: vibevoice_download

# Phase 4 — Whisper via daemon bridge
PHASE: whisper_download
PHASE_BYTES: 0 / 482344960
PHASE_PROGRESS: 0
PHASE_BYTES: 241172480 / 482344960
PHASE_PROGRESS: 50
PHASE_ETA: 78
PHASE_PROGRESS: 100
PHASE_DONE: whisper_download
```

After `PHASE_DONE: whisper_download`, Go writes the sentinel at `~/.jarvis/.setup-version-0.2.0` (TASK-008) and emits a final `setup_state { complete: true, phaseDoneCount: 4 }` so the App.tsx gate (TASK-012) can flip to the HUD.

## Unhappy paths

Four canonical failure scenarios. The React SetupScreen (TASK-011) must render each correctly with an inline error banner + RETRY button on the relevant phase row.

### A. Phase 1 network failure

curl-based python-build-standalone download fails (DNS / SSL / mid-stream disconnect). The script tries `--retry 3 --retry-delay 2` automatically; if all retries fail it emits:

```
PHASE: python_install
PHASE_BYTES: 0 / 92341056
PHASE_PROGRESS: 0
PHASE_BYTES: 8388608 / 92341056
PHASE_PROGRESS: 9
PHASE_ERROR: failed to download python-build-standalone after 3 retries; check your network connection
```

Script exits with non-zero status. Go emits `setup_progress { phase: 'python_install', state: 'error', error: '...' }`. SetupScreen renders error banner under phase 1 row + RETRY button. Clicking retry re-spawns `install-daemon.sh`; phase 1's sentinel is absent so it re-runs from scratch (no partial-file resume — curl wrote to a `.tmp` that gets cleaned up on PHASE_ERROR).

### B. Phase 2 uv install fails mid-install

`uv pip install -r requirements.txt` fails on a specific wheel (e.g. a transitive dep that needs to compile from source without Xcode CLI tools, or a wheel that's missing for cp313 arm64).

```
PHASE: venv_install
PHASE_PROGRESS: 5
PHASE_PROGRESS: 47
PHASE_ERROR: uv pip install failed on dep 'somepkg==1.2.3': missing wheel for cp313 arm64 (consider running 'xcode-select --install' if a compile is required)
```

Script exits non-zero. Go emits `setup_progress { phase: 'venv_install', state: 'error', error: '...' }`. SetupScreen renders error banner under phase 2 row. Phase 1's sentinel (`~/.jarvis/python/.fetch-complete`) is intact, so RETRY re-runs only from phase 2. Phase 2 itself has no completion sentinel until the END of the phase, so the retry re-runs the full `uv pip install` — but uv's cache at `~/.cache/uv/` means most already-downloaded wheels are reused, so the retry is fast.

### C. Phase 3 HuggingFace rate limit (or other download failure)

The daemon's `model_status.prefetch_models` raises an HFHubHTTPError (429 rate limit, 404 model not found, network drop). The daemon emits a `model_setup` event with an error field; the TASK-007 bridge translates that to:

```
PHASE: vibevoice_download
PHASE_BYTES: 0 / 1932735283
PHASE_PROGRESS: 0
PHASE_BYTES: 419430400 / 1932735283
PHASE_PROGRESS: 22
PHASE_ERROR: huggingface_hub returned 429 Too Many Requests for microsoft/VibeVoice-Realtime-0.5B; retry in a few minutes
```

SetupScreen renders the error inline under phase 3 row. RETRY re-spawns `install-daemon.sh`; phases 1 + 2 sentinels are intact so they no-op. The script exits at end of phase 2 and the daemon re-launches, which re-attempts the model download (Hugging Face client picks up partial files in `~/.cache/huggingface/`).

### D. User-quit mid-phase (signal handling)

User quits Jarvis mid-phase-2 (or kills the Jarvis process while `install-daemon.sh` is running its child `uv pip install`). The script receives SIGTERM from its parent (Go's `cmd.Process.Kill()` on shutdown).

```
PHASE: venv_install
PHASE_PROGRESS: 5
PHASE_PROGRESS: 23
(script killed; no PHASE_ERROR emitted)
```

No `PHASE_ERROR` is emitted — the script trap catches SIGTERM and exits silently (the user didn't ASK for an error report, they asked to quit). The Go side observes `cmd.Wait()` returning a signal-killed exit status; `RunSetup` returns without writing the sentinel.

On next launch, `IsSetupComplete()` returns false (no sentinel). `App.tsx` mounts `SetupScreen` again. The user clicks "Start setup" (or it auto-starts per TASK-012 spec); `install-daemon.sh` re-runs. Phase 1's sentinel is intact (phase 1 had completed) so it no-ops; phase 2 re-runs `uv pip install`, hits uv's cache, completes in a fraction of the original time. Phases 3 + 4 proceed normally.

This is the **resumability contract**: per-phase sentinels (`~/.jarvis/python/.fetch-complete`, `~/.jarvis/jarvis-daemon-env/.venv-complete`) make every interrupted install recoverable without manual cleanup. The user never has to `rm -rf` anything.

## Cross-references

| Doc consumer | File | Responsibility |
| ------------ | ---- | -------------- |
| TASK-002     | `internal/setup/setup.go`, `internal/paths/paths.go` | Owns the `SetupPhase` constants + Go event structs declared above. Path helpers for sentinels + log. |
| TASK-004     | `scripts/setup/install-daemon.sh`     | Emits the stderr format documented above for phases 1 + 2.                                                  |
| TASK-006     | `app_setup.go`                        | Parses the stderr, constructs the Go event structs, calls `runtime.EventsEmit(ctx, "setup", ...)`. Handles `request_setup_state` and `retry_setup_phase` from React. |
| TASK-007     | `app_setup.go` (or `app_setup_bridge.go`) | Bridges daemon `model_setup` / `model_download` events on the `jarvis` channel into `setup_progress` events on the `setup` channel, for phases 3 + 4. Stops bridging once setup completes. |
| TASK-010     | `frontend/src/lib/use-setup-state.ts` | Implements the React hook. Subscribes to `setup` channel, type-guards events, fires `request_setup_state` on mount. Exports the TS types declared above as the canonical source for other React files. |
| TASK-011     | `frontend/src/components/setup/SetupScreen.tsx` | Renders the 4 phases. Fires `retry_setup_phase` from its RETRY button. Calls `OpenSetupLog()` (TASK-016) from the "View setup log" link. |
| TASK-012     | `frontend/src/App.tsx`                | Subscribes to `setup_state` to flip the gate from `<SetupScreen>` to the regular HUD when `complete === true`. |
