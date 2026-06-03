// ---------------------------------------------------------------------------
// PermissionsPanel — source-level contract test (TASK-017, v0.3.0 P1).
//
// The frontend test harness ships without jsdom, so we use the same `?raw`
// import trick documented in SettingsView.test.tsx + VoicePanel.test.tsx to
// assert structural invariants on the rendered source. Sufficient to catch
// the regressions TASK-017 acceptance criteria call out:
//   1. All 24 tool names (15 mac_* + 9 spotify_*) appear in the source.
//   2. GetMacctlPolicy is called from a useEffect (read on mount).
//   3. SetMacctlPolicy is wired into the onChange / onClick path.
//   4. Segmented control offers all three options: allow / ask / deny.
//   5. Group titles per the spec (Spotify, Apps, Audio + display, Files +
//      clipboard, Screenshots + shortcuts) are present.
//
// The test file is co-located with the panel under
// frontend/src/views/settings/ so the existing `vitest.config` glob
// (src/**/*.test.tsx) picks it up automatically.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './PermissionsPanel.tsx?raw'
import TABS_SOURCE from './SettingsTabs.tsx?raw'

describe('PermissionsPanel TASK-017 (tool catalogue)', () => {
  // --- Spotify (9 tools) -----------------------------------------------
  it('lists all 9 spotify_* tool names', () => {
    expect(SOURCE).toMatch(/'spotify_search_and_play'/)
    expect(SOURCE).toMatch(/'spotify_pause'/)
    expect(SOURCE).toMatch(/'spotify_resume'/)
    expect(SOURCE).toMatch(/'spotify_skip'/)
    expect(SOURCE).toMatch(/'spotify_previous'/)
    expect(SOURCE).toMatch(/'spotify_what_is_playing'/)
    expect(SOURCE).toMatch(/'spotify_set_volume'/)
    expect(SOURCE).toMatch(/'spotify_like_current'/)
    expect(SOURCE).toMatch(/'spotify_queue'/)
  })

  // --- mac_* Apps + windows (3 tools) ----------------------------------
  it('lists all 3 mac_* apps/window tool names', () => {
    expect(SOURCE).toMatch(/'mac_open_app'/)
    expect(SOURCE).toMatch(/'mac_quit_app'/)
    expect(SOURCE).toMatch(/'mac_focus_window'/)
  })

  // --- mac_* Audio + display (5 tools) ---------------------------------
  it('lists all 5 mac_* audio/display tool names', () => {
    expect(SOURCE).toMatch(/'mac_set_volume'/)
    expect(SOURCE).toMatch(/'mac_mute'/)
    expect(SOURCE).toMatch(/'mac_unmute'/)
    expect(SOURCE).toMatch(/'mac_set_brightness'/)
    expect(SOURCE).toMatch(/'mac_toggle_dnd'/)
  })

  // --- mac_* Files + clipboard (4 tools) -------------------------------
  it('lists all 4 mac_* files/clipboard tool names', () => {
    expect(SOURCE).toMatch(/'mac_open_path'/)
    expect(SOURCE).toMatch(/'mac_spotlight'/)
    expect(SOURCE).toMatch(/'mac_clipboard_get'/)
    expect(SOURCE).toMatch(/'mac_clipboard_set'/)
  })

  // --- mac_* Screenshots + shortcuts (3 tools) -------------------------
  it('lists all 3 mac_* screenshots/shortcuts tool names', () => {
    expect(SOURCE).toMatch(/'mac_screenshot'/)
    expect(SOURCE).toMatch(/'mac_list_shortcuts'/)
    expect(SOURCE).toMatch(/'mac_run_shortcut'/)
  })

  // --- Total count guard -----------------------------------------------
  it('has exactly 24 tool definitions in TOOL_GROUPS', () => {
    // Sum the catalogue by counting `{ name: 'foo'` occurrences in the
    // source. Anchoring on the literal-quoted name property keeps stray
    // matches (e.g. DEFAULT_DECISIONS keys) from inflating the count.
    const matches = SOURCE.match(/\{\s*name:\s*'[a-z_]+',\s*desc:/g) ?? []
    expect(matches.length).toBe(24)
  })

  it('groups tools under the five expected category titles', () => {
    expect(SOURCE).toMatch(/title:\s*'Spotify'/)
    expect(SOURCE).toMatch(/title:\s*'Apps'/)
    expect(SOURCE).toMatch(/title:\s*'Audio \+ display'/)
    expect(SOURCE).toMatch(/title:\s*'Files \+ clipboard'/)
    expect(SOURCE).toMatch(/title:\s*'Screenshots \+ shortcuts'/)
  })
})

describe('PermissionsPanel TASK-017 (Wails bindings wiring)', () => {
  it('calls GetMacctlPolicy from a useEffect on mount', () => {
    // The binding must be referenced, and the call must sit inside the
    // useEffect block (we can't introspect AST so we pin the call site +
    // the useEffect hook independently and rely on the small surface area
    // of the file to keep the pair coupled).
    expect(SOURCE).toMatch(/GetMacctlPolicy/)
    expect(SOURCE).toMatch(/useEffect\s*\(\s*\(\)\s*=>/)
    // The body must invoke GetMacctlPolicy() (with parens) — a bare import
    // of the symbol isn't enough to satisfy this pin.
    expect(SOURCE).toMatch(/\.GetMacctlPolicy\s*\(\s*\)/)
  })

  it('wires SetMacctlPolicy into the onClick / handleChange path', () => {
    expect(SOURCE).toMatch(/SetMacctlPolicy/)
    // The call must accept (tool, decision) — we pin on the parameter pair.
    expect(SOURCE).toMatch(/\.SetMacctlPolicy\s*\(\s*tool\s*,\s*next\s*\)/)
    // The segmented control onClick must dispatch handleChange.
    expect(SOURCE).toMatch(/handleChange\s*\(/)
    expect(SOURCE).toMatch(/onClick=\{[^}]*handleChange\(tool\.name,\s*opt\)/)
  })

  it('resolves the bindings via the window.go.main.App runtime guard', () => {
    // Same pattern VoicePanel uses — keeps the panel buildable on branches
    // where the generated App.d.ts hasn't been refreshed yet.
    expect(SOURCE).toMatch(/macctlBindings\(\)/)
    expect(SOURCE).toMatch(/go\?\.main\?\.App/)
  })

  it('reverts the UI to the prior value when SetMacctlPolicy throws', () => {
    // Pin on the catch block restoring the prior decision so optimistic
    // updates can't strand the UI in a state the daemon disagrees with.
    expect(SOURCE).toMatch(/catch\s*\(\s*err\s*\)/)
    expect(SOURCE).toMatch(/setPolicy\(\s*\(p\)\s*=>\s*\(\{[^}]*\[tool\]:\s*prev/)
  })

  it('shows a "Saved" toast on success and an error toast on failure', () => {
    // We use the [\s\S]*? non-greedy spanner so template literal `${tool}`
    // (which contains a `}`) doesn't prematurely terminate the match.
    expect(SOURCE).toMatch(/setToast\(\s*\{[\s\S]*?type:\s*'success'/)
    expect(SOURCE).toMatch(/setToast\(\s*\{[\s\S]*?type:\s*'error'/)
    // The success message must mention "Saved" so the user gets
    // unambiguous confirmation.
    expect(SOURCE).toMatch(/`Saved/)
  })
})

describe('PermissionsPanel TASK-017 (segmented control)', () => {
  it('declares DECISIONS as the three-tuple allow/ask/deny', () => {
    expect(SOURCE).toMatch(/DECISIONS[^=]*=\s*\['allow',\s*'ask',\s*'deny'\]/)
  })

  it('renders a button per Decision option inside a radiogroup', () => {
    // We pin on the radiogroup role + the map over DECISIONS so the three
    // options always render together — a refactor that swaps a single
    // <select> in here would have to delete this expectation explicitly.
    expect(SOURCE).toMatch(/role=['"]radiogroup['"]/)
    expect(SOURCE).toMatch(/DECISIONS\.map\(/)
    expect(SOURCE).toMatch(/role=['"]radio['"]/)
  })

  it('marks the active decision via aria-checked={isActive}', () => {
    expect(SOURCE).toMatch(/aria-checked=\{isActive\}/)
  })

  it('uses the Decision type alias bound to the three string literals', () => {
    expect(SOURCE).toMatch(/type Decision = ['"]allow['"]\s*\|\s*['"]ask['"]\s*\|\s*['"]deny['"]/)
  })
})

describe('PermissionsPanel TASK-017 (tabpanel integration)', () => {
  it('renders exactly one tabpanel root with role="tabpanel"', () => {
    const matches = SOURCE.match(/role=['"]tabpanel['"]/g) ?? []
    expect(matches.length).toBe(1)
  })

  it('hides the panel when activeTab !== "permissions"', () => {
    expect(SOURCE).toMatch(/activeTab\s*!==\s*['"]permissions['"]/)
    expect(SOURCE).toMatch(/id=['"]settings-tab-panel-permissions['"]/)
  })

  it('registers a "permissions" tab in SettingsTabs', () => {
    // SettingsTabs.tsx must include the Permissions tab in the SETTINGS_TABS
    // constant + the SettingsTabId union, otherwise SettingsView can't
    // route the user to this panel.
    expect(TABS_SOURCE).toMatch(/['"]permissions['"]/)
    expect(TABS_SOURCE).toMatch(/label:\s*['"]Permissions['"]/)
  })
})

describe('PermissionsPanel TASK-017 (aesthetic / structural pinning)', () => {
  it('uses the holo-panel cyan-accent class for each section', () => {
    // Match the existing VoicePanel / BehaviorPanel styling so the new tab
    // doesn't look out of place. Two literal occurrences are sufficient
    // (one in the header, one inside the TOOL_GROUPS.map() body which
    // renders five times at runtime).
    const matches = SOURCE.match(/className=['"]holo-panel/g) ?? []
    expect(matches.length).toBeGreaterThanOrEqual(2)
  })

  it('explains the default behaviour in the header copy', () => {
    expect(SOURCE).toMatch(/ask before running by default/)
  })

  it('references the policy storage path in the UI', () => {
    expect(SOURCE).toMatch(/~\/\.jarvis\/policy\.json/)
  })

  it('uses monospace SF Mono / Menlo for tool names', () => {
    expect(SOURCE).toMatch(/'SF Mono', 'Menlo', monospace/)
  })
})
