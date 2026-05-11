// ---------------------------------------------------------------------------
// SettingsView 5-tab IA — source-level contract test (TASK-016 + Wave 3 prep).
//
// After the Wave 3 prep split, SettingsView is a thin shell that mounts five
// separate panel components (Connections, Voice, Behavior, Diagnostics,
// Advanced). The "fields must still exist" regression check therefore has to
// look across the SHELL plus all five panel files — a field can live in any
// of them.
//
// Verifies:
//   1. All 5 tab labels are referenced in SettingsView/SettingsTabs.
//   2. Every pre-existing field still appears somewhere in
//      frontend/src/views/ (shell + panels).
//   3. The component imports useState (for tab state).
//   4. The 5 tabs are wired up via a tablist/tabpanel pair so DOM state can
//      be retained via `hidden` instead of remounting.
//   5. All five panel files are imported in SettingsView.tsx.
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
import CONNECTIONS_SOURCE from './settings/ConnectionsPanel.tsx?raw'
import VOICE_SOURCE from './settings/VoicePanel.tsx?raw'
import BEHAVIOR_SOURCE from './settings/BehaviorPanel.tsx?raw'
import DIAGNOSTICS_SOURCE from './settings/DiagnosticsPanel.tsx?raw'
import ADVANCED_SOURCE from './settings/AdvancedPanel.tsx?raw'

// All source surfaces concatenated — used for "field exists somewhere"
// regression checks where the field may have migrated to a panel during the
// Wave 3 prep split. Specific structural assertions (panel imports, hidden=
// count, useState in shell, etc.) still pin against the relevant file alone.
const COMBINED =
  SOURCE +
  '\n' +
  TABS_SOURCE +
  '\n' +
  CONNECTIONS_SOURCE +
  '\n' +
  VOICE_SOURCE +
  '\n' +
  BEHAVIOR_SOURCE +
  '\n' +
  DIAGNOSTICS_SOURCE +
  '\n' +
  ADVANCED_SOURCE

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
    // role="tablist" + role="tab" live in SettingsTabs; role="tabpanel"
    // now lives in each panel file. Search the union.
    expect(COMBINED).toMatch(/role=['"]tablist['"]/)
    expect(COMBINED).toMatch(/role=['"]tab['"]/)
    expect(COMBINED).toMatch(/role=['"]tabpanel['"]/)
  })

  it('renders all panels and toggles via hidden (preserves form state)', () => {
    // The acceptance criterion is that tab switches MUST NOT remount
    // panels. The implementation strategy chosen is to render all panels
    // and toggle visibility with `hidden={activeTab !== ...}`. Each panel
    // file contains exactly one occurrence — we assert at least 5 across
    // the combined sources.
    const hiddenMatches = COMBINED.match(/hidden=\{activeTab\s*!==/g) ?? []
    expect(hiddenMatches.length).toBeGreaterThanOrEqual(5)
  })

  it('imports useState for tab state', () => {
    // The shell must hold tab state in React state. We pin on the import
    // line rather than the usage so the assertion is unambiguous.
    expect(SOURCE).toMatch(/import\s*\{[^}]*\buseState\b[^}]*\}\s*from\s*['"]react['"]/)
  })

  it('declares an activeTab state slot', () => {
    expect(SOURCE).toMatch(/useState<SettingsTabId>\(\s*['"]\w+['"]\s*\)/)
  })

  // -----------------------------------------------------------------
  // The 5 panel files must be imported in SettingsView (Wave 3 prep
  // acceptance criterion).
  // -----------------------------------------------------------------

  it('imports all 5 panel components in SettingsView', () => {
    expect(SOURCE).toMatch(
      /import\s*\{\s*ConnectionsPanel\s*\}\s*from\s*['"]\.\/settings\/ConnectionsPanel['"]/,
    )
    expect(SOURCE).toMatch(
      /import\s*\{\s*VoicePanel\s*\}\s*from\s*['"]\.\/settings\/VoicePanel['"]/,
    )
    expect(SOURCE).toMatch(
      /import\s*\{\s*BehaviorPanel\s*\}\s*from\s*['"]\.\/settings\/BehaviorPanel['"]/,
    )
    expect(SOURCE).toMatch(
      /import\s*\{\s*DiagnosticsPanel\s*\}\s*from\s*['"]\.\/settings\/DiagnosticsPanel['"]/,
    )
    expect(SOURCE).toMatch(
      /import\s*\{\s*AdvancedPanel\s*\}\s*from\s*['"]\.\/settings\/AdvancedPanel['"]/,
    )
  })

  // -----------------------------------------------------------------
  // Pre-existing fields must survive the refactor (regression check).
  // Each field may live in the shell OR a panel — search the union.
  // -----------------------------------------------------------------

  it('retains projectRootPaths field', () => {
    expect(COMBINED).toMatch(/projectRootPaths/)
  })

  it('retains all three notification toggles', () => {
    expect(COMBINED).toMatch(/notificationsEnabled/)
    expect(COMBINED).toMatch(/notifyOnApproval/)
    expect(COMBINED).toMatch(/notifyOnCompletion/)
  })

  it('retains mobile API token cluster (regenerate + reveal)', () => {
    expect(COMBINED).toMatch(/RegenerateMobileToken/)
    expect(COMBINED).toMatch(/mobileInfo\.token/)
    expect(COMBINED).toMatch(/tokenRevealed/)
  })

  it('retains ciWatchEnabled + ciProvider fields', () => {
    expect(COMBINED).toMatch(/ciWatchEnabled/)
    expect(COMBINED).toMatch(/ciProvider/)
  })

  it('retains scanIntervalSeconds field', () => {
    expect(COMBINED).toMatch(/scanIntervalSeconds/)
  })

  it('retains preferredTerminal field', () => {
    expect(COMBINED).toMatch(/preferredTerminal/)
  })

  it('retains defaultAgent field', () => {
    expect(COMBINED).toMatch(/defaultAgent/)
  })

  it('retains defaultCommand field', () => {
    expect(COMBINED).toMatch(/defaultCommand/)
  })

  it('retains dotClaudeSourcePath field', () => {
    expect(COMBINED).toMatch(/dotClaudeSourcePath/)
  })

  // -----------------------------------------------------------------
  // JarvisSettings hosts jarvisAPIKey, jarvisVoice, jarvisVerbosity,
  // jarvisAmbientEnabled, jarvisEnabled. It must still be mounted —
  // VoicePanel is the new home per Wave 3 prep.
  // -----------------------------------------------------------------

  it('still mounts <JarvisSettings/> so jarvisAPIKey / jarvisVoice / jarvisVerbosity survive', () => {
    expect(COMBINED).toMatch(
      /import\s*\{\s*JarvisSettings\s*\}\s*from\s*['"][^'"]*JarvisSettings['"]/,
    )
    expect(COMBINED).toMatch(/<JarvisSettings\b/)
  })

  it('still mounts <ApprovalRulesSettings/>', () => {
    expect(COMBINED).toMatch(
      /import\s*\{\s*ApprovalRulesSettings\s*\}\s*from\s*['"][^'"]*ApprovalRulesSettings['"]/,
    )
    expect(COMBINED).toMatch(/<ApprovalRulesSettings\b/)
  })

  // -----------------------------------------------------------------
  // Empty-section placeholders for downstream tasks. The placeholders
  // now live in the per-panel files, so we search the union.
  // -----------------------------------------------------------------

  it('contains TASK-NNN placeholder markers for downstream agents', () => {
    // Each gap that TASKs 017-024 will fill should leave a visible note.
    // We assert the presence of at least the headline TASK IDs the gap
    // doc maps to each tab.
    expect(COMBINED).toMatch(/TASK-017/)
    expect(COMBINED).toMatch(/TASK-018/)
    expect(COMBINED).toMatch(/TASK-019/)
    expect(COMBINED).toMatch(/TASK-020/)
    expect(COMBINED).toMatch(/TASK-021/)
    expect(COMBINED).toMatch(/TASK-022/)
    expect(COMBINED).toMatch(/TASK-023/)
  })
})

// ---------------------------------------------------------------------------
// v0.1.2: daemon-restart-required banner.
//
// SaveConfig now returns `{ daemonRestartNeeded: boolean }`. When true, the
// shell renders an amber banner above the sticky save bar with two buttons:
//   - "Apply now" calls RestartJarvis() (guarded for stale bindings)
//   - "Apply later" dismisses the banner and waits for the next manual
//     restart
//
// During an in-flight RestartJarvis the buttons disable and the label flips
// to `▸ RESTARTING…`. After success a cyan toast `Daemon reconnected`
// surfaces via the existing 3s showMsg path.
// ---------------------------------------------------------------------------

describe('SettingsView v0.1.2 (daemon-restart-required banner)', () => {
  it('declares restartNeeded + restarting state slots', () => {
    expect(SOURCE).toMatch(/setRestartNeeded/)
    expect(SOURCE).toMatch(/setRestarting/)
  })

  it('reads daemonRestartNeeded from SaveConfig response', () => {
    // The handler narrows the binding response through unknown to be
    // resilient to a stale `Promise<void>` binding. Pin on the field
    // access so any drop-back to the old `await SaveConfig(cfg)` line
    // (no return capture) surfaces here.
    expect(SOURCE).toMatch(/daemonRestartNeeded/)
    expect(SOURCE).toMatch(/SaveConfig\(cfg\)/)
  })

  it('renders the amber DAEMON RESTART REQUIRED banner conditionally', () => {
    // The banner ONLY renders when restartNeeded === true. We pin on the
    // canonical headline string AND the conditional render guard.
    expect(SOURCE).toMatch(/DAEMON RESTART REQUIRED/)
    expect(SOURCE).toMatch(/\{restartNeeded\s*&&/)
  })

  it('uses the amber accent (--accent-amber) for the banner palette', () => {
    expect(SOURCE).toMatch(/--accent-amber/)
  })

  it('renders both Apply now and Apply later buttons', () => {
    expect(SOURCE).toMatch(/Apply now/)
    expect(SOURCE).toMatch(/Apply later/)
  })

  it('references RestartJarvis and guards on its presence at call time', () => {
    // The bindings may be stale in this sandbox. The handler resolves
    // RestartJarvis via window.go.main.App and falls back to a toast if
    // missing. Pin on both the resolution pattern AND the typeof guard.
    expect(SOURCE).toMatch(/RestartJarvis/)
    expect(SOURCE).toMatch(/typeof\s+app\?\.RestartJarvis\s*===\s*['"]function['"]/)
  })

  it('disables banner buttons + shows ▸ RESTARTING… during in-flight restart', () => {
    expect(SOURCE).toMatch(/▸ RESTARTING…/)
    expect(SOURCE).toMatch(/disabled=\{restarting\}/)
  })

  it('emits a "Daemon reconnected" toast on successful restart', () => {
    expect(SOURCE).toMatch(/Daemon reconnected/)
  })

  it('Apply later dismisses the banner without invoking RestartJarvis', () => {
    // The "Apply later" handler sets restartNeeded to false without calling
    // the binding. We pin on the handler name + its setRestartNeeded(false)
    // call site.
    expect(SOURCE).toMatch(/handleApplyLater/)
    expect(SOURCE).toMatch(/setRestartNeeded\(false\)/)
  })
})
