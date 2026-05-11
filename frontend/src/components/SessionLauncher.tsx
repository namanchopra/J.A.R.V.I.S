import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { GetAvailableAgents, GetTasks, LaunchSession } from '../../wailsjs/go/main/App'
import { agent } from '../../wailsjs/go/models'
import { SessionTemplates } from './SessionTemplates'
import type { TemplateSelection } from './SessionTemplates'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface SessionLauncherProps {
  onClose: () => void
  onLaunched?: (sessionId: string) => void
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function SessionLauncher({
  onClose,
  onLaunched,
}: SessionLauncherProps): React.ReactElement {
  const [agents, setAgents] = useState<agent.AgentInfo[]>([])
  const [agentType, setAgentType] = useState('')
  const [repoPath, setRepoPath] = useState('')
  const [prompt, setPrompt] = useState('')
  const [repoPaths, setRepoPaths] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loadingAgents, setLoadingAgents] = useState(true)

  const overlayRef = useRef<HTMLDivElement>(null)
  const agentSelectRef = useRef<HTMLSelectElement>(null)

  // Fetch available agents on mount
  useEffect(() => {
    let cancelled = false
    async function load(): Promise<void> {
      try {
        const result = await GetAvailableAgents()
        if (cancelled) return
        const list = result ?? []
        setAgents(list)
        // Default to the first available agent
        const firstAvailable = list.find((a) => a.available)
        if (firstAvailable !== undefined) {
          setAgentType(firstAvailable.agentType)
        } else if (list.length > 0 && list[0] !== undefined) {
          setAgentType(list[0].agentType)
        }
      } catch (err) {
        console.warn('Failed to load agent types:', err)
      } finally {
        if (!cancelled) setLoadingAgents(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [])

  // Fetch recent repo paths from existing tasks
  useEffect(() => {
    let cancelled = false
    async function load(): Promise<void> {
      try {
        const tasks = await GetTasks('')
        if (cancelled) return
        const paths = Array.from(
          new Set((tasks ?? []).map((t) => t.repoPath).filter(Boolean)),
        )
        setRepoPaths(paths)
      } catch (err) {
        console.warn('Failed to fetch repo paths:', err)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [])

  // Focus the agent select on mount
  useEffect(() => {
    if (!loadingAgents) {
      agentSelectRef.current?.focus()
    }
  }, [loadingAgents])

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

  const handleTemplateSelect = useCallback(
    (selection: TemplateSelection) => {
      setPrompt(selection.prompt)
      // Only update agent type if the template agent is available
      const templateAgent = agents.find(
        (a) => a.agentType === selection.agentType,
      )
      if (templateAgent !== undefined) {
        setAgentType(selection.agentType)
      }
    },
    [agents],
  )

  const selectedAgent = useMemo(
    () => agents.find((a) => a.agentType === agentType),
    [agents, agentType],
  )

  const canSubmit =
    agentType !== '' &&
    repoPath.trim() !== '' &&
    prompt.trim() !== '' &&
    selectedAgent?.available === true &&
    !submitting

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault()

      const trimmedRepo = repoPath.trim()
      const trimmedPrompt = prompt.trim()

      if (agentType === '') {
        setError('Please select an agent.')
        return
      }
      if (selectedAgent !== undefined && !selectedAgent.available) {
        setError(`${selectedAgent.name} is not installed. Please install it first.`)
        return
      }
      if (trimmedRepo === '') {
        setError('Repo path is required.')
        return
      }
      if (trimmedPrompt === '') {
        setError('Prompt is required.')
        return
      }

      setSubmitting(true)
      setError(null)

      try {
        const session = await LaunchSession(agentType, trimmedRepo, trimmedPrompt)
        onLaunched?.(session.id)
        onClose()
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err)
        setError(message)
      } finally {
        setSubmitting(false)
      }
    },
    [agentType, repoPath, prompt, selectedAgent, onClose, onLaunched],
  )

  const inputClasses =
    'w-full rounded-md bg-border-m border border-border text-sm ' +
    'text-primary px-3 py-2 placeholder-gray-500 focus:outline-none ' +
    'focus-visible:ring-2 focus-visible:ring-acc-teal'

  const labelClasses = 'block text-sm font-medium text-primary mb-1'

  return (
    <div
      ref={overlayRef}
      onClick={handleOverlayClick}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby="session-launcher-title"
    >
      <div className="bg-elevated rounded-lg shadow-xl w-full max-w-2xl mx-4 border border-border max-h-[90vh] overflow-y-auto">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <h2
            id="session-launcher-title"
            className="text-base font-semibold text-primary"
          >
            Launch Session
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close dialog"
            className="text-secondary hover:text-primary transition-colors
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal
                       rounded-md p-1"
          >
            <CloseIcon />
          </button>
        </div>

        {/* Templates */}
        <div className="px-5 pt-4">
          <SessionTemplates
            onSelectTemplate={handleTemplateSelect}
            onBatchLaunchComplete={() => {
              onLaunched?.('')
              onClose()
            }}
          />
        </div>

        {/* Separator */}
        <div className="mx-5 flex items-center gap-3 py-1">
          <div className="flex-1 border-t border-border" />
          <span className="text-[11px] text-muted uppercase tracking-wider">
            or configure manually
          </span>
          <div className="flex-1 border-t border-border" />
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

          {/* Agent selector */}
          <div>
            <label htmlFor="session-agent" className={labelClasses}>
              Agent <span className="text-red-400">*</span>
            </label>
            {loadingAgents ? (
              <div className="text-sm text-muted py-2">Loading agents...</div>
            ) : (
              <select
                ref={agentSelectRef}
                id="session-agent"
                value={agentType}
                onChange={(e) => setAgentType(e.target.value)}
                className={inputClasses}
              >
                {agents.length === 0 && (
                  <option value="">No agents found</option>
                )}
                {agents.map((a) => (
                  <option
                    key={a.agentType}
                    value={a.agentType}
                    disabled={!a.available}
                  >
                    {a.name}
                    {a.available ? ' (installed)' : ' (not installed)'}
                    {a.version !== '' ? ` v${a.version}` : ''}
                  </option>
                ))}
              </select>
            )}
            {selectedAgent !== undefined && !selectedAgent.available && (
              <p className="mt-1 text-xs text-amber-400">
                This agent is not installed and cannot be used.
              </p>
            )}
          </div>

          {/* Repo Path */}
          <div>
            <label htmlFor="session-repo" className={labelClasses}>
              Repo Path <span className="text-red-400">*</span>
            </label>
            <input
              id="session-repo"
              type="text"
              list="session-repo-paths"
              value={repoPath}
              onChange={(e) => setRepoPath(e.target.value)}
              placeholder="/Users/you/projects/my-repo"
              className={inputClasses}
              required
            />
            {repoPaths.length > 0 && (
              <datalist id="session-repo-paths">
                {repoPaths.map((p) => (
                  <option key={p} value={p} />
                ))}
              </datalist>
            )}
          </div>

          {/* Prompt */}
          <div>
            <label htmlFor="session-prompt" className={labelClasses}>
              Prompt <span className="text-red-400">*</span>
            </label>
            <textarea
              id="session-prompt"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder="Describe what the agent should do..."
              rows={5}
              className={inputClasses + ' resize-none'}
              required
            />
          </div>

          {/* Actions */}
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={onClose}
              disabled={submitting}
              className="px-4 py-2 text-sm font-medium text-primary rounded-md
                         bg-border-m hover:bg-border transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal
                         disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className="px-4 py-2 text-sm font-medium text-white rounded-md
                         bg-blue-600 hover:bg-blue-500 transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal
                         disabled:opacity-50 flex items-center gap-2"
            >
              {submitting && <SpinnerIcon />}
              {submitting ? 'Launching...' : 'Launch Session'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function CloseIcon(): React.ReactElement {
  return (
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
  )
}

function SpinnerIcon(): React.ReactElement {
  return (
    <svg
      className="w-4 h-4 animate-spin"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
    >
      <circle
        className="opacity-25"
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        strokeWidth="4"
      />
      <path
        className="opacity-75"
        fill="currentColor"
        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
      />
    </svg>
  )
}
