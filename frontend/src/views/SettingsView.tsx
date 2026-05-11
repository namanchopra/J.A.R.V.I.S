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

  const showMsg = (text: string, type: 'success' | 'error') => {
    setMessage({ text, type })
    setTimeout(() => setMessage(null), 3000)
  }

  const handleSave = useCallback(async () => {
    if (!cfg) return
    setSaving(true)
    try {
      await SaveConfig(cfg)
      showMsg('Settings saved', 'success')
    } catch (err) {
      showMsg(`Failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
    }
    setSaving(false)
  }, [cfg])

  const handleSync = useCallback(async () => {
    setSyncing(true)
    try {
      const count = await SyncDotClaude()
      showMsg(`Synced .claude to ${count} workspace(s)`, 'success')
    } catch (err) {
      showMsg(`Sync failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
    }
    setSyncing(false)
  }, [])

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
  }, [])

  const copyToClipboard = useCallback(async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text)
      showMsg(`${label} copied to clipboard`, 'success')
    } catch {
      showMsg('Failed to copy to clipboard', 'error')
    }
  }, [])

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
        className="relative z-10 flex-shrink-0 flex items-center justify-between border-t border-[rgba(0,229,255,0.18)]"
        style={{
          padding: '14px 22px',
          background:
            'linear-gradient(0deg, rgba(2,12,10,0.95), rgba(2,12,10,0.75))',
          backdropFilter: 'blur(8px)',
        }}
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
  )
}
