import { useCallback, useEffect, useRef, useState } from 'react'
import {
  SuggestWorkflows,
  CreateWorkflow,
  AddTaskToWorkflow,
} from '../../wailsjs/go/main/App'
import { main } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const DISMISS_KEY = 'awm-workflow-suggestions-dismissed'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Extract a human-readable name from a directory path. */
function dirDisplayName(repoDir: string): string {
  const parts = repoDir.replace(/\/+$/, '').split('/')
  return parts[parts.length - 1] ?? repoDir
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function WorkflowSuggestions(): React.ReactElement | null {
  const [suggestions, setSuggestions] = useState<main.WorkflowSuggestion[]>([])
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem(DISMISS_KEY) === 'true'
    } catch (err) {
      console.warn('Failed to read dismiss key from localStorage:', err)
      return false
    }
  })
  const [creating, setCreating] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)

  // -------------------------------------------------------------------------
  // Fetch suggestions on mount
  // -------------------------------------------------------------------------

  useEffect(() => {
    mountedRef.current = true

    if (dismissed) return

    SuggestWorkflows()
      .then((result) => {
        if (mountedRef.current) {
          setSuggestions(result ?? [])
        }
      })
      .catch((err) => {
        console.warn('Failed to fetch workflow suggestions:', err)
      })

    return () => {
      mountedRef.current = false
    }
  }, [dismissed])

  // -------------------------------------------------------------------------
  // Handlers
  // -------------------------------------------------------------------------

  const handleDismiss = useCallback(() => {
    setDismissed(true)
    try {
      localStorage.setItem(DISMISS_KEY, 'true')
    } catch (err) {
      console.warn('Failed to write dismiss key to localStorage:', err)
    }
  }, [])

  const handleCreate = useCallback(
    async (suggestion: main.WorkflowSuggestion) => {
      setCreating(suggestion.name)
      setError(null)

      try {
        const workflow = await CreateWorkflow(suggestion.name, '')
        // Assign each task to the new workflow
        const taskIds = suggestion.taskIds ?? []
        for (const taskId of taskIds) {
          await AddTaskToWorkflow(taskId, workflow.id)
        }

        // Remove this suggestion from the list
        if (mountedRef.current) {
          setSuggestions((prev) =>
            prev.filter((s) => s.name !== suggestion.name),
          )
        }
      } catch (err) {
        if (mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Failed to create workflow')
        }
      } finally {
        if (mountedRef.current) {
          setCreating(null)
        }
      }
    },
    [],
  )

  // -------------------------------------------------------------------------
  // Render nothing if dismissed or no suggestions
  // -------------------------------------------------------------------------

  if (dismissed || suggestions.length === 0) {
    return null
  }

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <div className="rounded-xl border border-border bg-surface overflow-hidden">
      {/* Left-border gradient accent */}
      <div className="flex">
        <div
          className="w-1 flex-shrink-0 rounded-l-xl"
          style={{
            background: 'linear-gradient(to bottom, #6366f1, #3b82f6)',
          }}
        />

        <div className="flex-1 p-4 space-y-3">
          {/* Header */}
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold text-primary">
              Related repos detected
            </h3>
            <button
              type="button"
              onClick={handleDismiss}
              className="text-xs text-muted hover:text-secondary transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 rounded px-1"
            >
              Dismiss
            </button>
          </div>

          {/* Error */}
          {error !== null && (
            <p className="text-xs text-red-400">{error}</p>
          )}

          {/* Suggestion cards */}
          <div className="space-y-2">
            {suggestions.map((suggestion) => {
              const taskCount = (suggestion.taskIds ?? []).length
              const isCreating = creating === suggestion.name

              return (
                <div
                  key={suggestion.name}
                  className="flex items-center justify-between gap-3 px-3 py-2
                             rounded-lg bg-app border border-border-m"
                >
                  <p className="text-xs text-secondary flex-1">
                    <span className="text-primary font-medium">{taskCount}</span>
                    {' '}agent{taskCount !== 1 ? 's' : ''} working in repos under{' '}
                    <code className="font-mono text-acc-blue">
                      {suggestion.repoDir}
                    </code>
                    . Create a{' '}
                    <span className="text-primary font-medium">
                      &ldquo;{dirDisplayName(suggestion.repoDir)}&rdquo;
                    </span>
                    {' '}workflow?
                  </p>

                  <button
                    type="button"
                    onClick={() => void handleCreate(suggestion)}
                    disabled={isCreating}
                    className="flex-shrink-0 inline-flex items-center gap-1.5 px-3 py-1.5
                               rounded-lg text-xs font-medium transition-colors
                               bg-blue-600/80 text-white hover:bg-blue-600
                               disabled:opacity-50 disabled:cursor-not-allowed
                               focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
                  >
                    {isCreating ? (
                      <svg
                        className="w-3 h-3 animate-spin"
                        viewBox="0 0 16 16"
                        fill="none"
                        aria-hidden="true"
                      >
                        <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="2" opacity="0.25" />
                        <path
                          d="M14 8a6 6 0 00-6-6"
                          stroke="currentColor"
                          strokeWidth="2"
                          strokeLinecap="round"
                        />
                      </svg>
                    ) : (
                      'Create Workflow'
                    )}
                  </button>
                </div>
              )
            })}
          </div>
        </div>
      </div>
    </div>
  )
}
