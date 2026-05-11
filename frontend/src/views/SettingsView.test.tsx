// ---------------------------------------------------------------------------
// SettingsView 5-tab IA — source-level contract test (TASK-016).
//
// Verifies:
//   1. All 5 tab labels (Connections, Voice, Behavior, Diagnostics, Advanced)
//      are referenced in the SettingsView (or its tab-bar sub-component).
//   2. Every pre-existing field from the old SettingsView still appears in
//      the source — regression check that the IA refactor did not silently
//      drop a setting. Per the gap doc (docs/settings-ui-gap.md) the fields
//      that must survive are:
//        - jarvisAPIKey (via JarvisSettings — checked indirectly via the
//          component mount)
//        - projectRootPaths
//        - notification keys (notificationsEnabled, notifyOnApproval,
//          notifyOnCompletion)
//        - mobile API token (RegenerateMobileToken / mobileInfo.token)
//        - jarvisVerbosity (via JarvisSettings mount)
//        - ApprovalRulesSettings (must be mounted somewhere)
//        - ciWatchEnabled
//        - jarvisVoice (via JarvisSettings mount)
//   3. The component imports useState (for tab state).
//   4. The 5 tabs are wired up via a tablist/tabpanel pair so DOM state can
//      be retained via `hidden` instead of remounting.
//
// Why a source-level test:
//   the frontend does not ship `jsdom` / `@testing-library/react`, so we
//   cannot mount and click tabs in this environment. The same `?raw`
//   import trick used by JarvisHudView.test.tsx (TASK-002) is sufficient
//   to catch the regression patterns this task introduces. The Vitest
//   instance picks up the file under `src/**/*.test.tsx` per the default
//   include pattern.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './SettingsView.tsx?raw'
import TABS_SOURCE from './settings/SettingsTabs.tsx?raw'

describe('SettingsView 5-tab IA (TASK-016)', () => {
  // -----------------------------------------------------------------
  // Tab shell
  // -----------------------------------------------------------------

  it('references all 5 tab labels', () => {
    // Tab labels live in SettingsTabs but the parent imports & uses the
    // <SettingsTabs/> component. We accept a match in either source so
    // future refactors that inline the tab bar still pass.
    const combined = SOURCE + '\n' + TABS_SOURCE
    expect(combined).toMatch(/Connections/)
    expect(combined).toMatch(/Voice/)
    expect(combined).toMatch(/Behavior/)
    expect(combined).toMatch(/Diagnostics/)
    expect(combined).toMatch(/Advanced/)
  })

  it('uses tablist / tabpanel ARIA roles', () => {
    const combined = SOURCE + '\n' + TABS_SOURCE
    expect(combined).toMatch(/role=['"]tablist['"]/)
    expect(combined).toMatch(/role=['"]tab['"]/)
    expect(combined).toMatch(/role=['"]tabpanel['"]/)
  })

  it('renders all panels and toggles via hidden (preserves form state)', () => {
    // The acceptance criterion is that tab switches MUST NOT remount
    // panels. The implementation strategy chosen is to render all panels
    // and toggle visibility with `hidden={activeTab !== ...}`. We assert
    // the `hidden=` attribute appears at least 5 times (one per panel).
    const hiddenMatches = SOURCE.match(/hidden=\{activeTab\s*!==/g) ?? []
    expect(hiddenMatches.length).toBeGreaterThanOrEqual(5)
  })

  it('imports useState for tab state', () => {
    // The component must hold tab state in React state. We pin on the
    // import line rather than the usage so the assertion is unambiguous.
    expect(SOURCE).toMatch(/import\s*\{[^}]*\buseState\b[^}]*\}\s*from\s*['"]react['"]/)
  })

  it('declares an activeTab state slot', () => {
    expect(SOURCE).toMatch(/useState<SettingsTabId>\(\s*['"]\w+['"]\s*\)/)
  })

  // -----------------------------------------------------------------
  // Pre-existing fields must survive the refactor (regression check)
  // -----------------------------------------------------------------

  it('retains projectRootPaths field', () => {
    expect(SOURCE).toMatch(/projectRootPaths/)
  })

  it('retains all three notification toggles', () => {
    expect(SOURCE).toMatch(/notificationsEnabled/)
    expect(SOURCE).toMatch(/notifyOnApproval/)
    expect(SOURCE).toMatch(/notifyOnCompletion/)
  })

  it('retains mobile API token cluster (regenerate + reveal)', () => {
    expect(SOURCE).toMatch(/RegenerateMobileToken/)
    expect(SOURCE).toMatch(/mobileInfo\.token/)
    expect(SOURCE).toMatch(/tokenRevealed/)
  })

  it('retains ciWatchEnabled + ciProvider fields', () => {
    expect(SOURCE).toMatch(/ciWatchEnabled/)
    expect(SOURCE).toMatch(/ciProvider/)
  })

  it('retains scanIntervalSeconds field', () => {
    expect(SOURCE).toMatch(/scanIntervalSeconds/)
  })

  it('retains preferredTerminal field', () => {
    expect(SOURCE).toMatch(/preferredTerminal/)
  })

  it('retains defaultAgent field', () => {
    expect(SOURCE).toMatch(/defaultAgent/)
  })

  it('retains defaultCommand field', () => {
    expect(SOURCE).toMatch(/defaultCommand/)
  })

  it('retains dotClaudeSourcePath field', () => {
    expect(SOURCE).toMatch(/dotClaudeSourcePath/)
  })

  // -----------------------------------------------------------------
  // JarvisSettings hosts jarvisAPIKey, jarvisVoice, jarvisVerbosity,
  // jarvisAmbientEnabled, jarvisEnabled. The parent must still mount it.
  // -----------------------------------------------------------------

  it('still mounts <JarvisSettings/> so jarvisAPIKey / jarvisVoice / jarvisVerbosity survive', () => {
    expect(SOURCE).toMatch(/import\s*\{\s*JarvisSettings\s*\}\s*from\s*['"][^'"]*JarvisSettings['"]/)
    expect(SOURCE).toMatch(/<JarvisSettings\b/)
  })

  it('still mounts <ApprovalRulesSettings/>', () => {
    expect(SOURCE).toMatch(
      /import\s*\{\s*ApprovalRulesSettings\s*\}\s*from\s*['"][^'"]*ApprovalRulesSettings['"]/,
    )
    expect(SOURCE).toMatch(/<ApprovalRulesSettings\b/)
  })

  // -----------------------------------------------------------------
  // Empty-section placeholders for downstream tasks
  // -----------------------------------------------------------------

  it('contains TASK-NNN placeholder markers for downstream agents', () => {
    // Each gap that TASKs 017-024 will fill should leave a visible note.
    // We assert the presence of at least the headline TASK IDs the gap
    // doc maps to each tab.
    expect(SOURCE).toMatch(/TASK-017/)
    expect(SOURCE).toMatch(/TASK-018/)
    expect(SOURCE).toMatch(/TASK-019/)
    expect(SOURCE).toMatch(/TASK-020/)
    expect(SOURCE).toMatch(/TASK-021/)
    expect(SOURCE).toMatch(/TASK-022/)
    expect(SOURCE).toMatch(/TASK-023/)
  })
})
