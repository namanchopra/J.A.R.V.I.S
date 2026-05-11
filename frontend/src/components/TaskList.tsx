import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { GetTasks } from '../../wailsjs/go/main/App'
import { model } from '../../wailsjs/go/models'
import { type TaskStatus, statusBgClass } from '../lib/colors'
import { isAutoDetected, sortTasks } from '../lib/utils'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 2000

const STATUS_OPTIONS: Array<{ label: string; value: string }> = [
  { label: 'All', value: '' },
  { label: 'Pending', value: 'pending' },
  { label: 'Running', value: 'running' },
  { label: 'Done', value: 'done' },
  { label: 'Failed', value: 'failed' },
  { label: 'Needs Input', value: 'needs-input' },
]

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Returns the last two segments of a file path for compact display. */
function truncateRepoPath(repoPath: string): string {
  const segments = repoPath.replace(/\\/g, '/').split('/').filter(Boolean)
  if (segments.length <= 2) return segments.join('/')
  return '.../' + segments.slice(-2).join('/')
}

/** Agent type display labels */
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

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface TaskListProps {
  onSelectTask: (task: model.Task) => void
  selectedTaskId: string | null
  onAddTask: () => void
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function TaskList({
  onSelectTask,
  selectedTaskId,
  onAddTask,
}: TaskListProps): React.ReactElement {
  const [tasks, setTasks] = useState<model.Task[]>([])
  const [statusFilter, setStatusFilter] = useState('')
  const [error, setError] = useState<string | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const fetchTasks = useCallback(async (filter: string) => {
    try {
      const result = await GetTasks(filter)
      setTasks(result ?? [])
      setError(null)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err)
      setError(message)
    }
  }, [])

  // Initial fetch + polling
  useEffect(() => {
    // Fetch immediately
    void fetchTasks(statusFilter)

    // Poll every 2 seconds
    intervalRef.current = setInterval(() => {
      void fetchTasks(statusFilter)
    }, POLL_INTERVAL_MS)

    return () => {
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current)
      }
    }
  }, [fetchTasks, statusFilter])

  return (
    <>
      {/* Header with title, filter, and add button */}
      <div className="px-4 py-3 border-b border-border-m bg-surface space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-secondary">
            Tasks
          </h2>
          <button
            type="button"
            onClick={onAddTask}
            aria-label="Add new task"
            className="flex items-center justify-center w-7 h-7 rounded-md
                       bg-border-m text-secondary hover:bg-border
                       hover:text-primary transition-colors focus:outline-none
                       focus-visible:ring-2 focus-visible:ring-blue-500"
          >
            <span className="text-lg leading-none">+</span>
          </button>
        </div>

        {/* Status filter */}
        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          aria-label="Filter tasks by status"
          className="w-full rounded-md bg-app border border-border
                     text-sm text-primary px-2 py-1.5 focus:outline-none
                     focus-visible:ring-2 focus-visible:ring-blue-500"
        >
          {STATUS_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </div>

      {/* Auto-detected sessions count */}
      <AutoDetectedBanner tasks={tasks} />

      {/* Task list */}
      <div className="flex-1 overflow-y-auto bg-surface">
        {error !== null && (
          <div className="px-4 py-3 text-sm text-acc-red">{error}</div>
        )}

        {tasks.length === 0 && error === null && (
          <div className="flex-1 flex items-center justify-center px-4 py-8">
            <p className="text-sm text-muted">No tasks yet</p>
          </div>
        )}

        {sortTasks(tasks).map((task) => (
          <TaskRow
            key={task.id}
            task={task}
            isSelected={task.id === selectedTaskId}
            onSelect={onSelectTask}
          />
        ))}
      </div>
    </>
  )
}

// ---------------------------------------------------------------------------
// TaskRow sub-component
// ---------------------------------------------------------------------------

interface TaskRowProps {
  task: model.Task
  isSelected: boolean
  onSelect: (task: model.Task) => void
}

function TaskRow({ task, isSelected, onSelect }: TaskRowProps): React.ReactElement {
  const dotClass = statusBgClass(task.status as TaskStatus)
  const autoDetected = isAutoDetected(task)
  const isRunning = task.status === 'running'

  return (
    <button
      type="button"
      onClick={() => onSelect(task)}
      className={`w-full text-left px-4 py-3 border-b border-border-m
                  transition-all duration-150 focus:outline-none
                  focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-blue-500
                  ${isSelected ? 'bg-elevated' : 'hover:bg-elevated'}`}
    >
      <div className="flex items-start gap-2.5">
        {/* Status dot with glow for running */}
        <span className="mt-1.5 flex-shrink-0 relative">
          {isRunning && (
            <span
              className="absolute inset-0 rounded-full bg-amber-400 animate-ping opacity-40"
              aria-hidden="true"
            />
          )}
          <span
            className={`relative block w-2.5 h-2.5 rounded-full ${dotClass}`}
            aria-label={`Status: ${task.status}`}
          />
        </span>

        <div className="min-w-0 flex-1">
          {/* Task name + AUTO badge */}
          <p className="text-sm font-medium text-primary truncate flex items-center gap-1.5">
            <span className="truncate">{task.name}</span>
            {autoDetected && (
              <span className="flex-shrink-0 text-xs bg-blue-600/20 text-blue-400 rounded px-1">
                AUTO
              </span>
            )}
          </p>

          {/* Repo path */}
          <p className="text-xs text-secondary truncate mt-0.5">
            {truncateRepoPath(task.repoPath)}
          </p>

          {/* Agent badge */}
          <span
            className="inline-block mt-1 px-1.5 py-0.5 text-[10px] font-medium
                       rounded bg-border-m text-secondary"
          >
            {agentLabel(task.agentType)}
          </span>
        </div>
      </div>
    </button>
  )
}

// ---------------------------------------------------------------------------
// AutoDetectedBanner sub-component
// ---------------------------------------------------------------------------

function AutoDetectedBanner({
  tasks,
}: {
  tasks: model.Task[]
}): React.ReactElement | null {
  const count = useMemo(
    () =>
      tasks.filter(
        (t) => isAutoDetected(t) && t.status === 'running',
      ).length,
    [tasks],
  )

  if (count === 0) return null

  return (
    <div className="px-4 py-2 border-b border-border-m bg-blue-900/20 text-blue-300 text-xs">
      <span role="img" aria-label="Detected sessions">
        {'🔍'}
      </span>{' '}
      {count} active session{count !== 1 ? 's' : ''} detected
    </div>
  )
}
