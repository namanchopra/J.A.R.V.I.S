import { useCallback, useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { BrowserOpenURL, EventsOn } from '../../../wailsjs/runtime/runtime'
import '../../lib/hud-theme'

// ---------------------------------------------------------------------------
// HudCalendarPanel -- right-column panel that shows the next 1-5 upcoming
// Google Calendar events. Polls the Wails bindings every 60s and renders
// nothing-ish (a "Connect Google Calendar" prompt) when the user hasn't
// signed in via Settings -> Connections.
//
// Why a separate panel rather than reusing HudActivityPanel:
//   - Activity events are session/approval/plan lifecycle ticks; calendar
//     events are a different domain with a different cadence (mins-to-
//     hours, not seconds-to-minutes).
//   - The next-event headline is itself a status signal ("meeting in 14m"),
//     so it deserves prominent placement instead of being one row among
//     dozens of session events.
//
// Failure modes:
//   - Bindings missing (stale dev build) -> renders the disconnected state.
//   - GetNextEvent throws -> logged, retried at next poll tick.
//   - User disconnects calendar -> next poll switches to the disconnected
//     CTA without remounting the panel.
// ---------------------------------------------------------------------------

// CalendarEvent shape mirrors model.CalendarEvent in internal/model/gcal.go.
// Keep field names in JSON-tag form (camelCase) to match what Wails emits.
interface CalendarEvent {
  id?: string
  title?: string
  start?: string  // RFC3339 string after Wails JSON serialization
  end?: string
  attendees?: string[]
  location?: string
  htmlLink?: string
  timeZone?: string
}

// NextEventSnapshot is the compact projection from internal/model/gcal.go.
// RelativeTime is server-formatted ("in 14m" / "now" / "in 2h") so we
// don't ship time-math to the frontend.
interface NextEventSnapshot {
  title?: string
  start?: string
  relativeTime?: string
  location?: string
}

interface HudCalendarPanelProps {
  /** Poll cadence in ms. Default 60s. Tests override to fire faster. */
  pollIntervalMs?: number
}

const DEFAULT_POLL_MS = 60_000
const UPCOMING_LIMIT = 5

// ---------------------------------------------------------------------------
// Wails runtime bridge wrappers
// ---------------------------------------------------------------------------

async function callIsConnected(): Promise<boolean> {
  try {
    const fn = window?.go?.main?.App?.GoogleCalendarIsConnected as
      | (() => Promise<boolean>)
      | undefined
    return fn ? await fn() : false
  } catch {
    return false
  }
}

async function callGetNext(): Promise<NextEventSnapshot | null> {
  try {
    const fn = window?.go?.main?.App?.GoogleCalendarGetNextEvent as
      | (() => Promise<NextEventSnapshot | null>)
      | undefined
    if (!fn) return null
    const evt = await fn()
    return evt ?? null
  } catch {
    return null
  }
}

async function callGetUpcoming(limit: number): Promise<CalendarEvent[]> {
  try {
    const fn = window?.go?.main?.App?.GoogleCalendarGetUpcomingEvents as
      | ((n: number) => Promise<CalendarEvent[]>)
      | undefined
    if (!fn) return []
    const evts = await fn(limit)
    return evts ?? []
  } catch {
    return []
  }
}

// ---------------------------------------------------------------------------
// Meeting chip — Wails bridge wrappers
// ---------------------------------------------------------------------------
// The chip exposes the same Start/Stop/IsActive triplet that
// HudMeetingBanner + OverlayView already consume. All bindings are accessed
// at call time so a stale dev build with missing wrappers degrades to a
// no-op + console.warn rather than crashing the calendar panel.

async function callIsMeetingActive(): Promise<boolean> {
  try {
    const fn = window?.go?.main?.App?.IsMeetingActive as
      | (() => Promise<boolean>)
      | undefined
    if (!fn) return false
    return await fn()
  } catch (err) {
    console.warn('HudCalendarPanel: IsMeetingActive failed', err)
    return false
  }
}

async function callStartMeeting(title: string): Promise<void> {
  const fn = window?.go?.main?.App?.StartMeeting as
    | ((title: string) => Promise<void>)
    | undefined
  if (!fn) {
    // Binding unavailable — surface via console.warn for the dev build but
    // don't throw. The chip stays idle so the user can retry once bindings
    // load (same safe-default pattern as HudMeetingBanner).
    console.warn('HudCalendarPanel: StartMeeting binding unavailable')
    return
  }
  await fn(title)
}

async function callStopMeeting(): Promise<string> {
  const fn = window?.go?.main?.App?.StopMeeting as
    | (() => Promise<string>)
    | undefined
  if (!fn) {
    console.warn('HudCalendarPanel: StopMeeting binding unavailable')
    return ''
  }
  return await fn()
}

async function callTriggerMeetingRecap(): Promise<void> {
  // Replay the cached _LAST_MEETING_RECAP via the daemon. The chip surfaces
  // a Replay button next to STOP after the first successful stop in this
  // session, gated by the hasRecapAvailable state below. A missing binding
  // degrades to console.warn (same safe-default pattern as the other
  // bridges in this file) so a stale dev build doesn't crash the panel.
  const fn = window?.go?.main?.App?.TriggerMeetingRecap as
    | (() => Promise<void>)
    | undefined
  if (!fn) {
    console.warn('HudCalendarPanel: TriggerMeetingRecap binding unavailable')
    return
  }
  await fn()
}

async function callProbeMeetingPermission(): Promise<void> {
  // Mirrors OverlayView.callProbeMeetingPermission: re-throws on failure so
  // the caller can early-return from the click handler and skip StartMeeting
  // when permission was denied. The Go side emits the permission_error event
  // for UI feedback either way.
  const fn = window?.go?.main?.App?.ProbeMeetingPermission as
    | (() => Promise<void>)
    | undefined
  if (!fn) {
    console.warn('HudCalendarPanel: ProbeMeetingPermission binding unavailable')
    return
  }
  await fn()
}

// macOS Screen Recording deep-link. Duplicated from OverlayView /
// MeetingPanel rather than imported — the calendar panel is part of the
// main HUD bundle and we want zero cross-view coupling for a 1-line const.
const SYSTEM_SETTINGS_SCREEN_RECORDING_URL =
  'x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenRecording'

// Shared with OverlayView so a probe done from the overlay also counts
// here — we never want to probe twice. Stored as the literal "1"; only
// presence/absence matters.
const MEETING_PROBE_LOCALSTORAGE_KEY = 'jarvis:meetingPermissionProbed'

// How long the "✓ Notes saved" confirmation lingers before reverting to
// the idle chip. ~6 seconds matches OverlayView's transient toast.
const NOTES_SAVED_TOAST_MS = 6000

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

/** Format a Date / RFC3339 string as compact local time ("14:30"). */
function formatTime(raw?: string): string {
  if (!raw) return ''
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return ''
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${hh}:${mm}`
}

/** "Mon" / "Tue" / ... for events more than 24h out. */
function formatDay(raw?: string): string {
  if (!raw) return ''
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return ''
  const today = new Date()
  const sameDay =
    d.getFullYear() === today.getFullYear() &&
    d.getMonth() === today.getMonth() &&
    d.getDate() === today.getDate()
  if (sameDay) return 'Today'
  const tomorrow = new Date(today)
  tomorrow.setDate(today.getDate() + 1)
  const sameTomorrow =
    d.getFullYear() === tomorrow.getFullYear() &&
    d.getMonth() === tomorrow.getMonth() &&
    d.getDate() === tomorrow.getDate()
  if (sameTomorrow) return 'Tomorrow'
  return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
}

// ---------------------------------------------------------------------------
// MeetingChip — always-visible record control
// ---------------------------------------------------------------------------
// Sits inside the calendar panel and renders in ALL three states
// (has-events / connected-no-events / disconnected) so the user can manually
// start a meeting recording without leaving the main HUD and without having
// to open the overlay's 5-icon row.
//
// State machine:
//   idle  --click--> (first launch only) probe permission
//                --> StartMeeting(...)  --> 'active' via meeting:state
//   active --click--> StopMeeting() --> path --> show "✓ Notes saved" toast
//                                              --> auto-clear after ~6s
//                                              --> 'idle' via meeting:state
//
// Failure modes mirror OverlayView:
//   - Binding missing -> console.warn, chip stays idle.
//   - StartMeeting rejects on permission denial -> meeting:permission_error
//     fires and we render a clickable warning row below the chip linking
//     to System Settings → Privacy → Screen Recording.

function MeetingChip(): React.ReactElement {
  const [meetingActive, setMeetingActive] = useState<boolean>(false)
  const [meetingError, setMeetingError] = useState<string | null>(null)
  const [lastNotesPath, setLastNotesPath] = useState<string | null>(null)
  // hasRecapAvailable gates the ⟳ Replay button next to the main chip.
  // Set true after the first successful StopMeeting() resolves in this
  // session; reset to false whenever a new meeting becomes active (the
  // daemon's _LAST_MEETING_RECAP cache is cleared on the next start, so
  // the prior recap stops being meaningful). Intentionally not persisted
  // across reloads — the daemon's cache is in-memory only, so a daemon
  // restart invalidates the replay target anyway.
  const [hasRecapAvailable, setHasRecapAvailable] = useState<boolean>(false)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  // Initial state sync + lifecycle event subscriptions. Any external
  // start/stop (overlay icon, HudMeetingBanner, voice command) flips
  // meetingActive so the chip stays in lockstep with the daemon truth.
  useEffect(() => {
    let cancelled = false
    void callIsMeetingActive().then((active) => {
      if (!cancelled && mountedRef.current) setMeetingActive(active)
    })
    const cancelState = EventsOn('meeting:state', (payload: unknown) => {
      if (payload === 'active') {
        setMeetingActive(true)
        // A new meeting invalidates any prior cached recap on the daemon
        // side; clear the replay-availability flag so the ⟳ button hides
        // until the next StopMeeting() resolves successfully.
        setHasRecapAvailable(false)
      } else if (payload === 'idle') {
        setMeetingActive(false)
      }
    })
    const cancelErr = EventsOn('meeting:permission_error', (payload: unknown) => {
      const msg =
        typeof payload === 'string' && payload.length > 0
          ? payload
          : 'Screen Recording permission required'
      setMeetingError(msg)
    })
    return () => {
      cancelled = true
      cancelState()
      cancelErr()
    }
  }, [])

  const handleClick = useCallback((): void => {
    if (meetingActive) {
      // Stop the active recording. The path returned by StopMeeting drives
      // the transient "✓ Notes saved" toast; meeting:state=idle clears
      // meetingActive shortly after.
      void (async () => {
        const path = await callStopMeeting().catch((err) => {
          console.warn('HudCalendarPanel: StopMeeting rejected', err)
          return ''
        })
        if (path && mountedRef.current) {
          setLastNotesPath(path)
          // The daemon has now cached _LAST_MEETING_RECAP, so the ⟳ button
          // can render. Stays true until the next 'meeting:state'=active
          // event clears it (see the EventsOn handler above).
          setHasRecapAvailable(true)
          setTimeout(() => {
            if (mountedRef.current) setLastNotesPath(null)
          }, NOTES_SAVED_TOAST_MS)
        }
      })()
      return
    }

    // Idle → Start. First-launch permission probe is gated by the SAME
    // localStorage key used by OverlayView so a probe done from the
    // overlay also counts here — we never want to probe twice. After the
    // probe succeeds (or was already done previously), fall through to
    // the StartMeeting call.
    void (async () => {
      if (!localStorage.getItem(MEETING_PROBE_LOCALSTORAGE_KEY)) {
        localStorage.setItem(MEETING_PROBE_LOCALSTORAGE_KEY, '1')
        try {
          await callProbeMeetingPermission()
        } catch {
          // The probe already emitted meeting:permission_error via the
          // Go side; the warning row below will surface on the next
          // render. Don't proceed to StartMeeting — the user must grant
          // permission first.
          return
        }
      }
      const title = `Manual recording — ${new Date().toLocaleString()}`
      try {
        await callStartMeeting(title)
        // Clear any lingering permission warning — a successful start
        // means a prior denial CTA would now confuse the UI.
        if (mountedRef.current) setMeetingError(null)
      } catch (err) {
        // Bridge wrappers already log; permission denial surfaces via
        // meeting:permission_error.
        console.warn('HudCalendarPanel: StartMeeting failed', err)
      }
    })()
  }, [meetingActive])

  const handleReplay = useCallback((): void => {
    // Fire-and-forget: the daemon emits the recap audio via the standard
    // RouterTTS pipeline, so success surfaces as a speaking-state event,
    // not as a return value here. We swallow errors with a warn — the
    // chip's primary START/STOP affordance must not regress just because
    // a replay request couldn't be sent.
    void callTriggerMeetingRecap().catch((err) => {
      console.warn('HudCalendarPanel: TriggerMeetingRecap failed', err)
    })
  }, [])

  // Chip palette — cyan in idle, red in active. Letter-spacing + font-size
  // mirror the panel's existing typography (see headline + upcoming rows).
  const accent = meetingActive ? '#ff4444' : 'var(--accent-blue, #00e5ff)'
  const chipStyle = {
    marginTop: 8,
    padding: '6px 10px',
    background: meetingActive
      ? 'rgba(255,68,68,0.12)'
      : 'rgba(0,229,255,0.10)',
    border: `1px solid ${accent}`,
    borderRadius: 3,
    color: accent,
    fontSize: 10,
    fontWeight: 700,
    letterSpacing: '0.18em',
    fontFamily: "'SF Mono', 'Menlo', monospace",
    cursor: 'pointer',
    width: '100%',
    textAlign: 'center' as const,
    transition: 'background 120ms ease-out, border-color 120ms ease-out',
    boxShadow: meetingActive
      ? '0 0 10px rgba(255,68,68,0.35), inset 0 0 6px rgba(255,68,68,0.18)'
      : '0 0 8px rgba(0,229,255,0.18), inset 0 0 6px rgba(0,229,255,0.08)',
  }

  // Replay button shares the chip's vertical metrics (same border/font) but
  // is constrained to ~28px wide so the START/STOP chip retains visual
  // primacy. Cyan-tinted in both meetingActive states because the daemon's
  // cached recap is meaningful regardless of whether a new meeting is now
  // in progress (hasRecapAvailable gates render, see below).
  const replayChipStyle = {
    marginTop: 8,
    padding: '6px 0',
    width: 28,
    flex: '0 0 auto',
    background: 'rgba(0,229,255,0.10)',
    border: '1px solid var(--accent-blue, #00e5ff)',
    borderRadius: 3,
    color: 'var(--accent-blue, #00e5ff)',
    fontSize: 12,
    fontWeight: 700,
    lineHeight: 1,
    fontFamily: "'SF Mono', 'Menlo', monospace",
    cursor: 'pointer',
    textAlign: 'center' as const,
    transition: 'background 120ms ease-out, border-color 120ms ease-out',
    boxShadow:
      '0 0 8px rgba(0,229,255,0.18), inset 0 0 6px rgba(0,229,255,0.08)',
  }
  // Main chip flexes to fill the remaining width so the row hugs the
  // panel edge. Reuses chipStyle so START/STOP visuals are unchanged.
  const mainChipStyle = { ...chipStyle, flex: '1 1 auto', width: 'auto' }

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'stretch', gap: 6 }}>
        <button
          type="button"
          onClick={handleClick}
          aria-label={meetingActive ? 'Stop meeting recording' : 'Start meeting recording'}
          aria-pressed={meetingActive}
          style={mainChipStyle}
          className={meetingActive ? 'animate-pulse' : undefined}
        >
          {meetingActive ? '■ STOP MEETING' : '● START MEETING'}
        </button>
        {hasRecapAvailable && (
          <button
            type="button"
            onClick={handleReplay}
            aria-label="Replay last spoken recap"
            title="Replay last spoken recap"
            data-testid="meeting-replay-button"
            style={replayChipStyle}
          >
            ⟳
          </button>
        )}
      </div>
      {lastNotesPath && (
        <p
          role="status"
          aria-live="polite"
          style={{
            marginTop: 4,
            color: 'rgba(0,255,150,0.85)',
            fontSize: 9,
            letterSpacing: '0.08em',
            fontFamily: "'SF Mono', 'Menlo', monospace",
            textAlign: 'center',
          }}
        >
          ✓ Notes saved: {lastNotesPath.split('/').pop()}
        </p>
      )}
      {meetingError && (
        <button
          type="button"
          role="alert"
          aria-label="Open System Settings to grant Screen Recording permission"
          onClick={() => BrowserOpenURL(SYSTEM_SETTINGS_SCREEN_RECORDING_URL)}
          style={{
            marginTop: 4,
            width: '100%',
            background: 'transparent',
            border: 'none',
            borderBottom: '1px dashed rgba(255,68,68,0.4)',
            color: 'rgba(255,68,68,0.9)',
            fontSize: 9,
            letterSpacing: '0.08em',
            fontFamily: "'SF Mono', 'Menlo', monospace",
            textAlign: 'center',
            cursor: 'pointer',
            padding: '2px 0',
          }}
        >
          ⚠ {meetingError} · tap to open System Settings
        </button>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function HudCalendarPanel({ pollIntervalMs = DEFAULT_POLL_MS }: HudCalendarPanelProps): React.ReactElement {
  const [connected, setConnected] = useState<boolean | null>(null) // null = unknown
  const [nextEvent, setNextEvent] = useState<NextEventSnapshot | null>(null)
  const [upcoming, setUpcoming] = useState<CalendarEvent[]>([])

  useEffect(() => {
    let cancelled = false

    const refresh = async (): Promise<void> => {
      const isOn = await callIsConnected()
      if (cancelled) return
      setConnected(isOn)
      if (!isOn) {
        setNextEvent(null)
        setUpcoming([])
        return
      }
      const [next, list] = await Promise.all([
        callGetNext(),
        callGetUpcoming(UPCOMING_LIMIT),
      ])
      if (cancelled) return
      setNextEvent(next)
      // The next-event headline + the upcoming list overlap on the very
      // next entry. Drop the first upcoming if it matches so the panel
      // doesn't render "Sprint review in 14m" headline + "Sprint review"
      // row right under it.
      const first = list[0]
      if (next?.title && first && first.title === next.title) {
        setUpcoming(list.slice(1))
      } else {
        setUpcoming(list)
      }
    }

    void refresh()
    const id = setInterval(() => void refresh(), pollIntervalMs)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [pollIntervalMs])

  // ---- Disconnected state ----
  // The brief calls for the chip to render ABOVE the "Connect Google
  // Calendar" CTA here — meeting mode works without calendar; only the
  // auto-suggest banner depends on it.
  if (connected === false) {
    return (
      <div className="px-3 py-3" style={{ color: 'var(--hud-cyan-dim)', fontSize: 11, letterSpacing: '0.08em' }}>
        <p style={{ marginBottom: 6, fontWeight: 700, color: 'var(--hud-cyan)' }}>
          GOOGLE CALENDAR
        </p>
        <MeetingChip />
        <p style={{ opacity: 0.7, marginTop: 8 }}>
          Connect via Settings → Connections to see your upcoming events here.
        </p>
      </div>
    )
  }

  // ---- Loading state ----
  if (connected === null) {
    return (
      <div className="px-3 py-3" style={{ color: 'var(--hud-cyan-dim)', fontSize: 10, letterSpacing: '0.18em' }}>
        Loading calendar…
      </div>
    )
  }

  // ---- Connected, no events ----
  if (!nextEvent && upcoming.length === 0) {
    return (
      <div className="px-3 py-3" style={{ color: 'var(--hud-cyan-dim)', fontSize: 11 }}>
        <p style={{ marginBottom: 4, fontWeight: 700, color: 'var(--hud-cyan)', letterSpacing: '0.18em' }}>
          NEXT
        </p>
        <p style={{ opacity: 0.6 }}>No upcoming events.</p>
        <MeetingChip />
      </div>
    )
  }

  // ---- Happy path: headline + list ----
  return (
    <div className="px-3 py-3" style={{ color: 'var(--hud-cyan)', fontSize: 11 }}>
      {nextEvent && (
        <div style={{ marginBottom: 10 }} data-testid="hud-calendar-next">
          <p
            style={{
              fontSize: 9,
              letterSpacing: '0.24em',
              color: 'var(--hud-cyan-dim)',
              marginBottom: 2,
            }}
          >
            NEXT {nextEvent.relativeTime ? `· ${nextEvent.relativeTime.toUpperCase()}` : ''}
          </p>
          <p
            style={{
              fontSize: 14,
              fontWeight: 700,
              lineHeight: 1.25,
              textShadow: '0 0 8px rgba(0,229,255,0.45)',
            }}
          >
            {nextEvent.title || 'Untitled event'}
          </p>
          {nextEvent.start && (
            <p style={{ fontSize: 10, opacity: 0.7, marginTop: 2 }}>
              {formatDay(nextEvent.start)} · {formatTime(nextEvent.start)}
              {nextEvent.location ? ` · ${nextEvent.location}` : ''}
            </p>
          )}
        </div>
      )}

      {upcoming.length > 0 && (
        <ul style={{ listStyle: 'none', padding: 0, margin: 0 }} aria-label="Upcoming events">
          <AnimatePresence initial={false}>
            {upcoming.map((evt) => (
              <motion.li
                key={evt.id ?? `${evt.title}-${evt.start}`}
                initial={{ opacity: 0, y: 4 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0 }}
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  gap: 8,
                  padding: '4px 0',
                  borderTop: '1px solid rgba(0,229,255,0.12)',
                  fontSize: 10,
                }}
              >
                <span
                  style={{
                    flex: '1 1 auto',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    opacity: 0.85,
                  }}
                  title={evt.title}
                >
                  {evt.title || 'Untitled'}
                </span>
                <span
                  style={{
                    flex: '0 0 auto',
                    opacity: 0.6,
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {formatDay(evt.start)} {formatTime(evt.start)}
                </span>
              </motion.li>
            ))}
          </AnimatePresence>
        </ul>
      )}

      {/* Always-visible record control. Sits below the headline + upcoming
          list so the user can manually start a meeting recording at any
          time without leaving the main HUD. */}
      <MeetingChip />
    </div>
  )
}
