import { useCallback, useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { model } from '../../wailsjs/go/models'
import {
  FocusSession,
  GetPendingApprovals,
  RespondToApproval,
} from '../../wailsjs/go/main/App'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 2_000
const SENT_FLASH_MS = 1_200

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Tracks per-card response state so we can disable buttons and show "Sent". */
type CardState = 'idle' | 'sending' | 'sent'

// ---------------------------------------------------------------------------
// ApprovalPanel (exported)
// ---------------------------------------------------------------------------

export function ApprovalPanel(): React.ReactElement | null {
  const [approvals, setApprovals] = useState<model.ApprovalRequest[]>([])
  const [cardStates, setCardStates] = useState<Record<number, CardState>>({})
  const activeTimeouts = useRef<Set<ReturnType<typeof setTimeout>>>(new Set())

  // Cleanup all active timeouts on unmount
  useEffect(() => {
    return () => {
      for (const handle of activeTimeouts.current) {
        clearTimeout(handle)
      }
      activeTimeouts.current.clear()
    }
  }, [])

  // ---- Polling ----
  useEffect(() => {
    let cancelled = false

    async function poll(): Promise<void> {
      try {
        const pending = await GetPendingApprovals()
        if (!cancelled) {
          setApprovals(pending ?? [])
        }
      } catch (err) {
        console.warn('Failed to fetch pending approvals:', err)
      }
    }

    // Fetch immediately, then every POLL_INTERVAL_MS.
    void poll()
    const id = setInterval(() => void poll(), POLL_INTERVAL_MS)

    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [])

  // ---- Handlers ----

  const handleRespond = useCallback(
    async (pid: number, response: 'y' | 'n'): Promise<void> => {
      setCardStates((prev) => ({ ...prev, [pid]: 'sending' }))

      try {
        await RespondToApproval(pid, response)
      } catch (err) {
        console.warn('Failed to respond to approval:', err)
      }

      setCardStates((prev) => ({ ...prev, [pid]: 'sent' }))

      // Flash "Sent" briefly, then let the next poll cycle remove the card.
      const handle = setTimeout(() => {
        activeTimeouts.current.delete(handle)
        setCardStates((prev) => {
          const next = { ...prev }
          delete next[pid]
          return next
        })
      }, SENT_FLASH_MS)
      activeTimeouts.current.add(handle)
    },
    [],
  )

  const handleFocus = useCallback(async (pid: number): Promise<void> => {
    try {
      await FocusSession(pid)
    } catch (err) {
      console.warn('Failed to focus session:', err)
    }
  }, [])

  // ---- Render nothing when empty ----

  if (approvals.length === 0) {
    return null
  }

  // ---- Render ----

  return (
    <section aria-label="Pending approvals" className="space-y-2">
      {/* Section header */}
      <div className="flex items-center gap-2 px-1 mb-1">
        <span
          className="block w-2 h-2 rounded-full bg-teal-500 animate-pulse"
          aria-hidden="true"
        />
        <h2 className="text-xs font-semibold uppercase tracking-wider text-acc-teal">
          Needs Approval
        </h2>
        <span className="ml-auto text-[10px] text-muted">
          {approvals.length}
        </span>
      </div>

      {/* Cards */}
      <AnimatePresence initial={false}>
        {approvals.map((approval) => (
          <ApprovalCard
            key={approval.pid}
            approval={approval}
            state={cardStates[approval.pid] ?? 'idle'}
            onRespond={handleRespond}
            onFocus={handleFocus}
          />
        ))}
      </AnimatePresence>
    </section>
  )
}

// ---------------------------------------------------------------------------
// ApprovalCard
// ---------------------------------------------------------------------------

interface ApprovalCardProps {
  approval: model.ApprovalRequest
  state: CardState
  onRespond: (pid: number, response: 'y' | 'n') => Promise<void>
  onFocus: (pid: number) => Promise<void>
}

function ApprovalCard({
  approval,
  state,
  onRespond,
  onFocus,
}: ApprovalCardProps): React.ReactElement {
  const buttonsDisabled = state === 'sending' || state === 'sent'

  // Keep a ref so the "Sent" timeout doesn't fire-and-forget on unmounted cards.
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: -8, scale: 0.97 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 8, scale: 0.97 }}
      transition={{ duration: 0.2, ease: 'easeOut' }}
      className="bg-surface border border-acc-teal/30 rounded-lg p-3 space-y-2"
    >
      {/* Header: session name + cwd */}
      <div className="flex items-center gap-2 min-w-0">
        <span className="text-sm font-medium text-primary truncate">
          {approval.sessionName || `PID ${approval.pid}`}
        </span>
        <span className="ml-auto text-[10px] text-muted truncate max-w-[50%] text-right">
          {approval.cwd}
        </span>
      </div>

      {/* Prompt text */}
      <pre className="bg-app font-mono text-[11px] text-secondary rounded p-2 max-h-32 overflow-y-auto whitespace-pre-wrap break-words leading-relaxed">
        {formatPromptText(approval.promptText)}
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
              onClick={() => void onRespond(approval.pid, 'y')}
              aria-label={`Approve ${approval.sessionName}`}
              className="px-2.5 py-1 text-xs font-medium rounded
                         bg-green-600/20 text-green-400
                         hover:bg-green-600/40 transition-colors
                         disabled:opacity-40 disabled:cursor-not-allowed
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-green-500"
            >
              Approve
            </button>

            <button
              type="button"
              disabled={buttonsDisabled}
              onClick={() => void onRespond(approval.pid, 'n')}
              aria-label={`Reject ${approval.sessionName}`}
              className="px-2.5 py-1 text-xs font-medium rounded
                         bg-red-600/20 text-red-400
                         hover:bg-red-600/40 transition-colors
                         disabled:opacity-40 disabled:cursor-not-allowed
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500"
            >
              Reject
            </button>
          </>
        )}

        {/* Focus is always available (not affected by send state) */}
        <button
          type="button"
          onClick={() => void onFocus(approval.pid)}
          aria-label={`Focus session ${approval.sessionName}`}
          className="ml-auto px-2.5 py-1 text-xs font-medium rounded
                     bg-acc-teal/20 text-acc-teal
                     hover:bg-acc-teal/40 transition-colors
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal"
        >
          Focus
        </button>
      </div>
    </motion.div>
  )
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Trim and show only the last meaningful lines of the prompt text so the card
 * stays compact. If the full text is short enough, show it all.
 */
function formatPromptText(raw: string): string {
  const trimmed = raw.trim()
  if (!trimmed) return '(empty prompt)'

  const lines = trimmed.split('\n')
  const MAX_LINES = 12

  if (lines.length <= MAX_LINES) {
    return trimmed
  }

  return lines.slice(-MAX_LINES).join('\n')
}
