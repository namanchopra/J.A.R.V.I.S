// ---------------------------------------------------------------------------
// FridayPairingModal source-level contract test (TASK-025).
//
// Same `?raw` pattern as ConnectionsPanel.test.tsx — the frontend doesn't
// ship jsdom / @testing-library/react in this environment, so behavioural
// tests run against the panel source text. We pin:
//   1. The Wails binding (GenerateMobilePairingQR) is referenced.
//   2. The modal title matches the acceptance criteria copy ("Scan this
//      with Friday").
//   3. The footer copy matches ("Friday opens Expo Go → scans this QR →
//      done.").
//   4. ARIA: role="dialog" + aria-modal="true" are present.
//   5. The close button is wired (X icon + onClose call).
//   6. Backdrop click + Escape both dismiss the modal (defence against
//      a future refactor removing one or the other).
//   7. Loading / error / ready states are all rendered.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './FridayPairingModal.tsx?raw'

describe('FridayPairingModal (TASK-025)', () => {
  it('references the GenerateMobilePairingQR Wails binding', () => {
    expect(SOURCE).toMatch(/GenerateMobilePairingQR/)
  })

  it('renders the required title copy', () => {
    expect(SOURCE).toMatch(/Scan this with Friday/)
  })

  it('renders the required footer copy', () => {
    // The Unicode arrow → must appear in the footer string. Accepts a bit
    // of whitespace drift between the words.
    expect(SOURCE).toMatch(/Friday opens Expo Go\s*→\s*scans this QR\s*→\s*done\./)
  })

  it('uses role="dialog" + aria-modal for accessibility', () => {
    expect(SOURCE).toMatch(/role="dialog"/)
    expect(SOURCE).toMatch(/aria-modal="true"/)
  })

  it('renders an X close button wired to onClose', () => {
    // The label discriminates the close button from any future affordance
    // and is what screen readers announce.
    expect(SOURCE).toMatch(/aria-label="Close pairing dialog"/)
  })

  it('closes on backdrop click + Escape key', () => {
    // Backdrop close: the outer dialog div has onClick={onClose}.
    // Escape close: a keydown listener on window calling onClose.
    expect(SOURCE).toMatch(/onClick=\{onClose\}/)
    expect(SOURCE).toMatch(/['"]Escape['"]/)
  })

  it('renders all three QR fetch states (loading / error / ready)', () => {
    expect(SOURCE).toMatch(/['"]loading['"]/)
    expect(SOURCE).toMatch(/['"]ready['"]/)
    expect(SOURCE).toMatch(/['"]error['"]/)
  })

  it('renders the QR via an <img src=...> when ready', () => {
    // Pin both the img tag itself and the data-URL src binding so a future
    // refactor that swaps in a canvas or background-image surface trips
    // this check explicitly.
    expect(SOURCE).toMatch(/<img\b[\s\S]*?src=\{qr\.dataURL\}/)
  })

  it('shows a generating-QR placeholder while loading', () => {
    expect(SOURCE).toMatch(/Generating QR/)
  })
})

describe('FridayPairingModal wired into ConnectionsPanel', () => {
  // Pin the connection between the panel and the modal so a future
  // refactor doesn't accidentally drop the integration. Source-level so
  // it survives without jsdom.
  it('ConnectionsPanel imports + renders the modal', async () => {
    const panel = (
      await import('./ConnectionsPanel.tsx?raw')
    ).default as string
    expect(panel).toMatch(/FridayPairingModal/)
    expect(panel).toMatch(/Connect Friday phone/)
    expect(panel).toMatch(/Friday mobile/)
    // The button must open the modal — setFridayPairOpen(true) is the
    // observable contract.
    expect(panel).toMatch(/setFridayPairOpen\(true\)/)
  })
})
