import { ApprovalRulesSettings } from '../../components/ApprovalRulesSettings'
import { BrowseForDirectory } from '../../../wailsjs/go/main/App'
import type { SettingsPanelProps } from './types'

// ---------------------------------------------------------------------------
// BehaviorPanel — runtime behavior / automation surface.
//
// Owns: preferredTerminal, projectRootPaths, scanIntervalSeconds, the three
// notification toggles (notificationsEnabled, notifyOnApproval,
// notifyOnCompletion), ciWatchEnabled, ciProvider, and the
// <ApprovalRulesSettings/> child component.
//
// TASK-020: Audio transport dropdown (Local vs LiveKit) + conditional LiveKit
//           credential fields (useLiveKitTransport, livekitUrl, livekitApiKey,
//           livekitApiSecret). Ambient mode toggle (jarvisAmbientEnabled).
// TASK-021: Native "Browse..." button next to projectRootPaths textarea that
//           opens a folder picker via the BrowseForDirectory Wails binding
//           and appends the chosen path to the list.
// ---------------------------------------------------------------------------

export interface BehaviorPanelProps extends SettingsPanelProps {
  /** List of terminal providers detected on this machine — drives the
   *  "<x> detected" pill row under the Terminal dropdown. */
  terminals: string[]
}

export function BehaviorPanel({
  cfg,
  setCfg,
  activeTab,
  terminals,
}: BehaviorPanelProps): React.ReactElement {
  // TASK-021 — open native folder picker, append result to projectRootPaths.
  const handleBrowse = async (): Promise<void> => {
    try {
      const picked = await BrowseForDirectory('Add a project root directory')
      if (picked) {
        const current = cfg.projectRootPaths ?? []
        // Avoid duplicates if the user picks the same folder twice.
        if (!current.includes(picked)) {
          setCfg({ ...cfg, projectRootPaths: [...current, picked] })
        }
      }
    } catch (err) {
      // Native dialog cancellation can surface as a rejection on some
      // platforms — swallow silently. Other errors get a debug log.
      console.debug('BrowseForDirectory cancelled or failed:', err)
    }
  }

  return (
    <div
      role="tabpanel"
      id="settings-tab-panel-behavior"
      aria-labelledby="settings-tab-behavior"
      hidden={activeTab !== 'behavior'}
      className="space-y-6"
    >
      {/* Audio Transport (TASK-020) */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Audio Transport</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          How Jarvis captures microphone input and plays back voice output. Local uses the Mac's
          built-in mic + speaker and is recommended for desktop use. LiveKit routes audio through a
          LiveKit server — useful for remote/mobile control but requires extra credentials.
        </p>
        <select
          aria-label="Audio transport"
          value={cfg.useLiveKitTransport ? 'livekit' : 'local'}
          onChange={(e) =>
            setCfg({ ...cfg, useLiveKitTransport: e.target.value === 'livekit' })
          }
          className="sci-fi text-sm"
        >
          <option value="local">Local (Mac mic+speaker — recommended)</option>
          <option value="livekit">LiveKit (advanced — requires extra config)</option>
        </select>

        {cfg.useLiveKitTransport && (
          <div className="mt-4 space-y-3 border-l-2 border-[#00e5ff]/30 pl-4">
            <div>
              <label htmlFor="livekit-url" className="block text-xs text-[#8ba4b8] mb-1">
                LiveKit URL
              </label>
              <input
                id="livekit-url"
                type="text"
                placeholder="wss://your-project.livekit.cloud"
                value={cfg.livekitUrl ?? ''}
                onChange={(e) => setCfg({ ...cfg, livekitUrl: e.target.value })}
                className="sci-fi w-full text-sm font-mono"
              />
            </div>
            <div>
              <label htmlFor="livekit-api-key" className="block text-xs text-[#8ba4b8] mb-1">
                LiveKit API Key
              </label>
              <input
                id="livekit-api-key"
                type="text"
                placeholder="APIxxxxxxxxxxxxx"
                value={cfg.livekitApiKey ?? ''}
                onChange={(e) => setCfg({ ...cfg, livekitApiKey: e.target.value })}
                className="sci-fi w-full text-sm font-mono"
              />
            </div>
            <div>
              <label htmlFor="livekit-api-secret" className="block text-xs text-[#8ba4b8] mb-1">
                LiveKit API Secret
              </label>
              <input
                id="livekit-api-secret"
                type="password"
                placeholder="(server-only secret)"
                value={cfg.livekitApiSecret ?? ''}
                onChange={(e) => setCfg({ ...cfg, livekitApiSecret: e.target.value })}
                className="sci-fi w-full text-sm font-mono"
              />
            </div>
            <p className="text-[10px] text-[#4a6278] italic">
              Credentials are stored locally in ~/.jarvis/config.json. The API secret never leaves
              your machine.
            </p>
          </div>
        )}
      </section>

      {/* Ambient Mode (TASK-020 — jarvisAmbientEnabled lives in config.Config) */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Ambient Mode</h2>
        <p className="text-xs text-[#8ba4b8] mb-3">
          When enabled, Jarvis listens passively for the wake word ("Hey Jarvis") in the
          background. Disable to require an explicit hotkey or button press to start talking.
        </p>
        <label className="flex items-center justify-between cursor-pointer">
          <span className="text-sm text-[#8ba4b8]">Enable ambient listening</span>
          <button
            type="button"
            role="switch"
            aria-checked={cfg.jarvisAmbientEnabled}
            onClick={() =>
              setCfg({ ...cfg, jarvisAmbientEnabled: !cfg.jarvisAmbientEnabled })
            }
            className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${cfg.jarvisAmbientEnabled ? 'bg-[#00e5ff]' : 'bg-[#1a2332]'}`}
            style={cfg.jarvisAmbientEnabled ? { boxShadow: '0 0 8px rgba(0, 229, 255, 0.4)' } : {}}
          >
            <span
              className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${cfg.jarvisAmbientEnabled ? 'translate-x-[18px]' : 'translate-x-[3px]'}`}
            />
          </button>
        </label>
      </section>

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

      {/* Project Root Paths (TASK-021 adds Browse button) */}
      <section className="holo-panel p-4">
        <div className="flex items-center justify-between mb-1">
          <h2 className="text-sm font-semibold text-[#00e5ff]">Project Root Directories</h2>
          <button
            type="button"
            onClick={handleBrowse}
            className="text-xs px-3 py-1 rounded border border-[#00e5ff]/40 text-[#00e5ff] hover:bg-[#00e5ff]/10 transition-colors"
          >
            Browse...
          </button>
        </div>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Directories to scan for projects during discovery. One per line. Leave empty for
          auto-detect. Use Browse... to append a folder via the native picker.
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
}
