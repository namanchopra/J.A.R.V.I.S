import { useEffect, useRef, useState, useCallback } from 'react'
import { EventsOn } from '../../../wailsjs/runtime/runtime'
import '../../lib/hud-theme'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type JarvisState = 'idle' | 'listening' | 'thinking' | 'speaking'

interface JarvisEvent {
  type?: string
  text?: string
  role?: string
  level?: number
  partial?: boolean
}

interface HudVoiceBarProps {
  jarvisState: JarvisState
}

/** Which content is being displayed in the subtitle area. */
type DisplayMode = 'idle' | 'transcript' | 'jarvis'

// ---------------------------------------------------------------------------
// State indicator config
// ---------------------------------------------------------------------------

const STATE_CONFIG: Record<JarvisState, { color: string; label: string }> = {
  idle:      { color: 'var(--hud-text-dim)', label: 'IDLE' },
  listening: { color: 'var(--hud-green)',    label: 'LISTENING' },
  thinking:  { color: 'var(--hud-amber)',    label: 'THINKING' },
  speaking:  { color: 'var(--hud-cyan)',     label: 'SPEAKING' },
}

// ---------------------------------------------------------------------------
// Timing constants
// ---------------------------------------------------------------------------

/** How long (ms) before Jarvis subtitle text begins fading out. */
const JARVIS_FADE_DELAY_MS = 5000
/** How long (ms) before "You: ..." text begins fading out. */
const USER_FADE_DELAY_MS = 3000
/** How long (ms) after final transcript before clearing it for Jarvis's response. */
const TRANSCRIPT_LINGER_MS = 500
/** How often (ms) we flush the transcript ref to React state. */
const TRANSCRIPT_FLUSH_INTERVAL_MS = 80

// ---------------------------------------------------------------------------
// Mic icon SVG (inline to avoid dependency)
// ---------------------------------------------------------------------------

function MicIcon(): React.ReactElement {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      style={{ flexShrink: 0, marginRight: 6, opacity: 0.7 }}
      aria-hidden="true"
    >
      <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z" />
      <path d="M19 10v2a7 7 0 0 1-14 0v-2" />
      <line x1="12" y1="19" x2="12" y2="23" />
      <line x1="8" y1="23" x2="16" y2="23" />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// HudVoiceBar
// ---------------------------------------------------------------------------

export function HudVoiceBar({ jarvisState }: HudVoiceBarProps): React.ReactElement {
  // ---- Jarvis response subtitle state ----
  const [jarvisText, setJarvisText] = useState('')
  const [jarvisOpacity, setJarvisOpacity] = useState(0)
  const [isUserEcho, setIsUserEcho] = useState(false)

  // ---- Live transcript state ----
  const [transcriptText, setTranscriptText] = useState('')
  const [transcriptOpacity, setTranscriptOpacity] = useState(0)
  const [transcriptPartial, setTranscriptPartial] = useState(true)

  // ---- Which layer is active ----
  const [displayMode, setDisplayMode] = useState<DisplayMode>('idle')

  // ---- Refs ----

  // Accumulates streamed Jarvis response chunks between full message events.
  const subtitleRef = useRef('')

  // Accumulates high-frequency partial transcript text. Flushed periodically.
  const transcriptRef = useRef('')

  // Timer for fading out Jarvis text after a delay.
  const dexFadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Timer for the linger period after final transcript before switching to jarvis.
  const transcriptLingerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Interval for flushing transcript ref to state.
  const transcriptFlushRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // ------------------------------------------------------------------
  // Helpers
  // ------------------------------------------------------------------

  const clearJarvisFadeTimer = useCallback((): void => {
    if (dexFadeTimerRef.current !== null) {
      clearTimeout(dexFadeTimerRef.current)
      dexFadeTimerRef.current = null
    }
  }, [])

  const clearTranscriptLinger = useCallback((): void => {
    if (transcriptLingerRef.current !== null) {
      clearTimeout(transcriptLingerRef.current)
      transcriptLingerRef.current = null
    }
  }, [])

  const stopTranscriptFlush = useCallback((): void => {
    if (transcriptFlushRef.current !== null) {
      clearInterval(transcriptFlushRef.current)
      transcriptFlushRef.current = null
    }
  }, [])

  const startTranscriptFlush = useCallback((): void => {
    // Only start if not already running.
    if (transcriptFlushRef.current !== null) return

    transcriptFlushRef.current = setInterval(() => {
      setTranscriptText(transcriptRef.current)
    }, TRANSCRIPT_FLUSH_INTERVAL_MS)
  }, [])

  /** Show Jarvis text (full opacity) and schedule a fade. */
  const showJarvisText = useCallback((text: string, isUser: boolean, fadeDelayMs: number): void => {
    setJarvisText(text)
    setIsUserEcho(isUser)
    setJarvisOpacity(1)
    setDisplayMode('jarvis')

    clearJarvisFadeTimer()
    dexFadeTimerRef.current = setTimeout(() => {
      setJarvisOpacity(0)
    }, fadeDelayMs)
  }, [clearJarvisFadeTimer])

  /** Clear transcript display and stop the flush interval. */
  const clearTranscript = useCallback((): void => {
    transcriptRef.current = ''
    setTranscriptText('')
    setTranscriptOpacity(0)
    setTranscriptPartial(true)
    stopTranscriptFlush()
    clearTranscriptLinger()
  }, [stopTranscriptFlush, clearTranscriptLinger])

  // ------------------------------------------------------------------
  // Subscribe to Wails "jarvis" events
  // ------------------------------------------------------------------

  useEffect(() => {
    const cancel = EventsOn('jarvis', (event: JarvisEvent) => {
      if (!event || typeof event !== 'object') return

      // ---- Live transcript from STT ----
      if (event.type === 'transcript' && event.text) {
        clearTranscriptLinger()

        if (event.partial) {
          // Partial transcript -- update ref, show at half opacity.
          transcriptRef.current = event.text
          setTranscriptPartial(true)
          setTranscriptOpacity(1)
          setDisplayMode('transcript')
          startTranscriptFlush()

          // Also do an immediate flush so first words appear instantly.
          setTranscriptText(event.text)
        } else {
          // Final transcript -- show at full brightness briefly, then fade.
          transcriptRef.current = event.text
          setTranscriptText(event.text)
          setTranscriptPartial(false)
          setTranscriptOpacity(1)
          setDisplayMode('transcript')
          stopTranscriptFlush()

          // After linger period, fade out transcript.
          transcriptLingerRef.current = setTimeout(() => {
            setTranscriptOpacity(0)
            // After the CSS fade completes (~300ms), clear state entirely.
            setTimeout(() => {
              clearTranscript()
              setDisplayMode('idle')
            }, 350)
          }, TRANSCRIPT_LINGER_MS)
        }
        return
      }

      // ---- Jarvis response (streamed chunks) ----
      if (event.type === 'message_chunk' && event.text) {
        clearTranscript()
        subtitleRef.current =
          subtitleRef.current.length === 0
            ? event.text
            : subtitleRef.current + ' ' + event.text

        showJarvisText(subtitleRef.current, false, JARVIS_FADE_DELAY_MS)
        return
      }

      // ---- Jarvis response (WS "response" type with role) ----
      if (event.type === 'response' && event.role === 'jarvis' && event.text) {
        clearTranscript()
        subtitleRef.current = ''
        showJarvisText(event.text, false, JARVIS_FADE_DELAY_MS)
        return
      }

      if (event.type === 'response' && event.role === 'user' && event.text) {
        clearTranscript()
        subtitleRef.current = ''
        showJarvisText(`You: ${event.text}`, true, USER_FADE_DELAY_MS)
        return
      }

      // ---- Final Jarvis message (in-process engine) ----
      if (event.type === 'message' && event.role === 'jarvis' && event.text) {
        clearTranscript()
        subtitleRef.current = ''
        showJarvisText(event.text, false, JARVIS_FADE_DELAY_MS)
        return
      }

      if (event.type === 'message' && event.role === 'user' && event.text) {
        clearTranscript()
        subtitleRef.current = ''
        showJarvisText(`You: ${event.text}`, true, USER_FADE_DELAY_MS)
        return
      }
    })

    return () => {
      cancel()
      clearJarvisFadeTimer()
      clearTranscript()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // ------------------------------------------------------------------
  // Render
  // ------------------------------------------------------------------

  const { color: dotColor, label: stateLabel } = STATE_CONFIG[jarvisState]

  const showTranscript = displayMode === 'transcript' && transcriptText.length > 0
  const showJarvis = displayMode === 'jarvis' && jarvisText.length > 0

  return (
    <div
      role="status"
      aria-live="polite"
      aria-label="Voice activity bar"
      style={{
        background: 'rgba(0,12,10,0.9)',
        borderTop: '1px solid rgba(0,255,204,0.15)',
        height: 48,
      }}
      className="w-full flex items-center px-4 gap-3"
    >
      {/* ---- State indicator ---- */}
      <div className="flex items-center gap-2 flex-shrink-0">
        <span
          className="inline-block h-2 w-2 rounded-full"
          style={{ background: dotColor }}
        />
        <span className="hud-label" style={{ fontSize: 10 }}>
          {stateLabel}
        </span>
      </div>

      {/* ---- Subtitle area (stacked layers with CSS transitions) ---- */}
      <div className="flex-1 min-w-0 overflow-hidden" style={{ position: 'relative' }}>
        {/* Layer 1: Live transcript (user speaking) */}
        <p
          className="hud-text-dim"
          style={{
            fontSize: 14,
            lineHeight: '20px',
            fontStyle: 'italic',
            opacity: showTranscript ? transcriptOpacity * (transcriptPartial ? 0.5 : 0.85) : 0,
            transition: 'opacity 300ms ease',
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            margin: 0,
            display: 'flex',
            alignItems: 'center',
            position: showJarvis ? 'absolute' : 'relative',
            inset: showJarvis ? 0 : undefined,
            pointerEvents: 'none',
          }}
          aria-hidden={!showTranscript}
        >
          {showTranscript && <MicIcon />}
          {transcriptText}
        </p>

        {/* Layer 2: Jarvis response / user echo */}
        <p
          className={isUserEcho ? 'hud-text-dim' : 'hud-text'}
          style={{
            fontSize: 14,
            lineHeight: '20px',
            textShadow: isUserEcho
              ? 'none'
              : '0 0 8px rgba(0,255,204,0.5)',
            opacity: showJarvis ? jarvisOpacity : 0,
            transition: 'opacity 1000ms ease',
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            margin: 0,
            position: showTranscript ? 'absolute' : 'relative',
            inset: showTranscript ? 0 : undefined,
            pointerEvents: 'none',
          }}
          aria-hidden={!showJarvis}
        >
          {jarvisText}
        </p>
      </div>
    </div>
  )
}
