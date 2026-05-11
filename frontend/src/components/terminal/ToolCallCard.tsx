import { useState, useMemo } from 'react'
import type { TerminalBlock } from '../../lib/terminal-parser'
import { cleanOutputContent } from '../../lib/terminal-utils'
import { getToolTheme, ToolIcon } from '../../lib/terminal-theme'

// ---------------------------------------------------------------------------
// ToolCallCard -- renders a single tool invocation as a visually distinct card
//
// Compact mode (session card preview): single line with icon + name + args.
// Normal mode (detail view): full card with args, collapsible output, accent.
// ---------------------------------------------------------------------------

interface ToolCallCardProps {
  block: TerminalBlock
  output?: TerminalBlock
  compact?: boolean
}

/** Max visible output lines before collapsing behind a toggle. */
const COLLAPSE_THRESHOLD = 5

/** Max characters of args shown in compact mode. */
const COMPACT_ARGS_LENGTH = 60

export function ToolCallCard({
  block,
  output,
  compact = false,
}: ToolCallCardProps): React.ReactElement {
  const [outputExpanded, setOutputExpanded] = useState(false)

  const theme = getToolTheme(block.toolName ?? 'default')
  const toolName = block.toolName ?? 'Tool'
  const args = block.toolArgs ?? ''

  // Pre-process output lines so we only split once
  const outputData = useMemo(() => {
    if (!output) return null
    const cleaned = cleanOutputContent(output.content)
    const lines = cleaned.split('\n')
    return { cleaned, lines, total: lines.length }
  }, [output])

  // ---------------------------------------------------------------------------
  // Compact mode -- single line for session card previews
  // ---------------------------------------------------------------------------

  if (compact) {
    const truncatedArgs =
      args.length > COMPACT_ARGS_LENGTH
        ? args.slice(0, COMPACT_ARGS_LENGTH) + '\u2026'
        : args

    return (
      <div className="flex items-center gap-1.5 min-w-0">
        <ToolIcon
          toolName={toolName}
          className={`w-3.5 h-3.5 flex-shrink-0 ${theme.iconColor}`}
        />
        <span
          className={`text-xs font-semibold flex-shrink-0 ${theme.textColor}`}
        >
          {theme.label}
        </span>
        {truncatedArgs.length > 0 && (
          <span className="text-xs font-mono text-secondary truncate min-w-0">
            {truncatedArgs}
          </span>
        )}
      </div>
    )
  }

  // ---------------------------------------------------------------------------
  // Normal mode -- full card with left border accent
  // ---------------------------------------------------------------------------

  const visibleLines =
    outputData && !outputExpanded && outputData.total > COLLAPSE_THRESHOLD
      ? outputData.lines.slice(0, COLLAPSE_THRESHOLD)
      : outputData?.lines ?? []

  const hiddenCount =
    outputData && outputData.total > COLLAPSE_THRESHOLD
      ? outputData.total - COLLAPSE_THRESHOLD
      : 0

  return (
    <div
      className={`rounded-md border-l-2 ${theme.borderColor} ${theme.bgColor} overflow-hidden`}
    >
      {/* Header: icon + tool name */}
      <div className="flex items-center gap-2 px-3 py-2">
        <ToolIcon
          toolName={toolName}
          className={`w-4 h-4 flex-shrink-0 ${theme.iconColor}`}
        />
        <span className={`text-sm font-semibold ${theme.textColor}`}>
          {theme.label}
        </span>
      </div>

      {/* Arguments */}
      {args.length > 0 && (
        <div className="px-3 pb-2">
          <pre className="font-mono text-xs text-primary leading-relaxed line-clamp-2 whitespace-pre-wrap break-all">
            {args}
          </pre>
        </div>
      )}

      {/* Output section */}
      {outputData ? (
        <div className="bg-app border-t border-border-m">
          <pre className="px-3 py-2 font-mono text-xs text-secondary leading-relaxed whitespace-pre-wrap break-all">
            {visibleLines.join('\n')}
          </pre>

          {/* Collapse / expand toggle */}
          {hiddenCount > 0 && (
            <button
              type="button"
              onClick={() => setOutputExpanded((prev) => !prev)}
              className="w-full px-3 py-1.5 text-left text-xs text-acc-blue hover:text-acc-blue/80 hover:bg-surface transition-colors cursor-pointer border-t border-border-m"
            >
              {outputExpanded
                ? 'Show less'
                : `Show ${hiddenCount} more line${hiddenCount === 1 ? '' : 's'}`}
            </button>
          )}
        </div>
      ) : (
        /* No output yet -- running indicator */
        <div className="bg-app border-t border-border-m px-3 py-2">
          <span className="text-xs text-muted italic flex items-center gap-1.5">
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-muted animate-pulse" />
            Running...
          </span>
        </div>
      )}
    </div>
  )
}
