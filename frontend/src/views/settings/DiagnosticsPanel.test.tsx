// ---------------------------------------------------------------------------
// DiagnosticsPanel — source-level contract test (TASK-022).
//
// Verifies the surface the live health panel must expose:
//   1. All 7 status row labels are referenced in the source.
//   2. The `GetDiagnostics` Wails binding is called.
//   3. A "Copy diagnostics" master button exists.
//   4. A `setInterval(..., 2000)` polling loop is present.
//
// Source-level (vs DOM-mounted) because the frontend test environment
// does not ship `jsdom` / `@testing-library/react`. We use the same
// `?raw` trick the SettingsView and JarvisHudView tests use.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './DiagnosticsPanel.tsx?raw'

describe('DiagnosticsPanel (TASK-022)', () => {
  it('references all 7 status row labels', () => {
    expect(SOURCE).toMatch(/label=['"]Daemon['"]/)
    expect(SOURCE).toMatch(/label=['"]Mic permission['"]/)
    expect(SOURCE).toMatch(/label=['"]Mobile API['"]/)
    expect(SOURCE).toMatch(/label=['"]LLM chain['"]/)
    expect(SOURCE).toMatch(/label=['"]Models['"]/)
    expect(SOURCE).toMatch(/label=['"]Ollama['"]/)
    expect(SOURCE).toMatch(/label=['"]Disk['"]/)
  })

  it('calls the GetDiagnostics Wails binding', () => {
    expect(SOURCE).toMatch(/GetDiagnostics/)
  })

  it('renders a master "Copy diagnostics" button', () => {
    expect(SOURCE).toMatch(/Copy diagnostics/)
  })

  it('polls on a 2-second interval', () => {
    // Accept either compact (`setInterval(fn, 2000)`) or multi-line
    // (`setInterval(()=>{...}, 2000)`) call shapes. The literal 2000 ms
    // window is the contract — narrower than that would over-poll, wider
    // would feel laggy for the daemon-down scenario.
    expect(SOURCE).toMatch(/setInterval\([\s\S]*?,\s*2000\s*\)/)
  })

  it('cleans up the polling interval on unmount', () => {
    // Regression check — without clearInterval the panel would leak
    // a setInterval handle per mount (very visible during dev/HMR).
    expect(SOURCE).toMatch(/clearInterval\(/)
  })
})

// ---------------------------------------------------------------------------
// TASK-026 — Mic permission row deep-link to System Settings.
//
// Contract:
//   1. The panel imports `BrowserOpenURL` from the Wails runtime.
//   2. The mic permission row receives an "Open System Settings" action
//      ONLY in a branch gated on `'denied'` / `'restricted'`.
//   3. The action's onClick invokes `BrowserOpenURL` with the documented
//      `x-apple.systempreferences:` URL.
// ---------------------------------------------------------------------------

describe('DiagnosticsPanel mic permission deep-link (TASK-026)', () => {
  it('imports BrowserOpenURL from the Wails runtime', () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*BrowserOpenURL[^}]*\}\s*from\s+['"][^'"]*wailsjs\/runtime\/runtime['"]/,
    )
  })

  it('exposes an "Open System Settings" button text', () => {
    expect(SOURCE).toMatch(/Open System Settings/)
  })

  it('gates the System Settings action on denied or restricted status', () => {
    // The branch must mention both string literals so the button only
    // shows when the binding reports those two states (TASK-026 contract).
    expect(SOURCE).toMatch(/['"]denied['"]/)
    expect(SOURCE).toMatch(/['"]restricted['"]/)
    // Both literals should appear in the same gating expression — accept
    // either order as long as both are present near each other (within ~80
    // chars), which is the natural shape of a `=== 'denied' || === 'restricted'`
    // check.
    expect(SOURCE).toMatch(
      /['"]denied['"][\s\S]{0,80}['"]restricted['"]|['"]restricted['"][\s\S]{0,80}['"]denied['"]/,
    )
  })

  it('calls BrowserOpenURL with the macOS System Settings privacy URL', () => {
    expect(SOURCE).toMatch(
      /x-apple\.systempreferences:com\.apple\.preference\.security\?Privacy_Microphone/,
    )
    expect(SOURCE).toMatch(/BrowserOpenURL\(/)
  })
})
