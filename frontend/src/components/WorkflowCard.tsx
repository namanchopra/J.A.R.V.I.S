import { useCallback, useEffect, useRef, useState } from 'react'
import { DeleteWorkflow, GetWorkflowTasks } from '../../wailsjs/go/main/App'
import { model } from '../../wailsjs/go/models'
import { type TaskStatus, statusBgClass } from '../lib/colors'

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

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface WorkflowCardProps {
  workflow: model.Workflow
  onDeleted: () => void
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function WorkflowCard({
  workflow,
  onDeleted,
}: WorkflowCardProps): React.ReactElement {
  const [expanded, setExpanded] = useState(false)
  const [tasks, setTasks] = useState<model.Task[]>([])
  const [loading, setLoading] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  const handleToggle = useCallback(() => {
    setExpanded((prev) => {
      const next = !prev
      if (next) {
        setLoading(true)
        GetWorkflowTasks(workflow.id)
          .then((result) => {
            if (mountedRef.current) {
              setTasks(result ?? [])
            }
          })
          .catch(() => {
            // ignore
          })
          .finally(() => {
            if (mountedRef.current) {
              setLoading(false)
            }
          })
      }
      return next
    })
  }, [workflow.id])

  const handleDelete = useCallback(async () => {
    try {
      await DeleteWorkflow(workflow.id)
      onDeleted()
    } catch (err) {
      console.warn('Failed to delete workflow:', err)
    }
  }, [workflow.id, onDeleted])

  return (
    <div className="rounded-lg border border-border bg-elevated overflow-hidden">
      {/* Header */}
      <div className="flex items-center gap-2 px-4 py-3">
        <button
          type="button"
          onClick={handleToggle}
          className="flex-1 flex items-center gap-2 text-left focus:outline-none
                     focus-visible:ring-2 focus-visible:ring-blue-500 rounded"
          aria-expanded={expanded}
        >
          <svg
            className={`w-4 h-4 text-secondary transition-transform ${expanded ? 'rotate-90' : ''}`}
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
          <div className="min-w-0 flex-1">
            <span className="text-sm font-semibold text-primary">{workflow.name}</span>
            {workflow.description !== '' && (
              <p className="text-xs text-secondary truncate mt-0.5">{workflow.description}</p>
            )}
          </div>
        </button>

        {/* Delete button */}
        {confirmDelete ? (
          <div className="flex items-center gap-1 flex-shrink-0">
            <button
              type="button"
              onClick={() => void handleDelete()}
              className="text-[10px] font-medium text-red-400 bg-red-400/10 hover:bg-red-400/20
                         px-2 py-1 rounded transition-colors focus:outline-none
                         focus-visible:ring-2 focus-visible:ring-red-500"
            >
              Confirm
            </button>
            <button
              type="button"
              onClick={() => setConfirmDelete(false)}
              className="text-[10px] font-medium text-secondary hover:text-primary
                         px-2 py-1 rounded transition-colors focus:outline-none
                         focus-visible:ring-2 focus-visible:ring-blue-500"
            >
              Cancel
            </button>
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setConfirmDelete(true)}
            className="flex-shrink-0 text-muted hover:text-red-400 transition-colors
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500 rounded p-1"
            aria-label={`Delete workflow ${workflow.name}`}
          >
            <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
              <polyline points="3 6 5 6 21 6" />
              <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" />
            </svg>
          </button>
        )}
      </div>

      {/* Expanded task list */}
      {expanded && (
        <div className="border-t border-border/50 px-4 py-2">
          {loading && (
            <p className="text-xs text-muted py-2">Loading tasks...</p>
          )}
          {!loading && tasks.length === 0 && (
            <p className="text-xs text-muted py-2">No tasks in this workflow</p>
          )}
          {tasks.map((task) => (
            <div key={task.id} className="flex items-center gap-2 py-1.5">
              <span
                className={`w-2 h-2 rounded-full flex-shrink-0 ${statusBgClass(task.status as TaskStatus)}`}
              />
              <span className="text-[10px] font-medium bg-border-m text-primary rounded px-1.5 py-0.5">
                {agentLabel(task.agentType)}
              </span>
              <span className="text-sm text-primary truncate">{task.name}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
