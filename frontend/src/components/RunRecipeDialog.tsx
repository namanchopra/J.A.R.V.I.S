import { useCallback, useEffect, useState } from 'react'
import {
  callGetRecipeWithDetails,
  getAppBinding,
  INPUT_CLS,
  LABEL_CLS,
  type RecipeDetail,
} from '../lib/recipe-utils'

// ---------------------------------------------------------------------------
// Run dialog (param inputs before execution)
// ---------------------------------------------------------------------------

export function RunRecipeDialog({
  templateId,
  onClose,
}: {
  templateId: string
  onClose: () => void
}): React.ReactElement {
  const [detail, setDetail] = useState<RecipeDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [paramValues, setParamValues] = useState<Record<string, string>>({})
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    callGetRecipeWithDetails(templateId)
      .then((result) => {
        if (cancelled) return
        setDetail(result)
        if (result) {
          const defaults: Record<string, string> = {}
          for (const p of result.params) {
            defaults[p.name] = p.defaultValue
          }
          setParamValues(defaults)
        }
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load recipe')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [templateId])

  const handleRun = useCallback(async (): Promise<void> => {
    if (!detail) return
    setRunning(true)
    setError(null)
    try {
      // Build workflow phases from steps, substituting param values into prompts
      const phases = detail.steps.map((step) => {
        let prompt = step.promptTemplate
        for (const [paramName, paramValue] of Object.entries(paramValues)) {
          prompt = prompt.replaceAll(`{${paramName}}`, paramValue)
        }
        return {
          agentType: step.agentType,
          prompt,
          dependsOn: step.dependsOn,
          sortOrder: step.sortOrder,
        }
      })

      const executeFn = getAppBinding<void>('ExecuteWorkflow')
      if (executeFn) {
        await executeFn(phases)
        onClose()
      } else {
        setError('ExecuteWorkflow binding not available yet.')
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to run recipe')
    } finally {
      setRunning(false)
    }
  }, [detail, paramValues, onClose])

  // Close on Escape
  useEffect(() => {
    const handler = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="w-full max-w-md bg-app border border-border rounded-xl shadow-2xl overflow-hidden">
        <div className="px-5 py-3 border-b border-border bg-surface flex items-center justify-between">
          <h3 className="text-sm font-semibold text-primary">
            Run Recipe{detail ? `: ${detail.template.name}` : ''}
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="text-muted hover:text-primary transition-colors"
            aria-label="Close dialog"
          >
            <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M18 6L6 18M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div className="p-5 space-y-4 max-h-[60vh] overflow-y-auto">
          {loading && <p className="text-sm text-muted">Loading recipe...</p>}

          {!loading && !detail && (
            <p className="text-sm text-muted">GetRecipeWithDetails binding not available yet.</p>
          )}

          {detail && detail.params.length > 0 && (
            <div className="space-y-3">
              <span className="text-xs font-semibold uppercase tracking-wider text-secondary">Parameters</span>
              {detail.params.map((p) => (
                <div key={p.id}>
                  <label className={LABEL_CLS}>
                    {p.name}
                    {p.description && (
                      <span className="ml-1 font-normal text-muted">({p.description})</span>
                    )}
                  </label>
                  {p.paramType === 'boolean' ? (
                    <label className="flex items-center gap-2 text-sm text-primary cursor-pointer">
                      <input
                        type="checkbox"
                        checked={paramValues[p.name] === 'true'}
                        onChange={(e) =>
                          setParamValues((prev) => ({ ...prev, [p.name]: e.target.checked ? 'true' : 'false' }))
                        }
                        className="rounded border-border text-acc-teal focus:ring-acc-teal"
                      />
                      {p.name}
                    </label>
                  ) : (
                    <input
                      type="text"
                      value={paramValues[p.name] ?? ''}
                      onChange={(e) =>
                        setParamValues((prev) => ({ ...prev, [p.name]: e.target.value }))
                      }
                      placeholder={p.defaultValue || p.name}
                      className={INPUT_CLS}
                    />
                  )}
                </div>
              ))}
            </div>
          )}

          {detail && detail.params.length === 0 && (
            <p className="text-sm text-muted">This recipe has no configurable parameters.</p>
          )}

          {detail && detail.steps.length > 0 && (
            <div>
              <span className="text-[10px] font-semibold uppercase tracking-wider text-secondary">
                {String(detail.steps.length)} step{detail.steps.length !== 1 ? 's' : ''} will execute
              </span>
              <div className="mt-1 space-y-1">
                {detail.steps.map((s, idx) => (
                  <div key={s.id} className="text-[11px] text-muted">
                    {String(idx + 1)}. [{s.agentType}] {s.promptTemplate.slice(0, 60)}{s.promptTemplate.length > 60 ? '\u2026' : ''}
                  </div>
                ))}
              </div>
            </div>
          )}

          {error !== null && (
            <div role="alert" className="text-xs text-red-400 bg-red-400/10 border border-red-500/20 rounded-md px-3 py-2">
              {error}
            </div>
          )}
        </div>

        <div className="px-5 py-3 border-t border-border bg-surface flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 text-xs font-medium rounded-md text-secondary
                       hover:text-primary border border-border hover:border-muted transition-colors"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => void handleRun()}
            disabled={running || !detail}
            className="px-4 py-1.5 text-xs font-medium text-white rounded-md
                       bg-green-600 hover:bg-green-500 transition-colors
                       disabled:opacity-40 disabled:cursor-not-allowed"
          >
            {running ? 'Running\u2026' : 'Run Recipe'}
          </button>
        </div>
      </div>
    </div>
  )
}
