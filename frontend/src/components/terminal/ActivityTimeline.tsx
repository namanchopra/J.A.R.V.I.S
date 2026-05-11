// ---------------------------------------------------------------------------
// ActivityTimeline -- vertical activity timeline derived from parsed terminal
// blocks. Shows tool calls, agent spawns, completions, and user prompts in
// reverse chronological order (most recent at top).
// ---------------------------------------------------------------------------

import { useMemo } from 'react'
import type { TerminalBlock } from '../../lib/terminal-parser'

// ---------------------------------------------------------------------------
// Timeline entry types
// ---------------------------------------------------------------------------

type EntryKind = 'tool' | 'edit' | 'agent' | 'completion' | 'prompt'

interface TimelineEntry {
  kind: EntryKind
  description: string
}

// ---------------------------------------------------------------------------
// Dot color mapping per entry kind (GitHub dark palette accents)
// ---------------------------------------------------------------------------

const DOT_COLORS: Record<EntryKind, string> = {
  tool: 'bg-blue-400',
  edit: 'bg-green-400',
  agent: 'bg-teal-400',
  completion: 'bg-green-400',
  prompt: 'bg-secondary',
}

const TEXT_COLORS: Record<EntryKind, string> = {
  tool: 'text-blue-300',
  edit: 'text-green-300',
  agent: 'text-teal-300',
  completion: 'text-green-300',
  prompt: 'text-primary',
}

// ---------------------------------------------------------------------------
// Tool name to human-readable description
// ---------------------------------------------------------------------------

const TOOL_DESCRIPTIONS: Record<string, string> = {
  Bash: 'Running Bash',
  Read: 'Reading file',
  Edit: 'Editing file',
  Write: 'Writing file',
  MultiEdit: 'Editing file',
  Glob: 'Searching files',
  Grep: 'Searching content',
  Skill: 'Using skill',
  AskUserQuestion: 'Asking user',
  WebSearch: 'Searching web',
  WebFetch: 'Fetching URL',
  TodoRead: 'Reading todos',
  TodoWrite: 'Writing todos',
}

/** Tools that count as "edit" actions for dot coloring purposes. */
const EDIT_TOOLS: ReadonlySet<string> = new Set([
  'Edit',
  'Write',
  'MultiEdit',
])

// ---------------------------------------------------------------------------
// Block-to-entry extraction
// ---------------------------------------------------------------------------

function extractEntries(blocks: TerminalBlock[]): TimelineEntry[] {
  const entries: TimelineEntry[] = []

  for (const block of blocks) {
    switch (block.type) {
      case 'tool-call': {
        const toolName = block.toolName ?? 'Unknown'
        const description =
          TOOL_DESCRIPTIONS[toolName] ?? `Running ${toolName}`
        const kind: EntryKind = EDIT_TOOLS.has(toolName) ? 'edit' : 'tool'
        entries.push({ kind, description })
        break
      }

      case 'agent-spawn': {
        const args = block.toolArgs ?? ''
        const truncated =
          args.length > 40 ? args.slice(0, 40) + '\u2026' : args
        entries.push({
          kind: 'agent',
          description: `Spawned agent: ${truncated}`,
        })
        break
      }

      case 'completion': {
        const duration = block.duration
        const description =
          duration != null
            ? `Completed in ${duration}`
            : 'Completed'
        entries.push({ kind: 'completion', description })
        break
      }

      case 'prompt': {
        // Only include prompts that have actual user content after the
        // marker character. An empty prompt line (just "❯") is idle state.
        const content = block.content.trim()
        // The prompt marker is ❯ (U+276F). Strip it and check for content.
        const afterMarker = content.replace(/^[\u276F\s]+/, '').trim()
        if (afterMarker.length > 0) {
          entries.push({ kind: 'prompt', description: 'User prompt' })
        }
        break
      }

      // All other block types are skipped:
      // text, tool-output, separator, collapsed, summary
      default:
        break
    }
  }

  return entries
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface ActivityTimelineProps {
  blocks: TerminalBlock[]
}

export function ActivityTimeline({
  blocks,
}: ActivityTimelineProps): React.ReactElement {
  const entries = useMemo(() => extractEntries(blocks).reverse(), [blocks])

  // -- Empty state -----------------------------------------------------------
  if (entries.length === 0) {
    return (
      <div className="flex items-center justify-center py-8">
        <p className="text-sm text-muted">No activity yet</p>
      </div>
    )
  }

  // -- Timeline --------------------------------------------------------------
  return (
    <div className="relative max-h-64 overflow-y-auto pr-1 scrollbar-thin">
      {/* Vertical line */}
      <div
        className="absolute left-[5px] top-0 bottom-0 w-px bg-border-m"
        aria-hidden="true"
      />

      <ul className="space-y-2 py-1" role="list" aria-label="Activity timeline">
        {entries.map((entry, idx) => (
          <li key={idx} className="relative flex items-start gap-3 pl-0">
            {/* Colored dot on the vertical line */}
            <span
              className={`relative z-10 mt-[5px] h-[6px] w-[6px] flex-shrink-0 rounded-full ${DOT_COLORS[entry.kind]}`}
              aria-hidden="true"
            />

            {/* Description */}
            <span
              className={`text-xs leading-4 ${TEXT_COLORS[entry.kind]} truncate`}
            >
              {entry.description}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}
