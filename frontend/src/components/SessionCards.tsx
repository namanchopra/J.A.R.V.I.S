import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  FocusSession,
  GetSessionTerminalOutput,
  RespondToApproval,
  StopSession,
} from '../../wailsjs/go/main/App'
import type { claude } from '../../wailsjs/go/models'
import { useDuration } from '../lib/hooks'
import { parseTerminalOutput, type TerminalBlock } from '../lib/terminal-parser'
import { BlockRenderer } from './terminal/BlockRenderers'

// ---------------------------------------------------------------------------
// Status dot component
// ---------------------------------------------------------------------------

function StatusDot({ indicator }: { indicator: claude.SessionIndicator }): React.ReactElement {
  const isActive = indicator.lastActivity === 'typing' || indicator.lastActivity === 'tool_use'
  const hasQuestion = indicator.hasQuestion

  if (hasQuestion) {
    return (
      <span className="relative flex h-3 w-3 flex-shrink-0" title="Waiting for input">
        <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-teal-400 opacity-40" />
        <span className="relative inline-flex rounded-full h-3 w-3 bg-teal-400" />
      </span>
    )
  }

  if (isActive) {
    return (
      <span className="relative flex h-3 w-3 flex-shrink-0" title="Actively working">
        <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-40" />
        <span className="relative inline-flex rounded-full h-3 w-3 bg-green-400" />
      </span>
    )
  }

  return (
    <span className="relative flex h-3 w-3 flex-shrink-0" title="Idle">
      <span className="relative inline-flex rounded-full h-3 w-3 bg-muted" />
    </span>
  )
}

// ---------------------------------------------------------------------------
// Terminal output hook (shared between preview and notifications)
// ---------------------------------------------------------------------------

function useTerminalOutput(pid: number): string {
  const [output, setOutput] = useState('')

  useEffect(() => {
    const fetchOutput = (): void => {
      GetSessionTerminalOutput(pid).then(setOutput).catch((err) => console.warn('Terminal output fetch failed:', err))
    }
    fetchOutput()
    const id = setInterval(fetchOutput, 3000)
    return () => clearInterval(id)
  }, [pid])

  return output
}

// ---------------------------------------------------------------------------
// Prompt state detection
// ---------------------------------------------------------------------------

// Patterns that indicate the terminal is at a standard idle prompt or
// the agent is actively working — NOT blocked on user action.
const IDLE_OR_WORKING_PATTERNS = [
  /\?\s*(for\s+shortcuts|for help)/,     // "? for shortcuts" — Claude Code idle
  /esc to interrupt/,                     // agent is actively working, not blocked
  /\btype a message\b/i,                 // generic idle prompt
]

// Detect if the agent needs user attention (permission, approval, question,
// interactive dialog). Inverted approach: instead of matching every possible
// prompt, we check that the terminal is NOT in a known idle/working state
// while the process has been idle 30s+ (hasQuestion from backend).
function detectsNeedsAttention(output: string, hasQuestion: boolean): boolean {
  if (!output || !hasQuestion) return false
  const lines = output.split('\n').filter((l) => l.trim().length > 0).slice(-3)
  const tail = lines.join('\n')
  // If we see a known idle/working pattern → not blocked.
  if (IDLE_OR_WORKING_PATTERNS.some((p) => p.test(tail))) return false
  // Agent idle 30s+ and terminal doesn't look idle → needs attention.
  return true
}

// ---------------------------------------------------------------------------
// Rich terminal preview (last 2-3 parsed blocks)
// ---------------------------------------------------------------------------

/** Block types worth showing in the compact card preview. */
const SIGNIFICANT_PREVIEW_TYPES: ReadonlySet<string> = new Set([
  'tool-call',
  'agent-spawn',
  'text',
  'completion',
])

function RichTerminalPreview({
  parsedBlocks,
  onExpand,
}: {
  parsedBlocks: TerminalBlock[]
  onExpand: () => void
}): React.ReactElement {
  const significantBlocks = parsedBlocks.filter((b) =>
    SIGNIFICANT_PREVIEW_TYPES.has(b.type),
  )
  const lastBlocks = significantBlocks.slice(-3)

  return (
    <button
      type="button"
      onClick={onExpand}
      className="mt-2 w-full text-left rounded bg-app border border-border-m p-2 hover:border-border transition-colors group"
      title="Click to expand terminal view"
    >
      {lastBlocks.length === 0 ? (
        <p className="text-[10px] text-muted py-1">Waiting for output...</p>
      ) : (
        <div className="space-y-1">
          {lastBlocks.map((block, idx) => {
            // If the block is a tool-call or agent-spawn, look for a
            // directly following tool-output in the full (unfiltered) array
            // so ToolCallCard can render the output inline.
            let output: TerminalBlock | undefined
            if (block.type === 'tool-call' || block.type === 'agent-spawn') {
              const fullIdx = parsedBlocks.indexOf(block)
              if (fullIdx !== -1 && fullIdx + 1 < parsedBlocks.length) {
                const next = parsedBlocks[fullIdx + 1]!
                if (next.type === 'tool-output') {
                  output = next
                }
              }
            }

            return (
              <BlockRenderer
                key={idx}
                block={block}
                output={output}
                compact
              />
            )
          })}
        </div>
      )}
      <p className="text-[9px] text-muted mt-1 group-hover:text-secondary transition-colors">
        Click to expand
      </p>
    </button>
  )
}

// ---------------------------------------------------------------------------
// Session notifications
// ---------------------------------------------------------------------------

/**
 * Finds the last "significant" block from parsed terminal output.
 * Significant = tool-call, agent-spawn, or completion. Skips separators,
 * collapsed, tool-output, text, summary, and prompt blocks.
 */
function findLatestSignificantBlock(blocks: TerminalBlock[]): TerminalBlock | null {
  for (let i = blocks.length - 1; i >= 0; i--) {
    const block = blocks[i]!
    if (block.type === 'tool-call' || block.type === 'agent-spawn' || block.type === 'completion') {
      return block
    }
  }
  return null
}

function SessionNotifications({
  indicator,
  terminalOutput,
  parsedBlocks,
}: {
  indicator: claude.SessionIndicator
  terminalOutput: string
  parsedBlocks: TerminalBlock[]
}): React.ReactElement | null {
  const alerts: { icon: string; text: string; color: string; bg: string }[] = []

  const needsApproval = detectsNeedsAttention(terminalOutput, indicator.hasQuestion)

  if (needsApproval) {
    alerts.push({
      icon: '!',
      text: 'Needs approval — waiting for permission',
      color: 'text-acc-teal',
      bg: 'bg-teal-500/10 border-acc-teal/20',
    })
  }

  // Derive richer status from parsed blocks
  const latestBlock = findLatestSignificantBlock(parsedBlocks)

  if (indicator.lastActivity === 'typing') {
    alerts.push({
      icon: '~',
      text: 'Writing code',
      color: 'text-green-400',
      bg: 'bg-green-500/10 border-green-500/20',
    })
  } else if (indicator.lastActivity === 'tool_use') {
    // Use parsed block for a more specific tool name when available
    if (latestBlock?.type === 'tool-call' && latestBlock.toolName) {
      alerts.push({
        icon: '>',
        text: `Running: ${latestBlock.toolName}`,
        color: 'text-blue-400',
        bg: 'bg-blue-500/10 border-blue-500/20',
      })
    } else if (latestBlock?.type === 'agent-spawn') {
      const desc = (latestBlock.toolArgs ?? '').slice(0, 40) + ((latestBlock.toolArgs ?? '').length > 40 ? '...' : '')
      alerts.push({
        icon: '>',
        text: desc ? `Agent: ${desc}` : 'Agent spawned',
        color: 'text-acc-teal',
        bg: 'bg-teal-500/10 border-acc-teal/20',
      })
    } else {
      alerts.push({
        icon: '>',
        text: 'Running tools',
        color: 'text-blue-400',
        bg: 'bg-blue-500/10 border-blue-500/20',
      })
    }
  } else if (latestBlock?.type === 'completion' && latestBlock.duration) {
    // Session recently completed — show duration
    alerts.push({
      icon: '*',
      text: `Completed in ${latestBlock.duration}`,
      color: 'text-green-400',
      bg: 'bg-green-500/10 border-green-500/20',
    })
  } else if (indicator.hasQuestion && !needsApproval) {
    alerts.push({
      icon: '.',
      text: 'Idle — finished, waiting for new prompt',
      color: 'text-muted',
      bg: 'bg-border/5 border-border/50',
    })
  }

  if (alerts.length === 0) return null

  return (
    <div className="mt-2 space-y-1">
      {alerts.map((a, i) => (
        <div key={i} className={`flex items-center gap-2 rounded border ${a.bg} px-2 py-1`}>
          <span className={`text-[10px] font-bold ${a.color} flex-shrink-0 w-3 text-center`}>{a.icon}</span>
          <span className={`text-[10px] ${a.color}`}>{a.text}</span>
        </div>
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Single session card
// ---------------------------------------------------------------------------

export function SessionCard({
  indicator,
  onExpandTerminal,
}: {
  indicator: claude.SessionIndicator
  onExpandTerminal: (pid: number) => void
}): React.ReactElement {
  const duration = useDuration(indicator.startedAt)
  const displayName = indicator.name || indicator.cwd.split('/').pop() || 'Session'
  const terminalOutput = useTerminalOutput(indicator.pid)
  const parsedBlocks = useMemo(() => parseTerminalOutput(terminalOutput), [terminalOutput])
  const needsApproval = detectsNeedsAttention(terminalOutput, indicator.hasQuestion)

  const handleFocus = useCallback(async (): Promise<void> => {
    try {
      await FocusSession(indicator.pid)
    } catch (err) {
      console.warn('Failed to focus session:', err)
    }
  }, [indicator.pid])

  const handleApprove = useCallback(async (): Promise<void> => {
    try {
      await RespondToApproval(indicator.pid, 'y')
    } catch (err) {
      console.warn('Failed to approve session:', err)
    }
  }, [indicator.pid])

  const handleStop = useCallback(async (): Promise<void> => {
    if (!window.confirm(`Stop session "${displayName}"?`)) return
    try {
      await StopSession(indicator.sessionId)
    } catch (err) {
      console.warn('Failed to stop session:', err)
    }
  }, [indicator.sessionId, displayName])

  const isActive = indicator.lastActivity === 'typing' || indicator.lastActivity === 'tool_use'
  const isRunning = isActive || indicator.lastActivity === 'idle' || indicator.lastActivity === 'waiting'
  const borderColor = needsApproval
    ? 'border-acc-teal/30'
    : isActive
      ? 'border-green-500/20'
      : 'border-border'

  return (
    <div
      className={`rounded-lg border ${borderColor} bg-surface overflow-hidden transition-colors flex flex-col`}
    >
      <div className="p-4 flex-1">
        {/* Main row: status + info */}
        <div className="flex items-start gap-3">
          {/* Left: status dot */}
          <div className="pt-1">
            <StatusDot indicator={indicator} />
          </div>

          {/* Center: info */}
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <h4 className="text-sm font-semibold text-primary truncate">{displayName}</h4>
              {needsApproval && (
                <span className="text-[10px] px-1.5 py-0.5 rounded bg-teal-500/15 text-acc-teal flex-shrink-0">
                  needs approval
                </span>
              )}
            </div>
            <p className="text-[11px] font-mono text-muted mt-0.5 truncate">{indicator.cwd}</p>
            <div className="flex items-center gap-3 mt-1">
              <span className="text-[10px] text-secondary">{duration}</span>
              {indicator.tokensUsed > 0 && (
                <span className="text-[10px] text-muted">
                  {(indicator.tokensUsed / 1000).toFixed(1)}k tokens
                </span>
              )}
            </div>
          </div>
        </div>

        {/* Terminal preview */}
        <RichTerminalPreview parsedBlocks={parsedBlocks} onExpand={() => onExpandTerminal(indicator.pid)} />

        {/* Session notifications */}
        <SessionNotifications indicator={indicator} terminalOutput={terminalOutput} parsedBlocks={parsedBlocks} />
      </div>

      {/* Quick action buttons */}
      <div className="px-4 pb-3 pt-1 flex items-center gap-2">
        <button
          type="button"
          onClick={() => void handleFocus()}
          className="px-2.5 py-1 text-[11px] rounded-full bg-cyan-500/15 hover:bg-cyan-500/25 text-cyan-400 transition-colors font-medium"
        >
          Focus
        </button>
        <button
          type="button"
          onClick={async () => {
            const w = window as unknown as Record<string, unknown>
            const goNs = w?.go as Record<string, unknown> | undefined
            const appObj = (goNs?.main as Record<string, unknown>)?.App as Record<string, unknown> | undefined
            const forkFn = appObj?.ForkSession as
              ((id: string) => Promise<unknown>) | undefined
            if (forkFn && indicator.sessionId) {
              try {
                await forkFn(indicator.sessionId)
              } catch (e) {
                console.warn('Fork failed:', e)
              }
            }
          }}
          className="px-2.5 py-1 text-[11px] rounded-full bg-indigo-500/15 hover:bg-indigo-500/25 text-indigo-400 transition-colors font-medium flex items-center gap-1"
          title="Fork session"
        >
          <svg className="w-3 h-3" viewBox="0 0 16 16" fill="currentColor">
            <path fillRule="evenodd" d="M5 3.25a.75.75 0 11-1.5 0 .75.75 0 011.5 0zm0 2.122a2.25 2.25 0 10-1.5 0v.878A2.25 2.25 0 005.75 8.5h1.5v2.128a2.251 2.251 0 101.5 0V8.5h1.5a2.25 2.25 0 002.25-2.25v-.878a2.25 2.25 0 10-1.5 0v.878a.75.75 0 01-.75.75h-4.5A.75.75 0 015 6.25v-.878zm3.75 7.378a.75.75 0 11-1.5 0 .75.75 0 011.5 0zm3-8.75a.75.75 0 100-1.5.75.75 0 000 1.5z" clipRule="evenodd" />
          </svg>
          Fork
        </button>
        {indicator.hasQuestion && (
          <button
            type="button"
            onClick={() => void handleApprove()}
            className="px-2.5 py-1 text-[11px] rounded-full bg-green-500/15 hover:bg-green-500/25 text-green-400 transition-colors font-medium"
          >
            Approve
          </button>
        )}
        {isRunning && (
          <button
            type="button"
            onClick={() => void handleStop()}
            className="px-2.5 py-1 text-[11px] rounded-full bg-red-500/15 hover:bg-red-500/25 text-red-400 transition-colors font-medium"
          >
            Stop
          </button>
        )}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Active sessions section
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Filter types
// ---------------------------------------------------------------------------

export type StatusFilter = 'all' | 'running' | 'idle' | 'waiting'
export type AgentFilter = 'all' | 'claude-code' | 'kiro' | 'gemini' | 'codex' | 'aider'

// ---------------------------------------------------------------------------
// Active sessions section with grid + filter bar
// ---------------------------------------------------------------------------

export function ActiveSessionsSection({
  indicators,
  onExpandTerminal,
}: {
  indicators: claude.SessionIndicator[]
  onExpandTerminal: (pid: number) => void
}): React.ReactElement {
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [agentFilter, setAgentFilter] = useState<AgentFilter>('all')

  const filtered = useMemo(() => {
    let list = indicators

    if (statusFilter !== 'all') {
      list = list.filter((ind) => {
        const isActive = ind.lastActivity === 'typing' || ind.lastActivity === 'tool_use'
        switch (statusFilter) {
          case 'running':
            return isActive
          case 'idle':
            return ind.lastActivity === 'idle' && !ind.hasQuestion
          case 'waiting':
            return ind.hasQuestion
          default:
            return true
        }
      })
    }

    if (agentFilter !== 'all') {
      list = list.filter((ind) => {
        // The cwd or name might contain the agent type; the indicator.name
        // often reflects the agent. We match loosely against the name field.
        const nameLower = (ind.name || '').toLowerCase()
        return nameLower.includes(agentFilter)
      })
    }

    return list
  }, [indicators, statusFilter, agentFilter])

  if (indicators.length === 0) {
    return (
      <section className="px-5 py-6">
        <h2 className="text-sm font-semibold text-primary mb-1">Active Sessions</h2>
        <p className="text-xs text-muted">
          No active Claude Code sessions. Open a Claude Code session in any terminal to see it here.
        </p>
      </section>
    )
  }

  return (
    <section className="px-5 py-4">
      <h2 className="text-sm font-semibold text-primary mb-3">Active Sessions</h2>

      {/* Filter bar */}
      <div className="flex items-center gap-3 mb-4">
        <FilterSelect<StatusFilter>
          label="Status"
          value={statusFilter}
          onChange={setStatusFilter}
          options={[
            { value: 'all', label: 'All' },
            { value: 'running', label: 'Running' },
            { value: 'idle', label: 'Idle' },
            { value: 'waiting', label: 'Waiting for input' },
          ]}
        />
        <FilterSelect<AgentFilter>
          label="Agent"
          value={agentFilter}
          onChange={setAgentFilter}
          options={[
            { value: 'all', label: 'All' },
            { value: 'claude-code', label: 'claude-code' },
            { value: 'kiro', label: 'kiro' },
            { value: 'gemini', label: 'gemini' },
            { value: 'codex', label: 'codex' },
            { value: 'aider', label: 'aider' },
          ]}
        />
        {(statusFilter !== 'all' || agentFilter !== 'all') && (
          <span className="text-[10px] text-muted">
            {filtered.length} / {indicators.length}
          </span>
        )}
      </div>

      {/* Grid layout */}
      {filtered.length === 0 ? (
        <div className="flex items-center justify-center py-12">
          <p className="text-sm text-muted">No active sessions match the current filters.</p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {filtered.map((ind) => (
            <SessionCard key={ind.pid} indicator={ind} onExpandTerminal={onExpandTerminal} />
          ))}
        </div>
      )}
    </section>
  )
}

// ---------------------------------------------------------------------------
// Filter select component
// ---------------------------------------------------------------------------

function FilterSelect<T extends string>({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: T
  onChange: (v: T) => void
  options: { value: T; label: string }[]
}): React.ReactElement {
  return (
    <label className="flex items-center gap-1.5 text-[11px] text-secondary">
      {label}
      <select
        value={value}
        onChange={(e) => onChange(e.target.value as T)}
        className="bg-surface border border-border rounded px-2 py-1 text-[11px] text-primary
                   focus:outline-none focus:border-acc-teal/50 transition-colors"
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    </label>
  )
}
