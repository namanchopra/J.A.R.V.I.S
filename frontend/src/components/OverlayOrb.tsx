// ---------------------------------------------------------------------------
// OverlayOrb — minimal sci-fi orb tuned for the 180x180 Mac overlay window.
//
// Built for TASK-007 of plans/jarvis-mac-overlay.md. The full HUD orb
// (JarvisOrb.tsx) ships a Three.js scene that is too heavy for a sub-200ms
// cold render in an overlay surface, so this component is intentionally
// framer-motion only:
//   * a single SVG ring of ~40 particles
//   * one CSS-transform rotation on the parent <g> (no per-particle keyframes)
//   * one synthetic pulse on the inner glow when state === 'speaking'
//   * an audio-level scale composed via inline style on top of the pulse
//
// Bundle constraint: framer-motion is already in package.json, no new deps.
// Palette: pulls var(--accent-blue) from frontend/src/style.css.
// ---------------------------------------------------------------------------

import { useMemo } from 'react'
import type { CSSProperties } from 'react'
import { motion } from 'framer-motion'

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export type OverlayOrbState = 'idle' | 'listening' | 'speaking'

export interface OverlayOrbProps {
  state: OverlayOrbState
  /** 0..1 audio level. Defaults to 0. */
  audioLevel?: number
}

// ---------------------------------------------------------------------------
// Constants — keep the particle count capped at 40 per the failure-mode
// mitigation in the plan (low-power Macs).
// ---------------------------------------------------------------------------

const PARTICLE_COUNT = 40
const RING_RADIUS = 38 // inside the 0..100 viewBox, leaves room for glow halo
const VIEWBOX_CENTER = 50
const ACCENT = 'var(--accent-blue, #00e5ff)' // CSS-var first; literal fallback

/** Rotation period in seconds, per state. */
const ROTATION_SECONDS: Record<OverlayOrbState, number> = {
  idle: 8,
  listening: 2,
  speaking: 8,
}

/** Inner-glow base opacity, per state. */
const GLOW_OPACITY: Record<OverlayOrbState, number> = {
  idle: 0.18,
  listening: 0.55,
  speaking: 0.45,
}

/** Inner-glow base radius (in viewBox units), per state. */
const GLOW_RADIUS: Record<OverlayOrbState, number> = {
  idle: 18,
  listening: 26,
  speaking: 22,
}

/** Particle base opacity, per state. */
const PARTICLE_OPACITY: Record<OverlayOrbState, number> = {
  idle: 0.45,
  listening: 0.95,
  speaking: 0.7,
}

/** Human-readable label per state — also used as aria-label by tests. */
const ARIA_LABEL: Record<OverlayOrbState, string> = {
  idle: 'Idle',
  listening: 'Listening',
  speaking: 'Speaking',
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

interface ParticlePosition {
  cx: number
  cy: number
  // particle radii alternate slightly so the ring reads as "techy" not "dotty"
  r: number
}

function buildParticlePositions(): ParticlePosition[] {
  const positions: ParticlePosition[] = []
  for (let i = 0; i < PARTICLE_COUNT; i++) {
    const theta = (i / PARTICLE_COUNT) * Math.PI * 2
    positions.push({
      cx: VIEWBOX_CENTER + Math.cos(theta) * RING_RADIUS,
      cy: VIEWBOX_CENTER + Math.sin(theta) * RING_RADIUS,
      // alternate between two radii for a slight beat pattern
      r: i % 2 === 0 ? 1.4 : 0.9,
    })
  }
  return positions
}

function clamp01(value: number): number {
  if (Number.isNaN(value)) return 0
  if (value < 0) return 0
  if (value > 1) return 1
  return value
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function OverlayOrb({
  state,
  audioLevel = 0,
}: OverlayOrbProps): React.ReactElement {
  // Compute particle positions once — they don't depend on props.
  const particles = useMemo(buildParticlePositions, [])

  const rotationSeconds = ROTATION_SECONDS[state]
  const glowOpacity = GLOW_OPACITY[state]
  const glowRadius = GLOW_RADIUS[state]
  const particleOpacity = PARTICLE_OPACITY[state]

  // Audio-level scale: composes multiplicatively with the framer pulse below.
  // We apply it via inline style on the inner-glow <circle> so it does NOT
  // collide with framer-motion's animate prop.
  const normalizedAudio = clamp01(audioLevel)
  const audioScale = 1 + normalizedAudio * 0.5 // 0..1 => 1..1.5

  // The inline style attribute is critical for the test that asserts
  // audioLevel changes the DOM output — the value lands directly in the
  // rendered HTML so the test can inspect it without a real layout engine.
  const glowStyle: CSSProperties = {
    transform: `scale(${audioScale})`,
    transformOrigin: 'center',
    transformBox: 'fill-box',
  }

  // Pulse animation only fires in "speaking" mode (synthetic 4-Hz beat).
  // Outside of speaking we still want a stable animate target so framer
  // doesn't snap between unrelated values when state changes.
  const glowAnimate =
    state === 'speaking'
      ? { scale: [1, 1.08, 1], opacity: [glowOpacity, glowOpacity * 1.25, glowOpacity] }
      : { scale: 1, opacity: glowOpacity }

  const glowTransition =
    state === 'speaking'
      ? { duration: 0.25, repeat: Infinity, ease: 'easeInOut' as const }
      : { duration: 0.6, ease: 'easeInOut' as const }

  return (
    <div
      role="img"
      aria-label={ARIA_LABEL[state]}
      data-state={state}
      style={{
        width: '100%',
        height: '100%',
        display: 'block',
        position: 'relative',
        pointerEvents: 'none',
      }}
    >
      <svg
        viewBox="0 0 100 100"
        width="100%"
        height="100%"
        xmlns="http://www.w3.org/2000/svg"
        style={{ display: 'block', overflow: 'visible' }}
      >
        {/* Inner halo — large soft circle behind everything */}
        <motion.circle
          cx={VIEWBOX_CENTER}
          cy={VIEWBOX_CENTER}
          r={glowRadius}
          fill={ACCENT}
          data-testid="overlay-orb-glow"
          style={glowStyle}
          animate={glowAnimate}
          transition={glowTransition}
          // SVG attribute fallback for renderers that don't pick up framer's
          // opacity inline style (also lets tests sanity-check static state).
          opacity={glowOpacity}
        />

        {/* Particle ring — rotation is one transform on the parent <g> */}
        <motion.g
          data-testid="overlay-orb-ring"
          style={{ transformOrigin: `${VIEWBOX_CENTER}px ${VIEWBOX_CENTER}px` }}
          animate={{ rotate: 360 }}
          transition={{
            duration: rotationSeconds,
            repeat: Infinity,
            ease: 'linear',
          }}
        >
          {particles.map((p, i) => (
            <circle
              key={i}
              cx={p.cx}
              cy={p.cy}
              r={p.r}
              fill={ACCENT}
              opacity={particleOpacity}
            />
          ))}
        </motion.g>

        {/* Faint ring outline — gives the orb a closed silhouette even when
            particles thin out at certain rotations. */}
        <circle
          cx={VIEWBOX_CENTER}
          cy={VIEWBOX_CENTER}
          r={RING_RADIUS}
          fill="none"
          stroke={ACCENT}
          strokeOpacity={state === 'idle' ? 0.1 : 0.2}
          strokeWidth={0.4}
        />
      </svg>
    </div>
  )
}
