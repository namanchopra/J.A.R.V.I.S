// ---------------------------------------------------------------------------
// Session helpers -- shared by SessionsView, SessionRow, SessionDetail
// ---------------------------------------------------------------------------

const AGENT_LABELS: Record<string, string> = {
  'claude-code': 'Claude Code',
  kiro: 'Kiro',
  gemini: 'Gemini',
  codex: 'Codex',
  aider: 'Aider',
  other: 'Other',
}

export function agentLabel(agentType: string): string {
  return AGENT_LABELS[agentType] ?? agentType
}

/** Extract the last path segment (directory name) from a repo path. */
export function repoBasename(repoPath: string): string {
  const segments = repoPath.replace(/\\/g, '/').split('/').filter(Boolean)
  return segments[segments.length - 1] ?? repoPath
}

/** Truncate a string to a max length with ellipsis. */
export function truncate(text: string, maxLen: number): string {
  if (text.length <= maxLen) return text
  return text.slice(0, maxLen - 1) + '\u2026'
}

/** Format a Go time.Time (ISO string) to a readable date string. */
export function formatTimestamp(value: unknown): string {
  if (value === null || value === undefined || value === '') return '--'
  const date = new Date(String(value))
  if (isNaN(date.getTime())) return '--'
  return date.toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    hour12: true,
  })
}

/** Compute a human-readable duration between two timestamps. */
export function formatDuration(start: unknown, end: unknown): string {
  if (start === null || start === undefined || start === '') return '--'
  const startMs = new Date(String(start)).getTime()
  if (isNaN(startMs)) return '--'

  let endMs: number
  if (end === null || end === undefined || end === '') {
    endMs = Date.now()
  } else {
    endMs = new Date(String(end)).getTime()
    if (isNaN(endMs)) endMs = Date.now()
  }

  const diffS = Math.max(0, Math.floor((endMs - startMs) / 1000))
  if (diffS < 60) return `${diffS}s`
  const mins = Math.floor(diffS / 60)
  const secs = diffS % 60
  if (mins < 60) return `${mins}m ${secs}s`
  const hrs = Math.floor(mins / 60)
  const remMins = mins % 60
  return `${hrs}h ${remMins}m`
}

// ---------------------------------------------------------------------------
// Status styling helpers
// ---------------------------------------------------------------------------

export type SessionStatus = 'running' | 'stopped' | 'completed' | 'failed'

export function sessionStatusBg(status: string): string {
  const map: Record<SessionStatus, string> = {
    running: 'bg-amber-400',
    stopped: 'bg-gray-400',
    completed: 'bg-green-400',
    failed: 'bg-red-400',
  }
  return map[status as SessionStatus] ?? 'bg-gray-400'
}

export function sessionStatusBadgeBg(status: string): string {
  const map: Record<SessionStatus, string> = {
    running: 'bg-amber-400/10 text-amber-400',
    stopped: 'bg-gray-400/10 text-secondary',
    completed: 'bg-green-400/10 text-green-400',
    failed: 'bg-red-400/10 text-red-400',
  }
  return map[status as SessionStatus] ?? 'bg-gray-400/10 text-secondary'
}
