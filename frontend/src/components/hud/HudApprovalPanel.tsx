import { useState, useCallback } from 'react'
import '../../lib/hud-theme'

// ---------------------------------------------------------------------------
// Types (inline — no import from wailsjs/go/models)
// ---------------------------------------------------------------------------

interface ApprovalRequest {
  pid: number
  sessionName: string
  cwd: string
  promptText: string
  detectedAt: any
}

interface HudApprovalPanelProps {
  approvals: ApprovalRequest[]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Truncate text to maxLen characters, appending "..." if needed. */
function truncate(text: string, maxLen: number): string {
  if (text.length <= maxLen) return text
  return text.slice(0, maxLen) + '...'
}

/** Call the Wails binding to respond to an approval prompt. */
async function respondToApproval(pid: number, response: string): Promise<void> {
  const fn = (window as any)?.go?.main?.App?.RespondToApproval as
    | ((pid: number, response: string) => Promise<void>)
    | undefined
  if (fn) {
    await fn(pid, response)
  }
}

// ---------------------------------------------------------------------------
// HudApprovalPanel
// ---------------------------------------------------------------------------

export function HudApprovalPanel({ approvals }: HudApprovalPanelProps): React.ReactElement {
  // Track which PIDs are currently being responded to (prevents double-fire).
  const [responding, setResponding] = useState<Record<number, boolean>>({})

  // Track approvals that have been handled so we can hide them.
  const [handled, setHandled] = useState<Set<number>>(new Set())

  const pending = approvals.filter((a) => !handled.has(a.pid))
  const hasPending = pending.length > 0

  const respond = useCallback(
    async (pid: number, response: 'y' | 'n') => {
      setResponding((prev) => ({ ...prev, [pid]: true }))
      try {
        await respondToApproval(pid, response)
        setHandled((prev) => {
          const next = new Set(prev)
          next.add(pid)
          return next
        })
      } catch {
        // Re-enable buttons on failure so the user can retry.
        setResponding((prev) => ({ ...prev, [pid]: false }))
      }
    },
    [],
  )

  const approveAll = useCallback(async () => {
    // Mark all as responding.
    const pids = pending.map((a) => a.pid)
    setResponding((prev) => {
      const next = { ...prev }
      for (const pid of pids) next[pid] = true
      return next
    })

    const results = await Promise.allSettled(
      pids.map((pid) => respondToApproval(pid, 'y')),
    )

    const succeeded = new Set<number>()
    const failed = new Set<number>()
    results.forEach((result, i) => {
      const pid = pids[i]!
      if (result.status === 'fulfilled') {
        succeeded.add(pid)
      } else {
        failed.add(pid)
      }
    })

    if (succeeded.size > 0) {
      setHandled((prev) => {
        const next = new Set(prev)
        for (const pid of succeeded) next.add(pid)
        return next
      })
    }

    if (failed.size > 0) {
      setResponding((prev) => {
        const next = { ...prev }
        for (const pid of failed) next[pid] = false
        return next
      })
    }
  }, [pending])

  return (
    <div className="hud-panel flex flex-col overflow-hidden">
      {/* ---- Header ---- */}
      <div className="flex items-center gap-2">
        <span
          className="hud-label"
          style={
            hasPending
              ? {
                  color: 'var(--hud-amber)',
                  textShadow: '0 0 8px rgba(255, 170, 0, 0.5)',
                  animation: 'hud-amber-pulse 2s ease-in-out infinite',
                }
              : undefined
          }
        >
          APPROVALS
        </span>

        {hasPending && (
          <span
            className="text-[10px] font-bold rounded px-1.5 py-0.5 leading-none"
            style={{
              color: '#000',
              backgroundColor: 'var(--hud-amber)',
            }}
          >
            {pending.length}
          </span>
        )}

        {/* Approve All — only when 2+ pending */}
        {pending.length >= 2 && (
          <button
            className="ml-auto text-[10px] uppercase tracking-wider font-semibold px-2 py-0.5 rounded border transition-opacity"
            style={{
              color: 'var(--hud-cyan)',
              borderColor: 'var(--hud-cyan)',
              background: 'transparent',
              opacity: pending.every((a) => responding[a.pid]) ? 0.4 : 1,
            }}
            disabled={pending.every((a) => responding[a.pid])}
            onClick={approveAll}
          >
            Approve All
          </button>
        )}
      </div>

      {/* ---- Approval list ---- */}
      <div className="mt-2 flex-1 overflow-y-auto space-y-2">
        {!hasPending ? (
          <p className="hud-text-dim text-xs text-center mt-4">All clear</p>
        ) : (
          pending.map((a) => {
            const isResponding = !!responding[a.pid]
            return (
              <div
                key={a.pid}
                className="rounded px-2 py-2"
                style={{
                  border: '1px solid rgba(255, 170, 0, 0.2)',
                  background: 'rgba(255, 170, 0, 0.05)',
                }}
              >
                {/* Session name */}
                <p
                  className="hud-text text-xs font-medium truncate"
                  style={{ color: 'var(--hud-amber)' }}
                >
                  {a.sessionName}
                </p>

                {/* Prompt text preview */}
                <p className="hud-text-dim text-xs mt-1 leading-snug">
                  {truncate(a.promptText, 80)}
                </p>

                {/* Action buttons */}
                <div className="flex gap-2 mt-2">
                  <button
                    className="text-[10px] uppercase tracking-wider font-semibold px-2.5 py-1 rounded border transition-opacity"
                    style={{
                      color: 'var(--hud-cyan)',
                      borderColor: 'var(--hud-cyan)',
                      background: 'transparent',
                      opacity: isResponding ? 0.4 : 1,
                    }}
                    disabled={isResponding}
                    onClick={() => respond(a.pid, 'y')}
                  >
                    Approve
                  </button>
                  <button
                    className="text-[10px] uppercase tracking-wider font-semibold px-2.5 py-1 rounded border transition-opacity"
                    style={{
                      color: 'var(--hud-red)',
                      borderColor: 'var(--hud-red)',
                      background: 'transparent',
                      opacity: isResponding ? 0.4 : 1,
                    }}
                    disabled={isResponding}
                    onClick={() => respond(a.pid, 'n')}
                  >
                    Deny
                  </button>
                </div>
              </div>
            )
          })
        )}
      </div>

      {/* Inline keyframe for amber pulse animation */}
      <style>{`
        @keyframes hud-amber-pulse {
          0%, 100% { text-shadow: 0 0 8px rgba(255, 170, 0, 0.5); }
          50% { text-shadow: 0 0 16px rgba(255, 170, 0, 0.8), 0 0 32px rgba(255, 170, 0, 0.3); }
        }
      `}</style>
    </div>
  )
}
