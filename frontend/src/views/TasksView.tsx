import { useCallback, useState } from 'react'
import { model } from '../../wailsjs/go/models'
import { TaskList } from '../components/TaskList'
import { TaskDetail } from '../components/TaskDetail'
import { AddTaskForm } from '../components/AddTaskForm'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface TasksViewProps {
  /** If set, auto-select this task on mount (from dashboard navigation). */
  initialTaskId?: string | null
  /** Callback when a task is selected (for external state sync). */
  onTaskSelected?: (task: model.Task) => void
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function TasksView({
  initialTaskId = null,
  onTaskSelected,
}: TasksViewProps): React.ReactElement {
  const [selectedTask, setSelectedTask] = useState<model.Task | null>(null)
  const [showAddForm, setShowAddForm] = useState(false)

  const handleSelectTask = useCallback(
    (task: model.Task) => {
      setSelectedTask(task)
      onTaskSelected?.(task)
    },
    [onTaskSelected],
  )

  const selectedId = selectedTask?.id ?? initialTaskId ?? null

  return (
    <div className="flex-1 flex min-h-0">
      {/* Sidebar task list */}
      <aside className="w-80 flex-shrink-0 border-r border-[rgba(0,229,255,0.15)] bg-[#111827] flex flex-col">
        <TaskList
          selectedTaskId={selectedId}
          onSelectTask={handleSelectTask}
          onAddTask={() => setShowAddForm(true)}
        />
      </aside>

      {/* Detail panel */}
      <main className="flex-1 flex flex-col min-w-0 bg-[#0a0e1a]">
        {selectedTask !== null ? (
          <TaskDetail task={selectedTask} />
        ) : (
          <>
            <header className="h-12 flex-shrink-0 flex items-center px-5 border-b border-[rgba(0,229,255,0.15)] bg-[#111827]">
              <h1 className="text-base font-bold tracking-wide text-[#e8f4ff]">Tasks</h1>
            </header>
            <div className="flex-1 flex items-center justify-center">
              {/* Empty-state sci-fi prompt */}
              <div className="flex flex-col items-center gap-3">
                <div className="w-12 h-12 rounded-full border border-[rgba(0,229,255,0.25)] flex items-center justify-center">
                  <svg
                    className="w-5 h-5 text-[#00e5ff] opacity-60"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    aria-hidden="true"
                  >
                    <rect x="3" y="3" width="18" height="18" rx="2" />
                    <path d="M9 12h6M12 9v6" />
                  </svg>
                </div>
                <p className="text-sm text-[#4a6278] font-mono">Select a task to view details</p>
              </div>
            </div>
          </>
        )}
      </main>

      {/* Add task modal */}
      {showAddForm && <AddTaskForm onClose={() => setShowAddForm(false)} />}
    </div>
  )
}
