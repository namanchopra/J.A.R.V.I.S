import { AnimatePresence, motion } from 'framer-motion'
import '../../lib/hud-theme'

// ---------------------------------------------------------------------------
// Types (inline -- matches ActivityEvent shape from the Go model)
// ---------------------------------------------------------------------------

interface ActivityEvent {
  id: string
  taskId: string
  taskName: string
  eventType: string
  message: string
  metadata: string
  createdAt: any
}

interface HudActivityPanelProps {
  events: ActivityEvent[]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Map eventType to an icon character and CSS color variable. */
function eventIcon(eventType: string): { icon: string; color: string } {
  switch (eventType) {
    case 'completed':
      return { icon: '\u2713', color: 'var(--hud-green)' }
    case 'failed':
      return { icon: '\u2715', color: 'var(--hud-red)' }
    case 'needs_input':
      return { icon: '?', color: 'var(--hud-amber)' }
    default:
      return { icon: '\u2192', color: 'var(--hud-cyan)' }
  }
}

/** Format a timestamp into a compact relative time string. */
function relativeTime(createdAt: unknown): string {
  const ts = new Date(createdAt as string | number).getTime()
  if (Number.isNaN(ts)) return ''

  const diffMs = Date.now() - ts
  if (diffMs < 0) return 'just now'

  const seconds = Math.floor(diffMs / 1000)
  if (seconds < 10) return 'just now'

  const minutes = Math.floor(seconds / 60)
  if (minutes < 1) return `${seconds}s`

  const hours = Math.floor(minutes / 60)
  if (hours < 1) return `${minutes}m`

  return `${hours}h`
}

/** Truncate a string to maxLen characters, appending an ellipsis if needed. */
function truncate(text: string, maxLen: number): string {
  if (text.length <= maxLen) return text
  return text.slice(0, maxLen) + '\u2026'
}

// ---------------------------------------------------------------------------
// HudActivityPanel
// ---------------------------------------------------------------------------

export function HudActivityPanel({ events }: HudActivityPanelProps): React.ReactElement {
  const visible = events.slice(0, 5)

  return (
    <div className="hud-panel flex flex-col overflow-hidden">
      {/* ---- Header ---- */}
      <span className="hud-label">ACTIVITY</span>

      {/* ---- Event list ---- */}
      <div className="mt-2 flex-1 overflow-y-auto">
        {visible.length === 0 ? (
          <p className="hud-text-dim text-xs text-center mt-4">
            No recent activity
          </p>
        ) : (
          <ul className="space-y-1">
            <AnimatePresence initial={false}>
              {visible.map((evt) => {
                const { icon, color } = eventIcon(evt.eventType)
                return (
                  <motion.li
                    key={evt.id}
                    initial={{ opacity: 0, y: -10 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: -10 }}
                    transition={{ duration: 0.2 }}
                    className="flex items-center gap-2 px-1 py-1 rounded"
                  >
                    {/* Event icon */}
                    <span
                      className="flex-shrink-0 text-xs font-bold w-4 text-center"
                      style={{ color }}
                    >
                      {icon}
                    </span>

                    {/* Task name */}
                    <span className="hud-text text-xs truncate flex-1">
                      {truncate(evt.taskName, 20)}
                    </span>

                    {/* Relative time */}
                    <span className="hud-text-dim text-xs flex-shrink-0 tabular-nums">
                      {relativeTime(evt.createdAt)}
                    </span>
                  </motion.li>
                )
              })}
            </AnimatePresence>
          </ul>
        )}
      </div>
    </div>
  )
}
