import { useCallback, useEffect, useState } from 'react'
import {
  ListSavedProjects,
  DeleteSavedProject,
  ExecuteDivideAndConquer,
} from '../../wailsjs/go/main/App'
import type { store } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Saved workflow card
// ---------------------------------------------------------------------------

function SavedWorkflowCard({
  project,
  onDelete,
}: {
  project: store.Project
  onDelete: () => void
}): React.ReactElement {
  const [expanded, setExpanded] = useState(false)
  const [prompt, setPrompt] = useState('')
  const [executing, setExecuting] = useState(false)

  const repoPaths = project.repoPaths ?? []

  const handleRelaunch = useCallback(async (): Promise<void> => {
    if (!prompt.trim() || repoPaths.length === 0) return
    setExecuting(true)
    try {
      await ExecuteDivideAndConquer('claude-code', repoPaths, prompt, false)
    } catch (err) {
      console.warn('Failed to relaunch workflow:', err)
    }
    setExecuting(false)
  }, [prompt, repoPaths, project.path])

  return (
    <div className="rounded-lg border border-border bg-surface overflow-hidden">
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-4 py-3 hover:bg-elevated transition-colors"
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 min-w-0">
            <svg
              className="w-4 h-4 text-secondary flex-shrink-0"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
            >
              <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z" />
            </svg>
            <span className="text-sm font-medium text-primary truncate">{project.name}</span>
          </div>
          <div className="flex items-center gap-2 flex-shrink-0">
            <span className="text-[10px] text-muted">
              {repoPaths.length} repo{repoPaths.length !== 1 ? 's' : ''}
            </span>
            {project.updatedAt && (
              <span className="text-[10px] text-muted">
                {new Date(project.updatedAt).toLocaleDateString()}
              </span>
            )}
            <svg
              className={`w-3 h-3 text-muted transition-transform ${expanded ? 'rotate-180' : ''}`}
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <path d="M6 9l6 6 6-6" />
            </svg>
          </div>
        </div>
      </button>

      {expanded && (
        <div className="px-4 pb-4 pt-1 border-t border-border-m space-y-3">
          {/* Repo list */}
          <div>
            <p className="text-[10px] text-muted uppercase tracking-wider font-semibold mb-1.5">
              Repositories
            </p>
            <div className="space-y-1">
              {repoPaths.map((rp) => (
                <p key={rp} className="text-[11px] font-mono text-secondary truncate">
                  {rp}
                </p>
              ))}
            </div>
          </div>

          {/* Re-launch prompt */}
          <div>
            <textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder="Prompt for each repo..."
              rows={2}
              className="w-full px-3 py-2 text-sm font-mono bg-app border border-border rounded-lg text-primary placeholder-muted focus:border-acc-blue focus:outline-none resize-none"
            />
          </div>

          {/* Actions */}
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void handleRelaunch()}
              disabled={!prompt.trim() || executing}
              className="flex-1 px-3 py-1.5 text-xs rounded bg-green-600 hover:bg-green-500 text-white disabled:opacity-50 transition-colors font-medium"
            >
              {executing ? 'Launching...' : 'Re-launch'}
            </button>
            <button
              type="button"
              onClick={onDelete}
              className="px-3 py-1.5 text-xs rounded bg-surface border border-border text-red-400 hover:text-red-300 hover:border-red-500/30 transition-colors"
            >
              Delete
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Saved workflows section
// ---------------------------------------------------------------------------

export function SavedWorkflowsSection(): React.ReactElement | null {
  const [projects, setProjects] = useState<store.Project[]>([])
  const [loaded, setLoaded] = useState(false)

  const fetchProjects = useCallback(async (): Promise<void> => {
    try {
      const result = await ListSavedProjects()
      setProjects(result ?? [])
    } catch (err) {
      console.warn('Failed to list saved projects:', err)
    }
    setLoaded(true)
  }, [])

  useEffect(() => {
    void fetchProjects()
  }, [fetchProjects])

  const handleDelete = useCallback(
    async (id: string): Promise<void> => {
      try {
        await DeleteSavedProject(id)
        await fetchProjects()
      } catch (err) {
        console.warn('Failed to delete saved project:', err)
      }
    },
    [fetchProjects],
  )

  if (!loaded || projects.length === 0) return null

  return (
    <section className="px-5 py-4 border-t border-border-m">
      <h2 className="text-sm font-semibold text-primary mb-3">Saved Workflows</h2>
      <div className="space-y-2">
        {projects.map((p) => (
          <SavedWorkflowCard
            key={p.id}
            project={p}
            onDelete={() => void handleDelete(p.id)}
          />
        ))}
      </div>
    </section>
  )
}
