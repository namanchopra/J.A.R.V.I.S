import { useCallback, useEffect, useState } from 'react'
import {
  ListWorkspaces,
  DeleteWorkspace,
  OpenWorkspaceInTerminal,
  SyncDotClaude,
} from '../../wailsjs/go/main/App'
import type { workspace } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Types for re-launch callback
// ---------------------------------------------------------------------------

export interface RelaunchPayload {
  name: string
  repoPaths: string[]
  prompt: string
}

// ---------------------------------------------------------------------------
// Single workspace card
// ---------------------------------------------------------------------------

function WorkspaceCard({
  ws,
  onDelete,
  onRelaunch,
}: {
  ws: workspace.Workspace
  onDelete: () => void
  onRelaunch: (payload: RelaunchPayload) => void
}): React.ReactElement {
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [opening, setOpening] = useState(false)

  const repoPaths = ws.repoPaths ?? []
  const repoCount = repoPaths.length
  const promptPreview =
    (ws.prompt ?? '').length > 100
      ? ws.prompt.slice(0, 100) + '...'
      : ws.prompt ?? ''

  const createdLabel = ws.createdAt
    ? new Date(ws.createdAt).toLocaleDateString(undefined, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
      })
    : ''

  const handleOpenTerminal = useCallback(async (): Promise<void> => {
    setOpening(true)
    try {
      await OpenWorkspaceInTerminal(ws.path)
    } catch (err) {
      console.warn('Failed to open workspace in terminal:', err)
    }
    setOpening(false)
  }, [ws.path])

  const handleDelete = useCallback(async (): Promise<void> => {
    try {
      await DeleteWorkspace(ws.path)
      onDelete()
    } catch (err) {
      console.warn('Failed to delete workspace:', err)
    }
    setConfirmDelete(false)
  }, [ws.path, onDelete])

  return (
    <div className="rounded-lg border border-border bg-surface overflow-hidden">
      <div className="px-4 py-3">
        {/* Header row */}
        <div className="flex items-center justify-between mb-1.5">
          <div className="flex items-center gap-2 min-w-0">
            {/* Workspace icon */}
            <svg
              className="w-4 h-4 text-acc-teal flex-shrink-0"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
            >
              <path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
              <path d="M8 13h8M8 17h5" />
            </svg>
            <span className="text-sm font-medium text-primary truncate">
              {ws.name}
            </span>
          </div>
          <div className="flex items-center gap-2 flex-shrink-0">
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-teal-500/10 text-acc-teal">
              {repoCount} repo{repoCount !== 1 ? 's' : ''}
            </span>
            {createdLabel && (
              <span className="text-[10px] text-muted">{createdLabel}</span>
            )}
          </div>
        </div>

        {/* Prompt preview */}
        {promptPreview && (
          <p className="text-[11px] text-secondary line-clamp-2 mb-2 font-mono leading-relaxed">
            {promptPreview}
          </p>
        )}

        {/* Repo names */}
        <div className="flex flex-wrap gap-1 mb-3">
          {repoPaths.map((rp) => {
            const repoName = rp.split('/').pop() ?? rp
            return (
              <span
                key={rp}
                className="text-[10px] px-1.5 py-0.5 rounded bg-border-m text-secondary font-mono"
              >
                {repoName}
              </span>
            )
          })}
        </div>

        {/* Actions */}
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => void handleOpenTerminal()}
            disabled={opening}
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-border-m border border-border text-primary hover:bg-border transition-colors font-medium"
          >
            <svg className="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <polyline points="4 17 10 11 4 5" />
              <line x1="12" y1="19" x2="20" y2="19" />
            </svg>
            {opening ? 'Opening...' : 'Open in Terminal'}
          </button>
          <button
            type="button"
            onClick={() =>
              onRelaunch({
                name: ws.name,
                repoPaths: repoPaths,
                prompt: ws.prompt ?? '',
              })
            }
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-acc-teal/20 border border-acc-teal/30 text-acc-teal hover:bg-acc-teal/30 transition-colors font-medium"
          >
            <svg className="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M1 4v6h6M23 20v-6h-6" />
              <path d="M20.49 9A9 9 0 005.64 5.64L1 10m22 4l-4.64 4.36A9 9 0 013.51 15" />
            </svg>
            Re-launch
          </button>
          {confirmDelete ? (
            <div className="flex items-center gap-1.5 ml-auto">
              <span className="text-[10px] text-red-400">Delete?</span>
              <button
                type="button"
                onClick={() => void handleDelete()}
                className="px-2 py-1 text-[10px] rounded bg-red-600 text-white hover:bg-red-500 transition-colors font-medium"
              >
                Yes
              </button>
              <button
                type="button"
                onClick={() => setConfirmDelete(false)}
                className="px-2 py-1 text-[10px] rounded bg-border-m text-secondary hover:text-primary transition-colors"
              >
                No
              </button>
            </div>
          ) : (
            <button
              type="button"
              onClick={() => setConfirmDelete(true)}
              className="ml-auto px-3 py-1.5 text-xs rounded bg-surface border border-border text-red-400 hover:text-red-300 hover:border-red-500/30 transition-colors"
            >
              Delete
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Recent Workspaces section
// ---------------------------------------------------------------------------

export function RecentWorkspacesSection({
  onRelaunch,
  refreshKey,
}: {
  onRelaunch: (payload: RelaunchPayload) => void
  refreshKey: number
}): React.ReactElement | null {
  const [workspaces, setWorkspaces] = useState<workspace.Workspace[]>([])
  const [loaded, setLoaded] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [syncMsg, setSyncMsg] = useState<string | null>(null)

  const fetchWorkspaces = useCallback(async (): Promise<void> => {
    try {
      const result = await ListWorkspaces()
      setWorkspaces(result ?? [])
    } catch (err) {
      console.warn('Failed to list workspaces:', err)
    }
    setLoaded(true)
  }, [])

  useEffect(() => {
    void fetchWorkspaces()
  }, [fetchWorkspaces, refreshKey])

  const handleSync = useCallback(async () => {
    setSyncing(true)
    setSyncMsg(null)
    try {
      const count: number = await SyncDotClaude()
      setSyncMsg(`Synced .claude to ${count} workspace(s)`)
      setTimeout(() => setSyncMsg(null), 3000)
    } catch (err) {
      setSyncMsg(`Sync failed: ${err instanceof Error ? err.message : String(err)}`)
    }
    setSyncing(false)
  }, [])

  if (!loaded || workspaces.length === 0) return null

  return (
    <section className="px-5 py-4 border-t border-border-m">
      <div className="flex items-center justify-between mb-3">
        <div>
          <h2 className="text-sm font-semibold text-primary">
            Recent Workspaces
          </h2>
          <p className="text-xs text-muted mt-0.5">
            Virtual monorepo workspaces with symlinked repos
          </p>
        </div>
        <button
          type="button"
          onClick={() => void handleSync()}
          disabled={syncing}
          className="text-[10px] px-2 py-1 rounded bg-indigo-600/20 text-indigo-400 hover:bg-indigo-600/30 disabled:opacity-50 transition-colors"
          title="Pull latest dotAiAgent and re-sync .claude/ to all workspaces"
        >
          {syncing ? 'Syncing...' : 'Sync .claude'}
        </button>
      </div>
      {syncMsg && (
        <div className={`text-xs mb-2 px-2 py-1 rounded ${syncMsg.includes('failed') ? 'bg-red-500/10 text-red-400' : 'bg-green-500/10 text-green-400'}`}>
          {syncMsg}
        </div>
      )}
      <div className="space-y-2">
        {workspaces.map((ws) => (
          <WorkspaceCard
            key={ws.id || ws.path}
            ws={ws}
            onDelete={() => void fetchWorkspaces()}
            onRelaunch={onRelaunch}
          />
        ))}
      </div>
    </section>
  )
}
