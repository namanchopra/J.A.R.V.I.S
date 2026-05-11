import { useCallback, useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { GetRepoInfo } from '../../wailsjs/go/main/App'
import { git } from '../../wailsjs/go/models'
import { model } from '../../wailsjs/go/models'
import { type TaskStatus, statusBgClass } from '../lib/colors'
import { sortTasks } from '../lib/utils'
import { MiniOutput } from './MiniOutput'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const AGENT_LABELS: Record<string, string> = {
  'claude-code': 'Claude',
  kiro: 'Kiro',
  gemini: 'Gemini',
  codex: 'Codex',
  aider: 'Aider',
  other: 'Other',
}

function agentLabel(agentType: string): string {
  return AGENT_LABELS[agentType] ?? agentType
}

/** Returns the basename of a path. */
function repoBasename(repoPath: string): string {
  const segments = repoPath.replace(/\\/g, '/').split('/').filter(Boolean)
  return segments[segments.length - 1] ?? repoPath
}

/** Relative time from a date string (e.g., "3m ago", "1h ago"). */
function timeAgo(dateStr: unknown): string {
  if (dateStr === null || dateStr === undefined || dateStr === '') return ''
  const date = new Date(String(dateStr))
  if (isNaN(date.getTime())) return ''

  const seconds = Math.floor((Date.now() - date.getTime()) / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

/** Truncate a string to a max length. */
function truncate(str: string, maxLen: number): string {
  if (str.length <= maxLen) return str
  return str.slice(0, maxLen - 1) + '\u2026'
}

// ---------------------------------------------------------------------------
// Left accent border color based on highest-priority status
// ---------------------------------------------------------------------------

/** Priority: needs-input > running > failed > pending > done */
function groupAccentColor(tasks: model.Task[]): string {
  const statuses = new Set(tasks.map((t) => t.status))
  if (statuses.has('needs-input')) return 'border-l-teal-500'
  if (statuses.has('running')) return 'border-l-amber-500'
  if (statuses.has('failed')) return 'border-l-red-500'
  if (statuses.has('pending')) return 'border-l-gray-500'
  return 'border-l-green-500'
}

// ---------------------------------------------------------------------------
// Git info cache -- shared across all RepoGroup instances via module scope
// ---------------------------------------------------------------------------

const GIT_REFRESH_MS = 30_000

interface CachedGitInfo {
  data: git.RepoInfo
  fetchedAt: number
}

const gitInfoCache = new Map<string, CachedGitInfo>()

async function getCachedRepoInfo(repoPath: string): Promise<git.RepoInfo | null> {
  const cached = gitInfoCache.get(repoPath)
  const now = Date.now()

  if (cached && now - cached.fetchedAt < GIT_REFRESH_MS) {
    return cached.data
  }

  try {
    const info = await GetRepoInfo(repoPath)
    gitInfoCache.set(repoPath, { data: info, fetchedAt: now })
    return info
  } catch (err) {
    console.warn('Failed to fetch repo info:', err)
    return cached?.data ?? null
  }
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function ChevronIcon({ open }: { open: boolean }): React.ReactElement {
  return (
    <svg
      className={`w-4 h-4 text-secondary transition-transform duration-150 ${open ? 'rotate-90' : ''}`}
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

function BranchIcon(): React.ReactElement {
  return (
    <svg
      className="w-3.5 h-3.5"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <line x1="6" y1="3" x2="6" y2="15" />
      <circle cx="18" cy="6" r="3" />
      <circle cx="6" cy="18" r="3" />
      <path d="M18 9a9 9 0 0 1-9 9" />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface RepoGroupProps {
  repoPath: string
  tasks: model.Task[]
  onSelectTask: (task: model.Task) => void
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function RepoGroup({
  repoPath,
  tasks,
  onSelectTask,
}: RepoGroupProps): React.ReactElement {
  const [expanded, setExpanded] = useState(true)
  const [repoInfo, setRepoInfo] = useState<git.RepoInfo | null>(null)
  const mountedRef = useRef(true)
  const gitIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const sorted = sortTasks(tasks)
  const accentClass = groupAccentColor(tasks)

  const handleToggle = useCallback(() => {
    setExpanded((prev) => !prev)
  }, [])

  // Fetch git info on mount and every 30s
  useEffect(() => {
    mountedRef.current = true

    async function fetchGit(): Promise<void> {
      const info = await getCachedRepoInfo(repoPath)
      if (mountedRef.current && info !== null) {
        setRepoInfo(info)
      }
    }

    void fetchGit()

    gitIntervalRef.current = setInterval(() => {
      // Invalidate cache for this repo so next fetch gets fresh data
      gitInfoCache.delete(repoPath)
      void fetchGit()
    }, GIT_REFRESH_MS)

    return () => {
      mountedRef.current = false
      if (gitIntervalRef.current !== null) {
        clearInterval(gitIntervalRef.current)
      }
    }
  }, [repoPath])

  return (
    <div
      className={`bg-surface border border-border rounded-xl shadow-lg shadow-black/20
                  border-l-[3px] ${accentClass} overflow-hidden
                  transition-all duration-200 hover:shadow-xl hover:border-muted`}
    >
      {/* Header */}
      <button
        type="button"
        onClick={handleToggle}
        className="w-full flex items-center gap-2 px-4 py-3 text-left hover:bg-elevated
                   transition-colors focus:outline-none focus-visible:ring-2
                   focus-visible:ring-inset focus-visible:ring-blue-500"
        aria-expanded={expanded}
      >
        <ChevronIcon open={expanded} />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold text-primary truncate">
              {repoBasename(repoPath)}
            </span>
            <span className="flex-shrink-0 text-[10px] font-medium bg-border-m text-secondary rounded-full px-2 py-0.5">
              {tasks.length}
            </span>
            {/* Branch badge */}
            {repoInfo !== null && repoInfo.branch !== '' && (
              <span className="flex-shrink-0 flex items-center gap-1 text-[10px] font-medium
                             bg-border-m text-secondary rounded px-1.5 py-0.5">
                <BranchIcon />
                {truncate(repoInfo.branch, 24)}
              </span>
            )}
          </div>
          <div className="flex items-center gap-3 mt-0.5">
            <p className="text-[11px] text-muted truncate">{repoPath}</p>
            {/* Git diff stats */}
            {repoInfo !== null && repoInfo.filesChanged > 0 && (
              <span className="flex-shrink-0 text-[10px] text-secondary">
                {repoInfo.filesChanged} file{repoInfo.filesChanged !== 1 ? 's' : ''}{' '}
                <span className="text-acc-green">+{repoInfo.insertions}</span>
                {' / '}
                <span className="text-acc-red">-{repoInfo.deletions}</span>
              </span>
            )}
          </div>
        </div>
      </button>

      {/* Tasks with animated expand/collapse */}
      <AnimatePresence initial={false}>
        {expanded && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.2, ease: 'easeInOut' }}
            className="overflow-hidden"
          >
            <div className="border-t border-border-m">
              {sorted.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  onSelect={onSelectTask}
                  repoInfo={repoInfo}
                />
              ))}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

// ---------------------------------------------------------------------------
// TaskRow (dashboard variant) -- enhanced 2-row layout
// ---------------------------------------------------------------------------

interface TaskRowProps {
  task: model.Task
  onSelect: (task: model.Task) => void
  repoInfo: git.RepoInfo | null
}

function TaskRow({ task, onSelect, repoInfo }: TaskRowProps): React.ReactElement {
  const dotClass = statusBgClass(task.status as TaskStatus)
  const showOutput = task.outputPath !== ''

  return (
    <button
      type="button"
      onClick={() => onSelect(task)}
      className="w-full text-left px-4 py-3 border-b border-border-m last:border-b-0
                 hover:bg-elevated transition-colors focus:outline-none
                 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-blue-500"
    >
      {/* Row 1: Status, agent, name, time */}
      <div className="flex items-center gap-2">
        {/* Status dot */}
        <span
          className={`flex-shrink-0 w-2 h-2 rounded-full ${dotClass}`}
          aria-label={`Status: ${task.status}`}
        />

        {/* Agent badge */}
        <span className="flex-shrink-0 text-[10px] font-medium bg-border-m text-secondary rounded px-1.5 py-0.5">
          {agentLabel(task.agentType)}
        </span>

        {/* Task name */}
        <span className="text-sm text-primary truncate flex-1 min-w-0">
          {task.name}
        </span>

        {/* Duration (prominent) */}
        <span className="flex-shrink-0 text-xs font-medium text-secondary">
          {timeAgo(task.createdAt)}
        </span>
      </div>

      {/* Row 2: Git info + last commit */}
      <div className="flex items-center gap-3 mt-1.5 ml-4">
        {/* Branch */}
        {repoInfo !== null && repoInfo.branch !== '' && (
          <span className="flex items-center gap-1 text-[10px] text-muted">
            <BranchIcon />
            {truncate(repoInfo.branch, 20)}
          </span>
        )}

        {/* Files changed */}
        {repoInfo !== null && repoInfo.filesChanged > 0 && (
          <span className="text-[10px] text-muted">
            {repoInfo.filesChanged}F{' '}
            <span className="text-acc-green">+{repoInfo.insertions}</span>
            <span className="text-acc-red">-{repoInfo.deletions}</span>
          </span>
        )}

        {/* Last commit message */}
        {repoInfo !== null && repoInfo.lastCommitMsg !== '' && (
          <span className="text-[10px] text-muted truncate max-w-[200px]" title={repoInfo.lastCommitMsg}>
            {truncate(repoInfo.lastCommitMsg, 40)}
          </span>
        )}
      </div>

      {/* Mini output preview */}
      {showOutput && <MiniOutput taskId={task.id} maxLines={3} />}
    </button>
  )
}
