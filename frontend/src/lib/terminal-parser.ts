// ---------------------------------------------------------------------------
// Terminal output parser -- converts raw Claude Code terminal text (from CMux
// `read_text`) into structured typed blocks.
//
// The input contains Unicode markers (⏺, ⎿, ✻, ❯, …, ─) but NO ANSI escape
// codes — CMux strips those before we see the text.
// ---------------------------------------------------------------------------

export type BlockType =
  | 'tool-call'
  | 'tool-output'
  | 'text'
  | 'completion'
  | 'prompt'
  | 'collapsed'
  | 'summary'
  | 'separator'
  | 'agent-spawn'
  | 'agent-result'

export interface TerminalBlock {
  type: BlockType
  /** Raw text content of the block (may span multiple lines). */
  content: string
  /** Tool name for tool-call and agent-spawn blocks. */
  toolName?: string
  /** Tool arguments for tool-call blocks. */
  toolArgs?: string
  /** Human-readable duration for completion blocks (e.g. "2m 29s"). */
  duration?: string
  /** Number of collapsed lines for collapsed blocks. */
  lineCount?: number
}

// ---------------------------------------------------------------------------
// Known tool names emitted by Claude Code. Kept as a Set for O(1) lookup.
// ---------------------------------------------------------------------------

const KNOWN_TOOLS: ReadonlySet<string> = new Set([
  'Bash',
  'Read',
  'Edit',
  'Write',
  'Glob',
  'Grep',
  'Skill',
  'Agent',
  'AskUserQuestion',
  'WebSearch',
  'WebFetch',
  'TodoRead',
  'TodoWrite',
  'MultiEdit',
])

// ---------------------------------------------------------------------------
// Marker characters
// ---------------------------------------------------------------------------

/** Main bullet used by Claude for text output and tool calls. */
const MARKER_BULLET = '\u23FA' // ⏺
/** Indented output prefix from tool results. */
const MARKER_OUTPUT = '\u23BF' // ⎿
/** Completion / status asterisk. */
const MARKER_COMPLETION = '\u273B' // ✻
/** Prompt caret. */
const MARKER_PROMPT = '\u276F' // ❯
/** Ellipsis for collapsed sections. */
const MARKER_COLLAPSED = '\u2026' // …

// Box-drawing / separator characters used to detect separator lines.
const SEPARATOR_CHARS: ReadonlySet<string> = new Set([
  '\u2500', // ─
  '\u2501', // ━
  '\u2550', // ═
  '\u2502', // │
  '\u2503', // ┃
  '\u2551', // ║
  '\u250C', // ┌
  '\u250D', // ┍
  '\u250E', // ┎
  '\u250F', // ┏
  '\u2510', // ┐
  '\u2514', // └
  '\u2518', // ┘
  '\u253C', // ┼
  '\u2524', // ┤
  '\u251C', // ├
  '\u252C', // ┬
  '\u2534', // ┴
  '\u2015', // ―  (horizontal bar)
  '\u2014', // —  (em dash)
])

// ---------------------------------------------------------------------------
// Summary patterns — these lines typically describe a completed action and
// are followed by "(ctrl+o to expand)" or similar.
// ---------------------------------------------------------------------------

const SUMMARY_PATTERNS: readonly RegExp[] = [
  /^Read \d+ files?/,
  /^Wrote \d+ files?/,
  /^Edited \d+ files?/,
  /^Searched for \d+ patterns?/,
  /^Queried codemap \d+ times?/,
  /^Ran \d+ commands?/,
  /^Created \d+ files?/,
  /^Deleted \d+ files?/,
  /^Updated \d+ files?/,
  /\(ctrl\+o to expand\)$/,
]

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

/**
 * Tests whether a line is composed entirely of whitespace and/or
 * box-drawing / separator characters.
 */
function isSeparatorLine(line: string): boolean {
  if (line.length === 0) return false
  for (let i = 0; i < line.length; i++) {
    const ch = line[i]!
    if (ch === ' ' || ch === '\t' || SEPARATOR_CHARS.has(ch)) {
      continue
    }
    return false
  }
  return true
}

/**
 * Extracts a duration string from completion text.
 * Handles forms like "Worked for 2m 29s", "Worked for 45s", "Worked for 1h 3m".
 */
function extractDuration(text: string): string | undefined {
  const idx = text.indexOf('Worked for ')
  if (idx === -1) return undefined
  const afterPrefix = text.slice(idx + 'Worked for '.length).trim()
  // Duration runs until end-of-string or a non-duration character.
  // Valid chars: digits, h, m, s, spaces.
  let end = 0
  for (let i = 0; i < afterPrefix.length; i++) {
    const ch = afterPrefix[i]!
    if (
      (ch >= '0' && ch <= '9') ||
      ch === 'h' ||
      ch === 'm' ||
      ch === 's' ||
      ch === ' '
    ) {
      end = i + 1
    } else {
      break
    }
  }
  const result = afterPrefix.slice(0, end).trim()
  return result.length > 0 ? result : undefined
}

/**
 * Extracts the collapsed line count from a collapsed-section marker.
 * Example: "… +42 lines (ctrl+o to expand)" → 42
 */
function extractLineCount(text: string): number | undefined {
  // Look for "+N lines" or "+N line"
  const plusIdx = text.indexOf('+')
  if (plusIdx === -1) return undefined
  let numStr = ''
  for (let i = plusIdx + 1; i < text.length; i++) {
    const ch = text[i]!
    if (ch >= '0' && ch <= '9') {
      numStr += ch
    } else {
      break
    }
  }
  if (numStr.length === 0) return undefined
  const parsed = parseInt(numStr, 10)
  return isNaN(parsed) ? undefined : parsed
}

/**
 * Checks whether `text` (after the ⏺ marker) starts with a known tool name
 * followed immediately by `(`. Returns the tool name and the start index of
 * the opening paren, or null if no match.
 *
 * Uses indexOf-based extraction (not regex) so args containing special
 * characters, nested parens, etc. are handled safely.
 */
function matchToolCall(
  text: string,
): { toolName: string; argsStart: number } | null {
  for (const tool of KNOWN_TOOLS) {
    if (text.length < tool.length + 1) continue
    // Check that text starts with the tool name followed by '('
    if (text.startsWith(tool) && text[tool.length] === '(') {
      return { toolName: tool, argsStart: tool.length + 1 }
    }
  }
  return null
}

/**
 * Extracts tool arguments from text starting after the opening `(`.
 * Finds the *last* `)` in the string to handle nested parens in args.
 * If no closing paren is found (truncated output), returns everything
 * after the opening paren.
 */
function extractToolArgs(text: string, argsStart: number): string {
  const lastParen = text.lastIndexOf(')')
  if (lastParen > argsStart) {
    return text.slice(argsStart, lastParen)
  }
  // Truncated — return everything we have
  return text.slice(argsStart)
}

/**
 * Checks if a trimmed line matches a summary pattern.
 */
function isSummaryLine(trimmed: string): boolean {
  for (const pattern of SUMMARY_PATTERNS) {
    if (pattern.test(trimmed)) return true
  }
  return false
}

// ---------------------------------------------------------------------------
// Main parser
// ---------------------------------------------------------------------------

/**
 * Parses raw Claude Code terminal output into an array of structured blocks.
 *
 * The input is plain text with Unicode markers — no ANSI escapes.
 * Consecutive lines of the same groupable type (tool-output, separator) are
 * merged into a single block.
 */
export function parseTerminalOutput(raw: string): TerminalBlock[] {
  if (raw.length === 0) return []

  const lines = raw.split('\n')
  const blocks: TerminalBlock[] = []

  let i = 0
  while (i < lines.length) {
    const line = lines[i]!
    const trimmed = line.trim()

    // -- Empty lines are treated as separators so they can merge with
    //    adjacent separator blocks, or stand alone as whitespace blocks.
    if (trimmed.length === 0) {
      // Group consecutive empty / separator lines
      const startIdx = i
      const collected: string[] = [line]
      i++
      while (i < lines.length) {
        const next = lines[i]!
        const nextTrimmed = next.trim()
        if (nextTrimmed.length === 0 || isSeparatorLine(nextTrimmed)) {
          collected.push(next)
          i++
        } else {
          break
        }
      }
      blocks.push({
        type: 'separator',
        content: collected.join('\n'),
      })
      continue
    }

    // -- Separator lines (box-drawing characters only)
    if (isSeparatorLine(trimmed)) {
      const collected: string[] = [line]
      i++
      while (i < lines.length) {
        const next = lines[i]!
        const nextTrimmed = next.trim()
        if (
          nextTrimmed.length === 0 ||
          isSeparatorLine(nextTrimmed)
        ) {
          collected.push(next)
          i++
        } else {
          break
        }
      }
      blocks.push({
        type: 'separator',
        content: collected.join('\n'),
      })
      continue
    }

    // -- Tool output lines (⎿ prefix)
    if (trimmed.startsWith(MARKER_OUTPUT)) {
      const collected: string[] = [line]
      i++
      while (i < lines.length) {
        const next = lines[i]!
        if (next.trim().startsWith(MARKER_OUTPUT)) {
          collected.push(next)
          i++
        } else {
          break
        }
      }
      blocks.push({
        type: 'tool-output',
        content: collected.join('\n'),
      })
      continue
    }

    // -- Completion lines (✻ marker)
    if (trimmed.startsWith(MARKER_COMPLETION)) {
      const afterMarker = trimmed.slice(MARKER_COMPLETION.length).trim()
      const block: TerminalBlock = {
        type: 'completion',
        content: line,
      }
      const dur = extractDuration(afterMarker)
      if (dur !== undefined) {
        block.duration = dur
      }
      blocks.push(block)
      i++
      continue
    }

    // -- Prompt lines (❯ marker)
    if (trimmed.startsWith(MARKER_PROMPT)) {
      blocks.push({
        type: 'prompt',
        content: line,
      })
      i++
      continue
    }

    // -- Collapsed sections (… marker)
    if (trimmed.startsWith(MARKER_COLLAPSED)) {
      const block: TerminalBlock = {
        type: 'collapsed',
        content: line,
      }
      const count = extractLineCount(trimmed)
      if (count !== undefined) {
        block.lineCount = count
      }
      blocks.push(block)
      i++
      continue
    }

    // -- Bullet lines (⏺ marker) — could be tool-call, agent-spawn, or text
    if (trimmed.startsWith(MARKER_BULLET)) {
      const afterMarker = trimmed.slice(MARKER_BULLET.length).trim()
      const toolMatch = matchToolCall(afterMarker)

      if (toolMatch !== null) {
        const args = extractToolArgs(afterMarker, toolMatch.argsStart)
        const isAgent = toolMatch.toolName === 'Agent'

        blocks.push({
          type: isAgent ? 'agent-spawn' : 'tool-call',
          content: line,
          toolName: toolMatch.toolName,
          toolArgs: args,
        })
        i++
        continue
      }

      // Check for agent result notification: ⏺ Agent "description" completed
      if (/^Agent\s+"[^"]*"\s+completed/.test(afterMarker) || /^Agent\s+"[^"]*"\s+/.test(afterMarker)) {
        const nameMatch = afterMarker.match(/^Agent\s+"([^"]*)"/)
        blocks.push({
          type: 'agent-result',
          content: line,
          toolName: 'Agent',
          toolArgs: nameMatch?.[1] ?? afterMarker,
        })
        i++
        continue
      }

      // Not a tool call — this is Claude's conversational text.
      blocks.push({
        type: 'text',
        content: line,
      })
      i++
      continue
    }

    // -- Summary lines (action descriptions, often with ctrl+o hint)
    if (isSummaryLine(trimmed)) {
      blocks.push({
        type: 'summary',
        content: line,
      })
      i++
      continue
    }

    // -- Fallback: unrecognized lines are treated as text
    blocks.push({
      type: 'text',
      content: line,
    })
    i++
  }

  return mergeConsecutiveBlocks(blocks)
}

// ---------------------------------------------------------------------------
// Post-processing: merge consecutive blocks of the same type
// ---------------------------------------------------------------------------

/**
 * Merges consecutive text blocks and consecutive separator blocks into single
 * blocks. This prevents the UI from rendering every line as its own bubble.
 */
function mergeConsecutiveBlocks(blocks: TerminalBlock[]): TerminalBlock[] {
  if (blocks.length <= 1) return blocks

  const merged: TerminalBlock[] = []
  let i = 0
  while (i < blocks.length) {
    const block = blocks[i]!

    // Merge consecutive text blocks
    if (block.type === 'text') {
      const lines: string[] = [block.content]
      i++
      while (i < blocks.length && blocks[i]!.type === 'text') {
        lines.push(blocks[i]!.content)
        i++
      }
      merged.push({ type: 'text', content: lines.join('\n') })
      continue
    }

    // Merge consecutive separator blocks
    if (block.type === 'separator') {
      i++
      while (i < blocks.length && blocks[i]!.type === 'separator') {
        i++
      }
      merged.push({ type: 'separator', content: '' })
      continue
    }

    // Merge consecutive tool-output blocks
    if (block.type === 'tool-output') {
      const lines: string[] = [block.content]
      i++
      while (i < blocks.length && blocks[i]!.type === 'tool-output') {
        lines.push(blocks[i]!.content)
        i++
      }
      merged.push({ type: 'tool-output', content: lines.join('\n') })
      continue
    }

    merged.push(block)
    i++
  }
  return merged
}
