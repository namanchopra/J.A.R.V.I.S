// ---------------------------------------------------------------------------
// OverlayView -- 320x420 sci-fi panel that renders inside the morphed Wails
// window when overlay mode is active.
//
// Iterated design (v0.3.0):
//   - v1 was 180x180 with a tiny SVG orb + close button. UX feedback: too
//     small, no character, no in-overlay PTT/mute controls.
//   - v2 (this file) consumes the full JarvisOrb (Three.js) for visual
//     parity with the main HUD, sized to 320x420 to fit orb + state line
//     + control row. Mac chrome is stripped by app_overlay.go via the
//     internal/macctl CGO bridge so the window is truly borderless on Mac.
//
// Controls:
//   - Click+hold PTT button (large, center) sends ptt_active/ptt_release
//     to the daemon via OverlayPTTPress/OverlayPTTRelease Wails bindings.
//   - Mute toggle (small, left) sends __mute__ / __unmute__ HUD commands
//     via SendJarvisCommand.
//   - Interrupt button (small, right) sends __interrupt__ HUD command to
//     cancel in-flight TTS.
//   - Escape (window keydown) dismisses the overlay via OverlayHide.
//
// Drag: the wrapper is marked --wails-draggable: drag so the user can move
// the borderless window by dragging anywhere outside the interactive
// controls. Each clickable element opts out via --wails-draggable: no-drag.
//
// Wails binding access pattern:
//   Bindings are reached via window.go.main.App at call time -- a missing
//   binding (e.g. stale dev build) is logged and swallowed so the React
//   tree never crashes on a click handler.
// ---------------------------------------------------------------------------

import { useCallback, useEffect, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import { JarvisOrb } from '../components/JarvisOrb'
import { getJarvisState } from '../lib/jarvis-api'
import type { JarvisState } from '../lib/jarvis-api'
import { EventsOn, BrowserOpenURL } from '../../wailsjs/runtime/runtime'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const ACCENT = 'var(--accent-blue, #00e5ff)'
const FRAME_BG = 'rgba(2, 12, 10, 0.85)' // matches JarvisHudView palette at small scale
const STATE_POLL_MS = 500 // same cadence as JarvisHudView's DEX_STATE_POLL_MS

// ---------------------------------------------------------------------------
// Wails runtime bridge wrappers
// ---------------------------------------------------------------------------

/**
 * Call OverlayHide via the runtime bridge. The generated wrapper isn't
 * available yet (see header comment), so we look up the binding on
 * window.go.main.App at call time. A missing binding is logged and
 * swallowed so a hot-reloaded dev session can't crash on the close button.
 */
async function callOverlayHide(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.OverlayHide as
      | (() => Promise<void>)
      | undefined
    if (fn) {
      await fn()
      return
    }
    console.warn('OverlayView: window.go.main.App.OverlayHide is not available')
  } catch (err) {
    // Don't propagate -- the user pressed the close button and we should
    // never throw out of an onClick handler. Worst case the overlay stays
    // visible and the user retries.
    console.warn('OverlayView: OverlayHide rejected', err)
  }
}

/**
 * Read the OverlayShowTranscript config field via the GetConfig runtime
 * binding. Returns false on any failure (binding missing, config malformed)
 * so the transcript chip stays hidden by default -- matches the TASK-001
 * default value and the brief's "leave chip as a future enhancement" note.
 */
async function getOverlayShowTranscript(): Promise<boolean> {
  try {
    const fn = window?.go?.main?.App?.GetConfig as
      | (() => Promise<Record<string, unknown>>)
      | undefined
    if (!fn) return false
    const cfg = await fn()
    const val = cfg?.overlayShowTranscript
    return val === true
  } catch {
    // Binding not available or threw -- safe default is "hide chip".
    return false
  }
}

/**
 * Push-to-talk press. Calls OverlayPTTPress on the Go side which (a)
 * ensures the overlay is shown + focused and (b) sends a ptt_active
 * control frame to the daemon over the existing WS.
 */
async function callOverlayPTTPress(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.OverlayPTTPress as
      | (() => Promise<void>)
      | undefined
    if (fn) await fn()
    else console.warn('OverlayView: OverlayPTTPress binding unavailable')
  } catch (err) {
    console.warn('OverlayView: OverlayPTTPress rejected', err)
  }
}

/** Push-to-talk release; sends ptt_release to the daemon. */
async function callOverlayPTTRelease(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.OverlayPTTRelease as
      | (() => Promise<void>)
      | undefined
    if (fn) await fn()
    else console.warn('OverlayView: OverlayPTTRelease binding unavailable')
  } catch (err) {
    console.warn('OverlayView: OverlayPTTRelease rejected', err)
  }
}

/**
 * Send a HUD command string to the daemon via SendJarvisCommand. Used for
 * __mute__ / __unmute__ / __interrupt__. The daemon's main dispatcher in
 * scripts/jarvis-daemon/main.py handles these inline.
 */
async function callSendJarvisCommand(cmd: string): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.SendJarvisCommand as
      | ((c: string) => Promise<void>)
      | undefined
    if (fn) await fn(cmd)
    else console.warn(`OverlayView: SendJarvisCommand('${cmd}') unavailable`)
  } catch (err) {
    console.warn(`OverlayView: SendJarvisCommand('${cmd}') rejected`, err)
  }
}

/**
 * Begin a meeting-mode recording. Calls App.StartMeeting on the Go side,
 * which (a) starts ScreenCaptureKit system-audio capture and (b) sends
 * __meeting_start__ to the daemon. Re-throws so the click handler can react
 * to a failure (e.g. don't flip local state when the call rejects);
 * permission errors arrive separately via the "meeting:permission_error"
 * Wails event handled in the effect below.
 */
async function callStartMeeting(title: string): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.StartMeeting as
      | ((t: string) => Promise<void>)
      | undefined
    if (fn) await fn(title)
    else console.warn('OverlayView: StartMeeting binding unavailable')
  } catch (err) {
    console.warn('OverlayView: StartMeeting rejected', err)
    throw err
  }
}

/**
 * Stop a meeting-mode recording. Resolves to the path of the markdown notes
 * file the daemon wrote. Empty string on any failure (binding missing,
 * daemon rejected, etc.) so the caller can branch without try/catch.
 */
async function callStopMeeting(): Promise<string> {
  try {
    const fn = window?.go?.main?.App?.StopMeeting as
      | (() => Promise<string>)
      | undefined
    if (!fn) {
      console.warn('OverlayView: StopMeeting binding unavailable')
      return ''
    }
    return await fn()
  } catch (err) {
    console.warn('OverlayView: StopMeeting rejected', err)
    return ''
  }
}

/**
 * Read the Go-side meeting active flag. Used for the initial sync on mount
 * so a hot-reload doesn't desync the overlay's local view of meeting state.
 * Failure-safe: returns false if the binding is missing or throws.
 */
async function callIsMeetingActive(): Promise<boolean> {
  try {
    const fn = window?.go?.main?.App?.IsMeetingActive as
      | (() => Promise<boolean>)
      | undefined
    if (!fn) return false
    return await fn()
  } catch {
    return false
  }
}

/**
 * Trigger the macOS Screen Recording permission dialog by attempting a
 * minimal SCK Start+Stop cycle on the Go side (TASK-015). The overlay
 * calls this exactly once per user-profile -- gated by the
 * MEETING_PROBE_LOCALSTORAGE_KEY flag below -- so a fresh install pops
 * the system prompt the first time the user clicks the record-meeting
 * icon, rather than waiting until the user actually expects audio
 * capture to be running.
 *
 * Re-throws on rejection so the caller can early-return from
 * handleToggleMeeting and skip the subsequent StartMeeting call when
 * the probe was denied. Permission-error UI feedback arrives via the
 * "meeting:permission_error" event the Go side emits internally.
 */
async function callProbeMeetingPermission(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.ProbeMeetingPermission as
      | (() => Promise<void>)
      | undefined
    if (fn) await fn()
    else console.warn('OverlayView: ProbeMeetingPermission binding unavailable')
  } catch (err) {
    console.warn('OverlayView: ProbeMeetingPermission rejected', err)
    throw err
  }
}

// macOS Screen Recording deep-link, mirrors the constant in
// frontend/src/views/settings/MeetingPanel.tsx. Duplicated rather than
// shared because the overlay is loaded as its own bundle and we want to
// avoid a cross-view import for a 1-line constant.
const SYSTEM_SETTINGS_SCREEN_RECORDING_URL =
  'x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenRecording'

// localStorage flag for the first-launch probe. Stored as the literal
// string "1" when set; the only thing that matters is presence/absence.
// Cleared by the user wiping browser storage / dev tooling resets, which
// is the right semantics -- if state was wiped, re-probing is harmless.
const MEETING_PROBE_LOCALSTORAGE_KEY = 'jarvis:meetingPermissionProbed'

// ---------------------------------------------------------------------------
// State mapping helpers
// ---------------------------------------------------------------------------

/**
 * Derive a synthetic audioLevel from the current state. JarvisOrb's
 * shaders already react to per-frame audio_level events on the WS bus
 * (see JarvisOrb.tsx lines 12, 45), so this only matters when no event
 * is in flight. Mirrors the main HUD's heuristic for visual parity.
 */
function deriveAudioLevel(s: JarvisState): number {
  if (s === 'speaking') return 0.7
  if (s === 'listening') return 0.4
  return 0
}

/**
 * Sci-fi state label shown under the orb. Drives the overlay's personality
 * by mirroring what the daemon thinks Jarvis is doing right now.
 */
function stateLabel(s: JarvisState, muted: boolean): string {
  if (muted) return 'MUTED'
  switch (s) {
    case 'listening':
      return 'LISTENING'
    case 'thinking':
      return 'THINKING'
    case 'speaking':
      return 'SPEAKING'
    default:
      return 'STANDING BY'
  }
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

// Wails draggable-region opt-out marker. Set on every interactive element
// so the overlay's window-drag region doesn't swallow clicks.
const NO_DRAG: CSSProperties = {
  // The Wails v2 webview reads --wails-draggable on a per-element basis.
  // 'no-drag' carves out the element from the parent's drag region.
  ['WebkitAppRegion' as never]: 'no-drag' as never,
}
const DRAG_REGION: CSSProperties = {
  ['WebkitAppRegion' as never]: 'drag' as never,
}

interface PTTButtonProps {
  onPress: () => void
  onRelease: () => void
  pressed: boolean
}

function PTTButton({ onPress, onRelease, pressed }: PTTButtonProps): React.ReactElement {
  // Click+hold microphone button. Press starts a turn (ptt_active), release
  // ends it (ptt_release). Mouse-leave is treated as release so a drag-off
  // doesn't leave the gate stuck open.
  const style: CSSProperties = {
    ...NO_DRAG,
    width: 64,
    height: 64,
    borderRadius: '50%',
    border: `1.5px solid ${ACCENT}`,
    background: pressed
      ? 'radial-gradient(circle at center, rgba(0,229,255,0.45), rgba(0,229,255,0.1))'
      : 'radial-gradient(circle at center, rgba(0,229,255,0.18), rgba(0,12,10,0.6))',
    color: ACCENT,
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontFamily: "'SF Mono', 'Menlo', monospace",
    fontSize: 11,
    fontWeight: 700,
    letterSpacing: '0.16em',
    padding: 0,
    boxShadow: pressed
      ? '0 0 24px rgba(0,229,255,0.55), inset 0 0 14px rgba(0,229,255,0.35)'
      : '0 0 12px rgba(0,229,255,0.18), inset 0 0 10px rgba(0,229,255,0.1)',
    transition: 'box-shadow 100ms ease-out, background 100ms ease-out',
    userSelect: 'none',
  }
  return (
    <button
      type="button"
      aria-label="Push to talk"
      title="Hold to talk"
      onMouseDown={onPress}
      onMouseUp={onRelease}
      onMouseLeave={onRelease}
      style={style}
    >
      {/* Centred mic glyph. Unicode U+1F399 (studio mic) renders too soft;
          using a simple custom glyph keeps the sci-fi feel. */}
      <span aria-hidden="true" style={{ fontSize: 22, lineHeight: 1 }}>
        ◉
      </span>
    </button>
  )
}

interface IconControlProps {
  label: string
  glyph: string
  active?: boolean
  onClick: () => void
}

function IconControl({ label, glyph, active, onClick }: IconControlProps): React.ReactElement {
  const style: CSSProperties = {
    ...NO_DRAG,
    width: 36,
    height: 36,
    borderRadius: 6,
    border: `1px solid ${active ? ACCENT : 'rgba(0,229,255,0.3)'}`,
    background: active ? 'rgba(0,229,255,0.22)' : 'rgba(0,12,10,0.6)',
    color: ACCENT,
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontFamily: "'SF Mono', 'Menlo', monospace",
    fontSize: 14,
    padding: 0,
    transition: 'background 120ms ease-out, border-color 120ms ease-out',
  }
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      style={style}
    >
      <span aria-hidden="true">{glyph}</span>
    </button>
  )
}

// ---------------------------------------------------------------------------
// OverlayView
// ---------------------------------------------------------------------------

export function OverlayView(): React.ReactElement {
  // Track the current Jarvis state. Initial value 'idle' matches the
  // disconnected-daemon failure case from the acceptance criteria: when the
  // WS is down, getJarvisState() resolves to 'idle' (see jarvis-api.ts) and
  // the orb renders correctly without crashing.
  const [jarvisState, setJarvisState] = useState<JarvisState>('idle')
  const [showTranscript, setShowTranscript] = useState<boolean>(false)
  const mountedRef = useRef(true)

  // Mount/unmount guard so setState doesn't fire after the view unmounts
  // (e.g. when App.tsx flips back to HUD mode while a poll is in flight).
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  // -- Poll jarvis state at 500ms cadence (matches JarvisHudView pattern).
  // The hook isn't a stream because jarvis-api.ts exports a one-shot async
  // function; if/when a streaming hook lands the brief explicitly says to
  // adopt it without inventing a parallel hook here.
  useEffect(() => {
    let cancelled = false
    const poll = async (): Promise<void> => {
      const s = await getJarvisState()
      if (!cancelled && mountedRef.current) {
        setJarvisState(s)
      }
    }
    void poll()
    const id = setInterval(() => void poll(), STATE_POLL_MS)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [])

  // -- Also subscribe to the 'jarvis' event channel so state changes from
  // the daemon (state_change / state events) flip the orb faster than the
  // 500ms poll could on its own. Mirrors the dual poll+event approach in
  // JarvisHudView.tsx so the overlay feels equally snappy.
  useEffect(() => {
    const cancel = EventsOn('jarvis', (event: unknown) => {
      const e = event as { type?: string; state?: string; text?: string } | null
      if (!e || (e.type !== 'state_change' && e.type !== 'state')) return
      const s = e.state ?? e.text
      if (s === 'listening' || s === 'thinking' || s === 'speaking') {
        if (mountedRef.current) setJarvisState(s as JarvisState)
      } else if (s === 'idle' || s === 'running') {
        if (mountedRef.current) setJarvisState('idle')
      }
    })
    return () => {
      cancel()
    }
  }, [])

  // -- Load OverlayShowTranscript from config on mount. We don't currently
  // subscribe to a config-change event because the Settings panel (TASK-009)
  // will reload the overlay via OverlayHide/OverlayShow when the toggle is
  // changed, which remounts this view. If a hot-reload path is added later
  // an EventsOn('config:changed', ...) subscription would slot in here.
  useEffect(() => {
    let cancelled = false
    void getOverlayShowTranscript().then((v) => {
      if (!cancelled && mountedRef.current) setShowTranscript(v)
    })
    return () => {
      cancelled = true
    }
  }, [])

  // -- In-overlay control state.
  // pttPressed drives the PTT button's visual feedback locally; the
  // truth-of-record for the daemon's STT gate lives on the Go/daemon side.
  // 'muted' mirrors the daemon's wake-gate state; we maintain it locally
  // (no query API on the daemon) and trust the toggle to stay in sync.
  const [pttPressed, setPttPressed] = useState(false)
  const [muted, setMuted] = useState(false)

  // -- Meeting-mode state (TASK-010).
  //   * meetingActive — true while a meeting recording is in flight. Synced
  //     with the Go-side flag on mount and updated reactively via the
  //     "meeting:state" Wails event the daemon/Go side emits on transitions.
  //   * meetingError — populated when a "meeting:permission_error" event
  //     arrives (TASK-015 also surfaces this in Settings). Shown as a small
  //     red row under the state label so the user sees it without having to
  //     open Settings.
  //   * lastMeetingNotesPath — markdown path returned by StopMeeting; shown
  //     as a transient toast under the state label and auto-cleared after
  //     ~6 seconds so it doesn't linger in the overlay.
  const [meetingActive, setMeetingActive] = useState(false)
  const [meetingError, setMeetingError] = useState<string | null>(null)
  const [lastMeetingNotesPath, setLastMeetingNotesPath] = useState<string | null>(null)

  // -- Initial sync + Wails event subscriptions for meeting state.
  // Mount-time IsMeetingActive() handles the case where the user opened the
  // overlay while a meeting was already in progress (e.g. started from the
  // HUD banner). The two EventsOn handlers keep the local view in sync with
  // the Go/daemon side for the rest of the overlay's lifetime.
  useEffect(() => {
    let cancelled = false
    void callIsMeetingActive().then((active) => {
      if (!cancelled && mountedRef.current) setMeetingActive(active)
    })
    const cancelState = EventsOn('meeting:state', (payload: unknown) => {
      if (payload === 'active') setMeetingActive(true)
      else if (payload === 'idle') setMeetingActive(false)
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

  // -- Keyboard shortcuts while the overlay window has focus.
  //   * Escape   — dismiss the overlay (sole no-button dismissal affordance)
  //   * Space    — hold-to-talk (PTT). Equivalent to holding the big mic
  //                button without having to mouse over it. Ignored when
  //                muted because the daemon's gate is closed anyway.
  //                Ignored on auto-repeat so a long hold only fires
  //                ptt_active once.
  //
  // The pttPressedRef shadows the React state for the up-edge check.
  // Reading state in a closure that was set up at mount would always see
  // the initial false; the ref carries the live value.
  const pttPressedRef = useRef(false)
  useEffect(() => {
    pttPressedRef.current = pttPressed
  }, [pttPressed])

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.preventDefault()
        void callOverlayHide()
        return
      }
      if (e.code === 'Space' || e.key === ' ') {
        // Don't double-fire on macOS auto-repeat.
        if (e.repeat || pttPressedRef.current) {
          e.preventDefault()
          return
        }
        if (muted) return // daemon gate is closed; skip
        e.preventDefault()
        setPttPressed(true)
        pttPressedRef.current = true
        void callOverlayPTTPress()
      }
    }
    const onKeyUp = (e: KeyboardEvent): void => {
      if (e.code === 'Space' || e.key === ' ') {
        if (!pttPressedRef.current) return
        e.preventDefault()
        setPttPressed(false)
        pttPressedRef.current = false
        void callOverlayPTTRelease()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    window.addEventListener('keyup', onKeyUp)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      window.removeEventListener('keyup', onKeyUp)
    }
  }, [muted])

  const handlePTTPress = useCallback((): void => {
    if (muted) return // PTT while muted would race with __unmute__; ignore
    setPttPressed(true)
    void callOverlayPTTPress()
  }, [muted])

  const handlePTTRelease = useCallback((): void => {
    if (!pttPressed) return // ignore mouse-leave when we never started
    setPttPressed(false)
    void callOverlayPTTRelease()
  }, [pttPressed])

  const handleToggleMute = useCallback((): void => {
    setMuted((prev) => {
      const next = !prev
      void callSendJarvisCommand(next ? '__mute__' : '__unmute__')
      return next
    })
  }, [])

  const handleInterrupt = useCallback((): void => {
    void callSendJarvisCommand('__interrupt__')
  }, [])

  // Restore the main HUD. Same effect as pressing Escape, but exposed as
  // a click affordance for users who don't notice (or don't want to rely
  // on) the keyboard shortcut. Also redundant with re-pressing the global
  // hotkey -- three paths is intentional given the chrome-free design.
  const handleBackToMain = useCallback((): void => {
    void callOverlayHide()
  }, [])

  // Toggle meeting-mode recording. While idle, starts a manual recording
  // tagged with the current local timestamp; while active, stops it and
  // surfaces the resulting markdown path as a fading toast under the state
  // label. Permission-denial feedback comes through the
  // "meeting:permission_error" event (handled in the effect above); the
  // catch here just guarantees a failed StartMeeting can't bubble out of
  // the onClick handler.
  const handleToggleMeeting = useCallback((): void => {
    if (meetingActive) {
      void (async () => {
        const path = await callStopMeeting()
        if (path) {
          setLastMeetingNotesPath(path)
          // Auto-clear the toast so it doesn't linger in the overlay.
          setTimeout(() => {
            if (mountedRef.current) setLastMeetingNotesPath(null)
          }, 6000)
        }
      })()
    } else {
      // TASK-015: first-launch probe to surface the macOS Screen Recording
      // permission dialog BEFORE we attempt the real StartMeeting. The flag
      // gates the probe to a single invocation per user-profile so we don't
      // pester users who have already granted (or denied) permission.
      void (async () => {
        if (!localStorage.getItem(MEETING_PROBE_LOCALSTORAGE_KEY)) {
          localStorage.setItem(MEETING_PROBE_LOCALSTORAGE_KEY, '1')
          try {
            await callProbeMeetingPermission()
            // Fall through to the normal StartMeeting path on success.
          } catch {
            // The probe already emitted meeting:permission_error via the
            // Go side; the warning row is now showing. Don't proceed to
            // StartMeeting -- the user must grant permission first.
            return
          }
        }
        const title = `Manual recording — ${new Date().toLocaleString()}`
        void callStartMeeting(title).then(() => {
          // Clear any lingering permission warning -- the user has now
          // successfully started a meeting, so the prior denial CTA is
          // stale and would confuse the UI.
          setMeetingError(null)
        }).catch(() => {
          // Errors are already logged by the bridge wrapper. UI feedback
          // for a permission denial arrives via meeting:permission_error.
        })
      })()
    }
  }, [meetingActive])

  // Derived values for the orb. JarvisOrb consumes JarvisState directly
  // (4-state space) so no collapse helper is needed. When a meeting is in
  // progress the state label is overridden to RECORDING MEETING regardless
  // of what the daemon thinks (listening / thinking / etc.); the user's
  // primary signal in this mode is that a recording is running.
  const audioLevel = deriveAudioLevel(jarvisState)
  const label = meetingActive ? 'RECORDING MEETING' : stateLabel(jarvisState, muted)
  const hint = meetingActive
    ? 'HOLD ␣ OR THE MIC TO TALK · REC · ESC TO CLOSE'
    : 'HOLD ␣ OR THE MIC TO TALK · ESC TO CLOSE'

  // Outer-wrapper style: fills the 320x420 Wails window with a sci-fi cyan
  // frame and a subtle outer glow. The whole panel is a Wails drag region
  // so the user can move the borderless window from anywhere outside the
  // interactive controls (each control opts out via NO_DRAG).
  const wrapperStyle: CSSProperties = {
    ...DRAG_REGION,
    width: '100vw',
    height: '100vh',
    position: 'relative',
    overflow: 'hidden',
    background: FRAME_BG,
    border: `1px solid ${ACCENT}`,
    boxShadow: '0 0 12px rgba(0,229,255,0.25), inset 0 0 16px rgba(0,229,255,0.08)',
    borderRadius: 6,
    boxSizing: 'border-box',
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'stretch',
  }

  const orbContainerStyle: CSSProperties = {
    flex: '1 1 auto',
    minHeight: 0,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    paddingTop: 8,
  }

  const stateRowStyle: CSSProperties = {
    flex: '0 0 auto',
    textAlign: 'center',
    color: ACCENT,
    fontFamily: "'SF Mono', 'Menlo', monospace",
    fontSize: 11,
    letterSpacing: '0.32em',
    fontWeight: 700,
    textShadow: '0 0 8px rgba(0,229,255,0.55)',
    padding: '4px 0 8px',
    userSelect: 'none',
  }

  const controlsRowStyle: CSSProperties = {
    flex: '0 0 auto',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-around',
    padding: '4px 18px 16px',
  }

  const transcriptRowStyle: CSSProperties = {
    flex: '0 0 auto',
    textAlign: 'center',
    color: 'rgba(0,229,255,0.7)',
    fontFamily: "'SF Mono', 'Menlo', monospace",
    fontSize: 10,
    letterSpacing: '0.08em',
    padding: '0 14px 6px',
    minHeight: 16,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  }

  return (
    <div
      role="region"
      aria-label="Jarvis overlay"
      data-overlay-state={jarvisState}
      style={wrapperStyle}
    >
      <div style={orbContainerStyle}>
        {/*
          Recording-state visual: a wrapper around the orb mount that draws
          a circular red ring + soft glow when a meeting is being recorded.
          The borderRadius:50% frames the orb circle rather than the square
          container. Outside meeting mode this is a transparent passthrough.
        */}
        <div
          aria-hidden="true"
          style={{
            position: 'relative',
            width: '100%',
            height: '100%',
            ...(meetingActive
              ? {
                  boxShadow:
                    'inset 0 0 0 1.5px #ff4444, 0 0 14px rgba(255,68,68,0.55)',
                  borderRadius: '50%',
                }
              : {}),
          }}
        >
          <JarvisOrb
            state={jarvisState}
            audioLevel={audioLevel}
            className="w-full h-full"
          />
        </div>
      </div>
      <div role="status" aria-live="polite" style={stateRowStyle}>
        {label}
      </div>
      {lastMeetingNotesPath && (
        <div
          role="status"
          aria-live="polite"
          style={{
            flex: '0 0 auto',
            textAlign: 'center',
            color: 'rgba(0,255,150,0.85)',
            fontSize: 9,
            letterSpacing: '0.08em',
            fontFamily: "'SF Mono', 'Menlo', monospace",
            padding: '0 14px 4px',
          }}
        >
          ✓ Notes saved: {lastMeetingNotesPath.split('/').pop()}
        </div>
      )}
      {meetingError && (
        // TASK-015: the warning row is now a clickable button that opens
        // System Settings → Privacy → Screen Recording, mirroring the
        // deep-link CTA in MeetingPanel.tsx. role="alert" is preserved so
        // assistive tech still announces the permission denial on render.
        // NO_DRAG is required because the parent panel is a Wails drag
        // region; without it the click would be swallowed by the drag.
        <button
          type="button"
          role="alert"
          aria-label="Open System Settings to grant Screen Recording permission"
          onClick={() => BrowserOpenURL(SYSTEM_SETTINGS_SCREEN_RECORDING_URL)}
          style={{
            ...NO_DRAG,
            flex: '0 0 auto',
            textAlign: 'center',
            color: 'rgba(255,68,68,0.9)',
            fontSize: 9,
            letterSpacing: '0.08em',
            fontFamily: "'SF Mono', 'Menlo', monospace",
            padding: '2px 14px 4px',
            background: 'transparent',
            border: 'none',
            borderBottom: '1px dashed rgba(255,68,68,0.4)',
            cursor: 'pointer',
            width: '100%',
          }}
        >
          ⚠ {meetingError} · tap to open System Settings
        </button>
      )}
      <div
        aria-hidden="true"
        style={{
          flex: '0 0 auto',
          textAlign: 'center',
          color: 'rgba(0,229,255,0.45)',
          fontFamily: "'SF Mono', 'Menlo', monospace",
          fontSize: 9,
          letterSpacing: '0.18em',
          padding: '0 0 6px',
          userSelect: 'none',
        }}
      >
        {hint}
      </div>
      {showTranscript && (
        <div style={transcriptRowStyle} aria-label="Transcript">
          {/* Stub: live transcript stream not yet exposed by the daemon. */}
          ...
        </div>
      )}
      <div style={controlsRowStyle}>
        <IconControl
          label={muted ? 'Unmute microphone' : 'Mute microphone'}
          glyph={muted ? '⊘' : '◐'}
          active={muted}
          onClick={handleToggleMute}
        />
        <PTTButton
          onPress={handlePTTPress}
          onRelease={handlePTTRelease}
          pressed={pttPressed}
        />
        <IconControl
          label="Interrupt Jarvis"
          glyph="■"
          onClick={handleInterrupt}
        />
        <IconControl
          label={meetingActive ? 'Stop meeting recording' : 'Start meeting recording'}
          glyph="●"
          active={meetingActive}
          onClick={handleToggleMeeting}
        />
        <IconControl
          label="Back to main HUD"
          glyph="⤢"
          onClick={handleBackToMain}
        />
      </div>
    </div>
  )
}
