// ---------------------------------------------------------------------------
// OverlayPanel — source-level contract test + hotkey-spec helper unit
// tests (TASK-009, v0.3.0).
//
// This project's test harness does NOT ship jsdom + React Testing Library
// (see PermissionsPanel.test.tsx / DiagnosticsPanel.test.tsx for prior art).
// We mix two styles:
//   1. Pure-helper unit tests against the extracted `hotkey-spec.ts`
//      module — the canonicalize / blocklist / glyph helpers are tested
//      directly because they're plain functions on a structural KeyboardEvent
//      type.
//   2. Source-level contracts on `OverlayPanel.tsx` via the `?raw` import
//      trick — pins the wiring (config field names, SaveConfig call,
//      EventsOn listener + cleanup, reserved-shortcut warning string).
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './OverlayPanel.tsx?raw'
import SETTINGS_VIEW_SOURCE from '../SettingsView.tsx?raw'
import {
  BLOCKED_SPECS,
  canonicalizeSpec,
  glyphFormatSpec,
  isBlockedSpec,
  isModifierOnly,
  type KeyboardEventLike,
} from './hotkey-spec'

// ---------------------------------------------------------------------------
// hotkey-spec helper unit tests — pure functions, no React tree required.
// ---------------------------------------------------------------------------

function makeEvent(partial: Partial<KeyboardEventLike>): KeyboardEventLike {
  return {
    key: '',
    metaKey: false,
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    ...partial,
  }
}

describe('hotkey-spec / canonicalizeSpec', () => {
  it('serializes Cmd+Shift+J → "cmd+shift+j" with canonical order', () => {
    const spec = canonicalizeSpec(
      makeEvent({ key: 'J', metaKey: true, shiftKey: true }),
    )
    expect(spec).toBe('cmd+shift+j')
  })

  it('serializes Alt+Space → "alt+space" (the project default)', () => {
    const spec = canonicalizeSpec(makeEvent({ key: ' ', altKey: true }))
    expect(spec).toBe('alt+space')
  })

  it('serializes Ctrl+Shift+J → "ctrl+shift+j"', () => {
    const spec = canonicalizeSpec(
      makeEvent({ key: 'j', ctrlKey: true, shiftKey: true }),
    )
    expect(spec).toBe('ctrl+shift+j')
  })

  it('places modifiers in canonical order: cmd, ctrl, alt, shift', () => {
    // Press all four mods + a key to assert ordering deterministically.
    const spec = canonicalizeSpec(
      makeEvent({
        key: 'k',
        metaKey: true,
        ctrlKey: true,
        altKey: true,
        shiftKey: true,
      }),
    )
    expect(spec).toBe('cmd+ctrl+alt+shift+k')
  })

  it('maps named keys (Enter, Tab, Escape, arrows) to canonical tokens', () => {
    expect(canonicalizeSpec(makeEvent({ key: 'Enter', altKey: true }))).toBe('alt+return')
    expect(canonicalizeSpec(makeEvent({ key: 'Tab', altKey: true }))).toBe('alt+tab')
    expect(canonicalizeSpec(makeEvent({ key: 'Escape', altKey: true }))).toBe('alt+escape')
    expect(canonicalizeSpec(makeEvent({ key: 'ArrowLeft', altKey: true }))).toBe('alt+left')
    expect(canonicalizeSpec(makeEvent({ key: 'ArrowRight', altKey: true }))).toBe('alt+right')
    expect(canonicalizeSpec(makeEvent({ key: 'ArrowUp', altKey: true }))).toBe('alt+up')
    expect(canonicalizeSpec(makeEvent({ key: 'ArrowDown', altKey: true }))).toBe('alt+down')
  })

  it('handles function keys F1..F12 (lowercase in canonical form)', () => {
    expect(canonicalizeSpec(makeEvent({ key: 'F1', altKey: true }))).toBe('alt+f1')
    expect(canonicalizeSpec(makeEvent({ key: 'F12', altKey: true }))).toBe('alt+f12')
  })

  it('lowercases letter keys', () => {
    expect(canonicalizeSpec(makeEvent({ key: 'A', metaKey: true }))).toBe('cmd+a')
    expect(canonicalizeSpec(makeEvent({ key: 'z', metaKey: true }))).toBe('cmd+z')
  })
})

describe('hotkey-spec / isBlockedSpec', () => {
  it('rejects Cmd+Q (system quit)', () => {
    expect(isBlockedSpec('cmd+q')).toBe(true)
  })

  it('rejects Cmd+W (system close window)', () => {
    expect(isBlockedSpec('cmd+w')).toBe(true)
  })

  it('rejects Cmd+H (system hide app)', () => {
    expect(isBlockedSpec('cmd+h')).toBe(true)
  })

  it('allows non-reserved specs like alt+space', () => {
    expect(isBlockedSpec('alt+space')).toBe(false)
    expect(isBlockedSpec('cmd+shift+j')).toBe(false)
    expect(isBlockedSpec('ctrl+shift+j')).toBe(false)
  })

  it('normalizes case + whitespace on the input', () => {
    expect(isBlockedSpec('CMD+Q')).toBe(true)
    expect(isBlockedSpec('  cmd+q  ')).toBe(true)
  })

  it('exposes the canonical blocklist as a frozen-ish constant', () => {
    expect(BLOCKED_SPECS).toEqual(['cmd+q', 'cmd+w', 'cmd+h'])
  })
})

describe('hotkey-spec / isModifierOnly', () => {
  it('returns true for bare Meta/Control/Alt/Shift presses', () => {
    expect(isModifierOnly(makeEvent({ key: 'Meta', metaKey: true }))).toBe(true)
    expect(isModifierOnly(makeEvent({ key: 'Control', ctrlKey: true }))).toBe(true)
    expect(isModifierOnly(makeEvent({ key: 'Alt', altKey: true }))).toBe(true)
    expect(isModifierOnly(makeEvent({ key: 'Shift', shiftKey: true }))).toBe(true)
  })

  it('returns false when a real key is held alongside the modifier', () => {
    expect(isModifierOnly(makeEvent({ key: 'j', metaKey: true }))).toBe(false)
    expect(isModifierOnly(makeEvent({ key: ' ', altKey: true }))).toBe(false)
  })
})

describe('hotkey-spec / glyphFormatSpec', () => {
  it('renders "cmd+shift+j" as "⌘ ⇧ J"', () => {
    expect(glyphFormatSpec('cmd+shift+j')).toBe('⌘ ⇧ J')
  })

  it('renders "alt+space" as "⌥ Space"', () => {
    expect(glyphFormatSpec('alt+space')).toBe('⌥ Space')
  })

  it('renders an empty spec as an empty string (no crash on missing config)', () => {
    expect(glyphFormatSpec('')).toBe('')
  })

  it('uppercases function keys (f1 → F1)', () => {
    expect(glyphFormatSpec('cmd+f1')).toBe('⌘ F1')
  })
})

// ---------------------------------------------------------------------------
// Source-level contracts on OverlayPanel.tsx + SettingsView.tsx.
// ---------------------------------------------------------------------------

describe('OverlayPanel TASK-009 (config field wiring)', () => {
  it('wires the Enable toggle to overlayEnabled', () => {
    expect(SOURCE).toMatch(/overlayEnabled/)
    // The toggle's aria-checked must be derived from the same field so the
    // refactor that decouples them would have to delete this expectation
    // explicitly.
    expect(SOURCE).toMatch(/aria-checked=\{enabled\}/)
  })

  it('wires the Position select to overlayPosition', () => {
    expect(SOURCE).toMatch(/overlayPosition/)
    expect(SOURCE).toMatch(/data-testid=['"]overlay-position-select['"]/)
  })

  it('wires the Show transcript toggle to overlayShowTranscript', () => {
    expect(SOURCE).toMatch(/overlayShowTranscript/)
    expect(SOURCE).toMatch(/aria-checked=\{showTranscript\}/)
  })

  it('wires the Hotkey field to overlayHotkey', () => {
    expect(SOURCE).toMatch(/overlayHotkey/)
  })

  it('calls SaveConfig on hotkey rebind so the daemon sees the new binding', () => {
    // The toggle/select fields defer to the parent sticky Save button, but
    // the hotkey rebind has an immediate side effect (RebindOverlayHotkey)
    // so it must persist via SaveConfig mid-flow.
    expect(SOURCE).toContain('SaveConfig')
    expect(SOURCE).toMatch(/SaveConfig\(/)
  })

  it('exposes all five position options (4 corners + last-dragged)', () => {
    expect(SOURCE).toMatch(/['"]top-right['"]/)
    expect(SOURCE).toMatch(/['"]top-left['"]/)
    expect(SOURCE).toMatch(/['"]bottom-right['"]/)
    expect(SOURCE).toMatch(/['"]bottom-left['"]/)
    expect(SOURCE).toMatch(/['"]last-dragged['"]/)
  })
})

describe('OverlayPanel TASK-009 (hotkey-error event wiring)', () => {
  it('subscribes to the "overlay:hotkey_error" Wails event', () => {
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]overlay:hotkey_error['"]/)
  })

  it('returns a cleanup function from the EventsOn useEffect', () => {
    // EventsOn returns its own cancel fn — the panel must call it on
    // unmount or the listener leaks per remount (very visible during HMR).
    expect(SOURCE).toMatch(/const\s+cancel\s*=\s*EventsOn\(/)
    expect(SOURCE).toMatch(/return\s*\(\s*\)\s*=>\s*\{[\s\S]*?cancel\(\)/)
  })

  it('renders an Accessibility deep-link CTA in the warning row', () => {
    // Matches DiagnosticsPanel.tsx's deep-link convention to System
    // Settings via the BrowserOpenURL runtime helper. The Accessibility
    // pane is the right destination because global-hotkey registration on
    // macOS gates on Accessibility (not Microphone).
    expect(SOURCE).toMatch(/BrowserOpenURL/)
    expect(SOURCE).toMatch(/x-apple\.systempreferences:com\.apple\.preference\.security\?Privacy_Accessibility/)
    expect(SOURCE).toMatch(/Open System Settings/)
  })

  it('uses local useState to drive the warning row visibility (no payload coupling)', () => {
    // The brief: "Don't rely on the event having a payload — the
    // absence/presence of the warning row is state-driven by a local
    // useState<boolean>." We pin on the state setter being called from
    // the listener body without consuming the event's payload.
    expect(SOURCE).toMatch(/setHotkeyError\(\s*true\s*\)/)
  })
})

describe('OverlayPanel TASK-009 (capture mode + blocklist)', () => {
  it('surfaces a reserved-shortcut warning string in the source', () => {
    // The user must see WHY the rebind was rejected. Pin on the literal
    // "reserved" string + the testid so a refactor that drops the warning
    // surface area trips the test.
    expect(SOURCE).toMatch(/reserved/i)
    expect(SOURCE).toMatch(/data-testid=['"]overlay-reserved-warning['"]/)
  })

  it('calls canonicalizeSpec + isBlockedSpec from the capture handler', () => {
    // Pin on the imports + the call sites so the helper module stays
    // load-bearing for the rebind flow.
    expect(SOURCE).toMatch(/import\s*\{[^}]*canonicalizeSpec[^}]*\}\s*from\s*['"]\.\/hotkey-spec['"]/)
    expect(SOURCE).toMatch(/import\s*\{[^}]*isBlockedSpec[^}]*\}\s*from\s*['"]\.\/hotkey-spec['"]/)
    expect(SOURCE).toMatch(/canonicalizeSpec\(/)
    expect(SOURCE).toMatch(/isBlockedSpec\(/)
  })

  it('aborts capture on a bare Escape keypress', () => {
    // The brief mandates an Esc-to-cancel path. Pin on the literal key
    // comparison + the setCapturing(false) call in the abort branch.
    expect(SOURCE).toMatch(/event\.key\s*===\s*['"]Escape['"]/)
    expect(SOURCE).toMatch(/setCapturing\(\s*false\s*\)/)
  })

  it('attempts RebindOverlayHotkey via the runtime-optional binding shim', () => {
    // The Go-side RebindOverlayHotkey arrives with TASK-005; until then
    // the call must be type-guarded. Pin on the typeof === 'function'
    // check + the window.go?.main?.App resolution pattern (matches
    // PermissionsPanel.tsx / DiagnosticsPanel.tsx).
    expect(SOURCE).toMatch(/RebindOverlayHotkey/)
    expect(SOURCE).toMatch(/typeof\s+app\.RebindOverlayHotkey\s*===\s*['"]function['"]/)
    expect(SOURCE).toMatch(/go\?\.main\?\.App/)
  })
})

describe('OverlayPanel TASK-009 (tabpanel integration)', () => {
  it('renders exactly one tabpanel root keyed to activeTab !== "overlay"', () => {
    const matches = SOURCE.match(/role=['"]tabpanel['"]/g) ?? []
    expect(matches.length).toBe(1)
    expect(SOURCE).toMatch(/activeTab\s*!==\s*['"]overlay['"]/)
    expect(SOURCE).toMatch(/id=['"]settings-tab-panel-overlay['"]/)
  })

  it('is mounted in SettingsView.tsx via <OverlayPanel ... />', () => {
    // TASK-009 acceptance criterion #5 — the TASK-003 placeholder must be
    // replaced. We pin on the import + the JSX usage so a future refactor
    // that drops the panel from SettingsView trips this test loudly.
    expect(SETTINGS_VIEW_SOURCE).toMatch(/import\s*\{\s*OverlayPanel\s*\}\s*from\s*['"]\.\/settings\/OverlayPanel['"]/)
    expect(SETTINGS_VIEW_SOURCE).toMatch(/<OverlayPanel\b/)
  })

  it('does not retain the TASK-003 "coming soon" placeholder copy', () => {
    // The placeholder div SettingsView.tsx held while TASK-003 was the
    // current task must be gone — otherwise the user would see both the
    // panel and the stub stacked together.
    expect(SETTINGS_VIEW_SOURCE).not.toMatch(/Overlay settings are coming soon/)
  })
})
