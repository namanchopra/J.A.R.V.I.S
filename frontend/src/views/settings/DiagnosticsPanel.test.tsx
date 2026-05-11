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
