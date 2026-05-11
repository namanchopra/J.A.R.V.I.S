import { useCallback, useEffect, useRef, useState } from 'react'
import {
  GetTaskOutput,
  WatchTaskOutput,
  StopWatchingOutput,
} from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface OutputViewerProps {
  taskId: string
  outputPath: string
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const INITIAL_LINES = 200

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function OutputViewer({
  taskId,
  outputPath,
}: OutputViewerProps): React.ReactElement {
  const [lines, setLines] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [autoScroll, setAutoScroll] = useState(true)
  const [loading, setLoading] = useState(false)

  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  // Track the previous taskId so we can clean up correctly on change
  const prevTaskIdRef = useRef<string | null>(null)

  // -------------------------------------------------------------------------
  // Scroll to bottom helper
  // -------------------------------------------------------------------------

  const scrollToBottom = useCallback(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'instant' })
  }, [])

  // Auto-scroll when new lines arrive
  useEffect(() => {
    if (autoScroll) {
      scrollToBottom()
    }
  }, [lines, autoScroll, scrollToBottom])

  // -------------------------------------------------------------------------
  // No output path -- show helper
  // -------------------------------------------------------------------------

  if (outputPath === '') {
    return (
      <div className="flex-1 flex items-center justify-center bg-app rounded-lg border border-border mx-5 mb-5">
        <div className="text-center px-6 py-8 max-w-md">
          <p className="text-sm text-secondary">No output file attached.</p>
          <p className="text-xs text-muted mt-2 font-mono">
            Use{' '}
            <code className="bg-elevated px-1.5 py-0.5 rounded text-primary">
              awm update {taskId.slice(0, 8)}... --output-path /path/to/log
            </code>{' '}
            to attach one.
          </p>
        </div>
      </div>
    )
  }

  // -------------------------------------------------------------------------
  // Load initial output + start live tail
  // -------------------------------------------------------------------------

  // eslint-disable-next-line react-hooks/rules-of-hooks
  useEffect(() => {
    let cancelled = false
    const eventName = 'output:' + taskId

    // Clean up previous watcher if taskId changed
    if (prevTaskIdRef.current !== null && prevTaskIdRef.current !== taskId) {
      const oldId = prevTaskIdRef.current
      const oldEvent = 'output:' + oldId
      StopWatchingOutput(oldId).catch(() => {
        /* ignore */
      })
      EventsOff(oldEvent)
    }
    prevTaskIdRef.current = taskId

    // Reset state for the new task
    setLines([])
    setError(null)
    setLoading(true)
    setAutoScroll(true)

    // 1. Load initial lines
    GetTaskOutput(taskId, INITIAL_LINES)
      .then((initialLines) => {
        if (cancelled) return
        setLines(initialLines ?? [])
        setLoading(false)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        const msg = err instanceof Error ? err.message : String(err)
        // If the file doesn't exist yet, that's OK -- waiting state
        if (msg.includes('no such file') || msg.includes('does not exist')) {
          setError(null)
          setLines([])
        } else {
          setError(msg)
        }
        setLoading(false)
      })

    // 2. Subscribe to live output events
    EventsOn(eventName, (line: string) => {
      if (cancelled) return
      setLines((prev) => [...prev, line])
    })

    // 3. Start live tailing on the backend
    WatchTaskOutput(taskId).catch((err: unknown) => {
      if (cancelled) return
      const msg = err instanceof Error ? err.message : String(err)
      // Don't overwrite initial data with watch errors
      if (!msg.includes('no output path')) {
        setError(msg)
      }
    })

    return () => {
      cancelled = true
      StopWatchingOutput(taskId).catch(() => {
        /* ignore */
      })
      EventsOff(eventName)
    }
  }, [taskId]) // eslint-disable-line react-hooks/exhaustive-deps

  // -------------------------------------------------------------------------
  // Detect manual scroll to pause auto-scroll
  // -------------------------------------------------------------------------

  const handleScroll = useCallback(() => {
    const el = scrollContainerRef.current
    if (el === null) return

    // If user is near the bottom (within 40px), resume auto-scroll
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    setAutoScroll(nearBottom)
  }, [])

  // -------------------------------------------------------------------------
  // Render
  // -------------------------------------------------------------------------

  return (
    <div className="flex-1 flex flex-col min-h-0 mx-5 mb-5">
      {/* Toolbar */}
      <div className="flex items-center justify-between px-3 py-2 bg-elevated border border-border rounded-t-lg">
        <span className="text-xs text-secondary font-mono truncate">
          {outputPath}
        </span>
        <button
          type="button"
          onClick={() => setAutoScroll((prev) => !prev)}
          className={`text-xs px-2 py-1 rounded transition-colors focus:outline-none
                      focus-visible:ring-2 focus-visible:ring-blue-500
                      ${
                        autoScroll
                          ? 'bg-blue-600/20 text-blue-400'
                          : 'bg-border-m text-secondary hover:text-primary'
                      }`}
          aria-label={autoScroll ? 'Pause auto-scroll' : 'Resume auto-scroll'}
        >
          {autoScroll ? 'Auto-scroll ON' : 'Auto-scroll OFF'}
        </button>
      </div>

      {/* Log container */}
      <div
        ref={scrollContainerRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto bg-app border-x border-b border-border
                   rounded-b-lg font-mono text-sm text-primary relative"
      >
        {/* Loading state */}
        {loading && lines.length === 0 && (
          <div className="flex items-center justify-center h-32">
            <p className="text-xs text-muted">Loading output...</p>
          </div>
        )}

        {/* Error state */}
        {error !== null && (
          <div className="px-4 py-3 text-xs text-amber-400">
            {error}
          </div>
        )}

        {/* Waiting for output state */}
        {!loading && error === null && lines.length === 0 && (
          <div className="flex items-center justify-center h-32">
            <p className="text-xs text-muted">Waiting for output...</p>
          </div>
        )}

        {/* Output lines */}
        {lines.length > 0 && (
          <div className="px-4 py-3 space-y-0">
            {lines.map((line, i) => (
              <div
                key={i}
                className="leading-5 whitespace-pre-wrap break-all select-text"
              >
                {line || '\u00A0'}
              </div>
            ))}
          </div>
        )}

        {/* Scroll anchor */}
        <div ref={bottomRef} />

        {/* Jump to bottom button (shown when auto-scroll is off and not at bottom) */}
        {!autoScroll && lines.length > 0 && (
          <button
            type="button"
            onClick={() => {
              scrollToBottom()
              setAutoScroll(true)
            }}
            className="sticky bottom-3 left-1/2 -translate-x-1/2 z-10
                       bg-blue-600 hover:bg-blue-500 text-white text-xs
                       px-3 py-1.5 rounded-full shadow-lg transition-colors
                       focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-400"
            aria-label="Jump to bottom"
          >
            Jump to bottom
          </button>
        )}
      </div>
    </div>
  )
}
