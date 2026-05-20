// ---------------------------------------------------------------------------
// HUD Tokens -- React Native port of frontend/src/lib/hud-theme.ts
// ---------------------------------------------------------------------------
// React Native's StyleSheet does not consume CSS custom properties or Tailwind
// classes, so we re-express the Mac HUD's design tokens as plain TypeScript
// objects. Values mirror `frontend/src/lib/hud-theme.ts` 1:1 where applicable.
//
// Source-of-truth diff (kept inline for review during TASK-006):
//   Mac --hud-cyan         #00ffcc -> colors.cyan
//   Mac --hud-cyan-bright  #44ffee -> colors.cyanBright
//   Mac --hud-cyan-dim     rgba(0,255,204,0.15) -> colors.cyanDark
//   Mac --hud-bg-solid     #000c0a -> colors.bg
//   Mac --hud-bg           rgba(0,12,10,0.85) -> colors.bgPanel
//   Mac --hud-text         #00ffcc -> colors.textPrimary (cyan; also colors.cyan)
//   Mac --hud-text-dim     rgba(0,255,204,0.5) -> colors.textDim
//   Mac --hud-amber        #ffaa00 -> colors.amber
//   Mac --hud-red          #ff4444 -> colors.red
//   Mac --hud-green        #00ff88 -> colors.green
//
// CSS utilities that have no RN equivalent (hud-scanlines, hud-flash keyframes,
// hud-radar radial-gradient, hud-header-gradient) are intentionally skipped.
// They will be re-created in TASK-019 using react-native-skia / Reanimated
// when the orb component itself lands.
// ---------------------------------------------------------------------------

import { Platform } from 'react-native'

// ---- Colors --------------------------------------------------------------
// Values come straight from CSS_VARIABLES in hud-theme.ts; no new colors are
// introduced here. `cyanDim` is the alpha-0.4 mid-tone used by orb labels in
// JarvisHudView; `cyanDark` is the alpha-0.15 fill used by --hud-cyan-dim
// (renamed for clarity since "dim" already maps to text alpha).

export const colors = {
  /** Solid HUD background -- --hud-bg-solid */
  bg: '#000c0a',
  /** Translucent panel background -- --hud-bg */
  bgPanel: 'rgba(0, 12, 10, 0.85)',

  /** Primary cyan accent -- --hud-cyan / --hud-text */
  cyan: '#00ffcc',
  /** Hover / active brighter cyan -- --hud-cyan-bright */
  cyanBright: '#44ffee',
  /** Mid-tone cyan used on dimmed label outlines (matches text-dim alpha-0.5) */
  cyanDim: 'rgba(0, 255, 204, 0.4)',
  /** Low-alpha cyan fill -- --hud-cyan-dim (rgba(0,255,204,0.15)) */
  cyanDark: 'rgba(0, 255, 204, 0.15)',

  /** Cyan border color used by .hud-panel -- rgba(0,255,204,0.15) */
  border: 'rgba(0, 255, 204, 0.15)',

  /** Primary HUD text color -- --hud-text (same hex as --hud-cyan) */
  textPrimary: '#00ffcc',
  /** Dimmed HUD text -- --hud-text-dim */
  textDim: 'rgba(0, 255, 204, 0.5)',

  /** Warning amber -- --hud-amber */
  amber: '#ffaa00',
  /** Error red -- --hud-red */
  red: '#ff4444',
  /** Success / online green -- --hud-green */
  green: '#00ff88',
} as const

// ---- Fonts ----------------------------------------------------------------
// SF Mono is bundled in mobile/assets/fonts/SFMono-Regular.otf when the user
// supplies it (see mobile/assets/fonts/README.md). Until then we fall back to
// the platform mono: 'Menlo' (preinstalled on every iOS device) or
// 'monospace' (Android system mono alias). The fallback chain prevents the
// app from crashing on a missing-font require() at app startup.

const SF_MONO_AVAILABLE = false // flip to true once SF Mono OTFs are bundled

const platformMono = Platform.OS === 'ios' ? 'Menlo' : 'monospace'
const platformMonoBold = Platform.OS === 'ios' ? 'Menlo-Bold' : 'monospace'

export const fontFamilies = {
  /** Monospace text -- matches Mac's `font-family: 'SF Mono', 'Menlo', monospace`. */
  mono: SF_MONO_AVAILABLE ? 'SF Mono' : platformMono,
  /** Bold monospace -- Mac uses `font-weight: 600` on `.hud-label`. */
  monoBold: SF_MONO_AVAILABLE ? 'SF Mono Bold' : platformMonoBold,
} as const

// ---- Spacing --------------------------------------------------------------
// Mac side uses Tailwind spacing (Tailwind's default scale, base 4px).
// Mapped to a fixed scale so RN StyleSheet has a deterministic source.

export const spacing = {
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 24,
  xxl: 32,
} as const

// ---- Border radius --------------------------------------------------------
// Mac side: `.hud-panel { border-radius: 4px }`, `.hud-header-gradient { 2px }`.
// We keep three tiers for RN reuse.

export const borderRadius = {
  sm: 2,
  md: 4,
  lg: 12,
} as const

// ---- Typography sizes ------------------------------------------------------
// Mac side: `.hud-label { font-size: 10px; letter-spacing: 0.15em }`.
// RN does not support `em` units, so labels use a fixed 1.5px letter spacing
// (10px * 0.15 = 1.5px) and a 10px size that matches the Mac HUD.

export const typography = {
  label: {
    fontSize: 10,
    letterSpacing: 1.5,
    fontFamily: fontFamilies.monoBold,
    color: colors.textDim,
  },
  value: {
    fontSize: 12,
    fontFamily: fontFamilies.mono,
    color: colors.textPrimary,
  },
} as const

// ---- Type exports ---------------------------------------------------------

export type HudColors = typeof colors
export type HudFontFamilies = typeof fontFamilies
export type HudSpacing = typeof spacing
export type HudBorderRadius = typeof borderRadius
export type HudTypography = typeof typography
