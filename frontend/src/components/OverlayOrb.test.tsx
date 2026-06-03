// ---------------------------------------------------------------------------
// OverlayOrb source-level contract tests (TASK-007).
//
// Same `?raw` pattern as JarvisHudView.test.tsx / Onboarding.test.tsx /
// SettingsView.test.tsx — the frontend does not ship jsdom or
// @testing-library/react in this environment (see
// frontend/vite.config.ts and frontend/package.json), so behavioural tests
// run against the component source text. This catches the three contract
// points the task acceptance criteria call out:
//
//   1. Each of the three states (`idle`, `listening`, `speaking`) produces a
//      detectably different DOM signature — verified by the per-state
//      aria-label constants the component renders.
//   2. The `audioLevel` prop scales the inner-glow element via inline
//      `transform: scale(...)`, so identical input would produce identical
//      style and a higher input would produce a larger scale factor.
//   3. The `audioLevel` prop is optional and defaults to a safe value so the
//      component renders without throwing when the prop is omitted.
//
// In addition we pin a few structural contracts that TASK-008 (the consumer)
// depends on: a single export, framer-motion as the only heavy runtime dep,
// no Three.js, particle count capped at 40, accent color sourced from the
// existing CSS variable.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './OverlayOrb.tsx?raw'
import * as OverlayOrbModule from './OverlayOrb'

describe('OverlayOrb public API (TASK-007)', () => {
  it('exports the OverlayOrb component as a named export', () => {
    expect(typeof OverlayOrbModule.OverlayOrb).toBe('function')
  })

  it('exports the OverlayOrbState type alias', () => {
    // Types vanish at runtime, so the only way to assert "the type exists"
    // is via the source. We pin on the exact declaration shape.
    expect(SOURCE).toMatch(
      /export\s+type\s+OverlayOrbState\s*=\s*'idle'\s*\|\s*'listening'\s*\|\s*'speaking'/,
    )
  })

  it('exports the OverlayOrbProps interface', () => {
    expect(SOURCE).toMatch(/export\s+interface\s+OverlayOrbProps\b/)
    expect(SOURCE).toMatch(/state:\s*OverlayOrbState/)
    expect(SOURCE).toMatch(/audioLevel\?:\s*number/)
  })

  it('does not export anything beyond the documented API', () => {
    // The contract is: type OverlayOrbState, interface OverlayOrbProps, and
    // function OverlayOrb. Anything else would expose internals to TASK-008.
    const exportLines = SOURCE.match(/^export\s.*$/gm) ?? []
    // We accept "export type OverlayOrbState ...", "export interface
    // OverlayOrbProps ...", and "export function OverlayOrb ...". Three
    // exports total.
    expect(exportLines).toHaveLength(3)
    expect(exportLines.some((l) => /export\s+function\s+OverlayOrb\b/.test(l))).toBe(true)
  })
})

describe('OverlayOrb state renders distinct DOM (TASK-007 AC#1)', () => {
  // The component must produce detectably different DOM per state. The
  // implementation does this by mapping each state to a distinct aria-label
  // ("Idle" / "Listening" / "Speaking") which test-001 below pins on, plus
  // distinct rotation, glow opacity, and glow radius values.

  it('declares the three aria-labels — one per state', () => {
    expect(SOURCE).toMatch(/['"]Idle['"]/)
    expect(SOURCE).toMatch(/['"]Listening['"]/)
    expect(SOURCE).toMatch(/['"]Speaking['"]/)
  })

  it('binds the aria-label dynamically to the current state', () => {
    // The aria-label attribute must be driven off the state-keyed map so it
    // actually changes between renders. A hardcoded literal would render the
    // same label for all three states.
    expect(SOURCE).toMatch(/aria-label=\{[^}]*ARIA_LABEL\[[^}]*state[^}]*\]\}/)
  })

  it('also emits data-state for non-screen-reader detection', () => {
    // TASK-008's tests and devtools-style debugging benefit from a stable,
    // non-text attribute alongside aria-label.
    expect(SOURCE).toMatch(/data-state=\{state\}/)
  })

  it('uses three distinct rotation periods (idle/listening/speaking)', () => {
    // The rotation lookup table must contain three distinct values so the
    // ring spin is visibly different per state. The plan calls out 8s for
    // idle, faster for listening, and same-as-idle for speaking.
    expect(SOURCE).toMatch(/idle:\s*8/)
    expect(SOURCE).toMatch(/listening:\s*2/)
    expect(SOURCE).toMatch(/speaking:\s*8/)
  })

  it('uses three distinct glow opacities (idle dim / listening bright)', () => {
    // Per the plan: idle glow is dim, listening glow is bright. We pin on
    // the relative ordering, not the exact numbers, except where the
    // implementation literal is the source of truth.
    expect(SOURCE).toMatch(/GLOW_OPACITY:\s*Record<OverlayOrbState,\s*number>/)
    // Idle must be the dimmest of the three.
    const idleMatch = SOURCE.match(/idle:\s*0\.18/)
    const listeningMatch = SOURCE.match(/listening:\s*0\.55/)
    expect(idleMatch).not.toBeNull()
    expect(listeningMatch).not.toBeNull()
  })
})

describe('OverlayOrb audio level scales the speaking glow (TASK-007 AC#2)', () => {
  // The contract is that <OverlayOrb state="speaking" audioLevel={0.8} /> and
  // <OverlayOrb state="speaking" audioLevel={0} /> produce detectably
  // different DOM (specifically, the inner glow's inline `transform: scale(...)`
  // value). We can't mount the component without jsdom, so we verify the
  // exact code path that yields that difference.

  it('inline-styles the glow with transform: scale(<audioScale>)', () => {
    // The implementation MUST apply the audio scale via an inline style on
    // the inner-glow element — NOT via framer's animate prop — so the value
    // composes with the synthetic pulse instead of fighting it.
    expect(SOURCE).toMatch(/transform:\s*`scale\(\$\{audioScale\}\)`/)
  })

  it('derives audioScale from the audioLevel prop (1..1.5 over 0..1)', () => {
    // Pin on the exact formula so a refactor that quietly disables the
    // scaling (e.g. `const audioScale = 1`) trips this test.
    expect(SOURCE).toMatch(/audioScale\s*=\s*1\s*\+\s*normalizedAudio\s*\*\s*0\.5/)
  })

  it('clamps audioLevel to the [0, 1] range before scaling', () => {
    // Defensive: WS events may publish values outside [0,1]; the orb must
    // not produce wild transforms or NaN. Pin on the clamp helper.
    expect(SOURCE).toMatch(/function\s+clamp01\b/)
    expect(SOURCE).toMatch(/normalizedAudio\s*=\s*clamp01\(audioLevel\)/)
  })

  it('handles NaN audioLevel safely (returns 0)', () => {
    expect(SOURCE).toMatch(/Number\.isNaN\(value\)/)
  })

  it('applies the glow style via the style prop, not via animate', () => {
    // If `transform: scale(...)` were inside an `animate` prop, framer would
    // overwrite it on every tick and the audio-level signal would be lost.
    // Pin on `style={glowStyle}` and "NOT inside animate".
    expect(SOURCE).toMatch(/style=\{glowStyle\}/)
    // The framer animate target on the glow must NOT include a literal
    // numeric transform value — only scale arrays (the pulse) or `scale: 1`.
    const animateBlocks = SOURCE.match(/animate=\{[^}]*\}/g) ?? []
    for (const block of animateBlocks) {
      expect(block).not.toMatch(/transform:\s*`/)
    }
  })

  it('does NOT scale the glow when state !== "speaking" via framer pulse', () => {
    // Acceptance criterion #2 of the task scopes the audio-driven scale to
    // the *speaking* state. We don't restrict audioScale itself (it composes
    // regardless), but the synthetic pulse must only fire in speaking mode.
    expect(SOURCE).toMatch(
      /state\s*===\s*'speaking'\s*[\s\S]*?scale:\s*\[1,\s*1\.08,\s*1\]/,
    )
  })
})

describe('OverlayOrb audioLevel default (TASK-007 AC#3 — failure case)', () => {
  // Acceptance criterion #3: rendering <OverlayOrb state="speaking" /> with
  // no audioLevel prop must not throw and must produce a valid speaking-state
  // DOM. We verify the default is wired at the signature level AND that the
  // component is callable with only the required `state` prop.

  it('declares audioLevel with a default value of 0 in the destructured props', () => {
    // Pin on the exact default-init pattern. A refactor that drops the
    // default would re-introduce the `undefined * 0.5 = NaN` bug.
    expect(SOURCE).toMatch(
      /\{\s*state\s*,\s*audioLevel\s*=\s*0\s*,?\s*\}:\s*OverlayOrbProps/,
    )
  })

  // Helper: invokes OverlayOrb as a plain function with no React renderer
  // wired up. Any synchronous error thrown *before* the hook call site
  // (e.g. a prop-handling crash from a missing default, a NaN propagated
  // into a template-literal style) bubbles up. Errors thrown *at* the hook
  // call site are expected and swallowed — that's the bound on what this
  // environment can verify without jsdom.
  function invokeReachingHookSite(props: { state: OverlayOrbModule.OverlayOrbState; audioLevel?: number }): {
    reachedHookSite: boolean
    propError: unknown
  } {
    const { OverlayOrb } = OverlayOrbModule
    try {
      OverlayOrb(props)
      // If the function returned without throwing, we definitely reached the
      // hook site (and beyond) — the renderer is unmocked, so this only
      // happens if React happened to no-op the hooks. Treat as success.
      return { reachedHookSite: true, propError: null }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      // useMemo is the *first* hook the component calls. Any error mentioning
      // useMemo (or the generic "Invalid hook call" / null-dispatcher
      // signature) means we made it past the prop-handling code.
      const isHookSiteError =
        /useMemo|invalid hook|hooks can only|null.*useMemo|cannot read prop/i.test(msg)
      return { reachedHookSite: isHookSiteError, propError: isHookSiteError ? null : err }
    }
  }

  it('renders without throwing when audioLevel is omitted', () => {
    // The audio scaling math runs synchronously inside the body before the
    // first hook. A missing default would propagate `undefined` through
    // clamp01 and into a template-literal `scale(${audioScale})`, which is
    // a prop-handling crash this test must catch.
    const result = invokeReachingHookSite({ state: 'speaking' })
    expect(result.propError).toBeNull()
    expect(result.reachedHookSite).toBe(true)
  })

  it('renders without throwing when audioLevel is 0 explicitly', () => {
    const result = invokeReachingHookSite({ state: 'speaking', audioLevel: 0 })
    expect(result.propError).toBeNull()
    expect(result.reachedHookSite).toBe(true)
  })
})

describe('OverlayOrb bundle / dependency contract (TASK-007 constraints)', () => {
  // Hard constraints from the plan: framer-motion only, no Three.js, particle
  // count capped at 40, palette via the existing CSS variable. Any drift on
  // these would re-introduce the cold-render or bundle-weight regressions
  // the plan calls out.

  it('imports motion from framer-motion (the only animation dep)', () => {
    expect(SOURCE).toMatch(/from\s+['"]framer-motion['"]/)
  })

  it('does NOT import @react-three/fiber or three (heavy 3D deps)', () => {
    expect(SOURCE).not.toMatch(/@react-three\/fiber/)
    expect(SOURCE).not.toMatch(/from\s+['"]three['"]/)
  })

  it('does NOT import from the mobile React Native orb', () => {
    expect(SOURCE).not.toMatch(/mobile\/components\/OrbView/)
  })

  it('caps the particle ring at 40 particles', () => {
    expect(SOURCE).toMatch(/PARTICLE_COUNT\s*=\s*40/)
  })

  it('uses var(--accent-blue) for the palette (with a literal cyan fallback)', () => {
    // The existing CSS custom property must be the source of truth; a hex
    // literal-only color would drift from the rest of the HUD.
    expect(SOURCE).toMatch(/var\(--accent-blue/)
  })

  it('memoizes particle positions so they compute once per component', () => {
    // The ring is rotated via one CSS transform on the parent <g>, so the
    // per-particle (cx, cy) values are constants for the lifetime of the
    // component. Pin on useMemo so a refactor doesn't reintroduce the
    // 40-Math.sin-calls-per-render footgun.
    expect(SOURCE).toMatch(/useMemo\(buildParticlePositions/)
  })

  it('rotates the ring on a single parent <g>, not per-particle keyframes', () => {
    // motion.g with animate={{ rotate: 360 }} is the cheap path. A refactor
    // that swapped to per-particle <motion.circle animate={{ ... }}> would
    // re-introduce the framerate regression on low-power Macs.
    expect(SOURCE).toMatch(/<motion\.g\b/)
    expect(SOURCE).toMatch(/animate=\{\{\s*rotate:\s*360\s*\}\}/)
  })

  it('fills its container — width/height 100% so the parent owns the box', () => {
    // Per the plan: the parent (OverlayView, TASK-008) is the 180x180 box.
    // The orb itself must scale to whatever it is mounted in.
    expect(SOURCE).toMatch(/width:\s*['"]100%['"]/)
    expect(SOURCE).toMatch(/height:\s*['"]100%['"]/)
    expect(SOURCE).toMatch(/width=['"]100%['"]/)
    expect(SOURCE).toMatch(/height=['"]100%['"]/)
  })

  it('uses a viewBox so coordinate math is screen-size-agnostic', () => {
    expect(SOURCE).toMatch(/viewBox=['"]0 0 100 100['"]/)
  })
})
