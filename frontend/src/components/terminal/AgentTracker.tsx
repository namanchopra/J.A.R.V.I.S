// ---------------------------------------------------------------------------
// AgentTracker -- detects Agent() spawn blocks from parsed terminal output and
// renders each subagent as a trackable, collapsible card.
//
// Scans the blocks array for `agent-spawn` entries, determines whether each
// agent has completed (by checking for a subsequent `tool-output` block), and
// renders the list as a "Parallel Agents" section.
// ---------------------------------------------------------------------------

import { useState, useMemo, useCallback } from 'react'
import type { TerminalBlock } from '../../lib/terminal-parser'
import { cleanOutputContent } from '../../lib/terminal-utils'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface AgentTrackerProps {
  blocks: TerminalBlock[]
}

interface TrackedAgent {
  /** Index of the agent-spawn block within the blocks array. */
  index: number
  /** Description text extracted from toolArgs. */
  description: string
  /** Whether a tool-output block follows this agent spawn. */
  completed: boolean
  /** First two lines of the output preview (if completed). */
  outputPreview: string | null
  /** Full output content for expanded view. */
  fullOutput: string | null
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Build the list of tracked agents from the parsed blocks array.
 *
 * For each `agent-spawn` block, look at subsequent blocks (skipping
 * separators) to find a `tool-output` that serves as the agent's result.
 */
function buildTrackedAgents(blocks: TerminalBlock[]): TrackedAgent[] {
  const agents: TrackedAgent[] = []

  for (let i = 0; i < blocks.length; i++) {
    const block = blocks[i]!
    if (block.type !== 'agent-spawn') continue

    const description = block.toolArgs?.trim() ?? 'Subagent'

    // Look ahead for the next non-separator, non-text block to determine
    // whether this agent produced output.
    let completed = false
    let outputPreview: string | null = null
    let fullOutput: string | null = null

    for (let j = i + 1; j < blocks.length; j++) {
      const next = blocks[j]!

      // Skip whitespace/separator blocks when scanning ahead.
      if (next.type === 'separator') continue

      if (next.type === 'tool-output') {
        completed = true
        const cleaned = cleanOutputContent(next.content).trim()
        fullOutput = cleaned
        const lines = cleaned.split('\n')
        outputPreview = lines.slice(0, 2).join('\n')
      }

      // Stop scanning once we hit any non-separator block (whether it was
      // tool-output or something else).
      break
    }

    agents.push({
      index: i,
      description,
      completed,
      outputPreview,
      fullOutput,
    })
  }

  return agents
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

/** Inline SVG check icon for the completed state. */
function CheckIcon(): React.ReactElement {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={12}
      height={12}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className="text-green-400 flex-shrink-0"
    >
      <polyline points="3,8 6.5,11.5 13,4.5" />
    </svg>
  )
}

/** CSS-animated spinning dot for the running state. */
function SpinningDot(): React.ReactElement {
  return (
    <span
      className="
        inline-block w-2 h-2 rounded-full
        border-[1.5px] border-teal-400 border-t-transparent
        animate-spin flex-shrink-0
      "
      aria-hidden="true"
    />
  )
}

// ---------------------------------------------------------------------------
// AgentCard
// ---------------------------------------------------------------------------

interface AgentCardProps {
  agent: TrackedAgent
}

function AgentCard({ agent }: AgentCardProps): React.ReactElement {
  const [expanded, setExpanded] = useState(false)

  const handleToggle = useCallback(() => {
    if (agent.completed && agent.fullOutput) {
      setExpanded((prev) => !prev)
    }
  }, [agent.completed, agent.fullOutput])

  const borderClass = agent.completed
    ? 'border-l-2 border-l-green-500'
    : 'border-l-2 border-l-teal-500'

  const isExpandable = agent.completed && agent.fullOutput !== null

  return (
    <div
      className={`
        ${borderClass}
        bg-app rounded-r-md
        transition-colors
        ${isExpandable ? 'cursor-pointer hover:bg-surface' : ''}
      `}
      onClick={handleToggle}
      role={isExpandable ? 'button' : undefined}
      tabIndex={isExpandable ? 0 : undefined}
      onKeyDown={
        isExpandable
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                handleToggle()
              }
            }
          : undefined
      }
      aria-expanded={isExpandable ? expanded : undefined}
    >
      {/* Header row */}
      <div className="flex items-center gap-2 px-3 py-2">
        {agent.completed ? <CheckIcon /> : <SpinningDot />}

        <span className="text-xs text-primary truncate min-w-0 flex-1">
          {agent.description}
        </span>

        {!agent.completed && (
          <span className="text-[10px] text-acc-teal italic flex-shrink-0">
            Running...
          </span>
        )}

        {isExpandable && (
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width={10}
            height={10}
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            strokeWidth={2}
            strokeLinecap="round"
            strokeLinejoin="round"
            className={`
              text-muted flex-shrink-0
              transition-transform duration-150
              ${expanded ? 'rotate-180' : ''}
            `}
          >
            <polyline points="4,6 8,10 12,6" />
          </svg>
        )}
      </div>

      {/* Collapsed output preview (first 2 lines) */}
      {agent.completed && agent.outputPreview && !expanded && (
        <div className="px-3 pb-2 -mt-1">
          <pre className="text-[10px] text-secondary font-mono whitespace-pre-wrap break-all line-clamp-2 leading-relaxed">
            {agent.outputPreview}
          </pre>
        </div>
      )}

      {/* Expanded full output */}
      {expanded && agent.fullOutput && (
        <div className="px-3 pb-2 -mt-1 border-t border-border-m pt-2">
          <pre className="text-[10px] text-secondary font-mono whitespace-pre-wrap break-all leading-relaxed">
            {agent.fullOutput}
          </pre>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// AgentTracker (main export)
// ---------------------------------------------------------------------------

export function AgentTracker({ blocks }: AgentTrackerProps): React.ReactElement | null {
  const agents = useMemo(() => buildTrackedAgents(blocks), [blocks])

  // No agent-spawn blocks -- render nothing
  if (agents.length === 0) return null

  return (
    <div className="bg-surface border border-border rounded-lg overflow-hidden">
      {/* Section header */}
      <div className="flex items-center gap-2 px-3 py-2">
        <span className="text-xs font-semibold text-primary">
          Parallel Agents
        </span>
        <span className="bg-teal-500/15 text-acc-teal text-[10px] rounded-full px-1.5 py-px font-medium">
          {agents.length}
        </span>
      </div>

      {/* Agent list */}
      <div className="flex flex-col gap-1 px-2 pb-2">
        {agents.map((agent) => (
          <AgentCard key={agent.index} agent={agent} />
        ))}
      </div>
    </div>
  )
}
