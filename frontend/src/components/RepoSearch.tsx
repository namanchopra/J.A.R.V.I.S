import { useCallback, useEffect, useRef, useState } from 'react'
import {
  SearchRepos,
  LaunchReposInTerminalWithWorktree,
  GetConfig,
  SaveConfig,
} from '../../wailsjs/go/main/App'
import type { config, discovery } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Language badge colors
// ---------------------------------------------------------------------------

const LANG_COLORS: Record<string, string> = {
  typescript: 'bg-blue-500/15 text-blue-400',
  ts: 'bg-blue-500/15 text-blue-400',
  javascript: 'bg-yellow-500/15 text-yellow-400',
  js: 'bg-yellow-500/15 text-yellow-400',
  go: 'bg-cyan-500/15 text-cyan-400',
  python: 'bg-green-500/15 text-green-400',
  py: 'bg-green-500/15 text-green-400',
  rust: 'bg-orange-500/15 text-orange-400',
  java: 'bg-red-500/15 text-red-400',
  ruby: 'bg-red-500/15 text-red-400',
  swift: 'bg-orange-500/15 text-orange-400',
  kotlin: 'bg-teal-500/15 text-acc-teal',
  c: 'bg-border text-secondary',
  cpp: 'bg-border text-secondary',
}

function langBadgeClass(lang: string): string {
  return LANG_COLORS[lang.toLowerCase()] ?? 'bg-border text-secondary'
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface SelectedRepo {
  name: string
  path: string
  language: string
}

// ---------------------------------------------------------------------------
// Search result row
// ---------------------------------------------------------------------------

function SearchResultRow({
  result,
  isSelected,
  hasActiveSession,
  onToggle,
}: {
  result: discovery.RepoSearchResult
  isSelected: boolean
  hasActiveSession: boolean
  onToggle: () => void
}): React.ReactElement {
  return (
    <label className="flex items-center gap-3 px-3 py-2 rounded-lg hover:bg-elevated transition-colors cursor-pointer group">
      <input
        type="checkbox"
        checked={isSelected}
        onChange={onToggle}
        className="rounded border-border bg-surface text-teal-500 focus:ring-acc-teal/30 focus:ring-offset-0 flex-shrink-0"
      />
      <div className="flex-1 min-w-0 flex items-center gap-2">
        <span className="text-sm text-primary font-medium truncate group-hover:text-white">
          {result.name}
        </span>
        <span className={`text-[9px] px-1.5 py-0.5 rounded flex-shrink-0 ${langBadgeClass(result.language)}`}>
          {result.language}
        </span>
        <span className="text-[9px] px-1.5 py-0.5 rounded bg-border-m text-secondary flex-shrink-0">
          {result.branch}
        </span>
        {hasActiveSession && (
          <span className="w-2 h-2 rounded-full bg-green-400 flex-shrink-0" title="Active session" />
        )}
      </div>
      <span className="text-[10px] text-muted font-mono truncate max-w-[200px] hidden sm:inline">
        {result.path}
      </span>
    </label>
  )
}

// ---------------------------------------------------------------------------
// Selected repo chip
// ---------------------------------------------------------------------------

function RepoChip({
  repo,
  onRemove,
}: {
  repo: SelectedRepo
  onRemove: () => void
}): React.ReactElement {
  return (
    <span className="inline-flex items-center gap-1 px-2 py-1 rounded bg-teal-500/10 border border-acc-teal/20 text-xs text-acc-teal">
      {repo.name}
      <button
        type="button"
        onClick={onRemove}
        className="ml-0.5 hover:text-red-400 transition-colors"
        aria-label={`Remove ${repo.name}`}
      >
        <svg className="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <path d="M18 6L6 18M6 6l12 12" />
        </svg>
      </button>
    </span>
  )
}

// ---------------------------------------------------------------------------
// Terminal options
// ---------------------------------------------------------------------------

const TERMINAL_OPTIONS = [
  { value: '', label: 'Auto-detect' },
  { value: 'cmux', label: 'CMux' },
  { value: 'iterm2', label: 'iTerm2' },
  { value: 'terminal', label: 'Terminal.app' },
]

// ---------------------------------------------------------------------------
// Launch bar — shown when repos are selected
// ---------------------------------------------------------------------------

function LaunchBar({
  selectedRepos,
  onLaunched,
}: {
  selectedRepos: SelectedRepo[]
  onLaunched: () => void
}): React.ReactElement {
  const [command, setCommand] = useState('claude')
  const [launching, setLaunching] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [preferredTerminal, setPreferredTerminal] = useState('')

  // Load terminal preference from config
  useEffect(() => {
    GetConfig().then((cfg: config.Config) => {
      setPreferredTerminal(cfg.preferredTerminal ?? '')
    }).catch((err) => console.warn('Failed to load config:', err))
  }, [])

  const handleTerminalChange = useCallback(async (value: string): Promise<void> => {
    setPreferredTerminal(value)
    try {
      const cfg = await GetConfig()
      cfg.preferredTerminal = value
      await SaveConfig(cfg)
    } catch (err) { console.warn('Failed to save terminal preference:', err) }
  }, [])

  const handleLaunch = useCallback(async (): Promise<void> => {
    if (selectedRepos.length === 0) return
    setLaunching(true)
    setError(null)
    try {
      await LaunchReposInTerminalWithWorktree(
        selectedRepos.map((r) => r.path),
        command.trim(),
        false,
      )
      onLaunched()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLaunching(false)
    }
  }, [selectedRepos, command, onLaunched])

  return (
    <div className="mt-3 p-3 rounded-lg border border-border bg-app space-y-3">
      {/* Command + terminal picker row */}
      <div className="flex items-center gap-2">
        <div className="flex-1">
          <label htmlFor="launch-cmd" className="sr-only">Command</label>
          <div className="flex items-center gap-2 px-3 py-2 bg-surface border border-border rounded-lg">
            <span className="text-muted text-xs">$</span>
            <input
              id="launch-cmd"
              type="text"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="command to run (e.g. claude)"
              className="flex-1 bg-transparent text-sm font-mono text-primary placeholder-muted focus:outline-none"
            />
          </div>
        </div>
        <select
          value={preferredTerminal}
          onChange={(e) => void handleTerminalChange(e.target.value)}
          className="px-2.5 py-2 text-xs bg-surface border border-border rounded-lg text-secondary focus:outline-none focus:border-acc-blue"
        >
          {TERMINAL_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>{opt.label}</option>
          ))}
        </select>
      </div>

      {error && (
        <p className="text-xs text-red-400 bg-red-400/10 rounded px-2 py-1">{error}</p>
      )}

      {/* Launch button */}
      <button
        type="button"
        onClick={() => void handleLaunch()}
        disabled={launching || selectedRepos.length === 0}
        className="w-full flex items-center justify-center gap-2 px-4 py-2.5 text-xs rounded-lg
                   bg-green-600 hover:bg-green-500 text-white disabled:opacity-50
                   transition-colors font-semibold"
      >
        {launching ? (
          'Opening...'
        ) : (
          <>
            <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <polygon points="5 3 19 12 5 21 5 3" />
            </svg>
            Launch {selectedRepos.length} repo{selectedRepos.length !== 1 ? 's' : ''}
          </>
        )}
      </button>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Create Workflow section (search-based) -- now creates Virtual Monorepo Workspaces
// ---------------------------------------------------------------------------

export interface CreateWorkflowSectionProps {
  activePids: Set<string>
  /** Pre-fill from a re-launch action */
  prefill?: {
    name: string
    repoPaths: string[]
    prompt: string
  } | null
  onPrefillConsumed?: () => void
  onWorkspaceCreated?: () => void
}

export function CreateWorkflowSection({
  activePids,
  prefill,
  onPrefillConsumed,
  onWorkspaceCreated,
}: CreateWorkflowSectionProps): React.ReactElement {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<discovery.RepoSearchResult[]>([])
  const [selectedRepos, setSelectedRepos] = useState<SelectedRepo[]>([])
  const [searching, setSearching] = useState(false)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const prefillApplied = useRef(false)

  // Apply prefill when provided (from re-launch)
  useEffect(() => {
    if (prefill && !prefillApplied.current) {
      prefillApplied.current = true
      const repos: SelectedRepo[] = prefill.repoPaths.map((p) => ({
        name: p.split('/').pop() ?? p,
        path: p,
        language: '',
      }))
      setSelectedRepos(repos)
      onPrefillConsumed?.()
    }
  }, [prefill, onPrefillConsumed])

  // Reset prefill flag when prefill becomes null
  useEffect(() => {
    if (!prefill) {
      prefillApplied.current = false
    }
  }, [prefill])

  // Debounced search
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    if (!query.trim()) {
      setResults([])
      setSearching(false)
      return
    }
    setSearching(true)
    debounceRef.current = setTimeout(() => {
      SearchRepos(query)
        .then((res) => {
          setResults(res ?? [])
          setSearching(false)
        })
        .catch(() => {
          setSearching(false)
        })
    }, 300)
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [query])

  const selectedPaths = new Set(selectedRepos.map((r) => r.path))

  const toggleRepo = useCallback(
    (result: discovery.RepoSearchResult): void => {
      setSelectedRepos((prev) => {
        if (prev.some((r) => r.path === result.path)) {
          return prev.filter((r) => r.path !== result.path)
        }
        return [...prev, { name: result.name, path: result.path, language: result.language }]
      })
    },
    [],
  )

  const removeRepo = useCallback((path: string): void => {
    setSelectedRepos((prev) => prev.filter((r) => r.path !== path))
  }, [])

  return (
    <section className="px-5 py-4 border-t border-border-m">
      <div className="mb-3">
        <h2 className="text-sm font-semibold text-primary">Create Workspace</h2>
        <p className="text-xs text-muted mt-0.5">
          Search and select repos to create a virtual monorepo workspace
        </p>
      </div>

      {/* Search bar */}
      <div className="relative">
        <svg
          className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          aria-hidden="true"
        >
          <circle cx="11" cy="11" r="8" />
          <path d="M21 21l-4.35-4.35" />
        </svg>
        <input
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search repos by name..."
          className="w-full pl-10 pr-4 py-2.5 text-sm bg-surface border border-border rounded-lg text-primary placeholder-muted focus:border-acc-blue focus:outline-none"
        />
        {searching && (
          <svg
            className="absolute right-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted animate-spin"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
          >
            <path d="M12 2v4m0 12v4m-7.07-3.93l2.83-2.83m8.48-8.48l2.83-2.83M2 12h4m12 0h4m-3.93 7.07l-2.83-2.83M7.76 7.76L4.93 4.93" />
          </svg>
        )}
      </div>

      {/* Search results */}
      {results.length > 0 && (
        <div className="mt-2 rounded-lg border border-border-m bg-app max-h-60 overflow-y-auto">
          {results.map((r) => (
            <SearchResultRow
              key={r.path}
              result={r}
              isSelected={selectedPaths.has(r.path)}
              hasActiveSession={activePids.has(r.path)}
              onToggle={() => toggleRepo(r)}
            />
          ))}
        </div>
      )}

      {/* No results message */}
      {query.trim() && !searching && results.length === 0 && (
        <p className="mt-2 text-xs text-muted py-3 text-center">No repos found matching &quot;{query}&quot;</p>
      )}

      {/* Selected repos chips */}
      {selectedRepos.length > 0 && (
        <div className="mt-3">
          <p className="text-[10px] text-muted uppercase tracking-wider font-semibold mb-2">
            {selectedRepos.length} repo{selectedRepos.length !== 1 ? 's' : ''} selected
          </p>
          <div className="flex flex-wrap gap-1.5">
            {selectedRepos.map((r) => (
              <RepoChip key={r.path} repo={r} onRemove={() => removeRepo(r.path)} />
            ))}
          </div>
        </div>
      )}

      {/* Launch bar */}
      {selectedRepos.length > 0 && (
        <LaunchBar
          selectedRepos={selectedRepos}
          onLaunched={() => {
            setSelectedRepos([])
            setQuery('')
            setResults([])
            onWorkspaceCreated?.()
          }}
        />
      )}
    </section>
  )
}
