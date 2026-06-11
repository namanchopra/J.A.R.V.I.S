import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { SettingsPanelProps } from './types'
import { SaveConfig } from '../../../wailsjs/go/main/App'
import { EventsOn, BrowserOpenURL, Environment } from '../../../wailsjs/runtime/runtime'
import { config as cfgModels } from '../../../wailsjs/go/models'
import {
  canonicalizeSpec,
  glyphFormatSpec,
  isBlockedSpec,
  isModifierOnly,
} from './hotkey-spec'

// ---------------------------------------------------------------------------
// OverlayPanel — Settings → Overlay tab (TASK-009, v0.3.0).
//
// Four user-controllable fields backed by config.Config:
//   1. Enable overlay         (overlayEnabled)
//   2. Hotkey (click-to-rebind, with capture mode + Cmd+Q/W/H blocklist)
//   3. Position (top-right/top-left/bottom-right/bottom-left/last-dragged)
//   4. Show transcript chip   (overlayShowTranscript)
//
// Persistence model (matches BehaviorPanel for the toggle/select fields):
//   - Toggles/selects mutate cfg via setCfg → parent's sticky Save button
//     persists via SaveConfig(cfg).
//   - The hotkey rebind is special: it has an immediate side effect
//     (RebindOverlayHotkey) so we save the spec eagerly via SaveConfig
//     mid-flow rather than waiting for the Save button. This matches the
//     PermissionsPanel pattern of "the daemon needs to know about this
//     change RIGHT NOW".
//
// Failure-mode surfacing:
//   - TASK-005 (future) emits `"overlay:hotkey_error"` when global hotkey
//     registration fails (Accessibility permission denied). We listen for
//     that event on mount and surface a warning row above the Hotkey field
//     with a deep-link to System Settings → Privacy → Accessibility.
//   - The reserved-shortcut blocklist (Cmd+Q/W/H — see hotkey-spec.ts)
//     fires its own inline warning that auto-dismisses after 3s.
//
// Wails binding resilience:
//   - SaveConfig / BrowserOpenURL / EventsOn are imported normally from the
//     generated bindings.
//   - The Config model in wailsjs/go/models.ts does NOT yet declare the
//     overlay* fields (TASK-001 added them on the Go side but the regen
//     hasn't run in this sandbox). We treat the four fields as optional
//     via a narrowed Config view and default them on read.
//   - RebindOverlayHotkey (TASK-005, future) is resolved at call time via
//     window.go?.main?.App with a typeof === 'function' guard so a stale
//     Wails build doesn't break the save.
// ---------------------------------------------------------------------------

type Position = 'top-right' | 'top-left' | 'bottom-right' | 'bottom-left' | 'last-dragged'

const POSITION_OPTIONS: ReadonlyArray<{ value: Position; label: string }> = [
  { value: 'top-right', label: 'Top right' },
  { value: 'top-left', label: 'Top left' },
  { value: 'bottom-right', label: 'Bottom right' },
  { value: 'bottom-left', label: 'Bottom left' },
  { value: 'last-dragged', label: 'Last dragged' },
] as const

const DEFAULT_HOTKEY = 'alt+space'
const DEFAULT_POSITION: Position = 'top-right'

// Deep link the macOS System Settings → Privacy → Accessibility panel. Used
// by the hotkey-error warning row's CTA. Mirrors the deep-link pattern from
// DiagnosticsPanel.tsx (which links to the Microphone panel).
const ACCESSIBILITY_SETTINGS_URL =
  'x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility'

// ---------------------------------------------------------------------------
// Narrowed view of config.Config that includes the four overlay fields. The
// generated wailsjs/go/models.ts hasn't been regenerated against TASK-001 so
// it doesn't yet expose these slots; we read/write through a superset cast
// and default on read so the panel renders cleanly on stale builds.
// ---------------------------------------------------------------------------
interface OverlayConfigSlice {
  overlayEnabled?: boolean
  overlayHotkey?: string
  overlayPosition?: string
  overlayShowTranscript?: boolean
}

type OverlayCfg = cfgModels.Config & OverlayConfigSlice

interface RebindBindings {
  RebindOverlayHotkey?: (spec: string) => Promise<void>
}

function rebindBindings(): RebindBindings | null {
  const w = window as unknown as {
    go?: { main?: { App?: RebindBindings } }
  }
  return w.go?.main?.App ?? null
}

function isValidPosition(v: string | undefined): v is Position {
  return (
    v === 'top-right' ||
    v === 'top-left' ||
    v === 'bottom-right' ||
    v === 'bottom-left' ||
    v === 'last-dragged'
  )
}

// ---------------------------------------------------------------------------
// TASK-036 (v0.4.0 Windows port) — Windows-style hotkey label formatter.
//
// On Windows the user expects literal modifier names ("Ctrl + Space",
// "Alt + Space") rather than the macOS Unicode glyphs (⌃, ⌥, ⌘, ⇧). Windows
// global hotkeys also can't bind to the Command key, so we map any lingering
// `cmd` token to `Ctrl` (defensive — the canonical spec format is shared
// across platforms but a config persisted on macOS could still contain it).
//
// Pure / structural — no React or runtime dependency — so it can be tested
// directly against the source via the existing `?raw` source-level contracts.
// ---------------------------------------------------------------------------
const WINDOWS_MOD_LABELS: Readonly<Record<string, string>> = {
  cmd: 'Ctrl', // Win has no Command — closest analogue is Ctrl.
  ctrl: 'Ctrl',
  alt: 'Alt',
  shift: 'Shift',
}

function titleCaseKeyForWindows(token: string): string {
  if (token.length === 1) return token.toUpperCase()
  if (/^f([1-9]|1[0-2])$/.test(token)) return token.toUpperCase()
  return token.charAt(0).toUpperCase() + token.slice(1)
}

function windowsFormatSpec(spec: string): string {
  if (!spec) return ''
  const parts = spec
    .split('+')
    .map((p) => p.trim())
    .filter(Boolean)
  const out: string[] = []
  for (const p of parts) {
    const lower = p.toLowerCase()
    if (WINDOWS_MOD_LABELS[lower]) {
      out.push(WINDOWS_MOD_LABELS[lower])
    } else {
      out.push(titleCaseKeyForWindows(lower))
    }
  }
  return out.join(' + ')
}

export type OverlayPanelProps = SettingsPanelProps

export function OverlayPanel({ cfg, setCfg, activeTab }: OverlayPanelProps): React.ReactElement {
  const ocfg = cfg as OverlayCfg
  const enabled: boolean = ocfg.overlayEnabled ?? true
  const hotkey: string = ocfg.overlayHotkey ?? DEFAULT_HOTKEY
  const position: Position = isValidPosition(ocfg.overlayPosition)
    ? ocfg.overlayPosition
    : DEFAULT_POSITION
  const showTranscript: boolean = ocfg.overlayShowTranscript ?? false

  // Hotkey capture mode — when true, the next keydown event is recorded and
  // serialized into the canonical spec. Escape aborts without saving.
  const [capturing, setCapturing] = useState<boolean>(false)
  const [reservedWarning, setReservedWarning] = useState<string | null>(null)
  const [hotkeyError, setHotkeyError] = useState<boolean>(false)
  const captureBtnRef = useRef<HTMLButtonElement | null>(null)

  // TASK-036 (v0.4.0 Windows port) — platform detection so we can render
  // Windows-style hotkey labels ("Ctrl + Space") instead of the macOS Unicode
  // glyphs ("⌃ Space"). Defaults to 'darwin' so a stale/failed Environment()
  // call falls back to the existing macOS glyph rendering — that's the
  // failure-case acceptance criterion ("platform detection failure defaults
  // to Unicode symbols (the existing macOS path)").
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
        // macOS default — the existing Unicode rendering is the safest
        // fallback per the TASK-036 failure-case acceptance criterion.
        console.debug('OverlayPanel: Environment() unavailable, defaulting to darwin', err)
      })
    return () => {
      cancelled = true
    }
  }, [])
  const isWindows = platform === 'windows'

  // Memoize the formatted hotkey label so the same string flows into both the
  // visible badge and the aria-label without recomputing on every render. We
  // route through the Windows formatter when isWindows is true; everywhere
  // else (darwin, linux, unknown platform, pre-detection) uses the existing
  // glyphFormatSpec — preserving the macOS unchanged contract.
  const hotkeyLabel = useMemo<string>(
    () => (isWindows ? windowsFormatSpec(hotkey) : glyphFormatSpec(hotkey)),
    [hotkey, isWindows],
  )

  // ---------------------------------------------------------------
  // Subscribe to TASK-005's `"overlay:hotkey_error"` event. The Go side
  // emits this when global hotkey registration fails (typically because
  // macOS Accessibility permission was denied). The payload (if any) is
  // ignored — presence of the event is the signal.
  // ---------------------------------------------------------------
  useEffect(() => {
    const cancel = EventsOn('overlay:hotkey_error', () => {
      setHotkeyError(true)
    })
    return () => {
      cancel()
    }
  }, [])

  // Auto-dismiss the reserved-shortcut warning after 3s. The user stays in
  // the panel and can try again without an extra click.
  useEffect(() => {
    if (!reservedWarning) return
    const id = window.setTimeout(() => setReservedWarning(null), 3000)
    return () => window.clearTimeout(id)
  }, [reservedWarning])

  // ---------------------------------------------------------------
  // Hotkey capture handler. Bound to the document while `capturing` is
  // true; one-shot in spirit (committed on first non-modifier keydown,
  // or aborted on Escape). preventDefault on every captured event so
  // we don't accidentally fire the chord at other components.
  // ---------------------------------------------------------------
  useEffect(() => {
    if (!capturing) return
    const onKeyDown = (event: KeyboardEvent): void => {
      event.preventDefault()
      event.stopPropagation()

      // Escape aborts capture without saving. We check on key rather than
      // canonicalized spec because a plain Escape (no modifiers) should
      // ALWAYS exit capture, even if some future config could legitimately
      // bind "escape" as a chord with modifiers.
      if (
        event.key === 'Escape' &&
        !event.metaKey &&
        !event.ctrlKey &&
        !event.altKey &&
        !event.shiftKey
      ) {
        setCapturing(false)
        return
      }

      // Ignore pure modifier presses so the user can press Cmd, then Shift,
      // then J without prematurely committing on the first Cmd-down.
      if (isModifierOnly(event)) return

      const spec = canonicalizeSpec(event)

      if (isBlockedSpec(spec)) {
        setReservedWarning(
          `${glyphFormatSpec(spec)} is reserved by macOS — pick a different shortcut.`,
        )
        setCapturing(false)
        return
      }

      // Persist immediately. The toggle/select fields defer to the sticky
      // Save button, but the hotkey rebind has an immediate side effect
      // (RebindOverlayHotkey) so we save the spec eagerly here.
      const nextCfg = { ...cfg } as OverlayCfg
      nextCfg.overlayHotkey = spec
      setCfg(nextCfg)
      setCapturing(false)
      // Clear any prior hotkey error — the user has actively changed the
      // binding, so the daemon will retry registration and the next
      // overlay:hotkey_error event (if any) will re-light the warning.
      setHotkeyError(false)

      // Fire-and-forget SaveConfig + RebindOverlayHotkey. We don't await
      // here so the UI snaps back to the read-only render immediately.
      void persistHotkey(nextCfg, spec)
    }

    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      document.removeEventListener('keydown', onKeyDown, true)
    }
  }, [capturing, cfg, setCfg])

  const persistHotkey = useCallback(
    async (nextCfg: OverlayCfg, spec: string): Promise<void> => {
      try {
        await SaveConfig(nextCfg as cfgModels.Config)
      } catch (err) {
        console.warn('OverlayPanel: SaveConfig failed for hotkey rebind', err)
      }
      // RebindOverlayHotkey arrives with TASK-005; until then the call site
      // is type-guarded so a stale Wails build still saves the spec to
      // config without throwing a "function not found" exception.
      try {
        const app = rebindBindings()
        if (app && typeof app.RebindOverlayHotkey === 'function') {
          await app.RebindOverlayHotkey(spec)
        }
      } catch (err) {
        console.warn('OverlayPanel: RebindOverlayHotkey failed', err)
      }
    },
    [],
  )

  // ---------------------------------------------------------------
  // Toggle / select handlers — mutate cfg, defer persistence to the
  // parent's sticky Save button (matches BehaviorPanel).
  // ---------------------------------------------------------------
  const handleEnabledToggle = useCallback((): void => {
    const next = { ...cfg } as OverlayCfg
    next.overlayEnabled = !enabled
    setCfg(next)
  }, [cfg, enabled, setCfg])

  const handleTranscriptToggle = useCallback((): void => {
    const next = { ...cfg } as OverlayCfg
    next.overlayShowTranscript = !showTranscript
    setCfg(next)
  }, [cfg, showTranscript, setCfg])

  const handlePositionChange = useCallback(
    (value: string): void => {
      if (!isValidPosition(value)) return
      const next = { ...cfg } as OverlayCfg
      next.overlayPosition = value
      setCfg(next)
    },
    [cfg, setCfg],
  )

  const handleStartCapture = useCallback((): void => {
    setReservedWarning(null)
    setCapturing(true)
  }, [])

  return (
    <div
      role="tabpanel"
      id="settings-tab-panel-overlay"
      aria-labelledby="settings-tab-overlay"
      hidden={activeTab !== 'overlay'}
      className="space-y-6"
    >
      {/* TASK-009 marker — matches the pattern in DiagnosticsPanel so the
          SettingsView regression test can pin on task IDs if extended. */}
      <div className="hidden" aria-hidden="true" data-task="TASK-009" />

      {/* Header */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Mac Overlay</h2>
        <p className="text-xs text-[#8ba4b8]">
          Frameless, always-on-top push-to-talk widget. Press and hold the global hotkey to talk;
          release to hand the turn over to the LLM. The orb stays visible until you close it
          manually.
        </p>
        <p className="text-[10px] text-[#4a6278] mt-2 italic">
          Requires <span style={{ color: 'rgba(0,229,255,0.7)' }}>Accessibility</span> permission
          for the global hotkey.
        </p>
      </section>

      {/* Enable overlay */}
      <section className="holo-panel p-4">
        <h3
          className="text-xs font-semibold text-[#00e5ff] mb-2"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.18em',
            textTransform: 'uppercase',
          }}
        >
          ▸ Enable
        </h3>
        <label className="flex items-center justify-between cursor-pointer">
          <span className="text-sm text-[#8ba4b8]">Enable overlay</span>
          <button
            type="button"
            role="switch"
            aria-checked={enabled}
            aria-label="Enable overlay"
            data-testid="overlay-enabled-toggle"
            onClick={handleEnabledToggle}
            className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${enabled ? 'bg-[#00e5ff]' : 'bg-[#1a2332]'}`}
            style={enabled ? { boxShadow: '0 0 8px rgba(0, 229, 255, 0.4)' } : {}}
          >
            <span
              className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${enabled ? 'translate-x-[18px]' : 'translate-x-[3px]'}`}
            />
          </button>
        </label>
      </section>

      {/* Hotkey rebind */}
      <section className="holo-panel p-4">
        <h3
          className="text-xs font-semibold text-[#00e5ff] mb-2"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.18em',
            textTransform: 'uppercase',
          }}
        >
          ▸ Hotkey
        </h3>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Click the field below and press your desired combo. Cmd+Q, Cmd+W, and Cmd+H are reserved
          by macOS.
        </p>

        {/* Hotkey-error warning row. Mounted above the field so the user
            sees it adjacent to the input that produced it. Reuses the deep-
            link pattern from DiagnosticsPanel.tsx's mic-permission row. */}
        {hotkeyError && (
          <div
            role="alert"
            aria-live="polite"
            data-testid="overlay-hotkey-error"
            className="fade-in-up text-xs px-3 py-2 mb-3 rounded-sm flex items-center gap-3"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.04em',
              background: 'rgba(255, 184, 0, 0.08)',
              border: '1px solid rgba(255, 184, 0, 0.45)',
              color: 'var(--accent-amber)',
              boxShadow: '0 0 14px rgba(255, 184, 0, 0.18)',
            }}
          >
            <span style={{ fontWeight: 700, whiteSpace: 'nowrap' }}>⚠</span>
            <span style={{ flex: 1, color: 'rgba(255, 207, 100, 0.85)' }}>
              Couldn&apos;t register the global hotkey. Grant Jarvis{' '}
              <strong style={{ color: 'var(--accent-amber)' }}>Accessibility</strong> access in
              System Settings.
            </span>
            <button
              type="button"
              data-testid="overlay-open-accessibility"
              onClick={() => BrowserOpenURL(ACCESSIBILITY_SETTINGS_URL)}
              className="text-[11px] px-3 py-1 rounded border transition-colors"
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                letterSpacing: '0.05em',
                borderColor: 'rgba(255, 184, 0, 0.5)',
                color: 'rgba(255, 207, 100, 0.95)',
                background: 'transparent',
              }}
            >
              Open System Settings
            </button>
          </div>
        )}

        {/* Reserved-shortcut warning (auto-dismisses after 3s). */}
        {reservedWarning && (
          <div
            role="alert"
            aria-live="polite"
            data-testid="overlay-reserved-warning"
            className="fade-in-up text-xs px-3 py-2 mb-3 rounded-sm"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.04em',
              background: 'rgba(255, 71, 87, 0.08)',
              border: '1px solid rgba(255, 71, 87, 0.45)',
              color: '#ff6b78',
              boxShadow: '0 0 14px rgba(255, 71, 87, 0.18)',
            }}
          >
            <span style={{ fontWeight: 700, marginRight: 6 }}>✕</span>
            {reservedWarning}
          </div>
        )}

        <button
          ref={captureBtnRef}
          type="button"
          data-testid="overlay-hotkey-capture"
          aria-label={
            capturing
              ? 'Press your desired hotkey combo, or Escape to cancel'
              : `Rebind overlay hotkey (current: ${hotkeyLabel})`
          }
          onClick={handleStartCapture}
          disabled={capturing}
          className="text-sm px-4 py-2 w-full text-left transition-all"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.12em',
            background: capturing ? 'rgba(0, 229, 255, 0.14)' : 'rgba(0, 229, 255, 0.04)',
            color: capturing ? '#00e5ff' : '#cfe7ff',
            border: `1px solid ${capturing ? 'rgba(0, 229, 255, 0.6)' : 'rgba(0, 229, 255, 0.25)'}`,
            borderRadius: '3px',
            boxShadow: capturing ? '0 0 14px rgba(0, 229, 255, 0.25)' : 'none',
            cursor: capturing ? 'wait' : 'pointer',
          }}
        >
          {capturing ? (
            <span>
              <span
                style={{
                  animation: 'pulse-glow 1.2s ease-in-out infinite',
                  display: 'inline-block',
                  marginRight: 8,
                }}
              >
                ◌
              </span>
              Press combo… (Esc to cancel)
            </span>
          ) : (
            <span>
              <span style={{ marginRight: 12, color: '#00e5ff' }}>{hotkeyLabel}</span>
              <span style={{ color: 'rgba(207, 231, 255, 0.45)', fontSize: 11 }}>
                — click to rebind
              </span>
            </span>
          )}
        </button>
      </section>

      {/* Position */}
      <section className="holo-panel p-4">
        <h3
          className="text-xs font-semibold text-[#00e5ff] mb-2"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.18em',
            textTransform: 'uppercase',
          }}
        >
          ▸ Position
        </h3>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Where on screen the overlay anchors when it appears.
        </p>
        <select
          aria-label="Overlay position"
          data-testid="overlay-position-select"
          value={position}
          onChange={(e) => handlePositionChange(e.target.value)}
          className="sci-fi text-sm"
        >
          {POSITION_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </section>

      {/* Show transcript chip */}
      <section className="holo-panel p-4">
        <h3
          className="text-xs font-semibold text-[#00e5ff] mb-2"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.18em',
            textTransform: 'uppercase',
          }}
        >
          ▸ Transcript
        </h3>
        <label className="flex items-center justify-between cursor-pointer">
          <span className="text-sm text-[#8ba4b8]">Show transcript chip under the orb</span>
          <button
            type="button"
            role="switch"
            aria-checked={showTranscript}
            aria-label="Show transcript chip"
            data-testid="overlay-transcript-toggle"
            onClick={handleTranscriptToggle}
            className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${showTranscript ? 'bg-[#00e5ff]' : 'bg-[#1a2332]'}`}
            style={showTranscript ? { boxShadow: '0 0 8px rgba(0, 229, 255, 0.4)' } : {}}
          >
            <span
              className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${showTranscript ? 'translate-x-[18px]' : 'translate-x-[3px]'}`}
            />
          </button>
        </label>
      </section>
    </div>
  )
}
