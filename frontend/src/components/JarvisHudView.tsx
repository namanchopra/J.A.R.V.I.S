import { useCallback, useEffect, useRef, useState } from 'react'
import { JarvisOrb } from './JarvisOrb'
import { JarvisToastContainer } from './JarvisToast'
import { HudSessionPanel } from './hud/HudSessionPanel'
import { HudCostPanel } from './hud/HudCostPanel'
import { HudActivityPanel } from './hud/HudActivityPanel'
import { HudApprovalPanel } from './hud/HudApprovalPanel'
import { HudVoiceBar } from './hud/HudVoiceBar'
import { HudInput } from './hud/HudInput'
import { PlanPanel } from './hud/PlanPanel'
import type { PlanStep } from './hud/PlanPanel'
import '../lib/hud-theme'
import { useFlash } from '../lib/hud-animations'
import { sendJarvisCommand } from '../lib/jarvis-api'
import { EventsOn } from '../../wailsjs/runtime/runtime'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type JarvisState = 'idle' | 'listening' | 'thinking' | 'speaking'

/** Shape of a session indicator returned by GetSessionIndicators. */
interface SessionIndicator {
  pid: number
  sessionId: string
  name: string
  status: string
  cwd: string
  startedAt: number
  hasQuestion: boolean
  lastActivity: string
  tokensUsed: number
}

/** Minimal shape of cost summary returned by GetTotalSpend. */
interface TotalSpend {
  allTime: number
  thisMonth: number
  today: number
}

/** Shape of an approval request returned by GetPendingApprovals. */
interface ApprovalRequest {
  pid: number
  sessionName: string
  cwd: string
  promptText: string
  detectedAt: any
}

/** Shape of an activity event returned by GetActivityFeed. */
interface ActivityEvent {
  id: string
  taskId: string
  taskName: string
  eventType: string
  message: string
  metadata: string
  createdAt: any
}

/** Minimal shape of dashboard stats returned by GetDashboardStats. */
interface DashboardStats {
  total: number
  running: number
  pending: number
  done: number
  failed: number
  needsInput: number
}

/** Minimal shape of daily cost returned by GetDailyCostSummary. */
interface DailyCost {
  date: string
  inputTokens: number
  outputTokens: number
  costUsd: number
  sessionCount: number
}

// ---------------------------------------------------------------------------
// Wails binding wrappers (safe when bindings are not yet available)
// ---------------------------------------------------------------------------

async function getJarvisState(): Promise<JarvisState> {
  try {
    const fn = window?.go?.main?.App?.GetJarvisState as
      | (() => Promise<string>)
      | undefined
    if (fn) {
      const s = await fn()
      if (s === 'listening' || s === 'thinking' || s === 'speaking') return s
    }
  } catch { /* binding not available */ }
  return 'idle'
}

async function startJarvis(): Promise<void> {
  try {
    const fn = window?.go?.main?.App?.StartJarvis as
      | (() => Promise<void>)
      | undefined
    if (fn) await fn()
  } catch { /* binding not available */ }
}

async function getSessionIndicators(): Promise<SessionIndicator[]> {
  try {
    const fn = window?.go?.main?.App?.GetSessionIndicators as
      | (() => Promise<SessionIndicator[]>)
      | undefined
    if (fn) return await fn()
  } catch { /* binding not available */ }
  return []
}

async function getTotalSpend(): Promise<TotalSpend> {
  try {
    const fn = window?.go?.main?.App?.GetTotalSpend as
      | (() => Promise<TotalSpend>)
      | undefined
    if (fn) return await fn()
  } catch { /* binding not available */ }
  return { allTime: 0, thisMonth: 0, today: 0 }
}

async function getPendingApprovals(): Promise<ApprovalRequest[]> {
  try {
    const fn = window?.go?.main?.App?.GetPendingApprovals as
      | (() => Promise<ApprovalRequest[]>)
      | undefined
    if (fn) return await fn()
  } catch { /* binding not available */ }
  return []
}

async function getActivityFeed(): Promise<ActivityEvent[]> {
  try {
    const fn = window?.go?.main?.App?.GetActivityFeed as
      | ((limit: number, beforeId: string) => Promise<ActivityEvent[]>)
      | undefined
    if (fn) return await fn(10, '')
  } catch { /* binding not available */ }
  return []
}

async function getDashboardStats(): Promise<DashboardStats> {
  try {
    const fn = window?.go?.main?.App?.GetDashboardStats as
      | (() => Promise<DashboardStats>)
      | undefined
    if (fn) return await fn()
  } catch { /* binding not available */ }
  return { total: 0, running: 0, pending: 0, done: 0, failed: 0, needsInput: 0 }
}

async function getDailyCostSummary(): Promise<DailyCost[]> {
  try {
    const fn = window?.go?.main?.App?.GetDailyCostSummary as
      | (() => Promise<DailyCost[]>)
      | undefined
    if (fn) return await fn()
  } catch { /* binding not available */ }
  return []
}

// ---------------------------------------------------------------------------
// Augment Window type for Wails runtime bridge
// ---------------------------------------------------------------------------

declare global {
  interface Window {
    go?: {
      main?: {
        App?: Record<string, unknown>
      }
    }
  }
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const DEX_STATE_POLL_MS = 500
const SESSIONS_POLL_MS = 3000
const APPROVALS_POLL_MS = 3000
const COSTS_POLL_MS = 10000
const ACTIVITY_POLL_MS = 5000
const STATS_POLL_MS = 5000

// ---------------------------------------------------------------------------
// HudBracket -- corner-bracketed floating panel wrapper
// ---------------------------------------------------------------------------

function HudBracket({
  children,
  label,
  className = '',
  flash = '',
}: {
  children: React.ReactNode
  label: string
  className?: string
  flash?: string
}): React.ReactElement {
  return (
    <div className={`relative ${className} ${flash}`} style={{ padding: '14px 16px' }}>
      {/* Corner brackets */}
      <div
        className="absolute"
        style={{
          top: 0,
          left: 0,
          width: 14,
          height: 14,
          borderTop: '1px solid rgba(0,229,255,0.45)',
          borderLeft: '1px solid rgba(0,229,255,0.45)',
        }}
      />
      <div
        className="absolute"
        style={{
          top: 0,
          right: 0,
          width: 14,
          height: 14,
          borderTop: '1px solid rgba(0,229,255,0.45)',
          borderRight: '1px solid rgba(0,229,255,0.45)',
        }}
      />
      <div
        className="absolute"
        style={{
          bottom: 0,
          left: 0,
          width: 14,
          height: 14,
          borderBottom: '1px solid rgba(0,229,255,0.45)',
          borderLeft: '1px solid rgba(0,229,255,0.45)',
        }}
      />
      <div
        className="absolute"
        style={{
          bottom: 0,
          right: 0,
          width: 14,
          height: 14,
          borderBottom: '1px solid rgba(0,229,255,0.45)',
          borderRight: '1px solid rgba(0,229,255,0.45)',
        }}
      />
      {/* Label */}
      <div
        style={{
          fontSize: 9,
          fontFamily: "'SF Mono', 'Menlo', monospace",
          fontWeight: 600,
          letterSpacing: '0.2em',
          textTransform: 'uppercase' as const,
          color: 'rgba(0,229,255,0.45)',
          marginBottom: 6,
        }}
      >
        {label}
      </div>
      {children}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Concentric HUD rings SVG helper
// ---------------------------------------------------------------------------

function HudRings(): React.ReactElement {
  return (
    <div
      className="absolute inset-0 flex items-center justify-center"
      style={{ pointerEvents: 'none' }}
    >
      {/* Outermost ring -- slow rotation, dashed */}
      <div
        className="absolute rounded-full"
        style={{
          width: 440,
          height: 440,
          border: '1px dashed rgba(0,229,255,0.12)',
          animation: 'hud-spin 40s linear infinite',
        }}
      />

      {/* Outer ring -- slow rotation */}
      <div
        className="absolute rounded-full"
        style={{
          width: 400,
          height: 400,
          border: '1px solid rgba(0,229,255,0.1)',
          animation: 'hud-spin 30s linear infinite',
        }}
      />

      {/* Tick marks ring (72 ticks, every 6th is major) */}
      <svg
        className="absolute"
        width="420"
        height="420"
        viewBox="-210 -210 420 420"
        style={{ animation: 'hud-spin 25s linear infinite reverse' }}
      >
        {Array.from({ length: 72 }).map((_, i) => {
          const angle = (i / 72) * Math.PI * 2
          const isMajor = i % 6 === 0
          const r1 = 188
          const r2 = isMajor ? 200 : 195
          return (
            <line
              key={i}
              x1={Math.cos(angle) * r1}
              y1={Math.sin(angle) * r1}
              x2={Math.cos(angle) * r2}
              y2={Math.sin(angle) * r2}
              stroke={isMajor ? 'rgba(0,229,255,0.5)' : 'rgba(0,229,255,0.15)'}
              strokeWidth={isMajor ? 1.5 : 0.5}
            />
          )
        })}
        {/* Cardinal direction markers */}
        {[0, 90, 180, 270].map((deg) => {
          const rad = (deg / 180) * Math.PI
          return (
            <text
              key={deg}
              x={Math.cos(rad) * 176}
              y={Math.sin(rad) * 176 + 3}
              textAnchor="middle"
              fill="rgba(0,229,255,0.3)"
              fontSize="7"
              fontFamily="'SF Mono', 'Menlo', monospace"
            >
              {deg.toString().padStart(3, '0')}
            </text>
          )
        })}
      </svg>

      {/* Middle ring -- counter-rotation, slightly brighter */}
      <div
        className="absolute rounded-full"
        style={{
          width: 350,
          height: 350,
          border: '1.5px solid rgba(0,229,255,0.2)',
          animation: 'hud-spin 20s linear infinite reverse',
        }}
      />

      {/* Inner data ring with segment gaps */}
      <svg
        className="absolute"
        width="330"
        height="330"
        viewBox="-165 -165 330 330"
        style={{ animation: 'hud-spin 18s linear infinite' }}
      >
        {/* 4 arc segments with gaps */}
        {[0, 1, 2, 3].map((i) => {
          const startAngle = i * 90 + 8
          const endAngle = (i + 1) * 90 - 8
          const startRad = (startAngle / 180) * Math.PI
          const endRad = (endAngle / 180) * Math.PI
          const r = 155
          return (
            <path
              key={i}
              d={`M ${Math.cos(startRad) * r} ${Math.sin(startRad) * r} A ${r} ${r} 0 0 1 ${Math.cos(endRad) * r} ${Math.sin(endRad) * r}`}
              fill="none"
              stroke="rgba(0,229,255,0.25)"
              strokeWidth="1.5"
            />
          )
        })}
      </svg>

      {/* Inner glow ring -- fastest rotation */}
      <div
        className="absolute rounded-full"
        style={{
          width: 290,
          height: 290,
          border: '1px solid rgba(0,229,255,0.15)',
          animation: 'hud-spin 15s linear infinite',
          boxShadow: 'inset 0 0 30px rgba(0,229,255,0.05)',
        }}
      />

      {/* Innermost ring -- tight around orb */}
      <div
        className="absolute rounded-full"
        style={{
          width: 270,
          height: 270,
          border: '1px dotted rgba(0,229,255,0.12)',
          animation: 'hud-spin 12s linear infinite reverse',
        }}
      />

      {/* Crosshairs (subtle) */}
      <svg
        className="absolute"
        width="460"
        height="460"
        viewBox="-230 -230 460 460"
        style={{ opacity: 0.08 }}
      >
        <line x1="-220" y1="0" x2="-140" y2="0" stroke="#00e5ff" strokeWidth="0.5" />
        <line x1="140" y1="0" x2="220" y2="0" stroke="#00e5ff" strokeWidth="0.5" />
        <line x1="0" y1="-220" x2="0" y2="-140" stroke="#00e5ff" strokeWidth="0.5" />
        <line x1="0" y1="140" x2="0" y2="220" stroke="#00e5ff" strokeWidth="0.5" />
      </svg>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Inject HUD keyframes (idempotent)
// ---------------------------------------------------------------------------

const HUD_KEYFRAMES_ID = 'hud-ring-keyframes'

function injectHudKeyframes(): void {
  if (typeof document === 'undefined') return
  if (document.getElementById(HUD_KEYFRAMES_ID)) return

  const style = document.createElement('style')
  style.id = HUD_KEYFRAMES_ID
  style.textContent = `
    @keyframes hud-spin {
      from { transform: rotate(0deg); }
      to { transform: rotate(360deg); }
    }
    @keyframes hud-pulse-dot {
      0%, 100% { opacity: 1; }
      50% { opacity: 0.3; }
    }
  `
  document.head.appendChild(style)
}

// ---------------------------------------------------------------------------
// JarvisHudView
// ---------------------------------------------------------------------------

export function JarvisHudView(): React.ReactElement {
  // Inject keyframes on mount
  useEffect(() => {
    injectHudKeyframes()
  }, [])

  // -- Jarvis state ---------------------------------------------------------------
  const [jarvisState, setJarvisState] = useState<JarvisState>('idle')

  // -- Panel data --------------------------------------------------------------
  const [sessions, setSessions] = useState<SessionIndicator[]>([])
  const [costs, setCosts] = useState<TotalSpend>({ allTime: 0, thisMonth: 0, today: 0 })
  const [daily, setDaily] = useState<DailyCost[]>([])
  const [activity, setActivity] = useState<ActivityEvent[]>([])
  const [approvals, setApprovals] = useState<ApprovalRequest[]>([])
  const [stats, setStats] = useState<DashboardStats>({
    total: 0, running: 0, pending: 0, done: 0, failed: 0, needsInput: 0,
  })

  // -- Plan state (populated via Jarvis "plan" events) ----------------------
  const [plan, setPlan] = useState<{ goal: string; steps: PlanStep[] } | null>(null)

  // -- Clock state -----------------------------------------------------------
  const [clockStr, setClockStr] = useState<string>(() =>
    new Date().toLocaleTimeString('en-GB', { hour12: false }),
  )
  const [dateStr, setDateStr] = useState<string>(() => {
    const d = new Date()
    return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' }).toUpperCase()
  })

  useEffect(() => {
    const id = setInterval(() => {
      const now = new Date()
      setClockStr(now.toLocaleTimeString('en-GB', { hour12: false }))
      setDateStr(
        now.toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' }).toUpperCase(),
      )
    }, 1000)
    return () => clearInterval(id)
  }, [])

  // -- Flash triggers (glow panels on data changes) ---------------------------
  const sessionsFlash = useFlash(sessions.length)
  const costsFlash = useFlash(costs.today)
  const activityFlash = useFlash(activity.length > 0 ? activity[0]?.id : 0)
  const approvalsFlash = useFlash(approvals.length)

  // -- On-demand flash from Jarvis tool calls ---------------------------------
  const [flashPanel, setFlashPanel] = useState<string | null>(null)

  useEffect(() => {
    const cancel = EventsOn('jarvis', (event: any) => {
      // -- State change from daemon (listening/thinking/speaking/idle) --------
      if (event?.type === 'state_change' || event?.type === 'state') {
        const s = event?.state || event?.text
        if (s === 'listening' || s === 'thinking' || s === 'speaking') {
          setJarvisState(s as JarvisState)
        } else if (s === 'idle' || s === 'running') {
          setJarvisState('idle')
        }
      }

      if (event?.type === 'hud_action' && event?.panel) {
        setFlashPanel(event.panel)
        setTimeout(() => setFlashPanel(null), 600)
      }

      // -- Plan events -------------------------------------------------------
      if (event?.type === 'plan' && event?.goal && Array.isArray(event?.steps)) {
        setPlan({
          goal: event.goal as string,
          steps: (event.steps as string[]).map((s: string) => ({
            text: s,
            status: 'pending' as const,
          })),
        })
      }

      if (event?.type === 'plan_step_update' && typeof event?.stepIndex === 'number') {
        setPlan((prev) => {
          if (!prev) return prev
          const idx = event.stepIndex as number
          const newStatus = (event.status as PlanStep['status']) ?? 'done'
          if (idx < 0 || idx >= prev.steps.length) return prev
          const updated = prev.steps.map((step, i) =>
            i === idx ? { ...step, status: newStatus } : step,
          )
          return { ...prev, steps: updated }
        })
      }
    })
    return () => { cancel() }
  }, [])

  // -- Mounted guard -----------------------------------------------------------
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  // -- Mute state (session-only; intentionally NOT persisted) -----------------
  // Rationale: persisting mute across launches is a footgun -- a user who
  // muted in a previous session would launch a fresh DMG install and find
  // the mic permanently disabled with no obvious cause. Always start
  // unmuted; only honor toggles within the current session.
  const [isMuted, setIsMuted] = useState<boolean>(false)

  // Best-effort: clear any stale legacy key from older builds that did
  // persist mute. Runs once on mount; failures are silently ignored.
  useEffect(() => {
    try {
      localStorage.removeItem('jarvis-muted')
    } catch { /* storage unavailable */ }
  }, [])

  const toggleMute = useCallback(async () => {
    const next = !isMuted
    setIsMuted(next)
    await sendJarvisCommand(next ? '__mute__' : '__unmute__')
  }, [isMuted])

  // -- Keyboard shortcut: Cmd+Shift+M (macOS) / Ctrl+Shift+M -----------------
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent): void {
      if (e.key === 'M' && e.shiftKey && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        void toggleMute()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [toggleMute])

  // -- Derived audio level -----------------------------------------------------
  const audioLevel =
    isMuted ? 0
    : jarvisState === 'speaking' ? 0.7 : jarvisState === 'listening' ? 0.4 : 0

  // Jarvis auto-starts from the Go backend (app.go startup) when JarvisEnabled=true.
  // No need to call startJarvis() from the frontend -- it would race with the
  // backend and cause duplicate auto-greets.

  // -- Polling: Jarvis state (500ms) ----------------------------------------------
  useEffect(() => {
    const poll = async (): Promise<void> => {
      const s = await getJarvisState()
      if (mountedRef.current) setJarvisState(s)
    }
    void poll()
    const id = setInterval(() => void poll(), DEX_STATE_POLL_MS)
    return () => clearInterval(id)
  }, [])

  // -- Polling: Sessions + Approvals (3s) --------------------------------------
  useEffect(() => {
    const poll = async (): Promise<void> => {
      const [s, a] = await Promise.all([getSessionIndicators(), getPendingApprovals()])
      if (mountedRef.current) {
        setSessions(s)
        setApprovals(a)
      }
    }
    void poll()
    const id = setInterval(() => void poll(), SESSIONS_POLL_MS)
    return () => clearInterval(id)
  }, [])

  // -- Polling: Costs (10s) ----------------------------------------------------
  useEffect(() => {
    const poll = async (): Promise<void> => {
      const [c, d] = await Promise.all([getTotalSpend(), getDailyCostSummary()])
      if (mountedRef.current) {
        setCosts(c)
        setDaily(d)
      }
    }
    void poll()
    const id = setInterval(() => void poll(), COSTS_POLL_MS)
    return () => clearInterval(id)
  }, [])

  // -- Polling: Activity (5s) --------------------------------------------------
  useEffect(() => {
    const poll = async (): Promise<void> => {
      const a = await getActivityFeed()
      if (mountedRef.current) setActivity(a)
    }
    void poll()
    const id = setInterval(() => void poll(), ACTIVITY_POLL_MS)
    return () => clearInterval(id)
  }, [])

  // -- Polling: Dashboard stats (5s) -------------------------------------------
  useEffect(() => {
    const poll = async (): Promise<void> => {
      const d = await getDashboardStats()
      if (mountedRef.current) setStats(d)
    }
    void poll()
    const id = setInterval(() => void poll(), STATS_POLL_MS)
    return () => clearInterval(id)
  }, [])

  // ---------------------------------------------------------------------------
  // Derived: which sessions are actively running (for glow-active styling)
  // ---------------------------------------------------------------------------

  const runningCount = sessions.filter(
    (s) => s.lastActivity === 'running' || (!s.hasQuestion && sessions.length > 0),
  ).length

  // ---------------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------------

  return (
    <div
      className="flex-1 flex flex-col min-h-0 relative overflow-hidden"
      style={{ background: '#020a08' }}
    >
      {/* Scanline overlay */}
      <div className="hud-scanlines absolute inset-0" style={{ zIndex: 50, pointerEvents: 'none' }} />

      {/* Subtle radial vignette */}
      <div
        className="absolute inset-0"
        style={{
          background: 'radial-gradient(ellipse at center, transparent 40%, rgba(0,0,0,0.6) 100%)',
          pointerEvents: 'none',
          zIndex: 1,
        }}
      />

      {/* ---- TOP BAR: Time (center) + Status (right) ---- */}
      <div
        className="relative flex items-center justify-between"
        style={{ zIndex: 30, padding: '10px 20px 0 20px', flexShrink: 0 }}
      >
        {/* Left: System label */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <div
            style={{
              width: 6,
              height: 6,
              borderRadius: '50%',
              background: jarvisState === 'idle' ? '#00e5ff' : '#00d4ff',
              boxShadow: `0 0 6px ${jarvisState === 'idle' ? 'rgba(0,229,255,0.6)' : 'rgba(0,212,255,0.6)'}`,
              animation: jarvisState !== 'idle' ? 'hud-pulse-dot 1.5s ease-in-out infinite' : 'none',
            }}
          />
          <span
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 10,
              fontWeight: 700,
              letterSpacing: '0.2em',
              textTransform: 'uppercase' as const,
              color: 'rgba(0,229,255,0.6)',
            }}
          >
            JARVIS v2.0
          </span>
        </div>

        {/* Center: Clock */}
        <div style={{ position: 'absolute', left: '50%', transform: 'translateX(-50%)' }}>
          <div
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 18,
              fontWeight: 300,
              letterSpacing: '0.15em',
              color: 'rgba(0,229,255,0.7)',
              textShadow: '0 0 10px rgba(0,229,255,0.3)',
              textAlign: 'center' as const,
            }}
          >
            {clockStr}
          </div>
          <div
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 9,
              letterSpacing: '0.25em',
              color: 'rgba(0,229,255,0.35)',
              textAlign: 'center' as const,
              marginTop: 1,
            }}
          >
            {dateStr}
          </div>
        </div>

        {/* Right: Status */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          {isMuted && (
            <span
              style={{
                fontFamily: "'SF Mono', 'Menlo', monospace",
                fontSize: 9,
                fontWeight: 700,
                letterSpacing: '0.15em',
                textTransform: 'uppercase' as const,
                color: 'var(--hud-red)',
                textShadow: '0 0 6px rgba(255,68,68,0.4)',
                animation: 'hud-mute-pulse 1.5s ease-in-out infinite',
              }}
            >
              MUTED
            </span>
          )}
          <span
            style={{
              fontFamily: "'SF Mono', 'Menlo', monospace",
              fontSize: 9,
              letterSpacing: '0.15em',
              color: 'rgba(0,229,255,0.4)',
            }}
          >
            SES:{sessions.length} / RUN:{runningCount}
          </span>
        </div>
      </div>

      {/* ---- MAIN HUD AREA ---- */}
      <div className="flex-1 relative" style={{ zIndex: 20, minHeight: 0 }}>
        {/* ---- CENTER: Orb + Concentric Rings ---- */}
        <div
          className="absolute flex items-center justify-center"
          style={{
            top: '50%',
            left: '50%',
            transform: 'translate(-50%, -55%)',
            width: 460,
            height: 460,
          }}
        >
          {/* Radar background */}
          <div
            className="absolute inset-0 rounded-full"
            style={{
              background: `
                radial-gradient(circle,
                  transparent 28%,
                  rgba(0,229,255,0.03) 29%, transparent 30%,
                  transparent 48%,
                  rgba(0,229,255,0.02) 49%, transparent 50%,
                  transparent 68%,
                  rgba(0,229,255,0.015) 69%, transparent 70%
                )
              `,
            }}
          />

          {/* Concentric rotating rings */}
          <HudRings />

          {/* Orb */}
          <JarvisOrb state={jarvisState} audioLevel={audioLevel} className="w-56 h-56" />
        </div>

        {/* ---- LEFT COLUMN: Sessions + Activity ---- */}
        <div
          className="absolute flex flex-col"
          style={{
            top: 16,
            left: 16,
            width: 260,
            bottom: 8,
            gap: 12,
            zIndex: 25,
          }}
        >
          {/* Sessions panel */}
          <HudBracket
            label="SESSIONS"
            flash={`${sessionsFlash} ${flashPanel === 'sessions' ? 'hud-flash' : ''}`}
            className={runningCount > 0 ? 'glow-active' : ''}
          >
            <HudSessionPanel sessions={sessions} stats={stats} />
          </HudBracket>

          {/* Activity panel */}
          <HudBracket
            label="ACTIVITY"
            flash={`${activityFlash} ${flashPanel === 'activity' ? 'hud-flash' : ''}`}
          >
            <HudActivityPanel events={activity} />
          </HudBracket>
        </div>

        {/* ---- RIGHT COLUMN: Costs + Approvals ---- */}
        <div
          className="absolute flex flex-col"
          style={{
            top: 16,
            right: 16,
            width: 260,
            bottom: 8,
            gap: 12,
            zIndex: 25,
          }}
        >
          {/* Costs panel */}
          <HudBracket
            label="COST ANALYSIS"
            flash={`${costsFlash} ${flashPanel === 'costs' ? 'hud-flash' : ''}`}
          >
            <HudCostPanel spend={costs} daily={daily} />
          </HudBracket>

          {/* Approvals panel */}
          <HudBracket
            label="APPROVALS"
            flash={`${approvalsFlash} ${flashPanel === 'approvals' ? 'hud-flash' : ''}`}
            className={approvals.length > 0 ? 'pulse-glow' : ''}
          >
            <HudApprovalPanel approvals={approvals} />
          </HudBracket>
        </div>

        {/* ---- CENTER BOTTOM: Plan (visible only when active) ---- */}
        {plan !== null && (
          <div
            className="absolute"
            style={{
              bottom: 8,
              left: '50%',
              transform: 'translateX(-50%)',
              width: 'calc(100% - 580px)',
              maxWidth: 500,
              zIndex: 26,
            }}
          >
            <HudBracket label="EXECUTION PLAN">
              <PlanPanel goal={plan.goal} steps={plan.steps} />
            </HudBracket>
          </div>
        )}
      </div>

      {/* ---- BOTTOM BAR: Voice + Input ---- */}
      <div
        className="relative flex flex-col"
        style={{
          zIndex: 30,
          flexShrink: 0,
          padding: '0 16px 10px 16px',
          gap: 4,
        }}
      >
        {/* Decorative top line */}
        <div
          style={{
            height: 1,
            background: 'linear-gradient(90deg, transparent, rgba(0,229,255,0.2) 20%, rgba(0,229,255,0.2) 80%, transparent)',
            marginBottom: 4,
          }}
        />

        {/* Voice bar */}
        <div
          style={{
            background: 'rgba(0,12,10,0.6)',
            borderRadius: 4,
            overflow: 'hidden',
          }}
        >
          <HudVoiceBar jarvisState={jarvisState} />
        </div>

        {/* Input bar */}
        <div
          style={{
            background: 'rgba(0,12,10,0.6)',
            borderRadius: 4,
            overflow: 'hidden',
          }}
        >
          <HudInput isMuted={isMuted} onToggleMute={toggleMute} />
        </div>
      </div>

      {/* ---- Floating toast notifications ---- */}
      <JarvisToastContainer />
    </div>
  )
}
