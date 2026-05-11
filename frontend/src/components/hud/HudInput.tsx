import { useCallback, useEffect, useRef, useState } from 'react'
import { sendJarvisMessage } from '../../lib/jarvis-api'
import '../../lib/hud-theme'

// ---------------------------------------------------------------------------
// HudInput -- compact single-line input bar for sending messages to Jarvis
// ---------------------------------------------------------------------------

interface HudInputProps {
  /** Whether Jarvis mic is currently muted. */
  isMuted: boolean
  /** Callback to toggle mute on/off. */
  onToggleMute: () => void
}

export function HudInput({ isMuted, onToggleMute }: HudInputProps): React.ReactElement {
  const inputRef = useRef<HTMLInputElement>(null)
  const errorTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Auto-focus on mount
  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  // Cleanup error dismiss timer on unmount
  useEffect(() => {
    return () => {
      if (errorTimerRef.current) clearTimeout(errorTimerRef.current)
    }
  }, [])

  const showError = useCallback((message: string) => {
    setError(message)
    if (errorTimerRef.current) clearTimeout(errorTimerRef.current)
    errorTimerRef.current = setTimeout(() => {
      setError(null)
      errorTimerRef.current = null
    }, 5000)
  }, [])

  const handleSend = useCallback(async () => {
    const trimmed = text.trim()
    if (!trimmed || sending) return

    setSending(true)
    setError(null)

    try {
      await sendJarvisMessage(trimmed)
      setText('')
      // Re-focus input after successful send
      inputRef.current?.focus()
    } catch (err: unknown) {
      const message =
        err instanceof Error ? err.message : 'Failed to send message'
      showError(message)
    } finally {
      setSending(false)
    }
  }, [text, sending, showError])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        handleSend()
      }
    },
    [handleSend],
  )

  const isEmpty = text.trim().length === 0

  return (
    <div className="w-full flex flex-col">
      {/* ---- Input row ---- */}
      <div
        className="flex items-center gap-2 w-full"
        style={{
          height: 40,
          background: 'var(--hud-bg)',
          padding: '0 12px',
        }}
      >
        <input
          ref={inputRef}
          type="text"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={handleKeyDown}
          disabled={sending}
          placeholder="Type to Jarvis..."
          className="flex-1 bg-transparent outline-none"
          style={{
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 13,
            color: 'var(--hud-text)',
            borderBottom: '1px solid var(--hud-cyan-dim)',
            paddingBottom: 4,
            opacity: sending ? 0.5 : 1,
            transition: 'border-color 0.2s, opacity 0.2s',
          }}
          onFocus={(e) => {
            e.currentTarget.style.borderBottomColor = 'var(--hud-cyan)'
          }}
          onBlur={(e) => {
            e.currentTarget.style.borderBottomColor = 'var(--hud-cyan-dim)'
          }}
        />
        <button
          type="button"
          onClick={handleSend}
          disabled={isEmpty || sending}
          aria-label="Send message"
          className="flex-shrink-0 flex items-center justify-center"
          style={{
            width: 28,
            height: 28,
            borderRadius: 4,
            border: 'none',
            background: 'transparent',
            color: 'var(--hud-text)',
            fontFamily: "'SF Mono', 'Menlo', monospace",
            fontSize: 16,
            cursor: isEmpty || sending ? 'default' : 'pointer',
            opacity: isEmpty || sending ? 0.3 : 0.7,
            transition: 'opacity 0.2s',
          }}
          onMouseEnter={(e) => {
            if (!isEmpty && !sending) {
              e.currentTarget.style.opacity = '1'
            }
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.opacity =
              isEmpty || sending ? '0.3' : '0.7'
          }}
        >
          {'\u2192'}
        </button>

        {/* ---- Mute toggle ---- */}
        <button
          type="button"
          onClick={onToggleMute}
          aria-label={isMuted ? 'Unmute microphone' : 'Mute microphone'}
          title={`${isMuted ? 'Unmute' : 'Mute'} (\u2318\u21E7M)`}
          className="flex-shrink-0 flex items-center justify-center relative"
          style={{
            width: 28,
            height: 28,
            borderRadius: 4,
            border: 'none',
            background: 'transparent',
            cursor: 'pointer',
            transition: 'opacity 0.2s',
          }}
          onMouseEnter={(e) => {
            e.currentTarget.style.opacity = '1'
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.opacity = '0.8'
          }}
        >
          {/* Mic SVG icon */}
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke={isMuted ? 'var(--hud-red)' : 'var(--hud-cyan)'}
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{
              filter: isMuted
                ? 'drop-shadow(0 0 4px rgba(255,68,68,0.5))'
                : 'drop-shadow(0 0 4px rgba(0,255,204,0.3))',
              transition: 'filter 0.2s, stroke 0.2s',
            }}
          >
            {/* Mic body */}
            <rect x="9" y="1" width="6" height="12" rx="3" />
            {/* Mic base arc */}
            <path d="M5 10a7 7 0 0 0 14 0" />
            {/* Mic stand */}
            <line x1="12" y1="17" x2="12" y2="21" />
            <line x1="8" y1="21" x2="16" y2="21" />
            {/* Strikethrough line when muted */}
            {isMuted && (
              <line
                x1="2"
                y1="2"
                x2="22"
                y2="22"
                stroke="var(--hud-red)"
                strokeWidth="2"
              />
            )}
          </svg>
        </button>
      </div>

      {/* ---- Error message ---- */}
      {error && (
        <p
          className="px-3 pt-1"
          style={{
            fontSize: 11,
            fontFamily: "'SF Mono', 'Menlo', monospace",
            color: 'var(--hud-red)',
          }}
        >
          {error}
        </p>
      )}
    </div>
  )
}
