import { useEffect, useRef, useState } from 'react'
import { GetTaskOutput } from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'

// ---------------------------------------------------------------------------
// MiniOutput -- 2-3 line live output preview for dashboard
// ---------------------------------------------------------------------------

interface MiniOutputProps {
  taskId: string
  maxLines?: number
}

const DEFAULT_MAX_LINES = 3

export function MiniOutput({
  taskId,
  maxLines = DEFAULT_MAX_LINES,
}: MiniOutputProps): React.ReactElement {
  const [lines, setLines] = useState<string[]>([])
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    const eventName = 'output:' + taskId

    // Load initial last N lines
    GetTaskOutput(taskId, maxLines)
      .then((result) => {
        if (mountedRef.current) {
          setLines(result ?? [])
        }
      })
      .catch(() => {
        // Ignore errors -- file might not exist yet
      })

    // Subscribe to live output
    EventsOn(eventName, (line: string) => {
      if (!mountedRef.current) return
      setLines((prev) => {
        const next = [...prev, line]
        // Keep only the last N lines
        if (next.length > maxLines) {
          return next.slice(-maxLines)
        }
        return next
      })
    })

    return () => {
      mountedRef.current = false
      EventsOff(eventName)
    }
  }, [taskId, maxLines])

  if (lines.length === 0) {
    return (
      <div className="mt-1.5 rounded bg-app px-2 py-1.5 max-h-16 overflow-hidden">
        <p className="font-mono text-[10px] text-border italic">
          Waiting for output...
        </p>
      </div>
    )
  }

  return (
    <div className="mt-1.5 rounded bg-app px-2 py-1.5 max-h-16 overflow-hidden">
      {lines.map((line, i) => (
        <div
          key={i}
          className="font-mono text-[10px] leading-4 text-secondary truncate"
        >
          {line || '\u00A0'}
        </div>
      ))}
    </div>
  )
}
