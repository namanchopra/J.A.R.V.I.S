import { useCallback, useState } from 'react'
import {
  BroadcastCommand,
  BroadcastToAll,
} from '../../wailsjs/go/main/App'
import type { claude } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface BroadcastPanelProps {
  indicators: claude.SessionIndicator[]
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type SendState = 'idle' | 'loading' | 'done'

interface BroadcastResult {
  pid: number
  name: string
  outcome: string
  ok: boolean
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function BroadcastIcon(): React.ReactElement {
  return (
    <svg
      className="w-4 h-4 text-secondary"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {/* Antenna / broadcast tower */}
      <path d="M4.9 19.1C1 15.2 1 8.8 4.9 4.9" />
      <path d="M7.8 16.2c-2.3-2.3-2.3-6.1 0-8.4" />
      <path d="M16.2 7.8c2.3 2.3 2.3 6.1 0 8.4" />
      <path d="M19.1 4.9C23 8.8 23 15.1 19.1 19" />
      <circle cx="12" cy="12" r="2" />
    </svg>
  )
}

function ChevronIcon({ expanded }: { expanded: boolean }): React.ReactElement {
  return (
    <svg
      className={`w-4 h-4 text-secondary transition-transform flex-shrink-0 ${expanded ? 'rotate-90' : ''}`}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="9 18 15 12 9 6" />
    </svg>
  )
}

function SpinnerIcon(): React.ReactElement {
  return (
    <svg
      className="animate-spin w-3.5 h-3.5"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="2" opacity="0.25" />
      <path
        d="M14 8a6 6 0 00-6-6"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
      />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Shorten an absolute path to the last two segments for readability. */
function shortCwd(cwd: string): string {
  const parts = cwd.replace(/\/$/, '').split('/')
  return parts.length <= 2 ? cwd : '.../' + parts.slice(-2).join('/')
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function BroadcastPanel({
  indicators,
}: BroadcastPanelProps): React.ReactElement {
  // -- Collapse state -------------------------------------------------------
  const [expanded, setExpanded] = useState(false)

  // -- Selection state ------------------------------------------------------
  const [selectedPids, setSelectedPids] = useState<Set<number>>(new Set())

  // -- Command input --------------------------------------------------------
  const [command, setCommand] = useState('claude')

  // -- Send state -----------------------------------------------------------
  const [sendState, setSendState] = useState<SendState>('idle')
  const [results, setResults] = useState<BroadcastResult[]>([])

  // -- Derived --------------------------------------------------------------
  const allSelected =
    indicators.length > 0 && selectedPids.size === indicators.length

  // -- Handlers -------------------------------------------------------------

  const togglePid = useCallback((pid: number) => {
    setSelectedPids((prev) => {
      const next = new Set(prev)
      if (next.has(pid)) {
        next.delete(pid)
      } else {
        next.add(pid)
      }
      return next
    })
  }, [])

  const toggleAll = useCallback(() => {
    if (allSelected) {
      setSelectedPids(new Set())
    } else {
      setSelectedPids(new Set(indicators.map((i) => i.pid)))
    }
  }, [allSelected, indicators])

  /** Map the backend Record<number, string> to our result list. */
  const mapResults = useCallback(
    (raw: Record<number, string>): BroadcastResult[] => {
      return Object.entries(raw).map(([pidStr, outcome]) => {
        const pid = Number(pidStr)
        const ind = indicators.find((i) => i.pid === pid)
        const ok = outcome === '' || outcome.toLowerCase().startsWith('ok')
        return {
          pid,
          name: ind?.name ?? `PID ${pid}`,
          outcome: outcome === '' ? 'Sent' : outcome,
          ok,
        }
      })
    },
    [indicators],
  )

  const handleSendSelected = useCallback(async () => {
    if (selectedPids.size === 0 || command.trim() === '') return
    setSendState('loading')
    setResults([])
    try {
      const raw = await BroadcastCommand(
        Array.from(selectedPids),
        command.trim(),
      )
      setResults(mapResults(raw))
    } catch (err) {
      setResults([
        {
          pid: 0,
          name: 'Error',
          outcome: err instanceof Error ? err.message : String(err),
          ok: false,
        },
      ])
    } finally {
      setSendState('done')
    }
  }, [selectedPids, command, mapResults])

  const handleSendAll = useCallback(async () => {
    if (command.trim() === '') return
    setSendState('loading')
    setResults([])
    try {
      const raw = await BroadcastToAll(command.trim())
      setResults(mapResults(raw))
    } catch (err) {
      setResults([
        {
          pid: 0,
          name: 'Error',
          outcome: err instanceof Error ? err.message : String(err),
          ok: false,
        },
      ])
    } finally {
      setSendState('done')
    }
  }, [command, mapResults])

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <div className="bg-surface border border-border rounded-xl overflow-hidden">
      {/* Collapsible header */}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="w-full flex items-center gap-2 px-4 py-3 text-left
                   hover:bg-elevated transition-colors
                   focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal"
      >
        <ChevronIcon expanded={expanded} />
        <BroadcastIcon />
        <span className="text-sm font-semibold text-primary">
          Broadcast Command
        </span>
        <span className="ml-auto text-xs text-secondary">
          {indicators.length} session{indicators.length !== 1 ? 's' : ''}
        </span>
      </button>

      {expanded && (
        <div className="px-4 pb-4 space-y-3 border-t border-border">
          {/* -------------------------------------------------------------- */}
          {/* Session checkbox list                                           */}
          {/* -------------------------------------------------------------- */}
          {indicators.length === 0 ? (
            <p className="text-xs text-secondary py-2">
              No active sessions detected.
            </p>
          ) : (
            <>
              {/* Select / Deselect All toggle */}
              <div className="pt-3 flex items-center gap-2">
                <button
                  type="button"
                  onClick={toggleAll}
                  className="text-xs text-acc-teal hover:text-acc-teal/80 transition-colors
                             focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal rounded"
                >
                  {allSelected ? 'Deselect All' : 'Select All'}
                </button>
                <span className="text-xs text-muted">
                  {selectedPids.size} selected
                </span>
              </div>

              {/* Session list */}
              <ul className="space-y-1 max-h-48 overflow-y-auto pr-1">
                {indicators.map((ind) => {
                  const checked = selectedPids.has(ind.pid)
                  return (
                    <li key={ind.pid}>
                      <label
                        className={`flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg cursor-pointer
                                    text-xs transition-colors
                                    ${checked ? 'bg-acc-teal/10 border border-acc-teal/30' : 'hover:bg-elevated border border-transparent'}`}
                      >
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={() => togglePid(ind.pid)}
                          className="accent-teal-500 rounded flex-shrink-0"
                        />
                        <span className="text-primary font-medium truncate">
                          {ind.name || `Session ${ind.pid}`}
                        </span>
                        <span className="text-secondary font-mono truncate ml-auto">
                          {shortCwd(ind.cwd)}
                        </span>
                      </label>
                    </li>
                  )
                })}
              </ul>
            </>
          )}

          {/* -------------------------------------------------------------- */}
          {/* Command input                                                   */}
          {/* -------------------------------------------------------------- */}
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && command.trim() !== '') {
                  if (selectedPids.size > 0) {
                    void handleSendSelected()
                  } else {
                    void handleSendAll()
                  }
                }
              }}
              placeholder="claude"
              className="flex-1 px-2.5 py-1.5 rounded-lg text-xs font-mono
                         bg-app text-primary placeholder-muted
                         border border-border focus:border-acc-teal
                         focus:outline-none focus:ring-1 focus:ring-acc-teal"
            />
          </div>

          {/* -------------------------------------------------------------- */}
          {/* Action buttons                                                  */}
          {/* -------------------------------------------------------------- */}
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void handleSendSelected()}
              disabled={
                selectedPids.size === 0 ||
                command.trim() === '' ||
                sendState === 'loading'
              }
              className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg
                         text-xs font-medium transition-colors
                         bg-acc-teal/80 text-white hover:bg-acc-teal
                         disabled:opacity-40 disabled:cursor-not-allowed
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal"
            >
              {sendState === 'loading' ? (
                <SpinnerIcon />
              ) : (
                <>Send to Selected ({selectedPids.size})</>
              )}
            </button>

            <button
              type="button"
              onClick={() => void handleSendAll()}
              disabled={
                command.trim() === '' ||
                sendState === 'loading' ||
                indicators.length === 0
              }
              className="inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg
                         text-xs font-medium transition-colors
                         bg-border-m text-primary hover:bg-border
                         border border-border hover:border-muted
                         disabled:opacity-40 disabled:cursor-not-allowed
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal"
            >
              {sendState === 'loading' ? (
                <SpinnerIcon />
              ) : (
                <>Send to All ({indicators.length})</>
              )}
            </button>
          </div>

          {/* -------------------------------------------------------------- */}
          {/* Results area                                                    */}
          {/* -------------------------------------------------------------- */}
          {results.length > 0 && (
            <div className="space-y-1 pt-1">
              <h4 className="text-[10px] font-semibold uppercase tracking-wider text-secondary">
                Results
              </h4>
              <ul className="space-y-1 max-h-36 overflow-y-auto">
                {results.map((r, idx) => (
                  <li
                    key={r.pid === 0 ? `err-${idx}` : r.pid}
                    className={`flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-xs
                                ${r.ok ? 'bg-green-500/10 border border-green-500/20' : 'bg-red-500/10 border border-red-500/20'}`}
                  >
                    <span
                      className={`flex-shrink-0 w-1.5 h-1.5 rounded-full ${r.ok ? 'bg-green-400' : 'bg-red-400'}`}
                    />
                    <span className="text-primary font-medium truncate">
                      {r.name}
                    </span>
                    <span
                      className={`ml-auto truncate ${r.ok ? 'text-green-400' : 'text-red-400'}`}
                    >
                      {r.outcome}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
