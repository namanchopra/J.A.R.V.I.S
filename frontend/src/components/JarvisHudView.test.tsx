// ---------------------------------------------------------------------------
// JarvisHudView mute behavior — regression test for the "permanent mute"
// footgun on fresh installs.
//
// Bug history: an earlier version of JarvisHudView seeded `isMuted` from
// `localStorage["jarvis-muted"]` on mount and unconditionally dispatched
// `sendJarvisCommand("__mute__")` if it was truthy. The effect of this was
// that any user who had ever toggled mute would launch every subsequent
// session muted, with no obvious cause — including users installing a fresh
// DMG, if their browser data carried the key over.
//
// Contract:
//   1. The component MUST NOT read `localStorage["jarvis-muted"]` to seed
//      mute state.
//   2. The component MUST NOT dispatch `sendJarvisCommand("__mute__")` from a
//      mount-time `useEffect` based on persisted state.
//   3. The in-session mute toggle (button / keyboard shortcut) MUST remain
//      functional — i.e., `sendJarvisCommand` is still imported and invoked
//      with `__mute__` / `__unmute__` literals elsewhere in the source.
//
// Why a source-level contract test instead of a DOM-level test:
//   the frontend does not ship `jsdom` / `@testing-library/react`, so we
//   cannot mount the component in this test environment without adding new
//   dependencies. A source-level regression test catches the exact bug
//   pattern and is sufficient for this scope. If/when DOM testing
//   infrastructure is added, this test can be supplemented (not replaced)
//   with a true mount-time spec.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
// Vite/vitest support importing arbitrary files as strings via the `?raw`
// suffix, so we avoid pulling in `node:fs` / `@types/node` here.
import SOURCE from './JarvisHudView.tsx?raw'

describe('JarvisHudView mute persistence (TASK-002)', () => {
  it('does NOT read localStorage["jarvis-muted"] via getItem', () => {
    // Regex tolerates whitespace variations but pins on the exact key.
    expect(SOURCE).not.toMatch(/localStorage\.getItem\(\s*['"`]jarvis-muted['"`]\s*\)/)
    expect(SOURCE).not.toMatch(/getItem\(\s*MUTE_STORAGE_KEY\s*\)/)
  })

  it('does NOT write localStorage["jarvis-muted"] via setItem', () => {
    expect(SOURCE).not.toMatch(/localStorage\.setItem\(\s*['"`]jarvis-muted['"`]/)
    expect(SOURCE).not.toMatch(/setItem\(\s*MUTE_STORAGE_KEY/)
  })

  it('initializes mute state to literal false (always unmuted on mount)', () => {
    // `useState<boolean>(false)` for the mute slot. We assert the literal
    // initial value rather than the surrounding declaration name to keep the
    // test resilient to minor formatting changes.
    expect(SOURCE).toMatch(/useState<boolean>\(\s*false\s*\)/)
  })

  it('does NOT auto-dispatch __mute__ from a mount-time effect', () => {
    // Find every `sendJarvisCommand(...)` invocation whose argument list
    // mentions the `__mute__` literal (covers both plain string args and
    // ternary forms like `next ? '__mute__' : '__unmute__'`). For each call,
    // assert it is NOT inside a `useEffect(` scope — only inside a
    // user-action handler (useCallback / function). This catches the
    // historical bug of seeding mute from localStorage on mount.
    const callRegex = /sendJarvisCommand\(\s*[^)]*__mute__[^)]*\)/g
    const calls = [...SOURCE.matchAll(callRegex)]
    expect(calls.length).toBeGreaterThan(0) // toggleMute still calls it

    for (const call of calls) {
      // Look at the ~400 chars preceding the call. The closest enclosing
      // syntactic scope (useEffect / useCallback / function / arrow) must
      // NOT be a useEffect. We use a coarse heuristic because we just need
      // to catch the specific anti-pattern.
      const before = SOURCE.slice(Math.max(0, (call.index ?? 0) - 400), call.index)
      const lastUseEffect = before.lastIndexOf('useEffect(')
      const lastUseCallback = before.lastIndexOf('useCallback(')
      const lastFn = Math.max(before.lastIndexOf('function '), before.lastIndexOf('=> {'))

      // The closest enclosing scope must NOT be a useEffect — it must be a
      // user-action callback (useCallback or a handler function).
      const insideMountEffect =
        lastUseEffect > lastUseCallback && lastUseEffect > lastFn
      expect(insideMountEffect).toBe(false)
    }
  })

  it('still imports sendJarvisCommand so the in-session toggle works', () => {
    expect(SOURCE).toMatch(/from\s+['"][^'"]*jarvis-api['"]/)
    expect(SOURCE).toMatch(/sendJarvisCommand/)
  })

  it('still dispatches __unmute__ from the toggle path', () => {
    // The toggle must support both directions; otherwise a mute would be
    // sticky for the rest of the session.
    expect(SOURCE).toMatch(/sendJarvisCommand\(\s*[^)]*__unmute__/)
  })

  it('clears the legacy localStorage key on mount (best effort cleanup)', () => {
    // Optional but documented behavior: prevent stale keys from older
    // builds from sitting around in user storage forever.
    expect(SOURCE).toMatch(/localStorage\.removeItem\(\s*['"`]jarvis-muted['"`]\s*\)/)
  })
})

// ---------------------------------------------------------------------------
// TASK-026 — Mic permission denied banner.
//
// Contract:
//   1. The component imports `GetMicPermissionStatus` (the binding added by
//      TASK-025) and `BrowserOpenURL` from the Wails runtime.
//   2. There is a render branch with `role="alert"` that includes the
//      "Microphone" copy and references the `denied` / `restricted` states.
//   3. The banner's button calls `BrowserOpenURL` with the documented
//      `x-apple.systempreferences:` URL.
// ---------------------------------------------------------------------------

describe('JarvisHudView mic permission banner (TASK-026)', () => {
  it('imports GetMicPermissionStatus from the App bindings', () => {
    expect(SOURCE).toMatch(/GetMicPermissionStatus/)
    // Either the named import or a direct `window.go.main.App.GetMicPermissionStatus`
    // lookup is acceptable — both ultimately invoke the same binding.
    expect(SOURCE).toMatch(
      /(?:import[\s\S]*?GetMicPermissionStatus[\s\S]*?from\s+['"][^'"]*wailsjs\/go\/main\/App['"])|(?:GetMicPermissionStatus\s+as)|(?:window\?\.go\?\.main\?\.App\?\.GetMicPermissionStatus)/,
    )
  })

  it('imports BrowserOpenURL from the Wails runtime', () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*BrowserOpenURL[^}]*\}\s*from\s+['"][^'"]*wailsjs\/runtime\/runtime['"]/,
    )
  })

  it('renders a role="alert" banner that mentions Microphone', () => {
    expect(SOURCE).toMatch(/role=['"]alert['"]/)
    expect(SOURCE).toMatch(/Microphone/)
  })

  it('gates the banner render on denied or restricted state', () => {
    // Tolerate either string-equality form or the union-state check.
    expect(SOURCE).toMatch(/['"]denied['"]/)
    expect(SOURCE).toMatch(/['"]restricted['"]/)
    expect(SOURCE).toMatch(/micPermissionState/)
  })

  it('calls BrowserOpenURL with the macOS System Settings privacy URL', () => {
    // Either the literal URL or a constant referencing it is acceptable.
    expect(SOURCE).toMatch(
      /x-apple\.systempreferences:com\.apple\.preference\.security\?Privacy_Microphone/,
    )
    expect(SOURCE).toMatch(/BrowserOpenURL\(/)
  })

  it('polls mic permission on a ≤5s interval so the banner auto-clears', () => {
    // The constant must resolve to ≤5000ms. We pin on the literal that ships
    // in the source (4000) — if a future patch wants to widen the window, it
    // can update this test alongside the constant.
    expect(SOURCE).toMatch(/MIC_PERMISSION_POLL_MS\s*=\s*4000/)
    expect(SOURCE).toMatch(/setInterval\([\s\S]*?MIC_PERMISSION_POLL_MS\s*\)/)
  })
})
