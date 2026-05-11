import { useState } from 'react'
import {
  BrowseForDirectory,
  ExportConfig,
  ImportConfig,
  ResetConfig,
  OpenFileForImport,
} from '../../../wailsjs/go/main/App'
import { main } from '../../../wailsjs/go/models'
import type { SettingsPanelProps } from './types'

// ---------------------------------------------------------------------------
// AdvancedPanel — power-user / "everything else" surface.
//
// Owns:
//   - Mobile App token cluster (LAN addresses + Bearer token + Regenerate)
//     (originally seeded by TASK-016 prep)
//   - dotClaudeSourcePath input + Browse button (TASK-023)
//   - Export / Import / Reset to defaults config actions (TASK-023)
//
// TASK-023 design notes:
//   - Export is non-destructive → no confirmation.
//   - Import + Reset can clobber live settings → both gated by an inline
//     confirmation modal. Reset offers a "Preserve API keys" checkbox
//     (default: checked) that maps to the backend's preserveAPIKeys flag.
//   - All four action buttons live in a dedicated "Config" section above
//     the existing Mobile App section so the existing cluster stays
//     stable for downstream regression tests.
//   - Errors render as a small pill below the action buttons for ~5s.
// ---------------------------------------------------------------------------

export interface AdvancedPanelProps extends SettingsPanelProps {
  /** Mobile API connection info (LAN IPs + port + bearer token). May be
   *  null briefly while the initial GetMobileConnectionInfo() resolves. */
  mobileInfo: main.MobileConnectionInfo | null
  /** True while the initial GetMobileConnectionInfo() is in flight. Drives
   *  the skeleton loading placeholders. */
  mobileLoading: boolean
  /** True while RegenerateMobileToken() is in flight — disables the
   *  Regenerate button. */
  regenerating: boolean
  /** Triggers RegenerateMobileToken() + re-fetches connection info. */
  onRegenerateToken: () => Promise<void>
  /** True when the bearer token should render as plain text instead of
   *  password-masked. Toggled by the Reveal/Hide button. */
  tokenRevealed: boolean
  /** Setter for tokenRevealed — typically called with the negation pattern
   *  `setTokenRevealed((r) => !r)`. */
  setTokenRevealed: (next: boolean | ((prev: boolean) => boolean)) => void
  /** Copies the given text to the system clipboard and shows a toast/banner
   *  using `label` as the human-readable subject ("Address", "Token"). */
  copyToClipboard: (text: string, label: string) => Promise<void>
}

export function AdvancedPanel({
  activeTab,
  cfg,
  setCfg,
  mobileInfo,
  mobileLoading,
  regenerating,
  onRegenerateToken,
  tokenRevealed,
  setTokenRevealed,
  copyToClipboard,
}: AdvancedPanelProps): React.ReactElement {
  // --- TASK-023 local state -----------------------------------------------
  // confirmImport / confirmReset drive small inline modals. They hold the
  // data needed to actually perform the destructive op when the user
  // clicks "Confirm". A null value means "modal closed".
  const [confirmImport, setConfirmImport] = useState<{ path: string } | null>(null)
  const [confirmReset, setConfirmReset] = useState<boolean>(false)
  // "Preserve API keys" — default true so the safer option is one click.
  const [preserveKeys, setPreserveKeys] = useState<boolean>(true)
  // Generic error pill (auto-clears after 5s).
  const [errorMsg, setErrorMsg] = useState<string | null>(null)
  const [okMsg, setOkMsg] = useState<string | null>(null)
  // In-flight flags so the action buttons disable + visually de-emphasize
  // during the underlying Wails call.
  const [exporting, setExporting] = useState(false)
  const [importing, setImporting] = useState(false)
  const [resetting, setResetting] = useState(false)

  // Replaces the current error pill, schedules a 5s auto-clear.
  const flashError = (msg: string): void => {
    setErrorMsg(msg)
    setTimeout(() => setErrorMsg((cur) => (cur === msg ? null : cur)), 5000)
  }
  const flashOk = (msg: string): void => {
    setOkMsg(msg)
    setTimeout(() => setOkMsg((cur) => (cur === msg ? null : cur)), 5000)
  }

  // --- Browse for dotClaude source ----------------------------------------
  const handleBrowseDotClaude = async (): Promise<void> => {
    try {
      const picked = await BrowseForDirectory('Select .claude source directory')
      if (picked) {
        setCfg({ ...cfg, dotClaudeSourcePath: picked })
      }
    } catch (err) {
      // Native cancel can surface as a rejection on some platforms.
      console.debug('BrowseForDirectory cancelled or failed:', err)
    }
  }

  // --- Export -------------------------------------------------------------
  const handleExport = async (): Promise<void> => {
    if (exporting) return
    setExporting(true)
    try {
      const written = await ExportConfig()
      if (written) {
        flashOk(`Config exported to ${written}`)
      }
      // empty string = user cancelled the save dialog → silent no-op
    } catch (err) {
      flashError(`Export failed: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setExporting(false)
    }
  }

  // --- Import (two-step: pick → confirm → write) --------------------------
  const handleImportPick = async (): Promise<void> => {
    if (importing) return
    try {
      const path = await OpenFileForImport()
      if (path) {
        setConfirmImport({ path })
      }
    } catch (err) {
      flashError(`Import picker failed: ${err instanceof Error ? err.message : String(err)}`)
    }
  }

  const handleImportConfirm = async (): Promise<void> => {
    if (!confirmImport) return
    setImporting(true)
    try {
      // Import never preserves keys — the user explicitly chose to load
      // a config file, so the file's keys win. (Reset is the flow that
      // benefits from preserveAPIKeys.)
      await ImportConfig(confirmImport.path, false)
      setConfirmImport(null)
      flashOk('Config imported. Restart Jarvis to apply all changes.')
    } catch (err) {
      // Backend rejected the JSON; keep the modal open so the user can
      // either pick a different file or cancel.
      flashError(`Import failed: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setImporting(false)
    }
  }

  // --- Reset to defaults --------------------------------------------------
  const handleResetConfirm = async (): Promise<void> => {
    setResetting(true)
    try {
      await ResetConfig(preserveKeys)
      setConfirmReset(false)
      flashOk(
        preserveKeys
          ? 'Settings reset to defaults (API keys preserved). Restart Jarvis to apply.'
          : 'Settings fully reset to defaults. Restart Jarvis to apply.',
      )
    } catch (err) {
      flashError(`Reset failed: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setResetting(false)
    }
  }

  return (
    <div
      role="tabpanel"
      id="settings-tab-panel-advanced"
      aria-labelledby="settings-tab-advanced"
      hidden={activeTab !== 'advanced'}
      className="space-y-6"
    >
      {/* dotClaudeSource (TASK-023) */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">.claude Source Directory</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Path to a <code className="font-mono">.claude</code> folder (or a repo containing one)
          that Jarvis should copy into newly-created workspaces. Leave empty to auto-detect from
          common locations.
        </p>
        <div className="flex items-center gap-2">
          <input
            type="text"
            value={cfg.dotClaudeSourcePath ?? ''}
            onChange={(e) => setCfg({ ...cfg, dotClaudeSourcePath: e.target.value })}
            placeholder="~/code/dotAiAgent/.claude"
            className="sci-fi flex-1 text-sm font-mono"
            aria-label="dotClaude source path"
          />
          <button
            type="button"
            onClick={() => void handleBrowseDotClaude()}
            className="text-xs px-3 py-1.5 rounded border border-[#00e5ff]/40 text-[#00e5ff] hover:bg-[#00e5ff]/10 transition-colors"
          >
            Browse...
          </button>
        </div>
      </section>

      {/* Config import / export / reset (TASK-023) */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Configuration</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Back up, restore, or factory-reset your Jarvis configuration. Import and Reset both
          require confirmation before any settings are overwritten.
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => void handleExport()}
            disabled={exporting}
            className="text-xs px-3 py-1.5 rounded border border-[#00e5ff]/40 text-[#00e5ff] hover:bg-[#00e5ff]/10 disabled:opacity-50 transition-colors"
          >
            {exporting ? 'Exporting…' : 'Export config'}
          </button>
          <button
            type="button"
            onClick={() => void handleImportPick()}
            disabled={importing}
            className="text-xs px-3 py-1.5 rounded border border-[#00e5ff]/40 text-[#00e5ff] hover:bg-[#00e5ff]/10 disabled:opacity-50 transition-colors"
          >
            Import config
          </button>
          <button
            type="button"
            onClick={() => setConfirmReset(true)}
            disabled={resetting}
            className="text-xs px-3 py-1.5 rounded border border-[#ff4757]/50 text-[#ff4757] hover:bg-[#ff4757]/10 disabled:opacity-50 transition-colors"
          >
            Reset to defaults
          </button>
        </div>

        {/* Error / success pill (auto-clears after 5s) */}
        {errorMsg && (
          <div
            role="alert"
            className="mt-3 text-xs px-3 py-2 rounded bg-[#ff4757]/10 text-[#ff4757] border border-[#ff4757]/30"
          >
            {errorMsg}
          </div>
        )}
        {okMsg && (
          <div
            role="status"
            className="mt-3 text-xs px-3 py-2 rounded bg-[#00ff88]/10 text-[#00ff88] border border-[#00ff88]/30"
          >
            {okMsg}
          </div>
        )}
      </section>

      {/* --- Import confirmation modal --- */}
      {confirmImport && (
        <div
          role="dialog"
          aria-modal="true"
          aria-labelledby="confirm-import-title"
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
          onClick={() => !importing && setConfirmImport(null)}
        >
          <div
            className="holo-panel p-5 max-w-md w-full"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 id="confirm-import-title" className="text-sm font-semibold text-[#00e5ff] mb-2">
              Overwrite current configuration?
            </h3>
            <p className="text-xs text-[#8ba4b8] mb-3">
              Importing will replace all current settings with the contents of:
            </p>
            <p className="text-xs font-mono text-[#e8f4ff] break-all bg-[#0a0e1a] p-2 rounded mb-4 border border-[#00e5ff]/20">
              {confirmImport.path}
            </p>
            <p className="text-xs text-[#8ba4b8] mb-4">
              If the file is malformed, your current config will be left untouched.
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setConfirmImport(null)}
                disabled={importing}
                className="text-xs px-3 py-1.5 rounded border border-[#4a6278] text-[#8ba4b8] hover:text-[#e8f4ff] disabled:opacity-50 transition-colors"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => void handleImportConfirm()}
                disabled={importing}
                className="text-xs px-3 py-1.5 rounded bg-[#0d9488] hover:bg-[#0d9488]/80 text-white disabled:opacity-50 transition-colors"
              >
                {importing ? 'Importing…' : 'Confirm import'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* --- Reset confirmation modal --- */}
      {confirmReset && (
        <div
          role="dialog"
          aria-modal="true"
          aria-labelledby="confirm-reset-title"
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
          onClick={() => !resetting && setConfirmReset(false)}
        >
          <div
            className="holo-panel p-5 max-w-md w-full"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 id="confirm-reset-title" className="text-sm font-semibold text-[#ff4757] mb-2">
              Reset all settings to defaults?
            </h3>
            <p className="text-xs text-[#8ba4b8] mb-4">
              This wipes every setting in <code className="font-mono">~/.jarvis/config.json</code>{' '}
              back to its built-in default. Cannot be undone.
            </p>
            <label className="flex items-center gap-2 mb-4 cursor-pointer">
              <input
                type="checkbox"
                checked={preserveKeys}
                onChange={(e) => setPreserveKeys(e.target.checked)}
                disabled={resetting}
                className="sci-fi"
              />
              <span className="text-xs text-[#8ba4b8]">
                Preserve API keys (Anthropic, ElevenLabs, Picovoice, LiveKit, Mobile token)
              </span>
            </label>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setConfirmReset(false)}
                disabled={resetting}
                className="text-xs px-3 py-1.5 rounded border border-[#4a6278] text-[#8ba4b8] hover:text-[#e8f4ff] disabled:opacity-50 transition-colors"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => void handleResetConfirm()}
                disabled={resetting}
                className="text-xs px-3 py-1.5 rounded bg-[#ff4757] hover:bg-[#ff4757]/80 text-white disabled:opacity-50 transition-colors"
              >
                {resetting ? 'Resetting…' : 'Confirm reset'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Mobile App — token + LAN addresses + regenerate cluster.
          Moved into Advanced per gap doc field mapping (section 3.1).
          DO NOT modify — owned by TASK-016 prep. */}
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
                  onClick={() => void onRegenerateToken()}
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
}
