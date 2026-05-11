import { describe, it, expect } from 'vitest'
import { parseTerminalOutput, type TerminalBlock } from '../terminal-parser'

describe('parseTerminalOutput', () => {
  // -----------------------------------------------------------------------
  // 1. Empty string
  // -----------------------------------------------------------------------
  it('returns an empty array for empty string input', () => {
    const result = parseTerminalOutput('')
    expect(result).toEqual([])
  })

  // -----------------------------------------------------------------------
  // 2. Single text line
  // -----------------------------------------------------------------------
  it('parses a single plain text line into one text block', () => {
    const result = parseTerminalOutput('Hello, world!')
    expect(result).toHaveLength(1)
    expect(result[0]).toEqual<TerminalBlock>({
      type: 'text',
      content: 'Hello, world!',
    })
  })

  // -----------------------------------------------------------------------
  // 3. Tool call: Bash(git status)
  // -----------------------------------------------------------------------
  it('parses a Bash tool call with simple arguments', () => {
    const input = '\u23FA Bash(git status)'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'tool-call',
      toolName: 'Bash',
      toolArgs: 'git status',
    })
  })

  // -----------------------------------------------------------------------
  // 4. Tool call with nested parentheses
  // -----------------------------------------------------------------------
  it('extracts args correctly when they contain nested parentheses', () => {
    const input = "\u23FA Bash(echo '(hello)')"
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'tool-call',
      toolName: 'Bash',
      toolArgs: "echo '(hello)'",
    })
  })

  // -----------------------------------------------------------------------
  // 5. Tool output: single line
  // -----------------------------------------------------------------------
  it('parses a tool output line into a tool-output block', () => {
    const input = '  \u23BF  some output'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'tool-output',
      content: '  \u23BF  some output',
    })
  })

  // -----------------------------------------------------------------------
  // 6. Consecutive tool output lines grouped into one block
  // -----------------------------------------------------------------------
  it('groups consecutive tool-output lines into a single block', () => {
    const input = [
      '  \u23BF  line one',
      '  \u23BF  line two',
      '  \u23BF  line three',
    ].join('\n')
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]!.type).toBe('tool-output')
    expect(result[0]!.content).toBe(input)
  })

  // -----------------------------------------------------------------------
  // 7. Completion with duration
  // -----------------------------------------------------------------------
  it('parses a completion line and extracts duration', () => {
    const input = '\u273B Worked for 2m 29s'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'completion',
      duration: '2m 29s',
    })
  })

  // -----------------------------------------------------------------------
  // 8. Completion without duration
  // -----------------------------------------------------------------------
  it('parses a completion line without duration', () => {
    const input = '\u273B Reticulating\u2026'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]!.type).toBe('completion')
    expect(result[0]!.duration).toBeUndefined()
  })

  // -----------------------------------------------------------------------
  // 9. Prompt empty
  // -----------------------------------------------------------------------
  it('parses a bare prompt marker as a prompt block', () => {
    const input = '\u276F'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'prompt',
      content: '\u276F',
    })
  })

  // -----------------------------------------------------------------------
  // 10. Prompt with command
  // -----------------------------------------------------------------------
  it('parses a prompt with a command as a prompt block', () => {
    const input = '\u276F /stats'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'prompt',
      content: '\u276F /stats',
    })
  })

  // -----------------------------------------------------------------------
  // 11. Collapsed section with line count
  // -----------------------------------------------------------------------
  it('parses a collapsed section and extracts lineCount', () => {
    const input = '  \u2026 +31 lines (ctrl+o to expand)'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'collapsed',
      lineCount: 31,
    })
  })

  // -----------------------------------------------------------------------
  // 12. Summary: "Read 1 file (ctrl+o to expand)"
  // -----------------------------------------------------------------------
  it('parses a "Read N file" summary line', () => {
    const input = '  Read 1 file (ctrl+o to expand)'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'summary',
      content: '  Read 1 file (ctrl+o to expand)',
    })
  })

  // -----------------------------------------------------------------------
  // 13. Summary: "Queried codemap 5 times (ctrl+o to expand)"
  // -----------------------------------------------------------------------
  it('parses a "Queried codemap N times" summary line', () => {
    const input = '  Queried codemap 5 times (ctrl+o to expand)'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'summary',
      content: '  Queried codemap 5 times (ctrl+o to expand)',
    })
  })

  // -----------------------------------------------------------------------
  // 14. Separator: line of box-drawing characters
  // -----------------------------------------------------------------------
  it('parses a line of box-drawing characters as a separator', () => {
    const input = '\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]!.type).toBe('separator')
  })

  // -----------------------------------------------------------------------
  // 15. Agent spawn
  // -----------------------------------------------------------------------
  it('parses an Agent tool call as an agent-spawn block', () => {
    const input = '\u23FA Agent(Explore codebase)'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'agent-spawn',
      toolName: 'Agent',
      toolArgs: 'Explore codebase',
    })
  })

  // -----------------------------------------------------------------------
  // 16. Mixed real-world output
  // -----------------------------------------------------------------------
  it('correctly parses a multi-line mixed real-world output', () => {
    const input = [
      '\u23FA Bash(git status)',
      '  \u23BF  On branch main',
      '  \u23BF  nothing to commit',
      '\u23FA I have checked the repository status.',
      '\u273B Worked for 45s',
    ].join('\n')

    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(4)

    // Block 0: tool-call
    expect(result[0]).toMatchObject({
      type: 'tool-call',
      toolName: 'Bash',
      toolArgs: 'git status',
    })

    // Block 1: grouped tool-output (2 lines)
    expect(result[1]!.type).toBe('tool-output')
    expect(result[1]!.content).toContain('On branch main')
    expect(result[1]!.content).toContain('nothing to commit')

    // Block 2: text (bullet with non-tool text)
    expect(result[2]!.type).toBe('text')

    // Block 3: completion with duration
    expect(result[3]).toMatchObject({
      type: 'completion',
      duration: '45s',
    })
  })

  // -----------------------------------------------------------------------
  // 17. Malformed bullet line (no tool name match) falls back to text
  // -----------------------------------------------------------------------
  it('treats a malformed bullet line as text when no tool name matches', () => {
    const input = '\u23FA SomeUnknownThing(args here)'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]!.type).toBe('text')
  })

  // -----------------------------------------------------------------------
  // 18. Multiple consecutive separators grouped into one
  // -----------------------------------------------------------------------
  it('groups multiple consecutive separator lines into one block', () => {
    const input = [
      '\u2500\u2500\u2500\u2500\u2500',
      '\u2500\u2500\u2500\u2500\u2500',
      '\u2500\u2500\u2500\u2500\u2500',
    ].join('\n')
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]!.type).toBe('separator')
    expect(result[0]!.content).toBe(input)
  })

  // -----------------------------------------------------------------------
  // Additional edge-case tests
  // -----------------------------------------------------------------------

  it('handles a Read tool call correctly', () => {
    const input = '\u23FA Read(src/main.ts)'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'tool-call',
      toolName: 'Read',
      toolArgs: 'src/main.ts',
    })
  })

  it('groups empty lines with adjacent separator lines', () => {
    const input = [
      '',
      '\u2500\u2500\u2500',
      '',
    ].join('\n')
    const result = parseTerminalOutput(input)
    // Empty line starts a separator block that merges with the separator
    // line and the trailing empty line.
    expect(result).toHaveLength(1)
    expect(result[0]!.type).toBe('separator')
  })

  it('parses a summary line that only has the ctrl+o hint', () => {
    const input = 'Something here (ctrl+o to expand)'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]!.type).toBe('summary')
  })

  it('preserves original whitespace in content field', () => {
    const input = '    \u23BF  indented output'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    // The content field should be the raw line, preserving leading spaces.
    expect(result[0]!.content).toBe('    \u23BF  indented output')
  })

  it('handles completion with hour-based duration', () => {
    const input = '\u273B Worked for 1h 3m'
    const result = parseTerminalOutput(input)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      type: 'completion',
      duration: '1h 3m',
    })
  })
})
