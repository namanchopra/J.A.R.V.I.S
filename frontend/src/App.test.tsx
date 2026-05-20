// ---------------------------------------------------------------------------
// App.tsx — source-level contract test for the v0.2.0 setup gate (TASK-012).
//
// The frontend test harness ships without jsdom, so we use the `?raw` import
// pattern documented in SettingsView.test.tsx to assert structural invariants
// on the rendered source. Sufficient to catch regressions in:
//   - the IsSetupComplete-driven render gate
//   - the EventsOn('setup') subscription + setup_state.complete flip
//   - the malformed-event try/catch + console.warn fallback
//   - the daemon-launch-failed banner conditional + state slot
//   - Onboarding still mounts after setup completes
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './App.tsx?raw'

describe('App.tsx v0.2.0 setup gate (TASK-012)', () => {
  it('declares isSetupComplete state slot as boolean | null', () => {
    expect(SOURCE).toMatch(/useState<boolean\s*\|\s*null>\(null\)/)
  })

  it('resolves IsSetupComplete via the window.go.main.App runtime guard', () => {
    // Bindings may not be regenerated yet — pattern matches v0.1.x's
    // OpenDaemonLog approach for the same case.
    expect(SOURCE).toMatch(/setupBindings\(\)/)
    expect(SOURCE).toMatch(/IsSetupComplete/)
  })

  it('falls through to setIsSetupComplete(true) when the binding is absent (dev mode)', () => {
    // Dev mode pre-wails-build: the binding isn't on window.go.main.App,
    // so the gate must not trap the maintainer behind a SetupScreen.
    expect(SOURCE).toMatch(/typeof\s+bindings\?\.IsSetupComplete\s*!==\s*['"]function['"]/)
    expect(SOURCE).toMatch(/setIsSetupComplete\(true\)/)
  })

  it('auto-fires RunSetup when isSetupComplete resolves to false (v0.2.4)', () => {
    // Regression pin: v0.2.0..v0.2.3 shipped the binding but never wired the
    // trigger, so the SetupScreen would mount forever without anything
    // spawning install-daemon.sh. v0.2.4 fires RunSetup from a useEffect
    // gated on isSetupComplete === false, with a ref guard so it only
    // fires once per session.
    expect(SOURCE).toMatch(/setupRunFiredRef/)
    expect(SOURCE).toMatch(/if\s*\(\s*isSetupComplete\s*!==\s*false\s*\)\s*return/)
    expect(SOURCE).toMatch(/bindings\.RunSetup\(\)/)
  })

  it('subscribes to EventsOn for the "setup" channel', () => {
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]setup['"]/)
  })

  it('uses isSetupStateEvent to narrow incoming events', () => {
    expect(SOURCE).toMatch(/import\s+\{[^}]*isSetupStateEvent[^}]*\}\s+from\s+['"]\.\/lib\/use-setup-state['"]/)
    expect(SOURCE).toMatch(/isSetupStateEvent\(event\)/)
  })

  it('flips the gate when setup_state.complete arrives mid-session', () => {
    expect(SOURCE).toMatch(/event\.complete/)
    expect(SOURCE).toMatch(/setIsSetupComplete\(true\)/)
  })

  it('protects the gate from malformed events via try/catch + console.warn', () => {
    expect(SOURCE).toMatch(/try\s*\{/)
    expect(SOURCE).toMatch(/console\.warn\(\s*['"]App:\s*rejected malformed/)
  })

  it('mounts <SetupScreen /> when isSetupComplete === false', () => {
    expect(SOURCE).toMatch(/isSetupComplete\s*===\s*false/)
    expect(SOURCE).toMatch(/<SetupScreen\s*\/>/)
  })

  it('declares daemonLaunchFailed state slot (foundation for TASK-009 surfacing)', () => {
    expect(SOURCE).toMatch(/useState<boolean>\(false\)/)
    expect(SOURCE).toMatch(/daemonLaunchFailed/)
  })

  it('renders <DaemonFailedBanner> conditionally on the failed flag', () => {
    expect(SOURCE).toMatch(/\{daemonLaunchFailed\s*&&\s*<DaemonFailedBanner/)
  })

  it('still mounts <Onboarding> after setup completes (first-run flow preserved)', () => {
    expect(SOURCE).toMatch(/<Onboarding/)
  })

  it('keeps the existing settings overlay (Cmd+,) flow intact', () => {
    expect(SOURCE).toMatch(/<SettingsView\s+onClose=/)
    expect(SOURCE).toMatch(/JarvisHudView/)
  })
})
