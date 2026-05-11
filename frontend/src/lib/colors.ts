export type TaskStatus = 'pending' | 'running' | 'done' | 'failed' | 'needs-input'

/**
 * Maps task statuses to Tailwind CSS color classes.
 *
 * Usage:
 *   <span className={`bg-${statusColors[status]}`} />
 *
 * Note: Because Tailwind scans source files for class names at build time,
 * make sure these full class strings appear in the safelist or are referenced
 * as complete literals somewhere in the codebase.
 */
export const statusColors: Record<TaskStatus, string> = {
  pending: 'gray-400',
  running: 'amber-400',
  done: 'green-400',
  failed: 'red-400',
  'needs-input': 'teal-400',
} as const

/**
 * Returns a complete Tailwind `bg-*` class for a given task status.
 * Using full class names ensures Tailwind's JIT compiler includes them.
 */
export function statusBgClass(status: TaskStatus): string {
  const map: Record<TaskStatus, string> = {
    pending: 'bg-gray-400',
    running: 'bg-amber-400',
    done: 'bg-green-400',
    failed: 'bg-red-400',
    'needs-input': 'bg-teal-400',
  }
  return map[status]
}

/**
 * Returns a complete Tailwind `text-*` class for a given task status.
 */
export function statusTextClass(status: TaskStatus): string {
  const map: Record<TaskStatus, string> = {
    pending: 'text-secondary',
    running: 'text-amber-400',
    done: 'text-green-400',
    failed: 'text-red-400',
    'needs-input': 'text-teal-400',
  }
  return map[status]
}

/**
 * Agent-specific accent colors for visual differentiation.
 */
export const agentColors: Record<string, string> = {
  'claude-code': '#f97316', // orange
  'kiro': '#06b6d4',       // cyan
  'gemini': '#3b82f6',     // blue
  'codex': '#10b981',      // emerald
  'aider': '#eab308',      // yellow
  'other': '#8b949e',      // gray
}
