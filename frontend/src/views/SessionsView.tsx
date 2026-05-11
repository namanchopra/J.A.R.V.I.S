import { useCallback, useEffect, useRef, useState } from 'react'
import {
  DeleteSession,
  GetSession,
  ListSessions,
  ResumeSession,
  StopSession,
} from '../../wailsjs/go/main/App'
import { model } from '../../wailsjs/go/models'
import { SessionDetail } from '../components/SessionDetail'
import { SessionLauncher } from '../components/SessionLauncher'
import { SessionRow } from '../components/SessionRow'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 3000

const STATUS_OPTIONS: Array<{ label: string; value: string }> = [
  { label: 'All', value: '' },
  { label: 'Running', value: 'running' },
  { label: 'Stopped', value: 'stopped' },
  { label: 'Completed', value: 'completed' },
  { label: 'Failed', value: 'failed' },
]

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface SessionsViewProps {
  onSelectSession?: (id: string) => void
  initialSessionId?: string | null
  onSessionSelected?: () => void
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function SessionsView({
  onSelectSession,
  initialSessionId,
  onSessionSelected,
}: SessionsViewProps): React.ReactElement {
  const [sessions, setSessions] = useState<model.Session[]>([])
  const [statusFilter, setStatusFilter] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(null)
  const [selectedSession, setSelectedSession] = useState<model.Session | null>(null)
  const [showLauncher, setShowLauncher] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // -----------------------------------------------------------------------
  // Pick up initialSessionId from parent (e.g. dashboard click)
  // -----------------------------------------------------------------------

  useEffect(() => {
    if (initialSessionId != null && initialSessionId !== '') {
      setSelectedSessionId(initialSessionId)
      onSessionSelected?.()
    }
  }, [initialSessionId, onSessionSelected])

  // -----------------------------------------------------------------------
  // Fetch sessions
  // -----------------------------------------------------------------------

  const fetchSessions = useCallback(async (filter: string) => {
    try {
      const result = await ListSessions(filter)
      setSessions(result ?? [])
      setError(null)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err)
      setError(message)
    }
  }, [])

  // Initial fetch + polling
  useEffect(() => {
    void fetchSessions(statusFilter)
    intervalRef.current = setInterval(() => {
      void fetchSessions(statusFilter)
    }, POLL_INTERVAL_MS)
    return () => {
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current)
      }
    }
  }, [fetchSessions, statusFilter])

  // -----------------------------------------------------------------------
  // Fetch selected session detail (refresh on selection + polling)
  // -----------------------------------------------------------------------

  const fetchSelectedSession = useCallback(async (id: string) => {
    try {
      const session = await GetSession(id)
      setSelectedSession(session)
    } catch (err) {
      console.warn('Failed to fetch session:', err)
      setSelectedSession(null)
      setSelectedSessionId(null)
    }
  }, [])

  useEffect(() => {
    if (selectedSessionId === null) {
      setSelectedSession(null)
      return
    }
    void fetchSelectedSession(selectedSessionId)
    const detailInterval = setInterval(() => {
      void fetchSelectedSession(selectedSessionId)
    }, POLL_INTERVAL_MS)
    return () => clearInterval(detailInterval)
  }, [selectedSessionId, fetchSelectedSession])

  // -----------------------------------------------------------------------
  // Session actions
  // -----------------------------------------------------------------------

  const handleSelectSession = useCallback(
    (id: string) => {
      setSelectedSessionId(id)
      setActionError(null)
      onSelectSession?.(id)
    },
    [onSelectSession],
  )

  const handleStop = useCallback(
    async (id: string) => {
      setActionError(null)
      try {
        await StopSession(id)
        void fetchSessions(statusFilter)
        if (selectedSessionId === id) {
          void fetchSelectedSession(id)
        }
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err)
        setActionError(message)
      }
    },
    [statusFilter, selectedSessionId, fetchSessions, fetchSelectedSession],
  )

  const handleResume = useCallback(
    async (id: string) => {
      setActionError(null)
      try {
        await ResumeSession(id)
        void fetchSessions(statusFilter)
        if (selectedSessionId === id) {
          void fetchSelectedSession(id)
        }
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err)
        setActionError(message)
      }
    },
    [statusFilter, selectedSessionId, fetchSessions, fetchSelectedSession],
  )

  const handleDelete = useCallback(
    async (id: string) => {
      setActionError(null)
      try {
        await DeleteSession(id)
        if (selectedSessionId === id) {
          setSelectedSessionId(null)
          setSelectedSession(null)
        }
        void fetchSessions(statusFilter)
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err)
        setActionError(message)
      }
    },
    [statusFilter, selectedSessionId, fetchSessions],
  )

  const handleLaunched = useCallback(
    (sessionId: string) => {
      void fetchSessions(statusFilter)
      setSelectedSessionId(sessionId)
    },
    [fetchSessions, statusFilter],
  )

  // -----------------------------------------------------------------------
  // Sort sessions: running first, then by updatedAt descending
  // -----------------------------------------------------------------------

  const sortedSessions = [...sessions].sort((a, b) => {
    const aRunning = a.status === 'running' ? 0 : 1
    const bRunning = b.status === 'running' ? 0 : 1
    if (aRunning !== bRunning) return aRunning - bRunning
    const aTime = new Date(String(a.updatedAt)).getTime() || 0
    const bTime = new Date(String(b.updatedAt)).getTime() || 0
    return bTime - aTime
  })

  // -----------------------------------------------------------------------
  // Render
  // -----------------------------------------------------------------------

  return (
    <div className="flex-1 flex min-h-0 bg-[var(--bg-app)]">
      {/* Session list sidebar */}
      <aside className="w-80 flex-shrink-0 bg-[var(--bg-surface)] border-r border-[rgba(0,229,255,0.15)] flex flex-col">
        {/* Header */}
        <div className="px-4 py-3 border-b border-[rgba(0,229,255,0.15)] space-y-2">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold uppercase tracking-wider text-[var(--accent-blue)]">
              Sessions
            </h2>
            <button
              type="button"
              onClick={() => setShowLauncher(true)}
              aria-label="New session"
              className="flex items-center justify-center gap-1.5 px-2.5 py-1.5 rounded-md
                         bg-[rgba(0,229,255,0.15)] text-[var(--accent-blue)] text-xs font-medium
                         border border-[rgba(0,229,255,0.3)]
                         hover:bg-[rgba(0,229,255,0.25)] hover:shadow-[0_0_12px_rgba(0,229,255,0.2)]
                         transition-all duration-200 focus:outline-none
                         focus-visible:ring-2 focus-visible:ring-[var(--accent-blue)]"
            >
              <span className="text-sm leading-none">+</span>
              New
            </button>
          </div>

          {/* Status filter */}
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            aria-label="Filter sessions by status"
            className="sci-fi w-full rounded-md text-sm px-2 py-1.5"
          >
            {STATUS_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </div>

        {/* Session list */}
        <div className="flex-1 overflow-y-auto">
          {error !== null && (
            <div className="px-4 py-3 text-sm text-[var(--accent-red)]">{error}</div>
          )}

          {sessions.length === 0 && error === null && (
            <div className="flex-1 flex items-center justify-center px-4 py-8">
              <p className="text-sm text-[var(--text-muted)]">No sessions yet</p>
            </div>
          )}

          {sortedSessions.map((session) => (
            <SessionRow
              key={session.id}
              session={session}
              isSelected={session.id === selectedSessionId}
              onSelect={handleSelectSession}
              onStop={handleStop}
              onResume={handleResume}
              onDelete={handleDelete}
            />
          ))}
        </div>
      </aside>

      {/* Detail panel */}
      <main className="flex-1 flex flex-col min-w-0 bg-[var(--bg-app)]">
        {actionError !== null && (
          <div
            role="alert"
            className="mx-4 mt-3 text-sm text-[var(--accent-red)]
                       bg-[rgba(255,71,87,0.1)] border border-[rgba(255,71,87,0.25)]
                       rounded-md px-3 py-2"
          >
            {actionError}
          </div>
        )}

        {selectedSession !== null ? (
          <SessionDetail
            session={selectedSession}
            onStop={handleStop}
            onResume={handleResume}
            onDelete={handleDelete}
          />
        ) : (
          <>
            <header className="relative h-12 flex-shrink-0 flex items-center px-5 border-b border-[rgba(0,229,255,0.15)] bg-[var(--bg-elevated)]">
              <h1 className="text-base font-bold tracking-wide text-[var(--accent-blue)]">
                Sessions
              </h1>
              <div className="absolute bottom-0 left-5 w-20 h-[1px] bg-gradient-to-r from-[var(--accent-blue)] to-transparent" />
            </header>
            <div className="flex-1 flex items-center justify-center">
              <div className="text-center holo-panel px-8 py-10">
                {/* Decorative ring */}
                <div
                  className="mx-auto mb-4 w-16 h-16 rounded-full border border-[rgba(0,229,255,0.2)]
                             flex items-center justify-center"
                  style={{ boxShadow: '0 0 20px rgba(0, 229, 255, 0.1), inset 0 0 20px rgba(0, 229, 255, 0.05)' }}
                >
                  <svg
                    className="w-7 h-7 text-[var(--accent-blue)] opacity-60"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    aria-hidden="true"
                  >
                    <circle cx="12" cy="12" r="3" />
                    <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
                  </svg>
                </div>
                <p className="text-sm text-[var(--text-muted)]">
                  Select a session to view details
                </p>
                <button
                  type="button"
                  onClick={() => setShowLauncher(true)}
                  className="mt-3 text-sm text-[var(--accent-blue)] hover:text-[var(--accent-blue)]/80
                             transition-colors focus:outline-none focus-visible:ring-2
                             focus-visible:ring-[var(--accent-blue)] rounded"
                >
                  or launch a new session
                </button>
              </div>
            </div>
          </>
        )}
      </main>

      {/* Launcher modal */}
      {showLauncher && (
        <SessionLauncher
          onClose={() => setShowLauncher(false)}
          onLaunched={handleLaunched}
        />
      )}
    </div>
  )
}
