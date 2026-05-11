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
import { JarvisSettings } from '../components/JarvisSettings'
import { ApprovalRulesSettings } from '../components/ApprovalRulesSettings'
import { SettingsTabs, type SettingsTabId } from './settings/SettingsTabs'

// ---------------------------------------------------------------------------
// SettingsView — 5-tab IA shell (TASK-016).
//
// Tabs: Connections | Voice | Behavior | Diagnostics | Advanced.
//
// Form state retention across tab switches is achieved by rendering all 5
// panels at all times and toggling visibility with `hidden`. Controlled
// inputs keep their values; no remount happens on switch. Downstream tasks
// (TASKs 017-024) populate the placeholder slots in each tab.
//
// Field distribution (see docs/settings-ui-gap.md for the canonical mapping):
//   Connections:  jarvisAPIKey (via JarvisSettings), defaultAgent,
//                 defaultCommand, dotClaudeSourcePath, [TASK-017/018 keys]
//   Voice:        jarvisVoice + jarvisVerbosity + jarvisAmbientEnabled (via
//                 JarvisSettings), [TASK-018/019/020 TTS/STT/wake]
//   Behavior:     preferredTerminal, projectRootPaths, scanIntervalSeconds,
//                 notifications, ciWatchEnabled, ciProvider,
//                 ApprovalRulesSettings, [TASK-020/021 transport/mic]
//   Diagnostics:  [TASK-022 — placeholder for now]
//   Advanced:     mobile API token cluster, [TASK-023 import/export/reset]
//
// Notes for downstream agents:
//   - JarvisSettings is mounted in Voice. It currently hosts jarvisAPIKey
//     too. TASK-017 should pull jarvisAPIKey out of JarvisSettings into the
//     Connections tab; until then, the key still saves via this component's
//     internal state path so no field is lost.
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

  // -----------------------------------------------------------------------
  // Per-tab panel renderers. We render ALL panels always (with `hidden`
  // toggled) so controlled-input state survives tab switches.
  // -----------------------------------------------------------------------

  const connectionsPanel = (
    <div
      role="tabpanel"
      id="settings-tab-panel-connections"
      aria-labelledby="settings-tab-connections"
      hidden={activeTab !== 'connections'}
      className="space-y-6"
    >
      {/* Placeholder: API keys (OpenRouter, Google, Anthropic, Cartesia,
          ElevenLabs, Picovoice) with Validate buttons live here. */}
      <div className="text-xs text-[#4a6278] italic">
        API key fields populated by TASK-017. Provider availability dropdowns populated by TASK-018.
      </div>

      {/* Default Agent */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Default Agent</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          The default AI agent for new sessions and divide-and-conquer operations.
        </p>
        <select
          value={cfg.defaultAgent}
          onChange={(e) => setCfg({ ...cfg, defaultAgent: e.target.value })}
          className="sci-fi text-sm"
        >
          <option value="claude-code">Claude Code</option>
          <option value="kiro">Kiro CLI</option>
          <option value="gemini">Gemini CLI</option>
          <option value="codex">Codex CLI</option>
          <option value="aider">Aider</option>
        </select>
      </section>

      {/* Launch Defaults (default command) */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Launch Defaults</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Default command and arguments used when launching new agent sessions.
        </p>
        <div>
          <label htmlFor="default-command" className="block text-sm text-[#8ba4b8] mb-1">
            Default command
          </label>
          <input
            id="default-command"
            type="text"
            value={cfg.defaultCommand}
            onChange={(e) => setCfg({ ...cfg, defaultCommand: e.target.value })}
            placeholder="claude"
            className="sci-fi w-full text-sm font-mono"
          />
        </div>
      </section>

      {/* .claude Source Path
          (TASK-023 will additionally add a "Browse..." button in Advanced;
           for now the field stays here because it's the closest semantic
           home in the Connections-style "what does Jarvis hook up to"
           category.) */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">.claude Source Path</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Path to your dotAiAgent repo (or any folder containing a .claude directory with agents,
          skills, etc). This gets copied into every workspace you create.
        </p>
        <input
          type="text"
          value={cfg.dotClaudeSourcePath}
          onChange={(e) => setCfg({ ...cfg, dotClaudeSourcePath: e.target.value })}
          placeholder="Auto-detect (leave empty) or /path/to/dotAiAgent"
          className="sci-fi w-full text-sm font-mono"
        />
        <div className="flex items-center gap-2 mt-2">
          <button
            type="button"
            onClick={() => void handleSync()}
            disabled={syncing}
            className="text-xs px-3 py-1.5 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-50 transition-colors"
          >
            {syncing ? 'Syncing...' : 'Pull & Sync to All Workspaces'}
          </button>
          <span className="text-[10px] text-[#4a6278]">
            Runs git pull then copies .claude/ to all workspaces
          </span>
        </div>
      </section>
    </div>
  )

  const voicePanel = (
    <div
      role="tabpanel"
      id="settings-tab-panel-voice"
      aria-labelledby="settings-tab-voice"
      hidden={activeTab !== 'voice'}
      className="space-y-6"
    >
      {/* Placeholder: TTS provider, STT model, voice preset + preview,
          mic device picker, wake-word toggle + sensitivity, ElevenLabs
          voice surface live here. */}
      <div className="text-xs text-[#4a6278] italic">
        TTS provider dropdown populated by TASK-018. Voice preset + preview populated by TASK-019.
        Mic device picker + wake-word controls populated by TASK-020.
      </div>

      {/* Jarvis AI Companion — hosts jarvisEnabled, jarvisAPIKey (until
          TASK-017 moves the key to Connections), jarvisVoice,
          jarvisVerbosity, jarvisAmbientEnabled. We keep this component
          intact per TASK-016 scope. */}
      <JarvisSettings cfg={cfg} onChange={setCfg} />
    </div>
  )

  const behaviorPanel = (
    <div
      role="tabpanel"
      id="settings-tab-panel-behavior"
      aria-labelledby="settings-tab-behavior"
      hidden={activeTab !== 'behavior'}
      className="space-y-6"
    >
      {/* Placeholder: useLiveKitTransport + LiveKit creds, audio transport
          dropdown live here. */}
      <div className="text-xs text-[#4a6278] italic">
        Audio transport dropdown + LiveKit credentials populated by TASK-020. Scanner roots
        Browse button populated by TASK-021.
      </div>

      {/* Terminal Preference */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Terminal</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Which terminal to use for Focus/Send/Navigate actions. Auto-detect uses whichever is
          available.
        </p>
        <select
          value={cfg.preferredTerminal}
          onChange={(e) => setCfg({ ...cfg, preferredTerminal: e.target.value })}
          className="sci-fi text-sm"
        >
          <option value="">Auto-detect</option>
          <option value="cmux">CMux</option>
          <option value="iterm2">iTerm2</option>
          <option value="terminal">Terminal.app</option>
        </select>
        <div className="mt-2 flex flex-wrap gap-1">
          {terminals.map((t) => (
            <span key={t} className="text-[10px] px-2 py-0.5 rounded bg-[#00ff88]/10 text-[#00ff88]">
              {t} detected
            </span>
          ))}
          {terminals.length === 0 && (
            <span className="text-[10px] text-[#4a6278]">No terminal providers detected</span>
          )}
        </div>
      </section>

      {/* Project Root Paths */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Project Root Directories</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Directories to scan for projects during discovery. One per line. Leave empty for
          auto-detect.
        </p>
        <textarea
          value={(cfg.projectRootPaths ?? []).join('\n')}
          onChange={(e) =>
            setCfg({ ...cfg, projectRootPaths: e.target.value.split('\n').filter(Boolean) })
          }
          placeholder="~/Desktop/Projects&#10;~/code&#10;~/repos"
          rows={4}
          className="sci-fi w-full text-sm font-mono resize-none"
        />
      </section>

      {/* Scan Interval */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Session Scan Interval</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          How often (in seconds) Jarvis checks for new/ended Claude Code sessions.
        </p>
        <input
          type="number"
          min={1}
          max={60}
          value={cfg.scanIntervalSeconds}
          onChange={(e) =>
            setCfg({ ...cfg, scanIntervalSeconds: parseInt(e.target.value, 10) || 5 })
          }
          className="sci-fi w-24 text-sm font-mono"
        />
      </section>

      {/* Notifications */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Notifications</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Control when Jarvis sends desktop notifications.
        </p>
        <div className="space-y-3">
          <label className="flex items-center justify-between cursor-pointer">
            <span className="text-sm text-[#8ba4b8]">Enable notifications</span>
            <button
              type="button"
              role="switch"
              aria-checked={cfg.notificationsEnabled}
              onClick={() => setCfg({ ...cfg, notificationsEnabled: !cfg.notificationsEnabled })}
              className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${cfg.notificationsEnabled ? 'bg-[#00e5ff]' : 'bg-[#1a2332]'}`}
              style={cfg.notificationsEnabled ? { boxShadow: '0 0 8px rgba(0, 229, 255, 0.4)' } : {}}
            >
              <span
                className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${cfg.notificationsEnabled ? 'translate-x-[18px]' : 'translate-x-[3px]'}`}
              />
            </button>
          </label>
          <label className="flex items-center justify-between cursor-pointer">
            <span className="text-sm text-[#8ba4b8]">Notify on approval needed</span>
            <button
              type="button"
              role="switch"
              aria-checked={cfg.notifyOnApproval}
              onClick={() => setCfg({ ...cfg, notifyOnApproval: !cfg.notifyOnApproval })}
              className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${cfg.notifyOnApproval ? 'bg-[#00e5ff]' : 'bg-[#1a2332]'}`}
              style={cfg.notifyOnApproval ? { boxShadow: '0 0 8px rgba(0, 229, 255, 0.4)' } : {}}
            >
              <span
                className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${cfg.notifyOnApproval ? 'translate-x-[18px]' : 'translate-x-[3px]'}`}
              />
            </button>
          </label>
          <label className="flex items-center justify-between cursor-pointer">
            <span className="text-sm text-[#8ba4b8]">Notify on session completion</span>
            <button
              type="button"
              role="switch"
              aria-checked={cfg.notifyOnCompletion}
              onClick={() => setCfg({ ...cfg, notifyOnCompletion: !cfg.notifyOnCompletion })}
              className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${cfg.notifyOnCompletion ? 'bg-[#00e5ff]' : 'bg-[#1a2332]'}`}
              style={cfg.notifyOnCompletion ? { boxShadow: '0 0 8px rgba(0, 229, 255, 0.4)' } : {}}
            >
              <span
                className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${cfg.notifyOnCompletion ? 'translate-x-[18px]' : 'translate-x-[3px]'}`}
              />
            </button>
          </label>
        </div>
      </section>

      {/* CI Integration */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">CI Integration</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Watch CI pipelines and surface status in Jarvis.
        </p>
        <div className="space-y-3">
          <label className="flex items-center justify-between cursor-pointer">
            <span className="text-sm text-[#8ba4b8]">Enable CI watching</span>
            <button
              type="button"
              role="switch"
              aria-checked={cfg.ciWatchEnabled}
              onClick={() => setCfg({ ...cfg, ciWatchEnabled: !cfg.ciWatchEnabled })}
              className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${cfg.ciWatchEnabled ? 'bg-[#00e5ff]' : 'bg-[#1a2332]'}`}
              style={cfg.ciWatchEnabled ? { boxShadow: '0 0 8px rgba(0, 229, 255, 0.4)' } : {}}
            >
              <span
                className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${cfg.ciWatchEnabled ? 'translate-x-[18px]' : 'translate-x-[3px]'}`}
              />
            </button>
          </label>
          <div>
            <label htmlFor="ci-provider" className="block text-sm text-[#8ba4b8] mb-1">
              CI Provider
            </label>
            <select
              id="ci-provider"
              value={cfg.ciProvider}
              onChange={(e) => setCfg({ ...cfg, ciProvider: e.target.value })}
              className="sci-fi text-sm"
            >
              <option value="">None</option>
              <option value="github-actions">GitHub Actions</option>
              <option value="gitlab-ci">GitLab CI</option>
            </select>
          </div>
        </div>
      </section>

      {/* Approval Rules (must survive refactor — gap doc note section 6) */}
      <ApprovalRulesSettings />
    </div>
  )

  const diagnosticsPanel = (
    <div
      role="tabpanel"
      id="settings-tab-panel-diagnostics"
      aria-labelledby="settings-tab-diagnostics"
      hidden={activeTab !== 'diagnostics'}
      className="space-y-6"
    >
      <div className="text-xs text-[#4a6278] italic">
        Live health panel populated by TASK-022 (daemon status, mic permission, mobile API, LLM
        provider chain, bundled models, Ollama reachability, disk usage).
      </div>
    </div>
  )

  const advancedPanel = (
    <div
      role="tabpanel"
      id="settings-tab-panel-advanced"
      aria-labelledby="settings-tab-advanced"
      hidden={activeTab !== 'advanced'}
      className="space-y-6"
    >
      {/* Placeholder: Export / Import / Reset to defaults + dotClaudeSource
          Browse button live here once TASK-023 lands. */}
      <div className="text-xs text-[#4a6278] italic">
        Export / Import config buttons, Reset-to-defaults, and dotClaudeSource Browse button
        populated by TASK-023.
      </div>

      {/* Mobile App — token + LAN addresses + regenerate cluster.
          Moved into Advanced per gap doc field mapping (section 3.1). */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Mobile App</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Scan or copy this token into the Jarvis Mobile app settings.
        </p>
        {mobileLoading ? (
          <div className="space-y-2 animate-pulse">
            <div className="h-4 bg-[#1a2332] rounded w-48" />
            <div className="h-8 bg-[#1a2332] rounded w-full" />
            <div className="h-4 bg-[#1a2332] rounded w-32" />
          </div>
        ) : mobileInfo ? (
          <div className="space-y-3">
            {/* LAN Addresses */}
            <div>
              <label className="block text-xs text-[#4a6278] mb-1">LAN Address(es)</label>
              <div className="flex flex-wrap gap-2">
                {(mobileInfo.ips ?? []).length > 0 ? (
                  (mobileInfo.ips ?? []).map((ip) => {
                    const addr = `${ip}:${mobileInfo.port}`
                    return (
                      <button
                        key={ip}
                        type="button"
                        onClick={() => void copyToClipboard(addr, 'Address')}
                        title="Click to copy"
                        className="px-3 py-1.5 text-sm font-mono rounded-lg text-[#e8f4ff] cursor-pointer transition-all"
                        style={{
                          background: 'rgba(10, 14, 26, 0.8)',
                          border: '1px solid rgba(0, 229, 255, 0.15)',
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.borderColor = 'rgba(0, 229, 255, 0.5)'
                          e.currentTarget.style.boxShadow = '0 0 8px rgba(0, 229, 255, 0.2)'
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.borderColor = 'rgba(0, 229, 255, 0.15)'
                          e.currentTarget.style.boxShadow = 'none'
                        }}
                      >
                        {addr}
                      </button>
                    )
                  })
                ) : (
                  <span className="text-xs text-[#4a6278]">No LAN addresses detected</span>
                )}
              </div>
            </div>

            {/* Bearer Token */}
            <div>
              <label className="block text-xs text-[#4a6278] mb-1">Bearer Token</label>
              <div className="flex items-center gap-2">
                <input
                  type={tokenRevealed ? 'text' : 'password'}
                  value={mobileInfo.token ?? ''}
                  readOnly
                  className="sci-fi flex-1 text-sm font-mono"
                />
                <button
                  type="button"
                  onClick={() => setTokenRevealed((r) => !r)}
                  className="text-xs px-3 py-2 rounded-lg text-[#8ba4b8] hover:text-[#e8f4ff] transition-colors"
                  style={{
                    background: 'rgba(10, 14, 26, 0.8)',
                    border: '1px solid rgba(0, 229, 255, 0.15)',
                  }}
                >
                  {tokenRevealed ? 'Hide' : 'Reveal'}
                </button>
              </div>
              <div className="flex items-center gap-2 mt-2">
                <button
                  type="button"
                  onClick={() => void copyToClipboard(mobileInfo.token ?? '', 'Token')}
                  className="text-xs px-3 py-1.5 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white transition-colors"
                >
                  Copy Token
                </button>
                <button
                  type="button"
                  onClick={() => void handleRegenerateToken()}
                  disabled={regenerating}
                  className="text-xs px-3 py-1.5 rounded bg-[#ff4757] hover:bg-[#ff4757]/80 text-white disabled:opacity-50 transition-colors"
                >
                  {regenerating ? 'Regenerating...' : 'Regenerate'}
                </button>
              </div>
            </div>
          </div>
        ) : (
          <span className="text-xs text-[#4a6278]">Failed to load mobile connection info</span>
        )}
      </section>
    </div>
  )

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
          {connectionsPanel}
          {voicePanel}
          {behaviorPanel}
          {diagnosticsPanel}
          {advancedPanel}

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
