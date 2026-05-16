// ---------------------------------------------------------------------------
// useSetupState — source-level + runtime contract test (v0.2.0 / TASK-010).
//
// Mirrors the v0.1.5 use-pipeline-status.test.ts split:
//   1. Source-level assertions for the lifecycle / channel / refresh contract
//      (no jsdom — the frontend test harness doesn't ship one).
//   2. Runtime assertions for the exported `isSetupProgressEvent` /
//      `isSetupStateEvent` type guards, which are pure functions.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './use-setup-state.ts?raw'
import {
  isSetupProgressEvent,
  isSetupStateEvent,
} from './use-setup-state'

// ---------------------------------------------------------------------------
// 1. Source-level contract
// ---------------------------------------------------------------------------

describe('useSetupState source contract (v0.2.0)', () => {
  it('subscribes to the dedicated `setup` Wails channel via EventsOn', () => {
    // The hook MUST listen on the v0.2.0-dedicated `'setup'` channel, NOT
    // the daemon-multiplexed `'jarvis'` channel — see
    // docs/setup-events.md "Why a separate channel".
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*EventsOn[^}]*\}\s*from\s+['"][^'"]*wailsjs\/runtime\/runtime['"]/,
    )
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]setup['"]/)
    // Belt-and-braces: must NOT be listening on 'jarvis' for these events.
    expect(SOURCE).not.toMatch(/EventsOn\(\s*['"]jarvis['"]/)
  })

  it('refresh() dispatches a request_setup_state message via sendJarvisCommand', () => {
    // refresh() must JSON.stringify the documented command payload and
    // hand it to sendJarvisCommand (the same helper the v0.1.5 hook uses).
    expect(SOURCE).toMatch(/sendJarvisCommand/)
    expect(SOURCE).toMatch(/['"]request_setup_state['"]/)
    expect(SOURCE).toMatch(
      /JSON\.stringify\(\s*\{\s*type:\s*['"]request_setup_state['"]/,
    )
  })

  it('kicks off a refresh on mount so late-connecting clients repopulate', () => {
    // The mount-time `useEffect` MUST call `refresh()` so a SetupScreen
    // that mounts after Go already emitted its first event still
    // populates correctly.
    const useEffectBlock = SOURCE.match(
      /useEffect\(\(\)\s*=>\s*\{[\s\S]*?\},\s*\[[^\]]*\]\)/,
    )
    expect(useEffectBlock).not.toBeNull()
    if (useEffectBlock) {
      expect(useEffectBlock[0]).toMatch(/refresh\(\)/)
      // The same block must wire the EventsOn subscription.
      expect(useEffectBlock[0]).toMatch(/EventsOn\(\s*['"]setup['"]/)
    }
  })

  it('tears down the EventsOn subscription on unmount', () => {
    // Pattern: const cancel = EventsOn(...); return () => { cancel() }.
    // Without this we'd leak a listener per SetupScreen mount.
    expect(SOURCE).toMatch(/const\s+cancel\s*=\s*EventsOn\(/)
    expect(SOURCE).toMatch(/return\s*\(\)\s*=>\s*\{\s*cancel\(\)/)
  })

  it('exports the canonical TS types for downstream consumers', () => {
    // SetupScreen + App.tsx import these types. Keep them co-located with
    // the runtime guards so the contract lives in one file.
    expect(SOURCE).toMatch(/export\s+type\s+SetupPhase\b/)
    expect(SOURCE).toMatch(/export\s+interface\s+SetupProgressEvent\b/)
    expect(SOURCE).toMatch(/export\s+interface\s+SetupStateEvent\b/)
    expect(SOURCE).toMatch(/export\s+interface\s+PhaseRow\b/)
    expect(SOURCE).toMatch(/export\s+function\s+useSetupState\b/)
  })

  it('declares all four canonical phase strings in source', () => {
    // The canonical phase enum in docs/setup-events.md. If any of these
    // disappear, downstream tasks (TASK-006, TASK-011) will silently
    // diverge from the contract.
    expect(SOURCE).toMatch(/['"]python_install['"]/)
    expect(SOURCE).toMatch(/['"]venv_install['"]/)
    expect(SOURCE).toMatch(/['"]vibevoice_download['"]/)
    expect(SOURCE).toMatch(/['"]whisper_download['"]/)
  })

  it('drops malformed events with a single console.warn', () => {
    // Failure mode: a partial release / mistyped Go field would otherwise
    // crash the gate. Spec says: log-and-drop, do not throw.
    expect(SOURCE).toMatch(
      /console\.warn\(\s*['"]useSetupState: rejected malformed event['"]/,
    )
  })
})

// ---------------------------------------------------------------------------
// 2. Runtime type-guard behavior — isSetupProgressEvent
// ---------------------------------------------------------------------------

describe('isSetupProgressEvent (type guard)', () => {
  const validProgress = {
    type: 'setup_progress',
    phase: 'python_install',
    state: 'progress',
    phaseProgress: 42,
    bytesDone: 12582912,
    bytesTotal: 92341056,
    etaSeconds: 14,
    message: 'downloading python-build-standalone',
  }

  it('accepts a well-formed setup_progress payload', () => {
    expect(isSetupProgressEvent(validProgress)).toBe(true)
  })

  it('accepts a started/done event with no progress fields', () => {
    expect(
      isSetupProgressEvent({
        type: 'setup_progress',
        phase: 'venv_install',
        state: 'started',
      }),
    ).toBe(true)
    expect(
      isSetupProgressEvent({
        type: 'setup_progress',
        phase: 'whisper_download',
        state: 'done',
      }),
    ).toBe(true)
  })

  it('accepts an error event with the required `error` field', () => {
    expect(
      isSetupProgressEvent({
        type: 'setup_progress',
        phase: 'vibevoice_download',
        state: 'error',
        error: 'HF 429 rate limit',
      }),
    ).toBe(true)
  })

  it('rejects null / non-objects / arrays / primitives', () => {
    expect(isSetupProgressEvent(null)).toBe(false)
    expect(isSetupProgressEvent(undefined)).toBe(false)
    expect(isSetupProgressEvent('setup_progress')).toBe(false)
    expect(isSetupProgressEvent(42)).toBe(false)
    expect(isSetupProgressEvent(true)).toBe(false)
    expect(isSetupProgressEvent([])).toBe(false)
    expect(isSetupProgressEvent([validProgress])).toBe(false)
  })

  it('rejects events with the wrong `type` discriminator', () => {
    expect(
      isSetupProgressEvent({ ...validProgress, type: 'setup_state' }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, type: 'pipeline_status' }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, type: 'setup_progresss' }),
    ).toBe(false)
  })

  it('rejects unknown phase enum values', () => {
    expect(
      isSetupProgressEvent({ ...validProgress, phase: 'rust_install' }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, phase: '' }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, phase: 42 }),
    ).toBe(false)
  })

  it('rejects unknown state enum values', () => {
    expect(
      isSetupProgressEvent({ ...validProgress, state: 'running' }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, state: 'failed' }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, state: null }),
    ).toBe(false)
  })

  it('rejects events missing a required top-level field', () => {
    const { phase: _phase, ...noPhase } = validProgress
    void _phase
    expect(isSetupProgressEvent(noPhase)).toBe(false)

    const { state: _state, ...noState } = validProgress
    void _state
    expect(isSetupProgressEvent(noState)).toBe(false)

    const { type: _type, ...noType } = validProgress
    void _type
    expect(isSetupProgressEvent(noType)).toBe(false)
  })

  it('rejects optional fields with wrong types', () => {
    expect(
      isSetupProgressEvent({ ...validProgress, phaseProgress: 'forty-two' }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, bytesDone: '12MB' }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, etaSeconds: null }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, message: 42 }),
    ).toBe(false)
    expect(
      isSetupProgressEvent({ ...validProgress, error: 42 }),
    ).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// 3. Runtime type-guard behavior — isSetupStateEvent
// ---------------------------------------------------------------------------

describe('isSetupStateEvent (type guard)', () => {
  const validState = {
    type: 'setup_state',
    complete: false,
    phase: 'venv_install',
    phaseDoneCount: 1,
    setupVersion: '0.2.0',
    lastError: '',
  }

  it('accepts a well-formed setup_state payload', () => {
    expect(isSetupStateEvent(validState)).toBe(true)
  })

  it('accepts a snapshot with phase + lastError omitted (no run yet)', () => {
    expect(
      isSetupStateEvent({
        type: 'setup_state',
        complete: false,
        phaseDoneCount: 0,
        setupVersion: '0.2.0',
      }),
    ).toBe(true)
  })

  it('accepts a complete=true snapshot at end of run', () => {
    expect(
      isSetupStateEvent({
        type: 'setup_state',
        complete: true,
        phase: 'whisper_download',
        phaseDoneCount: 4,
        setupVersion: '0.2.0',
      }),
    ).toBe(true)
  })

  it('rejects null / non-objects / arrays / primitives', () => {
    expect(isSetupStateEvent(null)).toBe(false)
    expect(isSetupStateEvent(undefined)).toBe(false)
    expect(isSetupStateEvent('setup_state')).toBe(false)
    expect(isSetupStateEvent(0)).toBe(false)
    expect(isSetupStateEvent(false)).toBe(false)
    expect(isSetupStateEvent([])).toBe(false)
    expect(isSetupStateEvent([validState])).toBe(false)
  })

  it('rejects events with the wrong `type` discriminator', () => {
    expect(isSetupStateEvent({ ...validState, type: 'setup_progress' })).toBe(
      false,
    )
    expect(isSetupStateEvent({ ...validState, type: 'setup_statee' })).toBe(
      false,
    )
  })

  it('rejects required fields with wrong types', () => {
    expect(isSetupStateEvent({ ...validState, complete: 'true' })).toBe(false)
    expect(isSetupStateEvent({ ...validState, complete: 1 })).toBe(false)
    expect(isSetupStateEvent({ ...validState, phaseDoneCount: '2' })).toBe(
      false,
    )
    expect(isSetupStateEvent({ ...validState, setupVersion: 0.2 })).toBe(false)
  })

  it('rejects an unknown phase enum value (when phase is present)', () => {
    expect(
      isSetupStateEvent({ ...validState, phase: 'pip_install' }),
    ).toBe(false)
    expect(isSetupStateEvent({ ...validState, phase: 42 })).toBe(false)
  })

  it('rejects a non-string lastError (when lastError is present)', () => {
    expect(isSetupStateEvent({ ...validState, lastError: 42 })).toBe(false)
    expect(
      isSetupStateEvent({ ...validState, lastError: { msg: 'boom' } }),
    ).toBe(false)
  })

  it('rejects events missing a required top-level field', () => {
    const { complete: _complete, ...noComplete } = validState
    void _complete
    expect(isSetupStateEvent(noComplete)).toBe(false)

    const { phaseDoneCount: _pdc, ...noCount } = validState
    void _pdc
    expect(isSetupStateEvent(noCount)).toBe(false)

    const { setupVersion: _sv, ...noVersion } = validState
    void _sv
    expect(isSetupStateEvent(noVersion)).toBe(false)
  })
})
