import { model } from '../../wailsjs/go/models'
import { type TaskStatus } from './colors'

// ---------------------------------------------------------------------------
// Auto-detection helpers
// ---------------------------------------------------------------------------

const AUTO_DETECTED_PREFIX = '[auto-detected]'

/** Returns true if the task was created via automatic process detection. */
export function isAutoDetected(task: model.Task): boolean {
  return task.description.startsWith(AUTO_DETECTED_PREFIX)
}

/** Strips the "[auto-detected]" prefix from the description and trims whitespace. */
export function cleanDescription(task: model.Task): string {
  if (!isAutoDetected(task)) return task.description
  return task.description.slice(AUTO_DETECTED_PREFIX.length).trim()
}

// ---------------------------------------------------------------------------
// Task sorting
// ---------------------------------------------------------------------------

/** Priority order for statuses — lower number sorts first. */
const STATUS_PRIORITY: Record<TaskStatus, number> = {
  running: 0,
  'needs-input': 1,
  pending: 2,
  done: 3,
  failed: 4,
}

function statusPriority(status: string): number {
  return STATUS_PRIORITY[status as TaskStatus] ?? 99
}

/**
 * Sorts tasks with the following precedence:
 *  1. Running auto-detected tasks first
 *  2. By status priority (running > needs-input > pending > done > failed)
 *  3. By updatedAt descending (most recent first)
 *
 * Returns a new array — does not mutate the input.
 */
export function sortTasks(tasks: model.Task[]): model.Task[] {
  return [...tasks].sort((a, b) => {
    const aAutoRunning = isAutoDetected(a) && a.status === 'running' ? 0 : 1
    const bAutoRunning = isAutoDetected(b) && b.status === 'running' ? 0 : 1

    // 1. Running auto-detected tasks first
    if (aAutoRunning !== bAutoRunning) return aAutoRunning - bAutoRunning

    // 2. Status priority
    const aPriority = statusPriority(a.status)
    const bPriority = statusPriority(b.status)
    if (aPriority !== bPriority) return aPriority - bPriority

    // 3. Most recently updated first
    const aTime = new Date(String(a.updatedAt)).getTime() || 0
    const bTime = new Date(String(b.updatedAt)).getTime() || 0
    return bTime - aTime
  })
}
