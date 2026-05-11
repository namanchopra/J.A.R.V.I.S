import { useCallback, useEffect, useRef, useState } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { JarvisMessageContent } from './JarvisCard'
import { JarvisOrb } from './JarvisOrb'
import {
  getJarvisState,
  sendJarvisMessage,
  getJarvisHistory,
  startJarvis,
  type JarvisState,
  type JarvisMessage,
} from '../lib/jarvis-api'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const STATE_POLL_MS = 500
const HISTORY_POLL_MS = 1000

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Formats a timestamp into a relative string like "2m ago" or "just now". */
function relativeTime(ts: string | number): string {
  const ms = typeof ts === 'string' ? new Date(ts).getTime() : Number(ts)
  if (Number.isNaN(ms)) return ''
  const diffS = Math.floor((Date.now() - ms) / 1000)
  if (diffS < 5) return 'just now'
  if (diffS < 60) return `${diffS}s ago`
  if (diffS < 3600) return `${Math.floor(diffS / 60)}m ago`
  return `${Math.floor(diffS / 3600)}h ago`
}

// ---------------------------------------------------------------------------
// Sub-components: Icons
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
// Sub-component: Chat Bubble
// ---------------------------------------------------------------------------

function ChatBubble({ message }: { message: JarvisMessage }): React.ReactElement {
  const isUser = message.role === 'user'

  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'} mb-3`}>
      <div
        className={`max-w-[85%] rounded-lg px-3 py-2 text-sm ${
          isUser
            ? 'bg-acc-blue/20 text-primary'
            : 'bg-muted/30 text-primary'
        }`}
      >
        <JarvisMessageContent content={message.content} role={message.role} />
        {message.timestamp && (
          <p
            className={`mt-1 text-[10px] ${
              isUser ? 'text-acc-blue/60' : 'text-muted'
            }`}
          >
            {relativeTime(message.timestamp)}
          </p>
        )}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Sub-component: Empty State
// ---------------------------------------------------------------------------

function EmptyState({ state, audioLevel }: { state: JarvisState; audioLevel: number }): React.ReactElement {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-6 px-6 text-center">
      <JarvisOrb state={state} audioLevel={audioLevel} className="w-64 h-64" />
      <p className="text-sm text-secondary/60">
        {state === 'idle' ? 'Say something or type a message' : ''}
      </p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main Component
// ---------------------------------------------------------------------------

export function JarvisView(): React.ReactElement {
  // -- State -----------------------------------------------------------------
  const [jarvisState, setJarvisState] = useState<JarvisState>('idle')
  const [messages, setMessages] = useState<JarvisMessage[]>([])
  const [inputText, setInputText] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Streaming: progressive message display from message_chunk events
  const [streamingText, setStreamingText] = useState('')
  const [isStreaming, setIsStreaming] = useState(false)

  // -- Refs ------------------------------------------------------------------
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const mountedRef = useRef(true)
  // Tracks whether the event-based "message" handler already finalized the
  // streaming response into `messages`. Checked by handleSend to avoid
  // adding a duplicate when sendJarvisMessage resolves.
  const streamFinalizedRef = useRef(false)

  // -- Derived ---------------------------------------------------------------
  const inputDisabled =
    sending || jarvisState === 'thinking' || jarvisState === 'speaking'
  const canSubmit = inputText.trim().length > 0 && !inputDisabled

  // Simulated audio level until real audio data is wired (TASK-011)
  const audioLevel =
    jarvisState === 'speaking' ? 0.7 : jarvisState === 'listening' ? 0.4 : 0

  // Status dot color keyed to state
  const statusDotColor: Record<JarvisState, string> = {
    idle: 'bg-zinc-500',
    listening: 'bg-green-400',
    thinking: 'bg-amber-400',
    speaking: 'bg-blue-400',
  }

  // -- Auto-scroll to bottom -------------------------------------------------
  const scrollToBottom = useCallback(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [])

  useEffect(() => {
    scrollToBottom()
  }, [messages, streamingText, scrollToBottom])

  // -- Polling: state (500ms) ------------------------------------------------
  useEffect(() => {
    mountedRef.current = true

    const poll = async (): Promise<void> => {
      const s = await getJarvisState()
      if (mountedRef.current) setJarvisState(s)
    }

    void poll()
    const id = setInterval(() => void poll(), STATE_POLL_MS)

    return () => {
      mountedRef.current = false
      clearInterval(id)
    }
  }, [])

  // -- Polling: history (1s) -------------------------------------------------
  useEffect(() => {
    mountedRef.current = true

    const poll = async (): Promise<void> => {
      const history = await getJarvisHistory()
      if (mountedRef.current && history.length > 0) {
        setMessages(history)
      }
    }

    void poll()
    const id = setInterval(() => void poll(), HISTORY_POLL_MS)

    return () => {
      mountedRef.current = false
      clearInterval(id)
    }
  }, [])

  // -- Subscribe to jarvis events for streaming chunks --------------------------
  useEffect(() => {
    const cancel = EventsOn('jarvis', (event: { type?: string; text?: string; role?: string }) => {
      if (!mountedRef.current) return

      if (event?.type === 'message_chunk' && event.text) {
        // A new sentence arrived -- append to the streaming bubble.
        setIsStreaming(true)
        setStreamingText((prev) => {
          if (prev.length === 0) return event.text as string
          return prev + ' ' + (event.text as string)
        })
      } else if (event?.type === 'error' && event.text) {
        // Surface mic/voice errors to the user.
        setError(event.text as string)
      } else if (event?.type === 'message' && event.role === 'jarvis') {
        // The backend emits a final "message" event with the complete text
        // after streaming finishes. Promote the full response into messages
        // so there is no gap between the streaming bubble disappearing and
        // the finalized message appearing. Mark as finalized so handleSend
        // does not add a duplicate.
        setIsStreaming((wasStreaming) => {
          if (wasStreaming && event.text) {
            streamFinalizedRef.current = true
            setMessages((prev) => [
              ...prev,
              { role: 'assistant', content: event.text as string, timestamp: Date.now() },
            ])
          }
          return false
        })
        setStreamingText('')
      }
    })

    return () => {
      cancel()
    }
  }, [])

  // -- Send message ----------------------------------------------------------
  const handleSend = useCallback(async () => {
    const text = inputText.trim()
    if (text.length === 0 || inputDisabled) return

    setSending(true)
    setError(null)
    setInputText('')

    // Optimistic: show the user message immediately
    const userMsg: JarvisMessage = {
      role: 'user',
      content: text,
      timestamp: Date.now(),
    }
    setMessages((prev) => [...prev, userMsg])

    // Reset the finalization flag before the async call.
    streamFinalizedRef.current = false

    try {
      const reply = await sendJarvisMessage(text)
      if (mountedRef.current) {
        // If the "message" event already promoted the response into
        // messages (streaming path), skip adding a duplicate.
        if (!streamFinalizedRef.current) {
          // Non-streaming path (CLI/batch mode): this is the only place
          // the assistant message gets added.
          const jarvisMsg: JarvisMessage = {
            role: 'assistant',
            content: reply,
            timestamp: Date.now(),
          }
          setMessages((prev) => [...prev, jarvisMsg])
        }
        // Ensure streaming state is clean regardless of path.
        setIsStreaming(false)
        setStreamingText('')
        streamFinalizedRef.current = false
      }
    } catch (err: unknown) {
      if (mountedRef.current) {
        const msg = err instanceof Error ? err.message : String(err)
        setError(msg)
        // Clear streaming on error so the UI doesn't get stuck.
        setIsStreaming(false)
        setStreamingText('')
      }
    } finally {
      if (mountedRef.current) {
        setSending(false)
        textareaRef.current?.focus()
      }
    }
  }, [inputText, inputDisabled])

  // -- Keyboard handling -----------------------------------------------------
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        void handleSend()
      }
    },
    [handleSend],
  )

  // Jarvis auto-starts from the Go backend (app.go startup) when JarvisEnabled=true.

  // -- Focus textarea on mount -----------------------------------------------
  useEffect(() => {
    textareaRef.current?.focus()
  }, [])

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <div className="flex-1 flex flex-col min-h-0">
      {/* ---- Header ---- */}
      <header className="h-12 flex-shrink-0 flex items-center px-5 border-b border-border bg-surface">
        <h1 className="text-base font-bold tracking-wide text-primary">Jarvis</h1>
        <span
          className={`ml-2 inline-block h-2 w-2 rounded-full transition-colors ${statusDotColor[jarvisState]}`}
          aria-label={`Jarvis status: ${jarvisState}`}
        />
      </header>

      {/* ---- Chat area ---- */}
      {messages.length === 0 ? (
        <EmptyState state={jarvisState} audioLevel={audioLevel} />
      ) : (
        <div
          ref={scrollRef}
          className="flex-1 overflow-y-auto px-4 py-3"
        >
          {/* Orb at top of chat area */}
          <div className="flex justify-center py-3">
            <JarvisOrb state={jarvisState} audioLevel={audioLevel} className="w-24 h-24" />
          </div>

          {messages.map((msg, i) => (
            <ChatBubble key={`${msg.role}-${msg.timestamp}-${i}`} message={msg} />
          ))}

          {/* Streaming assistant bubble: grows as chunks arrive */}
          {isStreaming && streamingText.length > 0 && (
            <ChatBubble
              message={{
                role: 'assistant',
                content: streamingText,
                timestamp: Date.now(),
              }}
            />
          )}

          {/* Thinking indicator: shown only before streaming starts */}
          {(sending || jarvisState === 'thinking') && !isStreaming && (
            <div className="flex justify-start mb-3">
              <div className="inline-flex items-center gap-2 rounded-lg bg-muted/30 px-3 py-2 text-sm text-secondary">
                <SpinnerIcon />
                Thinking...
              </div>
            </div>
          )}
        </div>
      )}

      {/* ---- Input area ---- */}
      <div className="flex-shrink-0 border-t border-border p-3">
        <div className="flex items-end gap-2">
          <textarea
            ref={textareaRef}
            value={inputText}
            onChange={(e) => {
              setInputText(e.target.value)
              if (error !== null) setError(null)
            }}
            onKeyDown={handleKeyDown}
            placeholder={
              inputDisabled ? 'Jarvis is busy...' : 'Message Jarvis...'
            }
            disabled={inputDisabled}
            rows={2}
            className={`flex-1 resize-none rounded-lg border border-border bg-app
                        px-3 py-2 font-mono text-sm text-primary placeholder-muted
                        outline-none transition-colors
                        focus:border-acc-blue focus:ring-1 focus:ring-acc-blue/30
                        ${inputDisabled ? 'cursor-not-allowed opacity-50' : ''}`}
            aria-label="Message to Jarvis"
          />
          <button
            type="button"
            onClick={() => void handleSend()}
            disabled={!canSubmit}
            className={`inline-flex flex-shrink-0 items-center justify-center gap-1.5
                        rounded-md px-4 py-2 text-sm font-medium transition-colors
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
          <p className="mt-2 px-1 text-xs text-acc-red">{error}</p>
        )}
      </div>
    </div>
  )
}
