import { useCallback, useState } from 'react'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ApprovalCardData {
  pid: number
  session: string
  prompt: string
}

type SessionStatus = 'running' | 'completed' | 'failed' | 'needs-input' | 'paused'

interface SessionCardData {
  id: string
  name: string
  status: SessionStatus
  repo: string
  duration: string
}

/** A parsed segment from a Jarvis message: either plain text or a card block. */
type MessageSegment =
  | { kind: 'text'; content: string }
  | { kind: 'approval'; data: ApprovalCardData }
  | { kind: 'session'; data: SessionCardData }

type ApprovalCardState = 'idle' | 'sending' | 'sent'

// ---------------------------------------------------------------------------
// Card-block parser
//
// Extracts [CARD:type]{...json...}[/CARD] blocks from message content and
// returns an array of segments: plain text interleaved with typed card data.
// ---------------------------------------------------------------------------

const CARD_REGEX = /\[CARD:(approval|session)\]([\s\S]*?)\[\/CARD\]/g

export function parseCardBlocks(content: string): MessageSegment[] {
  const segments: MessageSegment[] = []
  let lastIndex = 0

  // Reset lastIndex in case regex was used before (global flag)
  CARD_REGEX.lastIndex = 0

  let match: RegExpExecArray | null = CARD_REGEX.exec(content)
  while (match !== null) {
    // Push any text before this match
    if (match.index > lastIndex) {
      const text = content.slice(lastIndex, match.index).trim()
      if (text.length > 0) {
        segments.push({ kind: 'text', content: text })
      }
    }

    const cardType = match[1] as 'approval' | 'session'
    const jsonStr = match[2] ?? ''

    try {
      const parsed: unknown = JSON.parse(jsonStr)

      if (cardType === 'approval' && isApprovalData(parsed)) {
        segments.push({ kind: 'approval', data: parsed })
      } else if (cardType === 'session' && isSessionData(parsed)) {
        segments.push({ kind: 'session', data: parsed })
      } else {
        // Malformed JSON for the card type -- render as text fallback
        segments.push({ kind: 'text', content: match[0] })
      }
    } catch {
      // Invalid JSON -- render the raw block as text
      segments.push({ kind: 'text', content: match[0] })
    }

    lastIndex = match.index + match[0].length
    match = CARD_REGEX.exec(content)
  }

  // Push any trailing text
  if (lastIndex < content.length) {
    const text = content.slice(lastIndex).trim()
    if (text.length > 0) {
      segments.push({ kind: 'text', content: text })
    }
  }

  return segments
}

/** Returns true if message content contains at least one [CARD:...] block. */
export function hasCardBlocks(content: string): boolean {
  CARD_REGEX.lastIndex = 0
  return CARD_REGEX.test(content)
}

// ---------------------------------------------------------------------------
// Type guards
// ---------------------------------------------------------------------------

function isApprovalData(value: unknown): value is ApprovalCardData {
  if (typeof value !== 'object' || value === null) return false
  const obj = value as Record<string, unknown>
  return (
    typeof obj['pid'] === 'number' &&
    typeof obj['session'] === 'string' &&
    typeof obj['prompt'] === 'string'
  )
}

function isSessionData(value: unknown): value is SessionCardData {
  if (typeof value !== 'object' || value === null) return false
  const obj = value as Record<string, unknown>
  return (
    typeof obj['id'] === 'string' &&
    typeof obj['name'] === 'string' &&
    typeof obj['status'] === 'string' &&
    typeof obj['repo'] === 'string' &&
    typeof obj['duration'] === 'string'
  )
}

// ---------------------------------------------------------------------------
// Wails binding wrappers
//
// These call through the same window.go.main.App bridge used by the rest of
// the frontend. Each wrapper handles the binding being unavailable gracefully.
// ---------------------------------------------------------------------------

async function respondToApproval(pid: number, response: 'y' | 'n'): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.RespondToApproval as
      | ((p: number, r: string) => Promise<void>)
      | undefined
    if (fn) await fn(pid, response)
  } catch {
    console.warn('RespondToApproval binding not available')
  }
}

async function resumeSession(sessionId: string): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.ResumeSession as
      | ((id: string) => Promise<unknown>)
      | undefined
    if (fn) await fn(sessionId)
  } catch {
    console.warn('ResumeSession binding not available')
  }
}

async function stopSession(sessionId: string): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.StopSession as
      | ((id: string) => Promise<void>)
      | undefined
    if (fn) await fn(sessionId)
  } catch {
    console.warn('StopSession binding not available')
  }
}

async function focusSession(pid: number): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.FocusSession as
      | ((p: number) => Promise<void>)
      | undefined
    if (fn) await fn(pid)
  } catch {
    console.warn('FocusSession binding not available')
  }
}

// ---------------------------------------------------------------------------
// ApprovalCard
// ---------------------------------------------------------------------------

function ApprovalCard({ data }: { data: ApprovalCardData }): React.ReactElement {
  const [state, setState] = useState<ApprovalCardState>('idle')

  const handleRespond = useCallback(
    async (response: 'y' | 'n'): Promise<void> => {
      setState('sending')
      await respondToApproval(data.pid, response)
      setState('sent')
    },
    [data.pid],
  )

  const buttonsDisabled = state === 'sending' || state === 'sent'

  return (
    <div className="bg-muted/20 border border-border rounded-lg p-3 my-2 space-y-2">
      {/* Header */}
      <div className="flex items-center gap-2 min-w-0">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-amber-400">
          Approval
        </span>
        <span className="ml-auto text-[10px] text-muted truncate max-w-[60%] text-right">
          {data.session}
        </span>
      </div>

      {/* Prompt text */}
      <pre className="bg-app font-mono text-[11px] text-secondary rounded p-2 max-h-24 overflow-y-auto whitespace-pre-wrap break-words leading-relaxed">
        {data.prompt}
      </pre>

      {/* Action buttons */}
      <div className="flex items-center gap-2">
        {state === 'sent' ? (
          <span className="text-xs font-medium text-acc-teal animate-pulse">
            Sent
          </span>
        ) : (
          <>
            <button
              type="button"
              disabled={buttonsDisabled}
              onClick={() => void handleRespond('y')}
              aria-label={`Approve: ${data.prompt}`}
              className="px-2.5 py-1 text-xs font-medium rounded
                         bg-green-600/20 text-green-400
                         hover:bg-green-600/40 transition-colors
                         disabled:opacity-40 disabled:cursor-not-allowed
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-green-500"
            >
              {state === 'sending' ? 'Sending...' : 'Approve'}
            </button>

            <button
              type="button"
              disabled={buttonsDisabled}
              onClick={() => void handleRespond('n')}
              aria-label={`Deny: ${data.prompt}`}
              className="px-2.5 py-1 text-xs font-medium rounded
                         bg-red-600/20 text-red-400
                         hover:bg-red-600/40 transition-colors
                         disabled:opacity-40 disabled:cursor-not-allowed
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500"
            >
              Deny
            </button>
          </>
        )}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// SessionCard
// ---------------------------------------------------------------------------

const STATUS_BADGE: Record<SessionStatus, string> = {
  running: 'bg-green-600/20 text-green-400',
  completed: 'bg-blue-600/20 text-blue-400',
  failed: 'bg-red-600/20 text-red-400',
  'needs-input': 'bg-amber-600/20 text-amber-400',
  paused: 'bg-muted/40 text-muted',
}

function SessionCard({ data }: { data: SessionCardData }): React.ReactElement {
  const [actionState, setActionState] = useState<'idle' | 'sending' | 'sent'>('idle')

  const handleResume = useCallback(async (): Promise<void> => {
    setActionState('sending')
    await resumeSession(data.id)
    setActionState('sent')
  }, [data.id])

  const handleStop = useCallback(async (): Promise<void> => {
    setActionState('sending')
    await stopSession(data.id)
    setActionState('sent')
  }, [data.id])

  const handleFocus = useCallback(async (): Promise<void> => {
    // FocusSession takes a pid (number). Parse from the id if numeric,
    // otherwise default to 0 (the binding will handle it).
    const pid = Number(data.id)
    if (!Number.isNaN(pid)) {
      await focusSession(pid)
    }
  }, [data.id])

  const badgeClasses = STATUS_BADGE[data.status] ?? STATUS_BADGE['paused']
  const buttonsDisabled = actionState === 'sending' || actionState === 'sent'

  return (
    <div className="bg-muted/20 border border-border rounded-lg p-3 my-2 space-y-2">
      {/* Header: name + status badge */}
      <div className="flex items-center gap-2 min-w-0">
        <span className="text-sm font-medium text-primary truncate">
          {data.name}
        </span>
        <span
          className={`ml-auto text-[10px] font-semibold uppercase tracking-wider rounded-full px-2 py-0.5 ${badgeClasses}`}
        >
          {data.status}
        </span>
      </div>

      {/* Details row */}
      <div className="flex items-center gap-3 text-[11px] text-muted">
        <span className="truncate max-w-[60%]" title={data.repo}>
          {data.repo}
        </span>
        {data.duration && (
          <span className="ml-auto whitespace-nowrap">{data.duration}</span>
        )}
      </div>

      {/* Action buttons (context-dependent) */}
      <div className="flex items-center gap-2">
        {actionState === 'sent' ? (
          <span className="text-xs font-medium text-acc-teal animate-pulse">
            Sent
          </span>
        ) : (
          <>
            {(data.status === 'paused' || data.status === 'needs-input') && (
              <button
                type="button"
                disabled={buttonsDisabled}
                onClick={() => void handleResume()}
                aria-label={`Resume session ${data.name}`}
                className="px-2.5 py-1 text-xs font-medium rounded
                           bg-green-600/20 text-green-400
                           hover:bg-green-600/40 transition-colors
                           disabled:opacity-40 disabled:cursor-not-allowed
                           focus:outline-none focus-visible:ring-2 focus-visible:ring-green-500"
              >
                {actionState === 'sending' ? 'Sending...' : 'Resume'}
              </button>
            )}

            {data.status === 'running' && (
              <button
                type="button"
                disabled={buttonsDisabled}
                onClick={() => void handleStop()}
                aria-label={`Stop session ${data.name}`}
                className="px-2.5 py-1 text-xs font-medium rounded
                           bg-red-600/20 text-red-400
                           hover:bg-red-600/40 transition-colors
                           disabled:opacity-40 disabled:cursor-not-allowed
                           focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500"
              >
                {actionState === 'sending' ? 'Stopping...' : 'Stop'}
              </button>
            )}

            <button
              type="button"
              onClick={() => void handleFocus()}
              aria-label={`Focus session ${data.name}`}
              className="px-2.5 py-1 text-xs font-medium rounded
                         bg-acc-teal/20 text-acc-teal
                         hover:bg-acc-teal/40 transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal"
            >
              Focus
            </button>
          </>
        )}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// JarvisMessageContent
//
// Exported component used by JarvisView's ChatBubble. For assistant messages,
// it parses [CARD:...] blocks and renders a mix of text and inline cards.
// For user messages (or messages with no card blocks) it renders plain text.
// ---------------------------------------------------------------------------

export function JarvisMessageContent({
  content,
  role,
}: {
  content: string
  role: string
}): React.ReactElement {
  // User messages and messages without card blocks: render as plain text.
  if (role === 'user' || !hasCardBlocks(content)) {
    return <p className="whitespace-pre-wrap break-words">{content}</p>
  }

  const segments = parseCardBlocks(content)

  return (
    <div className="space-y-0">
      {segments.map((segment, i) => {
        const key = `seg-${i}`

        switch (segment.kind) {
          case 'text':
            return (
              <p key={key} className="whitespace-pre-wrap break-words">
                {segment.content}
              </p>
            )
          case 'approval':
            return <ApprovalCard key={key} data={segment.data} />
          case 'session':
            return <SessionCard key={key} data={segment.data} />
        }
      })}
    </div>
  )
}
