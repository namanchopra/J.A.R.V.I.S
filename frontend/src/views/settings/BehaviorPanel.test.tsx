// ---------------------------------------------------------------------------
// BehaviorPanel — source-level contract test (TASK-020 + TASK-021).
//
// The frontend test harness ships without jsdom, so we lean on the same
// `?raw` import trick used by SettingsView.test.tsx and JarvisHudView.test.tsx
// to assert that the rendered source contains the required field bindings,
// JSX nodes, and Wails binding calls. This catches the regression patterns
// each task introduces without requiring a real DOM mount.
//
// TASK-020 — Audio transport dropdown:
//   - useLiveKitTransport toggle present
//   - Three LiveKit credential inputs (livekitUrl / livekitApiKey /
//     livekitApiSecret) gated behind `cfg.useLiveKitTransport &&` so they
//     only render when LiveKit is selected
//   - jarvisAmbientEnabled toggle
//
// TASK-021 — Browse + notifications regression:
//   - "Browse" button text
//   - BrowseForDirectory Wails binding call
//   - Pre-existing notification toggles and ci* fields still referenced
//     (no regression from TASK-016/prep)
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './BehaviorPanel.tsx?raw'

describe('BehaviorPanel TASK-020 (audio transport + LiveKit)', () => {
  it('binds the audio transport dropdown to cfg.useLiveKitTransport', () => {
    expect(SOURCE).toMatch(/useLiveKitTransport/)
    // The setter must write the boolean back to useLiveKitTransport.
    expect(SOURCE).toMatch(/useLiveKitTransport:\s*e\.target\.value\s*===\s*['"]livekit['"]/)
  })

  it('offers Local and LiveKit options labelled per spec', () => {
    expect(SOURCE).toMatch(/Local\s*\(Mac mic\+speaker — recommended\)/)
    expect(SOURCE).toMatch(/LiveKit\s*\(advanced — requires extra config\)/)
  })

  it('renders the three LiveKit credential fields conditionally on useLiveKitTransport', () => {
    // The credentials block must be gated on a truthy check against the
    // boolean field — `cfg.useLiveKitTransport &&` (with optional whitespace).
    expect(SOURCE).toMatch(/cfg\.useLiveKitTransport\s*&&/)
    // Each of the three keys must be bound to an input value.
    expect(SOURCE).toMatch(/cfg\.livekitUrl/)
    expect(SOURCE).toMatch(/cfg\.livekitApiKey/)
    expect(SOURCE).toMatch(/cfg\.livekitApiSecret/)
    // Setter writes back to each key.
    expect(SOURCE).toMatch(/livekitUrl:\s*e\.target\.value/)
    expect(SOURCE).toMatch(/livekitApiKey:\s*e\.target\.value/)
    expect(SOURCE).toMatch(/livekitApiSecret:\s*e\.target\.value/)
  })

  it('renders an ambient mode toggle bound to jarvisAmbientEnabled', () => {
    expect(SOURCE).toMatch(/jarvisAmbientEnabled/)
    // Toggle flips the boolean.
    expect(SOURCE).toMatch(/jarvisAmbientEnabled:\s*!cfg\.jarvisAmbientEnabled/)
  })
})

describe('BehaviorPanel TASK-021 (Browse button + scanner roots)', () => {
  it('imports BrowseForDirectory from the Wails main App bindings', () => {
    expect(SOURCE).toMatch(
      /import\s*\{\s*BrowseForDirectory\s*\}\s*from\s*['"][^'"]*wailsjs\/go\/main\/App['"]/,
    )
  })

  it('invokes BrowseForDirectory when the Browse button is clicked', () => {
    expect(SOURCE).toMatch(/BrowseForDirectory\s*\(/)
    expect(SOURCE).toMatch(/Browse\.\.\./)
  })

  it('appends the picked path to projectRootPaths', () => {
    // The handler must extend the existing list (spread + new path) rather
    // than overwrite it — pin on the spread pattern.
    expect(SOURCE).toMatch(/projectRootPaths:\s*\[\s*\.\.\.\s*current\s*,\s*picked\s*\]/)
  })
})

describe('BehaviorPanel TASK-016/prep regression (no removed fields)', () => {
  it('still renders all three notification toggles', () => {
    expect(SOURCE).toMatch(/notificationsEnabled/)
    expect(SOURCE).toMatch(/notifyOnApproval/)
    expect(SOURCE).toMatch(/notifyOnCompletion/)
  })

  it('still renders ciWatchEnabled + ciProvider controls', () => {
    expect(SOURCE).toMatch(/ciWatchEnabled/)
    expect(SOURCE).toMatch(/ciProvider/)
  })

  it('still mounts <ApprovalRulesSettings/>', () => {
    expect(SOURCE).toMatch(
      /import\s*\{\s*ApprovalRulesSettings\s*\}\s*from\s*['"][^'"]*ApprovalRulesSettings['"]/,
    )
    expect(SOURCE).toMatch(/<ApprovalRulesSettings\b/)
  })

  it('still renders preferredTerminal, projectRootPaths, scanIntervalSeconds', () => {
    expect(SOURCE).toMatch(/preferredTerminal/)
    expect(SOURCE).toMatch(/projectRootPaths/)
    expect(SOURCE).toMatch(/scanIntervalSeconds/)
  })
})
