// ---------------------------------------------------------------------------
// FirstRunDownloadOverlay -- full-screen overlay shown while the Python
// daemon downloads model weights (VibeVoice TTS + Whisper STT). Mounts when
// `model_setup state=downloading` is the latest aggregate event; unmounts on
// `model_setup state=ready`. Per-model progress comes via `model_download`
// events. See the event-schema contract in TASK spec.
// ---------------------------------------------------------------------------

import { useEffect, useMemo, useRef, useState } from 'react'
import { EventsOn } from '../../../wailsjs/runtime/runtime'
import { sendJarvisCommand } from '../../lib/jarvis-api'

// ---------------------------------------------------------------------------
// Event types (mirror of the daemon contract)
// ---------------------------------------------------------------------------

export type ModelName = 'vibevoice' | 'whisper' | 'kokoro'

export type ModelDownloadEvent = {
  type: 'model_download'
  model: ModelName
  state: 'started' | 'progress' | 'done' | 'error'
  total_bytes?: number
  downloaded_bytes?: number
  pct?: number
  speed_bytes_per_sec?: number
  eta_seconds?: number
  error?: string
}

export type ModelSetupEvent = {
  type: 'model_setup'
  state: 'ready' | 'downloading'
  models_pending: Array<{ name: string; approx_size_bytes: number }>
}

// ---------------------------------------------------------------------------
// Type guards -- raw WS payloads are untyped JSON; narrow defensively
// ---------------------------------------------------------------------------

function isModelName(v: unknown): v is ModelName {
  return v === 'vibevoice' || v === 'whisper' || v === 'kokoro'
}

function isModelDownloadEvent(v: unknown): v is ModelDownloadEvent {
  if (v === null || typeof v !== 'object') return false
  const o = v as Record<string, unknown>
  if (o.type !== 'model_download') return false
  if (!isModelName(o.model)) return false
  const st = o.state
  return st === 'started' || st === 'progress' || st === 'done' || st === 'error'
}

function isModelSetupEvent(v: unknown): v is ModelSetupEvent {
  if (v === null || typeof v !== 'object') return false
  const o = v as Record<string, unknown>
  if (o.type !== 'model_setup') return false
  if (o.state !== 'ready' && o.state !== 'downloading') return false
  if (!Array.isArray(o.models_pending)) return false
  return o.models_pending.every((m) => {
    if (m === null || typeof m !== 'object') return false
    const mm = m as Record<string, unknown>
    return typeof mm.name === 'string' && typeof mm.approx_size_bytes === 'number'
  })
}

// ---------------------------------------------------------------------------
// Per-model row state
// ---------------------------------------------------------------------------

type RowState = 'started' | 'progress' | 'done' | 'error'

interface ModelRow {
  model: ModelName
  state: RowState
  approxSize: number
  totalBytes?: number
  downloadedBytes?: number
  pct?: number
  speedBps?: number
  etaSeconds?: number
  error?: string
  // Set when state flips to 'done' so we can pulse the row briefly
  doneAt?: number
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

function formatRate(bps: number | undefined): string {
  if (bps === undefined || !Number.isFinite(bps) || bps <= 0) return ''
  return `${formatBytes(bps)}/s`
}

function formatEta(seconds: number | undefined): string {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds <= 0) return ''
  if (seconds < 60) return `~${Math.round(seconds)}s`
  const mins = Math.round(seconds / 60)
  if (mins < 60) return `~${mins} min`
  const hours = Math.floor(mins / 60)
  const rest = mins % 60
  return rest === 0 ? `~${hours}h` : `~${hours}h ${rest}m`
}

function joinReadout(parts: Array<string | undefined>): string {
  return parts.filter((p): p is string => Boolean(p && p.length > 0)).join(' · ')
}

const MODEL_LABEL: Record<ModelName, string> = {
  vibevoice: 'VIBEVOICE',
  whisper: 'WHISPER',
  kokoro: 'KOKORO',
}

// ---------------------------------------------------------------------------
// Keyframe injection (idempotent)
// ---------------------------------------------------------------------------

const DL_KEYFRAMES_ID = 'first-run-download-keyframes'

function injectDlKeyframes(): void {
  if (typeof document === 'undefined') return
  if (document.getElementById(DL_KEYFRAMES_ID)) return

  const style = document.createElement('style')
  style.id = DL_KEYFRAMES_ID
  style.textContent = `
    @keyframes frd-spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
    @keyframes frd-fade-in {
      from { opacity: 0; }
      to   { opacity: 1; }
    }
    @keyframes frd-fade-out {
      from { opacity: 1; }
      to   { opacity: 0; }
    }
    @keyframes frd-bar-flash {
      0%   { box-shadow: 0 0 0 1px rgba(0,229,255,0.25); }
      50%  { box-shadow: 0 0 0 1px rgba(0,229,255,0.85), 0 0 16px rgba(0,229,255,0.55); }
      100% { box-shadow: 0 0 0 1px rgba(0,229,255,0.25); }
    }
    @keyframes frd-row-pulse {
      0%   { background: rgba(0,229,255,0.04); }
      50%  { background: rgba(0,255,136,0.18); }
      100% { background: rgba(0,229,255,0.04); }
    }
    @keyframes frd-indeterminate {
      0%   { background-position: -200% 0; }
      100% { background-position: 200% 0; }
    }
    .frd-fade-in  { animation: frd-fade-in 250ms ease-out forwards; }
    .frd-spin     { animation: frd-spin 1.4s linear infinite; display: inline-block; }
    .frd-bar-flash { animation: frd-bar-flash 0.6s ease-out; }
    .frd-row-pulse { animation: frd-row-pulse 0.9s ease-out; }
  `
  document.head.appendChild(style)
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

interface CornerBracketsProps {
  size?: number
  color?: string
}

function CornerBrackets({
  size = 16,
  color = 'rgba(0,229,255,0.65)',
}: CornerBracketsProps): React.ReactElement {
  const common = { width: size, height: size, position: 'absolute' as const }
  return (
    <>
      <div
        aria-hidden="true"
        style={{ ...common, top: 0, left: 0, borderTop: `2px solid ${color}`, borderLeft: `2px solid ${color}` }}
      />
      <div
        aria-hidden="true"
        style={{ ...common, top: 0, right: 0, borderTop: `2px solid ${color}`, borderRight: `2px solid ${color}` }}
      />
      <div
        aria-hidden="true"
        style={{ ...common, bottom: 0, left: 0, borderBottom: `2px solid ${color}`, borderLeft: `2px solid ${color}` }}
      />
      <div
        aria-hidden="true"
        style={{ ...common, bottom: 0, right: 0, borderBottom: `2px solid ${color}`, borderRight: `2px solid ${color}` }}
      />
    </>
  )
}

// State icon: ◌ (in-progress, spins) / ◉ (done) / ✕ (error)
function StateIcon({ state }: { state: RowState }): React.ReactElement {
  if (state === 'done') {
    return (
      <span
        aria-hidden="true"
        style={{
          color: 'var(--accent-blue, #00e5ff)',
          textShadow: '0 0 8px rgba(0,229,255,0.7)',
          fontSize: 14,
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
          fontSize: 14,
          fontWeight: 700,
        }}
      >
        ✕
      </span>
    )
  }
  // started or progress -- spin
  return (
    <span
      aria-hidden="true"
      className="frd-spin"
      style={{
        color: 'var(--accent-blue, #00e5ff)',
        textShadow: '0 0 8px rgba(0,229,255,0.6)',
        fontSize: 14,
      }}
    >
      ◌
    </span>
  )
}

interface ProgressBarProps {
  pct?: number
  indeterminate: boolean
  flash: boolean
}

function ProgressBar({ pct, indeterminate, flash }: ProgressBarProps): React.ReactElement {
  const clamped = pct === undefined ? 0 : Math.max(0, Math.min(100, pct))
  return (
    <div
      className={flash ? 'frd-bar-flash' : ''}
      style={{
        position: 'relative',
        height: 5,
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
            animation: 'frd-indeterminate 1.2s linear infinite',
          }}
        />
      ) : (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            width: `${clamped}%`,
            background:
              'linear-gradient(90deg, rgba(0,229,255,0.85), rgba(68,255,238,1))',
            boxShadow: '0 0 8px rgba(0,229,255,0.6)',
            transition: 'width 180ms ease-out',
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

interface ModelRowViewProps {
  row: ModelRow
  flash: boolean
  pulse: boolean
  onRetry: (model: ModelName) => void
}

function ModelRowView({ row, flash, pulse, onRetry }: ModelRowViewProps): React.ReactElement {
  const readout = useMemo(() => {
    if (row.state === 'error') return ''
    if (row.state === 'done') {
      // Show "1.9 GB downloaded" so the user gets a sense of scale on completion.
      const size = formatBytes(row.totalBytes ?? row.approxSize)
      return size ? `${size} · DONE` : 'DONE'
    }
    // started / progress
    const downloaded = formatBytes(row.downloadedBytes)
    const total = formatBytes(row.totalBytes ?? row.approxSize)
    const sizeStr = downloaded && total ? `${downloaded} / ${total}` : (downloaded || total)
    return joinReadout([sizeStr, formatRate(row.speedBps), formatEta(row.etaSeconds)])
  }, [row])

  const showIndeterminate =
    row.state === 'started' ||
    (row.state === 'progress' && (row.pct === undefined))

  return (
    <div
      className={pulse ? 'frd-row-pulse' : ''}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        padding: '8px 10px',
        background: 'rgba(0,229,255,0.04)',
        border: '1px solid rgba(0,229,255,0.10)',
        borderRadius: 3,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        {/* Status icon (left) */}
        <div style={{ width: 18, display: 'flex', justifyContent: 'center', flexShrink: 0 }}>
          <StateIcon state={row.state} />
        </div>

        {/* Model name */}
        <div
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 11,
            fontWeight: 700,
            letterSpacing: '0.22em',
            textTransform: 'uppercase' as const,
            color: row.state === 'error' ? '#ff8a8a' : 'rgba(0,229,255,0.85)',
            textShadow:
              row.state === 'error'
                ? '0 0 6px rgba(255,68,68,0.4)'
                : '0 0 6px rgba(0,229,255,0.35)',
            flexShrink: 0,
            minWidth: 90,
          }}
        >
          {MODEL_LABEL[row.model]}
        </div>

        {/* Bar (fills middle) */}
        <div style={{ flex: 1, minWidth: 0 }}>
          <ProgressBar
            pct={row.pct}
            indeterminate={showIndeterminate}
            flash={flash}
          />
        </div>

        {/* Readout */}
        <div
          aria-live="polite"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 10,
            letterSpacing: '0.08em',
            color: 'rgba(0,229,255,0.6)',
            minWidth: 150,
            textAlign: 'right' as const,
            fontVariantNumeric: 'tabular-nums',
            flexShrink: 0,
          }}
        >
          {readout || (row.state === 'started' ? 'STARTING…' : '')}
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
            marginLeft: 28,
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
            {row.error || 'Download failed.'}
          </span>
          <button
            type="button"
            onClick={() => onRetry(row.model)}
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
              ;(e.currentTarget as HTMLButtonElement).style.background = 'rgb(220,38,38)'
            }}
            onMouseLeave={(e) => {
              ;(e.currentTarget as HTMLButtonElement).style.background = 'rgba(185,28,28,0.7)'
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
// Daemon-log opener -- guarded so the build doesn't break before the
// `OpenDaemonLog` Wails binding lands.
// ---------------------------------------------------------------------------

async function openDaemonLog(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.OpenDaemonLog as
      | (() => Promise<void>)
      | undefined
    if (typeof fn === 'function') {
      await fn()
    }
  } catch {
    // binding not available yet -- silent no-op
  }
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

interface FirstRunDownloadOverlayProps {
  /**
   * Optional: when provided, the parent owns the visibility flag. When omitted,
   * the overlay self-manages state from `EventsOn('jarvis', ...)` events.
   */
  forceVisible?: boolean
}

export function FirstRunDownloadOverlay(
  _props: FirstRunDownloadOverlayProps = {},
): React.ReactElement | null {
  // Inject keyframes on mount
  useEffect(() => {
    injectDlKeyframes()
  }, [])

  // Aggregate setup state -- 'unknown' until the daemon tells us otherwise.
  const [setupState, setSetupState] = useState<'unknown' | 'downloading' | 'ready'>('unknown')
  const [pending, setPending] = useState<Array<{ name: string; approx_size_bytes: number }>>([])

  // Per-model rows keyed by model name (only the canonical 3)
  const [rows, setRows] = useState<Record<ModelName, ModelRow | undefined>>({
    vibevoice: undefined,
    whisper: undefined,
    kokoro: undefined,
  })

  // Per-model flash trigger (bar outline pulses on progress event arrival)
  const [flashStamps, setFlashStamps] = useState<Record<ModelName, number>>({
    vibevoice: 0,
    whisper: 0,
    kokoro: 0,
  })

  // Per-model "just completed" pulse trigger
  const [pulseStamps, setPulseStamps] = useState<Record<ModelName, number>>({
    vibevoice: 0,
    whisper: 0,
    kokoro: 0,
  })

  // Subscribe to the 'jarvis' WS event channel
  useEffect(() => {
    const cancel = EventsOn('jarvis', (event: unknown) => {
      if (isModelSetupEvent(event)) {
        setSetupState(event.state)
        setPending(event.models_pending)
        return
      }
      if (isModelDownloadEvent(event)) {
        const m = event.model
        setRows((prev) => {
          const existing = prev[m]
          const wasProgressing = existing?.state === 'started' || existing?.state === 'progress'
          const nextRow: ModelRow = {
            model: m,
            state: event.state,
            approxSize: existing?.approxSize ?? 0,
            totalBytes: event.total_bytes ?? existing?.totalBytes,
            downloadedBytes: event.downloaded_bytes ?? existing?.downloadedBytes,
            pct: event.pct ?? existing?.pct,
            speedBps: event.speed_bytes_per_sec ?? existing?.speedBps,
            etaSeconds: event.eta_seconds ?? existing?.etaSeconds,
            error: event.state === 'error' ? event.error : undefined,
            doneAt: existing?.doneAt,
          }
          // If transitioning into a fresh "started" or retry, clear progress
          if (event.state === 'started') {
            nextRow.pct = undefined
            nextRow.downloadedBytes = undefined
            nextRow.speedBps = undefined
            nextRow.etaSeconds = undefined
            nextRow.error = undefined
          }
          // Detect progress -> done transition to trigger a row pulse
          if (event.state === 'done' && wasProgressing) {
            nextRow.doneAt = Date.now()
            setPulseStamps((p) => ({ ...p, [m]: Date.now() }))
            // auto-clear after pulse window
            setTimeout(() => {
              setPulseStamps((p) => (p[m] === nextRow.doneAt ? { ...p, [m]: 0 } : p))
            }, 950)
          }
          return { ...prev, [m]: nextRow }
        })
        // Flash the bar on progress event arrival
        if (event.state === 'progress' || event.state === 'started') {
          setFlashStamps((p) => ({ ...p, [m]: Date.now() }))
          setTimeout(() => {
            setFlashStamps((p) => (Date.now() - p[m] >= 600 ? { ...p, [m]: 0 } : p))
          }, 650)
        }
      }
    })
    return () => {
      cancel()
    }
  }, [])

  // Track elapsed time so we can surface the "slow connection" helper after 2 min
  const [elapsedSeconds, setElapsedSeconds] = useState(0)
  const startedAtRef = useRef<number | null>(null)
  useEffect(() => {
    if (setupState !== 'downloading') {
      startedAtRef.current = null
      setElapsedSeconds(0)
      return
    }
    if (startedAtRef.current === null) {
      startedAtRef.current = Date.now()
    }
    const id = setInterval(() => {
      if (startedAtRef.current !== null) {
        setElapsedSeconds(Math.floor((Date.now() - startedAtRef.current) / 1000))
      }
    }, 1000)
    return () => clearInterval(id)
  }, [setupState])

  // Compute mount visibility -- show when latest setup event is downloading AND
  // at least one model has been seen in started/progress/error.
  const hasActiveRow = (Object.values(rows) as Array<ModelRow | undefined>).some(
    (r) => r !== undefined && (r.state === 'started' || r.state === 'progress' || r.state === 'error'),
  )

  const visible = setupState === 'downloading' && hasActiveRow

  const onRetry = (model: ModelName): void => {
    // Optimistically reset the row to indeterminate; the daemon should follow up
    // with a 'started' / 'progress' event shortly.
    setRows((prev) => {
      const existing = prev[model]
      if (!existing) return prev
      return {
        ...prev,
        [model]: {
          ...existing,
          state: 'started',
          pct: undefined,
          downloadedBytes: undefined,
          speedBps: undefined,
          etaSeconds: undefined,
          error: undefined,
        },
      }
    })
    // Send the retry message back to the daemon. The existing daemon-write
    // path stringifies the command — we serialize the JSON payload here.
    void sendJarvisCommand(JSON.stringify({ type: 'retry_model_download', model }))
  }

  if (!visible) return null

  // Build the ordered list of rows to render. Prefer the order announced by
  // models_pending; fall back to whatever order we've seen rows.
  const orderedModels: ModelName[] = (() => {
    const fromPending = pending
      .map((p) => p.name)
      .filter(isModelName) as ModelName[]
    if (fromPending.length > 0) return fromPending
    return (['vibevoice', 'whisper', 'kokoro'] as ModelName[]).filter(
      (m) => rows[m] !== undefined,
    )
  })()

  // Merge approxSize from pending into rows for display
  const approxByName = new Map<string, number>(
    pending.map((p) => [p.name, p.approx_size_bytes]),
  )

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="frd-overlay-title"
      className="frd-fade-in"
      style={{
        position: 'absolute',
        inset: 0,
        zIndex: 80,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: 'rgba(2,10,8,0.85)',
        backdropFilter: 'blur(12px)',
        WebkitBackdropFilter: 'blur(12px)',
        padding: 24,
      }}
    >
      {/* Card */}
      <div
        className="holo-panel scanline"
        style={{
          position: 'relative',
          width: '100%',
          maxWidth: 600,
          padding: '28px 30px 22px 30px',
          background: 'linear-gradient(135deg, rgba(2,18,16,0.96), rgba(2,10,8,0.98))',
          border: '1px solid rgba(0,229,255,0.35)',
          boxShadow:
            '0 0 0 1px rgba(0,229,255,0.08), 0 0 40px rgba(0,229,255,0.18), inset 0 0 24px rgba(0,229,255,0.04)',
        }}
      >
        <CornerBrackets size={16} color="rgba(0,229,255,0.7)" />

        {/* Header */}
        <div
          id="frd-overlay-title"
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
            className="frd-spin"
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
          One-time download of voice + speech models. ~2.4 GB total.
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

        {/* Per-model rows */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {orderedModels.map((m) => {
            const r = rows[m]
            const merged: ModelRow = r
              ? { ...r, approxSize: r.approxSize || (approxByName.get(m) ?? 0) }
              : {
                  model: m,
                  state: 'started',
                  approxSize: approxByName.get(m) ?? 0,
                }
            return (
              <ModelRowView
                key={m}
                row={merged}
                flash={flashStamps[m] !== 0}
                pulse={pulseStamps[m] !== 0}
                onRetry={onRetry}
              />
            )
          })}
        </div>

        {/* Helper text */}
        <div
          style={{
            marginTop: 18,
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 10,
            letterSpacing: '0.08em',
            color: 'rgba(207,231,255,0.5)',
            lineHeight: 1.55,
          }}
        >
          Jarvis is downloading its voice and speech models from Hugging Face.
          This is one-time. After it finishes you can disconnect from the
          internet and Jarvis will keep working.
        </div>

        {/* Slow-connection secondary helper (after 2 min) */}
        {elapsedSeconds > 120 && (
          <div
            className="frd-fade-in"
            style={{
              marginTop: 8,
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 10,
              letterSpacing: '0.08em',
              color: 'rgba(255,170,0,0.7)',
              lineHeight: 1.55,
            }}
          >
            Slow connection? You can keep using your Mac while this finishes —
            Jarvis will come online automatically.
          </div>
        )}

        {/* Bottom: view daemon log link */}
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
              void openDaemonLog()
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
              ;(e.currentTarget as HTMLButtonElement).style.color = 'var(--accent-blue, #00e5ff)'
              ;(e.currentTarget as HTMLButtonElement).style.textShadow =
                '0 0 10px rgba(0,229,255,0.55)'
            }}
            onMouseLeave={(e) => {
              ;(e.currentTarget as HTMLButtonElement).style.color = 'rgba(0,229,255,0.6)'
              ;(e.currentTarget as HTMLButtonElement).style.textShadow =
                '0 0 6px rgba(0,229,255,0.25)'
            }}
          >
            ▸ VIEW DAEMON LOG →
          </button>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Augment Window for the Wails runtime bridge (kept narrow on purpose -- we
// only need OpenDaemonLog here, which may or may not exist yet)
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
