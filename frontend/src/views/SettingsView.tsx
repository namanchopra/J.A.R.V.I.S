import { useCallback, useEffect, useRef, useState } from 'react'
import {
  GetConfig,
  SaveConfig,
  SyncDotClaude,
  GetAvailableTerminals,
  GetMobileConnectionInfo,
  RegenerateMobileToken,
} from '../../wailsjs/go/main/App'
import { config, main } from '../../wailsjs/go/models'
import { SettingsTabs, type SettingsTabId } from './settings/SettingsTabs'
import { ConnectionsPanel } from './settings/ConnectionsPanel'
import { VoicePanel } from './settings/VoicePanel'
import { BehaviorPanel } from './settings/BehaviorPanel'
import { DiagnosticsPanel } from './settings/DiagnosticsPanel'
import { AdvancedPanel } from './settings/AdvancedPanel'

interface SettingsViewProps {
  /** Optional close handler -- if passed, renders an X button in the header. */
  onClose?: () => void
}

// ---------------------------------------------------------------------------
// SaveConfig binding shape (v0.1.2).
//
// The Go agent's parallel track changes SaveConfig's return type from
// `Promise<void>` to `Promise<{ daemonRestartNeeded: boolean }>`. The
// generated `wailsjs/go/main/App.d.ts` in this sandbox has NOT been
// regenerated yet, so the declared return is still `void`. We treat the
// response as opaque-but-narrowable: cast through unknown and probe for the
// flag. Old bindings ⇒ result is undefined ⇒ needs defaults to false. New
// bindings ⇒ flag flows through and triggers the banner.
// ---------------------------------------------------------------------------
interface SaveConfigResult {
  daemonRestartNeeded?: boolean
}

// Bindings shim for RestartJarvis — same rationale as the SaveConfig cast.
// The Go agent's RestartJarvis() binding may not be present at compile time
// in this sandbox; we resolve it at call time via `window.go.main.App`.
interface RestartCapableBindings {
  RestartJarvis?: () => Promise<void>
}

function restartBindings(): RestartCapableBindings | null {
  const w = window as unknown as {
    go?: { main?: { App?: RestartCapableBindings } }
  }
  return w.go?.main?.App ?? null
}

export function SettingsView({ onClose }: SettingsViewProps = {}): React.ReactElement {
  const [cfg, setCfg] = useState<config.Config | null>(null)
  const [saving, setSaving] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [message, setMessage] = useState<{ text: string; type: 'success' | 'error' } | null>(null)
  const [terminals, setTerminals] = useState<string[]>([])
  const [mobileInfo, setMobileInfo] = useState<main.MobileConnectionInfo | null>(null)
  const [mobileLoading, setMobileLoading] = useState(true)
  const [regenerating, setRegenerating] = useState(false)
  const [tokenRevealed, setTokenRevealed] = useState(false)
  const [activeTab, setActiveTab] = useState<SettingsTabId>('connections')

  // v0.1.2: daemon-restart-required banner state. Set true when SaveConfig
  // reports daemonRestartNeeded; cleared by either the Apply now action
  // (which restarts) or Apply later (which dismisses and waits for the
  // next manual quit/relaunch). Independent from the 3s toast so the
  // banner persists across user attention spans.
  const [restartNeeded, setRestartNeeded] = useState<boolean>(false)
  const [restarting, setRestarting] = useState<boolean>(false)

  // Container ref so the range-slider fill effect only watches sliders inside settings.
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    GetConfig().then(setCfg).catch((err) => console.warn('Failed to load config:', err))
    GetAvailableTerminals().then((t) => setTerminals(t ?? [])).catch((err) => console.warn('Failed to load terminals:', err))
    GetMobileConnectionInfo()
      .then(setMobileInfo)
      .catch((err) => console.warn('Failed to load mobile info:', err))
      .finally(() => setMobileLoading(false))
  }, [])

  // Paint range-slider fill: CSS gradient reads --range-pct; we update it
  // on every input event for every range slider inside the settings root.
  useEffect(() => {
    const root = rootRef.current
    if (!root) return
    const paint = (input: HTMLInputElement): void => {
      const min = Number(input.min) || 0
      const max = Number(input.max) || 100
      const val = Number(input.value)
      const pct = max === min ? 0 : ((val - min) / (max - min)) * 100
      input.style.setProperty('--range-pct', `${pct}%`)
    }
    const paintAll = (): void => {
      root.querySelectorAll<HTMLInputElement>('input[type="range"]').forEach(paint)
    }
    paintAll()
    const onInput = (e: Event): void => {
      const t = e.target as HTMLElement | null
      if (t instanceof HTMLInputElement && t.type === 'range') paint(t)
    }
    root.addEventListener('input', onInput)
    // Repaint on tab switch (DOM swap may have happened) — short timeout
    // batches across React commit + style application.
    const id = window.setTimeout(paintAll, 50)
    return () => {
      root.removeEventListener('input', onInput)
      window.clearTimeout(id)
    }
  }, [cfg, activeTab])

  const showMsg = useCallback((text: string, type: 'success' | 'error') => {
    setMessage({ text, type })
    setTimeout(() => setMessage(null), 3000)
  }, [])

  const handleSave = useCallback(async () => {
    if (!cfg) return
    setSaving(true)
    try {
      // The Go agent's SaveConfig now returns { daemonRestartNeeded }. We
      // narrow through unknown so the old `void` binding doesn't trip up
      // strict TS while the regen catches up.
      const raw = (await SaveConfig(cfg)) as unknown
      const result = (raw ?? {}) as SaveConfigResult
      const needs = result.daemonRestartNeeded ?? false
      if (needs) {
        setRestartNeeded(true)
      }
      showMsg('Settings saved', 'success')
    } catch (err) {
      showMsg(`Failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
    }
    setSaving(false)
  }, [cfg, showMsg])

  const handleApplyNow = useCallback(async () => {
    setRestarting(true)
    try {
      const app = restartBindings()
      if (typeof app?.RestartJarvis === 'function') {
        await app.RestartJarvis()
        // After RestartJarvis resolves the daemon is back up. Dismiss the
        // banner and surface a cyan "Daemon reconnected" toast for ~3s —
        // the existing toast system handles the timing.
        setRestartNeeded(false)
        showMsg('Daemon reconnected', 'success')
      } else {
        // Bindings not regenerated yet — fall back to an instructional toast
        // so users aren't stuck staring at a non-functional button.
        setRestartNeeded(false)
        showMsg('Restart Jarvis from the menu bar to apply changes', 'success')
      }
    } catch (err) {
      showMsg(`Restart failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
    } finally {
      setRestarting(false)
    }
  }, [showMsg])

  const handleApplyLater = useCallback(() => {
    // Changes are already persisted; just dismiss the banner. The python
    // daemon picks them up on its next load_config() (i.e. next manual
    // restart).
    setRestartNeeded(false)
  }, [])

  const handleSync = useCallback(async () => {
    setSyncing(true)
    try {
      const count = await SyncDotClaude()
      showMsg(`Synced .claude to ${count} workspace(s)`, 'success')
    } catch (err) {
      showMsg(`Sync failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
    }
    setSyncing(false)
  }, [showMsg])

  const handleRegenerateToken = useCallback(async () => {
    setRegenerating(true)
    try {
      await RegenerateMobileToken()
      const info = await GetMobileConnectionInfo()
      setMobileInfo(info)
      setTokenRevealed(false)
      showMsg('Mobile token regenerated', 'success')
    } catch (err) {
      showMsg(`Regenerate failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
    }
    setRegenerating(false)
  }, [showMsg])

  const copyToClipboard = useCallback(async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text)
      showMsg(`${label} copied to clipboard`, 'success')
    } catch {
      showMsg('Failed to copy to clipboard', 'error')
    }
  }, [showMsg])

  if (!cfg) {
    return (
      <div className="settings-bg flex-1 flex items-center justify-center font-mono text-[var(--accent-blue)] tracking-[0.2em] text-xs">
        <span className="animate-pulse">▸ LOADING CONFIG…</span>
      </div>
    )
  }

  const baseProps = {
    cfg,
    setCfg,
    onSave: handleSave,
    saving,
    activeTab,
  }

  return (
    <div ref={rootRef} className="settings-bg flex-1 flex flex-col min-h-0 relative">
      {/* ---- HEADER ---- */}
      <header
        className="relative z-10 flex-shrink-0 flex items-center justify-between border-b border-[rgba(0,229,255,0.18)]"
        style={{
          padding: '14px 22px',
          background: 'linear-gradient(180deg, rgba(0,229,255,0.06), transparent)',
        }}
      >
        <div className="flex items-baseline gap-3">
          <span
            className="text-[10px] tracking-[0.3em] text-[rgba(0,229,255,0.45)]"
            style={{ fontFamily: "'SF Mono', 'Menlo', monospace" }}
          >
            J.A.R.V.I.S //
          </span>
          <h1
            className="text-base font-bold tracking-[0.25em] uppercase text-[var(--accent-blue)]"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              textShadow: '0 0 8px rgba(0, 229, 255, 0.4)',
            }}
          >
            System Config
          </h1>
          <span
            className="text-[10px] tracking-[0.25em] text-[rgba(0,229,255,0.35)]"
            style={{ fontFamily: "'SF Mono', 'Menlo', monospace" }}
          >
            v2.0
          </span>
        </div>

        {onClose && (
          <button
            type="button"
            onClick={onClose}
            aria-label="Close settings"
            title="Close (Esc)"
            className="jarvis-iconbtn"
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        )}
      </header>

      <div className="relative z-10 flex-shrink-0">
        <SettingsTabs active={activeTab} onChange={setActiveTab} />
      </div>

      {/* ---- SCROLLABLE BODY ---- */}
      <div className="relative z-10 flex-1 min-h-0 overflow-y-auto">
        <div className="max-w-3xl mx-auto px-7 py-8 space-y-6 pb-32">
          {/* Toast banner */}
          {message && (
            <div
              role="status"
              className="fade-in-up text-xs font-mono px-4 py-3 rounded-sm flex items-center gap-3"
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                letterSpacing: '0.05em',
                background: message.type === 'success'
                  ? 'rgba(0, 255, 136, 0.08)'
                  : 'rgba(255, 71, 87, 0.08)',
                border: `1px solid ${message.type === 'success' ? 'rgba(0,255,136,0.4)' : 'rgba(255,71,87,0.45)'}`,
                color: message.type === 'success' ? '#00ff88' : '#ff6b78',
                boxShadow: message.type === 'success'
                  ? '0 0 14px rgba(0, 255, 136, 0.15)'
                  : '0 0 14px rgba(255, 71, 87, 0.18)',
              }}
            >
              <span style={{ fontWeight: 700 }}>
                {message.type === 'success' ? '◉ OK ::' : '✕ ERR ::'}
              </span>
              <span style={{ flex: 1 }}>{message.text}</span>
            </div>
          )}

          <ConnectionsPanel {...baseProps} syncing={syncing} onSync={handleSync} />
          <VoicePanel {...baseProps} />
          <BehaviorPanel {...baseProps} terminals={terminals} />
          <DiagnosticsPanel {...baseProps} />
          <AdvancedPanel
            {...baseProps}
            mobileInfo={mobileInfo}
            mobileLoading={mobileLoading}
            regenerating={regenerating}
            onRegenerateToken={handleRegenerateToken}
            tokenRevealed={tokenRevealed}
            setTokenRevealed={setTokenRevealed}
            copyToClipboard={copyToClipboard}
          />
        </div>
      </div>

      {/* ---- STICKY SAVE BAR ---- */}
      <div
        className="relative z-10 flex-shrink-0 border-t border-[rgba(0,229,255,0.18)]"
        style={{
          background:
            'linear-gradient(0deg, rgba(2,12,10,0.95), rgba(2,12,10,0.75))',
          backdropFilter: 'blur(8px)',
        }}
      >
        {/* v0.1.2: daemon-restart banner. Mounts above the save row so the
            user sees it adjacent to the save action that produced it.
            Distinct amber palette (not cyan) so the user notices it. */}
        {restartNeeded && (
          <div
            role="alert"
            aria-live="polite"
            data-testid="daemon-restart-banner"
            className="fade-in-up flex items-center gap-3 px-5 py-3 border-b"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.05em',
              background: 'rgba(255, 184, 0, 0.08)',
              borderBottomColor: 'rgba(255, 184, 0, 0.45)',
              color: 'var(--accent-amber)',
              boxShadow: '0 0 14px rgba(255, 184, 0, 0.18)',
            }}
          >
            <span
              style={{
                fontWeight: 700,
                fontSize: '11px',
                whiteSpace: 'nowrap',
                color: 'var(--accent-amber)',
              }}
            >
              ▸ DAEMON RESTART REQUIRED
            </span>
            <span
              style={{
                flex: 1,
                fontSize: '11px',
                color: 'rgba(255, 207, 100, 0.85)',
                letterSpacing: '0.04em',
              }}
            >
              Some changes need a daemon restart to take effect. Jarvis will go offline for ~3 seconds.
            </span>
            <button
              type="button"
              onClick={() => void handleApplyNow()}
              disabled={restarting}
              data-testid="daemon-restart-apply-now"
              className="jarvis-cta"
              style={{ padding: '6px 14px', fontSize: '11px' }}
            >
              {restarting ? (
                <>
                  <span style={{ animation: 'pulse-glow 1.2s ease-in-out infinite', display: 'inline-block' }}>
                    ◌
                  </span>
                  <span>▸ RESTARTING…</span>
                </>
              ) : (
                <span>Apply now</span>
              )}
            </button>
            <button
              type="button"
              onClick={handleApplyLater}
              disabled={restarting}
              data-testid="daemon-restart-apply-later"
              className="text-[11px] px-3 py-1.5 rounded border transition-colors disabled:opacity-40"
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                letterSpacing: '0.05em',
                borderColor: 'rgba(255, 184, 0, 0.4)',
                color: 'rgba(255, 207, 100, 0.85)',
                background: 'transparent',
              }}
            >
              Apply later
            </button>
          </div>
        )}

        <div
          className="flex items-center justify-between"
          style={{ padding: '14px 22px' }}
        >
          <span
            className="text-[10px] tracking-[0.2em] uppercase"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              color: 'rgba(207, 231, 255, 0.4)',
            }}
          >
            ◇ Stored at <span style={{ color: 'rgba(0,229,255,0.7)' }}>~/.jarvis/config.json</span>
          </span>
          <button
            type="button"
            onClick={() => void handleSave()}
            disabled={saving}
            className="jarvis-cta"
          >
            {saving ? (
              <>
                <span style={{ animation: 'pulse-glow 1.2s ease-in-out infinite', display: 'inline-block' }}>
                  ◌
                </span>
                <span>Saving…</span>
              </>
            ) : (
              <>
                <span>◆</span>
                <span>Save Config</span>
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  )
}
