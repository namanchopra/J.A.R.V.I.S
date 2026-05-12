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

// ---------------------------------------------------------------------------
// v0.1.5 — Voice Pipeline row (live `pipeline_status` from the daemon).
//
// Contract:
//   1. The panel imports `usePipelineStatus` from the shared hook.
//   2. A "Voice Pipeline" section header is in the source.
//   3. All four sub-rows (LLM / STT / TTS / Wake) are present.
//   4. An empty-state copy and a "Request now" button are in the source,
//      and the button is wired to the hook's `refresh()`.
//   5. The user-pick diamond marker is gated on `llm.source === 'user-pick'`.
// ---------------------------------------------------------------------------

describe('DiagnosticsPanel voice pipeline row (v0.1.5)', () => {
  it('imports usePipelineStatus from the shared hook module', () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*usePipelineStatus[^}]*\}\s*from\s+['"][^'"]*use-pipeline-status['"]/,
    )
    expect(SOURCE).toMatch(/usePipelineStatus\(\)/)
  })

  it('renders a "Voice Pipeline" section header', () => {
    expect(SOURCE).toMatch(/VOICE PIPELINE/)
  })

  it('renders all four sub-row labels (LLM / STT / TTS / Wake)', () => {
    // Each label appears as visible row text inside the dl. We pin on the
    // literal label strings — the casing matches the spec's mockup.
    expect(SOURCE).toMatch(/>\s*LLM\s*</)
    expect(SOURCE).toMatch(/>\s*STT\s*</)
    expect(SOURCE).toMatch(/>\s*TTS\s*</)
    expect(SOURCE).toMatch(/>\s*Wake\s*</)
  })

  it('reads each sub-row value off the pipeline_status event branches', () => {
    // The spec calls out that values come from the event, not hardcoded.
    expect(SOURCE).toMatch(/status\.llm\.provider/)
    expect(SOURCE).toMatch(/status\.llm\.model/)
    expect(SOURCE).toMatch(/status\.stt\.model/)
    expect(SOURCE).toMatch(/status\.tts\.provider/)
    expect(SOURCE).toMatch(/status\.tts\.voice/)
    expect(SOURCE).toMatch(/status\.wake_word\.enabled/)
    expect(SOURCE).toMatch(/status\.wake_word\.sensitivity/)
  })

  it('gates the user-pick marker on llm.source === "user-pick"', () => {
    expect(SOURCE).toMatch(/status\.llm\.source\s*===\s*['"]user-pick['"]/)
    expect(SOURCE).toMatch(/user-pick/)
  })

  it('renders the empty-state copy when no pipeline_status has arrived', () => {
    expect(SOURCE).toMatch(/no pipeline_status received from daemon yet/)
  })

  it('wires the "Request now" button to the hook refresh()', () => {
    expect(SOURCE).toMatch(/Request now/)
    // The hook returns `refresh` — the button onClick must call it. We pin
    // on the call shape rather than the exact identifier order.
    expect(SOURCE).toMatch(/onClick=\{\s*refresh\s*\}/)
  })

  it('shows a "last updated Xs ago" stamp when status is present', () => {
    expect(SOURCE).toMatch(/last updated/)
    // The formatter is the one we ship — verify both arms (`s ago` / `m ago`).
    expect(SOURCE).toMatch(/s ago/)
    expect(SOURCE).toMatch(/m ago/)
  })
})
