# Terminal Output Parsing Format

## Overview

Jarvis renders Claude Code's terminal output by parsing the plain text returned from CMux `read_text` into structured `TerminalBlock` objects. CMux strips all ANSI escape codes before delivery, so the parser relies entirely on Unicode marker characters to identify block boundaries and types.

**Data flow:** CMux `read_text` (plain text with Unicode markers) -> `parseTerminalOutput()` -> `TerminalBlock[]` -> `BlockRenderer` component tree.

Source files:
- **Parser:** `frontend/src/lib/terminal-parser.ts`
- **Renderers:** `frontend/src/components/terminal/BlockRenderers.tsx`
- **Themes:** `frontend/src/lib/terminal-theme.ts`

---

## Unicode Markers

| Character | Code Point | Name | Role |
|-----------|-----------|------|------|
| `\u23FA` | U+23FA | Black Circle for Record | Tool invocations and conversational text |
| `\u23BF` | U+23BF | Dentistry Symbol Light Down | Tool output / response lines |
| `\u273B` | U+273B | Teardrop-Spoked Asterisk | Completion / status |
| `\u276F` | U+276F | Heavy Right-Pointing Angle Quotation Mark | User prompt |
| `\u2026` | U+2026 | Horizontal Ellipsis | Collapsed section |
| `\u2500` | U+2500 | Box Drawings Light Horizontal | Separator (plus 19 other box-drawing chars) |

The full separator character set includes: `\u2500` `\u2501` `\u2550` `\u2502` `\u2503` `\u2551` `\u250C` `\u250D` `\u250E` `\u250F` `\u2510` `\u2514` `\u2518` `\u253C` `\u2524` `\u251C` `\u252C` `\u2534` `\u2015` `\u2014`. A line composed entirely of these characters (and whitespace) is classified as a separator.

---

## Block Types

The parser produces 9 block types, defined by the `BlockType` union:

```ts
type BlockType =
  | 'tool-call' | 'tool-output' | 'text' | 'completion'
  | 'prompt' | 'collapsed' | 'summary' | 'separator' | 'agent-spawn'
```

### 1. `tool-call`

**Detection:** Line starts with `\u23FA`, and the text after the marker matches `<KnownToolName>(`.

**Fields:** `content`, `toolName`, `toolArgs`

**Example:**
```
Raw:    ⏺ Bash(cd /tmp && ls -la)
Parsed: { type: 'tool-call', content: '⏺ Bash(cd /tmp && ls -la)', toolName: 'Bash', toolArgs: 'cd /tmp && ls -la' }
```

Tool args are extracted from the opening `(` to the *last* `)` in the line, handling nested parentheses. If no closing paren exists (truncated output), everything after the opening paren is returned.

### 2. `agent-spawn`

**Detection:** Same as `tool-call`, but `toolName` is `'Agent'`.

**Fields:** `content`, `toolName` (always `'Agent'`), `toolArgs`

**Example:**
```
Raw:    ⏺ Agent(investigate the auth module)
Parsed: { type: 'agent-spawn', content: '⏺ Agent(investigate the auth module)', toolName: 'Agent', toolArgs: 'investigate the auth module' }
```

### 3. `tool-output`

**Detection:** Line starts with `\u23BF`. Consecutive `\u23BF` lines are merged into a single block.

**Fields:** `content` (multi-line, joined with `\n`)

**Example:**
```
Raw:    ⎿ total 48
        ⎿ drwxr-xr-x  6 user staff 192 Apr 10 09:00 .
Parsed: { type: 'tool-output', content: '⎿ total 48\n⎿ drwxr-xr-x  6 user staff 192 Apr 10 09:00 .' }
```

### 4. `text`

**Detection:** Line starts with `\u23FA` but the text after the marker does NOT match any known tool name followed by `(`. Also the fallback for any unrecognized line.

**Fields:** `content`

**Example:**
```
Raw:    ⏺ I'll read the configuration file to understand the setup.
Parsed: { type: 'text', content: '⏺ I\'ll read the configuration file to understand the setup.' }
```

### 5. `completion`

**Detection:** Line starts with `\u273B`.

**Fields:** `content`, `duration` (optional, extracted from "Worked for ..." pattern)

**Example:**
```
Raw:    ✻ Worked for 2m 29s
Parsed: { type: 'completion', content: '✻ Worked for 2m 29s', duration: '2m 29s' }
```

Duration parsing accepts combinations of `Nh`, `Nm`, `Ns` (e.g., `1h 3m`, `45s`, `2m 29s`).

### 6. `prompt`

**Detection:** Line starts with `\u276F`.

**Fields:** `content`

**Example:**
```
Raw:    ❯ fix the login bug
Parsed: { type: 'prompt', content: '❯ fix the login bug' }
```

### 7. `collapsed`

**Detection:** Line starts with `\u2026`.

**Fields:** `content`, `lineCount` (optional, extracted from "+N lines" pattern)

**Example:**
```
Raw:    … +42 lines (ctrl+o to expand)
Parsed: { type: 'collapsed', content: '… +42 lines (ctrl+o to expand)', lineCount: 42 }
```

### 8. `summary`

**Detection:** Line matches one of the summary patterns (checked after all marker-based detections). Does NOT require a Unicode marker prefix.

**Fields:** `content`

**Patterns matched:**
- `Read N file(s)`
- `Wrote N file(s)`
- `Edited N file(s)`
- `Searched for N pattern(s)`
- `Queried codemap N time(s)`
- `Ran N command(s)`
- `Created N file(s)`
- `Deleted N file(s)`
- `Updated N file(s)`
- Any line ending with `(ctrl+o to expand)`

**Example:**
```
Raw:    Read 3 files
Parsed: { type: 'summary', content: 'Read 3 files' }
```

### 9. `separator`

**Detection:** Empty lines, or lines composed entirely of box-drawing / separator characters and whitespace. Consecutive separator and empty lines are merged into one block.

**Fields:** `content` (multi-line, joined with `\n`)

**Example:**
```
Raw:    ───────────────────
        (empty line)
Parsed: { type: 'separator', content: '───────────────────\n' }
```

---

## Known Tool Names

The parser recognizes these 14 tool names (must appear after `\u23FA` followed immediately by `(`):

| Tool | Category |
|------|----------|
| `Bash` | Command execution |
| `Read` | File reading |
| `Edit` | File modification |
| `Write` | File creation |
| `MultiEdit` | Multi-file edit |
| `Glob` | File search |
| `Grep` | Content search |
| `Agent` | Subagent spawn (produces `agent-spawn` block type) |
| `Skill` | Skill invocation |
| `AskUserQuestion` | User interaction |
| `WebSearch` | Web search |
| `WebFetch` | Web fetch |
| `TodoRead` | Todo reading |
| `TodoWrite` | Todo writing |

---

## Adding a New Block Type

### Step 1: Add to `BlockType` union

In `terminal-parser.ts`, extend the union:

```ts
export type BlockType =
  | 'tool-call'
  | 'tool-output'
  // ... existing types ...
  | 'your-new-type'
```

### Step 2: Add detection logic

In the `parseTerminalOutput` function in `terminal-parser.ts`, add a detection branch in the `while` loop. Order matters -- marker-based checks run top-to-bottom, and unrecognized lines fall through to `text`.

```ts
// Place before the fallback text case
if (/* your detection condition */) {
  blocks.push({
    type: 'your-new-type',
    content: line,
    // ...additional fields
  })
  i++
  continue
}
```

If the new type uses a new marker character, define it as a constant alongside the existing `MARKER_*` constants.

### Step 3: Add renderer

In `BlockRenderers.tsx`, create a leaf component and add a `case` to the `BlockRenderer` switch:

```tsx
export function YourNewBlock({ block }: { block: TerminalBlock }): React.ReactElement {
  return <div>{block.content}</div>
}

// In BlockRenderer switch:
case 'your-new-type':
  return <YourNewBlock block={block} />
```

### Step 4: Add theme

In `terminal-theme.ts`, add an entry to `BLOCK_THEMES` (for non-tool blocks) or `TOOL_THEMES` (for tool blocks):

```ts
export const BLOCK_THEMES: Record<string, { borderColor: string; bgColor: string; textColor: string }> = {
  // ...existing entries...
  'your-new-type': {
    borderColor: 'border-...',
    bgColor: 'bg-...',
    textColor: 'text-...',
  },
}
```

### Step 5: Ensure exhaustiveness

The `BlockRenderer` switch has a `default` branch with a `never` type assertion. TypeScript will error if any `BlockType` variant is unhandled, so the compiler enforces completeness.

---

## Sample Transformation

### Raw input (from CMux `read_text`)

```
❯ fix the broken test in utils.ts
⏺ I'll start by reading the test file to understand what's failing.
⏺ Read(src/lib/__tests__/utils.test.ts)
⎿ import { describe, it, expect } from 'vitest'
⎿ import { formatDate } from '../utils'
⎿
⎿ describe('formatDate', () => {
… +18 lines (ctrl+o to expand)
⏺ Edit(src/lib/utils.ts)
⎿ Updated src/lib/utils.ts
⏺ Bash(cd /Users/me/project && npm test)
⎿ PASS src/lib/__tests__/utils.test.ts
Ran 1 commands
───────────────────
✻ Worked for 45s
❯
```

### Parsed output (`TerminalBlock[]`)

```ts
[
  { type: 'prompt',      content: '❯ fix the broken test in utils.ts' },
  { type: 'text',        content: '⏺ I\'ll start by reading the test file to understand what\'s failing.' },
  { type: 'tool-call',   content: '⏺ Read(src/lib/__tests__/utils.test.ts)', toolName: 'Read', toolArgs: 'src/lib/__tests__/utils.test.ts' },
  { type: 'tool-output', content: '⎿ import { describe, it, expect } from \'vitest\'\n⎿ import { formatDate } from \'../utils\'\n⎿\n⎿ describe(\'formatDate\', () => {' },
  { type: 'collapsed',   content: '… +18 lines (ctrl+o to expand)', lineCount: 18 },
  { type: 'tool-call',   content: '⏺ Edit(src/lib/utils.ts)', toolName: 'Edit', toolArgs: 'src/lib/utils.ts' },
  { type: 'tool-output', content: '⎿ Updated src/lib/utils.ts' },
  { type: 'tool-call',   content: '⏺ Bash(cd /Users/me/project && npm test)', toolName: 'Bash', toolArgs: 'cd /Users/me/project && npm test' },
  { type: 'tool-output', content: '⎿ PASS src/lib/__tests__/utils.test.ts' },
  { type: 'summary',     content: 'Ran 1 commands' },
  { type: 'separator',   content: '───────────────────' },
  { type: 'completion',  content: '✻ Worked for 45s', duration: '45s' },
  { type: 'prompt',      content: '❯' },
]
```
