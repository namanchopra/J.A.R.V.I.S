import { useCallback, useEffect, useState } from 'react'
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

// ---------------------------------------------------------------------------
// SettingsView — thin 5-tab shell (TASK-016 / Wave 3 prep).
//
// Tabs: Connections | Voice | Behavior | Diagnostics | Advanced. This file
// owns activeTab state, loads/saves Config, and renders all 5 panels at
// once (visibility-toggled via `hidden`) so controlled-input state survives
// tab switches. Field distribution lives in docs/settings-ui-gap.md and in
// the per-panel file headers; Wave 3 (TASK-017 … TASK-023) populates the
// `[TASK-NNN]` markers inside each panel.
// ---------------------------------------------------------------------------

export function SettingsView(): React.ReactElement {
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

  useEffect(() => {
    GetConfig().then(setCfg).catch((err) => console.warn('Failed to load config:', err))
    GetAvailableTerminals().then((t) => setTerminals(t ?? [])).catch((err) => console.warn('Failed to load terminals:', err))
    GetMobileConnectionInfo()
      .then(setMobileInfo)
      .catch((err) => console.warn('Failed to load mobile info:', err))
      .finally(() => setMobileLoading(false))
  }, [])

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
      <div className="flex-1 flex items-center justify-center text-[#00e5ff] font-mono animate-pulse">
        Loading...
      </div>
    )
  }

  // Shared base props every panel receives. Panel-specific extras are
  // appended at each call site below.
  const baseProps = {
    cfg,
    setCfg,
    onSave: handleSave,
    saving,
    activeTab,
  }

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <header className="h-12 flex-shrink-0 flex items-center px-5 border-b border-[rgba(0,229,255,0.15)] bg-[#111827]">
        <h1 className="text-base font-bold tracking-wide text-[#00e5ff]">Settings</h1>
      </header>

      <SettingsTabs active={activeTab} onChange={setActiveTab} />

      <div className="flex-1 overflow-y-auto">
        <div className="max-w-2xl mx-auto p-6 space-y-6">
          {/* Message banner */}
          {message && (
            <div
              className={`text-sm px-3 py-2 rounded glow-border ${message.type === 'success' ? 'bg-[#00ff88]/10 text-[#00ff88]' : 'bg-[#ff4757]/10 text-[#ff4757]'}`}
            >
              {message.text}
            </div>
          )}

          {/* All 5 panels rendered always; inactive ones hidden so form
              state is preserved across tab switches (no unmount). */}
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

          {/* Save button (shared across all tabs) */}
          <div className="flex justify-end pb-6">
            <button
              type="button"
              onClick={() => void handleSave()}
              disabled={saving}
              className="px-5 py-2 text-sm font-medium rounded-lg text-white disabled:opacity-50 transition-all"
              style={{
                background: 'linear-gradient(135deg, #0d9488, #00e5ff)',
                boxShadow: '0 0 12px rgba(0, 229, 255, 0.3)',
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.boxShadow = '0 0 20px rgba(0, 229, 255, 0.5)'
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.boxShadow = '0 0 12px rgba(0, 229, 255, 0.3)'
              }}
            >
              {saving ? 'Saving...' : 'Save Settings'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
