import '../../lib/hud-theme'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface PlanStep {
  text: string
  status: 'pending' | 'active' | 'done'
}

export interface PlanPanelProps {
  goal: string
  steps: PlanStep[]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Return the status icon and inline color for a plan step. */
function stepIndicator(status: PlanStep['status']): { icon: string; color: string } {
  switch (status) {
    case 'done':
      return { icon: '\u2713', color: 'var(--hud-green)' }
    case 'active':
      return { icon: '\u25c9', color: 'var(--hud-cyan)' }
    case 'pending':
    default:
      return { icon: '\u25cb', color: 'var(--hud-text-dim)' }
  }
}

// ---------------------------------------------------------------------------
// PlanPanel
// ---------------------------------------------------------------------------

export function PlanPanel({ goal, steps }: PlanPanelProps): React.ReactElement {
  return (
    <div className="hud-panel flex flex-col overflow-hidden" style={{ maxHeight: 200 }}>
      {/* Header */}
      <div className="hud-header-gradient flex items-center gap-2 mb-2">
        <span className="hud-label">PLAN</span>
      </div>

      {/* Goal */}
      <p
        className="hud-text text-xs font-semibold truncate mb-2"
        title={goal}
      >
        {goal}
      </p>

      {/* Steps — scrollable */}
      <ol className="flex-1 overflow-y-auto space-y-1 list-none m-0 p-0">
        {steps.map((step, idx) => {
          const { icon, color } = stepIndicator(step.status)
          const dimClass = step.status === 'pending' ? 'hud-text-dim' : 'hud-text'

          return (
            <li key={idx} className="flex items-start gap-2 px-1">
              {/* Step number */}
              <span
                className="text-[10px] hud-text-dim flex-shrink-0 tabular-nums"
                style={{ minWidth: 14, textAlign: 'right' }}
              >
                {idx + 1}.
              </span>

              {/* Status icon */}
              <span
                className={`flex-shrink-0 text-xs leading-none${step.status === 'active' ? ' animate-pulse' : ''}`}
                style={{ color, marginTop: 1 }}
                aria-label={step.status}
              >
                {icon}
              </span>

              {/* Step text */}
              <span className={`${dimClass} text-xs leading-snug`}>
                {step.text}
              </span>
            </li>
          )
        })}
      </ol>
    </div>
  )
}
