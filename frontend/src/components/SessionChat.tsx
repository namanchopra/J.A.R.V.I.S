import { useCallback, useRef, useState } from 'react'
import { SendSessionMessage } from '../../wailsjs/go/main/App'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface SessionChatProps {
  sessionId: string
  sessionStatus: string
  onMessageSent: (message: string) => void
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Returns true when the session status allows sending messages. */
function canSendMessage(status: string): boolean {
  return status === 'running' || status === 'needs-input'
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function SendIcon(): React.ReactElement {
  return (
    <svg
      className="w-4 h-4"
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M10.894 2.553a1 1 0 00-1.788 0l-7 14a1 1 0 001.169 1.409l5-1.429A1 1 0 009 15.571V11a1 1 0 112 0v4.571a1 1 0 00.725.962l5 1.428a1 1 0 001.17-1.408l-7-14z" />
    </svg>
  )
}

function SpinnerIcon(): React.ReactElement {
  return (
    <svg
      className="w-3.5 h-3.5 animate-spin"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
    >
      <circle
        className="opacity-25"
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        strokeWidth="4"
      />
      <path
        className="opacity-75"
        fill="currentColor"
        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
      />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function SessionChat({
  sessionId,
  sessionStatus,
  onMessageSent,
}: SessionChatProps): React.ReactElement {
  const [message, setMessage] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const enabled = canSendMessage(sessionStatus)
  const trimmedMessage = message.trim()
  const canSubmit = enabled && trimmedMessage.length > 0 && !sending

  // -------------------------------------------------------------------------
  // Submission
  // -------------------------------------------------------------------------

  const handleSubmit = useCallback(async () => {
    if (!canSubmit) return

    const text = trimmedMessage
    setSending(true)
    setError(null)

    // Emit the user message locally immediately (optimistic)
    onMessageSent(text)

    try {
      await SendSessionMessage(sessionId, text)
      setMessage('')
      // Re-focus textarea after clearing
      textareaRef.current?.focus()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(msg)
    } finally {
      setSending(false)
    }
  }, [canSubmit, trimmedMessage, sessionId, onMessageSent])

  // -------------------------------------------------------------------------
  // Keyboard handling
  // -------------------------------------------------------------------------

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      // Enter without Shift sends the message
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        void handleSubmit()
      }
    },
    [handleSubmit],
  )

  // -------------------------------------------------------------------------
  // Disabled hint text
  // -------------------------------------------------------------------------

  function placeholderText(): string {
    if (sending) return 'Sending...'
    if (!enabled) {
      switch (sessionStatus) {
        case 'launching':
          return 'Waiting for agent to start...'
        case 'completed':
          return 'Session completed'
        case 'failed':
          return 'Session failed'
        default:
          return 'Chat unavailable'
      }
    }
    return 'Send a message... (Enter to send, Shift+Enter for newline)'
  }

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <div className="flex-shrink-0 mx-4 mb-4 mt-3">
      <div className="bg-surface border border-border rounded-lg px-3 py-2.5">
        <div className="flex items-end gap-2">
          {/* Textarea */}
          <textarea
            ref={textareaRef}
            value={message}
            onChange={(e) => {
              setMessage(e.target.value)
              if (error !== null) setError(null)
            }}
            onKeyDown={handleKeyDown}
            placeholder={placeholderText()}
            disabled={!enabled || sending}
            rows={2}
            onFocus={(e) => {
              e.currentTarget.rows = 4
            }}
            onBlur={(e) => {
              // Only shrink if empty
              if (e.currentTarget.value.trim() === '') {
                e.currentTarget.rows = 2
              }
            }}
            className={`flex-1 bg-app border border-border rounded-lg px-3 py-2
                        font-mono text-sm text-primary placeholder-muted
                        resize-none outline-none transition-colors
                        focus:border-acc-blue focus:ring-1 focus:ring-acc-blue/30
                        ${!enabled || sending ? 'opacity-50 cursor-not-allowed' : ''}`}
            aria-label="Message to agent"
          />

          {/* Send button */}
          <button
            type="button"
            onClick={() => void handleSubmit()}
            disabled={!canSubmit}
            className={`flex-shrink-0 inline-flex items-center justify-center gap-1.5
                        px-4 py-2 rounded-md text-sm font-medium transition-colors
                        focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500
                        ${
                          canSubmit
                            ? 'bg-blue-600 text-white hover:bg-blue-500 active:bg-blue-700'
                            : 'bg-blue-600/40 text-blue-300/50 cursor-not-allowed opacity-50'
                        }`}
            aria-label="Send message"
          >
            {sending ? (
              <>
                <SpinnerIcon />
                Sending
              </>
            ) : (
              <>
                <SendIcon />
                Send
              </>
            )}
          </button>
        </div>

        {/* Error display */}
        {error !== null && (
          <p className="text-xs text-acc-red mt-2 px-1">{error}</p>
        )}
      </div>
    </div>
  )
}
