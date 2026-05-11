import { useCallback, useEffect, useRef, useState } from 'react'
import { CreateWorkflow } from '../../wailsjs/go/main/App'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface CreateWorkflowFormProps {
  onClose: () => void
  onCreated: () => void
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function CreateWorkflowForm({
  onClose,
  onCreated,
}: CreateWorkflowFormProps): React.ReactElement {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const nameInputRef = useRef<HTMLInputElement>(null)
  const overlayRef = useRef<HTMLDivElement>(null)

  // Focus name input on mount
  useEffect(() => {
    nameInputRef.current?.focus()
  }, [])

  // Close on Escape
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent): void {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const handleOverlayClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (e.target === overlayRef.current) onClose()
    },
    [onClose],
  )

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault()
      const trimmed = name.trim()
      if (trimmed === '') {
        setError('Workflow name is required.')
        return
      }

      setSubmitting(true)
      setError(null)

      try {
        await CreateWorkflow(trimmed, description.trim())
        onCreated()
        onClose()
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : String(err)
        setError(msg)
      } finally {
        setSubmitting(false)
      }
    },
    [name, description, onClose, onCreated],
  )

  const inputClasses =
    'w-full rounded-md bg-border-m border border-border text-sm ' +
    'text-primary px-3 py-2 placeholder-gray-500 focus:outline-none ' +
    'focus-visible:ring-2 focus-visible:ring-blue-500'

  return (
    <div
      ref={overlayRef}
      onClick={handleOverlayClick}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby="create-workflow-title"
    >
      <div className="bg-elevated rounded-lg shadow-xl w-full max-w-md mx-4 border border-border">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <h2 id="create-workflow-title" className="text-base font-semibold text-primary">
            New Workflow
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close dialog"
            className="text-secondary hover:text-primary transition-colors
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500
                       rounded-md p-1"
          >
            <svg className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
              <path
                fillRule="evenodd"
                d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z"
                clipRule="evenodd"
              />
            </svg>
          </button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="px-5 py-4 space-y-4">
          {error !== null && (
            <div role="alert" className="text-sm text-red-400 bg-red-400/10 rounded-md px-3 py-2">
              {error}
            </div>
          )}

          <div>
            <label htmlFor="workflow-name" className="block text-sm font-medium text-primary mb-1">
              Name <span className="text-red-400">*</span>
            </label>
            <input
              ref={nameInputRef}
              id="workflow-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Feature sprint, Bug triage..."
              className={inputClasses}
              required
            />
          </div>

          <div>
            <label htmlFor="workflow-desc" className="block text-sm font-medium text-primary mb-1">
              Description
            </label>
            <textarea
              id="workflow-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Optional description..."
              rows={2}
              className={inputClasses + ' resize-none'}
            />
          </div>

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
              {submitting ? 'Creating...' : 'Create Workflow'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
