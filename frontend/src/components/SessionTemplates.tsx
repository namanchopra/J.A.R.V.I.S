import { useCallback, useEffect, useState } from 'react'
import { GetTasks, LaunchSession } from '../../wailsjs/go/main/App'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface SessionTemplate {
  id: string
  name: string
  prompt: string
  agentType: string
  icon: string
}

export interface TemplateSelection {
  agentType: string
  prompt: string
}

interface SessionTemplatesProps {
  onSelectTemplate: (selection: TemplateSelection) => void
  onBatchLaunchComplete?: () => void
}

// ---------------------------------------------------------------------------
// Built-in templates
// ---------------------------------------------------------------------------

const BUILTIN_TEMPLATES: readonly SessionTemplate[] = [
  {
    id: 'builtin-start-project',
    name: 'Start Project',
    prompt: 'Use the /start skill to analyze this project',
    agentType: 'claude-code',
    icon: '\u{1F680}',
  },
  {
    id: 'builtin-map-project',
    name: 'Map Project',
    prompt: 'Use the /map-project skill to generate CLAUDE.md',
    agentType: 'claude-code',
    icon: '\u{1F5FA}\uFE0F',
  },
  {
    id: 'builtin-review-code',
    name: 'Review Code',
    prompt:
      'Review the current branch for bugs, security issues, and code quality',
    agentType: 'claude-code',
    icon: '\u{1F50D}',
  },
  {
    id: 'builtin-fix-bugs',
    name: 'Fix Bugs',
    prompt: 'Find and fix bugs in the current branch',
    agentType: 'claude-code',
    icon: '\u{1F41B}',
  },
  {
    id: 'builtin-write-tests',
    name: 'Write Tests',
    prompt: 'Write tests for untested code in this project',
    agentType: 'claude-code',
    icon: '\u{1F9EA}',
  },
] as const

const STORAGE_KEY = 'awm-session-templates'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function loadCustomTemplates(): SessionTemplate[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw === null) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed as SessionTemplate[]
  } catch (err) {
    console.warn('Failed to parse custom templates from localStorage:', err)
    return []
  }
}

function saveCustomTemplates(templates: SessionTemplate[]): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(templates))
}

function truncatePrompt(prompt: string, maxLen: number): string {
  if (prompt.length <= maxLen) return prompt
  return prompt.slice(0, maxLen - 1) + '\u2026'
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function SessionTemplates({
  onSelectTemplate,
  onBatchLaunchComplete,
}: SessionTemplatesProps): React.ReactElement {
  const [customTemplates, setCustomTemplates] = useState<SessionTemplate[]>(
    loadCustomTemplates,
  )
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [batchMode, setBatchMode] = useState(false)

  return (
    <div className="space-y-4">
      {/* Built-in templates */}
      <div>
        <h3 className="text-xs font-semibold uppercase tracking-wider text-secondary mb-2">
          Quick Templates
        </h3>
        <TemplateGrid
          templates={[...BUILTIN_TEMPLATES]}
          onSelect={onSelectTemplate}
          onDelete={undefined}
        />
      </div>

      {/* Custom templates */}
      {customTemplates.length > 0 && (
        <div>
          <h3 className="text-xs font-semibold uppercase tracking-wider text-secondary mb-2">
            Custom Templates
          </h3>
          <TemplateGrid
            templates={customTemplates}
            onSelect={onSelectTemplate}
            onDelete={(id) => {
              const updated = customTemplates.filter((t) => t.id !== id)
              setCustomTemplates(updated)
              saveCustomTemplates(updated)
            }}
          />
        </div>
      )}

      {/* Action buttons */}
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => setShowCreateForm((prev) => !prev)}
          className="text-xs text-blue-400 hover:text-blue-300 transition-colors
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 rounded px-2 py-1"
        >
          {showCreateForm ? 'Cancel' : '+ Create Template'}
        </button>
        <button
          type="button"
          onClick={() => setBatchMode((prev) => !prev)}
          className={`text-xs transition-colors focus:outline-none
                      focus-visible:ring-2 focus-visible:ring-blue-500 rounded px-2 py-1
                      ${batchMode ? 'text-amber-400 hover:text-amber-300' : 'text-secondary hover:text-primary'}`}
        >
          {batchMode ? 'Exit Batch Mode' : 'Batch Launch'}
        </button>
      </div>

      {/* Create template form */}
      {showCreateForm && (
        <CreateTemplateForm
          onCreated={(template) => {
            const updated = [...customTemplates, template]
            setCustomTemplates(updated)
            saveCustomTemplates(updated)
            setShowCreateForm(false)
          }}
          onCancel={() => setShowCreateForm(false)}
        />
      )}

      {/* Batch launch panel */}
      {batchMode && (
        <BatchLaunchPanel
          templates={[...BUILTIN_TEMPLATES, ...customTemplates]}
          onComplete={() => {
            setBatchMode(false)
            onBatchLaunchComplete?.()
          }}
        />
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Template Grid
// ---------------------------------------------------------------------------

interface TemplateGridProps {
  templates: SessionTemplate[]
  onSelect: (selection: TemplateSelection) => void
  onDelete: ((id: string) => void) | undefined
}

function TemplateGrid({
  templates,
  onSelect,
  onDelete,
}: TemplateGridProps): React.ReactElement {
  return (
    <div className="grid grid-cols-3 gap-2">
      {templates.map((template) => (
        <TemplateCard
          key={template.id}
          template={template}
          onSelect={onSelect}
          onDelete={onDelete}
        />
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Template Card
// ---------------------------------------------------------------------------

interface TemplateCardProps {
  template: SessionTemplate
  onSelect: (selection: TemplateSelection) => void
  onDelete: ((id: string) => void) | undefined
}

function TemplateCard({
  template,
  onSelect,
  onDelete,
}: TemplateCardProps): React.ReactElement {
  return (
    <button
      type="button"
      onClick={() =>
        onSelect({ agentType: template.agentType, prompt: template.prompt })
      }
      className="group relative flex flex-col items-start gap-1 rounded-md
                 bg-border-m/60 hover:bg-border-m border border-border/50
                 hover:border-border px-3 py-2.5 text-left transition-colors
                 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
    >
      <div className="flex items-center gap-1.5 w-full">
        <span className="text-base leading-none" aria-hidden="true">
          {template.icon}
        </span>
        <span className="text-xs font-medium text-primary truncate">
          {template.name}
        </span>
      </div>
      <p className="text-[11px] text-muted leading-tight line-clamp-2">
        {truncatePrompt(template.prompt, 80)}
      </p>

      {/* Delete button for custom templates */}
      {onDelete !== undefined && (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation()
            onDelete(template.id)
          }}
          aria-label={`Delete template ${template.name}`}
          className="absolute top-1 right-1 hidden group-hover:flex items-center
                     justify-center w-4 h-4 rounded text-muted hover:text-red-400
                     hover:bg-red-400/10 transition-colors focus:outline-none
                     focus-visible:ring-2 focus-visible:ring-red-500"
        >
          <svg
            className="w-3 h-3"
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
      )}
    </button>
  )
}

// ---------------------------------------------------------------------------
// Create Template Form
// ---------------------------------------------------------------------------

interface CreateTemplateFormProps {
  onCreated: (template: SessionTemplate) => void
  onCancel: () => void
}

function CreateTemplateForm({
  onCreated,
  onCancel,
}: CreateTemplateFormProps): React.ReactElement {
  const [name, setName] = useState('')
  const [prompt, setPrompt] = useState('')
  const [agentType, setAgentType] = useState('claude-code')
  const [icon, setIcon] = useState('')

  const canCreate = name.trim() !== '' && prompt.trim() !== ''

  const handleCreate = useCallback(() => {
    if (!canCreate) return
    const template: SessionTemplate = {
      id: `custom-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      name: name.trim(),
      prompt: prompt.trim(),
      agentType: agentType.trim() || 'claude-code',
      icon: icon.trim() || '\u{2B50}',
    }
    onCreated(template)
  }, [name, prompt, agentType, icon, canCreate, onCreated])

  const inputClasses =
    'w-full rounded-md bg-border-m border border-border text-sm ' +
    'text-primary px-2.5 py-1.5 placeholder-gray-500 focus:outline-none ' +
    'focus-visible:ring-2 focus-visible:ring-blue-500'

  const labelClasses = 'block text-xs font-medium text-secondary mb-0.5'

  return (
    <div className="rounded-md bg-border-m/40 border border-border/50 p-3 space-y-3">
      <h4 className="text-xs font-semibold text-primary">New Template</h4>
      <div className="grid grid-cols-[1fr_80px] gap-2">
        <div>
          <label htmlFor="tpl-name" className={labelClasses}>
            Name
          </label>
          <input
            id="tpl-name"
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="My Template"
            className={inputClasses}
          />
        </div>
        <div>
          <label htmlFor="tpl-icon" className={labelClasses}>
            Icon
          </label>
          <input
            id="tpl-icon"
            type="text"
            value={icon}
            onChange={(e) => setIcon(e.target.value)}
            placeholder="\u{2B50}"
            maxLength={4}
            className={inputClasses}
          />
        </div>
      </div>
      <div>
        <label htmlFor="tpl-prompt" className={labelClasses}>
          Prompt
        </label>
        <textarea
          id="tpl-prompt"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          placeholder="Describe what the agent should do..."
          rows={2}
          className={inputClasses + ' resize-none'}
        />
      </div>
      <div>
        <label htmlFor="tpl-agent" className={labelClasses}>
          Agent Type
        </label>
        <input
          id="tpl-agent"
          type="text"
          value={agentType}
          onChange={(e) => setAgentType(e.target.value)}
          placeholder="claude-code"
          className={inputClasses}
        />
      </div>
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={onCancel}
          className="px-3 py-1.5 text-xs font-medium text-secondary rounded-md
                     bg-border-m hover:bg-border transition-colors
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={handleCreate}
          disabled={!canCreate}
          className="px-3 py-1.5 text-xs font-medium text-white rounded-md
                     bg-blue-600 hover:bg-blue-500 transition-colors
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500
                     disabled:opacity-50"
        >
          Create
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Batch Launch Panel
// ---------------------------------------------------------------------------

interface BatchLaunchPanelProps {
  templates: SessionTemplate[]
  onComplete: () => void
}

function BatchLaunchPanel({
  templates,
  onComplete,
}: BatchLaunchPanelProps): React.ReactElement {
  const [repoPaths, setRepoPaths] = useState<string[]>([])
  const [selectedRepos, setSelectedRepos] = useState<Set<string>>(new Set())
  const [selectedTemplateId, setSelectedTemplateId] = useState(
    templates[0]?.id ?? '',
  )
  const [launching, setLaunching] = useState(false)
  const [progress, setProgress] = useState<{
    current: number
    total: number
  } | null>(null)
  const [batchError, setBatchError] = useState<string | null>(null)

  // Fetch recent repo paths
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
        console.warn('Failed to fetch repo paths for templates:', err)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [])

  const toggleRepo = useCallback((path: string) => {
    setSelectedRepos((prev) => {
      const next = new Set(prev)
      if (next.has(path)) {
        next.delete(path)
      } else {
        next.add(path)
      }
      return next
    })
  }, [])

  const selectAll = useCallback(() => {
    setSelectedRepos(new Set(repoPaths))
  }, [repoPaths])

  const selectNone = useCallback(() => {
    setSelectedRepos(new Set())
  }, [])

  const selectedTemplate = templates.find((t) => t.id === selectedTemplateId)

  const canLaunch =
    selectedRepos.size > 0 && selectedTemplate !== undefined && !launching

  const handleLaunchAll = useCallback(async () => {
    if (!canLaunch || selectedTemplate === undefined) return

    setLaunching(true)
    setBatchError(null)
    const repos = Array.from(selectedRepos)
    setProgress({ current: 0, total: repos.length })

    let errorCount = 0
    for (let i = 0; i < repos.length; i++) {
      const repo = repos[i]
      if (repo === undefined) continue
      try {
        await LaunchSession(selectedTemplate.agentType, repo, selectedTemplate.prompt)
      } catch (err) {
        console.warn(`Failed to launch session for ${repo}:`, err)
        errorCount++
      }
      setProgress({ current: i + 1, total: repos.length })
    }

    setLaunching(false)
    if (errorCount > 0) {
      setBatchError(
        `Launched with ${String(errorCount)} error${errorCount > 1 ? 's' : ''}`,
      )
    } else {
      onComplete()
    }
  }, [canLaunch, selectedTemplate, selectedRepos, onComplete])

  return (
    <div className="rounded-md bg-border-m/40 border border-amber-600/30 p-3 space-y-3">
      <h4 className="text-xs font-semibold text-amber-400">Batch Launch</h4>

      {/* Template selector */}
      <div>
        <label
          htmlFor="batch-template"
          className="block text-xs font-medium text-secondary mb-0.5"
        >
          Template
        </label>
        <select
          id="batch-template"
          value={selectedTemplateId}
          onChange={(e) => setSelectedTemplateId(e.target.value)}
          className="w-full rounded-md bg-border-m border border-border text-sm
                     text-primary px-2.5 py-1.5 focus:outline-none
                     focus-visible:ring-2 focus-visible:ring-blue-500"
        >
          {templates.map((t) => (
            <option key={t.id} value={t.id}>
              {t.icon} {t.name}
            </option>
          ))}
        </select>
      </div>

      {/* Repo multi-select */}
      <div>
        <div className="flex items-center justify-between mb-1">
          <span className="text-xs font-medium text-secondary">
            Repositories ({String(selectedRepos.size)}/{String(repoPaths.length)})
          </span>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={selectAll}
              className="text-[11px] text-blue-400 hover:text-blue-300 transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 rounded"
            >
              All
            </button>
            <button
              type="button"
              onClick={selectNone}
              className="text-[11px] text-secondary hover:text-primary transition-colors
                         focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 rounded"
            >
              None
            </button>
          </div>
        </div>

        {repoPaths.length === 0 ? (
          <p className="text-xs text-muted py-2">
            No repositories found. Launch a session first to populate this list.
          </p>
        ) : (
          <div className="max-h-32 overflow-y-auto rounded-md bg-elevated/50 border border-border/50 divide-y divide-border/50">
            {repoPaths.map((path) => (
              <label
                key={path}
                className="flex items-center gap-2 px-2.5 py-1.5 hover:bg-border-m/50
                           cursor-pointer transition-colors"
              >
                <input
                  type="checkbox"
                  checked={selectedRepos.has(path)}
                  onChange={() => toggleRepo(path)}
                  className="rounded border-border bg-border-m text-blue-500
                             focus:ring-blue-500 focus:ring-offset-0 h-3.5 w-3.5"
                />
                <span className="text-xs text-primary truncate">{path}</span>
              </label>
            ))}
          </div>
        )}
      </div>

      {/* Error */}
      {batchError !== null && (
        <div
          role="alert"
          className="text-xs text-red-400 bg-red-400/10 rounded-md px-2.5 py-1.5"
        >
          {batchError}
        </div>
      )}

      {/* Progress */}
      {progress !== null && launching && (
        <div className="space-y-1">
          <div className="flex items-center justify-between text-xs text-secondary">
            <span>Launching sessions...</span>
            <span>
              {String(progress.current)}/{String(progress.total)}
            </span>
          </div>
          <div className="w-full bg-border rounded-full h-1.5">
            <div
              className="bg-blue-500 h-1.5 rounded-full transition-all duration-300"
              style={{
                width: `${String(
                  progress.total > 0
                    ? Math.round((progress.current / progress.total) * 100)
                    : 0,
                )}%`,
              }}
            />
          </div>
        </div>
      )}

      {/* Launch button */}
      <div className="flex justify-end">
        <button
          type="button"
          onClick={() => void handleLaunchAll()}
          disabled={!canLaunch}
          className="px-3 py-1.5 text-xs font-medium text-white rounded-md
                     bg-amber-600 hover:bg-amber-500 transition-colors
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-500
                     disabled:opacity-50 flex items-center gap-1.5"
        >
          {launching && <BatchSpinner />}
          {launching
            ? `Launching ${String(progress?.current ?? 0)}/${String(progress?.total ?? 0)}...`
            : `Launch All (${String(selectedRepos.size)})`}
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Icons
// ---------------------------------------------------------------------------

function BatchSpinner(): React.ReactElement {
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
