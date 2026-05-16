// ---------------------------------------------------------------------------
// useSetupState — subscribes to v0.2.0 `setup_progress` / `setup_state` events
// emitted by the Go side directly on the dedicated `'setup'` Wails channel
// (NOT the daemon-multiplexed `'jarvis'` channel — see docs/setup-events.md
// "Why a separate channel" for the rationale).
//
// Consumers:
//   - <SetupScreen> (TASK-011) — renders the 4-phase install HUD and the
//     RETRY button.
//   - <App.tsx> (TASK-012) — flips the gate from SetupScreen to the regular
//     HUD when `setup_state.complete === true`.
//
// Shape mirrors `use-pipeline-status.ts` (v0.1.5) exactly: an EventsOn
// subscription torn down on unmount, a refresh() helper that re-asks Go for
// the current state via sendJarvisCommand, and a defensive runtime type
// guard so a malformed payload never crashes the gate.
// ---------------------------------------------------------------------------

import { useCallback, useEffect, useState } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { sendJarvisCommand } from './jarvis-api'

// ---------------------------------------------------------------------------
// Event schema — canonical source is docs/setup-events.md. These types are
// the v0.2.0 contract; downstream React modules import them from here.
// ---------------------------------------------------------------------------

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
  phaseProgress?: number // integer 0..100
  bytesDone?: number // integer bytes
  bytesTotal?: number // integer bytes
  etaSeconds?: number // integer seconds
  message?: string // single-line, no '\n'
  error?: string // single-line, no '\n'; required iff state === 'error'
}

export interface SetupStateEvent {
  type: 'setup_state'
  complete: boolean
  phase?: SetupPhase // most-recently-active phase; absent if not started
  phaseDoneCount: number // integer 0..4
  setupVersion: string // e.g. '0.2.0'
  lastError?: string
}

/**
 * A single row in the 4-phase install table. Every phase starts in `pending`
 * (before any event has arrived for it) and ratchets through `started` →
 * `progress` → `done` (or → `error`). `doneAt` is set on the `done` transition
 * so the UI can show a "completed at HH:MM:SS" timestamp.
 */
export interface PhaseRow {
  phase: SetupPhase
  state: 'pending' | 'started' | 'progress' | 'done' | 'error'
  phaseProgress?: number
  bytesDone?: number
  bytesTotal?: number
  etaSeconds?: number
  error?: string
  doneAt?: number
}

// All four canonical phases in order. Used to seed the `phases` record so
// SetupScreen can render the rows even before any event has arrived.
const SETUP_PHASES: readonly SetupPhase[] = [
  'python_install',
  'venv_install',
  'vibevoice_download',
  'whisper_download',
] as const

const SETUP_PROGRESS_STATES: readonly SetupProgressState[] = [
  'started',
  'progress',
  'done',
  'error',
] as const

// ---------------------------------------------------------------------------
// Type guards — defensive narrowing because raw Wails payloads are unknown.
// Mirrors the v0.1.5 use-pipeline-status guard pattern.
// ---------------------------------------------------------------------------

function asRecord(v: unknown): Record<string, unknown> | null {
  if (v === null || typeof v !== 'object') return null
  if (Array.isArray(v)) return null
  return v as Record<string, unknown>
}

function isSetupPhase(v: unknown): v is SetupPhase {
  return (
    v === 'python_install' ||
    v === 'venv_install' ||
    v === 'vibevoice_download' ||
    v === 'whisper_download'
  )
}

function isSetupProgressState(v: unknown): v is SetupProgressState {
  return (
    v === 'started' || v === 'progress' || v === 'done' || v === 'error'
  )
}

export function isSetupProgressEvent(
  value: unknown,
): value is SetupProgressEvent {
  const o = asRecord(value)
  if (!o) return false
  if (o.type !== 'setup_progress') return false
  if (!isSetupPhase(o.phase)) return false
  if (!isSetupProgressState(o.state)) return false

  // Optional numeric fields: if present, must be `number`.
  if (o.phaseProgress !== undefined && typeof o.phaseProgress !== 'number') {
    return false
  }
  if (o.bytesDone !== undefined && typeof o.bytesDone !== 'number') {
    return false
  }
  if (o.bytesTotal !== undefined && typeof o.bytesTotal !== 'number') {
    return false
  }
  if (o.etaSeconds !== undefined && typeof o.etaSeconds !== 'number') {
    return false
  }

  // Optional string fields: if present, must be `string`.
  if (o.message !== undefined && typeof o.message !== 'string') return false
  if (o.error !== undefined && typeof o.error !== 'string') return false

  return true
}

export function isSetupStateEvent(value: unknown): value is SetupStateEvent {
  const o = asRecord(value)
  if (!o) return false
  if (o.type !== 'setup_state') return false
  if (typeof o.complete !== 'boolean') return false
  if (typeof o.phaseDoneCount !== 'number') return false
  if (typeof o.setupVersion !== 'string') return false

  // Optional fields.
  if (o.phase !== undefined && !isSetupPhase(o.phase)) return false
  if (o.lastError !== undefined && typeof o.lastError !== 'string') return false

  return true
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

export interface UseSetupStateResult {
  /** Latest `setup_state` event (snapshot of the gate). null until first
   *  event arrives. */
  state: SetupStateEvent | null
  /** Latest `setup_progress` event (per-tick update). null until first
   *  event arrives. */
  progress: SetupProgressEvent | null
  /** All 4 phases keyed by name. Each row starts at state `pending` and
   *  advances as events arrive. */
  phases: Record<SetupPhase, PhaseRow>
  /** Re-ask Go for the current setup snapshot — used on mount and as a
   *  manual "Request now" trigger. */
  refresh: () => void
}

function initialPhases(): Record<SetupPhase, PhaseRow> {
  return {
    python_install: { phase: 'python_install', state: 'pending' },
    venv_install: { phase: 'venv_install', state: 'pending' },
    vibevoice_download: { phase: 'vibevoice_download', state: 'pending' },
    whisper_download: { phase: 'whisper_download', state: 'pending' },
  }
}

function applyProgress(
  current: Record<SetupPhase, PhaseRow>,
  event: SetupProgressEvent,
): Record<SetupPhase, PhaseRow> {
  const prev = current[event.phase]
  const next: PhaseRow = {
    phase: event.phase,
    state: event.state,
    phaseProgress:
      event.phaseProgress !== undefined
        ? event.phaseProgress
        : prev.phaseProgress,
    bytesDone:
      event.bytesDone !== undefined ? event.bytesDone : prev.bytesDone,
    bytesTotal:
      event.bytesTotal !== undefined ? event.bytesTotal : prev.bytesTotal,
    etaSeconds:
      event.etaSeconds !== undefined ? event.etaSeconds : prev.etaSeconds,
    error: event.state === 'error' ? event.error : prev.error,
    doneAt: event.state === 'done' ? Date.now() : prev.doneAt,
  }
  return { ...current, [event.phase]: next }
}

/**
 * Subscribe to v0.2.0 setup events on the `'setup'` Wails channel.
 *
 * - On mount: subscribes to `EventsOn('setup', ...)` and fires
 *   `{type: 'request_setup_state'}` so a late-mounted SetupScreen
 *   doesn't sit on `state: null` forever.
 * - On unmount: tears down the subscription via the `EventsOn` return
 *   handle (same idiom as `usePipelineStatus`).
 * - Malformed events are dropped with a single `console.warn` and do not
 *   touch component state.
 */
export function useSetupState(): UseSetupStateResult {
  const [state, setState] = useState<SetupStateEvent | null>(null)
  const [progress, setProgress] = useState<SetupProgressEvent | null>(null)
  const [phases, setPhases] = useState<Record<SetupPhase, PhaseRow>>(
    initialPhases,
  )

  const refresh = useCallback((): void => {
    void sendJarvisCommand(
      JSON.stringify({ type: 'request_setup_state' }),
    )
  }, [])

  useEffect(() => {
    const cancel = EventsOn('setup', (event: unknown) => {
      if (isSetupProgressEvent(event)) {
        setProgress(event)
        setPhases((cur) => applyProgress(cur, event))
        return
      }
      if (isSetupStateEvent(event)) {
        setState(event)
        return
      }
      console.warn('useSetupState: rejected malformed event', event)
    })

    // Kick Go once on mount so a late-mounting SetupScreen repopulates.
    refresh()

    return () => {
      cancel()
    }
  }, [refresh])

  return { state, progress, phases, refresh }
}

// Internal exports for test/debug visibility. Kept at the bottom so they
// don't pollute the public surface read-order.
export const __SETUP_PHASES_FOR_TEST = SETUP_PHASES
export const __SETUP_PROGRESS_STATES_FOR_TEST = SETUP_PROGRESS_STATES
