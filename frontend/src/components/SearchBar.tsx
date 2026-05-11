import { useCallback, useEffect, useRef, useState } from 'react'
import { SearchOutput } from '../../wailsjs/go/main/App'
import { store } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface SearchBarProps {
  onSelectTask: (taskId: string) => void
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const DEBOUNCE_MS = 500

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function SearchBar({ onSelectTask }: SearchBarProps): React.ReactElement {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<store.OutputSearchResult[]>([])
  const [isOpen, setIsOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // -------------------------------------------------------------------------
  // Search logic
  // -------------------------------------------------------------------------

  const performSearch = useCallback(async (q: string) => {
    if (q.trim().length === 0) {
      setResults([])
      setIsOpen(false)
      return
    }

    setLoading(true)
    try {
      const searchResults = await SearchOutput(q)
      setResults(searchResults ?? [])
      setIsOpen(true)
    } catch (err) {
      console.warn('Failed to search output:', err)
      setResults([])
    } finally {
      setLoading(false)
    }
  }, [])

  const handleInputChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const value = e.target.value
      setQuery(value)

      if (debounceRef.current !== null) {
        clearTimeout(debounceRef.current)
      }

      debounceRef.current = setTimeout(() => {
        void performSearch(value)
      }, DEBOUNCE_MS)
    },
    [performSearch],
  )

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter') {
        if (debounceRef.current !== null) {
          clearTimeout(debounceRef.current)
        }
        void performSearch(query)
      }
      if (e.key === 'Escape') {
        setIsOpen(false)
      }
    },
    [performSearch, query],
  )

  const handleSelectResult = useCallback(
    (taskId: string) => {
      setIsOpen(false)
      setQuery('')
      setResults([])
      onSelectTask(taskId)
    },
    [onSelectTask],
  )

  // -------------------------------------------------------------------------
  // Click outside to close
  // -------------------------------------------------------------------------

  useEffect(() => {
    function handleClick(e: MouseEvent): void {
      if (
        containerRef.current !== null &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setIsOpen(false)
      }
    }

    if (isOpen) {
      document.addEventListener('mousedown', handleClick)
    }
    return () => document.removeEventListener('mousedown', handleClick)
  }, [isOpen])

  // Cleanup debounce on unmount
  useEffect(() => {
    return () => {
      if (debounceRef.current !== null) {
        clearTimeout(debounceRef.current)
      }
    }
  }, [])

  // -------------------------------------------------------------------------
  // Highlight matching text
  // -------------------------------------------------------------------------

  function highlightMatch(text: string, q: string): React.ReactNode {
    if (q.trim().length === 0) return text
    const idx = text.toLowerCase().indexOf(q.toLowerCase())
    if (idx === -1) return text
    return (
      <>
        {text.slice(0, idx)}
        <span className="bg-amber-500/30 text-amber-200">
          {text.slice(idx, idx + q.length)}
        </span>
        {text.slice(idx + q.length)}
      </>
    )
  }

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <div ref={containerRef} className="relative">
      {/* Search input */}
      <div className="flex items-center gap-2 bg-border-m/50 rounded-lg px-3 py-1.5">
        {/* Magnifying bg-surface border border-border icon */}
        <svg
          className="w-4 h-4 text-secondary flex-shrink-0"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <circle cx="11" cy="11" r="8" />
          <line x1="21" y1="21" x2="16.65" y2="16.65" />
        </svg>
        <input
          type="text"
          value={query}
          onChange={handleInputChange}
          onKeyDown={handleKeyDown}
          placeholder="Search output..."
          aria-label="Search task output"
          className="bg-transparent text-sm text-primary placeholder-muted
                     outline-none w-48 focus:w-64 transition-all"
        />
        {loading && (
          <div className="w-3 h-3 border-2 border-border border-t-primary rounded-full animate-spin" />
        )}
      </div>

      {/* Results dropdown */}
      {isOpen && results.length > 0 && (
        <div
          className="absolute top-full right-0 mt-1 w-[480px] max-h-80 overflow-y-auto
                     bg-elevated border border-border rounded-lg shadow-xl z-50"
        >
          {results.map((result, idx) => (
            <button
              key={`${result.taskId}-${result.lineNum}-${idx}`}
              type="button"
              onClick={() => handleSelectResult(result.taskId)}
              className="w-full text-left px-3 py-2 border-b border-border/50 last:border-b-0
                         hover:bg-border-m/50 transition-colors focus:outline-none
                         focus-visible:bg-border-m/50"
            >
              <div className="flex items-center gap-2 mb-1">
                <span className="text-xs font-medium text-acc-teal truncate">
                  {result.taskName}
                </span>
                <span className="text-[10px] text-muted">
                  line {result.lineNum}
                </span>
              </div>
              <div className="font-mono text-xs text-primary truncate">
                {highlightMatch(result.line, query)}
              </div>
            </button>
          ))}
        </div>
      )}

      {/* No results */}
      {isOpen && results.length === 0 && query.trim().length > 0 && !loading && (
        <div
          className="absolute top-full right-0 mt-1 w-[480px]
                     bg-elevated border border-border rounded-lg shadow-xl z-50 p-4"
        >
          <p className="text-sm text-muted text-center">No results found</p>
        </div>
      )}
    </div>
  )
}
