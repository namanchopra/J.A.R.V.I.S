import { useCallback, useEffect, useRef, useState } from 'react'
import { CreateTask } from '../../wailsjs/go/main/App'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const AGENT_TYPES: Array<{ label: string; value: string }> = [
  { label: 'Claude Code', value: 'claude-code' },
  { label: 'Kiro', value: 'kiro' },
  { label: 'Gemini', value: 'gemini' },
  { label: 'Codex', value: 'codex' },
  { label: 'Aider', value: 'aider' },
  { label: 'Other', value: 'other' },
]

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface AddTaskFormProps {
  onClose: () => void
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function AddTaskForm({ onClose }: AddTaskFormProps): React.ReactElement {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [repoPath, setRepoPath] = useState('')
  const [agentType, setAgentType] = useState('claude-code')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const nameInputRef = useRef<HTMLInputElement>(null)
  const overlayRef = useRef<HTMLDivElement>(null)

  // Focus the name input on mount
  useEffect(() => {
    nameInputRef.current?.focus()
  }, [])

  // Close on Escape key
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent): void {
      if (e.key === 'Escape') {
        onClose()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const handleOverlayClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (e.target === overlayRef.current) {
        onClose()
      }
    },
    [onClose],
  )

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault()

      const trimmedName = name.trim()
      const trimmedRepo = repoPath.trim()

      if (trimmedName === '') {
        setError('Task name is required.')
        return
      }
      if (trimmedRepo === '') {
        setError('Repo path is required.')
        return
      }

      setSubmitting(true)
      setError(null)

      try {
        await CreateTask(trimmedName, description.trim(), trimmedRepo, agentType)
        onClose()
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err)
        setError(message)
      } finally {
        setSubmitting(false)
      }
    },
    [name, description, repoPath, agentType, onClose],
  )

  const inputClasses =
    'w-full rounded-md bg-border-m border border-border text-sm ' +
    'text-primary px-3 py-2 placeholder-gray-500 focus:outline-none ' +
    'focus-visible:ring-2 focus-visible:ring-blue-500'

  const labelClasses = 'block text-sm font-medium text-primary mb-1'

  return (
    <div
      ref={overlayRef}
      onClick={handleOverlayClick}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby="add-task-title"
    >
      <div className="bg-elevated rounded-lg shadow-xl w-full max-w-md mx-4 border border-border">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <h2 id="add-task-title" className="text-base font-semibold text-primary">
            New Task
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close dialog"
            className="text-secondary hover:text-primary transition-colors
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500
                       rounded-md p-1"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              className="h-5 w-5"
              viewBox="0 0 20 20"
              fill="currentColor"
              aria-hidden="true"
            >
              <path
                fillRule="evenodd"
                d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1
                   0 111.414 1.414L11.414 10l4.293 4.293a1 1 0
                   01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0
                   01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z"
                clipRule="evenodd"
              />
            </svg>
          </button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="px-5 py-4 space-y-4">
          {/* Error message */}
          {error !== null && (
            <div
              role="alert"
              className="text-sm text-red-400 bg-red-400/10 rounded-md px-3 py-2"
            >
              {error}
            </div>
          )}

          {/* Name */}
          <div>
            <label htmlFor="task-name" className={labelClasses}>
              Name <span className="text-red-400">*</span>
            </label>
            <input
              ref={nameInputRef}
              id="task-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Implement login page"
              className={inputClasses}
              required
            />
          </div>

          {/* Description */}
          <div>
            <label htmlFor="task-description" className={labelClasses}>
              Description
            </label>
            <textarea
              id="task-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional details about the task..."
              rows={3}
              className={inputClasses + ' resize-none'}
            />
          </div>

          {/* Repo Path */}
          <div>
            <label htmlFor="task-repo-path" className={labelClasses}>
              Repo Path <span className="text-red-400">*</span>
            </label>
            <input
              id="task-repo-path"
              type="text"
              value={repoPath}
              onChange={(e) => setRepoPath(e.target.value)}
              placeholder="/Users/you/projects/my-repo"
              className={inputClasses}
              required
            />
          </div>

          {/* Agent Type */}
          <div>
            <label htmlFor="task-agent-type" className={labelClasses}>
              Agent Type
            </label>
            <select
              id="task-agent-type"
              value={agentType}
              onChange={(e) => setAgentType(e.target.value)}
              className={inputClasses}
            >
              {AGENT_TYPES.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>

          {/* Actions */}
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={onClose}
              disabled={submitting}
              className="px-4 py-2 text-sm font-medium text-primary rounded-md
                         bg-border-m hover:bg-border transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500
                         disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={submitting}
              className="px-4 py-2 text-sm font-medium text-white rounded-md
                         bg-blue-600 hover:bg-blue-500 transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500
                         disabled:opacity-50"
            >
              {submitting ? 'Creating...' : 'Create Task'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
