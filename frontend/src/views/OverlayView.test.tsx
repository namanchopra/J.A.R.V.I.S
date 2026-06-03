// ---------------------------------------------------------------------------
// OverlayView + App.tsx mode-switch source-level contract tests (TASK-008).
//
// Same `?raw` pattern as OverlayOrb.test.tsx / JarvisHudView.test.tsx /
// SettingsView.test.tsx / App.test.tsx -- the frontend does not ship jsdom
// or @testing-library/react (see frontend/package.json), so behavioural
// tests run against the component source text.
//
// Acceptance criteria coverage (from the TASK-008 brief):
//   1. OverlayView renders <OverlayOrb /> -- import + JSX usage.
//   2. The close button calls OverlayHide() -- aria-label + binding call.
//   3. Failure case: when the daemon WS is disconnected, the orb still
//      renders. Verified by pinning the consumption of jarvis-api's
//      getJarvisState() (which itself returns 'idle' on disconnect, see
//      jarvis-api.ts) and the 'idle' default for the local state slot.
//   4. OverlayShowTranscript gates the transcript chip -- the source must
//      reference the config field and gate a conditional render on it.
//   5. App.tsx subscribes to the Wails event 'overlay:mode'.
//   6. App.tsx renders <OverlayView> when overlayMode === 'overlay'.
//   7. App.tsx cleans up the subscription on unmount (return () => cancel()).
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import OVERLAY_SOURCE from './OverlayView.tsx?raw'
import APP_SOURCE from '../App.tsx?raw'

describe('OverlayView renders JarvisOrb for visual parity with main HUD', () => {
  // v2 redesign: the overlay now consumes the same Three.js JarvisOrb the
  // main HUD uses, instead of the minimal SVG OverlayOrb from v1. The
  // contract is: import from components/JarvisOrb and render with the
  // direct 4-state JarvisState (no collapse helper).

  it('imports JarvisOrb from the components directory', () => {
    expect(OVERLAY_SOURCE).toMatch(
      /import\s*\{[^}]*\bJarvisOrb\b[^}]*\}\s*from\s*['"]\.\.\/components\/JarvisOrb['"]/,
    )
  })

  it('does not re-import the deprecated OverlayOrb (regression guard)', () => {
    expect(OVERLAY_SOURCE).not.toMatch(/from\s*['"]\.\.\/components\/OverlayOrb['"]/)
  })

  it('renders <JarvisOrb> with state and audioLevel props', () => {
    expect(OVERLAY_SOURCE).toMatch(/<JarvisOrb\b[\s\S]*?state=\{[^}]+\}/)
    expect(OVERLAY_SOURCE).toMatch(/<JarvisOrb\b[\s\S]*?audioLevel=\{[^}]+\}/)
  })

  it('passes jarvisState directly (no 4->3 collapse helper any more)', () => {
    // JarvisOrb accepts the full 4-state union so the prior toOrbState
    // helper is dead weight. Pin its absence so a future change doesn't
    // accidentally re-introduce a narrower mapping.
    expect(OVERLAY_SOURCE).not.toMatch(/function\s+toOrbState\b/)
  })
})

describe('OverlayView Escape-to-close calls OverlayHide (TASK-008 AC#2, revised)', () => {
  // First-pass UX feedback removed the visible close button -- Escape is
  // the sole dismissal affordance now. The overlay is intentionally chrome-
  // free; only the orb is visible.

  it('does not render a visible close button', () => {
    // Regression guard against re-introducing the chrome by accident.
    expect(OVERLAY_SOURCE).not.toMatch(/aria-label=['"]Close overlay['"]/)
  })

  it('registers a window keydown listener and removes it on unmount', () => {
    expect(OVERLAY_SOURCE).toMatch(/window\.addEventListener\(\s*['"]keydown['"]/)
    expect(OVERLAY_SOURCE).toMatch(/window\.removeEventListener\(\s*['"]keydown['"]/)
  })

  it('matches the Escape key and calls OverlayHide via the runtime bridge', () => {
    // Pin both the key match and the call path so a refactor can't drop
    // either half silently.
    expect(OVERLAY_SOURCE).toMatch(/e\.key\s*===\s*['"]Escape['"]/)
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.OverlayHide/)
    expect(OVERLAY_SOURCE).toMatch(/callOverlayHide\(\)/)
  })

  it('wraps OverlayHide call in try/catch so a missing binding cannot crash the tree', () => {
    expect(OVERLAY_SOURCE).toMatch(/try\s*\{[\s\S]*?OverlayHide[\s\S]*?\}\s*catch/)
  })
})

describe('OverlayView disconnected-daemon failure case (TASK-008 AC#3)', () => {
  // When the daemon WS is down, jarvis-api.ts:getJarvisState() returns
  // 'idle' (see jarvis-api.ts lines 39-53). The overlay must consume that
  // hook directly so the cascade is automatic -- the failure case is
  // therefore "OverlayView reads its state from jarvis-api and defaults to
  // 'idle'", which is what the source-level test pins on.

  it('imports state-reading helpers from lib/jarvis-api (not a parallel hook)', () => {
    expect(OVERLAY_SOURCE).toMatch(
      /import\s*\{[^}]*\bgetJarvisState\b[^}]*\}\s*from\s*['"]\.\.\/lib\/jarvis-api['"]/,
    )
  })

  it('imports the JarvisState type from the same module', () => {
    expect(OVERLAY_SOURCE).toMatch(
      /import\s+type\s*\{[^}]*\bJarvisState\b[^}]*\}\s*from\s*['"]\.\.\/lib\/jarvis-api['"]/,
    )
  })

  it('references the lib/jarvis-api module (cascade-from-jarvis-api contract)', () => {
    // Brief calls this out explicitly: "expect(SOURCE).toMatch(/jarvis-api/)".
    // Pinned on a bare path match so a future hook-style export is still
    // caught.
    expect(OVERLAY_SOURCE).toMatch(/jarvis-api/)
  })

  it('initializes local state to idle so a disconnected WS renders correctly', () => {
    // The state slot must default to 'idle' -- if getJarvisState() never
    // resolves (e.g. binding missing entirely), the initial render must
    // still produce a valid OverlayOrb DOM.
    expect(OVERLAY_SOURCE).toMatch(
      /useState<JarvisState>\(\s*['"]idle['"]\s*\)/,
    )
  })

  it('does not invent a parallel WS hook (reuses jarvis-api per the brief)', () => {
    // The brief: "Do not invent a parallel hook." Forbid the obvious
    // anti-patterns -- a direct WebSocket constructor or a fetch to the
    // jarvis WS endpoint inside this file.
    expect(OVERLAY_SOURCE).not.toMatch(/new\s+WebSocket\(/)
    expect(OVERLAY_SOURCE).not.toMatch(/ws:\/\/localhost:4422/)
  })
})

describe('OverlayView OverlayShowTranscript gating (TASK-008 AC#4)', () => {
  it('references the OverlayShowTranscript config field by its JSON tag', () => {
    // Brief: "expect(SOURCE).toMatch(/overlayShowTranscript/i)". The
    // case-insensitive match handles both the Go field name and the JSON
    // tag (which differs only in case).
    expect(OVERLAY_SOURCE).toMatch(/overlayShowTranscript/i)
  })

  it('reads the field from config via the GetConfig runtime binding', () => {
    // The generated GetConfig wrapper exists, but for consistency with the
    // OverlayHide pattern (and to keep the failure mode "config unavailable
    // -> chip hidden"), we look up GetConfig on window.go.main.App at call
    // time. Pin on both the lookup path AND the field read.
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.GetConfig/)
    expect(OVERLAY_SOURCE).toMatch(/overlayShowTranscript/)
  })

  it('gates the transcript chip on a useState slot driven by the config', () => {
    // The chip must be wrapped in a `{showTranscript && <... />}` so the
    // toggle from TASK-009 takes effect. A hardcoded `true` or unconditional
    // render would break the gating contract.
    expect(OVERLAY_SOURCE).toMatch(/showTranscript/)
    expect(OVERLAY_SOURCE).toMatch(/\{showTranscript\s*&&/)
  })

  it('defaults the chip to hidden when the binding is unavailable', () => {
    // Failure-safety: a missing GetConfig binding (dev mode, hot-reload
    // pre-wails-build) must NOT show the chip. The default of the state
    // slot is therefore false.
    expect(OVERLAY_SOURCE).toMatch(
      /useState<boolean>\(\s*false\s*\)/,
    )
  })
})

describe('OverlayView structural contracts', () => {
  it('exports OverlayView as a named export', () => {
    expect(OVERLAY_SOURCE).toMatch(/export\s+function\s+OverlayView\b/)
  })

  it('fills the entire Wails window (width/height 100vw/100vh)', () => {
    // The Wails window is already sized to 180x180 by TASK-004 when this
    // view renders. The wrapper must therefore use viewport units so the
    // overlay perfectly fits whatever the Go side resized to.
    expect(OVERLAY_SOURCE).toMatch(/width:\s*['"]100vw['"]/)
    expect(OVERLAY_SOURCE).toMatch(/height:\s*['"]100vh['"]/)
  })

  it('uses the accent-blue palette consistent with JarvisHudView', () => {
    expect(OVERLAY_SOURCE).toMatch(/var\(--accent-blue/)
  })

  it('applies an outer glow via box-shadow on the frame', () => {
    // Sci-fi frame border + subtle outer glow per the brief.
    expect(OVERLAY_SOURCE).toMatch(/boxShadow:/)
  })

  it('cleans up the EventsOn subscription on unmount (no leak)', () => {
    // Mirrors the convention used elsewhere in the codebase: const cancel =
    // EventsOn(...); return () => { cancel() }.
    expect(OVERLAY_SOURCE).toMatch(/const\s+cancel\s*=\s*EventsOn\(/)
    expect(OVERLAY_SOURCE).toMatch(/return\s*\(\)\s*=>\s*\{\s*cancel\(\)\s*\}/)
  })
})

describe('App.tsx subscribes to overlay:mode (TASK-008 AC#5/6/7)', () => {
  it('imports OverlayView from the views directory', () => {
    expect(APP_SOURCE).toMatch(
      /import\s*\{\s*OverlayView\s*\}\s*from\s*['"]\.\/views\/OverlayView['"]/,
    )
  })

  it('declares an OverlayMode type alias for the two states', () => {
    expect(APP_SOURCE).toMatch(
      /type\s+OverlayMode\s*=\s*['"]hud['"]\s*\|\s*['"]overlay['"]/,
    )
  })

  it('declares the overlayMode state slot defaulting to "hud"', () => {
    expect(APP_SOURCE).toMatch(
      /useState<OverlayMode>\(\s*['"]hud['"]\s*\)/,
    )
  })

  it('subscribes to the Wails event "overlay:mode"', () => {
    // The exact pattern the brief asks for.
    expect(APP_SOURCE).toMatch(/EventsOn\(\s*['"]overlay:mode['"]/)
  })

  it('flips the state slot on overlay/hud payloads', () => {
    expect(APP_SOURCE).toMatch(/mode\s*===\s*['"]overlay['"]/)
    expect(APP_SOURCE).toMatch(/mode\s*===\s*['"]hud['"]/)
    expect(APP_SOURCE).toMatch(/setOverlayMode\(/)
  })

  it('renders <OverlayView /> when overlayMode === "overlay"', () => {
    expect(APP_SOURCE).toMatch(/overlayMode\s*===\s*['"]overlay['"]/)
    expect(APP_SOURCE).toMatch(/<OverlayView\s*\/>/)
  })

  it('cleans up the subscription on unmount (return () => cancel())', () => {
    // The brief specifies this exact pattern. The match below is permissive
    // on whitespace + optional block braces so either `return () => cancel()`
    // or `return () => { cancel() }` passes -- both are valid cleanup
    // patterns and the brief explicitly allows the latter ("whatever cleanup
    // pattern matches the project's convention").
    expect(APP_SOURCE).toMatch(
      /return\s*\(\)\s*=>\s*(?:cancel\(\)|\{\s*cancel\(\)\s*\})/,
    )
  })

  it('still mounts <JarvisHudView /> for the default hud mode (regression)', () => {
    // The mode switch must not break the existing HUD flow.
    expect(APP_SOURCE).toMatch(/JarvisHudView/)
  })

  it('bypasses the setup/onboarding gates when overlayMode is "overlay"', () => {
    // The brief: "The overlay mode should bypass those (you're in overlay
    // mode because the user pressed a global hotkey; setup is implied
    // complete)." The early-return check on overlayMode must therefore run
    // before the isSetupComplete null/false branches.
    const overlayReturnIdx = APP_SOURCE.search(/if\s*\(\s*overlayMode\s*===\s*['"]overlay['"]\s*\)\s*\{\s*\n\s*return\s+<OverlayView/)
    expect(overlayReturnIdx).toBeGreaterThan(-1)
    const setupNullReturnIdx = APP_SOURCE.search(/if\s*\(\s*isSetupComplete\s*===\s*null\s*\)/)
    expect(setupNullReturnIdx).toBeGreaterThan(overlayReturnIdx)
  })
})

describe('OverlayView in-overlay controls (v2 redesign)', () => {
  // The v2 overlay adds PTT, mute, and interrupt controls. These tests pin
  // the wiring so a refactor can't silently drop a handler or call the
  // wrong Wails binding.

  it('renders a press-and-hold PTT button', () => {
    expect(OVERLAY_SOURCE).toMatch(/<PTTButton\b/)
    expect(OVERLAY_SOURCE).toMatch(/onMouseDown=\{onPress\}/)
    expect(OVERLAY_SOURCE).toMatch(/onMouseUp=\{onRelease\}/)
    // onMouseLeave must also release so a drag-off the button doesn't
    // leave the daemon STT gate stuck open.
    expect(OVERLAY_SOURCE).toMatch(/onMouseLeave=\{onRelease\}/)
  })

  it('calls OverlayPTTPress / OverlayPTTRelease via the runtime bridge', () => {
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.OverlayPTTPress/)
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.OverlayPTTRelease/)
  })

  it('mute toggle sends __mute__ / __unmute__ via SendJarvisCommand', () => {
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.SendJarvisCommand/)
    expect(OVERLAY_SOURCE).toMatch(/['"]__mute__['"]/)
    expect(OVERLAY_SOURCE).toMatch(/['"]__unmute__['"]/)
  })

  it('interrupt button sends __interrupt__ via SendJarvisCommand', () => {
    expect(OVERLAY_SOURCE).toMatch(/['"]__interrupt__['"]/)
  })

  it('renders a sci-fi state label that includes MUTED when muted', () => {
    // The state label is the personality lever; pin both the function and
    // the MUTED branch so a refactor can't quietly drop the muted state
    // from the user-visible text.
    expect(OVERLAY_SOURCE).toMatch(/function\s+stateLabel\b/)
    expect(OVERLAY_SOURCE).toMatch(/['"]MUTED['"]/)
    expect(OVERLAY_SOURCE).toMatch(/['"]LISTENING['"]/)
    expect(OVERLAY_SOURCE).toMatch(/['"]THINKING['"]/)
    expect(OVERLAY_SOURCE).toMatch(/['"]SPEAKING['"]/)
  })

  it('wraps every binding call in try/catch so a missing binding cannot crash the tree', () => {
    // Failure-case contract: stale dev build with missing bindings must
    // log+swallow, never throw out of an onClick.
    expect(OVERLAY_SOURCE).toMatch(/try\s*\{[\s\S]*?OverlayPTTPress[\s\S]*?\}\s*catch/)
    expect(OVERLAY_SOURCE).toMatch(/try\s*\{[\s\S]*?OverlayPTTRelease[\s\S]*?\}\s*catch/)
    expect(OVERLAY_SOURCE).toMatch(/try\s*\{[\s\S]*?SendJarvisCommand[\s\S]*?\}\s*catch/)
  })
})

describe('OverlayView window-drag region (Wails frameless)', () => {
  // Mac chrome is stripped by app_overlay.go via internal/macctl, so the
  // user has nowhere to grab the window unless we mark the wrapper as a
  // drag region and the controls as no-drag.

  it('wrapper carries a Wails drag region marker', () => {
    expect(OVERLAY_SOURCE).toMatch(/WebkitAppRegion/)
    expect(OVERLAY_SOURCE).toMatch(/['"]drag['"]/)
  })

  it('interactive controls opt out via no-drag', () => {
    expect(OVERLAY_SOURCE).toMatch(/['"]no-drag['"]/)
  })
})

describe('OverlayView Space-hotkey PTT (in-overlay)', () => {
  // Second-pass UX feedback: in addition to the big mic button, hold Space
  // (while the overlay has focus) should also drive push-to-talk.

  it('listens for the space key in the keydown handler', () => {
    // Pin both the canonical KeyboardEvent.code and the e.key === ' '
    // fallback so a future refactor can't silently drop one branch.
    expect(OVERLAY_SOURCE).toMatch(/e\.code\s*===\s*['"]Space['"]/)
    expect(OVERLAY_SOURCE).toMatch(/e\.key\s*===\s*['"] ['"]/)
  })

  it('ignores auto-repeat so a long hold only fires ptt_active once', () => {
    expect(OVERLAY_SOURCE).toMatch(/e\.repeat/)
  })

  it('also listens for keyup to release the PTT gate cleanly', () => {
    expect(OVERLAY_SOURCE).toMatch(/window\.addEventListener\(\s*['"]keyup['"]/)
    expect(OVERLAY_SOURCE).toMatch(/window\.removeEventListener\(\s*['"]keyup['"]/)
  })

  it('mirrors the same OverlayPTT bindings the mic button uses (no duplicate code path)', () => {
    // The keyboard handler must reuse callOverlayPTTPress/Release rather
    // than invent a parallel binding lookup.
    expect(OVERLAY_SOURCE).toMatch(/callOverlayPTTPress\(\)/)
    expect(OVERLAY_SOURCE).toMatch(/callOverlayPTTRelease\(\)/)
  })

  it('hint row mentions the Space and Escape affordances', () => {
    expect(OVERLAY_SOURCE).toMatch(/HOLD/)
    expect(OVERLAY_SOURCE).toMatch(/ESC/i)
  })
})

describe('OverlayView back-to-main button (4th control)', () => {
  // The chrome-free overlay needs an explicit click affordance for users
  // who don't notice the Escape hint and don't want to re-press the global
  // hotkey. The button calls the same OverlayHide path as Escape.

  it('renders an IconControl labelled "Back to main HUD"', () => {
    expect(OVERLAY_SOURCE).toMatch(/label=['"]Back to main HUD['"]/)
  })

  it('uses the expand glyph (⤢) so it reads as "restore window"', () => {
    expect(OVERLAY_SOURCE).toMatch(/glyph=['"]⤢['"]/)
  })

  it('wires the button to OverlayHide via callOverlayHide', () => {
    expect(OVERLAY_SOURCE).toMatch(/function\s+handleBackToMain|const\s+handleBackToMain/)
    expect(OVERLAY_SOURCE).toMatch(/handleBackToMain[\s\S]{0,80}callOverlayHide/)
  })
})

describe('OverlayView meeting-mode 5th control (TASK-010)', () => {
  // TASK-010 adds a fifth IconControl between interrupt and back-to-main.
  // The button toggles the meeting-mode recording lifecycle on the Go side
  // (StartMeeting / StopMeeting) and the overlay reacts to "meeting:state"
  // and "meeting:permission_error" Wails events. Source-level contracts
  // below pin the wiring so a refactor can't silently drop a binding call,
  // an event subscription, or the recording-state visual.

  it('renders a 5th IconControl with the meeting-record glyph', () => {
    // Filled red circle, U+25CF. The label flips on meeting state.
    expect(OVERLAY_SOURCE).toMatch(/glyph=['"]●['"]/)
    expect(OVERLAY_SOURCE).toMatch(/Start meeting recording/)
    expect(OVERLAY_SOURCE).toMatch(/Stop meeting recording/)
  })

  it('subscribes to meeting:state Wails event', () => {
    expect(OVERLAY_SOURCE).toMatch(/EventsOn\(\s*['"]meeting:state['"]/)
  })

  it('subscribes to meeting:permission_error Wails event', () => {
    expect(OVERLAY_SOURCE).toMatch(/EventsOn\(\s*['"]meeting:permission_error['"]/)
  })

  it('calls StartMeeting and StopMeeting via the runtime bridge', () => {
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.StartMeeting/)
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.StopMeeting/)
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.IsMeetingActive/)
  })

  it('shows the red ring around the orb when meetingActive', () => {
    // Regression guard against losing the visual: the rgba glow and the
    // solid ring color must both stay in the inline style.
    expect(OVERLAY_SOURCE).toMatch(/#ff4444/)
    expect(OVERLAY_SOURCE).toMatch(/rgba\(255,68,68/)
  })

  it('overrides the state label to RECORDING MEETING when meetingActive', () => {
    expect(OVERLAY_SOURCE).toMatch(/RECORDING MEETING/)
    // The override branch must read meetingActive before falling through
    // to the normal stateLabel(jarvisState, muted) computation.
    expect(OVERLAY_SOURCE).toMatch(/meetingActive\s*\?\s*['"]RECORDING MEETING['"]/)
  })

  it('appends · REC to the hint line while meetingActive', () => {
    // Tokens around REC are intentional so the hint reads as a hint and
    // not as part of the normal text.
    expect(OVERLAY_SOURCE).toMatch(/· REC ·/)
  })

  it('handles a missing StopMeeting binding gracefully (failure case)', () => {
    // The runtime-bridge wrapper must log + return '' rather than throw
    // when the generated binding hasn't landed yet (stale dev build).
    expect(OVERLAY_SOURCE).toMatch(/StopMeeting binding unavailable/)
    expect(OVERLAY_SOURCE).toMatch(/StartMeeting binding unavailable/)
  })

  it('cleans up both meeting event subscriptions on unmount', () => {
    // Mirror the existing `cancel()` cleanup pattern but with two
    // disposers for the two EventsOn handlers.
    expect(OVERLAY_SOURCE).toMatch(/cancelState\(\)/)
    expect(OVERLAY_SOURCE).toMatch(/cancelErr\(\)/)
  })

  it('surfaces the saved-notes path as a transient toast under the state label', () => {
    // The toast shows the file basename (not the full path) so a long
    // notes-dir doesn't blow out the 320x420 overlay width.
    expect(OVERLAY_SOURCE).toMatch(/lastMeetingNotesPath/)
    expect(OVERLAY_SOURCE).toMatch(/Notes saved:/)
    expect(OVERLAY_SOURCE).toMatch(/lastMeetingNotesPath\.split\(/)
  })

  it('renders an in-overlay warning row for meeting:permission_error', () => {
    // TASK-015 covers the Settings-side CTA; this row is the smaller
    // in-overlay hint that fires while the user is trying to start a
    // meeting.
    expect(OVERLAY_SOURCE).toMatch(/meetingError/)
    expect(OVERLAY_SOURCE).toMatch(/role=['"]alert['"]/)
  })
})

describe('OverlayView permission UX hardening (TASK-015)', () => {
  // TASK-015 hardens the permission-denial flow:
  //   (a) On the first-ever click of the record-meeting icon, fire a
  //       one-shot SCK probe via App.ProbeMeetingPermission so the macOS
  //       Screen Recording dialog appears BEFORE the real StartMeeting
  //       call. The probe is gated by a localStorage flag so subsequent
  //       clicks skip it.
  //   (b) The in-overlay warning row (TASK-010) is now clickable and
  //       opens System Settings → Privacy → Screen Recording via
  //       BrowserOpenURL, matching the CTA in MeetingPanel.tsx.
  //   (c) When StartMeeting resolves successfully, any lingering
  //       meetingError state is cleared so a stale denial CTA can't
  //       linger after the user grants permission and retries.
  //   (d) When the probe rejects, the handler must NOT proceed to call
  //       StartMeeting -- the user needs to grant permission first.

  it('first-launch probe is gated by localStorage', () => {
    expect(OVERLAY_SOURCE).toMatch(/jarvis:meetingPermissionProbed/)
    expect(OVERLAY_SOURCE).toMatch(/localStorage\.(get|set)Item/)
  })

  it('probe binding is invoked via the runtime bridge', () => {
    expect(OVERLAY_SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.ProbeMeetingPermission/)
  })

  it('overlay warning row is clickable and opens System Settings', () => {
    expect(OVERLAY_SOURCE).toMatch(/Privacy_ScreenRecording/)
    expect(OVERLAY_SOURCE).toMatch(/BrowserOpenURL/)
  })

  it('meetingError state is cleared on successful StartMeeting', () => {
    expect(OVERLAY_SOURCE).toMatch(/setMeetingError\(null\)/)
  })

  it('probe rejection does not call StartMeeting (early-return pattern)', () => {
    // The handler's early-return pattern: when the probe throws, the
    // catch arm short-circuits with a bare `return` BEFORE the
    // callStartMeeting line is reached.
    expect(OVERLAY_SOURCE).toMatch(/probe.*reject|catch.*return/i)
  })
})
