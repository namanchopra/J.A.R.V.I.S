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

// ---------------------------------------------------------------------------
// TASK-035 (v0.4.0 Windows port) -- Source-level coverage for the Windows
// copy variants delivered by TASK-032 (ms-settings: deep-link CTA). Pinning
// is intentionally source-string based (the same ?raw pattern the
// TASK-017 block above uses) so the test stays jsdom-free; sibling
// MeetingPanel.test.tsx + OverlayPanel.test.tsx follow the same convention
// for their Windows variants (TASK-045 / TASK-036).
//
// Acceptance criteria pinned here:
//   1. "ms-settings:privacy-microphone in tests"
//   2. "All 490+ existing tests pass" (TASK-017 block above must remain
//      untouched -- this section is append-only)
//   3. Failure case: platform-detection branch falls through on Linux
//      (i.e. the macOS branch renders when platform !== 'windows', which
//      includes Linux + any unrecognised GOOS value).
// ---------------------------------------------------------------------------

describe('PermissionsPanel TASK-035 (Windows ms-settings copy)', () => {
  it('imports Environment from the Wails runtime for platform detection', () => {
    // Pin on the import line + the call site so a refactor that silently
    // drops platform detection (and thus always falls through to macOS)
    // surfaces here rather than at runtime on a Windows user's machine.
    expect(SOURCE).toMatch(/Environment/)
    expect(SOURCE).toMatch(/from\s+['"]\.\.\/\.\.\/\.\.\/wailsjs\/runtime\/runtime['"]/)
    expect(SOURCE).toMatch(/Environment\(\)/)
  })

  it('treats "windows" as the trigger value for the Windows UX branch', () => {
    // Wails reports runtime.GOOS verbatim ('darwin', 'windows', 'linux').
    // Pin on both the literal + the isWindows flag so a swap to a
    // different string (e.g. 'win32') or a renamed flag trips here.
    expect(SOURCE).toMatch(/['"]windows['"]/)
    expect(SOURCE).toMatch(/isWindows/)
    expect(SOURCE).toMatch(/platform\s*===\s*['"]windows['"]/)
  })

  it('deep-links to ms-settings:privacy-microphone (acceptance criterion)', () => {
    // The destination URI is the user-visible contract. It must be the
    // microphone privacy panel (not camera, not general privacy root).
    // Same scheme MeetingPanel.tsx adopts on Windows (TASK-045).
    expect(SOURCE).toMatch(/ms-settings:privacy-microphone/)
    // The URI must be bound to a constant referenced by BrowserOpenURL --
    // pin on the constant name to keep the wiring traceable.
    expect(SOURCE).toMatch(/WINDOWS_SETTINGS_MICROPHONE_URL\s*=\s*['"]ms-settings:privacy-microphone['"]/)
    expect(SOURCE).toMatch(/BrowserOpenURL\(WINDOWS_SETTINGS_MICROPHONE_URL\)/)
  })

  it('renders a distinct data-testid for the Windows deep-link row', () => {
    // The Windows-only row uses a dedicated data-testid so SettingsView /
    // end-to-end tests can scope by platform without false positives from
    // the macOS row. Pin on the literal so a rename surfaces here.
    expect(SOURCE).toMatch(/data-testid=['"]permissions-os-deeplink-windows['"]/)
    expect(SOURCE).toMatch(/data-testid=['"]permissions-open-windows-microphone['"]/)
  })

  it('uses the "Open Windows Settings" CTA label on the Windows branch', () => {
    // The CTA label is the user-visible contract -- localised copy changes
    // must trip this test so a maintainer reviews the Windows screenshot.
    expect(SOURCE).toMatch(/Open Windows Settings/)
  })

  it('shows fallback copy for the group-policy-blocked failure case', () => {
    // Failure-mode acceptance criterion ("group-policy-blocked URI shows
    // fallback text"): a locked-down corp environment can disable the
    // ms-settings: handler, in which case BrowserOpenURL silently no-ops.
    // The Windows branch surfaces a fallback row instructing the user to
    // search the Start menu manually. Pin on the dedicated data-testid +
    // a fragment of the helper copy so a rewrite that drops the fallback
    // surfaces loudly.
    expect(SOURCE).toMatch(/data-testid=['"]permissions-os-deeplink-windows-fallback['"]/)
    expect(SOURCE).toMatch(/group policy/)
    expect(SOURCE).toMatch(/Microphone privacy settings/)
  })

  it('keeps the macOS deep link unchanged (acceptance: macOS deep link unchanged)', () => {
    // The macOS branch must still render the existing System Settings row
    // with the x-apple.systempreferences: URI + the "Open System Settings"
    // CTA so existing Mac users see no change. Pin on the constant name,
    // the URI, the data-testid + the CTA copy.
    expect(SOURCE).toMatch(/MACOS_SETTINGS_MICROPHONE_URL\s*=/)
    expect(SOURCE).toMatch(/x-apple\.systempreferences:com\.apple\.preference\.security\?Privacy_Microphone/)
    expect(SOURCE).toMatch(/data-testid=['"]permissions-os-deeplink-darwin['"]/)
    expect(SOURCE).toMatch(/data-testid=['"]permissions-open-macos-microphone['"]/)
    expect(SOURCE).toMatch(/Open System Settings/)
  })

  it('defaults the platform state to "darwin" so detection failure falls back to macOS', () => {
    // Failure-mode safety: if the Wails runtime is unavailable (SSR / test
    // harness / pre-init), the panel must render the macOS UX rather than
    // showing Windows controls to a Mac user. This is the same convention
    // MeetingPanel + OverlayPanel adopt. The Linux failure-case acceptance
    // criterion ("platform-detection branch falls through on Linux")
    // collapses to this same default because the platform value 'linux'
    // also takes the !isWindows branch -- there is no Linux-only UI today.
    expect(SOURCE).toMatch(/useState<string>\(\s*['"]darwin['"]\s*\)/)
  })

  it('gates the deep-link rows so only one platform branch renders at a time', () => {
    // The two branches are mutually exclusive: the JSX uses a ternary on
    // isWindows so a maintainer that accidentally swaps it for `&&` (which
    // would render both rows on macOS) trips here.
    expect(SOURCE).toMatch(/isWindows\s*\?\s*\(/)
    // The else branch must wrap the macOS row -- pin on the data-testid so
    // an inverted condition (isWindows ? darwin : windows) trips here.
    expect(SOURCE).toMatch(/:\s*\(\s*<div[\s\S]*?data-testid=['"]permissions-os-deeplink-darwin['"]/)
  })

  it('falls through to the macOS branch on Linux / unknown platforms', () => {
    // Failure-case acceptance criterion: "platform-detection branch falls
    // through on Linux". The only positive check in the source is
    // `platform === 'windows'` -- every other value (including 'linux',
    // 'freebsd', '' and any malformed string) renders the macOS branch.
    // Pin on the absence of any Linux-specific branch + the strict
    // equality so a future maintainer that adds e.g. `||
    // platform === 'linux'` to the Windows branch trips here and has to
    // explicitly update this expectation.
    expect(SOURCE).not.toMatch(/['"]linux['"]/)
    expect(SOURCE).not.toMatch(/isLinux/)
    // The Windows branch must be an exact equality check, not a substring
    // / startsWith match that could accidentally swallow Linux values.
    const windowsChecks = SOURCE.match(/platform\s*===\s*['"]windows['"]/g) ?? []
    expect(windowsChecks.length).toBeGreaterThanOrEqual(1)
  })

  it('exposes the OS deep-link wrapper section with a stable data-testid', () => {
    // The wrapper <section> data-testid is shared across both platforms so
    // a single locator can find the row regardless of the user's OS. Pin
    // it here so a rename trips both the platform branches in one go.
    expect(SOURCE).toMatch(/data-testid=['"]permissions-os-deeplink['"]/)
  })
})
