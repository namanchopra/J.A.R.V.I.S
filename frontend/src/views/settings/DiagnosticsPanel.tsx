import { useCallback, useEffect, useRef, useState } from 'react'
import type { SettingsPanelProps } from './types'
import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
import { usePipelineStatus } from '../../lib/use-pipeline-status'

// macOS URL that opens System Settings → Privacy → Microphone. Used when the
// mic permission row is in the `denied` / `restricted` state (TASK-026).
const MIC_SETTINGS_URL =
  'x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone'

// ---------------------------------------------------------------------------
// DiagnosticsPanel — live system health surface (TASK-022).
//
// Polls `GetDiagnostics` every 2 seconds and renders 7 status rows:
//   1. Daemon         — running/stopped + restart count
//   2. Mic permission — granted/denied/not_determined/restricted
//   3. Mobile API     — port + token preview (last 4 chars)
//   4. LLM chain      — active provider + last error
//   5. Models         — bundled vibevoice / whisper-small status
//   6. Ollama         — local Ollama server reachability
//   7. Disk           — ~/.jarvis size on disk
//
// Each row has a copy-to-clipboard button for the raw value. A master
// "Copy diagnostics" button at the bottom produces a markdown summary
// for bug reports.
//
// Bindings are accessed through `window.go.main.App` instead of the
// generated wailsjs/go/main/App.js wrappers because the binding was added
// in this task and the generated TS surface won't include it until the
// frontend dev server runs (`wails dev` / `wails generate module`).
// This is the same pattern jarvis-api.ts and JarvisSettings.tsx use for
// not-yet-generated bindings.
// ---------------------------------------------------------------------------

export type DiagnosticsPanelProps = SettingsPanelProps

// ---------------------------------------------------------------------------
// Wails binding surface — kept local because the generated types in
// wailsjs/go/main/App.d.ts won't include GetDiagnostics until the next
// `wails generate module`. We mirror the Go-side struct shape here.
// ---------------------------------------------------------------------------

interface DaemonStatus {
  running: boolean
  restarts: number
  lastError?: string
}

interface MobileAPIStatus {
  port: number
  tokenPreview: string
}

interface LLMChainStatus {
  active: string
  lastError?: string
}

interface ModelStatus {
  name: string
  path: string
  loaded: boolean
  sizeMb?: number
}

interface DiskUsageStatus {
  jarvisHome: string
  sizeMb: number
}

interface DiagnosticsSnapshot {
  daemon: DaemonStatus
  micPermission: string
  mobileApi: MobileAPIStatus
  llmChain: LLMChainStatus
  models: ModelStatus[]
  ollamaRunning: boolean
  diskUsage: DiskUsageStatus
}

declare global {
  interface Window {
    go?: {
      main?: {
        App?: Record<string, unknown>
      }
    }
  }
}

async function callGetDiagnostics(): Promise<DiagnosticsSnapshot | null> {
  try {
    const fn = window?.go?.main?.App?.GetDiagnostics as
      | (() => Promise<DiagnosticsSnapshot>)
      | undefined
    if (!fn) return null
    return await fn()
  } catch {
    return null
  }
}

// ---------------------------------------------------------------------------
// Display helpers
// ---------------------------------------------------------------------------

function formatMB(mb: number): string {
  if (mb >= 1024) return `${(mb / 1024).toFixed(2)} GB`
  return `${mb} MB`
}

function daemonDisplay(d: DaemonStatus): string {
  const state = d.running ? 'running' : 'stopped'
  const restarts = `restarts: ${d.restarts}`
  return d.lastError
    ? `${state} (${restarts}) — ${d.lastError}`
    : `${state} (${restarts})`
}

function llmDisplay(l: LLMChainStatus): string {
  return l.lastError ? `${l.active} — ${l.lastError}` : l.active
}

function modelsDisplay(models: ModelStatus[]): string {
  if (!models.length) return 'no models configured'
  return models
    .map((m) => {
      const tick = m.loaded ? '✓' : '✗'
      const size = m.sizeMb ? ` (${formatMB(m.sizeMb)})` : ''
      return `${m.name} ${tick}${size}`
    })
    .join(', ')
}

function ollamaDisplay(running: boolean): string {
  return running ? 'running on localhost:11434' : 'not running'
}

function micDisplay(s: string): string {
  return s || 'unknown'
}

function mobileDisplay(m: MobileAPIStatus): string {
  return `port ${m.port}, token ${m.tokenPreview}`
}

function diskDisplay(d: DiskUsageStatus): string {
  return `${d.jarvisHome} (${formatMB(d.sizeMb)})`
}

// ---------------------------------------------------------------------------
// Clipboard helper
// ---------------------------------------------------------------------------

async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through
  }
  return false
}

// ---------------------------------------------------------------------------
// Row component — label + value + copy button.
// ---------------------------------------------------------------------------

interface DiagnosticRowAction {
  label: string
  onClick: () => void
  /** Optional aria-label override. Defaults to `label`. */
  ariaLabel?: string
}

interface DiagnosticRowProps {
  label: string
  value: string
  raw: string
  ok?: boolean
  onCopy: (raw: string, label: string) => void
  /** Optional inline action button rendered before the Copy button. */
  action?: DiagnosticRowAction
}

function DiagnosticRow({
  label,
  value,
  raw,
  ok,
  onCopy,
  action,
}: DiagnosticRowProps): React.ReactElement {
  const dotColor =
    ok === undefined
      ? '#8ba4b8'
      : ok
        ? '#00ff88'
        : '#ff4757'
  return (
    <div
      className="flex items-center gap-3 py-2 border-b border-[#1a2332] last:border-b-0"
      role="row"
    >
      <span
        aria-hidden="true"
        className="inline-block w-2 h-2 rounded-full flex-shrink-0"
        style={{ background: dotColor }}
      />
      <span className="text-xs text-[#8ba4b8] w-32 flex-shrink-0">{label}</span>
      <span className="text-sm text-[#e8f4ff] font-mono flex-1 truncate">{value}</span>
      {action && (
        <button
          type="button"
          onClick={action.onClick}
          className="text-xs px-2 py-1 rounded text-[#e8f4ff] hover:text-white transition-colors"
          style={{
            background: 'rgba(127, 29, 29, 0.6)',          // red-900/60
            border: '1px solid rgba(220, 38, 38, 0.6)',    // red-600/60
          }}
          aria-label={action.ariaLabel ?? action.label}
          title={action.label}
        >
          {action.label}
        </button>
      )}
      <button
        type="button"
        onClick={() => onCopy(raw, label)}
        className="text-xs px-2 py-1 rounded text-[#8ba4b8] hover:text-[#00e5ff] transition-colors"
        style={{
          background: 'rgba(10, 14, 26, 0.8)',
          border: '1px solid rgba(0, 229, 255, 0.15)',
        }}
        aria-label={`Copy ${label} value`}
        title="Copy raw value"
      >
        Copy
      </button>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Voice Pipeline block — live `pipeline_status` from the daemon (v0.1.5).
//
// Schema and refresh contract live in `lib/use-pipeline-status.ts`. We
// render a small 4-line readout (LLM / STT / TTS / Wake) plus an "X s ago"
// stamp in the header. If no event has arrived yet, we surface a dim empty
// state with a "Request now" button that re-asks the daemon.
// ---------------------------------------------------------------------------

function formatSecondsAgo(receivedAt: number, now: number): string {
  if (receivedAt === 0) return ''
  const secs = Math.max(0, Math.floor((now - receivedAt) / 1000))
  if (secs < 1) return 'just now'
  if (secs === 1) return '1s ago'
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins === 1) return '1m ago'
  return `${mins}m ago`
}

function VoicePipelineRow(): React.ReactElement {
  const { status, receivedAt, refresh } = usePipelineStatus()

  // Tick once a second so the "X s ago" stamp stays fresh without a global
  // store. Costs ~1 setState/s while the Diagnostics tab is mounted.
  const [now, setNow] = useState<number>(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  const stamp = formatSecondsAgo(receivedAt, now)

  return (
    <section
      className="rounded border border-[#1a2332] bg-[rgba(10,14,26,0.6)] p-3"
      aria-label="Voice Pipeline"
    >
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-xs font-mono tracking-[0.2em] text-[#00e5ff]">
          <span aria-hidden="true">▸ </span>VOICE PIPELINE
        </h3>
        {status && stamp && (
          <span className="text-[10px] font-mono text-[#4a6278]">
            · last updated {stamp}
          </span>
        )}
      </div>

      {status ? (
        <dl className="grid grid-cols-[64px_1fr] gap-y-1 gap-x-3 text-sm">
          <dt className="text-xs font-mono uppercase text-[#8ba4b8]">LLM</dt>
          <dd className="font-mono text-[#e8f4ff] truncate">
            {status.llm.provider} · {status.llm.model}
            {status.llm.source === 'user-pick' && (
              <span className="ml-2 text-[#00e5ff]" aria-label="user pick">
                ◆ user-pick
              </span>
            )}
          </dd>

          <dt className="text-xs font-mono uppercase text-[#8ba4b8]">STT</dt>
          <dd className="font-mono text-[#e8f4ff] truncate">
            {status.stt.model}
          </dd>

          <dt className="text-xs font-mono uppercase text-[#8ba4b8]">TTS</dt>
          <dd className="font-mono text-[#e8f4ff] truncate">
            {status.tts.provider} · {status.tts.voice}
          </dd>

          <dt className="text-xs font-mono uppercase text-[#8ba4b8]">Wake</dt>
          <dd className="font-mono text-[#e8f4ff] truncate">
            {status.wake_word.enabled ? 'ENABLED' : 'DISABLED'}
            {' · sensitivity '}
            {status.wake_word.sensitivity.toFixed(2)}
          </dd>
        </dl>
      ) : (
        <div className="flex items-center gap-3">
          <span className="text-xs font-mono italic text-[#4a6278]">
            — no pipeline_status received from daemon yet —
          </span>
          <button
            type="button"
            onClick={refresh}
            className="text-xs px-2 py-1 rounded text-[#8ba4b8] hover:text-[#00e5ff] transition-colors"
            style={{
              background: 'rgba(10, 14, 26, 0.8)',
              border: '1px solid rgba(0, 229, 255, 0.15)',
            }}
            aria-label="Request pipeline status now"
          >
            Request now
          </button>
        </div>
      )}
    </section>
  )
}

// ---------------------------------------------------------------------------
// Main panel
// ---------------------------------------------------------------------------

export function DiagnosticsPanel({ activeTab }: DiagnosticsPanelProps): React.ReactElement {
  const [snapshot, setSnapshot] = useState<DiagnosticsSnapshot | null>(null)
  const [lastFetch, setLastFetch] = useState<Date | null>(null)
  const [copiedLabel, setCopiedLabel] = useState<string | null>(null)
  const mountedRef = useRef(true)

  // Polling loop — `setInterval(<fn>, 2000)` per TASK-022 spec. The fetcher
  // is also invoked synchronously on mount so the panel paints something
  // before the first interval tick fires (2s feels laggy without it).
  useEffect(() => {
    mountedRef.current = true
    let cancelled = false

    const fetchOnce = async (): Promise<void> => {
      const next = await callGetDiagnostics()
      if (cancelled || !mountedRef.current) return
      setSnapshot(next)
      setLastFetch(new Date())
    }

    void fetchOnce()
    const intervalId = setInterval(() => {
      void fetchOnce()
    }, 2000)

    return () => {
      cancelled = true
      mountedRef.current = false
      clearInterval(intervalId)
    }
  }, [])

  // Copy helper that also surfaces a transient "Copied X" banner.
  const handleCopy = useCallback((raw: string, label: string) => {
    void copyText(raw).then((ok) => {
      if (!ok) return
      setCopiedLabel(label)
      window.setTimeout(() => setCopiedLabel(null), 1500)
    })
  }, [])

  // Master "Copy diagnostics" — produces a markdown bug-report snippet.
  const handleCopyAll = useCallback(() => {
    if (!snapshot) return
    const now = new Date()
    const stamp = now.toISOString().replace('T', ' ').replace(/\.\d+Z$/, '')
    const lines: string[] = [
      `# Jarvis Diagnostics — ${stamp}`,
      '',
      `- Daemon: ${snapshot.daemon.running ? '✓ running' : '✗ stopped'} (restarts: ${snapshot.daemon.restarts})`,
      `- Mic permission: ${micDisplay(snapshot.micPermission)}`,
      `- Mobile API: ${mobileDisplay(snapshot.mobileApi)}`,
      `- LLM chain: ${llmDisplay(snapshot.llmChain)}`,
      `- Models: ${modelsDisplay(snapshot.models)}`,
      `- Ollama: ${ollamaDisplay(snapshot.ollamaRunning)}`,
      `- Disk: ${diskDisplay(snapshot.diskUsage)}`,
    ]
    const snippet = lines.join('\n')
    void copyText(snippet).then((ok) => {
      if (!ok) return
      setCopiedLabel('Diagnostics')
      window.setTimeout(() => setCopiedLabel(null), 1500)
    })
  }, [snapshot])

  // Pre-compute display + raw text for each row. Falls back to placeholders
  // when the snapshot hasn't loaded yet (first paint).
  const rows = (() => {
    if (!snapshot) {
      const placeholder = 'loading…'
      return {
        daemon: { display: placeholder, raw: '', ok: undefined as boolean | undefined },
        mic: { display: placeholder, raw: '', ok: undefined },
        mobileApi: { display: placeholder, raw: '' },
        llm: { display: placeholder, raw: '', ok: undefined },
        models: { display: placeholder, raw: '', ok: undefined },
        ollama: { display: placeholder, raw: '', ok: undefined },
        disk: { display: placeholder, raw: '' },
      }
    }
    const allModelsLoaded = snapshot.models.every((m) => m.loaded)
    const llmOk = snapshot.llmChain.active !== 'none' && !snapshot.llmChain.lastError
    return {
      daemon: {
        display: daemonDisplay(snapshot.daemon),
        raw: `running=${snapshot.daemon.running} restarts=${snapshot.daemon.restarts}${snapshot.daemon.lastError ? ` lastError=${snapshot.daemon.lastError}` : ''}`,
        ok: snapshot.daemon.running,
      },
      mic: {
        display: micDisplay(snapshot.micPermission),
        raw: snapshot.micPermission,
        ok: snapshot.micPermission === 'granted',
      },
      mobileApi: {
        display: mobileDisplay(snapshot.mobileApi),
        raw: `port=${snapshot.mobileApi.port} token=${snapshot.mobileApi.tokenPreview}`,
      },
      llm: {
        display: llmDisplay(snapshot.llmChain),
        raw: `active=${snapshot.llmChain.active}${snapshot.llmChain.lastError ? ` lastError=${snapshot.llmChain.lastError}` : ''}`,
        ok: llmOk,
      },
      models: {
        display: modelsDisplay(snapshot.models),
        raw: snapshot.models
          .map((m) => `${m.name}=${m.loaded ? 'loaded' : 'missing'}${m.sizeMb ? ` (${m.sizeMb}MB)` : ''} path=${m.path}`)
          .join(' | '),
        ok: snapshot.models.length > 0 && allModelsLoaded,
      },
      ollama: {
        display: ollamaDisplay(snapshot.ollamaRunning),
        raw: String(snapshot.ollamaRunning),
        ok: snapshot.ollamaRunning,
      },
      disk: {
        display: diskDisplay(snapshot.diskUsage),
        raw: `${snapshot.diskUsage.jarvisHome} ${snapshot.diskUsage.sizeMb}MB`,
      },
    }
  })()

  return (
    <div
      role="tabpanel"
      id="settings-tab-panel-diagnostics"
      aria-labelledby="settings-tab-diagnostics"
      hidden={activeTab !== 'diagnostics'}
      className="space-y-6"
    >
      {/* TASK-022 marker — kept for the SettingsView.test.tsx regression
          check that asserts the headline TASK IDs appear in the combined
          panel source. */}
      <div className="hidden" aria-hidden="true" data-task="TASK-022" />

      <section className="holo-panel p-4">
        <div className="flex items-center justify-between mb-3">
          <div>
            <h2 className="text-sm font-semibold text-[#00e5ff]">System Health</h2>
            <p className="text-xs text-[#8ba4b8]">
              Live status (polls every 2s). Click Copy to grab a single value
              or use the master button below for a bug-report snippet.
            </p>
          </div>
          {lastFetch && (
            <div className="text-[10px] text-[#4a6278] font-mono">
              updated {lastFetch.toLocaleTimeString()}
            </div>
          )}
        </div>

        <div className="rounded border border-[#1a2332] bg-[rgba(10,14,26,0.6)] px-3" role="table">
          <DiagnosticRow
            label="Daemon"
            value={rows.daemon.display}
            raw={rows.daemon.raw}
            ok={rows.daemon.ok}
            onCopy={handleCopy}
          />
          {/* TASK-026: when the binding reports `denied` or `restricted`, surface
              an inline "Open System Settings" button that deep-links into
              System Settings → Privacy → Microphone via the
              `x-apple.systempreferences:` URL scheme. */}
          <DiagnosticRow
            label="Mic permission"
            value={rows.mic.display}
            raw={rows.mic.raw}
            ok={rows.mic.ok}
            onCopy={handleCopy}
            action={
              snapshot && (snapshot.micPermission === 'denied' || snapshot.micPermission === 'restricted')
                ? {
                    label: 'Open System Settings',
                    ariaLabel: 'Open System Settings to grant microphone access',
                    onClick: () => BrowserOpenURL(MIC_SETTINGS_URL),
                  }
                : undefined
            }
          />
          <DiagnosticRow
            label="Mobile API"
            value={rows.mobileApi.display}
            raw={rows.mobileApi.raw}
            onCopy={handleCopy}
          />
          <DiagnosticRow
            label="LLM chain"
            value={rows.llm.display}
            raw={rows.llm.raw}
            ok={rows.llm.ok}
            onCopy={handleCopy}
          />
          <DiagnosticRow
            label="Models"
            value={rows.models.display}
            raw={rows.models.raw}
            ok={rows.models.ok}
            onCopy={handleCopy}
          />
          <DiagnosticRow
            label="Ollama"
            value={rows.ollama.display}
            raw={rows.ollama.raw}
            ok={rows.ollama.ok}
            onCopy={handleCopy}
          />
          <DiagnosticRow
            label="Disk"
            value={rows.disk.display}
            raw={rows.disk.raw}
            onCopy={handleCopy}
          />
        </div>

        <div className="flex items-center gap-3 mt-4">
          <button
            type="button"
            onClick={handleCopyAll}
            disabled={!snapshot}
            className="text-xs px-3 py-1.5 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-50 transition-colors"
          >
            Copy diagnostics
          </button>
          {copiedLabel && (
            <span className="text-xs text-[#00ff88]">Copied {copiedLabel}</span>
          )}
          {!snapshot && (
            <span className="text-xs text-[#4a6278] italic">
              Waiting for first GetDiagnostics snapshot…
            </span>
          )}
        </div>
      </section>

      {/* Voice Pipeline — live `pipeline_status` from the Python daemon
          (v0.1.5). Subscribes to the same `'jarvis'` channel as the HUD
          orb labels via `usePipelineStatus()`. */}
      <VoicePipelineRow />
    </div>
  )
}
