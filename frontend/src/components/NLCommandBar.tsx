import { useCallback, useEffect, useRef, useState } from 'react'

// ---------------------------------------------------------------------------
// Wails binding (placeholder until backend adds ExecuteNLQuery)
// ---------------------------------------------------------------------------

// Once the Go method is wired up, replace this with:
//   import { ExecuteNLQuery } from '../../wailsjs/go/main/App'
// The binding signature will be:
//   ExecuteNLQuery(query: string): Promise<NLQueryResult>

interface NLQueryResult {
  action: string
  intent: string
  data: unknown
  needsConfirm: boolean
  error: string
}

let cachedExecuteNLQuery: ((q: string) => Promise<NLQueryResult>) | null = null

async function getExecuteNLQuery(): Promise<((q: string) => Promise<NLQueryResult>) | null> {
  if (cachedExecuteNLQuery) return cachedExecuteNLQuery
  try {
    const mod = await import('../../wailsjs/go/main/App')
    if ('ExecuteNLQuery' in mod && typeof mod.ExecuteNLQuery === 'function') {
      cachedExecuteNLQuery = mod.ExecuteNLQuery as (q: string) => Promise<NLQueryResult>
      return cachedExecuteNLQuery
    }
  } catch (err) {
    console.warn('Failed to load ExecuteNLQuery binding:', err)
  }
  return null
}

async function ExecuteNLQuery(query: string): Promise<NLQueryResult> {
  const fn = await getExecuteNLQuery()
  if (fn) {
    return fn(query)
  }

  return {
    action: '',
    intent: '',
    data: null,
    needsConfirm: false,
    error: `Backend binding "ExecuteNLQuery" is not available yet. Query: "${query}"`,
  }
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const MAX_RECENT = 5

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function SparkleIcon({ className }: { className?: string }): React.ReactElement {
  return (
    <svg
      className={className}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M12 2 L14 9 L21 9 L15.5 13.5 L17.5 21 L12 16.5 L6.5 21 L8.5 13.5 L3 9 L10 9 Z" />
    </svg>
  )
}

function Spinner(): React.ReactElement {
  return (
    <div className="w-4 h-4 border-2 border-border border-t-acc-blue rounded-full animate-spin" />
  )
}

// ---------------------------------------------------------------------------
// Result rendering helpers
// ---------------------------------------------------------------------------

function renderData(data: unknown): React.ReactElement {
  if (data === null || data === undefined) {
    return <span className="text-muted italic">No data</span>
  }

  if (typeof data === 'string' || typeof data === 'number' || typeof data === 'boolean') {
    return <span className="text-primary font-mono text-xs">{String(data)}</span>
  }

  if (data && typeof data === 'object' && !Array.isArray(data)) {
    const entries = Object.entries(data as Record<string, unknown>)
    if (entries.length === 0) {
      return <span className="text-muted italic">Empty</span>
    }
    return (
      <div className="space-y-1">
        {entries.map(([key, value]) => (
          <div key={key} className="flex gap-2 items-start">
            <span className="text-muted text-xs font-medium min-w-[100px] shrink-0">
              {key}:
            </span>
            <span className="text-primary text-xs font-mono break-all">
              {typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value ?? '')}
            </span>
          </div>
        ))}
      </div>
    )
  }

  return <span className="text-primary font-mono text-xs">{JSON.stringify(data)}</span>
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function NLCommandBar(): React.ReactElement {
  const [query, setQuery] = useState('')
  const [result, setResult] = useState<NLQueryResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [isFocused, setIsFocused] = useState(false)
  const [recentQueries, setRecentQueries] = useState<string[]>([])

  const inputRef = useRef<HTMLInputElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  // -------------------------------------------------------------------------
  // Cmd+K global shortcut to focus
  // -------------------------------------------------------------------------

  useEffect(() => {
    function handleGlobalKeyDown(e: KeyboardEvent): void {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        inputRef.current?.focus()
        setIsFocused(true)
      }
    }

    window.addEventListener('keydown', handleGlobalKeyDown)
    return () => window.removeEventListener('keydown', handleGlobalKeyDown)
  }, [])

  // -------------------------------------------------------------------------
  // Click outside to close results
  // -------------------------------------------------------------------------

  useEffect(() => {
    function handleClickOutside(e: MouseEvent): void {
      if (
        containerRef.current !== null &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setIsFocused(false)
      }
    }

    if (isFocused) {
      document.addEventListener('mousedown', handleClickOutside)
    }
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [isFocused])

  // -------------------------------------------------------------------------
  // Execute query
  // -------------------------------------------------------------------------

  const executeQuery = useCallback(async (q: string) => {
    const trimmed = q.trim()
    if (trimmed.length === 0) return

    setLoading(true)
    setResult(null)

    try {
      const res = await ExecuteNLQuery(trimmed)
      setResult(res)
    } catch (err) {
      setResult({
        action: '',
        intent: '',
        data: null,
        needsConfirm: false,
        error: err instanceof Error ? err.message : 'Unknown error occurred',
      })
    } finally {
      setLoading(false)
    }

    // Add to recent queries (deduplicate, cap at MAX_RECENT)
    setRecentQueries((prev) => {
      const filtered = prev.filter((item) => item !== trimmed)
      return [trimmed, ...filtered].slice(0, MAX_RECENT)
    })
  }, [])

  // -------------------------------------------------------------------------
  // Confirm / Cancel handlers for actions that need confirmation
  // -------------------------------------------------------------------------

  const handleConfirm = useCallback(async () => {
    if (result === null) return
    setLoading(true)
    try {
      // Re-execute with the same query; the backend should recognize
      // a confirmed action (or a separate ConfirmNLQuery binding can be added).
      const res = await ExecuteNLQuery(`confirm: ${result.action}`)
      setResult(res)
    } catch (err) {
      setResult({
        action: '',
        intent: '',
        data: null,
        needsConfirm: false,
        error: err instanceof Error ? err.message : 'Confirmation failed',
      })
    } finally {
      setLoading(false)
    }
  }, [result])

  const handleCancel = useCallback(() => {
    setResult(null)
  }, [])

  // -------------------------------------------------------------------------
  // Input key handling
  // -------------------------------------------------------------------------

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        void executeQuery(query)
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        setIsFocused(false)
        setResult(null)
        inputRef.current?.blur()
      }
    },
    [executeQuery, query],
  )

  // -------------------------------------------------------------------------
  // Recent query click
  // -------------------------------------------------------------------------

  const handleRecentClick = useCallback(
    (q: string) => {
      setQuery(q)
      void executeQuery(q)
    },
    [executeQuery],
  )

  // -------------------------------------------------------------------------
  // Derived state
  // -------------------------------------------------------------------------

  const showResults = isFocused && (result !== null || loading || recentQueries.length > 0)

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <div ref={containerRef} className="relative w-full">
      {/* Input bar */}
      <div
        className={`
          flex items-center gap-3 px-4 py-2.5
          bg-app border rounded-lg
          transition-colors
          ${isFocused ? 'border-acc-blue shadow-acc-blue/20' : 'border-border'}
        `}
      >
        <SparkleIcon className="w-5 h-5 text-secondary flex-shrink-0" />

        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={handleKeyDown}
          onFocus={() => setIsFocused(true)}
          placeholder="Ask Jarvis anything..."
          aria-label="Natural language command bar"
          className="
            flex-1 bg-transparent text-sm text-primary
            placeholder-muted outline-none
          "
        />

        {loading && <Spinner />}

        {/* Cmd+K badge */}
        {!isFocused && (
          <kbd className="hidden sm:inline-flex items-center gap-0.5 text-[10px] text-muted bg-border-m border border-border rounded px-1.5 py-0.5 font-mono">
            <span className="text-[9px]">&#8984;</span>K
          </kbd>
        )}
      </div>

      {/* Results dropdown */}
      {showResults && (
        <div
          className="
            absolute top-full left-0 right-0 mt-2 z-50
            bg-surface border border-border rounded-lg shadow-xl
            max-h-[400px] overflow-y-auto
          "
        >
          {/* Loading state */}
          {loading && result === null && (
            <div className="flex items-center gap-2 px-4 py-3">
              <Spinner />
              <span className="text-sm text-secondary">Thinking...</span>
            </div>
          )}

          {/* Error result */}
          {result !== null && result.error.length > 0 && (
            <div className="px-4 py-3 border-b border-border-m">
              <div className="flex items-start gap-2">
                <svg
                  className="w-4 h-4 text-acc-red mt-0.5 flex-shrink-0"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <circle cx="12" cy="12" r="10" />
                  <line x1="15" y1="9" x2="9" y2="15" />
                  <line x1="9" y1="9" x2="15" y2="15" />
                </svg>
                <div>
                  <p className="text-sm text-acc-red">{result.error}</p>
                  <p className="text-xs text-muted mt-1">
                    Try: "show active sessions", "create task", "what's running?"
                  </p>
                </div>
              </div>
            </div>
          )}

          {/* Successful result with confirmation needed */}
          {result !== null && result.error.length === 0 && result.needsConfirm && (
            <div className="px-4 py-3 border-b border-border-m">
              <p className="text-sm text-primary font-medium mb-2">{result.intent}</p>
              {result.data !== null && result.data !== undefined && (
                <div className="mb-3 px-3 py-2 bg-app rounded border border-border-m">
                  {renderData(result.data)}
                </div>
              )}
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => void handleConfirm()}
                  className="
                    px-3 py-1.5 text-xs font-medium rounded-md
                    bg-acc-green hover:bg-acc-green/80 text-white
                    transition-colors focus:outline-none focus-visible:ring-2
                    focus-visible:ring-acc-green focus-visible:ring-offset-2
                    focus-visible:ring-offset-surface
                  "
                >
                  Confirm
                </button>
                <button
                  type="button"
                  onClick={handleCancel}
                  className="
                    px-3 py-1.5 text-xs font-medium rounded-md
                    bg-border-m hover:bg-border text-secondary
                    transition-colors focus:outline-none focus-visible:ring-2
                    focus-visible:ring-border focus-visible:ring-offset-2
                    focus-visible:ring-offset-surface
                  "
                >
                  Cancel
                </button>
              </div>
            </div>
          )}

          {/* Successful result (info / query response) */}
          {result !== null && result.error.length === 0 && !result.needsConfirm && (
            <div className="px-4 py-3 border-b border-border-m">
              {result.intent.length > 0 && (
                <p className="text-sm text-acc-blue font-medium mb-2">{result.intent}</p>
              )}
              {result.action.length > 0 && (
                <span className="inline-block text-[10px] font-mono text-muted bg-border-m rounded px-1.5 py-0.5 mb-2">
                  {result.action}
                </span>
              )}
              {(result.data !== null && result.data !== undefined) && (
                <div className="px-3 py-2 bg-app rounded border border-border-m">
                  {renderData(result.data)}
                </div>
              )}
            </div>
          )}

          {/* Recent queries */}
          {recentQueries.length > 0 && (
            <div className="px-4 py-2.5">
              <p className="text-[10px] text-muted uppercase tracking-wider font-medium mb-2">
                Recent
              </p>
              <div className="flex flex-wrap gap-1.5">
                {recentQueries.map((rq) => (
                  <button
                    key={rq}
                    type="button"
                    onClick={() => handleRecentClick(rq)}
                    className="
                      px-2 py-1 text-[10px] rounded-md
                      bg-border-m text-secondary
                      hover:bg-border hover:text-primary
                      transition-colors focus:outline-none
                      focus-visible:ring-1 focus-visible:ring-acc-blue
                      truncate max-w-[200px]
                    "
                    title={rq}
                  >
                    {rq}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
