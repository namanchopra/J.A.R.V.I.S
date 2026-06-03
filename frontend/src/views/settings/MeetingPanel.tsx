import { useCallback, useEffect, useState } from 'react'
import type { SettingsPanelProps } from './types'
import { SaveConfig } from '../../../wailsjs/go/main/App'
import { EventsOn, BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
import { config as cfgModels } from '../../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// MeetingPanel — Settings → Meeting tab (TASK-012, v0.3.0 meeting-mode).
//
// Three user-controllable fields backed by config.Config (added in TASK-001):
//   1. Auto-suggest from calendar  (meetingAutoSuggest) — boolean toggle
//   2. Notes directory              (meetingNotesDir)   — text input
//   3. Meeting keywords             (meetingKeywords)   — chip-style editor
//
// Plus a hidden-by-default permission warning row that surfaces when the
// daemon emits `"meeting:permission_error"` (TASK-005 — Screen Recording
// permission denied during SCK Start). Deep-links to System Settings →
// Privacy & Security → Screen Recording, mirroring the Accessibility CTA
// in OverlayPanel.tsx.
//
// Persistence model (mirrors OverlayPanel's hybrid):
//   - The auto-suggest toggle and notes-dir input defer to the parent's
//     sticky Save button via setCfg (matches BehaviorPanel/OverlayPanel
//     toggle/select handling).
//   - The keyword chip add/delete actions have an immediate side effect
//     (the auto-suggest banner reads the list on its next poll), so we
//     eagerly persist via SaveConfig() mid-flow — same pattern as
//     OverlayPanel's hotkey rebind. This avoids a stale-list window where
//     the user adds a chip, then the calendar fires the banner check
//     before they tap Save.
//
// Wails binding resilience:
//   - The generated wailsjs/go/models.ts in this sandbox DOES include the
//     three meeting fields (TASK-001 ran the regen). We still treat them
//     as defensively-optional on read because we ship via a build that
//     may predate the regen on a contributor's machine.
//   - TASK-015 extends this panel's permission warning row (the row's
//     wiring is owned here; TASK-015 layers an in-overlay toast with the
//     same CTA URL).
// ---------------------------------------------------------------------------

// Deep link to macOS System Settings → Privacy & Security → Screen
// Recording. Required by SCK because TASK-004's bridge needs that
// permission to capture system audio output. Same URL-scheme convention
// as DiagnosticsPanel.tsx's mic-permission deep-link.
const SYSTEM_SETTINGS_SCREEN_RECORDING_URL =
  'x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenRecording'

// ---------------------------------------------------------------------------
// Runtime bridge: OpenMeetingNotesFolder
// ---------------------------------------------------------------------------
// Reveals the configured (tilde-expanded) notes directory in Finder. We go
// through a Go-side binding rather than BrowserOpenURL("file://...") because
// the notes dir may contain a literal '~' that the file:// scheme won't
// expand on the frontend, and because the binding additionally mkdir-p's
// the directory if it doesn't yet exist. Missing binding degrades to a
// console.warn (same safe-default pattern as the EventsOn listener below).
async function callOpenMeetingNotesFolder(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.OpenMeetingNotesFolder as
      | (() => Promise<void>)
      | undefined
    if (fn) await fn()
    else console.warn('MeetingPanel: OpenMeetingNotesFolder binding unavailable')
  } catch (err) {
    console.warn('MeetingPanel: OpenMeetingNotesFolder rejected', err)
  }
}

const DEFAULT_NOTES_DIR = '~/.jarvis/meetings'
const DEFAULT_KEYWORDS: ReadonlyArray<string> = [
  'call',
  'sync',
  '1:1',
  'meeting',
  'standup',
  'review',
  'interview',
]

// ---------------------------------------------------------------------------
// Narrowed view of config.Config that includes the three meeting fields.
// The generated wailsjs/go/models.ts declares them, but we read through
// a defensive alias so a stale build (mid-regen) renders cleanly with
// defaults instead of crashing on `undefined.toLowerCase()`.
// ---------------------------------------------------------------------------
interface MeetingConfigSlice {
  meetingNotesDir?: string
  meetingKeywords?: string[]
  meetingAutoSuggest?: boolean
}

type MeetingCfg = cfgModels.Config & MeetingConfigSlice

export type MeetingPanelProps = SettingsPanelProps

export function MeetingPanel({ cfg, setCfg, activeTab }: MeetingPanelProps): React.ReactElement {
  const mcfg = cfg as MeetingCfg
  const notesDir: string =
    typeof mcfg.meetingNotesDir === 'string' && mcfg.meetingNotesDir.length > 0
      ? mcfg.meetingNotesDir
      : DEFAULT_NOTES_DIR
  const keywords: string[] = Array.isArray(mcfg.meetingKeywords)
    ? mcfg.meetingKeywords
    : [...DEFAULT_KEYWORDS]
  const autoSuggest: boolean =
    typeof mcfg.meetingAutoSuggest === 'boolean' ? mcfg.meetingAutoSuggest : true

  // Permission warning is event-driven: hidden by default, lit by an
  // EventsOn subscription to `"meeting:permission_error"` (TASK-005).
  const [permissionError, setPermissionError] = useState<string | null>(null)
  // Keyword draft input state — local-only; committed to config on Enter.
  const [keywordDraft, setKeywordDraft] = useState<string>('')

  // ---------------------------------------------------------------
  // Listen for permission errors emitted by TASK-005. Payload may be a
  // string message (preferred) or empty — we default to a generic copy.
  // ---------------------------------------------------------------
  useEffect(() => {
    const cancel = EventsOn('meeting:permission_error', (payload: unknown) => {
      const msg =
        typeof payload === 'string' && payload.length > 0
          ? payload
          : 'Screen Recording permission denied'
      setPermissionError(msg)
    })
    return () => {
      cancel()
    }
  }, [])

  // ---------------------------------------------------------------
  // Eager-save helper for chip mutations. Mirrors OverlayPanel's
  // persistHotkey() — fire-and-forget so the UI snaps back immediately;
  // log on failure so a stale Wails build doesn't swallow the error.
  // ---------------------------------------------------------------
  const persistKeywords = useCallback(async (nextCfg: MeetingCfg): Promise<void> => {
    try {
      await SaveConfig(nextCfg as cfgModels.Config)
    } catch (err) {
      console.warn('MeetingPanel: SaveConfig failed for keyword change', err)
    }
  }, [])

  // ---------------------------------------------------------------
  // Field handlers. The toggle + notes-dir defer to the parent's sticky
  // Save button via setCfg. The chip add/delete actions eagerly persist
  // via SaveConfig because the auto-suggest poll reads the keyword list
  // on a 15s cadence (TASK-011) and a stale-list window between chip
  // edit + Save would be user-visible.
  // ---------------------------------------------------------------
  const handleAutoSuggestToggle = useCallback((): void => {
    const next = { ...cfg } as MeetingCfg
    next.meetingAutoSuggest = !autoSuggest
    setCfg(next)
  }, [cfg, autoSuggest, setCfg])

  const handleNotesDirChange = useCallback(
    (value: string): void => {
      const next = { ...cfg } as MeetingCfg
      next.meetingNotesDir = value
      setCfg(next)
    },
    [cfg, setCfg],
  )

  const handleAddKeyword = useCallback((): void => {
    const trimmed = keywordDraft.trim().toLowerCase()
    if (trimmed.length === 0) return
    if (keywords.includes(trimmed)) {
      setKeywordDraft('')
      return
    }
    const nextKeywords = [...keywords, trimmed]
    const next = { ...cfg } as MeetingCfg
    next.meetingKeywords = nextKeywords
    setCfg(next)
    setKeywordDraft('')
    void persistKeywords(next)
  }, [cfg, keywords, keywordDraft, setCfg, persistKeywords])

  const handleRemoveKeyword = useCallback(
    (idx: number): void => {
      const nextKeywords = keywords.filter((_, j) => j !== idx)
      const next = { ...cfg } as MeetingCfg
      next.meetingKeywords = nextKeywords
      setCfg(next)
      void persistKeywords(next)
    },
    [cfg, keywords, setCfg, persistKeywords],
  )

  const handleKeywordKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLInputElement>): void => {
      if (event.key === 'Enter') {
        event.preventDefault()
        handleAddKeyword()
      }
    },
    [handleAddKeyword],
  )

  return (
    <div
      role="tabpanel"
      id="settings-tab-panel-meeting"
      aria-labelledby="settings-tab-meeting"
      hidden={activeTab !== 'meeting'}
      className="space-y-6"
    >
      {/* TASK-012 marker — pattern from OverlayPanel.tsx so the SettingsView
          regression test can pin on task IDs if extended. */}
      <div className="hidden" aria-hidden="true" data-task="TASK-012" />

      {/* Header */}
      <section className="holo-panel p-4">
        <h2 className="text-sm font-semibold text-[#00e5ff] mb-1">Meeting Mode</h2>
        <p className="text-xs text-[#8ba4b8]">
          Passively listens during real-time meetings (Zoom, Meet, Teams) and emits a Markdown
          notes file with Summary / Key Points / Action Items / Raw Transcript when you stop the
          recording. Captures both the microphone and system audio output.
        </p>
        <p className="text-[10px] text-[#4a6278] mt-2 italic">
          Requires <span style={{ color: 'rgba(0,229,255,0.7)' }}>Screen Recording</span>{' '}
          permission for system-audio capture.
        </p>
      </section>

      {/* Permission warning row — hidden until the daemon emits the event.
          Mirrors the Accessibility CTA in OverlayPanel.tsx. */}
      {permissionError && (
        <section
          role="alert"
          aria-live="polite"
          data-testid="meeting-permission-error"
          className="fade-in-up text-xs px-3 py-2 rounded-sm flex items-center gap-3"
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
            Couldn&apos;t capture system audio. Grant Jarvis{' '}
            <strong style={{ color: 'var(--accent-amber)' }}>Screen Recording</strong> access in
            System Settings.
          </span>
          <button
            type="button"
            data-testid="meeting-open-screen-recording"
            onClick={() => BrowserOpenURL(SYSTEM_SETTINGS_SCREEN_RECORDING_URL)}
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
        </section>
      )}

      {/* Auto-suggest toggle */}
      <section className="holo-panel p-4">
        <h3
          className="text-xs font-semibold text-[#00e5ff] mb-2"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.18em',
            textTransform: 'uppercase',
          }}
        >
          ▸ Auto-suggest from calendar
        </h3>
        <label className="flex items-start justify-between cursor-pointer gap-4">
          <span className="text-sm text-[#8ba4b8] flex-1">
            When a calendar event matching a keyword starts in under 2 minutes, show a one-tap
            <span style={{ color: 'rgba(0,229,255,0.7)' }}> Start note-taking</span> banner in the
            main HUD.
          </span>
          <button
            type="button"
            role="switch"
            aria-checked={autoSuggest}
            aria-label="Auto-suggest meeting recording"
            data-testid="meeting-auto-suggest-toggle"
            onClick={handleAutoSuggestToggle}
            className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors flex-shrink-0 mt-0.5 ${autoSuggest ? 'bg-[#00e5ff]' : 'bg-[#1a2332]'}`}
            style={autoSuggest ? { boxShadow: '0 0 8px rgba(0, 229, 255, 0.4)' } : {}}
          >
            <span
              className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${autoSuggest ? 'translate-x-[18px]' : 'translate-x-[3px]'}`}
            />
          </button>
        </label>
      </section>

      {/* Notes directory */}
      <section className="holo-panel p-4">
        <h3
          className="text-xs font-semibold text-[#00e5ff] mb-2"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.18em',
            textTransform: 'uppercase',
          }}
        >
          ▸ Notes directory
        </h3>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Where Markdown meeting notes are written. Uses{' '}
          <code style={{ color: 'rgba(0,229,255,0.7)' }}>~</code> expansion. The directory is
          created on first write.
        </p>
        <div className="flex items-center gap-2">
          <input
            type="text"
            aria-label="Meeting notes directory"
            data-testid="meeting-notes-dir-input"
            value={notesDir}
            onChange={(e) => handleNotesDirChange(e.target.value)}
            placeholder={DEFAULT_NOTES_DIR}
            className="sci-fi text-sm flex-1"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.04em',
              padding: '8px 12px',
              background: 'rgba(0, 229, 255, 0.04)',
              border: '1px solid rgba(0, 229, 255, 0.25)',
              color: '#cfe7ff',
              borderRadius: '3px',
            }}
          />
          {/* OPEN FOLDER ↗ — reveals the resolved (tilde-expanded) notes
              directory in Finder via the OpenMeetingNotesFolder Go binding.
              Visual styling mirrors the permission-row "Open System Settings"
              CTA above so the panel's button vocabulary stays coherent. */}
          <button
            type="button"
            aria-label="Reveal meeting notes folder in Finder"
            title="Reveal in Finder"
            data-testid="meeting-open-notes-folder"
            onClick={() => void callOpenMeetingNotesFolder()}
            className="text-[11px] px-3 py-1.5 rounded border transition-colors whitespace-nowrap"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.06em',
              borderColor: 'rgba(0, 229, 255, 0.4)',
              color: 'rgba(0, 229, 255, 0.9)',
              background: 'transparent',
            }}
          >
            OPEN FOLDER ↗
          </button>
        </div>
      </section>

      {/* Keyword chip editor */}
      <section className="holo-panel p-4">
        <h3
          className="text-xs font-semibold text-[#00e5ff] mb-2"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            letterSpacing: '0.18em',
            textTransform: 'uppercase',
          }}
        >
          ▸ Meeting keywords
        </h3>
        <p className="text-xs text-[#8ba4b8] mb-3">
          Calendar events whose titles contain any of these (case-insensitive) trigger the
          auto-suggest banner.
        </p>

        <div
          data-testid="meeting-keywords-list"
          className="flex flex-wrap gap-2 mb-3"
          role="list"
          aria-label="Meeting keywords"
        >
          {keywords.length === 0 && (
            <span
              className="text-[11px] italic"
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                color: 'rgba(207, 231, 255, 0.35)',
              }}
            >
              No keywords — banner will never auto-suggest.
            </span>
          )}
          {keywords.map((kw, i) => (
            <span
              key={`${kw}-${i}`}
              role="listitem"
              className="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs"
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                letterSpacing: '0.06em',
                background: 'rgba(0, 229, 255, 0.08)',
                border: '1px solid rgba(0, 229, 255, 0.35)',
                color: '#cfe7ff',
                borderRadius: '3px',
              }}
            >
              <span>{kw}</span>
              <button
                type="button"
                aria-label={`Remove keyword ${kw}`}
                data-testid={`meeting-keyword-remove-${i}`}
                onClick={() => handleRemoveKeyword(i)}
                className="text-[14px] leading-none transition-colors"
                style={{
                  color: 'rgba(207, 231, 255, 0.55)',
                  background: 'transparent',
                  border: 'none',
                  cursor: 'pointer',
                  padding: 0,
                  marginLeft: 2,
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.color = '#ff6b78'
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.color = 'rgba(207, 231, 255, 0.55)'
                }}
              >
                ×
              </button>
            </span>
          ))}
        </div>

        <div className="flex items-center gap-2">
          <input
            type="text"
            aria-label="Add meeting keyword"
            data-testid="meeting-keyword-add-input"
            value={keywordDraft}
            onChange={(e) => setKeywordDraft(e.target.value)}
            onKeyDown={handleKeywordKeyDown}
            placeholder="Add keyword (Enter)"
            className="sci-fi text-sm flex-1"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.04em',
              padding: '6px 10px',
              background: 'rgba(0, 229, 255, 0.04)',
              border: '1px solid rgba(0, 229, 255, 0.25)',
              color: '#cfe7ff',
              borderRadius: '3px',
            }}
          />
          <button
            type="button"
            data-testid="meeting-keyword-add-button"
            onClick={handleAddKeyword}
            disabled={keywordDraft.trim().length === 0}
            className="text-[11px] px-3 py-1.5 rounded border transition-colors disabled:opacity-40"
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              letterSpacing: '0.06em',
              borderColor: 'rgba(0, 229, 255, 0.4)',
              color: 'rgba(0, 229, 255, 0.9)',
              background: 'transparent',
            }}
          >
            Add
          </button>
        </div>
      </section>
    </div>
  )
}
