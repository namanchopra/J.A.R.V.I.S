import { useEffect, useRef, useState } from 'react'
import { EventsOn } from '../../../wailsjs/runtime/runtime'

// ---------------------------------------------------------------------------
// HudMeetingBanner -- top-center banner in the main HUD that surfaces a
// one-tap "start meeting note-taking" prompt when a calendar event is
// imminent + matches a meeting keyword + auto-suggest is enabled.
//
// Polling: 15s cadence. Cheaper than 5s for responsiveness, snappier
// than HudCalendarPanel's 60s because the relevant time-window is small.
//
// Lifecycle quirks (each tested below):
//   - [Dismiss] persists for the session only -- a refresh re-arms the
//     banner. Keyed by event.id.
//   - Auto-stop only fires when the meeting was started VIA the banner
//     (tracked via startedFromBannerEventId ref). Manually-started
//     meetings never auto-stop -- the user controls them.
//   - When IsMeetingActive() returns true (any other start path), the
//     banner is suppressed so we don't show "Start" mid-meeting.
//
// Failure modes:
//   - GoogleCalendarGetUpcomingEvents binding missing -> banner stays
//     hidden, no crash.
//   - StartMeeting throws -> log + leave banner up so user can retry.
//   - Auto-stop StopMeeting throws -> log + the manual button still
//     works, so the user can recover.
// ---------------------------------------------------------------------------

interface CalendarEvent {
  id?: string
  title?: string
  start?: string // RFC3339
  end?: string
}

interface MeetingConfigSubset {
  meetingKeywords: string[]
  meetingAutoSuggest: boolean
}

const POLL_INTERVAL_MS = 15_000
const WINDOW_BEFORE_MS = 2 * 60_000 // 2 min before start
const WINDOW_AFTER_MS = 30_000 // 30s grace after start
const UPCOMING_LIMIT = 5

// ---------------------------------------------------------------------------
// Wails runtime bridge wrappers
// ---------------------------------------------------------------------------
// All return safe defaults on missing bindings so a stale dev build
// doesn't blow up the banner.

async function callGetUpcoming(limit: number): Promise<CalendarEvent[]> {
  try {
    const fn = window?.go?.main?.App?.GoogleCalendarGetUpcomingEvents as
      | ((n: number) => Promise<CalendarEvent[]>)
      | undefined
    if (!fn) return []
    const evts = await fn(limit)
    return evts ?? []
  } catch (err) {
    console.warn('HudMeetingBanner: GoogleCalendarGetUpcomingEvents failed', err)
    return []
  }
}

async function callGetConfig(): Promise<MeetingConfigSubset> {
  try {
    const fn = window?.go?.main?.App?.GetConfig as
      | (() => Promise<Record<string, unknown>>)
      | undefined
    if (!fn) return { meetingKeywords: [], meetingAutoSuggest: true }
    const cfg = (await fn()) ?? {}
    const keywords = Array.isArray(cfg.meetingKeywords)
      ? (cfg.meetingKeywords as unknown[]).filter(
          (k): k is string => typeof k === 'string',
        )
      : []
    const autoSuggest =
      typeof cfg.meetingAutoSuggest === 'boolean'
        ? (cfg.meetingAutoSuggest as boolean)
        : true
    return { meetingKeywords: keywords, meetingAutoSuggest: autoSuggest }
  } catch (err) {
    console.warn('HudMeetingBanner: GetConfig failed', err)
    return { meetingKeywords: [], meetingAutoSuggest: true }
  }
}

async function callIsMeetingActive(): Promise<boolean> {
  try {
    const fn = window?.go?.main?.App?.IsMeetingActive as
      | (() => Promise<boolean>)
      | undefined
    if (!fn) return false
    return await fn()
  } catch (err) {
    console.warn('HudMeetingBanner: IsMeetingActive failed', err)
    return false
  }
}

async function callStartMeeting(title: string): Promise<void> {
  const fn = window?.go?.main?.App?.StartMeeting as
    | ((title: string) => Promise<void>)
    | undefined
  if (!fn) {
    // Binding missing — surface via console for the dev build but don't
    // throw. The banner stays up so the user can retry once bindings load.
    console.warn('HudMeetingBanner: StartMeeting binding unavailable')
    return
  }
  await fn(title)
}

async function callStopMeeting(): Promise<string> {
  const fn = window?.go?.main?.App?.StopMeeting as
    | (() => Promise<string>)
    | undefined
  if (!fn) {
    console.warn('HudMeetingBanner: StopMeeting binding unavailable')
    return ''
  }
  return await fn()
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface HudMeetingBannerProps {
  /** Override poll cadence for tests. Default 15_000ms. */
  pollIntervalMs?: number
}

export function HudMeetingBanner({
  pollIntervalMs = POLL_INTERVAL_MS,
}: HudMeetingBannerProps = {}): React.ReactElement | null {
  const [matchedEvent, setMatchedEvent] = useState<CalendarEvent | null>(null)
  const [keywords, setKeywords] = useState<string[]>([])
  const [autoSuggest, setAutoSuggest] = useState<boolean>(true)
  const [meetingActive, setMeetingActive] = useState<boolean>(false)

  // Session-scoped dismissals. Keyed by event id so a re-poll doesn't
  // resurrect the banner for the same event.
  const dismissedIds = useRef<Set<string>>(new Set<string>())
  // Tracks which event was started VIA this banner. Manually-started
  // meetings never auto-stop -- we only set this on a successful Start
  // click. Cleared on `meeting:state === 'idle'`.
  const startedFromBannerEventId = useRef<string | null>(null)
  // Armed only after a banner-started meeting; the poll loop fires
  // StopMeeting when Date.now() crosses endsAt. Cleared on idle.
  const autoStopArmed = useRef<{ eventId: string; endsAt: number } | null>(null)

  // Initial config load.
  useEffect(() => {
    void callGetConfig().then((cfg) => {
      setKeywords((cfg.meetingKeywords ?? []).map((k) => k.toLowerCase()))
      setAutoSuggest(cfg.meetingAutoSuggest ?? true)
    })
  }, [])

  // Initial meeting-active sync + subscription to the daemon's lifecycle
  // events. Any external start (overlay icon, voice command) also flips
  // `meetingActive`, so the banner correctly suppresses itself.
  useEffect(() => {
    void callIsMeetingActive().then(setMeetingActive)
    const cancel = EventsOn('meeting:state', (p: unknown) => {
      if (p === 'active') {
        setMeetingActive(true)
      } else if (p === 'idle') {
        setMeetingActive(false)
        // Auto-stop ran (or manual stop); clear tracking refs so a
        // future meeting in the same session doesn't see stale state.
        startedFromBannerEventId.current = null
        autoStopArmed.current = null
      }
    })
    return () => {
      cancel()
    }
  }, [])

  // Poll for upcoming events.
  useEffect(() => {
    if (!autoSuggest) return // disabled in settings; banner never fires.

    let cancelled = false

    const tick = async (): Promise<void> => {
      // Auto-stop arm check first (cheap, no network).
      const arm = autoStopArmed.current
      if (arm && Date.now() >= arm.endsAt && meetingActive) {
        void callStopMeeting().catch((e) =>
          console.warn('HudMeetingBanner: auto-stop failed', e),
        )
        autoStopArmed.current = null
      }

      // Don't poll for new banners while a meeting is in progress.
      if (meetingActive) {
        setMatchedEvent(null)
        return
      }

      const events = await callGetUpcoming(UPCOMING_LIMIT)
      if (cancelled) return
      const now = Date.now()
      for (const evt of events) {
        if (!evt.id || !evt.start || !evt.title) continue
        if (dismissedIds.current.has(evt.id)) continue

        const startMs = Date.parse(evt.start)
        if (Number.isNaN(startMs)) continue
        const delta = startMs - now
        // Window: starting in <2min AND not more than 30s late.
        if (delta > WINDOW_BEFORE_MS || delta < -WINDOW_AFTER_MS) continue

        const title = evt.title.toLowerCase()
        const matchedKw = keywords.find((kw) => title.includes(kw))
        if (!matchedKw) continue

        setMatchedEvent(evt)
        return
      }
      setMatchedEvent(null)
    }

    void tick()
    const id = setInterval(() => void tick(), pollIntervalMs)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [autoSuggest, keywords, meetingActive, pollIntervalMs])

  if (!matchedEvent) return null

  const minutesUntil = Math.max(
    0,
    Math.round((Date.parse(matchedEvent.start ?? '') - Date.now()) / 60_000),
  )

  const handleStart = async (): Promise<void> => {
    try {
      await callStartMeeting(matchedEvent.title ?? 'Meeting')
      startedFromBannerEventId.current = matchedEvent.id ?? null
      const endMs = matchedEvent.end ? Date.parse(matchedEvent.end) : NaN
      if (matchedEvent.id && !Number.isNaN(endMs)) {
        autoStopArmed.current = { eventId: matchedEvent.id, endsAt: endMs }
      }
      setMatchedEvent(null)
    } catch (err) {
      console.warn('HudMeetingBanner: StartMeeting failed', err)
      // Leave banner up so user can retry.
    }
  }

  const handleDismiss = (): void => {
    if (matchedEvent.id) {
      dismissedIds.current.add(matchedEvent.id)
    }
    setMatchedEvent(null)
  }

  return (
    <div
      role="alert"
      aria-live="polite"
      style={{
        // Top-center positioning -- coordinated with JarvisHudView's
        // z-index pattern for floating panels. ~540px wide so it doesn't
        // stretch across the full HUD; centered horizontally. z=40 sits
        // above the canvas (z=1/20) and below modal overlays (z=80/100).
        position: 'absolute',
        top: 12,
        left: '50%',
        transform: 'translateX(-50%)',
        width: 540,
        zIndex: 40,
        background: 'rgba(0,12,10,0.92)',
        border: '1px solid var(--accent-blue, #00e5ff)',
        boxShadow:
          '0 0 16px rgba(0,229,255,0.25), inset 0 0 12px rgba(0,229,255,0.08)',
        borderRadius: 4,
        padding: '12px 16px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 12,
        color: 'var(--accent-blue, #00e5ff)',
        fontFamily: "'SF Mono', 'Menlo', monospace",
      }}
    >
      <div style={{ flex: '1 1 auto', minWidth: 0 }}>
        <p
          style={{
            fontSize: 9,
            letterSpacing: '0.24em',
            opacity: 0.7,
            marginBottom: 2,
          }}
        >
          ⌖ MEETING IN ~{minutesUntil}M
        </p>
        <p
          style={{
            fontSize: 13,
            fontWeight: 700,
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}
        >
          {matchedEvent.title}
        </p>
      </div>
      <div style={{ display: 'flex', gap: 8, flex: '0 0 auto' }}>
        <button
          type="button"
          onClick={() => void handleStart()}
          style={{
            border: '1px solid var(--accent-blue, #00e5ff)',
            background: 'rgba(0,229,255,0.15)',
            color: 'var(--accent-blue, #00e5ff)',
            padding: '6px 14px',
            fontSize: 11,
            letterSpacing: '0.12em',
            cursor: 'pointer',
            fontFamily: 'inherit',
          }}
        >
          START
        </button>
        <button
          type="button"
          onClick={handleDismiss}
          style={{
            border: '1px solid rgba(207, 231, 255, 0.35)',
            background: 'transparent',
            color: 'rgba(207, 231, 255, 0.7)',
            padding: '6px 14px',
            fontSize: 11,
            letterSpacing: '0.12em',
            cursor: 'pointer',
            fontFamily: 'inherit',
          }}
        >
          DISMISS
        </button>
      </div>
    </div>
  )
}
