// ---------------------------------------------------------------------------
// BlockRenderers — visually rich leaf components for each TerminalBlock type.
// ---------------------------------------------------------------------------

import type { TerminalBlock } from '../../lib/terminal-parser'
import { BLOCK_THEMES, getToolTheme, ToolIcon } from '../../lib/terminal-theme'
import { ToolCallCard } from './ToolCallCard'

// ---------------------------------------------------------------------------
// TextBlock — Claude's conversational text (chat-bubble style)
// ---------------------------------------------------------------------------

export function TextBlock({ block }: { block: TerminalBlock }): React.ReactElement {
  // Strip ⏺ markers from each line and clean up
  const lines = block.content
    .split('\n')
    .map((l) => l.replace(/^\s*\u23FA\s*/, '').trimEnd())
  // Remove leading/trailing empty lines
  while (lines.length > 0 && lines[0]!.trim() === '') lines.shift()
  while (lines.length > 0 && lines[lines.length - 1]!.trim() === '') lines.pop()
  const content = lines.join('\n')
  if (!content.trim()) return <div className="h-0.5" />

  return (
    <div className="rounded-lg bg-elevated border border-border px-3 py-2.5 text-[13px] text-primary whitespace-pre-wrap leading-relaxed">
      {content}
    </div>
  )
}

// ---------------------------------------------------------------------------
// CompletionBlock — green success banner with duration
// ---------------------------------------------------------------------------

export function CompletionBlock({ block }: { block: TerminalBlock }): React.ReactElement {
  const theme = BLOCK_THEMES['completion']!
  return (
    <div className={`flex items-center gap-2 rounded-lg border px-3 py-2 ${theme.bgColor} ${theme.borderColor}`}>
      <svg width={14} height={14} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth={2.5} strokeLinecap="round" className="text-green-400 flex-shrink-0">
        <polyline points="3,8 6.5,11.5 13,4.5" />
      </svg>
      <span className="text-xs font-semibold text-green-400">Completed</span>
      {block.duration && (
        <span className="text-[11px] text-green-400/70 font-mono">{block.duration}</span>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// PromptBlock — user input with blue caret
// ---------------------------------------------------------------------------

export function PromptBlock({ block }: { block: TerminalBlock }): React.ReactElement {
  const theme = BLOCK_THEMES['prompt']!
  const PROMPT_CHAR = '\u276F'
  const trimmed = block.content.trim()
  const afterPrompt = trimmed.startsWith(PROMPT_CHAR)
    ? trimmed.slice(PROMPT_CHAR.length).trim()
    : trimmed

  if (!afterPrompt) {
    return (
      <div className={`flex items-center gap-1.5 px-3 py-1 rounded ${theme.bgColor}`}>
        <span className="text-blue-400 font-mono text-xs font-bold">{PROMPT_CHAR}</span>
        <span className="w-2 h-4 bg-blue-400/30 rounded-sm animate-pulse" />
      </div>
    )
  }

  return (
    <div className={`flex items-center gap-2 rounded-lg border border-blue-500/20 bg-blue-500/5 px-3 py-2`}>
      <span className="text-blue-400 font-mono text-sm font-bold flex-shrink-0">{PROMPT_CHAR}</span>
      <span className="font-mono text-xs text-primary whitespace-pre-wrap">{afterPrompt}</span>
    </div>
  )
}

// ---------------------------------------------------------------------------
// AgentResultBlock — agent completion notification card
// ---------------------------------------------------------------------------

export function AgentResultBlock({ block }: { block: TerminalBlock }): React.ReactElement {
  const desc = block.toolArgs ?? ''
  return (
    <div className="flex items-center gap-2 rounded-lg border border-green-500/30 bg-green-500/5 px-3 py-2">
      <svg width={14} height={14} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth={2.5} strokeLinecap="round" className="text-green-400 flex-shrink-0">
        <polyline points="3,8 6.5,11.5 13,4.5" />
      </svg>
      <div className="flex items-center gap-1.5 min-w-0">
        <span className="text-[10px] font-bold text-acc-teal bg-teal-500/15 px-1.5 py-0.5 rounded flex-shrink-0">Agent</span>
        <span className="text-xs text-primary truncate">{desc}</span>
        <span className="text-[10px] text-green-400/70 flex-shrink-0">completed</span>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// SummaryBlock — compact chip
// ---------------------------------------------------------------------------

export function SummaryBlock({ block }: { block: TerminalBlock }): React.ReactElement {
  return (
    <div className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-surface text-[10px] text-secondary italic">
      {block.content.trim()}
    </div>
  )
}

// ---------------------------------------------------------------------------
// SeparatorBlock — minimal divider
// ---------------------------------------------------------------------------

export function SeparatorBlock(): React.ReactElement {
  return <div className="my-0.5" />
}

// ---------------------------------------------------------------------------
// CollapsedBlock — expandable hint
// ---------------------------------------------------------------------------

export function CollapsedBlock({ block }: { block: TerminalBlock }): React.ReactElement {
  return (
    <div className="inline-flex items-center gap-1 px-2 py-0.5 text-[10px] text-muted border border-dotted border-border rounded">
      {block.lineCount ? `+${block.lineCount} lines collapsed` : block.content.trim()}
    </div>
  )
}

// ---------------------------------------------------------------------------
// ToolOutputBlock — standalone tool output (not paired with a call)
// ---------------------------------------------------------------------------

function ToolOutputBlock({ block }: { block: TerminalBlock }): React.ReactElement {
  const content = block.content.replace(/\u23BF/g, '').trim()
  return (
    <div className="rounded bg-app border border-border-m px-3 py-1.5 font-mono text-[10px] text-secondary whitespace-pre-wrap leading-relaxed">
      {content}
    </div>
  )
}

// ---------------------------------------------------------------------------
// BlockRenderer — dispatcher
// ---------------------------------------------------------------------------

export function BlockRenderer({
  block,
  output,
  compact,
}: {
  block: TerminalBlock
  output?: TerminalBlock
  compact?: boolean
}): React.ReactElement {
  switch (block.type) {
    case 'tool-call':
    case 'agent-spawn':
      return <ToolCallCard block={block} output={output} compact={compact} />

    case 'agent-result':
      return <AgentResultBlock block={block} />

    case 'text':
      return <TextBlock block={block} />

    case 'completion':
      return <CompletionBlock block={block} />

    case 'prompt':
      return <PromptBlock block={block} />

    case 'summary':
      return <SummaryBlock block={block} />

    case 'separator':
      return <SeparatorBlock />

    case 'collapsed':
      return <CollapsedBlock block={block} />

    case 'tool-output':
      return <ToolOutputBlock block={block} />

    default:
      return <TextBlock block={block} />
  }
}
