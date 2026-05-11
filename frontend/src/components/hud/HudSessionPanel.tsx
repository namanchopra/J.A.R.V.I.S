import '../../lib/hud-theme'

// ---------------------------------------------------------------------------
// Types (inline — no import from wailsjs/go/models)
// ---------------------------------------------------------------------------

interface SessionIndicator {
  pid: number
  sessionId: string
  cwd: string
  name: string
  startedAt: number
  hasQuestion: boolean
  lastActivity: string
  tokensUsed: number
}

interface DashboardStats {
  total: number
  running: number
  pending: number
  done: number
  failed: number
  needsInput: number
}

interface HudSessionPanelProps {
  sessions: SessionIndicator[]
  stats: DashboardStats
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Extract the last path segment as a display name. */
function basename(cwd: string): string {
  return cwd.split('/').pop() || cwd
}

/** Format a millisecond duration into a compact string (e.g., "2m", "1h 30m"). */
function formatDuration(ms: number): string {
  if (ms < 0) return '0m'

  const totalMinutes = Math.floor(ms / 60_000)
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60

  if (hours > 0) {
    return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`
  }
  return `${Math.max(totalMinutes, 1)}m`
}

// ---------------------------------------------------------------------------
// Max visible rows before scrolling
// ---------------------------------------------------------------------------

const MAX_VISIBLE_ROWS = 6

// ---------------------------------------------------------------------------
// HudSessionPanel
// ---------------------------------------------------------------------------

export function HudSessionPanel({ sessions, stats }: HudSessionPanelProps): React.ReactElement {
  const now = Date.now()

  return (
    <div className="hud-panel flex flex-col overflow-hidden">
      {/* ---- Header ---- */}
      <div className="flex items-center gap-2">
        <span className="hud-label">SESSIONS</span>
        {stats.running > 0 && (
          <span className="text-[10px]" style={{ color: 'var(--hud-cyan)' }}>
            {stats.running} running
          </span>
        )}
      </div>

      {/* ---- Session list ---- */}
      <div
        className="mt-2 flex-1 overflow-y-auto"
        style={{ maxHeight: `${MAX_VISIBLE_ROWS * 28}px` }}
      >
        {sessions.length === 0 ? (
          <p className="hud-text-dim text-xs text-center mt-4">
            No active sessions
          </p>
        ) : (
          <ul className="space-y-1">
            {sessions.map((s) => {
              const elapsed = now - s.startedAt
              return (
                <li
                  key={s.pid}
                  className="flex items-center gap-2 px-1 py-1 rounded"
                >
                  {/* Status dot */}
                  <span
                    className={`inline-block h-2 w-2 rounded-full flex-shrink-0${
                      s.hasQuestion ? ' animate-pulse' : ''
                    }`}
                    style={{
                      background: s.hasQuestion
                        ? 'var(--hud-amber)'
                        : 'var(--hud-green)',
                    }}
                  />

                  {/* Session name (basename of cwd) */}
                  <span className="hud-text text-xs truncate flex-1">
                    {basename(s.cwd)}
                  </span>

                  {/* Duration */}
                  <span className="hud-text-dim text-xs flex-shrink-0 tabular-nums">
                    {formatDuration(elapsed)}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </div>
  )
}
