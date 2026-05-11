import { useCallback, useEffect, useRef, useState } from 'react'
import { FocusSession, GetPendingApprovals } from '../../wailsjs/go/main/App'
import { model } from '../../wailsjs/go/models'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type NotificationType = 'approval' | 'completed'

interface Notification {
  id: string
  type: NotificationType
  sessionName: string
  pid: number
  message: string
  createdAt: number // epoch ms
  read: boolean
  /** Duration string for completed sessions, e.g. "3m 12s" */
  duration?: string
}

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface NotificationBellProps {
  /** Optional extra className for the wrapper button */
  className?: string
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const POLL_INTERVAL_MS = 5_000
const AUTO_DISMISS_MS = 60_000
const MAX_NOTIFICATIONS = 20

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function relativeTime(epochMs: number): string {
  const diffSec = Math.floor((Date.now() - epochMs) / 1000)
  if (diffSec < 5) return 'just now'
  if (diffSec < 60) return `${diffSec}s ago`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  return `${diffHr}h ago`
}

function buildApprovalNotification(
  approval: model.ApprovalRequest,
): Notification {
  return {
    id: `approval-${approval.pid}-${Date.now()}`,
    type: 'approval',
    sessionName: approval.sessionName || `PID ${approval.pid}`,
    pid: approval.pid,
    message: `${approval.sessionName || 'Session'} is waiting for input`,
    createdAt: Date.now(),
    read: false,
  }
}

// ---------------------------------------------------------------------------
// Icons (inline SVGs to avoid extra deps)
// ---------------------------------------------------------------------------

function BellSvg(): React.ReactElement {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-5 w-5"
    >
      <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" />
      <path d="M10.3 21a1.94 1.94 0 0 0 3.4 0" />
    </svg>
  )
}

function ApprovalIcon(): React.ReactElement {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-4 w-4 shrink-0 text-acc-teal"
    >
      <circle cx="12" cy="12" r="10" />
      <path d="M12 8v4" />
      <path d="M12 16h.01" />
    </svg>
  )
}

function CompletedIcon(): React.ReactElement {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-4 w-4 shrink-0 text-green-400"
    >
      <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
      <polyline points="22 4 12 14.01 9 11.01" />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// Shared state hook
// ---------------------------------------------------------------------------

/**
 * Core notification state and polling logic. Consumed by both
 * `NotificationBell` (badge count) and `NotificationCenter` (dropdown list).
 */
function useNotifications(): {
  notifications: Notification[]
  unreadCount: number
  markRead: (id: string) => void
  markAllRead: () => void
  clearAll: () => void
  focusSession: (pid: number) => Promise<void>
} {
  const [notifications, setNotifications] = useState<Notification[]>([])
  const knownPidsRef = useRef<Set<number>>(new Set())

  // -------------------------------------------------------------------------
  // Poll for pending approvals
  // -------------------------------------------------------------------------

  useEffect(() => {
    let cancelled = false

    async function poll(): Promise<void> {
      try {
        const approvals = await GetPendingApprovals()
        if (cancelled) return

        const newNotifs: Notification[] = []
        for (const a of approvals) {
          if (!knownPidsRef.current.has(a.pid)) {
            knownPidsRef.current.add(a.pid)
            newNotifs.push(buildApprovalNotification(a))
          }
        }

        if (newNotifs.length > 0) {
          setNotifications((prev) =>
            [...newNotifs, ...prev].slice(0, MAX_NOTIFICATIONS),
          )
        }
      } catch (err) {
        console.warn('Failed to fetch pending approvals:', err)
      }
    }

    void poll()
    const handle = setInterval(() => void poll(), POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      clearInterval(handle)
    }
  }, [])

  // -------------------------------------------------------------------------
  // Auto-dismiss after 60 seconds
  // -------------------------------------------------------------------------

  useEffect(() => {
    const handle = setInterval(() => {
      const cutoff = Date.now() - AUTO_DISMISS_MS
      setNotifications((prev) => prev.filter((n) => n.createdAt > cutoff))
    }, 5_000)
    return () => clearInterval(handle)
  }, [])

  // -------------------------------------------------------------------------
  // Actions
  // -------------------------------------------------------------------------

  const unreadCount = notifications.filter((n) => !n.read).length

  const markRead = useCallback((id: string) => {
    setNotifications((prev) =>
      prev.map((n) => (n.id === id ? { ...n, read: true } : n)),
    )
  }, [])

  const markAllRead = useCallback(() => {
    setNotifications((prev) => prev.map((n) => ({ ...n, read: true })))
  }, [])

  const clearAll = useCallback(() => {
    setNotifications([])
  }, [])

  const handleFocus = useCallback(async (pid: number) => {
    try {
      await FocusSession(pid)
    } catch (err) {
      console.warn('Failed to focus session:', err)
    }
  }, [])

  return {
    notifications,
    unreadCount,
    markRead,
    markAllRead,
    clearAll,
    focusSession: handleFocus,
  }
}

// ---------------------------------------------------------------------------
// NotificationBell
// ---------------------------------------------------------------------------

export function NotificationBell({
  className = '',
}: NotificationBellProps): React.ReactElement {
  const { notifications, unreadCount, markRead, markAllRead, clearAll, focusSession } =
    useNotifications()
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  // Close dropdown on outside click
  useEffect(() => {
    function handleClickOutside(e: MouseEvent): void {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  // Close on Escape
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent): void {
      if (e.key === 'Escape') setOpen(false)
    }
    if (open) {
      document.addEventListener('keydown', handleKeyDown)
      return () => document.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  const hasUnread = unreadCount > 0

  return (
    <div ref={containerRef} className={`relative ${className}`}>
      {/* Bell button */}
      <button
        type="button"
        onClick={() => setOpen((prev) => !prev)}
        className={`relative p-1.5 rounded-md transition-colors hover:bg-border-m focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal ${
          hasUnread ? 'text-primary' : 'text-secondary'
        }`}
        aria-label={
          hasUnread
            ? `Notifications: ${unreadCount} unread`
            : 'Notifications'
        }
        aria-expanded={open}
        aria-haspopup="true"
      >
        <BellSvg />
        {hasUnread && (
          <span className="absolute -top-0.5 -right-0.5 flex h-4 min-w-[16px] items-center justify-center rounded-full bg-red-500 px-1 text-[9px] font-bold leading-none text-white">
            {unreadCount > 99 ? '99+' : unreadCount}
          </span>
        )}
      </button>

      {/* Dropdown */}
      {open && (
        <NotificationCenter
          notifications={notifications}
          onMarkRead={markRead}
          onMarkAllRead={markAllRead}
          onClearAll={clearAll}
          onFocus={focusSession}
          onClose={() => setOpen(false)}
        />
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// NotificationCenter (dropdown panel)
// ---------------------------------------------------------------------------

interface NotificationCenterProps {
  notifications: Notification[]
  onMarkRead: (id: string) => void
  onMarkAllRead: () => void
  onClearAll: () => void
  onFocus: (pid: number) => Promise<void>
  onClose: () => void
}

export function NotificationCenter({
  notifications,
  onMarkRead,
  onMarkAllRead,
  onClearAll,
  onFocus,
  onClose,
}: NotificationCenterProps): React.ReactElement {
  const handleFocusClick = useCallback(
    async (n: Notification) => {
      onMarkRead(n.id)
      await onFocus(n.pid)
      onClose()
    },
    [onMarkRead, onFocus, onClose],
  )

  return (
    <div
      role="region"
      aria-label="Notification center"
      className="absolute right-0 top-full z-50 mt-2 w-80 overflow-hidden rounded-lg border border-border bg-surface shadow-xl sm:w-96"
    >
      {/* Header */}
      <div className="flex items-center justify-between border-b border-border-m px-4 py-2.5">
        <h3 className="text-sm font-semibold text-primary">Notifications</h3>
        {notifications.some((n) => !n.read) && (
          <button
            type="button"
            onClick={onMarkAllRead}
            className="text-xs text-acc-teal hover:text-acc-teal/80 transition-colors"
          >
            Mark all read
          </button>
        )}
      </div>

      {/* List */}
      <div className="max-h-96 overflow-y-auto" role="list" aria-label="Notifications">
        {notifications.length === 0 ? (
          <div className="px-4 py-8 text-center text-sm text-secondary">
            No notifications
          </div>
        ) : (
          notifications.map((n) => (
            <NotificationRow
              key={n.id}
              notification={n}
              onFocusClick={handleFocusClick}
            />
          ))
        )}
      </div>

      {/* Footer */}
      {notifications.length > 0 && (
        <div className="border-t border-border-m px-4 py-2">
          <button
            type="button"
            onClick={onClearAll}
            className="w-full rounded-md py-1.5 text-center text-xs text-secondary transition-colors hover:bg-border-m hover:text-primary"
          >
            Clear all
          </button>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// NotificationRow
// ---------------------------------------------------------------------------

interface NotificationRowProps {
  notification: Notification
  onFocusClick: (n: Notification) => Promise<void>
}

function NotificationRow({
  notification: n,
  onFocusClick,
}: NotificationRowProps): React.ReactElement {
  const [timeLabel, setTimeLabel] = useState(() => relativeTime(n.createdAt))

  // Update relative time every 10s
  useEffect(() => {
    const handle = setInterval(() => {
      setTimeLabel(relativeTime(n.createdAt))
    }, 10_000)
    return () => clearInterval(handle)
  }, [n.createdAt])

  const isApproval = n.type === 'approval'

  return (
    <div
      role="listitem"
      className={`flex items-start gap-3 border-b border-border-m px-4 py-3 transition-colors last:border-b-0 ${
        n.read ? 'opacity-60' : ''
      }`}
    >
      {/* Icon */}
      <div className="mt-0.5">
        {isApproval ? <ApprovalIcon /> : <CompletedIcon />}
      </div>

      {/* Content */}
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span
            className={`inline-block rounded-full px-1.5 py-0.5 text-[10px] font-medium leading-none ${
              isApproval
                ? 'bg-teal-500/20 text-acc-teal'
                : 'bg-green-500/20 text-green-400'
            }`}
          >
            {isApproval ? 'Needs Approval' : 'Completed'}
          </span>
          <span className="text-[11px] text-secondary">{timeLabel}</span>
        </div>

        <p className="mt-1 truncate text-sm text-primary">
          {n.sessionName}
        </p>
        <p className="mt-0.5 truncate text-xs text-secondary">
          {n.message}
          {n.duration != null && (
            <span className="ml-1 text-green-400">({n.duration})</span>
          )}
        </p>
      </div>

      {/* Focus button */}
      <button
        type="button"
        onClick={() => void onFocusClick(n)}
        className="shrink-0 rounded-md border border-border bg-border-m px-2.5 py-1 text-xs font-medium text-primary transition-colors hover:border-secondary hover:bg-border focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-acc-teal"
        aria-label={`Focus session ${n.sessionName}`}
      >
        Focus
      </button>
    </div>
  )
}
