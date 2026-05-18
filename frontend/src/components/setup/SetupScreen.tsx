// ---------------------------------------------------------------------------
// SetupScreen — v0.2.0 full-viewport install HUD.
//
// Replaces the JarvisOrb during the v0.2.0 setup-on-launch flow. Renders the
// 4-phase install table (Python runtime → voice pipeline → VibeVoice →
// Whisper), live progress bars, ETA readouts, per-phase error banners with
// RETRY buttons, and a footer "View setup log" link.
//
// Mount/unmount is gated by <App.tsx> (TASK-012) based on
// `useSetupState().state?.complete`. This component does NOT decide its own
// visibility — it always renders all four phase rows, and just animates the
// active row's progress.
//
// Visual vocabulary is borrowed from `hud/FirstRunDownloadOverlay.tsx`:
// corner brackets, scanline texture, cyan tokens (#00e5ff / --accent-blue),
// SF Mono uppercase labels, holo-panel card.
// ---------------------------------------------------------------------------

import { useEffect, useMemo } from 'react'
import {
  type PhaseRow,
  type SetupPhase,
  useSetupState,
} from '../../lib/use-setup-state'
import { sendJarvisCommand } from '../../lib/jarvis-api'

// ---------------------------------------------------------------------------
// Canonical phase order — must match docs/setup-events.md "Canonical phases".
// Exported on `__SETUP_SCREEN_PHASE_ORDER` for test visibility.
// ---------------------------------------------------------------------------

const PHASE_ORDER: readonly SetupPhase[] = [
  'python_install',
  'venv_install',
  'vibevoice_download',
  'whisper_download',
] as const

const PHASE_LABEL: Record<SetupPhase, string> = {
  python_install: 'Installing Python runtime',
  venv_install: 'Installing voice pipeline',
  vibevoice_download: 'Downloading VibeVoice (~1.9 GB)',
  whisper_download: 'Downloading Whisper (~460 MB)',
}

// ---------------------------------------------------------------------------
// Display helpers
// ---------------------------------------------------------------------------

function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined || !Number.isFinite(bytes) || bytes < 0) return ''
  const KB = 1024
  const MB = KB * 1024
  const GB = MB * 1024
  if (bytes >= GB) return `${(bytes / GB).toFixed(1)} GB`
  if (bytes >= MB) return `${Math.round(bytes / MB)} MB`
  if (bytes >= KB) return `${Math.round(bytes / KB)} KB`
  return `${bytes} B`
}

function formatEta(seconds: number | undefined): string {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds <= 0) {
    return ''
  }
  if (seconds < 60) return `~${Math.round(seconds)}s left`
  const mins = Math.round(seconds / 60)
  if (mins < 60) return `~${mins} min left`
  const hours = Math.floor(mins / 60)
  const rest = mins % 60
  return rest === 0 ? `~${hours}h left` : `~${hours}h ${rest}m left`
}

function joinReadout(parts: Array<string | undefined>): string {
  return parts
    .filter((p): p is string => Boolean(p && p.length > 0))
    .join(' · ')
}

// ---------------------------------------------------------------------------
// Keyframe injection (idempotent) — mirrors FirstRunDownloadOverlay's pattern.
// ---------------------------------------------------------------------------

const SETUP_KEYFRAMES_ID = 'setup-screen-keyframes'

function injectSetupKeyframes(): void {
  if (typeof document === 'undefined') return
  if (document.getElementById(SETUP_KEYFRAMES_ID)) return

  const style = document.createElement('style')
  style.id = SETUP_KEYFRAMES_ID
  style.textContent = `
    @keyframes setup-spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
    @keyframes setup-fade-in {
      from { opacity: 0; }
      to   { opacity: 1; }
    }
    @keyframes setup-indeterminate {
      0%   { background-position: -200% 0; }
      100% { background-position: 200% 0; }
    }
    .setup-fade-in { animation: setup-fade-in 250ms ease-out forwards; }
    .setup-spin    { animation: setup-spin 1.4s linear infinite; display: inline-block; }
  `
  document.head.appendChild(style)
}

// ---------------------------------------------------------------------------
// Corner brackets — borrowed from FirstRunDownloadOverlay.
// ---------------------------------------------------------------------------

interface CornerBracketsProps {
  size?: number
  color?: string
}

function CornerBrackets({
  size = 18,
  color = 'rgba(0,229,255,0.7)',
}: CornerBracketsProps): React.ReactElement {
  const common = { width: size, height: size, position: 'absolute' as const }
  return (
    <>
      <div
        aria-hidden="true"
        style={{
          ...common,
          top: 0,
          left: 0,
          borderTop: `2px solid ${color}`,
          borderLeft: `2px solid ${color}`,
        }}
      />
      <div
        aria-hidden="true"
        style={{
          ...common,
          top: 0,
          right: 0,
          borderTop: `2px solid ${color}`,
          borderRight: `2px solid ${color}`,
        }}
      />
      <div
        aria-hidden="true"
        style={{
          ...common,
          bottom: 0,
          left: 0,
          borderBottom: `2px solid ${color}`,
          borderLeft: `2px solid ${color}`,
        }}
      />
      <div
        aria-hidden="true"
        style={{
          ...common,
          bottom: 0,
          right: 0,
          borderBottom: `2px solid ${color}`,
          borderRight: `2px solid ${color}`,
        }}
      />
    </>
  )
}

// ---------------------------------------------------------------------------
// State glyph: ◌ (in progress, spins) / ◉ (done) / ✕ (error) / ◯ (pending)
// ---------------------------------------------------------------------------

function StateGlyph({
  state,
}: {
  state: PhaseRow['state']
}): React.ReactElement {
  if (state === 'done') {
    return (
      <span
        aria-hidden="true"
        style={{
          color: 'var(--accent-blue, #00e5ff)',
          textShadow: '0 0 8px rgba(0,229,255,0.7)',
          fontSize: 15,
        }}
      >
        ◉
      </span>
    )
  }
  if (state === 'error') {
    return (
      <span
        aria-hidden="true"
        style={{
          color: '#ff4444',
          textShadow: '0 0 8px rgba(255,68,68,0.6)',
          fontSize: 15,
          fontWeight: 700,
        }}
      >
        ✕
      </span>
    )
  }
  if (state === 'pending') {
    return (
      <span
        aria-hidden="true"
        style={{
          color: 'rgba(0,229,255,0.35)',
          fontSize: 15,
        }}
      >
        ◯
      </span>
    )
  }
  // started or progress — spin
  return (
    <span
      aria-hidden="true"
      className="setup-spin"
      style={{
        color: 'var(--accent-blue, #00e5ff)',
        textShadow: '0 0 8px rgba(0,229,255,0.6)',
        fontSize: 15,
      }}
    >
      ◌
    </span>
  )
}

// ---------------------------------------------------------------------------
// Progress bar (re-used for the row + adapted from FirstRunDownloadOverlay)
// ---------------------------------------------------------------------------

interface ProgressBarProps {
  pct: number | undefined
  indeterminate: boolean
  done: boolean
}

function ProgressBar({
  pct,
  indeterminate,
  done,
}: ProgressBarProps): React.ReactElement {
  const clamped = pct === undefined ? 0 : Math.max(0, Math.min(100, pct))
  const fillPct = done ? 100 : clamped
  return (
    <div
      style={{
        position: 'relative',
        height: 6,
        background: 'rgba(0,12,10,0.95)',
        border: '1px solid rgba(0,229,255,0.18)',
        borderRadius: 2,
        overflow: 'hidden',
        boxShadow: 'inset 0 0 6px rgba(0,229,255,0.06)',
      }}
    >
      {indeterminate ? (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            background:
              'linear-gradient(90deg, transparent 0%, rgba(0,229,255,0.55) 50%, transparent 100%)',
            backgroundSize: '200% 100%',
            animation: 'setup-indeterminate 1.2s linear infinite',
          }}
        />
      ) : (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            width: `${fillPct}%`,
            background:
              'linear-gradient(90deg, rgba(0,229,255,0.85), rgba(68,255,238,1))',
            boxShadow: '0 0 8px rgba(0,229,255,0.6)',
            transition: 'width 200ms ease-out',
          }}
        />
      )}
      {/* Scanline texture over the fill */}
      <div
        aria-hidden="true"
        style={{
          position: 'absolute',
          inset: 0,
          backgroundImage:
            'repeating-linear-gradient(90deg, rgba(0,0,0,0.18) 0 2px, transparent 2px 4px)',
          pointerEvents: 'none',
        }}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// PhaseRow component — one row per setup phase. Always rendered, but the
// readout/progress only update for the active phase.
// ---------------------------------------------------------------------------

interface PhaseRowProps {
  phase: SetupPhase
  /** 1-indexed display number ([1/4], [2/4], ...). */
  index: number
  row: PhaseRow
  onRetry: (phase: SetupPhase) => void
}

function PhaseRowView({
  phase,
  index,
  row,
  onRetry,
}: PhaseRowProps): React.ReactElement {
  const readout = useMemo(() => {
    if (row.state === 'pending') return ''
    if (row.state === 'error') return ''
    if (row.state === 'done') return 'DONE'

    // started / progress
    const downloaded = formatBytes(row.bytesDone)
    const total = formatBytes(row.bytesTotal)
    const sizeStr =
      downloaded && total ? `${downloaded} / ${total}` : downloaded || total
    const pctStr =
      row.phaseProgress !== undefined && !sizeStr
        ? `${row.phaseProgress}%`
        : undefined
    const eta = formatEta(row.etaSeconds)
    return joinReadout([sizeStr, pctStr, eta])
  }, [row])

  const showIndeterminate =
    (row.state === 'started' && row.phaseProgress === undefined) ||
    (row.state === 'progress' && row.phaseProgress === undefined)

  const labelDimmed = row.state === 'pending'
  const labelErrored = row.state === 'error'

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        padding: '10px 12px',
        background: 'rgba(0,229,255,0.04)',
        border: '1px solid rgba(0,229,255,0.10)',
        borderRadius: 3,
        opacity: labelDimmed ? 0.55 : 1,
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 10,
        }}
      >
        {/* Glyph */}
        <div
          style={{
            width: 20,
            display: 'flex',
            justifyContent: 'center',
            flexShrink: 0,
          }}
        >
          <StateGlyph state={row.state} />
        </div>

        {/* Label */}
        <div
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 11,
            fontWeight: 700,
            letterSpacing: '0.18em',
            textTransform: 'uppercase' as const,
            color: labelErrored
              ? '#ff8a8a'
              : labelDimmed
                ? 'rgba(0,229,255,0.55)'
                : 'rgba(0,229,255,0.9)',
            textShadow: labelErrored
              ? '0 0 6px rgba(255,68,68,0.4)'
              : '0 0 6px rgba(0,229,255,0.35)',
            flex: 1,
            minWidth: 0,
            whiteSpace: 'nowrap' as const,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}
        >
          {PHASE_LABEL[phase]}
        </div>

        {/* Phase index */}
        <div
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 10,
            letterSpacing: '0.15em',
            color: 'rgba(0,229,255,0.5)',
            flexShrink: 0,
          }}
        >
          [{index}/4]
        </div>
      </div>

      {/* Progress bar + readout */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <ProgressBar
            pct={row.phaseProgress}
            indeterminate={showIndeterminate}
            done={row.state === 'done'}
          />
        </div>
        <div
          aria-live="polite"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 10,
            letterSpacing: '0.08em',
            color: 'rgba(0,229,255,0.6)',
            minWidth: 180,
            textAlign: 'right' as const,
            fontVariantNumeric: 'tabular-nums',
            flexShrink: 0,
          }}
        >
          {readout}
        </div>
      </div>

      {/* Inline error banner + retry */}
      {row.state === 'error' && (
        <div
          role="alert"
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            marginLeft: 30,
            padding: '6px 10px',
            background: 'rgba(127,29,29,0.35)',
            border: '1px solid rgba(255,68,68,0.45)',
            borderRadius: 2,
          }}
        >
          <span
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 10,
              color: 'rgb(254,202,202)',
              letterSpacing: '0.05em',
              flex: 1,
              minWidth: 0,
              wordBreak: 'break-word' as const,
            }}
          >
            {row.error || 'Phase failed.'}
          </span>
          <button
            type="button"
            onClick={() => onRetry(phase)}
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 9,
              fontWeight: 700,
              letterSpacing: '0.2em',
              textTransform: 'uppercase' as const,
              padding: '4px 10px',
              color: '#ffd5d5',
              background: 'rgba(185,28,28,0.7)',
              border: '1px solid rgba(255,68,68,0.7)',
              borderRadius: 2,
              cursor: 'pointer',
              flexShrink: 0,
            }}
            onMouseEnter={(e) => {
              ;(e.currentTarget as HTMLButtonElement).style.background =
                'rgb(220,38,38)'
            }}
            onMouseLeave={(e) => {
              ;(e.currentTarget as HTMLButtonElement).style.background =
                'rgba(185,28,28,0.7)'
            }}
          >
            RETRY
          </button>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Setup-log opener — guarded so the build doesn't break before the
// `OpenSetupLog` Wails binding lands (TASK-016).
// ---------------------------------------------------------------------------

async function openSetupLog(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.OpenSetupLog as
      | (() => Promise<void>)
      | undefined
    if (typeof fn === 'function') {
      await fn()
    }
  } catch {
    // binding not available yet — silent no-op
  }
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export function SetupScreen(): React.ReactElement {
  useEffect(() => {
    injectSetupKeyframes()
  }, [])

  const { phases } = useSetupState()

  const onRetry = (phase: SetupPhase): void => {
    void sendJarvisCommand(
      JSON.stringify({ type: 'retry_setup_phase', phase }),
    )
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="setup-screen-title"
      className="setup-fade-in"
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 95,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: 'rgba(2,10,8,0.92)',
        backdropFilter: 'blur(14px)',
        WebkitBackdropFilter: 'blur(14px)',
        padding: 24,
      }}
    >
      {/* Card */}
      <div
        className="holo-panel scanline"
        style={{
          position: 'relative',
          width: '100%',
          maxWidth: 640,
          padding: '30px 32px 24px 32px',
          background:
            'linear-gradient(135deg, rgba(2,18,16,0.96), rgba(2,10,8,0.98))',
          border: '1px solid rgba(0,229,255,0.35)',
          boxShadow:
            '0 0 0 1px rgba(0,229,255,0.08), 0 0 40px rgba(0,229,255,0.18), inset 0 0 24px rgba(0,229,255,0.04)',
        }}
      >
        <CornerBrackets size={18} color="rgba(0,229,255,0.7)" />

        {/* Header */}
        <div
          id="setup-screen-title"
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 12,
            fontWeight: 700,
            letterSpacing: '0.25em',
            textTransform: 'uppercase' as const,
            color: 'var(--accent-blue, #00e5ff)',
            textShadow: '0 0 10px rgba(0,229,255,0.55)',
          }}
        >
          <span aria-hidden="true">▸</span>
          <span>SETTING UP JARVIS</span>
          <span
            aria-hidden="true"
            className="setup-spin"
            style={{
              marginLeft: 'auto',
              fontSize: 16,
              textShadow: '0 0 10px rgba(0,229,255,0.7)',
            }}
          >
            ◌
          </span>
        </div>

        {/* Subtitle */}
        <div
          style={{
            marginTop: 8,
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 11,
            letterSpacing: '0.12em',
            color: 'rgba(0,229,255,0.55)',
          }}
        >
          One-time install of Jarvis runtime + voice models. ~2.4 GB total.
        </div>

        {/* Divider */}
        <div
          aria-hidden="true"
          style={{
            height: 1,
            margin: '18px 0 16px',
            background:
              'linear-gradient(90deg, transparent, rgba(0,229,255,0.35) 20%, rgba(0,229,255,0.35) 80%, transparent)',
          }}
        />

        {/* Phase rows — always render all 4 in canonical order */}
        <div
          style={{ display: 'flex', flexDirection: 'column', gap: 10 }}
          aria-live="polite"
        >
          {PHASE_ORDER.map((phase, i) => (
            <PhaseRowView
              key={phase}
              phase={phase}
              index={i + 1}
              row={phases[phase]}
              onRetry={onRetry}
            />
          ))}
        </div>

        {/* Helper text */}
        <div
          style={{
            marginTop: 18,
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 10,
            letterSpacing: '0.08em',
            color: 'rgba(207,231,255,0.55)',
            lineHeight: 1.55,
          }}
        >
          Total ~10 min on home internet.
          <br />
          You can keep using your Mac while this runs.
        </div>

        {/* Bottom: view setup log link */}
        <div
          style={{
            marginTop: 18,
            paddingTop: 12,
            borderTop: '1px solid rgba(0,229,255,0.12)',
            display: 'flex',
            justifyContent: 'flex-end',
          }}
        >
          <button
            type="button"
            onClick={() => {
              void openSetupLog()
            }}
            style={{
              background: 'transparent',
              border: 'none',
              padding: 0,
              cursor: 'pointer',
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 10,
              fontWeight: 600,
              letterSpacing: '0.22em',
              textTransform: 'uppercase' as const,
              color: 'rgba(0,229,255,0.6)',
              textShadow: '0 0 6px rgba(0,229,255,0.25)',
              transition: 'color 0.15s, text-shadow 0.15s',
            }}
            onMouseEnter={(e) => {
              ;(e.currentTarget as HTMLButtonElement).style.color =
                'var(--accent-blue, #00e5ff)'
              ;(e.currentTarget as HTMLButtonElement).style.textShadow =
                '0 0 10px rgba(0,229,255,0.55)'
            }}
            onMouseLeave={(e) => {
              ;(e.currentTarget as HTMLButtonElement).style.color =
                'rgba(0,229,255,0.6)'
              ;(e.currentTarget as HTMLButtonElement).style.textShadow =
                '0 0 6px rgba(0,229,255,0.25)'
            }}
          >
            ▸ VIEW SETUP LOG →
          </button>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Test-only exports — kept at the bottom so they don't pollute the public
// surface read-order.
// ---------------------------------------------------------------------------

export const __SETUP_SCREEN_PHASE_ORDER = PHASE_ORDER
export const __SETUP_SCREEN_PHASE_LABEL = PHASE_LABEL

// ---------------------------------------------------------------------------
// Augment Window for the Wails runtime bridge (kept narrow — only
// OpenSetupLog matters here, and may not exist until TASK-016).
// ---------------------------------------------------------------------------

declare global {
  interface Window {
    go?: {
      main?: {
        App?: Record<string, unknown>
      }
    }
  }
}
