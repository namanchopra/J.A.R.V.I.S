import { useCallback, useEffect, useState } from 'react'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ApprovalRule {
  id: string
  name: string
  pattern: string
  action: string
  scope: string
  projectPath: string
  enabled: boolean
  createdAt: string
}

interface FormState {
  name: string
  pattern: string
  action: string
  scope: string
  projectPath: string
}

const EMPTY_FORM: FormState = {
  name: '',
  pattern: '',
  action: 'approve',
  scope: 'all',
  projectPath: '',
}

// ---------------------------------------------------------------------------
// Safe Wails binding access
// ---------------------------------------------------------------------------

function getBinding<T>(name: string): T | undefined {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (window as any)?.go?.main?.App?.[name] as T | undefined
}

async function listRules(): Promise<ApprovalRule[]> {
  const fn = getBinding<() => Promise<ApprovalRule[]>>('ListApprovalRules')
  if (!fn) return []
  const result = await fn()
  return result ?? []
}

async function createRule(
  name: string,
  pattern: string,
  action: string,
  scope: string,
  projectPath: string,
): Promise<ApprovalRule | null> {
  const fn = getBinding<
    (name: string, pattern: string, action: string, scope: string, projectPath: string) => Promise<ApprovalRule>
  >('CreateApprovalRule')
  if (!fn) return null
  return fn(name, pattern, action, scope, projectPath)
}

async function updateRule(id: string, updates: Record<string, unknown>): Promise<void> {
  const fn = getBinding<(id: string, updates: Record<string, unknown>) => Promise<void>>('UpdateApprovalRule')
  if (!fn) return
  await fn(id, updates)
}

async function deleteRule(id: string): Promise<void> {
  const fn = getBinding<(id: string) => Promise<void>>('DeleteApprovalRule')
  if (!fn) return
  await fn(id)
}

// ---------------------------------------------------------------------------
// Validation helper
// ---------------------------------------------------------------------------

function validateRegex(pattern: string): string | null {
  if (!pattern.trim()) return 'Pattern is required'
  try {
    new RegExp(pattern)
    return null
  } catch (err) {
    return `Invalid regex: ${err instanceof SyntaxError ? err.message : String(err)}`
  }
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function ApprovalRulesSettings(): React.ReactElement {
  const [rules, setRules] = useState<ApprovalRule[]>([])
  const [loading, setLoading] = useState(true)
  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [formError, setFormError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<{ text: string; type: 'success' | 'error' } | null>(null)

  const showMsg = useCallback((text: string, type: 'success' | 'error') => {
    setMessage({ text, type })
    setTimeout(() => setMessage(null), 3000)
  }, [])

  // ---- Load rules ----
  const loadRules = useCallback(async () => {
    try {
      const data = await listRules()
      setRules(data)
    } catch (err) {
      console.warn('Failed to load approval rules:', err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadRules()
  }, [loadRules])

  // ---- Toggle enabled ----
  const handleToggle = useCallback(
    async (rule: ApprovalRule) => {
      try {
        await updateRule(rule.id, { enabled: !rule.enabled })
        setRules((prev) => prev.map((r) => (r.id === rule.id ? { ...r, enabled: !r.enabled } : r)))
      } catch (err) {
        showMsg(`Toggle failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
      }
    },
    [showMsg],
  )

  // ---- Delete rule ----
  const handleDelete = useCallback(
    async (rule: ApprovalRule) => {
      if (!window.confirm(`Delete rule "${rule.name}"?`)) return
      try {
        await deleteRule(rule.id)
        setRules((prev) => prev.filter((r) => r.id !== rule.id))
        showMsg(`Deleted "${rule.name}"`, 'success')
      } catch (err) {
        showMsg(`Delete failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
      }
    },
    [showMsg],
  )

  // ---- Add rule ----
  const handleAdd = useCallback(async () => {
    // Validate name
    if (!form.name.trim()) {
      setFormError('Name is required')
      return
    }
    // Validate regex
    const regexError = validateRegex(form.pattern)
    if (regexError) {
      setFormError(regexError)
      return
    }
    // Validate project path when scope=project
    if (form.scope === 'project' && !form.projectPath.trim()) {
      setFormError('Project path is required when scope is "project"')
      return
    }

    setFormError(null)
    setSaving(true)
    try {
      const newRule = await createRule(
        form.name.trim(),
        form.pattern.trim(),
        form.action,
        form.scope,
        form.scope === 'project' ? form.projectPath.trim() : '',
      )
      if (newRule) {
        setRules((prev) => [...prev, newRule])
      }
      showMsg(`Rule "${form.name.trim()}" created`, 'success')
      setForm(EMPTY_FORM)
      setFormOpen(false)
    } catch (err) {
      showMsg(`Create failed: ${err instanceof Error ? err.message : String(err)}`, 'error')
    } finally {
      setSaving(false)
    }
  }, [form, showMsg])

  // ---- Render ----
  return (
    <section className="p-4 rounded-xl border border-border bg-surface">
      <div className="flex items-center justify-between mb-1">
        <h2 className="text-sm font-semibold text-primary">Approval Rules</h2>
        <button
          type="button"
          onClick={() => {
            setFormOpen((o) => !o)
            setFormError(null)
          }}
          className="text-xs px-2.5 py-1 rounded bg-acc-teal hover:bg-acc-teal/80 text-white transition-colors"
        >
          {formOpen ? 'Cancel' : '+ Add Rule'}
        </button>
      </div>
      <p className="text-xs text-muted mb-3">
        Auto-approve or auto-deny tool-use prompts that match a regex pattern.
      </p>

      {/* Message banner */}
      {message && (
        <div
          className={`text-xs px-3 py-1.5 rounded mb-3 ${
            message.type === 'success' ? 'bg-green-500/10 text-green-400' : 'bg-red-500/10 text-red-400'
          }`}
        >
          {message.text}
        </div>
      )}

      {/* Add Rule Form (collapsible) */}
      {formOpen && (
        <div className="mb-4 p-3 rounded-lg border border-border bg-app space-y-3">
          {/* Name */}
          <div>
            <label htmlFor="rule-name" className="block text-xs text-muted mb-1">
              Name
            </label>
            <input
              id="rule-name"
              type="text"
              value={form.name}
              onChange={(e) => {
                setForm((f) => ({ ...f, name: e.target.value }))
                setFormError(null)
              }}
              placeholder="e.g. Allow file reads"
              className="w-full px-3 py-1.5 text-sm bg-surface border border-border rounded-lg text-primary placeholder-muted focus:border-acc-blue focus:outline-none"
            />
          </div>

          {/* Pattern */}
          <div>
            <label htmlFor="rule-pattern" className="block text-xs text-muted mb-1">
              Pattern
            </label>
            <input
              id="rule-pattern"
              type="text"
              value={form.pattern}
              onChange={(e) => {
                setForm((f) => ({ ...f, pattern: e.target.value }))
                setFormError(null)
              }}
              placeholder="e.g. Read\s+file|cat\s+"
              className="w-full px-3 py-1.5 text-sm font-mono bg-surface border border-border rounded-lg text-primary placeholder-muted focus:border-acc-blue focus:outline-none"
            />
            <span className="text-[10px] text-muted mt-0.5 block">
              Regex matched against approval prompt text
            </span>
          </div>

          {/* Action + Scope (side-by-side) */}
          <div className="flex gap-3">
            <div className="flex-1">
              <label htmlFor="rule-action" className="block text-xs text-muted mb-1">
                Action
              </label>
              <select
                id="rule-action"
                value={form.action}
                onChange={(e) => setForm((f) => ({ ...f, action: e.target.value }))}
                className="w-full px-3 py-1.5 text-sm bg-surface border border-border rounded-lg text-primary focus:border-acc-blue focus:outline-none"
              >
                <option value="approve">Approve</option>
                <option value="deny">Deny</option>
              </select>
            </div>
            <div className="flex-1">
              <label htmlFor="rule-scope" className="block text-xs text-muted mb-1">
                Scope
              </label>
              <select
                id="rule-scope"
                value={form.scope}
                onChange={(e) => setForm((f) => ({ ...f, scope: e.target.value }))}
                className="w-full px-3 py-1.5 text-sm bg-surface border border-border rounded-lg text-primary focus:border-acc-blue focus:outline-none"
              >
                <option value="all">All projects</option>
                <option value="project">Specific project</option>
              </select>
            </div>
          </div>

          {/* Project Path (only when scope=project) */}
          {form.scope === 'project' && (
            <div>
              <label htmlFor="rule-project" className="block text-xs text-muted mb-1">
                Project Path
              </label>
              <input
                id="rule-project"
                type="text"
                value={form.projectPath}
                onChange={(e) => {
                  setForm((f) => ({ ...f, projectPath: e.target.value }))
                  setFormError(null)
                }}
                placeholder="/path/to/project"
                className="w-full px-3 py-1.5 text-sm font-mono bg-surface border border-border rounded-lg text-primary placeholder-muted focus:border-acc-blue focus:outline-none"
              />
            </div>
          )}

          {/* Form error */}
          {formError && <div className="text-xs text-red-400 bg-red-500/10 px-3 py-1.5 rounded">{formError}</div>}

          {/* Save button */}
          <div className="flex justify-end">
            <button
              type="button"
              onClick={() => void handleAdd()}
              disabled={saving}
              className="text-xs px-3 py-1.5 rounded bg-acc-teal hover:bg-acc-teal/80 text-white disabled:opacity-50 transition-colors"
            >
              {saving ? 'Creating...' : 'Create Rule'}
            </button>
          </div>
        </div>
      )}

      {/* Rules list */}
      {loading ? (
        <div className="space-y-2 animate-pulse">
          <div className="h-8 bg-border rounded w-full" />
          <div className="h-8 bg-border rounded w-full" />
        </div>
      ) : rules.length === 0 ? (
        <div className="text-xs text-muted text-center py-4">
          No approval rules configured. Click "+ Add Rule" to create one.
        </div>
      ) : (
        <div className="space-y-2">
          {rules.map((rule) => (
            <div
              key={rule.id}
              className="flex items-center gap-3 px-3 py-2 rounded-lg border border-border bg-app"
            >
              {/* Name + pattern */}
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm text-primary font-medium truncate">{rule.name}</span>
                  <span
                    className={`text-[10px] px-1.5 py-0.5 rounded font-medium uppercase tracking-wide ${
                      rule.action === 'approve'
                        ? 'bg-green-500/15 text-green-400'
                        : 'bg-red-500/15 text-red-400'
                    }`}
                  >
                    {rule.action}
                  </span>
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-border text-muted">
                    {rule.scope === 'all' ? 'all projects' : 'project'}
                  </span>
                </div>
                <div className="flex items-center gap-2 mt-0.5">
                  <code className="text-[11px] font-mono text-secondary truncate">{rule.pattern}</code>
                  {rule.scope === 'project' && rule.projectPath && (
                    <span className="text-[10px] text-muted truncate" title={rule.projectPath}>
                      {rule.projectPath}
                    </span>
                  )}
                </div>
              </div>

              {/* Enabled toggle */}
              <button
                type="button"
                role="switch"
                aria-checked={rule.enabled}
                aria-label={`${rule.enabled ? 'Disable' : 'Enable'} rule "${rule.name}"`}
                onClick={() => void handleToggle(rule)}
                className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${
                  rule.enabled ? 'bg-acc-teal' : 'bg-border'
                }`}
              >
                <span
                  className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${
                    rule.enabled ? 'translate-x-[18px]' : 'translate-x-[3px]'
                  }`}
                />
              </button>

              {/* Delete button */}
              <button
                type="button"
                onClick={() => void handleDelete(rule)}
                title={`Delete rule "${rule.name}"`}
                className="text-muted hover:text-red-400 transition-colors flex-shrink-0"
              >
                <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
                  <path
                    fillRule="evenodd"
                    d="M9 2a1 1 0 00-.894.553L7.382 4H4a1 1 0 000 2v10a2 2 0 002 2h8a2 2 0 002-2V6a1 1 0 100-2h-3.382l-.724-1.447A1 1 0 0011 2H9zM7 8a1 1 0 012 0v6a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v6a1 1 0 102 0V8a1 1 0 00-1-1z"
                    clipRule="evenodd"
                  />
                </svg>
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
