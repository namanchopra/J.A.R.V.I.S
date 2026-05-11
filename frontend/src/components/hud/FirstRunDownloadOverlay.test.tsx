// ---------------------------------------------------------------------------
// FirstRunDownloadOverlay source-level contract test.
//
// The frontend does not ship `jsdom` / `@testing-library/react`, so we
// cannot mount the component in this test environment. Following the same
// pattern as `JarvisHudView.test.tsx`, we assert source-level invariants
// that pin the visual contract + event wiring. If/when DOM testing
// infrastructure is added, this test can be supplemented with a mount-time
// spec.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './FirstRunDownloadOverlay.tsx?raw'
import HUD_SOURCE from '../JarvisHudView.tsx?raw'

describe('FirstRunDownloadOverlay -- structural contract', () => {
  it('declares the dialog with role="dialog" and aria-modal="true"', () => {
    expect(SOURCE).toMatch(/role=['"]dialog['"]/)
    expect(SOURCE).toMatch(/aria-modal=['"]true['"]/)
  })

  it('labels the dialog via aria-labelledby pointing at the header element', () => {
    expect(SOURCE).toMatch(/aria-labelledby=['"]frd-overlay-title['"]/)
    expect(SOURCE).toMatch(/id=['"]frd-overlay-title['"]/)
  })

  it('renders an aria-live="polite" readout for screen-reader progress', () => {
    expect(SOURCE).toMatch(/aria-live=['"]polite['"]/)
  })

  it('renders corner brackets via the CornerBrackets sub-component', () => {
    // Both the declaration and a usage must exist
    expect(SOURCE).toMatch(/function\s+CornerBrackets/)
    expect(SOURCE).toMatch(/<CornerBrackets/)
    // And the brackets must use border-top/border-left etc. styles
    expect(SOURCE).toMatch(/borderTop:\s*`?2px solid/)
    expect(SOURCE).toMatch(/borderLeft:\s*`?2px solid/)
  })

  it('renders the per-model row via ModelRowView', () => {
    expect(SOURCE).toMatch(/function\s+ModelRowView/)
    expect(SOURCE).toMatch(/<ModelRowView\b/)
  })

  it('renders the geometric state icons (◌ / ◉ / ✕) in source', () => {
    expect(SOURCE).toContain('◌')
    expect(SOURCE).toContain('◉')
    expect(SOURCE).toContain('✕')
  })

  it('uses the ▸ marker and → in the header and view-daemon-log link', () => {
    // Header: ▸ glyph appears in source AND the "SETTING UP JARVIS" copy.
    expect(SOURCE).toContain('▸')
    expect(SOURCE).toContain('SETTING UP JARVIS')
    // Daemon-log link bundles the prefix + copy + arrow in one literal.
    expect(SOURCE).toContain('▸ VIEW DAEMON LOG →')
  })

  it('uses the monospace label vocabulary (SF Mono, uppercase, letter-spacing)', () => {
    expect(SOURCE).toMatch(/'SF Mono'\s*,\s*'Menlo'\s*,\s*monospace/)
    expect(SOURCE).toMatch(/textTransform:\s*['"]uppercase['"]/)
  })

  it('uses the cyan accent token (#00e5ff or --accent-blue)', () => {
    // Either the variable reference or a hex fallback is acceptable.
    expect(
      /var\(--accent-blue/.test(SOURCE) || /#00e5ff/i.test(SOURCE),
    ).toBe(true)
  })
})

describe('FirstRunDownloadOverlay -- event wiring', () => {
  it('subscribes to the existing jarvis WS channel via EventsOn', () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*EventsOn[^}]*\}\s*from\s+['"][^'"]*wailsjs\/runtime\/runtime['"]/,
    )
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]jarvis['"]/)
  })

  it('defines a type guard for the model_setup event', () => {
    expect(SOURCE).toMatch(/function\s+isModelSetupEvent\s*\(/)
    expect(SOURCE).toMatch(/['"]model_setup['"]/)
  })

  it('defines a type guard for the model_download event', () => {
    expect(SOURCE).toMatch(/function\s+isModelDownloadEvent\s*\(/)
    expect(SOURCE).toMatch(/['"]model_download['"]/)
  })

  it('handles all four download states: started / progress / done / error', () => {
    expect(SOURCE).toMatch(/['"]started['"]/)
    expect(SOURCE).toMatch(/['"]progress['"]/)
    expect(SOURCE).toMatch(/['"]done['"]/)
    expect(SOURCE).toMatch(/['"]error['"]/)
  })

  it('hides the overlay when setupState !== "downloading"', () => {
    // The visibility gate must require both downloading state and an active row.
    expect(SOURCE).toMatch(/setupState\s*===\s*['"]downloading['"]/)
    expect(SOURCE).toMatch(/if\s*\(\s*!visible\s*\)\s*return\s+null/)
  })
})

describe('FirstRunDownloadOverlay -- retry wiring', () => {
  it('renders a RETRY button bound to the row error state', () => {
    expect(SOURCE).toMatch(/>\s*RETRY\s*</)
    // The button must call the row's retry handler with the model name
    expect(SOURCE).toMatch(/onClick=\{[^}]*onRetry\(\s*row\.model\s*\)/)
  })

  it('fires the retry_model_download payload via sendJarvisCommand', () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*sendJarvisCommand[^}]*\}\s*from\s+['"][^'"]*jarvis-api['"]/,
    )
    expect(SOURCE).toMatch(/sendJarvisCommand\(/)
    expect(SOURCE).toMatch(/['"]retry_model_download['"]/)
  })

  it('resets the row to indeterminate on retry (clears pct/error)', () => {
    // The optimistic reset zeroes out pct/error so the bar goes indeterminate.
    expect(SOURCE).toMatch(/state:\s*['"]started['"]/)
    expect(SOURCE).toMatch(/pct:\s*undefined/)
    expect(SOURCE).toMatch(/error:\s*undefined/)
  })
})

describe('FirstRunDownloadOverlay -- view-daemon-log link', () => {
  it('guards OpenDaemonLog call with a typeof check', () => {
    // Required so the link works once the binding lands without breaking the
    // build today.
    expect(SOURCE).toMatch(/OpenDaemonLog/)
    expect(SOURCE).toMatch(/typeof\s+fn\s*===\s*['"]function['"]/)
  })
})

describe('FirstRunDownloadOverlay -- visuals / animation', () => {
  it('mounts/unmounts with a 250ms fade-in animation', () => {
    expect(SOURCE).toMatch(/frd-fade-in/)
    expect(SOURCE).toMatch(/@keyframes\s+frd-fade-in/)
    expect(SOURCE).toMatch(/250ms/)
  })

  it('spins the header indicator via a 1.4s linear infinite rotation', () => {
    expect(SOURCE).toMatch(/@keyframes\s+frd-spin/)
    expect(SOURCE).toMatch(/frd-spin\s+1\.4s\s+linear\s+infinite/)
  })

  it('briefly flashes the bar outline on progress event arrival', () => {
    expect(SOURCE).toMatch(/@keyframes\s+frd-bar-flash/)
    expect(SOURCE).toMatch(/frd-bar-flash/)
  })

  it('pulses the row green-cyan on progress -> done transition', () => {
    expect(SOURCE).toMatch(/@keyframes\s+frd-row-pulse/)
    expect(SOURCE).toMatch(/rgba\(0,255,136/) // green-cyan tone
  })
})

describe('FirstRunDownloadOverlay -- z-index discipline', () => {
  it('uses zIndex 80 -- above orb scanline (50), below mic banner (100)', () => {
    expect(SOURCE).toMatch(/zIndex:\s*80/)
  })
})

describe('JarvisHudView integration', () => {
  it('mounts the FirstRunDownloadOverlay component', () => {
    expect(HUD_SOURCE).toMatch(
      /import\s*\{[^}]*FirstRunDownloadOverlay[^}]*\}\s*from\s+['"][^'"]*FirstRunDownloadOverlay['"]/,
    )
    expect(HUD_SOURCE).toMatch(/<FirstRunDownloadOverlay\b/)
  })
})
