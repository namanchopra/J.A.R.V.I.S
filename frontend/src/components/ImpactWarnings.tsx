import { useCallback, useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { impact, claude } from '../../wailsjs/go/models'
import {
  FocusSession,
  GetImpactWarnings,
  GetSessionIndicators,
} from '../../wailsjs/go/main/App'

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 10_000

interface SeverityStyle {
  label: string
  bg: string
  text: string
  border: string
}

const SEVERITY_LOW: SeverityStyle = {
  label: 'Low',
  bg: 'bg-muted/30',
  text: 'text-secondary',
  border: 'border-muted',
}

const SEVERITY_CONFIG: Record<string, SeverityStyle> = {
  high: {
    label: 'High',
    bg: 'bg-red-600/20',
    text: 'text-red-400',
    border: 'border-red-500',
  },
  medium: {
    label: 'Medium',
    bg: 'bg-amber-600/20',
    text: 'text-amber-400',
    border: 'border-amber-500',
  },
  low: SEVERITY_LOW,
}

const CONFLICT_TYPE_LABELS: Record<string, string> = {
  shared_dependency: 'Shared Dependency',
  shared_file: 'Shared File',
  api_change: 'API Change',
}

// ---------------------------------------------------------------------------
// ImpactWarnings (exported)
// ---------------------------------------------------------------------------

export function ImpactWarnings(): React.ReactElement | null {
  const [warnings, setWarnings] = useState<impact.ImpactWarning[]>([])
  const [indicators, setIndicators] = useState<claude.SessionIndicator[]>([])
  const mountedRef = useRef(true)

  // ---- Polling ----
  useEffect(() => {
    mountedRef.current = true

    async function poll(): Promise<void> {
      try {
        const [w, ind] = await Promise.all([
          GetImpactWarnings(),
          GetSessionIndicators(),
        ])
        if (!mountedRef.current) return
        setWarnings(w ?? [])
        setIndicators(ind ?? [])
      } catch (err) {
        console.warn('Failed to fetch impact warnings:', err)
      }
    }

    void poll()
    const id = setInterval(() => void poll(), POLL_INTERVAL_MS)

    return () => {
      mountedRef.current = false
      clearInterval(id)
    }
  }, [])

  // ---- Focus handler ----

  const handleFocus = useCallback(
    async (sessionName: string): Promise<void> => {
      const match = indicators.find(
        (ind) => ind.name === sessionName || ind.sessionId === sessionName,
      )
      if (!match) return
      try {
        await FocusSession(match.pid)
      } catch (err) {
        console.warn('Failed to focus session:', err)
      }
    },
    [indicators],
  )

  // ---- Render nothing when empty ----

  if (warnings.length === 0) {
    return null
  }

  // ---- Render ----

  return (
    <section aria-label="Impact warnings" className="space-y-2">
      {/* Section header */}
      <div className="flex items-center gap-2 px-1 mb-1">
        <svg
          className="w-3.5 h-3.5 text-amber-500"
          viewBox="0 0 16 16"
          fill="currentColor"
          aria-hidden="true"
        >
          <path d="M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0 1 14.082 15H1.918a1.75 1.75 0 0 1-1.543-2.575ZM8 5a.75.75 0 0 0-.75.75v2.5a.75.75 0 0 0 1.5 0v-2.5A.75.75 0 0 0 8 5Zm0 9a1 1 0 1 0 0-2 1 1 0 0 0 0 2Z" />
        </svg>
        <h2 className="text-xs font-semibold uppercase tracking-wider text-amber-400">
          Potential Conflicts
        </h2>
        <span className="ml-auto inline-flex items-center justify-center min-w-[18px] h-[18px] px-1 rounded-full bg-amber-600/20 text-[10px] font-medium text-amber-400">
          {warnings.length}
        </span>
      </div>

      {/* Warning cards */}
      <AnimatePresence initial={false}>
        {warnings.map((warning) => (
          <WarningCard
            key={warning.id}
            warning={warning}
            onFocus={handleFocus}
          />
        ))}
      </AnimatePresence>
    </section>
  )
}

// ---------------------------------------------------------------------------
// WarningCard
// ---------------------------------------------------------------------------

interface WarningCardProps {
  warning: impact.ImpactWarning
  onFocus: (sessionName: string) => Promise<void>
}

function WarningCard({
  warning,
  onFocus,
}: WarningCardProps): React.ReactElement {
  const severity = SEVERITY_CONFIG[warning.severity] ?? SEVERITY_LOW
  const conflictLabel =
    CONFLICT_TYPE_LABELS[warning.conflictType] ?? warning.conflictType

  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: -8, scale: 0.97 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 8, scale: 0.97 }}
      transition={{ duration: 0.2, ease: 'easeOut' }}
      className={`bg-surface border border-amber-500/30 rounded-lg p-3 space-y-2 border-l-2 ${severity.border}`}
    >
      {/* Top row: severity badge + conflict type */}
      <div className="flex items-center gap-2 min-w-0">
        <span
          className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-semibold uppercase tracking-wide ${severity.bg} ${severity.text}`}
        >
          {severity.label}
        </span>
        <span className="text-[10px] text-muted font-medium">
          {conflictLabel}
        </span>
      </div>

      {/* Description */}
      <p className="text-xs text-primary leading-relaxed">
        {warning.description}
      </p>

      {/* Details (file/package name) */}
      {warning.details && (
        <div className="bg-app rounded px-2 py-1">
          <code className="text-[11px] text-secondary font-mono break-all">
            {warning.details}
          </code>
        </div>
      )}

      {/* Session buttons */}
      <div className="flex items-center gap-2 flex-wrap">
        <SessionFocusButton
          sessionName={warning.sessionA}
          onFocus={onFocus}
        />
        <span className="text-[10px] text-muted" aria-hidden="true">
          vs
        </span>
        <SessionFocusButton
          sessionName={warning.sessionB}
          onFocus={onFocus}
        />
      </div>
    </motion.div>
  )
}

// ---------------------------------------------------------------------------
// SessionFocusButton
// ---------------------------------------------------------------------------

interface SessionFocusButtonProps {
  sessionName: string
  onFocus: (sessionName: string) => Promise<void>
}

function SessionFocusButton({
  sessionName,
  onFocus,
}: SessionFocusButtonProps): React.ReactElement {
  return (
    <button
      type="button"
      onClick={() => void onFocus(sessionName)}
      aria-label={`Focus session ${sessionName}`}
      className="inline-flex items-center gap-1.5 px-2 py-1 text-xs font-medium rounded
                 bg-acc-teal/20 text-acc-teal
                 hover:bg-acc-teal/40 transition-colors
                 focus:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal"
    >
      <svg
        className="w-3 h-3"
        viewBox="0 0 16 16"
        fill="currentColor"
        aria-hidden="true"
      >
        <path d="M2 3.75C2 2.784 2.784 2 3.75 2h8.5c.966 0 1.75.784 1.75 1.75v8.5A1.75 1.75 0 0 1 12.25 14h-8.5A1.75 1.75 0 0 1 2 12.25Zm1.75-.25a.25.25 0 0 0-.25.25v8.5c0 .138.112.25.25.25h8.5a.25.25 0 0 0 .25-.25v-8.5a.25.25 0 0 0-.25-.25Z" />
      </svg>
      <span className="truncate max-w-[120px]">{sessionName}</span>
    </button>
  )
}
