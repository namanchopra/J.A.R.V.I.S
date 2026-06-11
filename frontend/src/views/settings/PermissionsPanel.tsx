import { useCallback, useEffect, useState } from 'react'
import type { SettingsPanelProps } from './types'
import { BrowserOpenURL, Environment } from '../../../wailsjs/runtime/runtime'

// ---------------------------------------------------------------------------
// PermissionsPanel — Settings → Permissions tab (TASK-017, v0.3.0 P1).
//
// Renders all 24 voice tools (15 mac_* + 9 spotify_*) as rows with an
// `allow / ask / deny` segmented control per row. The current decision is
// read from `App.GetMacctlPolicy()` on mount and written back per-row via
// `App.SetMacctlPolicy(tool, decision)`.
//
// Persistence model:
//   - Optimistic update: the segmented control snaps to the new value
//     immediately so there's no perceptible delay or layout jump.
//   - On error from SetMacctlPolicy we revert the row to its previous
//     decision + flash an error pill, so a malformed write never leaves the
//     UI in a state the daemon doesn't agree with.
//   - A "Saved" toast confirms the round-trip succeeded; auto-dismisses
//     after 2s so it doesn't accumulate.
//
// Tool catalogue:
//   The 24 tools are hardcoded with one-line descriptions in TOOL_GROUPS
//   below. The policy itself (internal/macctl/policy.go) carries decision
//   state per tool name but no human-readable descriptions — those live
//   here so the UI is self-contained. Adding a new tool requires updating
//   both the policy defaults (Go side, TASK-003) AND this catalogue. The
//   test pins on every tool name so a drift between the two surfaces
//   loudly.
//
// Wails binding shim:
//   `GetMacctlPolicy` / `SetMacctlPolicy` are real bindings on the Go App
//   struct (app_macctl.go, shipped in TASK-015) but the regenerated
//   `wailsjs/go/main/App.d.ts` lags this branch — same situation
//   VoicePanel.tsx and App.tsx work around. We resolve the two bindings
//   at call time via `window.go.main.App`, with graceful degradation when
//   they're absent (dev mode before `wails generate module` runs):
//     - GetMacctlPolicy missing → render the catalogue with "ask" defaults
//       so the user can still see + click through the table.
//     - SetMacctlPolicy missing → toggling shows an error pill instead of
//       silently no-oping.
// ---------------------------------------------------------------------------

type Decision = 'allow' | 'ask' | 'deny'

const DECISIONS: ReadonlyArray<Decision> = ['allow', 'ask', 'deny'] as const

// ---------------------------------------------------------------------------
// TASK-032 (v0.4.0 Windows port) — Deep-link URIs to OS privacy settings.
//
// macOS: x-apple.systempreferences: opens System Settings → Privacy &
// Security → Microphone. Same scheme DiagnosticsPanel.tsx uses for the mic
// permission row.
//
// Windows: ms-settings: opens Settings → Privacy → Microphone. The scheme
// is documented at https://learn.microsoft.com/en-us/windows/uwp/launch-resume/launch-settings-app.
// Note: a locked-down corp environment can disable the ms-settings: handler
// via group policy ("Prevent access to the Settings app"); when that
// happens BrowserOpenURL silently no-ops, so we surface a fallback hint
// below the button telling the user the manual path through Start menu
// search. This satisfies the "group-policy-blocked URI shows fallback
// text" acceptance criterion.
// ---------------------------------------------------------------------------
const MACOS_SETTINGS_MICROPHONE_URL =
  'x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone'
const WINDOWS_SETTINGS_MICROPHONE_URL = 'ms-settings:privacy-microphone'

interface ToolDef {
  name: string
  desc: string
}

interface ToolGroup {
  title: string
  tools: ToolDef[]
}

// TOOL_GROUPS — canonical catalogue of every tool the user can govern.
// Order is intentional: Spotify first (lowest stakes), then progressively
// more system-y categories. Group titles + tool names are pinned by the
// regression test so a typo here surfaces loudly.
const TOOL_GROUPS: ReadonlyArray<ToolGroup> = [
  {
    title: 'Spotify',
    tools: [
      { name: 'spotify_search_and_play', desc: 'Search and play a track' },
      { name: 'spotify_pause', desc: 'Pause playback' },
      { name: 'spotify_resume', desc: 'Resume playback' },
      { name: 'spotify_skip', desc: 'Skip to next track' },
      { name: 'spotify_previous', desc: 'Previous track' },
      { name: 'spotify_what_is_playing', desc: 'Read currently playing' },
      { name: 'spotify_set_volume', desc: 'Set Spotify volume' },
      { name: 'spotify_like_current', desc: 'Like current track' },
      { name: 'spotify_queue', desc: 'Queue a track' },
    ],
  },
  {
    title: 'Apps',
    tools: [
      { name: 'mac_open_app', desc: 'Open or activate an app' },
      { name: 'mac_quit_app', desc: 'Quit an app' },
      { name: 'mac_focus_window', desc: 'Focus a specific window' },
    ],
  },
  {
    title: 'Audio + display',
    tools: [
      { name: 'mac_set_volume', desc: 'Set system volume' },
      { name: 'mac_mute', desc: 'Mute output' },
      { name: 'mac_unmute', desc: 'Unmute output' },
      { name: 'mac_set_brightness', desc: 'Set screen brightness' },
      { name: 'mac_toggle_dnd', desc: 'Toggle Do Not Disturb' },
    ],
  },
  {
    title: 'Files + clipboard',
    tools: [
      { name: 'mac_open_path', desc: 'Open a file or URL' },
      { name: 'mac_spotlight', desc: 'Search Spotlight' },
      { name: 'mac_clipboard_get', desc: 'Read clipboard' },
      { name: 'mac_clipboard_set', desc: 'Write clipboard' },
    ],
  },
  {
    title: 'Screenshots + shortcuts',
    tools: [
      { name: 'mac_screenshot', desc: 'Take a screenshot' },
      { name: 'mac_list_shortcuts', desc: 'List Shortcuts.app' },
      { name: 'mac_run_shortcut', desc: 'Run a Shortcut' },
    ],
  },
] as const

// ---------------------------------------------------------------------------
// Wails binding shim. Same window.go.main.App lookup pattern VoicePanel.tsx
// uses — keeps this panel buildable on branches where the generated
// App.d.ts hasn't been refreshed yet.
// ---------------------------------------------------------------------------
interface MacctlBindings {
  GetMacctlPolicy?: () => Promise<Record<string, string>>
  SetMacctlPolicy?: (tool: string, decision: string) => Promise<void>
  // TASK-028 — manual "Test push" trigger. Fans an Expo push notification
  // out to every paired Friday device. The binding is optional in the type
  // so the panel stays buildable on branches where `wails generate module`
  // hasn't been rerun (same resilience pattern as the macctl bindings).
  JarvisSendTestPush?: () => Promise<string>
}

function macctlBindings(): MacctlBindings | null {
  const w = window as unknown as {
    go?: { main?: { App?: MacctlBindings } }
  }
  return w.go?.main?.App ?? null
}

function isDecision(value: string): value is Decision {
  return value === 'allow' || value === 'ask' || value === 'deny'
}

// Decision label/glyph helpers — small, declarative so the segmented control
// JSX stays scannable.
function decisionLabel(d: Decision): string {
  switch (d) {
    case 'allow':
      return 'allow'
    case 'ask':
      return 'ask'
    case 'deny':
      return 'deny'
  }
}

function decisionGlyph(d: Decision): string {
  switch (d) {
    case 'allow':
      return '✓'
    case 'ask':
      return '?'
    case 'deny':
      return '✕'
  }
}

// Color tokens per decision — match the existing panel palette
// (cyan accents, green for allow, amber for ask, red for deny).
function decisionColor(d: Decision): { fg: string; bg: string; border: string; glow: string } {
  switch (d) {
    case 'allow':
      return {
        fg: '#00ff88',
        bg: 'rgba(0, 255, 136, 0.14)',
        border: 'rgba(0, 255, 136, 0.5)',
        glow: '0 0 10px rgba(0, 255, 136, 0.22)',
      }
    case 'ask':
      return {
        fg: '#ffb800',
        bg: 'rgba(255, 184, 0, 0.14)',
        border: 'rgba(255, 184, 0, 0.5)',
        glow: '0 0 10px rgba(255, 184, 0, 0.22)',
      }
    case 'deny':
      return {
        fg: '#ff6b78',
        bg: 'rgba(255, 71, 87, 0.14)',
        border: 'rgba(255, 71, 87, 0.5)',
        glow: '0 0 10px rgba(255, 71, 87, 0.22)',
      }
  }
}

// Default decisions when the binding isn't reachable. Mirrors the Go-side
// defaultAllowTools/defaultAskTools split in internal/macctl/policy.go so
// the UI shows the same intent the daemon would enforce.
const DEFAULT_DECISIONS: Record<string, Decision> = {
  // Read-only / safe tools — default allow.
  mac_spotlight: 'allow',
  mac_clipboard_get: 'allow',
  mac_screenshot: 'allow',
  mac_list_shortcuts: 'allow',
  spotify_what_is_playing: 'allow',
  spotify_pause: 'allow',
  spotify_resume: 'allow',
  spotify_skip: 'allow',
  spotify_previous: 'allow',
  spotify_set_volume: 'allow',
  spotify_like_current: 'allow',
  spotify_queue: 'allow',
  spotify_search_and_play: 'allow',
  // Destructive / surprising tools — default ask.
  mac_open_app: 'ask',
  mac_quit_app: 'ask',
  mac_focus_window: 'ask',
  mac_set_volume: 'ask',
  mac_mute: 'ask',
  mac_unmute: 'ask',
  mac_set_brightness: 'ask',
  mac_toggle_dnd: 'ask',
  mac_open_path: 'ask',
  mac_clipboard_set: 'ask',
  mac_run_shortcut: 'ask',
}

export type PermissionsPanelProps = SettingsPanelProps

export function PermissionsPanel({ activeTab }: PermissionsPanelProps): React.ReactElement {
  // Policy state — keyed by tool name. Initialised from the runtime call to
  // GetMacctlPolicy on mount; falls through to DEFAULT_DECISIONS when the
  // binding is absent (dev mode pre-wails-generate).
  const [policy, setPolicy] = useState<Record<string, Decision>>(DEFAULT_DECISIONS)
  const [loaded, setLoaded] = useState<boolean>(false)
  const [toast, setToast] = useState<{ text: string; type: 'success' | 'error' } | null>(null)
  // In-flight per-tool flag — disables a row's controls while its save is
  // running so a frantic double-click can't queue conflicting writes.
  const [savingTool, setSavingTool] = useState<string | null>(null)

  // TASK-032 (v0.4.0 Windows port) — platform detection so we can render a
  // Windows-specific deep-link CTA (ms-settings:privacy-microphone) instead
  // of the macOS x-apple.systempreferences: scheme. Defaults to 'darwin' so
  // a stale/failed Environment() call renders the existing macOS UI rather
  // than spuriously offering Windows controls (acceptance criterion: "macOS
  // deep link unchanged"). Same pattern MeetingPanel.tsx + OverlayPanel.tsx
  // use for platform-aware rendering.
  const [platform, setPlatform] = useState<string>('darwin')
  useEffect(() => {
    let cancelled = false
    Environment()
      .then((env) => {
        if (cancelled) return
        if (env && typeof env.platform === 'string' && env.platform.length > 0) {
          setPlatform(env.platform)
        }
      })
      .catch((err) => {
        // Wails runtime not available (e.g. SSR / test harness). Keep the
        // macOS default — the existing behaviour is the safest fallback.
        console.debug('PermissionsPanel: Environment() unavailable, defaulting to darwin', err)
      })
    return () => {
      cancelled = true
    }
  }, [])
  const isWindows = platform === 'windows'

  // ---------------------------------------------------------------
  // GetMacctlPolicy on mount. We deliberately do NOT depend on cfg/setCfg
  // here — the policy is its own ~/.jarvis/policy.json file on disk and is
  // managed independently of config.json.
  // ---------------------------------------------------------------
  useEffect(() => {
    const app = macctlBindings()
    if (!app?.GetMacctlPolicy) {
      // Binding not yet generated — keep the DEFAULT_DECISIONS map so the
      // UI is still functional; the maintainer can see the table layout
      // without a built app.
      setLoaded(true)
      return
    }
    let cancelled = false
    app
      .GetMacctlPolicy()
      .then((raw) => {
        if (cancelled) return
        const next: Record<string, Decision> = { ...DEFAULT_DECISIONS }
        for (const [tool, decision] of Object.entries(raw ?? {})) {
          if (isDecision(decision)) {
            next[tool] = decision
          }
        }
        setPolicy(next)
        setLoaded(true)
      })
      .catch((err) => {
        if (cancelled) return
        console.warn('GetMacctlPolicy failed:', err)
        // Fall back to defaults rather than blocking the UI.
        setLoaded(true)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Auto-dismiss toast after 2s. Independent of the parent SettingsView's
  // 3s toast so a Save here doesn't race the global one.
  useEffect(() => {
    if (!toast) return
    const id = window.setTimeout(() => setToast(null), 2000)
    return () => window.clearTimeout(id)
  }, [toast])

  // ---------------------------------------------------------------
  // TASK-028 -- "Test push" button. Fans a notification out to every paired
  // Friday device via the JarvisSendTestPush Wails binding (app_push.go).
  // Surfaces the result via the same toast channel as the policy writes so
  // there's a single visual feedback path for the panel.
  //
  // Local in-flight flag (testPushPending) disables the button while the
  // request is in-flight -- the round-trip to Expo's push service takes
  // ~200-800ms and a frantic double-click would otherwise fire two pushes.
  // ---------------------------------------------------------------
  const [testPushPending, setTestPushPending] = useState<boolean>(false)
  const handleTestPush = useCallback(async (): Promise<void> => {
    setTestPushPending(true)
    try {
      const app = macctlBindings()
      if (!app?.JarvisSendTestPush) {
        setToast({
          text: 'JarvisSendTestPush binding unavailable — restart Jarvis after build.',
          type: 'error',
        })
        return
      }
      // Go returns a status string on success ("Sent test push to N device(s).")
      // and an Error on failure ("no push tokens registered", etc.). Surface
      // both verbatim so the maintainer can debug from the toast alone.
      const status = await app.JarvisSendTestPush()
      setToast({ text: status || 'Test push sent — check your phone.', type: 'success' })
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setToast({ text: `Test push failed: ${msg}`, type: 'error' })
    } finally {
      setTestPushPending(false)
    }
  }, [])

  // ---------------------------------------------------------------
  // Optimistic write. Snap the UI to the new value first; revert on error.
  // This keeps the segmented control feeling instant even on a slow
  // SetMacctlPolicy round-trip and prevents the row from briefly jumping
  // back to the prior value while the daemon writes the file.
  // ---------------------------------------------------------------
  const handleChange = useCallback(
    async (tool: string, next: Decision): Promise<void> => {
      // Capture the prior value BEFORE the optimistic update so we can revert.
      const prev = policy[tool] ?? DEFAULT_DECISIONS[tool] ?? 'ask'
      if (prev === next) return // No-op click on the already-selected option.

      // Optimistic update — UI snaps instantly.
      setPolicy((p) => ({ ...p, [tool]: next }))
      setSavingTool(tool)

      const app = macctlBindings()
      if (!app?.SetMacctlPolicy) {
        // Binding absent — revert and surface a clear error so the user
        // knows the change didn't persist.
        setPolicy((p) => ({ ...p, [tool]: prev }))
        setToast({ text: 'SetMacctlPolicy binding unavailable — restart Jarvis after build.', type: 'error' })
        setSavingTool(null)
        return
      }

      try {
        await app.SetMacctlPolicy(tool, next)
        setToast({ text: `Saved · ${tool}`, type: 'success' })
      } catch (err) {
        // Revert + show error toast. SetMacctlPolicy returns a wrapped error
        // string from Go (see app_macctl.go), which we surface verbatim.
        setPolicy((p) => ({ ...p, [tool]: prev }))
        const msg = err instanceof Error ? err.message : String(err)
        setToast({ text: `Failed: ${msg}`, type: 'error' })
      } finally {
        setSavingTool(null)
      }
    },
    [policy],
  )

  return (
    <div
      role="tabpanel"
      id="settings-tab-panel-permissions"
      aria-labelledby="settings-tab-permissions"
      hidden={activeTab !== 'permissions'}
      className="space-y-6"
    >
      {/* ---------------------------------------------------------- */}
      {/* Header / explanation                                        */}
      {/* ---------------------------------------------------------- */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Voice Tool Permissions</h2>
        <p className="text-xs text-[#8ba4b8]">
          Voice tools that change state on your Mac ask before running by default. You can change
          the default per tool — set to <span className="text-[#00ff88]">allow</span> for tools you
          trust, or <span className="text-[#ff6b78]">deny</span> to refuse outright.
        </p>
        <p className="text-[10px] text-[#4a6278] mt-2 italic">
          Stored at <span style={{ color: 'rgba(0,229,255,0.7)' }}>~/.jarvis/policy.json</span>.
          Changes save instantly.
        </p>
      </section>

      {/* ---------------------------------------------------------- */}
      {/* TASK-032 — OS-level Microphone permission deep link.        */}
      {/* Same row layout on both platforms; only the button label,  */}
      {/* deep-link target, and helper copy change per platform.     */}
      {/* macOS branch is the existing System Settings behaviour     */}
      {/* (unchanged per acceptance criterion). Windows branch uses  */}
      {/* the ms-settings: URI and surfaces a fallback hint for the  */}
      {/* group-policy-blocked failure case.                          */}
      {/* ---------------------------------------------------------- */}
      <section className="holo-panel p-4" data-testid="permissions-os-deeplink">
        <h3
          className="text-xs font-semibold text-[#00e5ff] mb-2"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.18em',
            textTransform: 'uppercase',
          }}
        >
          ▸ OS-level Microphone Access
        </h3>
        {isWindows ? (
          <div
            className="flex items-center justify-between gap-4"
            data-testid="permissions-os-deeplink-windows"
          >
            <div className="flex-1 min-w-0">
              <p className="text-[11px] text-[#8ba4b8]">
                Jarvis needs Microphone access from Windows. Open{' '}
                <span style={{ color: 'rgba(0,229,255,0.7)' }}>
                  Settings → Privacy → Microphone
                </span>{' '}
                and enable access for Jarvis.
              </p>
              <p
                className="text-[10px] text-[#4a6278] mt-1 italic"
                data-testid="permissions-os-deeplink-windows-fallback"
              >
                If your organisation blocks <code>ms-settings:</code> via group policy, open the
                Start menu and search for <em>Microphone privacy settings</em> instead.
              </p>
            </div>
            <button
              type="button"
              data-testid="permissions-open-windows-microphone"
              onClick={() => BrowserOpenURL(WINDOWS_SETTINGS_MICROPHONE_URL)}
              className="text-[11px] px-3 py-1 rounded border transition-colors flex-shrink-0"
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                letterSpacing: '0.05em',
                borderColor: 'rgba(0, 229, 255, 0.5)',
                color: 'rgba(0, 229, 255, 0.95)',
                background: 'transparent',
              }}
            >
              Open Windows Settings
            </button>
          </div>
        ) : (
          <div
            className="flex items-center justify-between gap-4"
            data-testid="permissions-os-deeplink-darwin"
          >
            <div className="flex-1 min-w-0">
              <p className="text-[11px] text-[#8ba4b8]">
                Jarvis needs Microphone access from macOS. Open{' '}
                <span style={{ color: 'rgba(0,229,255,0.7)' }}>
                  System Settings → Privacy &amp; Security → Microphone
                </span>{' '}
                and enable access for Jarvis.
              </p>
            </div>
            <button
              type="button"
              data-testid="permissions-open-macos-microphone"
              onClick={() => BrowserOpenURL(MACOS_SETTINGS_MICROPHONE_URL)}
              className="text-[11px] px-3 py-1 rounded border transition-colors flex-shrink-0"
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                letterSpacing: '0.05em',
                borderColor: 'rgba(0, 229, 255, 0.5)',
                color: 'rgba(0, 229, 255, 0.95)',
                background: 'transparent',
              }}
            >
              Open System Settings
            </button>
          </div>
        )}
      </section>

      {/* ---------------------------------------------------------- */}
      {/* TASK-028 -- Test push button. Fans an Expo push notification */}
      {/* out to every paired Friday device. Lives in the Permissions  */}
      {/* tab per the v0.3.0 plan rather than its own dedicated panel  */}
      {/* because push is the same "Mac reaching the phone" surface    */}
      {/* the tool permissions govern -- a maintainer auditing per-    */}
      {/* tool decisions is exactly the person who wants to verify the */}
      {/* phone is reachable end-to-end.                               */}
      {/* ---------------------------------------------------------- */}
      <section className="holo-panel p-4">
        <div className="flex items-center justify-between gap-4">
          <div className="flex-1 min-w-0">
            <h3
              className="text-xs font-semibold text-[#00e5ff] mb-1"
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                letterSpacing: '0.18em',
                textTransform: 'uppercase',
              }}
            >
              ▸ Push Notifications
            </h3>
            <p className="text-[11px] text-[#8ba4b8]">
              Send a test notification to every paired Friday device. Verify the Mac can reach your
              phone end-to-end before relying on session alerts.
            </p>
          </div>
          <button
            type="button"
            data-testid="permissions-test-push-button"
            disabled={testPushPending}
            onClick={() => void handleTestPush()}
            className="text-[11px] px-4 py-2 transition-all disabled:opacity-50 flex-shrink-0"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.12em',
              textTransform: 'uppercase',
              fontWeight: 600,
              background: testPushPending ? 'rgba(0, 229, 255, 0.04)' : 'rgba(0, 229, 255, 0.08)',
              color: '#00e5ff',
              border: '1px solid rgba(0, 229, 255, 0.4)',
              borderRadius: '3px',
              cursor: testPushPending ? 'wait' : 'pointer',
              boxShadow: testPushPending ? 'none' : '0 0 10px rgba(0, 229, 255, 0.18)',
            }}
          >
            {testPushPending ? 'Sending…' : 'Test push'}
          </button>
        </div>
      </section>

      {/* ---------------------------------------------------------- */}
      {/* Toast — "Saved" / error                                     */}
      {/* ---------------------------------------------------------- */}
      {toast && (
        <div
          role="status"
          aria-live="polite"
          data-testid="permissions-toast"
          className="fade-in-up text-xs font-mono px-4 py-2 rounded-sm flex items-center gap-3"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.05em',
            background:
              toast.type === 'success'
                ? 'rgba(0, 255, 136, 0.08)'
                : 'rgba(255, 71, 87, 0.08)',
            border: `1px solid ${
              toast.type === 'success' ? 'rgba(0,255,136,0.4)' : 'rgba(255,71,87,0.45)'
            }`,
            color: toast.type === 'success' ? '#00ff88' : '#ff6b78',
            boxShadow:
              toast.type === 'success'
                ? '0 0 14px rgba(0, 255, 136, 0.15)'
                : '0 0 14px rgba(255, 71, 87, 0.18)',
          }}
        >
          <span style={{ fontWeight: 700 }}>
            {toast.type === 'success' ? '◉ OK ::' : '✕ ERR ::'}
          </span>
          <span style={{ flex: 1 }}>{toast.text}</span>
        </div>
      )}

      {/* ---------------------------------------------------------- */}
      {/* Tool table — grouped by category                            */}
      {/* ---------------------------------------------------------- */}
      {TOOL_GROUPS.map((group) => (
        <section key={group.title} className="holo-panel p-4">
          <h3
            className="text-xs font-semibold text-[#00e5ff] mb-3"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.18em',
              textTransform: 'uppercase',
            }}
          >
            ▸ {group.title}
          </h3>
          <div className="space-y-2">
            {group.tools.map((tool) => {
              const current: Decision = policy[tool.name] ?? DEFAULT_DECISIONS[tool.name] ?? 'ask'
              const inFlight = savingTool === tool.name
              return (
                <div
                  key={tool.name}
                  data-testid={`permission-row-${tool.name}`}
                  className="flex items-center justify-between gap-4 px-3 py-2 rounded"
                  style={{
                    background: 'rgba(0, 229, 255, 0.03)',
                    border: '1px solid rgba(0, 229, 255, 0.08)',
                  }}
                >
                  <div className="flex-1 min-w-0">
                    <code
                      className="text-xs text-[#cfe7ff] block"
                      style={{ fontFamily: "'SF Mono', 'Menlo', monospace" }}
                    >
                      {tool.name}
                    </code>
                    <p className="text-[11px] text-[#8ba4b8] mt-0.5">{tool.desc}</p>
                  </div>
                  <div
                    role="radiogroup"
                    aria-label={`Permission for ${tool.name}`}
                    className="flex items-stretch flex-shrink-0"
                    style={{
                      border: '1px solid rgba(0, 229, 255, 0.18)',
                      borderRadius: '3px',
                      overflow: 'hidden',
                    }}
                  >
                    {DECISIONS.map((opt) => {
                      const isActive = current === opt
                      const colors = decisionColor(opt)
                      return (
                        <button
                          key={opt}
                          type="button"
                          role="radio"
                          aria-checked={isActive}
                          aria-label={`${decisionLabel(opt)} ${tool.name}`}
                          disabled={inFlight && !isActive}
                          onClick={() => void handleChange(tool.name, opt)}
                          className="text-[11px] px-3 py-1.5 transition-all disabled:opacity-50"
                          style={{
                            fontFamily: "'SF Mono', 'Menlo', monospace",
                            letterSpacing: '0.12em',
                            textTransform: 'uppercase',
                            fontWeight: 600,
                            background: isActive ? colors.bg : 'transparent',
                            color: isActive ? colors.fg : 'rgba(207, 231, 255, 0.4)',
                            border: 'none',
                            borderRight: opt === 'deny' ? 'none' : '1px solid rgba(0, 229, 255, 0.18)',
                            boxShadow: isActive ? colors.glow : 'none',
                            cursor: inFlight && !isActive ? 'not-allowed' : 'pointer',
                          }}
                          onMouseEnter={(e) => {
                            if (!isActive && !inFlight) {
                              e.currentTarget.style.color = colors.fg
                              e.currentTarget.style.background = 'rgba(0, 229, 255, 0.04)'
                            }
                          }}
                          onMouseLeave={(e) => {
                            if (!isActive) {
                              e.currentTarget.style.color = 'rgba(207, 231, 255, 0.4)'
                              e.currentTarget.style.background = 'transparent'
                            }
                          }}
                        >
                          <span aria-hidden="true" style={{ marginRight: 4 }}>
                            {decisionGlyph(opt)}
                          </span>
                          {decisionLabel(opt)}
                        </button>
                      )
                    })}
                  </div>
                </div>
              )
            })}
          </div>
        </section>
      ))}

      {/* ---------------------------------------------------------- */}
      {/* Footer hint — only when loaded                              */}
      {/* ---------------------------------------------------------- */}
      {loaded && (
        <p
          className="text-[10px] text-[#4a6278] italic text-center pt-2"
          style={{ fontFamily: "'SF Mono', 'Menlo', monospace", letterSpacing: '0.05em' }}
        >
          {TOOL_GROUPS.reduce((n, g) => n + g.tools.length, 0)} tools · default policy stored at
          ~/.jarvis/policy.json
        </p>
      )}
    </div>
  )
}
