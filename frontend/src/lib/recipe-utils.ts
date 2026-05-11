import type { model } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Local types (Go models exist but Wails TS bindings not yet regenerated)
// ---------------------------------------------------------------------------

export interface TemplateParam {
  id: string
  templateId: string
  name: string
  paramType: string // "string" | "boolean" | "select"
  defaultValue: string
  description: string
  sortOrder: number
}

export interface RecipeStep {
  id: string
  templateId: string
  agentType: string
  promptTemplate: string
  dependsOn: string
  sortOrder: number
}

export interface RecipeDetail {
  template: model.SessionTemplate
  params: TemplateParam[]
  steps: RecipeStep[]
}

// ---------------------------------------------------------------------------
// Agent type options
// ---------------------------------------------------------------------------

export const AGENT_OPTIONS = [
  { value: 'claude-code', label: 'Claude Code' },
  { value: 'kiro', label: 'Kiro' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'codex', label: 'Codex' },
  { value: 'aider', label: 'Aider' },
] as const

// ---------------------------------------------------------------------------
// Shared style classes
// ---------------------------------------------------------------------------

export const INPUT_CLS =
  'w-full rounded-md bg-app border border-border text-sm text-primary px-3 py-2 ' +
  'placeholder-muted focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal focus:border-acc-teal'

export const LABEL_CLS = 'block text-xs font-medium text-secondary mb-1'

export const SELECT_CLS =
  'rounded-md bg-app border border-border text-sm text-primary px-2 py-1.5 ' +
  'focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal focus:border-acc-teal'

// ---------------------------------------------------------------------------
// Wails binding helpers (bindings may not exist yet -- fallback to window.go)
// ---------------------------------------------------------------------------

export function getAppBinding<T>(methodName: string): (((...args: unknown[]) => Promise<T>) | undefined) {
  try {
    const w = window as unknown as Record<string, unknown>
    const goNs = w?.go as Record<string, unknown> | undefined
    const mainNs = goNs?.main as Record<string, unknown> | undefined
    const appObj = mainNs?.App as Record<string, unknown> | undefined
    return appObj?.[methodName] as ((...args: unknown[]) => Promise<T>) | undefined
  } catch {
    return undefined
  }
}

export async function callCreateRecipe(
  name: string,
  params: Omit<TemplateParam, 'id' | 'templateId'>[],
  steps: Omit<RecipeStep, 'id' | 'templateId'>[],
): Promise<model.SessionTemplate | null> {
  const fn = getAppBinding<model.SessionTemplate>('CreateRecipe')
  if (!fn) {
    console.warn('CreateRecipe binding not available yet')
    return null
  }
  return fn(name, params, steps)
}

export async function callGetRecipeWithDetails(templateID: string): Promise<RecipeDetail | null> {
  const fn = getAppBinding<RecipeDetail>('GetRecipeWithDetails')
  if (!fn) {
    console.warn('GetRecipeWithDetails binding not available yet')
    return null
  }
  return fn(templateID)
}
