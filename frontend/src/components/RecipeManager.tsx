import { useCallback, useEffect, useState } from 'react'
import { ListSessionTemplates } from '../../wailsjs/go/main/App'
import type { model } from '../../wailsjs/go/models'
import {
  callCreateRecipe,
  callGetRecipeWithDetails,
  getAppBinding,
  AGENT_OPTIONS,
  INPUT_CLS,
  LABEL_CLS,
  SELECT_CLS,
  type RecipeDetail,
} from '../lib/recipe-utils'
import { RunRecipeDialog } from './RunRecipeDialog'

// ---------------------------------------------------------------------------
// Create Recipe form
// ---------------------------------------------------------------------------

interface DraftParam {
  key: string // local temp key for React key prop
  name: string
  paramType: string
  defaultValue: string
  description: string
}

interface DraftStep {
  key: string
  agentType: string
  promptTemplate: string
  dependsOn: string
}

function CreateRecipeForm({ onCreated }: { onCreated: () => void }): React.ReactElement {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [params, setParams] = useState<DraftParam[]>([])
  const [steps, setSteps] = useState<DraftStep[]>([])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const addParam = (): void => {
    setParams((prev) => [
      ...prev,
      { key: crypto.randomUUID(), name: '', paramType: 'string', defaultValue: '', description: '' },
    ])
  }

  const removeParam = (key: string): void => {
    setParams((prev) => prev.filter((p) => p.key !== key))
  }

  const updateParam = (key: string, field: keyof DraftParam, value: string): void => {
    setParams((prev) => prev.map((p) => (p.key === key ? { ...p, [field]: value } : p)))
  }

  const addStep = (): void => {
    setSteps((prev) => [
      ...prev,
      { key: crypto.randomUUID(), agentType: 'claude-code', promptTemplate: '', dependsOn: '' },
    ])
  }

  const removeStep = (key: string): void => {
    setSteps((prev) => prev.filter((s) => s.key !== key))
  }

  const updateStep = (key: string, field: keyof DraftStep, value: string): void => {
    setSteps((prev) => prev.map((s) => (s.key === key ? { ...s, [field]: value } : s)))
  }

  const handleSave = useCallback(async (): Promise<void> => {
    if (!name.trim()) return
    setSaving(true)
    setError(null)
    try {
      const paramPayload = params.map((p, i) => ({
        name: p.name,
        paramType: p.paramType,
        defaultValue: p.defaultValue,
        description: p.description,
        sortOrder: i,
      }))
      const stepPayload = steps.map((s, i) => ({
        agentType: s.agentType,
        promptTemplate: s.promptTemplate,
        dependsOn: s.dependsOn,
        sortOrder: i,
      }))
      const result = await callCreateRecipe(name.trim(), paramPayload, stepPayload)
      if (result) {
        setName('')
        setParams([])
        setSteps([])
        setOpen(false)
        onCreated()
      } else {
        setError('CreateRecipe binding not available. Wire the Go binding first.')
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create recipe')
    } finally {
      setSaving(false)
    }
  }, [name, params, steps, onCreated])

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="w-full px-4 py-2.5 text-sm rounded-lg border border-dashed border-border
                   text-secondary hover:text-primary hover:border-muted transition-colors"
      >
        + Create Recipe
      </button>
    )
  }

  return (
    <div className="rounded-lg border border-acc-teal/30 bg-surface p-4 space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-primary">New Recipe</h3>
        <button
          type="button"
          onClick={() => setOpen(false)}
          className="text-muted hover:text-primary transition-colors"
          aria-label="Close create recipe form"
        >
          <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>

      {/* Name */}
      <div>
        <label htmlFor="recipe-name" className={LABEL_CLS}>Recipe Name</label>
        <input
          id="recipe-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Full Stack Feature"
          className={INPUT_CLS}
        />
      </div>

      {/* Params section */}
      <div>
        <div className="flex items-center justify-between mb-2">
          <span className="text-xs font-semibold uppercase tracking-wider text-secondary">Parameters</span>
          <button
            type="button"
            onClick={addParam}
            className="text-[11px] text-acc-teal hover:text-acc-teal/80 transition-colors"
          >
            + Add Param
          </button>
        </div>
        {params.length === 0 && (
          <p className="text-[11px] text-muted">No parameters. Params let you customize prompts at run time.</p>
        )}
        <div className="space-y-2">
          {params.map((p) => (
            <div key={p.key} className="rounded-md border border-border-m bg-app p-3 space-y-2">
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  value={p.name}
                  onChange={(e) => updateParam(p.key, 'name', e.target.value)}
                  placeholder="Param name"
                  className={INPUT_CLS + ' flex-1'}
                />
                <select
                  value={p.paramType}
                  onChange={(e) => updateParam(p.key, 'paramType', e.target.value)}
                  className={SELECT_CLS}
                >
                  <option value="string">String</option>
                  <option value="boolean">Boolean</option>
                  <option value="select">Select</option>
                </select>
                <button
                  type="button"
                  onClick={() => removeParam(p.key)}
                  className="text-muted hover:text-red-400 transition-colors"
                  aria-label="Remove parameter"
                >
                  <svg className="w-3.5 h-3.5" viewBox="0 0 20 20" fill="currentColor">
                    <path fillRule="evenodd" d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z" clipRule="evenodd" />
                  </svg>
                </button>
              </div>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={p.defaultValue}
                  onChange={(e) => updateParam(p.key, 'defaultValue', e.target.value)}
                  placeholder="Default value"
                  className={INPUT_CLS + ' flex-1'}
                />
                <input
                  type="text"
                  value={p.description}
                  onChange={(e) => updateParam(p.key, 'description', e.target.value)}
                  placeholder="Description"
                  className={INPUT_CLS + ' flex-1'}
                />
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Steps section */}
      <div>
        <div className="flex items-center justify-between mb-2">
          <span className="text-xs font-semibold uppercase tracking-wider text-secondary">Steps</span>
          <button
            type="button"
            onClick={addStep}
            className="text-[11px] text-acc-teal hover:text-acc-teal/80 transition-colors"
          >
            + Add Step
          </button>
        </div>
        {steps.length === 0 && (
          <p className="text-[11px] text-muted">No steps. Add at least one step with an agent and prompt.</p>
        )}
        <div className="space-y-2">
          {steps.map((s, idx) => (
            <div key={s.key} className="rounded-md border border-border-m bg-app p-3 space-y-2">
              <div className="flex items-center gap-2">
                <span className="text-[10px] text-muted font-bold w-5 text-center flex-shrink-0">
                  {String(idx + 1)}
                </span>
                <select
                  value={s.agentType}
                  onChange={(e) => updateStep(s.key, 'agentType', e.target.value)}
                  className={SELECT_CLS + ' flex-shrink-0'}
                >
                  {AGENT_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>{opt.label}</option>
                  ))}
                </select>
                <select
                  value={s.dependsOn}
                  onChange={(e) => updateStep(s.key, 'dependsOn', e.target.value)}
                  className={SELECT_CLS + ' flex-shrink-0'}
                >
                  <option value="">No dependency</option>
                  {steps
                    .filter((other) => other.key !== s.key)
                    .map((other, otherIdx) => (
                      <option key={other.key} value={other.key}>
                        After Step {String(otherIdx + 1)}
                      </option>
                    ))}
                </select>
                <button
                  type="button"
                  onClick={() => removeStep(s.key)}
                  className="ml-auto text-muted hover:text-red-400 transition-colors"
                  aria-label="Remove step"
                >
                  <svg className="w-3.5 h-3.5" viewBox="0 0 20 20" fill="currentColor">
                    <path fillRule="evenodd" d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z" clipRule="evenodd" />
                  </svg>
                </button>
              </div>
              <textarea
                value={s.promptTemplate}
                onChange={(e) => updateStep(s.key, 'promptTemplate', e.target.value)}
                placeholder="Prompt template. Use {paramName} for placeholders."
                rows={2}
                className={INPUT_CLS + ' resize-y'}
              />
            </div>
          ))}
        </div>
      </div>

      {/* Error */}
      {error !== null && (
        <div role="alert" className="text-xs text-red-400 bg-red-400/10 border border-red-500/20 rounded-md px-3 py-2">
          {error}
        </div>
      )}

      {/* Save */}
      <div className="flex justify-end">
        <button
          type="button"
          onClick={() => void handleSave()}
          disabled={!name.trim() || saving}
          className="px-4 py-2 text-xs font-medium text-white rounded-md
                     bg-acc-teal hover:bg-acc-teal/80 transition-colors
                     focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal
                     disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {saving ? 'Saving\u2026' : 'Save Recipe'}
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Recipe card (list item)
// ---------------------------------------------------------------------------

function RecipeCard({
  template,
  onRun,
  onDelete,
}: {
  template: model.SessionTemplate
  onRun: (id: string) => void
  onDelete: (id: string) => void
}): React.ReactElement {
  const [expanded, setExpanded] = useState(false)
  const [detail, setDetail] = useState<RecipeDetail | null>(null)
  const [loadingDetail, setLoadingDetail] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)

  const handleExpand = useCallback(async (): Promise<void> => {
    if (expanded) {
      setExpanded(false)
      return
    }
    setExpanded(true)
    if (!detail) {
      setLoadingDetail(true)
      try {
        const result = await callGetRecipeWithDetails(template.id)
        setDetail(result)
      } catch (err) {
        console.warn('Failed to load recipe details:', err)
      } finally {
        setLoadingDetail(false)
      }
    }
  }, [expanded, detail, template.id])

  const repoPaths: string[] = template.repoPaths ?? []

  return (
    <div className="rounded-lg border border-border bg-surface p-4 transition-colors hover:border-muted">
      {/* Header */}
      <div className="flex items-start justify-between gap-2">
        <button
          type="button"
          onClick={() => void handleExpand()}
          className="min-w-0 flex-1 text-left"
        >
          <h4 className="text-sm font-semibold text-primary truncate">{template.name}</h4>
          <div className="flex items-center gap-2 mt-1">
            {repoPaths.length > 0 && (
              <span className="text-[11px] px-2 py-0.5 rounded-full bg-border-m text-secondary flex-shrink-0">
                {String(repoPaths.length)} repo{repoPaths.length !== 1 ? 's' : ''}
              </span>
            )}
            <span className="text-[11px] font-mono text-muted truncate">
              {template.agentType || 'claude-code'}
            </span>
          </div>
        </button>
        <svg
          className={`w-4 h-4 text-muted transition-transform ${expanded ? 'rotate-180' : ''}`}
          viewBox="0 0 20 20"
          fill="currentColor"
        >
          <path fillRule="evenodd" d="M5.293 7.293a1 1 0 011.414 0L10 10.586l3.293-3.293a1 1 0 111.414 1.414l-4 4a1 1 0 01-1.414 0l-4-4a1 1 0 010-1.414z" clipRule="evenodd" />
        </svg>
      </div>

      {/* Expanded detail */}
      {expanded && (
        <div className="mt-3 space-y-3 border-t border-border-m pt-3">
          {loadingDetail && (
            <p className="text-[11px] text-muted">Loading recipe details...</p>
          )}

          {detail && detail.params.length > 0 && (
            <div>
              <span className="text-[10px] font-semibold uppercase tracking-wider text-secondary">Parameters</span>
              <div className="mt-1 space-y-1">
                {detail.params.map((p) => (
                  <div key={p.id} className="flex items-center gap-2 text-[11px]">
                    <span className="font-mono text-primary">{'{' + p.name + '}'}</span>
                    <span className="text-muted">({p.paramType})</span>
                    {p.defaultValue && <span className="text-secondary">= {p.defaultValue}</span>}
                    {p.description && <span className="text-muted">{'\u2014'} {p.description}</span>}
                  </div>
                ))}
              </div>
            </div>
          )}

          {detail && detail.steps.length > 0 && (
            <div>
              <span className="text-[10px] font-semibold uppercase tracking-wider text-secondary">Steps</span>
              <div className="mt-1 space-y-1">
                {detail.steps.map((s, idx) => (
                  <div key={s.id} className="flex items-start gap-2 text-[11px]">
                    <span className="text-muted font-bold flex-shrink-0 w-4 text-center">{String(idx + 1)}</span>
                    <span className="text-secondary flex-shrink-0">[{s.agentType}]</span>
                    <span className="text-primary truncate">{s.promptTemplate.slice(0, 80)}{s.promptTemplate.length > 80 ? '\u2026' : ''}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {detail === null && !loadingDetail && (
            <p className="text-[11px] text-muted">Detail view requires GetRecipeWithDetails binding.</p>
          )}
        </div>
      )}

      {/* Actions */}
      <div className="mt-3 flex items-center justify-end gap-2">
        {confirmDelete ? (
          <div className="flex items-center gap-1.5 mr-auto">
            <span className="text-[11px] text-red-400">Delete?</span>
            <button
              type="button"
              onClick={() => {
                onDelete(template.id)
                setConfirmDelete(false)
              }}
              className="px-2 py-1 text-[11px] font-medium rounded bg-red-600 hover:bg-red-500 text-white transition-colors"
            >
              Yes
            </button>
            <button
              type="button"
              onClick={() => setConfirmDelete(false)}
              className="px-2 py-1 text-[11px] font-medium rounded bg-border-m hover:bg-border text-secondary transition-colors"
            >
              No
            </button>
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setConfirmDelete(true)}
            aria-label={`Delete recipe ${template.name}`}
            className="mr-auto px-2.5 py-1.5 text-[11px] font-medium rounded
                       text-muted hover:text-red-400 hover:bg-red-400/10 transition-colors"
          >
            Delete
          </button>
        )}
        <button
          type="button"
          onClick={() => onRun(template.id)}
          className="px-3 py-1.5 text-[11px] font-medium rounded-md
                     bg-green-600 hover:bg-green-500 text-white transition-colors"
        >
          Run
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main RecipeManager component
// ---------------------------------------------------------------------------

export function RecipeManager(): React.ReactElement {
  const [templates, setTemplates] = useState<model.SessionTemplate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [runningId, setRunningId] = useState<string | null>(null)

  const loadTemplates = useCallback(async (): Promise<void> => {
    try {
      const list = await ListSessionTemplates()
      setTemplates(list ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load recipes')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadTemplates()
  }, [loadTemplates])

  const handleDelete = useCallback(async (id: string): Promise<void> => {
    try {
      const deleteFn = getAppBinding<void>('DeleteSessionTemplate')
      if (deleteFn) {
        await deleteFn(id)
      }
      setTemplates((prev) => prev.filter((t) => t.id !== id))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete recipe')
    }
  }, [])

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-secondary">Recipes</h3>
      </div>

      {/* Create form */}
      <CreateRecipeForm onCreated={() => void loadTemplates()} />

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
            className="ml-2 text-red-400 hover:text-red-300 transition-colors rounded"
            aria-label="Dismiss error"
          >
            <svg className="w-3.5 h-3.5" viewBox="0 0 20 20" fill="currentColor">
              <path fillRule="evenodd" d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z" clipRule="evenodd" />
            </svg>
          </button>
        </div>
      )}

      {/* Loading */}
      {loading && (
        <div className="flex items-center justify-center py-6">
          <svg className="w-4 h-4 animate-spin text-secondary" viewBox="0 0 24 24" fill="none">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
          <span className="ml-2 text-xs text-secondary">Loading recipes...</span>
        </div>
      )}

      {/* Empty state */}
      {!loading && templates.length === 0 && (
        <div className="rounded-lg border border-dashed border-border bg-app px-6 py-8 text-center">
          <svg className="mx-auto w-8 h-8 text-border mb-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
            <path strokeLinecap="round" strokeLinejoin="round" d="M9 12h3.75M9 15h3.75M9 18h3.75m3 .75H18a2.25 2.25 0 002.25-2.25V6.108c0-1.135-.845-2.098-1.976-2.192a48.424 48.424 0 00-1.123-.08m-5.801 0c-.065.21-.1.433-.1.664 0 .414.336.75.75.75h4.5a.75.75 0 00.75-.75 2.25 2.25 0 00-.1-.664m-5.8 0A2.251 2.251 0 0113.5 2.25H15c1.012 0 1.867.668 2.15 1.586m-5.8 0c-.376.023-.75.05-1.124.08C9.095 4.01 8.25 4.973 8.25 6.108V8.25m0 0H4.875c-.621 0-1.125.504-1.125 1.125v11.25c0 .621.504 1.125 1.125 1.125h9.75c.621 0 1.125-.504 1.125-1.125V9.375c0-.621-.504-1.125-1.125-1.125H8.25zM6.75 12h.008v.008H6.75V12zm0 3h.008v.008H6.75V15zm0 3h.008v.008H6.75V18z" />
          </svg>
          <p className="text-sm text-secondary">No recipes yet.</p>
          <p className="text-xs text-muted mt-1">Create a multi-step recipe with params and agent steps.</p>
        </div>
      )}

      {/* Recipe list */}
      {!loading && templates.length > 0 && (
        <div className="grid gap-3">
          {templates.map((template) => (
            <RecipeCard
              key={template.id}
              template={template}
              onRun={(id) => setRunningId(id)}
              onDelete={(id) => void handleDelete(id)}
            />
          ))}
        </div>
      )}

      {/* Run dialog */}
      {runningId !== null && (
        <RunRecipeDialog
          templateId={runningId}
          onClose={() => setRunningId(null)}
        />
      )}
    </div>
  )
}
