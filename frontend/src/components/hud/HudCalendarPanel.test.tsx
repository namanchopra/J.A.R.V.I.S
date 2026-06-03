// ---------------------------------------------------------------------------
// HudCalendarPanel source-level contract test.
//
// The frontend does not ship `jsdom` / `@testing-library/react`, so we
// cannot mount the component in this test environment. Following the same
// pattern as `HudMeetingBanner.test.tsx` and `FirstRunDownloadOverlay.test.tsx`,
// we assert source-level invariants that pin the visual contract + event
// wiring for the always-visible meeting-record chip integrated into the
// calendar panel.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './HudCalendarPanel.tsx?raw'

describe('HudCalendarPanel meeting chip -- bridge wrappers', () => {
  it('calls StartMeeting via the window.go.main.App runtime bridge', () => {
    // Runtime bridge pattern: the binding may not be in the generated
    // wrapper yet, so we look it up on window.go.main.App at call time.
    expect(SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.StartMeeting/)
  })

  it('calls StopMeeting via the window.go.main.App runtime bridge', () => {
    expect(SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.StopMeeting/)
  })

  it('calls IsMeetingActive via the window.go.main.App runtime bridge', () => {
    expect(SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.IsMeetingActive/)
  })

  it('calls ProbeMeetingPermission via the runtime bridge for first-launch gating', () => {
    // Same first-launch probe pattern as OverlayView -- surfaces the macOS
    // Screen Recording dialog BEFORE the real StartMeeting call.
    expect(SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.ProbeMeetingPermission/)
  })
})

describe('HudCalendarPanel meeting chip -- event wiring', () => {
  it('subscribes to the meeting:state lifecycle event for live sync', () => {
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]meeting:state['"]/)
  })

  it('subscribes to the meeting:permission_error event for denial feedback', () => {
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]meeting:permission_error['"]/)
  })

  it("imports EventsOn + BrowserOpenURL from the wails runtime", () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*EventsOn[^}]*\}\s*from\s+['"][^'"]*wailsjs\/runtime\/runtime['"]/,
    )
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*BrowserOpenURL[^}]*\}\s*from\s+['"][^'"]*wailsjs\/runtime\/runtime['"]/,
    )
  })
})

describe('HudCalendarPanel meeting chip -- permission probe gating', () => {
  it('shares the jarvis:meetingPermissionProbed localStorage key with OverlayView', () => {
    // Critical: the SAME key as OverlayView so a probe done from the
    // overlay also counts here -- we never want to probe the user twice.
    expect(SOURCE).toMatch(/jarvis:meetingPermissionProbed/)
  })

  it('gates the first-launch probe behind localStorage.getItem', () => {
    expect(SOURCE).toMatch(/localStorage\.getItem\(\s*MEETING_PROBE_LOCALSTORAGE_KEY/)
    expect(SOURCE).toMatch(/localStorage\.setItem\(\s*MEETING_PROBE_LOCALSTORAGE_KEY/)
  })

  it('wires the permission-error CTA to the Screen Recording deep-link', () => {
    // System Settings -> Privacy -> Screen Recording — mirrors OverlayView
    // and MeetingPanel so the user gets a single consistent path to grant.
    expect(SOURCE).toMatch(/Privacy_ScreenRecording/)
    expect(SOURCE).toMatch(/BrowserOpenURL\(/)
  })
})

describe('HudCalendarPanel meeting chip -- copy + visual contract', () => {
  it('renders the idle-state START MEETING label', () => {
    expect(SOURCE).toMatch(/START MEETING|Start Meeting/i)
  })

  it('renders the active-state STOP MEETING label', () => {
    expect(SOURCE).toMatch(/STOP MEETING|Stop Meeting/i)
  })

  it('uses the cyan HUD accent for the idle chip', () => {
    expect(
      /var\(--accent-blue/.test(SOURCE) || /#00e5ff/i.test(SOURCE),
    ).toBe(true)
  })

  it('uses the red active-state accent (#ff4444) for the recording chip', () => {
    expect(SOURCE).toMatch(/#ff4444/)
  })

  it('uses the monospace label vocabulary', () => {
    expect(SOURCE).toMatch(/'SF Mono'\s*,\s*'Menlo'\s*,\s*monospace/)
  })
})

describe('HudCalendarPanel meeting chip -- failure modes', () => {
  it('handles a missing binding with the safe-default pattern (console.warn)', () => {
    // Mirrors the safe-default fallback used by HudMeetingBanner +
    // OverlayView. A stale dev build with no StartMeeting wrapper must
    // not crash the calendar panel.
    expect(SOURCE).toMatch(/binding unavailable|console\.warn/)
  })

  it('shows a transient "Notes saved" toast after StopMeeting resolves', () => {
    // The toast surfaces the markdown notes path the daemon wrote, then
    // auto-clears after ~6 seconds so it doesn't linger in the panel.
    expect(SOURCE).toMatch(/Notes saved/)
  })
})

describe('HudCalendarPanel meeting chip -- replay button', () => {
  // The Replay button sits next to the START/STOP chip and asks the daemon
  // to replay the cached _LAST_MEETING_RECAP (TASK-008) via the
  // TriggerMeetingRecap Wails binding. It is gated by hasRecapAvailable so
  // the button only renders after the first successful StopMeeting() call
  // in this session and disappears again when a new meeting starts.
  it('calls TriggerMeetingRecap via the window.go.main.App runtime bridge', () => {
    expect(SOURCE).toMatch(/window\?\.go\?\.main\?\.App\?\.TriggerMeetingRecap/)
  })

  it('exposes the title text "Replay last spoken recap" on the button', () => {
    expect(SOURCE).toMatch(/Replay last spoken recap/)
  })

  it('uses the ⟳ (U+27F3) glyph for the replay button', () => {
    expect(SOURCE).toMatch(/⟳/)
  })

  it('gates render of the replay button on a hasRecapAvailable state', () => {
    // The flag is set true after a successful Stop and cleared when the
    // next meeting:state=active event arrives. Pin on the state name so a
    // refactor that swaps it for a different gating mechanism trips here.
    expect(SOURCE).toMatch(/hasRecapAvailable/)
  })
})

describe('HudCalendarPanel meeting chip -- always-visible contract', () => {
  it('defines a MeetingChip sub-component rendered in all calendar states', () => {
    // The chip must render in: has-events, connected-no-events, AND
    // disconnected. Asserting the sub-component name + usage count keeps
    // the contract simple — three render sites means three mentions.
    expect(SOURCE).toMatch(/function MeetingChip\(/)
    const matches = SOURCE.match(/<MeetingChip\s*\/>/g) ?? []
    expect(matches.length).toBeGreaterThanOrEqual(3)
  })
})
