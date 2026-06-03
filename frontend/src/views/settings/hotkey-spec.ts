// ---------------------------------------------------------------------------
// hotkey-spec — pure helpers for the overlay-hotkey rebind capture flow
// (TASK-009). Extracted from OverlayPanel.tsx so the serialization /
// blocklist / glyph-format rules can be unit-tested independently of the
// React tree (the project's test harness doesn't ship jsdom + RTL).
//
// Canonical serialization rules (must match TASK-005's Go-side parser
// exactly):
//   - Modifier tokens before the key, joined with `+`, all lowercase.
//   - Modifier order: cmd, ctrl, alt, shift.
//   - Key token is the literal lowercase letter for A-Z (`"j"`) or a
//     named token for special keys (`"space"`, `"return"`, `"escape"`,
//     `"tab"`, `"left"`, `"right"`, `"up"`, `"down"`, `"f1"`-`"f12"`).
//
// Glyph format rules (for the read-only display of the current spec):
//   - cmd → ⌘, ctrl → ⌃, alt → ⌥, shift → ⇧
//   - Letter keys uppercase; named keys title-cased (Space, Return, …).
// ---------------------------------------------------------------------------

/** Reserved system shortcuts the rebind capture must refuse. macOS bakes
 *  these into the OS-level UX (quit/close/hide) and binding the overlay to
 *  any of them would brick the user. */
export const BLOCKED_SPECS: ReadonlyArray<string> = ['cmd+q', 'cmd+w', 'cmd+h'] as const

/** Map for translating event.key values into canonical key tokens. Anything
 *  not in this map falls through to `event.key.toLowerCase()`, which works
 *  for the A-Z letter row, digit row, and `f1`…`f12` (Wails / WebKit emits
 *  `event.key === 'F1'` etc., which lowercases correctly). */
const KEY_NAME_MAP: Readonly<Record<string, string>> = {
  ' ': 'space',
  spacebar: 'space',
  escape: 'escape',
  esc: 'escape',
  enter: 'return',
  return: 'return',
  tab: 'tab',
  arrowleft: 'left',
  arrowright: 'right',
  arrowup: 'up',
  arrowdown: 'down',
  left: 'left',
  right: 'right',
  up: 'up',
  down: 'down',
}

/** Subset of KeyboardEvent we depend on. Defining a structural type lets the
 *  helper be unit-tested with a plain object literal — no jsdom required. */
export interface KeyboardEventLike {
  key: string
  metaKey: boolean
  ctrlKey: boolean
  altKey: boolean
  shiftKey: boolean
}

/** Returns true when the event is a pure modifier press (the user hasn't
 *  yet picked a real key). The capture flow should ignore these so the
 *  user can press Cmd, then Shift, then J without prematurely committing. */
export function isModifierOnly(event: KeyboardEventLike): boolean {
  const k = event.key.toLowerCase()
  return (
    k === 'meta' ||
    k === 'control' ||
    k === 'alt' ||
    k === 'shift' ||
    k === 'os' ||
    k === 'hyper'
  )
}

/** Serialize a KeyboardEvent into the canonical `"cmd+shift+j"` spec. */
export function canonicalizeSpec(event: KeyboardEventLike): string {
  const tokens: string[] = []
  if (event.metaKey) tokens.push('cmd')
  if (event.ctrlKey) tokens.push('ctrl')
  if (event.altKey) tokens.push('alt')
  if (event.shiftKey) tokens.push('shift')

  const rawKey = (event.key ?? '').toLowerCase()
  const mapped = KEY_NAME_MAP[rawKey]
  const keyToken = mapped ?? rawKey
  if (keyToken) tokens.push(keyToken)
  return tokens.join('+')
}

/** True if `spec` matches a reserved macOS shortcut the user must not bind
 *  the overlay to. Comparison is case-insensitive on the spec input. */
export function isBlockedSpec(spec: string): boolean {
  const normalized = spec.trim().toLowerCase()
  return BLOCKED_SPECS.includes(normalized)
}

const MOD_GLYPHS: Readonly<Record<string, string>> = {
  cmd: '⌘',
  ctrl: '⌃',
  alt: '⌥',
  shift: '⇧',
}

function titleCaseKey(token: string): string {
  if (token.length === 1) return token.toUpperCase()
  // Function keys: f1..f12 → F1..F12
  if (/^f([1-9]|1[0-2])$/.test(token)) return token.toUpperCase()
  return token.charAt(0).toUpperCase() + token.slice(1)
}

/** Render a canonical spec as a human-readable glyph string for display in
 *  the panel. Example: `"cmd+shift+j"` → `"⌘ ⇧ J"`. Unknown tokens render
 *  verbatim so a future protocol bump doesn't black-hole the value. */
export function glyphFormatSpec(spec: string): string {
  if (!spec) return ''
  const parts = spec.split('+').map((p) => p.trim()).filter(Boolean)
  const out: string[] = []
  for (const p of parts) {
    const lower = p.toLowerCase()
    if (MOD_GLYPHS[lower]) {
      out.push(MOD_GLYPHS[lower])
    } else {
      out.push(titleCaseKey(lower))
    }
  }
  return out.join(' ')
}
