// ---------------------------------------------------------------------------
// HUD Theme -- cyan/teal CSS variables and utility classes for sci-fi panels
// ---------------------------------------------------------------------------
// Import this module to inject the HUD CSS custom properties and classes.
// Injection is idempotent: safe to import from multiple files.
// ---------------------------------------------------------------------------

const STYLE_ID = 'hud-theme-styles'

// ---- CSS custom properties ------------------------------------------------

const CSS_VARIABLES = `
:root {
  --hud-cyan: #00ffcc;
  --hud-cyan-bright: #44ffee;
  --hud-cyan-dim: rgba(0, 255, 204, 0.15);
  --hud-cyan-glow: 0 0 8px rgba(0, 255, 204, 0.3), 0 0 20px rgba(0, 255, 204, 0.1);
  --hud-border: 1px solid rgba(0, 255, 204, 0.15);
  --hud-bg: rgba(0, 12, 10, 0.85);
  --hud-bg-solid: #000c0a;
  --hud-text: #00ffcc;
  --hud-text-dim: rgba(0, 255, 204, 0.5);
  --hud-amber: #ffaa00;
  --hud-red: #ff4444;
  --hud-green: #00ff88;
}
`

// ---- Utility class rules --------------------------------------------------

const CSS_CLASSES = `
.hud-panel {
  background: var(--hud-bg);
  border: var(--hud-border);
  border-radius: 4px;
  box-shadow: var(--hud-cyan-glow);
  padding: 12px 16px;
}

.hud-text {
  color: var(--hud-text);
}

.hud-text-dim {
  color: var(--hud-text-dim);
}

.hud-label {
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.15em;
  color: var(--hud-text-dim);
  font-weight: 600;
}

.hud-value {
  font-family: 'SF Mono', 'Menlo', monospace;
  font-variant-numeric: tabular-nums;
  color: var(--hud-text);
}

.hud-glow {
  box-shadow: var(--hud-cyan-glow);
}

.hud-scanlines {
  background: repeating-linear-gradient(
    0deg,
    transparent,
    transparent 2px,
    rgba(0, 255, 204, 0.03) 2px,
    rgba(0, 255, 204, 0.03) 4px
  );
  pointer-events: none;
}

@keyframes hud-flash {
  0%   { box-shadow: 0 0 8px rgba(0,255,204,0.3), 0 0 20px rgba(0,255,204,0.1); }
  50%  { box-shadow: 0 0 15px rgba(0,255,204,0.6), 0 0 40px rgba(0,255,204,0.3); }
  100% { box-shadow: 0 0 8px rgba(0,255,204,0.3), 0 0 20px rgba(0,255,204,0.1); }
}
.hud-flash {
  animation: hud-flash 0.6s ease-out;
}

@keyframes hud-mute-pulse {
  0%, 100% { opacity: 1; }
  50%      { opacity: 0.4; }
}

.hud-radar {
  background: radial-gradient(
    circle,
    transparent 30%, rgba(0,255,204,0.03) 31%, transparent 32%,
    transparent 50%, rgba(0,255,204,0.02) 51%, transparent 52%,
    transparent 70%, rgba(0,255,204,0.015) 71%, transparent 72%
  );
}

.hud-header-gradient {
  background: linear-gradient(90deg, rgba(0,255,204,0.08) 0%, transparent 100%);
  padding: 4px 8px;
  border-radius: 2px;
}
`

// ---- Injection (idempotent) -----------------------------------------------

function injectStyles(): void {
  if (typeof document === 'undefined') return
  if (document.getElementById(STYLE_ID)) return

  const style = document.createElement('style')
  style.id = STYLE_ID
  style.textContent = CSS_VARIABLES + CSS_CLASSES
  document.head.appendChild(style)
}

// Inject immediately on module load
injectStyles()

// ---- Exported class-name constants ----------------------------------------

/** CSS class names for HUD-themed elements. Use with `className={HUD.panel}`. */
export const HUD = {
  /** Dark bg, cyan border, subtle glow */
  panel: 'hud-panel',
  /** Cyan text color */
  text: 'hud-text',
  /** Dimmed cyan text */
  textDim: 'hud-text-dim',
  /** Uppercase, tracking-widest, tiny, dimmed */
  label: 'hud-label',
  /** Monospace, cyan, tabular-nums */
  value: 'hud-value',
  /** Box-shadow glow */
  glow: 'hud-glow',
  /** Scan-line overlay effect */
  scanlines: 'hud-scanlines',
  /** One-shot border/glow brightening on data change */
  flash: 'hud-flash',
  /** Radial gradient concentric rings (behind orb) */
  radar: 'hud-radar',
  /** Subtle left-to-right gradient for panel headers */
  headerGradient: 'hud-header-gradient',
} as const

// ---- Helper functions -----------------------------------------------------

type HudColorName = 'cyan' | 'amber' | 'red' | 'green'

const COLOR_VAR_MAP: Record<HudColorName, string> = {
  cyan: 'var(--hud-cyan)',
  amber: 'var(--hud-amber)',
  red: 'var(--hud-red)',
  green: 'var(--hud-green)',
}

/** Returns the CSS variable reference for use in inline styles. */
export function hudColor(name: HudColorName): string {
  return COLOR_VAR_MAP[name]
}
