// ---------------------------------------------------------------------------
// OrbView -- React Native port of the Mac HUD's JarvisHudView centerpiece.
// ---------------------------------------------------------------------------
// Source-of-truth aesthetic: frontend/src/components/JarvisHudView.tsx
//   - HudRings (~line 332-480): 6 concentric SVG/div rings with CSS keyframe
//     rotations, dashed/dotted strokes, tick marks, arc segments, crosshairs.
//   - JarvisOrb call site (~line 944): central sphere with radial gradient.
//
// Phone adaptation: we render 4 concentric rings + sphere (the Mac's 6 rings
// + crosshairs are too dense at phone-width). Visual order from outer -> in:
//
//   1. Particle ring   -- 60 small Circle dots, counter-clockwise rotation.
//   2. Dashed ring     -- Circle with strokeDasharray, clockwise (faster).
//   3. Arc-segments    -- 4 quarter-arcs with cardinal gaps, counter-cw.
//   4. Tick-mark ring  -- 12 radial line segments (clock-tick), slow.
//   5. Central sphere  -- 3 nested Circles to fake an inner radial glow.
//
// Rendering approach: react-native-svg for all geometry (works on Expo Go,
// runs on GPU on iOS/Android), react-native-reanimated v4 for rotation +
// scale + opacity. Each ring's rotation lives in its own useSharedValue so
// they animate independently on the UI thread (no per-frame React renders).
//
// We avoided @shopify/react-native-skia because Friday targets Expo Go
// (TASK-026), and Skia requires a native dev build.
// ---------------------------------------------------------------------------

import { useEffect, useMemo } from 'react'
import { StyleSheet, Text, View, useWindowDimensions } from 'react-native'
import Animated, {
  Easing,
  cancelAnimation,
  useAnimatedStyle,
  useSharedValue,
  withRepeat,
  withTiming,
} from 'react-native-reanimated'
import Svg, { Circle, Line, Path } from 'react-native-svg'

import { colors, fontFamilies, spacing } from '../lib/hud-tokens'

// ---- Public types ---------------------------------------------------------

/** Three visual states the orb supports. Driven by Pipecat daemon events. */
export type OrbState = 'idle' | 'listening' | 'speaking'

export interface OrbViewProps {
  /** Visual mode. Drives rotation speed, sphere opacity, and pulse behaviour. */
  state: OrbState
  /** 0..1 audio level. Only consulted when `state === 'speaking'`. */
  audioLevel?: number
  /** Top-left corner readout. Format: `LLM::<value>` or `LLM::—` when undefined. */
  llmLabel?: string
  /** Top-right corner readout. Format: `STT::<value>` or `STT::—`. */
  sttLabel?: string
  /** Bottom-left corner readout. Format: `TTS::<value>` or `TTS::—`. */
  ttsLabel?: string
  /** Bottom-right corner readout. Format: `SESSIONS::<count>` (defaults to 0). */
  sessions?: number
}

// ---- Layout constants -----------------------------------------------------
// All ring sizes are computed from window width so the orb scales sensibly
// across iPhone SE (320px) through to iPad (834+). The Mac HUD pegs the
// outermost ring at 460px on a 1400px window (~33%); the phone uses 75% of
// width so the orb fills the focal area.

/** Ring radii as a fraction of the smaller screen dimension. */
const RING_FRACTIONS = {
  particles: 0.375, // outermost particle ring (~75% of width diameter)
  dashed: 0.32,
  arcs: 0.27,
  ticks: 0.21,
  sphere: 0.075, // ~15% of width diameter
} as const

/** Number of particles on the outermost ring. Matches Mac density. */
const PARTICLE_COUNT = 60
/** Particle dot radius in SVG units. */
const PARTICLE_RADIUS = 1.6
/** Number of tick marks on the tick ring; every 3rd is a major tick. */
const TICK_COUNT = 12

/**
 * Rotation periods (ms per full revolution) per ring per state.
 * Inner rings rotate faster than outer rings to mimic the Mac HUD's depth
 * effect. Sign of the speed = direction (handled via two-tier easing).
 */
const RING_PERIODS: Record<OrbState, {
  particles: number
  dashed: number
  arcs: number
  ticks: number
}> = {
  idle: {
    particles: 40_000,
    dashed: 25_000,
    arcs: 18_000,
    ticks: 30_000,
  },
  listening: {
    particles: 20_000,
    dashed: 12_500,
    arcs: 9_000,
    ticks: 15_000,
  },
  speaking: {
    // Base speeds; outermost particle ring period is further modulated by
    // audioLevel in the audio-reactive effect below.
    particles: 12_000,
    dashed: 8_000,
    arcs: 6_000,
    ticks: 10_000,
  },
}

/** Sphere base opacity per state. */
const SPHERE_OPACITY: Record<OrbState, number> = {
  idle: 0.3,
  listening: 0.7,
  speaking: 0.85,
}

/** Sphere base scale per state. Speaking adds an audio-level pulse on top. */
const SPHERE_BASE_SCALE: Record<OrbState, number> = {
  idle: 1.0,
  listening: 1.05,
  speaking: 1.0,
}

/** Particle/ring composition opacity per state. */
const RING_OPACITY: Record<OrbState, number> = {
  idle: 0.55,
  listening: 0.85,
  speaking: 1.0,
}

/** Dashed-ring scale pulse target when in listening state. */
const DASHED_LISTEN_PULSE = 1.04

// ---- Component ------------------------------------------------------------

export function OrbView({
  state,
  audioLevel = 0,
  llmLabel,
  sttLabel,
  ttsLabel,
  sessions = 0,
}: OrbViewProps): React.ReactElement {
  // ---- Responsive layout ------------------------------------------------
  // useWindowDimensions re-renders on rotation; we lock the orb to the
  // smaller of width/height so portrait & landscape both fit.
  const { width, height } = useWindowDimensions()
  const base = Math.min(width, height)

  const radii = useMemo(() => ({
    particles: base * RING_FRACTIONS.particles,
    dashed: base * RING_FRACTIONS.dashed,
    arcs: base * RING_FRACTIONS.arcs,
    ticks: base * RING_FRACTIONS.ticks,
    sphere: base * RING_FRACTIONS.sphere,
  }), [base])

  // SVG canvas size = 2 * outermost radius + padding for stroke/particles.
  const svgSize = useMemo(
    () => (radii.particles + PARTICLE_RADIUS * 4) * 2,
    [radii.particles],
  )
  const center = svgSize / 2

  // ---- Pre-computed geometry --------------------------------------------
  // Memo-ed: each only depends on its ring radius. Avoids recomputing on
  // every animation frame (animation values live in shared values; only
  // transform style updates per frame).

  const particles = useMemo(() => {
    return Array.from({ length: PARTICLE_COUNT }, (_, i) => {
      const angle = (i / PARTICLE_COUNT) * Math.PI * 2
      return {
        key: i,
        cx: center + Math.cos(angle) * radii.particles,
        cy: center + Math.sin(angle) * radii.particles,
      }
    })
  }, [center, radii.particles])

  /** Circumference-based dash pattern for the dashed middle ring. */
  const dashedStroke = useMemo(() => {
    const circumference = 2 * Math.PI * radii.dashed
    // 24 dashes around the circle, roughly equal dash/gap.
    const segment = circumference / 48
    return `${segment} ${segment}`
  }, [radii.dashed])

  /**
   * Four quarter-arc paths with 16-degree gaps at the cardinal points.
   * Mirrors HudRings's "Inner data ring with segment gaps" SVG block.
   */
  const arcPaths = useMemo(() => {
    const r = radii.arcs
    return [0, 1, 2, 3].map((i) => {
      const startAngle = i * 90 + 8
      const endAngle = (i + 1) * 90 - 8
      const startRad = (startAngle / 180) * Math.PI
      const endRad = (endAngle / 180) * Math.PI
      const x1 = center + Math.cos(startRad) * r
      const y1 = center + Math.sin(startRad) * r
      const x2 = center + Math.cos(endRad) * r
      const y2 = center + Math.sin(endRad) * r
      return {
        key: i,
        d: `M ${x1} ${y1} A ${r} ${r} 0 0 1 ${x2} ${y2}`,
      }
    })
  }, [center, radii.arcs])

  /** 12 tick marks; every 3rd is "major" (longer + brighter). */
  const ticks = useMemo(() => {
    const r1 = radii.ticks
    return Array.from({ length: TICK_COUNT }, (_, i) => {
      const angle = (i / TICK_COUNT) * Math.PI * 2
      const isMajor = i % 3 === 0
      const r2 = isMajor ? r1 + 14 : r1 + 8
      return {
        key: i,
        x1: center + Math.cos(angle) * r1,
        y1: center + Math.sin(angle) * r1,
        x2: center + Math.cos(angle) * r2,
        y2: center + Math.sin(angle) * r2,
        isMajor,
      }
    })
  }, [center, radii.ticks])

  // ---- Animation shared values -----------------------------------------
  // One shared value per rotating ring + sphere opacity/scale + global
  // ring-opacity envelope + dashed-ring pulse. Each animation runs on the
  // UI thread; React never re-renders the tree at animation frequency.
  const rotParticles = useSharedValue(0)
  const rotDashed = useSharedValue(0)
  const rotArcs = useSharedValue(0)
  const rotTicks = useSharedValue(0)
  const sphereScale = useSharedValue(SPHERE_BASE_SCALE[state])
  const sphereOpacity = useSharedValue(SPHERE_OPACITY[state])
  const ringOpacity = useSharedValue(RING_OPACITY[state])
  const dashedPulse = useSharedValue(1)

  // ---- Restart rotations on state change -------------------------------
  // Reanimated's withRepeat doesn't accept dynamic durations, so we cancel
  // + restart each ring whenever state flips. We also reset rotation values
  // to 0 to keep them in their normal 0..360 range and avoid float drift.
  useEffect(() => {
    const periods = RING_PERIODS[state]

    const restart = (
      sv: typeof rotParticles,
      durationMs: number,
      direction: 1 | -1,
    ): void => {
      cancelAnimation(sv)
      sv.value = 0
      sv.value = withRepeat(
        withTiming(360 * direction, {
          duration: durationMs,
          easing: Easing.linear,
        }),
        -1, // infinite
        false, // no reverse -- always one direction
      )
    }

    // Directions mirror the Mac HUD: particles counter-clockwise, dashed
    // clockwise, arcs counter-clockwise, ticks clockwise.
    restart(rotParticles, periods.particles, -1)
    restart(rotDashed, periods.dashed, 1)
    restart(rotArcs, periods.arcs, -1)
    restart(rotTicks, periods.ticks, 1)

    sphereOpacity.value = withTiming(SPHERE_OPACITY[state], { duration: 300 })
    ringOpacity.value = withTiming(RING_OPACITY[state], { duration: 300 })

    // Dashed-ring pulse: 1.0 <-> 1.04, only in listening state.
    cancelAnimation(dashedPulse)
    if (state === 'listening') {
      dashedPulse.value = withRepeat(
        withTiming(DASHED_LISTEN_PULSE, {
          duration: 900,
          easing: Easing.inOut(Easing.ease),
        }),
        -1,
        true, // reverse -- pulse up + down
      )
    } else {
      dashedPulse.value = withTiming(1, { duration: 250 })
    }

    return () => {
      cancelAnimation(rotParticles)
      cancelAnimation(rotDashed)
      cancelAnimation(rotArcs)
      cancelAnimation(rotTicks)
      cancelAnimation(dashedPulse)
    }
  }, [
    state,
    rotParticles,
    rotDashed,
    rotArcs,
    rotTicks,
    sphereOpacity,
    ringOpacity,
    dashedPulse,
  ])

  // ---- Audio-level reactivity (speaking state only) --------------------
  // audioLevel updates at ~30Hz from the WS frame loop. We map it to:
  //   - sphereScale: 1.0 + audioLevel * 0.15 (visible peaks at audioLevel >= 0.5)
  //   - particle-ring speed: at audioLevel=1, period halves (rings spin 2x faster).
  // The particle-ring speed coupling is done by adjusting only that one
  // shared value's repeat duration; we re-create the withRepeat on each
  // significant audioLevel change. We debounce by only restarting when the
  // level moves more than 0.1 from the last restart point (handled via a
  // ref-like sharedValue tracker).
  useEffect(() => {
    const clamped = Math.max(0, Math.min(1, audioLevel))

    if (state === 'speaking') {
      sphereScale.value = withTiming(
        SPHERE_BASE_SCALE[state] + clamped * 0.15,
        { duration: 80, easing: Easing.out(Easing.quad) },
      )
    } else {
      sphereScale.value = withTiming(SPHERE_BASE_SCALE[state], {
        duration: 200,
      })
    }
  }, [audioLevel, state, sphereScale])

  // Re-coupling the particle ring's rotation period to audioLevel. We
  // sample audioLevel and only restart the rotation when it crosses one of
  // a few discrete bands (0, 0.33, 0.66, 1.0). This keeps the visual change
  // perceptible without thrashing the animation driver every WS frame.
  useEffect(() => {
    if (state !== 'speaking') return

    const clamped = Math.max(0, Math.min(1, audioLevel))
    // Map audioLevel -> rotation period multiplier in [0.5, 1.0].
    // audioLevel=0 -> 1.0x base period; audioLevel=1 -> 0.5x (2x speed).
    const multiplier = 1 - clamped * 0.5
    const period = RING_PERIODS.speaking.particles * multiplier

    cancelAnimation(rotParticles)
    rotParticles.value = withRepeat(
      withTiming(-360, {
        duration: Math.max(2_000, period),
        easing: Easing.linear,
      }),
      -1,
      false,
    )
  }, [audioLevel, state, rotParticles])

  // ---- Animated styles --------------------------------------------------
  // One per rotating layer + sphere. Each is a worklet that reads only its
  // own shared value, so updates are O(1) per frame.

  const particleRingStyle = useAnimatedStyle(() => ({
    transform: [{ rotateZ: `${rotParticles.value}deg` }],
    opacity: ringOpacity.value,
  }))

  const dashedRingStyle = useAnimatedStyle(() => ({
    transform: [
      { rotateZ: `${rotDashed.value}deg` },
      { scale: dashedPulse.value },
    ],
    opacity: ringOpacity.value,
  }))

  const arcRingStyle = useAnimatedStyle(() => ({
    transform: [{ rotateZ: `${rotArcs.value}deg` }],
    opacity: ringOpacity.value,
  }))

  const tickRingStyle = useAnimatedStyle(() => ({
    transform: [{ rotateZ: `${rotTicks.value}deg` }],
    opacity: ringOpacity.value,
  }))

  const sphereStyle = useAnimatedStyle(() => ({
    transform: [{ scale: sphereScale.value }],
    opacity: sphereOpacity.value,
  }))

  // ---- Render -----------------------------------------------------------

  // Strokes use the cyanDark (low-alpha cyan) for outer rings and the full
  // cyan for the dotted particles + sphere outline, mirroring the Mac
  // HUD's depth gradient (outer rings dimmer, focal sphere brighter).

  return (
    <View style={styles.container} testID="orb-view">
      <View style={styles.center} pointerEvents="none">
        {/* --- Ring 1: Particle ring (outermost) ---------------------- */}
        <Animated.View
          style={[
            styles.layer,
            { width: svgSize, height: svgSize },
            particleRingStyle,
          ]}
        >
          <Svg width={svgSize} height={svgSize}>
            {particles.map((p) => (
              <Circle
                key={p.key}
                cx={p.cx}
                cy={p.cy}
                r={PARTICLE_RADIUS}
                fill={colors.cyan}
              />
            ))}
          </Svg>
        </Animated.View>

        {/* --- Ring 2: Dashed ring (middle) --------------------------- */}
        <Animated.View
          style={[
            styles.layer,
            { width: svgSize, height: svgSize },
            dashedRingStyle,
          ]}
        >
          <Svg width={svgSize} height={svgSize}>
            <Circle
              cx={center}
              cy={center}
              r={radii.dashed}
              fill="none"
              stroke={colors.cyan}
              strokeOpacity={0.6}
              strokeWidth={1}
              strokeDasharray={dashedStroke}
            />
          </Svg>
        </Animated.View>

        {/* --- Ring 3: Arc-segments ring ------------------------------ */}
        <Animated.View
          style={[
            styles.layer,
            { width: svgSize, height: svgSize },
            arcRingStyle,
          ]}
        >
          <Svg width={svgSize} height={svgSize}>
            {arcPaths.map((p) => (
              <Path
                key={p.key}
                d={p.d}
                fill="none"
                stroke={colors.cyan}
                strokeOpacity={0.45}
                strokeWidth={1.5}
              />
            ))}
          </Svg>
        </Animated.View>

        {/* --- Ring 4: Tick-mark ring (innermost ring layer) ---------- */}
        <Animated.View
          style={[
            styles.layer,
            { width: svgSize, height: svgSize },
            tickRingStyle,
          ]}
        >
          <Svg width={svgSize} height={svgSize}>
            {ticks.map((t) => (
              <Line
                key={t.key}
                x1={t.x1}
                y1={t.y1}
                x2={t.x2}
                y2={t.y2}
                stroke={colors.cyan}
                strokeOpacity={t.isMajor ? 0.7 : 0.25}
                strokeWidth={t.isMajor ? 1.5 : 0.75}
              />
            ))}
          </Svg>
        </Animated.View>

        {/* --- Central sphere ----------------------------------------- */}
        <Animated.View
          style={[
            styles.layer,
            { width: radii.sphere * 2, height: radii.sphere * 2 },
            sphereStyle,
          ]}
        >
          <Svg width={radii.sphere * 2} height={radii.sphere * 2}>
            {/* Outer bright outline */}
            <Circle
              cx={radii.sphere}
              cy={radii.sphere}
              r={radii.sphere - 2}
              fill="none"
              stroke={colors.cyan}
              strokeWidth={1.5}
            />
            {/* Mid-radius softer ring -- fakes a radial gradient on plain
                react-native-svg by stacking decreasing-alpha circles. */}
            <Circle
              cx={radii.sphere}
              cy={radii.sphere}
              r={radii.sphere * 0.7}
              fill={colors.cyanDark}
              stroke={colors.cyan}
              strokeOpacity={0.35}
              strokeWidth={1}
            />
            {/* Inner core -- subtle bright spot */}
            <Circle
              cx={radii.sphere}
              cy={radii.sphere}
              r={radii.sphere * 0.35}
              fill={colors.cyan}
              fillOpacity={0.15}
            />
          </Svg>
        </Animated.View>
      </View>

      {/* Corner labels -- 4 absolutely-positioned readouts */}
      <CornerLabel
        position="top-left"
        prefix="LLM"
        value={llmLabel}
        testID="orb-label-llm"
      />
      <CornerLabel
        position="top-right"
        prefix="STT"
        value={sttLabel}
        testID="orb-label-stt"
      />
      <CornerLabel
        position="bottom-left"
        prefix="TTS"
        value={ttsLabel}
        testID="orb-label-tts"
      />
      <CornerLabel
        position="bottom-right"
        prefix="SESSIONS"
        value={String(sessions)}
        testID="orb-label-sessions"
      />
    </View>
  )
}

// ---- Corner label sub-component ------------------------------------------
// Split out so the four labels can share format ("PREFIX::VALUE") and the
// "—" fallback without copy-paste. Each corner is absolutely positioned at
// the orb container's edge.

type CornerPosition = 'top-left' | 'top-right' | 'bottom-left' | 'bottom-right'

interface CornerLabelProps {
  position: CornerPosition
  prefix: string
  value: string | undefined
  testID: string
}

function CornerLabel({
  position,
  prefix,
  value,
  testID,
}: CornerLabelProps): React.ReactElement {
  // The dim cyan colour for the prefix matches the Mac HUD's text-dim
  // (rgba(0,255,204,0.5)). The value uses the full-bright cyan. Shadows in
  // SVG are expensive on Android, so we skip them on the phone.
  const positionStyle = positionStyles[position]
  // Em-dash fallback when prop is undefined or whitespace-only. This
  // matches the Mac HUD behaviour at JarvisHudView.tsx:961 where the
  // pipeline status hook emits '—' before the first event.
  const displayValue = value && value.trim() !== '' ? value : '—'

  return (
    <View style={[styles.cornerLabel, positionStyle]} testID={testID}>
      <Text style={styles.cornerPrefix}>{prefix}::</Text>
      <Text style={styles.cornerValue} testID={`${testID}-value`}>
        {displayValue}
      </Text>
    </View>
  )
}

// ---- Styles ---------------------------------------------------------------

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: colors.bg,
    position: 'relative',
    overflow: 'hidden',
  },
  center: {
    ...StyleSheet.absoluteFillObject,
    alignItems: 'center',
    justifyContent: 'center',
  },
  /** Every concentric ring + the sphere share the same absolute-center layout. */
  layer: {
    position: 'absolute',
    alignItems: 'center',
    justifyContent: 'center',
  },
  cornerLabel: {
    position: 'absolute',
    flexDirection: 'row',
    alignItems: 'baseline',
  },
  cornerPrefix: {
    color: colors.textDim,
    fontFamily: fontFamilies.mono,
    fontSize: 10,
    letterSpacing: 1.5,
    opacity: 0.6,
  },
  cornerValue: {
    color: colors.cyan,
    fontFamily: fontFamilies.mono,
    fontSize: 10,
    letterSpacing: 1.5,
    opacity: 1,
  },
})

const positionStyles: Record<
  CornerPosition,
  { top?: number; bottom?: number; left?: number; right?: number }
> = {
  'top-left': { top: spacing.xl + spacing.md, left: spacing.lg },
  'top-right': { top: spacing.xl + spacing.md, right: spacing.lg },
  'bottom-left': { bottom: spacing.xl + spacing.md, left: spacing.lg },
  'bottom-right': { bottom: spacing.xl + spacing.md, right: spacing.lg },
}
