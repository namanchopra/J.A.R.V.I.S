// ---------------------------------------------------------------------------
// NavRail -- narrow left navigation rail (sci-fi Jarvis theme)
// ---------------------------------------------------------------------------

import { useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import jarvisLogo from '../assets/jarvis-logo.png'

export type ViewId = 'jarvis' | 'tasks' | 'sessions' | 'workflows' | 'history' | 'groups' | 'settings'

interface NavRailProps {
  activeView: ViewId
  onNavigate: (view: ViewId) => void
}

interface NavItem {
  id: ViewId
  label: string
  icon: React.ReactNode
}

// ---------------------------------------------------------------------------
// Sci-fi color tokens
// ---------------------------------------------------------------------------

const CYAN = '#00e5ff'
const CYAN_MUTED = 'rgba(0,229,255,0.1)'
const ICON_DEFAULT = '#4a6278'
const ICON_ACTIVE = CYAN

// ---------------------------------------------------------------------------
// Icons (inline SVG to avoid extra deps)
// ---------------------------------------------------------------------------

function TerminalIcon(): React.ReactElement {
  return (
    <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="2" y="3" width="20" height="18" rx="2" />
      <path d="M7 8l3 3-3 3" />
      <path d="M13 16h4" />
    </svg>
  )
}

function GridIcon(): React.ReactElement {
  return (
    <svg
      className="w-5 h-5"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="3" y="3" width="7" height="7" />
      <rect x="14" y="3" width="7" height="7" />
      <rect x="3" y="14" width="7" height="7" />
      <rect x="14" y="14" width="7" height="7" />
    </svg>
  )
}

function ListIcon(): React.ReactElement {
  return (
    <svg
      className="w-5 h-5"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <line x1="8" y1="6" x2="21" y2="6" />
      <line x1="8" y1="12" x2="21" y2="12" />
      <line x1="8" y1="18" x2="21" y2="18" />
      <line x1="3" y1="6" x2="3.01" y2="6" />
      <line x1="3" y1="12" x2="3.01" y2="12" />
      <line x1="3" y1="18" x2="3.01" y2="18" />
    </svg>
  )
}

function ActivityIcon(): React.ReactElement {
  return (
    <svg
      className="w-5 h-5"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </svg>
  )
}

function SessionIcon(): React.ReactElement {
  return (
    <svg
      className="w-5 h-5"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polygon points="5 3 19 12 5 21 5 3" />
    </svg>
  )
}

function FolderIcon(): React.ReactElement {
  return (
    <svg
      className="w-5 h-5"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z" />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// NavButton with hover tooltip -- sci-fi styling
// ---------------------------------------------------------------------------

function NavButton({
  item,
  isActive,
  onNavigate,
}: {
  item: NavItem
  isActive: boolean
  onNavigate: (view: ViewId) => void
}): React.ReactElement {
  const [hovered, setHovered] = useState(false)

  return (
    <div className="relative w-full">
      <button
        type="button"
        onClick={() => onNavigate(item.id)}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        aria-label={item.label}
        aria-current={isActive ? 'page' : undefined}
        className="w-full h-10 flex items-center justify-center transition-all duration-150
                   focus:outline-none focus-visible:ring-2 focus-visible:ring-[#00e5ff]"
        style={
          isActive
            ? {
                color: ICON_ACTIVE,
                borderLeft: `2px solid ${CYAN}`,
                boxShadow: `0 0 8px rgba(0,229,255,0.3)`,
                background: 'rgba(0,229,255,0.08)',
              }
            : {
                color: ICON_DEFAULT,
                borderLeft: '2px solid transparent',
              }
        }
        onMouseOver={(e) => {
          if (!isActive) {
            e.currentTarget.style.color = CYAN
          }
        }}
        onMouseOut={(e) => {
          if (!isActive) {
            e.currentTarget.style.color = ICON_DEFAULT
          }
        }}
      >
        {item.icon}
      </button>

      {/* Hover tooltip */}
      <AnimatePresence>
        {hovered && (
          <motion.div
            initial={{ opacity: 0, x: -4 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -4 }}
            transition={{ duration: 0.1 }}
            className="absolute left-full top-1/2 -translate-y-1/2 ml-2 z-50
                       px-2.5 py-1 rounded-md text-xs font-medium
                       whitespace-nowrap pointer-events-none"
            style={{
              background: '#0d1525',
              color: '#c8dbe8',
              border: `1px solid ${CYAN_MUTED}`,
              boxShadow: '0 4px 12px rgba(0,0,0,0.5)',
            }}
          >
            {item.label}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

function SettingsIcon(): React.ReactElement {
  return (
    <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-2 2 2 2 0 01-2-2v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83 0 2 2 0 010-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 01-2-2 2 2 0 012-2h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 010-2.83 2 2 0 012.83 0l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 012-2 2 2 0 012 2v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 0 2 2 0 010 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 012 2 2 2 0 01-2 2h-.09a1.65 1.65 0 00-1.51 1z" />
    </svg>
  )
}

function HistoryIcon(): React.ReactElement {
  return (
    <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 8v4l3 3" />
      <path d="M3.05 11a9 9 0 1 1 .5 4m-.5 5v-5h5" />
    </svg>
  )
}

function DollarIcon(): React.ReactElement {
  return (
    <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="12" y1="1" x2="12" y2="23" />
      <path d="M17 5H9.5a3.5 3.5 0 000 7h5a3.5 3.5 0 010 7H6" />
    </svg>
  )
}

function GroupIcon(): React.ReactElement {
  return (
    <svg className="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="3" y="3" width="8" height="8" rx="1" />
      <rect x="13" y="3" width="8" height="8" rx="1" />
      <rect x="3" y="13" width="8" height="8" rx="1" />
      <rect x="13" y="13" width="8" height="8" rx="1" />
    </svg>
  )
}

// ---------------------------------------------------------------------------
// State → glow color (kept in sync with JarvisOrb palette)
const JARVIS_STATE_GLOW: Record<JarvisState, string> = {
  idle: '#00ffcc',
  listening: '#00ff88',
  thinking: '#0088ff',
  speaking: '#44ffee',
}

// Jarvis state polling helper
// ---------------------------------------------------------------------------

type JarvisState = 'idle' | 'listening' | 'thinking' | 'speaking'

const JARVIS_POLL_MS = 500

async function fetchJarvisState(): Promise<JarvisState> {
  try {
    const fn = window?.go?.main?.App?.GetJarvisState as
      | (() => Promise<string>)
      | undefined
    if (fn) {
      const s = await fn()
      if (s === 'listening' || s === 'thinking' || s === 'speaking') return s
      return 'idle'
    }
  } catch {
    // binding not available yet
  }
  return 'idle'
}

// ---------------------------------------------------------------------------
// JarvisIndicator button
// ---------------------------------------------------------------------------

function JarvisIndicator({
  onNavigate,
  isActive,
}: {
  onNavigate?: () => void
  isActive?: boolean
}): React.ReactElement {
  const [hovered, setHovered] = useState(false)
  const [jarvisState, setJarvisState] = useState<JarvisState>('idle')
  const mountedRef = useRef(true)

  // Poll Jarvis state every 500ms
  useEffect(() => {
    mountedRef.current = true

    const poll = async (): Promise<void> => {
      const s = await fetchJarvisState()
      if (mountedRef.current) setJarvisState(s)
    }

    void poll()
    const id = setInterval(() => void poll(), JARVIS_POLL_MS)

    return () => {
      mountedRef.current = false
      clearInterval(id)
    }
  }, [])

  return (
    <div className="relative flex flex-col items-center w-full">
      <button
        type="button"
        onClick={onNavigate}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        aria-label="Jarvis (Cmd+Shift+V)"
        aria-current={isActive ? 'page' : undefined}
        className="relative w-full h-11 flex items-center justify-center transition-all duration-150
                   focus:outline-none focus-visible:ring-2 focus-visible:ring-[#00e5ff]"
        style={
          isActive
            ? {
                color: CYAN,
                borderLeft: `2px solid ${CYAN}`,
                boxShadow: `0 0 8px rgba(0,229,255,0.3)`,
                background: 'rgba(0,229,255,0.08)',
              }
            : {
                color: ICON_DEFAULT,
                borderLeft: '2px solid transparent',
              }
        }
        onMouseOver={(e) => {
          if (!isActive) {
            e.currentTarget.style.color = CYAN
          }
        }}
        onMouseOut={(e) => {
          if (!isActive) {
            e.currentTarget.style.color = ICON_DEFAULT
          }
        }}
      >
        <img
          src={jarvisLogo}
          alt="Jarvis"
          className="w-8 h-8 rounded-full object-contain transition-[box-shadow] duration-300"
          style={{
            boxShadow: `0 0 10px ${JARVIS_STATE_GLOW[jarvisState]}, 0 0 20px ${JARVIS_STATE_GLOW[jarvisState]}55`,
          }}
        />
      </button>

      {/* Label */}
      <span
        className="text-[10px] mt-0.5 font-medium leading-tight"
        style={{ color: isActive ? CYAN : ICON_DEFAULT }}
      >
        Jarvis
      </span>

      {/* Hover tooltip */}
      <AnimatePresence>
        {hovered && (
          <motion.div
            initial={{ opacity: 0, x: -4 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -4 }}
            transition={{ duration: 0.1 }}
            className="absolute left-full top-1/2 -translate-y-1/2 ml-2 z-50
                       px-2.5 py-1 rounded-md text-xs font-medium
                       whitespace-nowrap pointer-events-none"
            style={{
              background: '#0d1525',
              color: '#c8dbe8',
              border: `1px solid ${CYAN_MUTED}`,
              boxShadow: '0 4px 12px rgba(0,0,0,0.5)',
            }}
          >
            Jarvis (Cmd+Shift+V)
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Nav items
// ---------------------------------------------------------------------------

const NAV_ITEMS: NavItem[] = [
  { id: 'tasks', label: 'Tasks', icon: <ListIcon /> },
  { id: 'sessions', label: 'Sessions', icon: <SessionIcon /> },
  { id: 'groups', label: 'Groups', icon: <GroupIcon /> },
  { id: 'history', label: 'History', icon: <HistoryIcon /> },
  { id: 'workflows', label: 'Workflows', icon: <FolderIcon /> },
  { id: 'settings', label: 'Settings', icon: <SettingsIcon /> },
]

export function NavRail({ activeView, onNavigate }: NavRailProps): React.ReactElement {
  return (
    <nav
      className="w-16 flex-shrink-0 flex flex-col items-center py-3 gap-1"
      style={{
        background: '#080c16',
        borderRight: `1px solid ${CYAN_MUTED}`,
      }}
      aria-label="Main navigation"
    >
      {/* Jarvis -- primary nav item */}
      <JarvisIndicator onNavigate={() => onNavigate('jarvis')} isActive={activeView === 'jarvis'} />

      {/* Separator between Jarvis and other nav items */}
      <div
        className="w-6 my-1.5"
        style={{ borderTop: `1px solid ${CYAN_MUTED}` }}
      />

      {/* Nav buttons */}
      {NAV_ITEMS.map((item) => (
        <NavButton
          key={item.id}
          item={item}
          isActive={activeView === item.id}
          onNavigate={onNavigate}
        />
      ))}

      {/* Spacer */}
      <div className="flex-1" />

      {/* Bottom Jarvis status dot */}
      <div
        className="w-6 my-1"
        style={{ borderTop: `1px solid ${CYAN_MUTED}` }}
      />
      <div className="flex items-center justify-center w-10 h-10" title="Jarvis Online">
        <span
          className="block w-2 h-2 rounded-full pulse-glow"
          style={{
            background: CYAN,
            boxShadow: `0 0 6px ${CYAN}, 0 0 12px rgba(0,229,255,0.3)`,
          }}
        />
      </div>
    </nav>
  )
}
