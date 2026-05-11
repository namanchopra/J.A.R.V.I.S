import { useCallback, useEffect, useState } from 'react'
import {
  DeleteSessionTemplate,
  LaunchFromTemplate,
  ListSessionTemplates,
  SaveSessionTemplate,
} from '../../wailsjs/go/main/App'
import type { model } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface TemplateManagerProps {
  selectedRepos?: { name: string; path: string }[]
  showSave?: boolean
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Extract the last path segment as a short display name. */
function repoDisplayName(fullPath: string): string {
  return fullPath.split('/').pop() ?? fullPath
}

/** Truncate a command string for preview. */
function truncateCommand(cmd: string, maxLen: number): string {
  if (cmd.length <= maxLen) return cmd
  return cmd.slice(0, maxLen - 1) + '\u2026'
}

// ---------------------------------------------------------------------------
// Save-as-template form
// ---------------------------------------------------------------------------

interface SaveFormProps {
  repos: { name: string; path: string }[]
  onSaved: (template: model.SessionTemplate) => void
}

function SaveTemplateForm({ repos, onSaved }: SaveFormProps): React.ReactElement {
  const [name, setName] = useState('')
  const [command, setCommand] = useState('claude')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const canSave = name.trim().length > 0 && repos.length > 0 && !saving

  const handleSave = useCallback(async (): Promise<void> => {
    if (!canSave) return
    setSaving(true)
    setError(null)
    try {
      const repoPaths = repos.map((r) => r.path)
      const template = await SaveSessionTemplate(
        name.trim(),
        'claude-code',
        repoPaths,
        command.trim() || 'claude',
      )
      setName('')
      setCommand('claude')
      onSaved(template)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save template')
    } finally {
      setSaving(false)
    }
  }, [canSave, name, command, repos, onSaved])

  const inputClasses =
    'w-full rounded-md bg-app border border-border text-sm ' +
    'text-primary px-3 py-2 placeholder-muted focus:outline-none ' +
    'focus-visible:ring-2 focus-visible:ring-acc-teal focus:border-acc-teal'

  const labelClasses = 'block text-xs font-medium text-secondary mb-1'

  return (
    <div className="rounded-lg border border-acc-teal/30 bg-surface p-4 space-y-3">
      <h3 className="text-sm font-semibold text-primary">Save as Template</h3>

      {/* Repo summary */}
      <div className="rounded-md bg-app border border-border-m px-3 py-2">
        <span className="text-[11px] text-secondary">
          {String(repos.length)} repo{repos.length !== 1 ? 's' : ''} selected:
        </span>
        <div className="flex flex-wrap gap-1.5 mt-1.5">
          {repos.map((r) => (
            <span
              key={r.path}
              className="text-[11px] bg-border-m text-secondary px-2 py-0.5 rounded-full"
            >
              {r.name}
            </span>
          ))}
        </div>
      </div>

      {/* Name input */}
      <div>
        <label htmlFor="tmpl-save-name" className={labelClasses}>
          Template Name
        </label>
        <input
          id="tmpl-save-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Frontend + API"
          className={inputClasses}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void handleSave()
          }}
        />
      </div>

      {/* Command input */}
      <div>
        <label htmlFor="tmpl-save-cmd" className={labelClasses}>
          Command
        </label>
        <input
          id="tmpl-save-cmd"
          type="text"
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          placeholder="claude"
          className={inputClasses}
        />
      </div>

      {/* Error */}
      {error !== null && (
        <div
          role="alert"
          className="text-xs text-red-400 bg-red-400/10 border border-red-500/20 rounded-md px-3 py-2"
        >
          {error}
        </div>
      )}

      {/* Save button */}
      <div className="flex justify-end">
        <button
          type="button"
          onClick={() => void handleSave()}
          disabled={!canSave}
          className="px-4 py-2 text-xs font-medium text-white rounded-md
                     bg-acc-teal hover:bg-acc-teal/80 transition-colors
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal
                     disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {saving ? 'Saving\u2026' : 'Save Template'}
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Template card
// ---------------------------------------------------------------------------

interface TemplateCardProps {
  template: model.SessionTemplate
  onLaunch: (id: string) => void
  onDelete: (id: string) => void
  launching: boolean
}

function TemplateCard({
  template,
  onLaunch,
  onDelete,
  launching,
}: TemplateCardProps): React.ReactElement {
  const [confirmDelete, setConfirmDelete] = useState(false)

  const repoPaths: string[] = template.repoPaths ?? []
  const visibleRepos = repoPaths.slice(0, 3)
  const hiddenCount = repoPaths.length - visibleRepos.length

  return (
    <div className="rounded-lg border border-border bg-surface p-4 transition-colors hover:border-muted">
      {/* Header row: name + badges */}
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <h4 className="text-sm font-semibold text-primary truncate">
            {template.name}
          </h4>
          <div className="flex items-center gap-2 mt-1">
            <span className="text-[11px] px-2 py-0.5 rounded-full bg-border-m text-secondary flex-shrink-0">
              {String(repoPaths.length)} repo{repoPaths.length !== 1 ? 's' : ''}
            </span>
            <span className="text-[11px] font-mono text-muted truncate">
              {truncateCommand(template.command || 'claude', 40)}
            </span>
          </div>
        </div>
      </div>

      {/* Repo paths */}
      {repoPaths.length > 0 && (
        <div className="mt-3 space-y-1">
          {visibleRepos.map((repoPath) => (
            <div
              key={repoPath}
              className="flex items-center gap-1.5 text-[11px] text-secondary"
            >
              <svg
                className="w-3 h-3 text-muted flex-shrink-0"
                viewBox="0 0 16 16"
                fill="currentColor"
                aria-hidden="true"
              >
                <path
                  fillRule="evenodd"
                  d="M2 2.5A2.5 2.5 0 014.5 0h8.75a.75.75 0 01.75.75v12.5a.75.75
                     0 01-.75.75h-2.5a.75.75 0 110-1.5h1.75v-2h-8a1 1 0 00-.714
                     1.7.75.75 0 01-1.072 1.05A2.495 2.495 0 012 11.5v-9zm10.5-1h-8a1
                     1 0 00-1 1v6.708A2.486 2.486 0 014.5 9h8.5V1.5zm-6.25 3.75a.75.75
                     0 01.75-.75h3.5a.75.75 0 010 1.5h-3.5a.75.75 0 01-.75-.75z"
                  clipRule="evenodd"
                />
              </svg>
              <span className="truncate font-mono">{repoDisplayName(repoPath)}</span>
              <span className="text-muted truncate hidden sm:inline">
                {repoPath}
              </span>
            </div>
          ))}
          {hiddenCount > 0 && (
            <p className="text-[11px] text-muted pl-[18px]">
              +{String(hiddenCount)} more
            </p>
          )}
        </div>
      )}

      {/* Actions row */}
      <div className="mt-3 flex items-center justify-end gap-2">
        {/* Delete button (with confirmation) */}
        {confirmDelete ? (
          <div className="flex items-center gap-1.5 mr-auto">
            <span className="text-[11px] text-red-400">Delete?</span>
            <button
              type="button"
              onClick={() => {
                onDelete(template.id)
                setConfirmDelete(false)
              }}
              className="px-2 py-1 text-[11px] font-medium rounded
                         bg-red-600 hover:bg-red-500 text-white transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500"
            >
              Yes
            </button>
            <button
              type="button"
              onClick={() => setConfirmDelete(false)}
              className="px-2 py-1 text-[11px] font-medium rounded
                         bg-border-m hover:bg-border text-secondary transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal"
            >
              No
            </button>
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setConfirmDelete(true)}
            aria-label={`Delete template ${template.name}`}
            className="mr-auto px-2.5 py-1.5 text-[11px] font-medium rounded
                       text-muted hover:text-red-400 hover:bg-red-400/10
                       transition-colors focus:outline-none
                       focus-visible:ring-2 focus-visible:ring-red-500"
          >
            Delete
          </button>
        )}

        {/* Launch button */}
        <button
          type="button"
          onClick={() => onLaunch(template.id)}
          disabled={launching}
          className="px-3 py-1.5 text-[11px] font-medium rounded-md
                     bg-green-600 hover:bg-green-500 text-white transition-colors
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-green-500
                     disabled:opacity-40 disabled:cursor-not-allowed
                     flex items-center gap-1.5"
        >
          {launching ? (
            <>
              <LaunchSpinner />
              Launching...
            </>
          ) : (
            'Launch'
          )}
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Spinner
// ---------------------------------------------------------------------------

function LaunchSpinner(): React.ReactElement {
  return (
    <svg
      className="w-3 h-3 animate-spin"
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

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

function EmptyState(): React.ReactElement {
  return (
    <div className="rounded-lg border border-dashed border-border bg-app px-6 py-10 text-center">
      <svg
        className="mx-auto w-8 h-8 text-border mb-3"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        aria-hidden="true"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125
             1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25
             0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125
             1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0
             00-9-9z"
        />
      </svg>
      <p className="text-sm text-secondary">No templates saved.</p>
      <p className="text-xs text-muted mt-1">
        Select repos above and save as template.
      </p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export function TemplateManager({
  selectedRepos,
  showSave = false,
}: TemplateManagerProps): React.ReactElement {
  const [templates, setTemplates] = useState<model.SessionTemplate[]>([])
  const [loading, setLoading] = useState(true)
  const [launchingId, setLaunchingId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  // ---- Load templates ----
  const loadTemplates = useCallback(async (): Promise<void> => {
    try {
      const list = await ListSessionTemplates()
      setTemplates(list ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load templates')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadTemplates()
  }, [loadTemplates])

  // ---- Launch ----
  const handleLaunch = useCallback(async (id: string): Promise<void> => {
    setLaunchingId(id)
    setError(null)
    try {
      await LaunchFromTemplate(id)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to launch template')
    } finally {
      setLaunchingId(null)
    }
  }, [])

  // ---- Delete ----
  const handleDelete = useCallback(
    async (id: string): Promise<void> => {
      setError(null)
      try {
        await DeleteSessionTemplate(id)
        setTemplates((prev) => prev.filter((t) => t.id !== id))
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to delete template')
      }
    },
    [],
  )

  // ---- Saved callback ----
  const handleSaved = useCallback((template: model.SessionTemplate): void => {
    setTemplates((prev) => [template, ...prev])
  }, [])

  // ---- Render ----
  return (
    <div className="space-y-4">
      {/* Save form (conditional) */}
      {showSave === true &&
        selectedRepos !== undefined &&
        selectedRepos.length > 0 && (
          <SaveTemplateForm repos={selectedRepos} onSaved={handleSaved} />
        )}

      {/* Error banner */}
      {error !== null && (
        <div
          role="alert"
          className="flex items-center justify-between text-xs text-red-400
                     bg-red-400/10 border border-red-500/20 rounded-md px-3 py-2"
        >
          <span>{error}</span>
          <button
            type="button"
            onClick={() => setError(null)}
            className="ml-2 text-red-400 hover:text-red-300 transition-colors
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-red-500 rounded"
            aria-label="Dismiss error"
          >
            <svg
              className="w-3.5 h-3.5"
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
      )}

      {/* Loading state */}
      {loading && (
        <div className="flex items-center justify-center py-8">
          <LaunchSpinner />
          <span className="ml-2 text-xs text-secondary">Loading templates...</span>
        </div>
      )}

      {/* Template list or empty state */}
      {!loading && templates.length === 0 && <EmptyState />}

      {!loading && templates.length > 0 && (
        <div className="space-y-3">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-secondary">
            Saved Templates
          </h3>
          <div className="grid gap-3">
            {templates.map((template) => (
              <TemplateCard
                key={template.id}
                template={template}
                onLaunch={(id) => void handleLaunch(id)}
                onDelete={(id) => void handleDelete(id)}
                launching={launchingId === template.id}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
