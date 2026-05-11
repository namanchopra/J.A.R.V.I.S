// ---------------------------------------------------------------------------
// HistoryView -- Browse past recorded sessions with snapshot timelines.
// Lists sessions in reverse chronological order. Click to expand and view
// a vertical timeline of snapshots with activity state, tool calls, and
// collapsible terminal text previews.
// ---------------------------------------------------------------------------

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  GetSessionRecording,
  ListRecordedSessions,
} from '../../wailsjs/go/main/App'
import { recording } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type DateFilter = 'all' | 'today' | 'week' | 'month'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const DATE_FILTER_OPTIONS: Array<{ label: string; value: DateFilter }> = [
  { label: 'All time', value: 'all' },
  { label: 'Today', value: 'today' },
  { label: 'This week', value: 'week' },
  { label: 'This month', value: 'month' },
]

/** Activity state to dot color. */
const ACTIVITY_DOT_COLORS: Record<string, string> = {
  idle: 'bg-[#4a6278]',
  thinking: 'bg-[#00e5ff]',
  tool_use: 'bg-[#00ff88]',
  waiting: 'bg-[#ffb800]',
}

const ACTIVITY_DOT_GLOW: Record<string, string> = {
  idle: '',
  thinking: 'shadow-[0_0_6px_rgba(0,229,255,0.5)]',
  tool_use: 'shadow-[0_0_6px_rgba(0,255,136,0.5)]',
  waiting: 'shadow-[0_0_6px_rgba(255,184,0,0.5)]',
}

const ACTIVITY_TEXT_COLORS: Record<string, string> = {
  idle: 'text-[#4a6278]',
  thinking: 'text-[#00e5ff]',
  tool_use: 'text-[#00ff88]',
  waiting: 'text-[#ffb800]',
}

const TERMINAL_PREVIEW_LINES = 10

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatDate(raw: unknown): string {
  if (!raw) return ''
  const date = new Date(String(raw))
  if (isNaN(date.getTime())) return ''
  return date.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

function formatTime(raw: unknown): string {
  if (!raw) return ''
  const date = new Date(String(raw))
  if (isNaN(date.getTime())) return ''
  return date.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function startOfDay(date: Date): Date {
  const d = new Date(date)
  d.setHours(0, 0, 0, 0)
  return d
}

function passesDateFilter(raw: unknown, filter: DateFilter): boolean {
  if (filter === 'all') return true
  if (!raw) return false
  const date = new Date(String(raw))
  if (isNaN(date.getTime())) return false

  const now = new Date()
  const today = startOfDay(now)

  switch (filter) {
    case 'today':
      return date >= today
    case 'week': {
      const weekAgo = new Date(today)
      weekAgo.setDate(weekAgo.getDate() - 7)
      return date >= weekAgo
    }
    case 'month': {
      const monthAgo = new Date(today)
      monthAgo.setMonth(monthAgo.getMonth() - 1)
      return date >= monthAgo
    }
    default:
      return true
  }
}

function lastNLines(text: string, n: number): string {
  if (!text) return ''
  const lines = text.split('\n')
  return lines.slice(-n).join('\n')
}

function shortenCwd(cwd: string): string {
  if (!cwd) return ''
  const home = cwd.replace(/^\/Users\/[^/]+/, '~').replace(/^\/home\/[^/]+/, '~')
  const parts = home.split('/')
  if (parts.length <= 3) return home
  return parts.slice(0, 1).join('/') + '/.../' + parts.slice(-2).join('/')
}

function activityLabel(activity: string): string {
  if (!activity) return 'Unknown'
  return activity
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase())
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function HistoryView(): React.ReactElement {
  const [sessions, setSessions] = useState<recording.RecordingSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [dateFilter, setDateFilter] = useState<DateFilter>('all')
  const [expandedSessionId, setExpandedSessionId] = useState<string | null>(null)
  const [snapshots, setSnapshots] = useState<recording.Snapshot[]>([])
  const [snapshotsLoading, setSnapshotsLoading] = useState(false)
  const [snapshotsError, setSnapshotsError] = useState<string | null>(null)
  const [collapsedPreviews, setCollapsedPreviews] = useState<Set<number>>(
    () => new Set(),
  )

  const mountedRef = useRef(true)

  // -----------------------------------------------------------------------
  // Fetch session list
  // -----------------------------------------------------------------------

  const fetchSessions = useCallback(async () => {
    try {
      const result = await ListRecordedSessions()
      if (mountedRef.current) {
        setSessions(result ?? [])
        setError(null)
      }
    } catch (err: unknown) {
      if (mountedRef.current) {
        const message = err instanceof Error ? err.message : String(err)
        setError(message)
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    mountedRef.current = true
    void fetchSessions()
    return () => {
      mountedRef.current = false
    }
  }, [fetchSessions])

  // -----------------------------------------------------------------------
  // Fetch snapshots for expanded session
  // -----------------------------------------------------------------------

  const fetchSnapshots = useCallback(async (sessionId: string) => {
    setSnapshotsLoading(true)
    setSnapshotsError(null)
    setSnapshots([])
    setCollapsedPreviews(new Set())
    try {
      const result = await GetSessionRecording(sessionId)
      if (mountedRef.current) {
        setSnapshots(result ?? [])
      }
    } catch (err: unknown) {
      if (mountedRef.current) {
        const message = err instanceof Error ? err.message : String(err)
        setSnapshotsError(message)
      }
    } finally {
      if (mountedRef.current) {
        setSnapshotsLoading(false)
      }
    }
  }, [])

  // -----------------------------------------------------------------------
  // Expand / collapse a session row
  // -----------------------------------------------------------------------

  const handleToggleSession = useCallback(
    (sessionId: string) => {
      if (expandedSessionId === sessionId) {
        setExpandedSessionId(null)
        setSnapshots([])
        setSnapshotsError(null)
        return
      }
      setExpandedSessionId(sessionId)
      void fetchSnapshots(sessionId)
    },
    [expandedSessionId, fetchSnapshots],
  )

  const handleTogglePreview = useCallback((idx: number) => {
    setCollapsedPreviews((prev) => {
      const next = new Set(prev)
      if (next.has(idx)) {
        next.delete(idx)
      } else {
        next.add(idx)
      }
      return next
    })
  }, [])

  // -----------------------------------------------------------------------
  // Filter + sort
  // -----------------------------------------------------------------------

  const filtered = sessions
    .filter((s) => {
      if (searchQuery.trim() !== '') {
        const q = searchQuery.toLowerCase()
        const nameMatch = (s.name ?? '').toLowerCase().includes(q)
        const cwdMatch = (s.cwd ?? '').toLowerCase().includes(q)
        if (!nameMatch && !cwdMatch) return false
      }
      return passesDateFilter(s.startedAt, dateFilter)
    })
    .sort((a, b) => {
      const aTime = new Date(String(a.startedAt)).getTime() || 0
      const bTime = new Date(String(b.startedAt)).getTime() || 0
      return bTime - aTime
    })

  // -----------------------------------------------------------------------
  // Render
  // -----------------------------------------------------------------------

  return (
    <div className="flex-1 flex flex-col min-h-0 bg-[#0a0e1a]">
      {/* Header */}
      <header className="h-12 flex-shrink-0 flex items-center gap-3 px-5 border-b border-[rgba(0,229,255,0.15)] bg-[#111827]">
        <h1 className="text-base font-bold tracking-wide text-[#e8f4ff]">
          Session History
        </h1>
        {!loading && (
          <span className="inline-flex items-center justify-center px-2 py-0.5 rounded-full text-[11px] font-mono font-medium bg-[rgba(0,229,255,0.1)] text-[#00e5ff] border border-[rgba(0,229,255,0.2)]">
            {filtered.length}
          </span>
        )}
      </header>

      {/* Filters */}
      <div className="flex-shrink-0 flex items-center gap-3 px-5 py-3 border-b border-[rgba(0,229,255,0.1)] bg-[#111827]/60">
        {/* Text search */}
        <div className="relative flex-1 max-w-xs">
          <SearchIcon />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search by name or path..."
            aria-label="Search recorded sessions"
            className="sci-fi w-full pl-8 pr-3 py-1.5 rounded-md text-sm"
          />
        </div>

        {/* Date range filter */}
        <select
          value={dateFilter}
          onChange={(e) => setDateFilter(e.target.value as DateFilter)}
          aria-label="Filter by date range"
          className="sci-fi rounded-md text-sm px-2 py-1.5"
        >
          {DATE_FILTER_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto">
        <div className="p-5 space-y-2">
          {/* Loading */}
          {loading && (
            <div className="flex items-center justify-center py-12">
              <div className="w-5 h-5 border-2 border-[rgba(0,229,255,0.2)] border-t-[#00e5ff] rounded-full animate-spin" />
            </div>
          )}

          {/* Error */}
          {!loading && error !== null && (
            <div
              role="alert"
              className="text-sm text-[#ff4757] bg-[rgba(255,71,87,0.1)] border border-[rgba(255,71,87,0.2)] rounded-md px-3 py-2"
            >
              {error}
            </div>
          )}

          {/* Empty state */}
          {!loading && error === null && filtered.length === 0 && (
            <div className="text-center py-12">
              <HistoryEmptyIcon />
              <p className="mt-3 text-sm text-[#4a6278] font-mono">
                {sessions.length === 0
                  ? 'No recorded sessions yet'
                  : 'No sessions match your filters'}
              </p>
            </div>
          )}

          {/* Session rows */}
          {filtered.map((session) => {
            const isExpanded = expandedSessionId === session.sessionId
            return (
              <div
                key={session.sessionId}
                className={`holo-panel overflow-hidden transition-all ${isExpanded ? 'glow-active' : ''}`}
              >
                {/* Session header row */}
                <button
                  type="button"
                  onClick={() => handleToggleSession(session.sessionId)}
                  aria-expanded={isExpanded}
                  className="w-full text-left flex items-center gap-3 px-4 py-3
                             hover:bg-[rgba(0,229,255,0.05)] transition-colors
                             focus:outline-none focus-visible:ring-2
                             focus-visible:ring-inset focus-visible:ring-[#00e5ff]"
                >
                  {/* Chevron */}
                  <ChevronIcon expanded={isExpanded} />

                  {/* Name + CWD */}
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-[#e8f4ff] truncate">
                      {session.name || session.sessionId}
                    </p>
                    <p className="text-xs font-mono text-[#4a6278] truncate mt-0.5">
                      {shortenCwd(session.cwd)}
                    </p>
                  </div>

                  {/* Date */}
                  <span className="flex-shrink-0 text-xs font-mono text-[#8ba4b8]">
                    {formatDate(session.startedAt)}
                  </span>

                  {/* Snapshot count badge */}
                  <span className="flex-shrink-0 inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-mono font-medium bg-[rgba(0,229,255,0.1)] text-[#00e5ff] border border-[rgba(0,229,255,0.15)]">
                    <SnapshotIcon />
                    {session.snapshotCount}
                  </span>
                </button>

                {/* Expanded snapshot timeline */}
                {isExpanded && (
                  <div className="border-t border-[rgba(0,229,255,0.15)] px-4 py-4 bg-[#0a0e1a]/50">
                    <SnapshotTimeline
                      snapshots={snapshots}
                      loading={snapshotsLoading}
                      error={snapshotsError}
                      collapsedPreviews={collapsedPreviews}
                      onTogglePreview={handleTogglePreview}
                    />
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// SnapshotTimeline -- vertical timeline of snapshots (dot + line style)
// ---------------------------------------------------------------------------

interface SnapshotTimelineProps {
  snapshots: recording.Snapshot[]
  loading: boolean
  error: string | null
  collapsedPreviews: Set<number>
  onTogglePreview: (idx: number) => void
}

function SnapshotTimeline({
  snapshots,
  loading,
  error,
  collapsedPreviews,
  onTogglePreview,
}: SnapshotTimelineProps): React.ReactElement {
  if (loading) {
    return (
      <div className="flex items-center justify-center py-6">
        <div className="w-4 h-4 border-2 border-[rgba(0,229,255,0.2)] border-t-[#00e5ff] rounded-full animate-spin" />
      </div>
    )
  }

  if (error !== null) {
    return (
      <div
        role="alert"
        className="text-sm text-[#ff4757] bg-[rgba(255,71,87,0.1)] border border-[rgba(255,71,87,0.2)] rounded-md px-3 py-2"
      >
        {error}
      </div>
    )
  }

  if (snapshots.length === 0) {
    return (
      <p className="text-sm text-[#4a6278] text-center py-4 font-mono">
        No snapshots in this recording
      </p>
    )
  }

  return (
    <div className="relative">
      {/* Vertical line -- cyan-tinted */}
      <div
        className="absolute left-[7px] top-2 bottom-2 w-px bg-[rgba(0,229,255,0.2)]"
        aria-hidden="true"
      />

      <ul className="space-y-4" role="list" aria-label="Snapshot timeline">
        {snapshots.map((snap, idx) => {
          const dotColor = ACTIVITY_DOT_COLORS[snap.activity] ?? 'bg-[#4a6278]'
          const dotGlow = ACTIVITY_DOT_GLOW[snap.activity] ?? ''
          const textColor =
            ACTIVITY_TEXT_COLORS[snap.activity] ?? 'text-[#4a6278]'
          const toolCalls = snap.toolCalls ?? []
          const hasTerminal = (snap.terminalText ?? '').trim().length > 0
          const previewCollapsed = collapsedPreviews.has(idx)

          return (
            <li key={idx} className="relative flex gap-3 pl-0">
              {/* Dot with glow */}
              <span
                className={`relative z-10 mt-1.5 h-[10px] w-[10px] flex-shrink-0 rounded-full border-2 border-[#0a0e1a] ${dotColor} ${dotGlow}`}
                aria-hidden="true"
              />

              {/* Content */}
              <div className="flex-1 min-w-0 pb-1">
                {/* Timestamp + Activity */}
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="text-xs font-mono text-[#8ba4b8]">
                    {formatTime(snap.timestamp)}
                  </span>
                  <span
                    className={`text-xs font-medium ${textColor}`}
                  >
                    {activityLabel(snap.activity)}
                  </span>
                </div>

                {/* Tool calls */}
                {toolCalls.length > 0 && (
                  <div className="flex flex-wrap gap-1.5 mt-1.5">
                    {toolCalls.map((tool, tIdx) => (
                      <span
                        key={tIdx}
                        className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono bg-[rgba(0,229,255,0.08)] text-[#00e5ff] border border-[rgba(0,229,255,0.15)]"
                      >
                        {tool}
                      </span>
                    ))}
                  </div>
                )}

                {/* Terminal text preview (collapsible) */}
                {hasTerminal && (
                  <div className="mt-2">
                    <button
                      type="button"
                      onClick={() => onTogglePreview(idx)}
                      className="flex items-center gap-1 text-[11px] text-[#4a6278]
                                 hover:text-[#00e5ff] transition-colors
                                 focus:outline-none focus-visible:ring-1
                                 focus-visible:ring-[#00e5ff] rounded"
                    >
                      <ChevronIcon expanded={!previewCollapsed} />
                      <span className="font-mono">Terminal output</span>
                    </button>
                    {!previewCollapsed && (
                      <pre className="mt-1.5 p-2.5 rounded-md bg-[#0a0e1a] border border-[rgba(0,229,255,0.1)] text-[11px] leading-relaxed text-[#8ba4b8] font-mono overflow-x-auto max-h-48 whitespace-pre-wrap break-all">
                        {lastNLines(snap.terminalText, TERMINAL_PREVIEW_LINES)}
                      </pre>
                    )}
                  </div>
                )}
              </div>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Inline icons (no external dependency)
// ---------------------------------------------------------------------------

function ChevronIcon({
  expanded,
}: {
  expanded: boolean
}): React.ReactElement {
  return (
    <svg
      className={`w-3.5 h-3.5 flex-shrink-0 text-[#4a6278] transition-transform ${expanded ? 'rotate-90' : ''}`}
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden="true"
    >
      <path
        fillRule="evenodd"
        d="M7.21 14.77a.75.75 0 01.02-1.06L11.168 10 7.23 6.29a.75.75 0 111.04-1.08l4.5 4.25a.75.75 0 010 1.08l-4.5 4.25a.75.75 0 01-1.06-.02z"
        clipRule="evenodd"
      />
    </svg>
  )
}

function SearchIcon(): React.ReactElement {
  return (
    <svg
      className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-[#4a6278] pointer-events-none"
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden="true"
    >
      <path
        fillRule="evenodd"
        d="M9 3.5a5.5 5.5 0 100 11 5.5 5.5 0 000-11zM2 9a7 7 0 1112.452 4.391l3.328 3.329a.75.75 0 11-1.06 1.06l-3.329-3.328A7 7 0 012 9z"
        clipRule="evenodd"
      />
    </svg>
  )
}

function SnapshotIcon(): React.ReactElement {
  return (
    <svg
      className="w-3 h-3"
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M10 2a.75.75 0 01.75.75v1.5a.75.75 0 01-1.5 0v-1.5A.75.75 0 0110 2zm0 13a.75.75 0 01.75.75v1.5a.75.75 0 01-1.5 0v-1.5A.75.75 0 0110 15zm-8-5a.75.75 0 01.75-.75h1.5a.75.75 0 010 1.5h-1.5A.75.75 0 012 10zm13 0a.75.75 0 01.75-.75h1.5a.75.75 0 010 1.5h-1.5A.75.75 0 0115 10zM10 7a3 3 0 100 6 3 3 0 000-6z" />
    </svg>
  )
}

function HistoryEmptyIcon(): React.ReactElement {
  return (
    <svg
      className="mx-auto w-10 h-10 text-[#4a6278]"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </svg>
  )
}
