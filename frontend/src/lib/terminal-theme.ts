import type { ReactElement } from 'react'
import { createElement } from 'react'

// ---------------------------------------------------------------------------
// Tool themes — each Claude Code tool type gets a distinct visual treatment
// ---------------------------------------------------------------------------

export interface ToolTheme {
  /** Tailwind border class, e.g. 'border-blue-500/30' */
  borderColor: string
  /** Tailwind bg class, e.g. 'bg-blue-500/10' */
  bgColor: string
  /** Tailwind text class, e.g. 'text-blue-400' */
  textColor: string
  /** Tailwind text class applied to the SVG icon */
  iconColor: string
  /** Human-readable display name */
  label: string
}

/**
 * Theme map keyed by tool name.
 *
 * GitHub dark palette reference:
 *   blue   #58a6ff   green  #3fb950   gray   #8b949e
 *   amber  #d29922   purple #bc8cff   cyan   #39d2c0
 *   teal   #2ea043   slate  #8b949e
 *
 * Backgrounds use the accent at /10 opacity, borders at /30.
 * Text colors use the 400 Tailwind shade for accessibility against #0d1117.
 */
export const TOOL_THEMES: Record<string, ToolTheme> = {
  // Terminal / command execution ------------------------------------------------
  Bash: {
    borderColor: 'border-blue-500/30',
    bgColor: 'bg-blue-500/10',
    textColor: 'text-blue-400',
    iconColor: 'text-blue-400',
    label: 'Terminal',
  },

  // File modification -----------------------------------------------------------
  Edit: {
    borderColor: 'border-green-500/30',
    bgColor: 'bg-green-500/10',
    textColor: 'text-green-400',
    iconColor: 'text-green-400',
    label: 'Edit',
  },

  // File creation (same family as Edit) -----------------------------------------
  Write: {
    borderColor: 'border-green-500/30',
    bgColor: 'bg-green-500/10',
    textColor: 'text-green-400',
    iconColor: 'text-green-400',
    label: 'Write',
  },

  // File reading ----------------------------------------------------------------
  Read: {
    borderColor: 'border-gray-500/30',
    bgColor: 'bg-gray-500/10',
    textColor: 'text-secondary',
    iconColor: 'text-secondary',
    label: 'Read',
  },

  // File search -----------------------------------------------------------------
  Glob: {
    borderColor: 'border-amber-500/30',
    bgColor: 'bg-amber-500/10',
    textColor: 'text-amber-400',
    iconColor: 'text-amber-400',
    label: 'Glob',
  },

  // Content search (same family as Glob) ----------------------------------------
  Grep: {
    borderColor: 'border-amber-500/30',
    bgColor: 'bg-amber-500/10',
    textColor: 'text-amber-400',
    iconColor: 'text-amber-400',
    label: 'Grep',
  },

  // Subagent spawn --------------------------------------------------------------
  Agent: {
    borderColor: 'border-teal-500/30',
    bgColor: 'bg-teal-500/10',
    textColor: 'text-teal-400',
    iconColor: 'text-teal-400',
    label: 'Agent',
  },

  // Skill invocation ------------------------------------------------------------
  Skill: {
    borderColor: 'border-cyan-500/30',
    bgColor: 'bg-cyan-500/10',
    textColor: 'text-cyan-400',
    iconColor: 'text-cyan-400',
    label: 'Skill',
  },

  // Web operations --------------------------------------------------------------
  WebSearch: {
    borderColor: 'border-teal-500/30',
    bgColor: 'bg-teal-500/10',
    textColor: 'text-teal-400',
    iconColor: 'text-teal-400',
    label: 'Search',
  },
  WebFetch: {
    borderColor: 'border-teal-500/30',
    bgColor: 'bg-teal-500/10',
    textColor: 'text-teal-400',
    iconColor: 'text-teal-400',
    label: 'Fetch',
  },

  // User interaction ------------------------------------------------------------
  AskUserQuestion: {
    borderColor: 'border-teal-500/30',
    bgColor: 'bg-teal-500/10',
    textColor: 'text-teal-400',
    iconColor: 'text-teal-400',
    label: 'Question',
  },

  // Fallback for unknown tools --------------------------------------------------
  default: {
    borderColor: 'border-border/30',
    bgColor: 'bg-border/10',
    textColor: 'text-secondary',
    iconColor: 'text-secondary',
    label: 'Tool',
  },
} as const

/**
 * Returns the theme for a given tool name. Falls back to `default` for
 * unrecognised tools.
 */
export function getToolTheme(toolName: string): ToolTheme {
  return TOOL_THEMES[toolName] ?? TOOL_THEMES['default']!
}

// ---------------------------------------------------------------------------
// Block-level themes (non-tool blocks)
// ---------------------------------------------------------------------------

export const BLOCK_THEMES: Record<
  string,
  { borderColor: string; bgColor: string; textColor: string }
> = {
  /** Successful completion / result blocks */
  completion: {
    borderColor: 'border-green-500/30',
    bgColor: 'bg-green-500/10',
    textColor: 'text-green-400',
  },

  /** User prompt / input area */
  prompt: {
    borderColor: 'border-border',
    bgColor: 'bg-app',
    textColor: 'text-primary',
  },

  /** Collapsed / minimised sections */
  collapsed: {
    borderColor: 'border-border-m',
    bgColor: 'bg-transparent',
    textColor: 'text-muted',
  },

  /** Summary text blocks */
  summary: {
    borderColor: 'border-transparent',
    bgColor: 'bg-transparent',
    textColor: 'text-muted',
  },

  /** Visual separator between conversation turns */
  separator: {
    borderColor: 'border-transparent',
    bgColor: 'bg-transparent',
    textColor: 'text-transparent',
  },
} as const

// ---------------------------------------------------------------------------
// Inline SVG icon components — 16x16 viewBox
// ---------------------------------------------------------------------------

interface ToolIconProps {
  toolName: string
  className?: string
}

// Shared SVG wrapper props
const SVG_BASE = {
  xmlns: 'http://www.w3.org/2000/svg',
  width: 16,
  height: 16,
  viewBox: '0 0 16 16',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.5,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
}

/** Helper to build an SVG element with consistent base attributes. */
function svg(
  children: ReactElement | ReactElement[],
  className: string | undefined,
): ReactElement {
  return createElement('svg', { ...SVG_BASE, className }, children)
}

/** Terminal prompt icon (>_) for Bash */
function TerminalIcon({ className }: { className?: string }): ReactElement {
  return svg(
    [
      createElement('polyline', { key: 'a', points: '2,5 6,8 2,11' }),
      createElement('line', { key: 'b', x1: 8, y1: 12, x2: 14, y2: 12 }),
    ],
    className,
  )
}

/** Pencil icon for Edit / Write */
function PencilIcon({ className }: { className?: string }): ReactElement {
  return svg(
    [
      createElement('path', {
        key: 'a',
        d: 'M11.5 2.5 l2 2 L5 13 l-3 1 l1-3 Z',
      }),
      createElement('line', { key: 'b', x1: 9.5, y1: 4.5, x2: 11.5, y2: 6.5 }),
    ],
    className,
  )
}

/** Eye icon for Read */
function EyeIcon({ className }: { className?: string }): ReactElement {
  return svg(
    [
      createElement('path', {
        key: 'a',
        d: 'M1 8 C3 4 6 2.5 8 2.5 C10 2.5 13 4 15 8 C13 12 10 13.5 8 13.5 C6 13.5 3 12 1 8 Z',
      }),
      createElement('circle', { key: 'b', cx: 8, cy: 8, r: 2 }),
    ],
    className,
  )
}

/** Magnifying glass icon for Glob / Grep */
function SearchIcon({ className }: { className?: string }): ReactElement {
  return svg(
    [
      createElement('circle', { key: 'a', cx: 7, cy: 7, r: 4 }),
      createElement('line', { key: 'b', x1: 10, y1: 10, x2: 14, y2: 14 }),
    ],
    className,
  )
}

/** Robot / nodes icon for Agent */
function AgentIcon({ className }: { className?: string }): ReactElement {
  return svg(
    [
      createElement('circle', { key: 'a', cx: 8, cy: 5, r: 2.5 }),
      createElement('circle', { key: 'b', cx: 3, cy: 12, r: 1.5 }),
      createElement('circle', { key: 'c', cx: 13, cy: 12, r: 1.5 }),
      createElement('line', { key: 'd', x1: 8, y1: 7.5, x2: 3, y2: 10.5 }),
      createElement('line', { key: 'e', x1: 8, y1: 7.5, x2: 13, y2: 10.5 }),
    ],
    className,
  )
}

/** Lightning bolt icon for Skill */
function BoltIcon({ className }: { className?: string }): ReactElement {
  return svg(
    createElement('polyline', {
      points: '9,1 4,9 8,9 7,15 12,7 8,7 9,1',
      fill: 'currentColor',
      stroke: 'currentColor',
      strokeWidth: 0.5,
    }),
    className,
  )
}

/** Globe icon for WebSearch / WebFetch */
function GlobeIcon({ className }: { className?: string }): ReactElement {
  return svg(
    [
      createElement('circle', { key: 'a', cx: 8, cy: 8, r: 6 }),
      createElement('ellipse', { key: 'b', cx: 8, cy: 8, rx: 3, ry: 6 }),
      createElement('line', { key: 'c', x1: 2, y1: 6, x2: 14, y2: 6 }),
      createElement('line', { key: 'd', x1: 2, y1: 10, x2: 14, y2: 10 }),
    ],
    className,
  )
}

/** Question mark icon for AskUserQuestion */
function QuestionIcon({ className }: { className?: string }): ReactElement {
  return svg(
    [
      createElement('path', {
        key: 'a',
        d: 'M5.5 5 C5.5 3 6.5 2 8 2 C9.5 2 10.5 3 10.5 4.5 C10.5 6 9 6.5 8 7.5 L8 9.5',
      }),
      createElement('circle', {
        key: 'b',
        cx: 8,
        cy: 12.5,
        r: 0.75,
        fill: 'currentColor',
        stroke: 'none',
      }),
    ],
    className,
  )
}

/** Code brackets icon for unknown / default tools */
function CodeIcon({ className }: { className?: string }): ReactElement {
  return svg(
    [
      createElement('polyline', { key: 'a', points: '5,3 1,8 5,13' }),
      createElement('polyline', { key: 'b', points: '11,3 15,8 11,13' }),
      createElement('line', { key: 'c', x1: 9, y1: 2, x2: 7, y2: 14 }),
    ],
    className,
  )
}

// ---------------------------------------------------------------------------
// Icon dispatch
// ---------------------------------------------------------------------------

/** Maps tool name to its icon component. */
const ICON_MAP: Record<string, (props: { className?: string }) => ReactElement> = {
  Bash: TerminalIcon,
  Edit: PencilIcon,
  Write: PencilIcon,
  Read: EyeIcon,
  Glob: SearchIcon,
  Grep: SearchIcon,
  Agent: AgentIcon,
  Skill: BoltIcon,
  WebSearch: GlobeIcon,
  WebFetch: GlobeIcon,
  AskUserQuestion: QuestionIcon,
}

/**
 * Returns the appropriate inline SVG icon for a tool.
 *
 * Falls back to a code brackets icon (`</>`) for unrecognised tools.
 *
 * @example
 * ```tsx
 * <ToolIcon toolName="Bash" className="w-4 h-4 text-blue-400" />
 * ```
 */
export function ToolIcon({ toolName, className }: ToolIconProps): ReactElement {
  const Icon = ICON_MAP[toolName] ?? CodeIcon
  return Icon({ className })
}
