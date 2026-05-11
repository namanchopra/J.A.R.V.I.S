import { useCallback, useEffect, useRef, useState } from 'react'
import { CreateWorkflow, GetWorkflows } from '../../wailsjs/go/main/App'
import { model } from '../../wailsjs/go/models'
import { WorkflowCard } from '../components/WorkflowCard'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 5000

// ---------------------------------------------------------------------------
// Inline create form
// ---------------------------------------------------------------------------

function InlineCreateWorkflow({ onCreated }: { onCreated: () => void }): React.ReactElement {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault()
      const trimmed = name.trim()
      if (trimmed === '') return

      setSubmitting(true)
      setError(null)
      try {
        await CreateWorkflow(trimmed, description.trim())
        setName('')
        setDescription('')
        onCreated()
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        setSubmitting(false)
      }
    },
    [name, description, onCreated],
  )

  return (
    <form onSubmit={handleSubmit} className="holo-panel p-4">
      <h2 className="text-sm font-semibold text-[#00e5ff] mb-3">Create Workflow</h2>

      {error !== null && (
        <div role="alert" className="text-xs text-[#ff4757] bg-[#ff4757]/10 rounded-md px-3 py-2 mb-3">
          {error}
        </div>
      )}

      <div className="flex items-start gap-3">
        <div className="flex-1 space-y-2">
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Workflow name..."
            className="sci-fi w-full text-sm"
            required
          />
          <input
            type="text"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Description (optional)"
            className="sci-fi w-full text-xs"
          />
        </div>
        <button
          type="submit"
          disabled={submitting || name.trim() === ''}
          className="px-4 py-1.5 text-xs font-medium text-white rounded-md
                     transition-all self-start
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-[#00e5ff]
                     disabled:opacity-40 disabled:cursor-not-allowed"
          style={{
            background: 'linear-gradient(135deg, #0d9488, #00e5ff)',
            boxShadow: '0 0 8px rgba(0, 229, 255, 0.2)',
          }}
        >
          {submitting ? 'Creating...' : 'Create'}
        </button>
      </div>
    </form>
  )
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function WorkflowsView(): React.ReactElement {
  const [workflows, setWorkflows] = useState<model.Workflow[]>([])
  const [error, setError] = useState<string | null>(null)

  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const mountedRef = useRef(true)

  const fetchWorkflows = useCallback(async () => {
    try {
      const result = await GetWorkflows()
      if (mountedRef.current) {
        setWorkflows(result ?? [])
        setError(null)
      }
    } catch (err: unknown) {
      if (mountedRef.current) {
        const msg = err instanceof Error ? err.message : String(err)
        setError(msg)
      }
    }
  }, [])

  useEffect(() => {
    mountedRef.current = true
    void fetchWorkflows()

    intervalRef.current = setInterval(() => {
      void fetchWorkflows()
    }, POLL_INTERVAL_MS)

    return () => {
      mountedRef.current = false
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current)
      }
    }
  }, [fetchWorkflows])

  return (
    <div className="flex-1 flex flex-col min-h-0">
      {/* Header */}
      <header className="h-12 flex-shrink-0 flex items-center px-5 border-b border-[rgba(0,229,255,0.15)] bg-[#111827]">
        <h1 className="text-base font-bold tracking-wide text-[#00e5ff]">Workflows</h1>
      </header>

      {/* Scrollable content */}
      <div className="flex-1 overflow-y-auto">
        <div className="p-5 space-y-3">
          {/* Create workflow -- always at top */}
          <InlineCreateWorkflow onCreated={() => void fetchWorkflows()} />

          {error !== null && (
            <div className="text-sm text-[#ff4757] bg-[#ff4757]/10 rounded-md px-3 py-2 glow-border" style={{ borderColor: 'rgba(255, 71, 87, 0.3)' }}>
              {error}
            </div>
          )}

          {workflows.length === 0 && error === null && (
            <p className="text-xs text-[#4a6278] text-center py-6">
              No workflows yet. Create one above to group related tasks.
            </p>
          )}

          {workflows.map((wf) => (
            <WorkflowCard
              key={wf.id}
              workflow={wf}
              onDeleted={() => void fetchWorkflows()}
            />
          ))}
        </div>
      </div>
    </div>
  )
}
