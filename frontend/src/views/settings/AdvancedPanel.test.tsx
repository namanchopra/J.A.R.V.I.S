// ---------------------------------------------------------------------------
// AdvancedPanel — source-level contract test (TASK-023).
//
// The frontend test harness ships without jsdom, so we lean on the same
// `?raw` import trick used by SettingsView.test.tsx, BehaviorPanel.test.tsx,
// and JarvisHudView.test.tsx to assert that the rendered source contains the
// required field bindings, JSX nodes, and Wails binding calls. This catches
// the regression patterns TASK-023 introduces without requiring a real DOM
// mount.
//
// TASK-023 — Advanced tab config IO:
//   - Four action buttons: Export config, Import config, Reset to defaults,
//     and Browse (for dotClaudeSourcePath)
//   - "Preserve API keys" checkbox in the Reset confirmation flow
//   - Confirmation modal pattern for both Import and Reset (destructive ops)
//   - Imports the four Wails bindings (ExportConfig, ImportConfig,
//     ResetConfig, OpenFileForImport) plus BrowseForDirectory for the
//     dotClaude picker
//   - Pre-existing Mobile App token cluster regression-checks (must NOT be
//     removed by this task — see header comment in AdvancedPanel.tsx)
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './AdvancedPanel.tsx?raw'

describe('AdvancedPanel TASK-023 (config IO action buttons)', () => {
  it('renders all four action button labels', () => {
    expect(SOURCE).toMatch(/Export config/)
    expect(SOURCE).toMatch(/Import config/)
    expect(SOURCE).toMatch(/Reset to defaults/)
    // Browse for dotClaudeSource — same label convention as BehaviorPanel.
    expect(SOURCE).toMatch(/Browse\.\.\./)
  })

  it('imports the four config-IO Wails bindings plus BrowseForDirectory', () => {
    // We allow a single multi-line import with all five names so the source
    // can group them however it likes — the assertion just requires each
    // name to appear in some import-from-App statement.
    expect(SOURCE).toMatch(
      /import\s*\{[\s\S]*?BrowseForDirectory[\s\S]*?\}\s*from\s*['"][^'"]*wailsjs\/go\/main\/App['"]/,
    )
    expect(SOURCE).toMatch(/\bExportConfig\b/)
    expect(SOURCE).toMatch(/\bImportConfig\b/)
    expect(SOURCE).toMatch(/\bResetConfig\b/)
    expect(SOURCE).toMatch(/\bOpenFileForImport\b/)
  })

  it('binds the dotClaude input to cfg.dotClaudeSourcePath', () => {
    expect(SOURCE).toMatch(/cfg\.dotClaudeSourcePath/)
    // Setter writes back to dotClaudeSourcePath.
    expect(SOURCE).toMatch(/dotClaudeSourcePath:\s*e\.target\.value/)
  })

  it('invokes BrowseForDirectory from the dotClaude browse handler', () => {
    expect(SOURCE).toMatch(/BrowseForDirectory\s*\(/)
  })

  it('uses a confirmation modal pattern for destructive ops', () => {
    // Both Import + Reset must go through a confirm state slot before
    // calling the backend. We pin on the two state setters that drive
    // each modal so a future refactor that renames either state slot is
    // forced to update this test as well.
    expect(SOURCE).toMatch(/confirmImport/)
    expect(SOURCE).toMatch(/confirmReset/)
    // Modal nodes carry role="dialog" + aria-modal="true".
    expect(SOURCE).toMatch(/role=['"]dialog['"]/)
    expect(SOURCE).toMatch(/aria-modal=['"]true['"]/)
  })

  it('renders a "Preserve API keys" checkbox in the Reset modal', () => {
    // Label text from the spec.
    expect(SOURCE).toMatch(/Preserve API keys/)
    // The checkbox must be bound to a boolean state slot.
    expect(SOURCE).toMatch(/preserveKeys/)
    // Default value should be true so the safer option is one-click.
    expect(SOURCE).toMatch(/useState<boolean>\(true\)|useState\(true\)/)
  })

  it('passes the preserveKeys flag through to ResetConfig', () => {
    expect(SOURCE).toMatch(/ResetConfig\(\s*preserveKeys\s*\)/)
  })

  it('Export action does NOT have a confirmation modal (non-destructive)', () => {
    // Export writes a fresh file at a user-chosen location — there's no
    // existing data to clobber, so the spec says no confirmation. We pin
    // by asserting the export handler calls ExportConfig directly without
    // routing through any "confirmExport" state slot.
    expect(SOURCE).toMatch(/ExportConfig\s*\(/)
    expect(SOURCE).not.toMatch(/confirmExport/)
  })
})

describe('AdvancedPanel TASK-016 prep regression (Mobile App token cluster)', () => {
  // The Mobile App cluster was seeded by TASK-016 prep and MUST survive
  // the TASK-023 additions. These assertions mirror the SettingsView.test
  // checks but pin against AdvancedPanel.tsx directly so the regression
  // is localized to this file.

  it('retains the Mobile App section heading and copy', () => {
    expect(SOURCE).toMatch(/Mobile App/)
    expect(SOURCE).toMatch(/Bearer Token/)
  })

  it('retains the Regenerate / Reveal / Copy Token controls', () => {
    expect(SOURCE).toMatch(/onRegenerateToken/)
    expect(SOURCE).toMatch(/tokenRevealed/)
    expect(SOURCE).toMatch(/Copy Token/)
    expect(SOURCE).toMatch(/Regenerate/)
  })

  it('still references mobileInfo for token + LAN address rendering', () => {
    expect(SOURCE).toMatch(/mobileInfo\.token/)
    expect(SOURCE).toMatch(/mobileInfo\.ips/)
    expect(SOURCE).toMatch(/mobileInfo\.port/)
  })
})
