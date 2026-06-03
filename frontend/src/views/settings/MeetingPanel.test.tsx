// ---------------------------------------------------------------------------
// MeetingPanel — source-level contract tests (TASK-012, v0.3.0).
//
// This project's test harness does NOT ship jsdom + React Testing Library.
// We use the same `?raw` import trick as OverlayPanel.test.tsx /
// DiagnosticsPanel.test.tsx — pinning the wiring contract (config field
// names, SaveConfig call, EventsOn listener + cleanup, deep-link URL,
// chip-editor Enter handling, lowercase normalisation, conditional
// permission-row render).
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './MeetingPanel.tsx?raw'
import SETTINGS_VIEW_SOURCE from '../SettingsView.tsx?raw'
import SETTINGS_TABS_SOURCE from './SettingsTabs.tsx?raw'

describe('MeetingPanel TASK-012 (config field wiring)', () => {
  it('reads/writes the meetingNotesDir field', () => {
    // TASK-001 wired this on the Go side. The panel must address it by
    // name so a refactor that renames the field trips this expectation.
    expect(SOURCE).toMatch(/meetingNotesDir/)
  })

  it('reads/writes the meetingKeywords field', () => {
    expect(SOURCE).toMatch(/meetingKeywords/)
  })

  it('reads/writes the meetingAutoSuggest field', () => {
    expect(SOURCE).toMatch(/meetingAutoSuggest/)
  })

  it('calls SaveConfig so the daemon sees chip edits without waiting for the sticky Save bar', () => {
    // The auto-suggest banner polls the keyword list on a 15s cadence
    // (TASK-011), so chip edits must persist immediately to avoid a stale
    // window. Mirrors OverlayPanel's hotkey eager-save pattern.
    expect(SOURCE).toContain('SaveConfig')
    expect(SOURCE).toMatch(/SaveConfig\(/)
  })
})

describe('MeetingPanel TASK-012 (permission_error event wiring)', () => {
  it('subscribes to the "meeting:permission_error" Wails event', () => {
    // TASK-005 emits this on the Go side when SCK Start fails with
    // ErrPermissionDenied. The panel must listen to surface the CTA.
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]meeting:permission_error['"]/)
  })

  it('returns a cleanup function from the EventsOn useEffect', () => {
    // EventsOn returns its own cancel fn — the panel must call it on
    // unmount or the listener leaks per remount (very visible during HMR).
    expect(SOURCE).toMatch(/const\s+cancel\s*=\s*EventsOn\(/)
    expect(SOURCE).toMatch(/return\s*\(\s*\)\s*=>\s*\{[\s\S]*?cancel\(\)/)
  })

  it('renders the permission warning row only when permissionError is set', () => {
    // Hidden-by-default per TASK-012 brief. The conditional render is the
    // contract; a refactor that always-renders the row (even greyed) trips
    // this expectation.
    expect(SOURCE).toMatch(/permissionError\s*&&/)
  })

  it('uses local useState to drive the warning row visibility', () => {
    // Same pattern OverlayPanel uses for hotkeyError — the listener body
    // calls setPermissionError, not a global store. Pin on the setter
    // being called from the EventsOn callback.
    expect(SOURCE).toMatch(/setPermissionError\(/)
  })

  it('deep-links to System Settings → Privacy → Screen Recording', () => {
    // The right destination is Screen Recording (not Microphone, not
    // Accessibility) — SCK gates on this specific permission. Pin on the
    // URL fragment so a copy-paste error to the wrong panel trips here.
    expect(SOURCE).toMatch(/BrowserOpenURL/)
    expect(SOURCE).toMatch(/Privacy_ScreenRecording/)
    expect(SOURCE).toMatch(/Open System Settings/)
  })
})

describe('MeetingPanel TASK-012 (keyword chip editor)', () => {
  it('adds a chip on Enter keypress in the draft input', () => {
    // Regression guard against a refactor that silently swaps the
    // keystroke (e.g. to comma-separator parsing). The Enter contract is
    // the documented UX in the plan brief.
    expect(SOURCE).toMatch(/\.key\s*===\s*['"]Enter['"]/)
  })

  it('normalises keywords to lowercase before appending', () => {
    // Matches Go-side keyword comparison behaviour (case-insensitive
    // substring match in TASK-011). Storing mixed-case would break the
    // calendar event filter.
    expect(SOURCE).toMatch(/toLowerCase\(\)/)
  })

  it('trims whitespace before appending so " review " doesn\'t become its own chip', () => {
    expect(SOURCE).toMatch(/\.trim\(\)/)
  })

  it('exposes a per-chip remove button', () => {
    expect(SOURCE).toMatch(/handleRemoveKeyword/)
  })
})

describe('MeetingPanel TASK-012 (tabpanel integration)', () => {
  it('renders exactly one tabpanel root keyed to activeTab !== "meeting"', () => {
    const matches = SOURCE.match(/role=['"]tabpanel['"]/g) ?? []
    expect(matches.length).toBe(1)
    expect(SOURCE).toMatch(/activeTab\s*!==\s*['"]meeting['"]/)
    expect(SOURCE).toMatch(/id=['"]settings-tab-panel-meeting['"]/)
  })

  it('is mounted in SettingsView.tsx via <MeetingPanel ... />', () => {
    // The tab swap must actually wire the panel into the parent layout
    // (otherwise the new tab id just renders a blank page).
    expect(SETTINGS_VIEW_SOURCE).toMatch(
      /import\s*\{\s*MeetingPanel\s*\}\s*from\s*['"]\.\/settings\/MeetingPanel['"]/,
    )
    expect(SETTINGS_VIEW_SOURCE).toMatch(/<MeetingPanel\b/)
  })

  it('registers the "meeting" tab id in SettingsTabs.tsx union + array', () => {
    // The SettingsTabs union must accept 'meeting' so the SettingsView
    // setActiveTab('meeting') call type-checks. Pin on both the union
    // literal and the array entry.
    expect(SETTINGS_TABS_SOURCE).toMatch(/['"]meeting['"]/)
    expect(SETTINGS_TABS_SOURCE).toMatch(/id:\s*['"]meeting['"]/)
  })
})

describe('MeetingPanel (Open notes folder button)', () => {
  // The OPEN FOLDER ↗ button reveals the configured (tilde-expanded) notes
  // directory in Finder via the OpenMeetingNotesFolder Go binding. The
  // binding side mkdir-p's the dir + shells out to `open` on macOS.
  it('calls OpenMeetingNotesFolder via the window.go.main.App runtime bridge', () => {
    // Runtime bridge pattern (same as MeetingPanel's permission-error CTA)
    // so a stale dev build degrades to console.warn instead of crashing.
    expect(SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.OpenMeetingNotesFolder/)
  })

  it('renders an OPEN FOLDER button labelled Reveal in Finder', () => {
    // Accept either the button text or the title attribute so a future
    // copy tweak that only adjusts capitalisation doesn't trip the test.
    expect(SOURCE).toMatch(/OPEN FOLDER|Open folder|Reveal in Finder/i)
  })
})

describe('MeetingPanel TASK-012 (defaults + resilience)', () => {
  it('falls back to ~/.jarvis/meetings when meetingNotesDir is empty', () => {
    // The Go side normalises empty → default in Load(), but the panel
    // must also render cleanly against a stale Wails build that has not
    // yet been regenerated. Pin on the literal default path.
    expect(SOURCE).toMatch(/~\/\.jarvis\/meetings/)
  })

  it('falls back to the documented seven keywords when the slice is empty', () => {
    // Default keyword list from TASK-001 — empty/nil falls back to these.
    expect(SOURCE).toMatch(/['"]standup['"]/)
    expect(SOURCE).toMatch(/['"]1:1['"]/)
    expect(SOURCE).toMatch(/['"]interview['"]/)
  })
})
