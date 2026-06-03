// ---------------------------------------------------------------------------
// HudMeetingBanner source-level contract test.
//
// The frontend does not ship `jsdom` / `@testing-library/react`, so we
// cannot mount the component in this test environment. Following the same
// pattern as `FirstRunDownloadOverlay.test.tsx`, we assert source-level
// invariants that pin the visual contract + event wiring.
// ---------------------------------------------------------------------------

import { describe, it, expect } from 'vitest'
import SOURCE from './HudMeetingBanner.tsx?raw'
import HUD_SOURCE from '../JarvisHudView.tsx?raw'

describe('HudMeetingBanner -- config + types', () => {
  it('reads meetingKeywords and meetingAutoSuggest from config', () => {
    expect(SOURCE).toMatch(/meetingKeywords/)
    expect(SOURCE).toMatch(/meetingAutoSuggest/)
  })

  it('declares the CalendarEvent shape (id/title/start/end)', () => {
    expect(SOURCE).toMatch(/interface\s+CalendarEvent/)
    expect(SOURCE).toMatch(/id\?:\s*string/)
    expect(SOURCE).toMatch(/title\?:\s*string/)
    expect(SOURCE).toMatch(/start\?:\s*string/)
    expect(SOURCE).toMatch(/end\?:\s*string/)
  })
})

describe('HudMeetingBanner -- polling', () => {
  it('polls upcoming events at the configured interval via setInterval', () => {
    expect(SOURCE).toMatch(/setInterval/)
    expect(SOURCE).toMatch(/pollIntervalMs/)
  })

  it('exposes pollIntervalMs as a prop so tests can override it', () => {
    expect(SOURCE).toMatch(/pollIntervalMs\?:\s*number/)
    expect(SOURCE).toMatch(/pollIntervalMs\s*=\s*POLL_INTERVAL_MS/)
  })

  it('defaults the poll cadence to 15 seconds', () => {
    // 15s = 15_000ms — snappier than the 60s calendar panel because the
    // <2min window is small.
    expect(SOURCE).toMatch(/POLL_INTERVAL_MS\s*=\s*15_000/)
  })

  it('calls GoogleCalendarGetUpcomingEvents via the runtime bridge', () => {
    expect(SOURCE).toMatch(/GoogleCalendarGetUpcomingEvents/)
    expect(SOURCE).toMatch(/window\?\.\s*go\?\.\s*main\?\.\s*App\?\./)
  })
})

describe('HudMeetingBanner -- matching window + keywords', () => {
  it('filters by meeting keywords (lowercase, substring)', () => {
    expect(SOURCE).toMatch(/toLowerCase\(\)/)
    expect(SOURCE).toMatch(/\.includes\(/)
  })

  it('enforces the 2-minute window (WINDOW_BEFORE_MS)', () => {
    expect(SOURCE).toMatch(/WINDOW_BEFORE_MS|2\s*\*\s*60_000/)
  })

  it('allows a 30s grace for late polls (WINDOW_AFTER_MS)', () => {
    expect(SOURCE).toMatch(/WINDOW_AFTER_MS/)
    expect(SOURCE).toMatch(/30_000/)
  })

  it('compares the delta against both window bounds', () => {
    // delta > WINDOW_BEFORE_MS rejects too-far-future events;
    // delta < -WINDOW_AFTER_MS rejects too-late events.
    expect(SOURCE).toMatch(/delta\s*>\s*WINDOW_BEFORE_MS/)
    expect(SOURCE).toMatch(/delta\s*<\s*-WINDOW_AFTER_MS/)
  })
})

describe('HudMeetingBanner -- dismiss + auto-stop refs', () => {
  it('dismiss persists across polls via a Set ref', () => {
    expect(SOURCE).toMatch(/dismissedIds/)
    expect(SOURCE).toMatch(/Set</)
  })

  it('skips events whose id is already dismissed', () => {
    expect(SOURCE).toMatch(/dismissedIds\.current\.has\(/)
    expect(SOURCE).toMatch(/dismissedIds\.current\.add\(/)
  })

  it('auto-stop only fires for banner-started meetings', () => {
    // Two refs are required to enforce the failure-case regression
    // guard: a manually-started meeting must NOT auto-stop.
    expect(SOURCE).toMatch(/startedFromBannerEventId/)
    expect(SOURCE).toMatch(/autoStopArmed/)
  })

  it('arms auto-stop only on a successful Start click', () => {
    expect(SOURCE).toMatch(/autoStopArmed\.current\s*=\s*\{\s*eventId/)
    expect(SOURCE).toMatch(/endsAt/)
  })

  it('clears tracking refs on meeting:state idle', () => {
    // A manual stop mid-meeting must not leave a stale auto-stop arm
    // pointing at a now-closed meeting.
    expect(SOURCE).toMatch(/startedFromBannerEventId\.current\s*=\s*null/)
    expect(SOURCE).toMatch(/autoStopArmed\.current\s*=\s*null/)
  })
})

describe('HudMeetingBanner -- event wiring', () => {
  it('subscribes to meeting:state for live sync', () => {
    expect(SOURCE).toMatch(/EventsOn\(\s*['"]meeting:state['"]/)
  })

  it("imports EventsOn from wails runtime", () => {
    expect(SOURCE).toMatch(
      /import\s*\{[^}]*EventsOn[^}]*\}\s*from\s+['"][^'"]*wailsjs\/runtime\/runtime['"]/,
    )
  })

  it('calls StartMeeting via the runtime bridge on the Start button', () => {
    expect(SOURCE).toMatch(/StartMeeting/)
    // Start handler also sets startedFromBannerEventId so auto-stop is armed.
    expect(SOURCE).toMatch(/await\s+callStartMeeting/)
  })

  it('calls StopMeeting via the runtime bridge for auto-stop', () => {
    expect(SOURCE).toMatch(/StopMeeting/)
    expect(SOURCE).toMatch(/callStopMeeting/)
  })

  it('suppresses the banner mid-meeting (IsMeetingActive gate)', () => {
    expect(SOURCE).toMatch(/IsMeetingActive/)
    // The poll early-returns when meetingActive is true.
    expect(SOURCE).toMatch(/if\s*\(\s*meetingActive\s*\)/)
  })
})

describe('HudMeetingBanner -- render contract', () => {
  it('renders null when no event matches', () => {
    expect(SOURCE).toMatch(/return null/)
  })

  it('renders an aria-live alert (accessibility)', () => {
    expect(SOURCE).toMatch(/role=['"]alert['"]/)
    expect(SOURCE).toMatch(/aria-live=['"]polite['"]/)
  })

  it('renders both START and DISMISS buttons', () => {
    expect(SOURCE).toMatch(/>\s*START\s*</)
    expect(SOURCE).toMatch(/>\s*DISMISS\s*</)
  })

  it('uses the cyan HUD accent token (#00e5ff / --accent-blue)', () => {
    expect(
      /var\(--accent-blue/.test(SOURCE) || /#00e5ff/i.test(SOURCE),
    ).toBe(true)
  })

  it('uses the monospace label vocabulary', () => {
    expect(SOURCE).toMatch(/'SF Mono'\s*,\s*'Menlo'\s*,\s*monospace/)
  })

  it('uses zIndex 40 -- floats above panels but below modal overlays', () => {
    expect(SOURCE).toMatch(/zIndex:\s*40/)
  })
})

describe('HudMeetingBanner -- failure modes', () => {
  it('missing bindings do not crash (console.warn fallback)', () => {
    // Mirrors the safe-default pattern from HudCalendarPanel.
    expect(SOURCE).toMatch(/console\.warn/)
  })

  it('auto-suggest=false disables the poll loop entirely', () => {
    // Guards against unnecessary network polls when the user has
    // explicitly turned the banner off in Settings.
    expect(SOURCE).toMatch(/if\s*\(\s*!autoSuggest\s*\)\s*return/)
  })
})

describe('JarvisHudView integration', () => {
  it('imports HudMeetingBanner from ./hud/HudMeetingBanner', () => {
    expect(HUD_SOURCE).toMatch(
      /import\s*\{[^}]*HudMeetingBanner[^}]*\}\s*from\s+['"][^'"]*hud\/HudMeetingBanner['"]/,
    )
  })

  it('mounts the HudMeetingBanner component', () => {
    expect(HUD_SOURCE).toMatch(/<HudMeetingBanner\b/)
  })
})
