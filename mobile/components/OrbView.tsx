// ---------------------------------------------------------------------------
// OrbView -- React Native port of the Mac HUD's JarvisHudView centerpiece.
// ---------------------------------------------------------------------------
// Source-of-truth aesthetic: frontend/src/components/JarvisHudView.tsx, lines
// ~332-505 (HudRings) and ~944-998 (Orb + corner labels).
//
// The Mac HUD uses ~6 concentric rotating div+svg rings around a sphere. For
// the phone, we collapse that into a single particle ring + a central sphere
// outline. The cyan/dark palette + monospace corner labels carry the visual
// identity; the multi-ring complexity is unnecessary on a 6" screen.
//
// Rendering approach: react-native-svg for the GPU-friendly particle Circles,
// react-native-reanimated v4 for rotation + scale + opacity transforms. We
// avoided @shopify/react-native-skia because Friday targets Expo Go (TASK-026),
// and Skia requires a native dev build.
// ---------------------------------------------------------------------------

import { useEffect, useMemo } from 'react'
import { Dimensions, StyleSheet, Text, View } from 'react-native'
import Animated, {
  Easing,
  cancelAnimation,
  useAnimatedStyle,
  useSharedValue,
  withRepeat,
  withTiming,
} from 'react-native-reanimated'
import Svg, { Circle } from 'react-native-svg'

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
// All sizes are computed from the window width so the orb scales sensibly
// from a small iPhone SE to an iPad. Mac HUD has the orb at 460px on a
// ~1400px window (~33% width). Friday targets ~80% of the window so the orb
// reads as the focal point.

const PARTICLE_COUNT = 60
const PARTICLE_RADIUS = 1.6 // SVG units (px)

/** Rotation period (ms) per state. Lower = faster. */
const ROTATION_PERIOD_MS: Record<OrbState, number> = {
  idle: 60_000, // 1 revolution / 60s
  listening: 15_000, // 1 revolution / 15s
  speaking: 8_000, // 1 revolution / 8s (driven by audio level on top)
}

/** Sphere base opacity per state. Speaking adds an audio-level boost on top. */
const SPHERE_OPACITY: Record<OrbState, number> = {
  idle: 0.4,
  listening: 0.6,
  speaking: 0.8,
}

/** Sphere base scale per state. Speaking adds an audio-level pulse on top. */
const SPHERE_BASE_SCALE: Record<OrbState, number> = {
  idle: 1.0,
  listening: 1.05,
  speaking: 1.0,
}

/** Particle ring opacity per state. */
const RING_OPACITY: Record<OrbState, number> = {
  idle: 0.5,
  listening: 0.8,
  speaking: 1.0,
}

// ---- Component ------------------------------------------------------------

export function OrbView({
  state,
  audioLevel = 0,
  llmLabel,
  sttLabel,
  ttsLabel,
  sessions = 0,
}: OrbViewProps): React.ReactElement {
  // ---- Layout (recomputed only on prop change / mount) ------------------
  // We capture Dimensions once; Friday is portrait-locked (TASK-005) so
  // window-size changes are rare. If we ever support landscape we should
  // wrap this in useWindowDimensions().
  const window = Dimensions.get('window')
  const ringRadius = window.width * 0.4 // ~140px on a typical phone
  const sphereRadius = ringRadius * 0.55 // ~80px on a typical phone
  const svgSize = (ringRadius + PARTICLE_RADIUS * 4) * 2 // padded for stroke

  // Pre-compute the (cx, cy) of each particle. Memo-ed so we don't recompute
  // on every render -- particle positions only depend on ringRadius.
  const particles = useMemo(() => {
    const center = svgSize / 2
    return Array.from({ length: PARTICLE_COUNT }, (_, i) => {
      const angle = (i / PARTICLE_COUNT) * Math.PI * 2
      return {
        key: i,
        cx: center + Math.cos(angle) * ringRadius,
        cy: center + Math.sin(angle) * ringRadius,
      }
    })
  }, [ringRadius, svgSize])

  // ---- Animation shared values -----------------------------------------
  // rotation: drives the ring's <Animated.View> rotateZ transform.
  // sphereScale: pulses based on audioLevel when state==='speaking'.
  // sphereOpacity: dim/bright sweep on state change.
  const rotation = useSharedValue(0)
  const sphereScale = useSharedValue(SPHERE_BASE_SCALE[state])
  const sphereOpacity = useSharedValue(SPHERE_OPACITY[state])
  const ringOpacity = useSharedValue(RING_OPACITY[state])

  // ---- Restart rotation on state change -------------------------------
  // We cancel the existing animation and kick off a new one with the new
  // period. Reanimated's withRepeat doesn't accept dynamic durations, so a
  // cancel + restart is the cleanest path.
  useEffect(() => {
    cancelAnimation(rotation)
    rotation.value = 0
    rotation.value = withRepeat(
      withTiming(360, {
        duration: ROTATION_PERIOD_MS[state],
        easing: Easing.linear,
      }),
      -1, // infinite
      false, // do not reverse -- always clockwise
    )

    sphereOpacity.value = withTiming(SPHERE_OPACITY[state], { duration: 300 })
    ringOpacity.value = withTiming(RING_OPACITY[state], { duration: 300 })

    return () => {
      cancelAnimation(rotation)
    }
  }, [state, rotation, sphereOpacity, ringOpacity])

  // ---- Audio-level-driven sphere pulse (speaking state) ---------------
  // Clamp audioLevel to [0, 1] then map to a 0..0.15 scale boost. We do
  // this on the JS thread because audioLevel is updated at ~30Hz by the
  // daemon, low enough that the bridge overhead is negligible.
  useEffect(() => {
    const clamped = Math.max(0, Math.min(1, audioLevel))
    if (state === 'speaking') {
      sphereScale.value = withTiming(
        SPHERE_BASE_SCALE[state] + clamped * 0.15,
        { duration: 80 },
      )
    } else {
      sphereScale.value = withTiming(SPHERE_BASE_SCALE[state], { duration: 200 })
    }
  }, [audioLevel, state, sphereScale])

  // ---- Animated styles --------------------------------------------------
  const ringAnimatedStyle = useAnimatedStyle(() => ({
    transform: [{ rotateZ: `${rotation.value}deg` }],
    opacity: ringOpacity.value,
  }))

  const sphereAnimatedStyle = useAnimatedStyle(() => ({
    transform: [{ scale: sphereScale.value }],
    opacity: sphereOpacity.value,
  }))

  // ---- Render -----------------------------------------------------------

  return (
    <View style={styles.container} testID="orb-view">
      {/* Center stack: ring (animated rotation) + sphere (animated scale) */}
      <View style={styles.center} pointerEvents="none">
        <Animated.View
          style={[
            styles.ring,
            { width: svgSize, height: svgSize },
            ringAnimatedStyle,
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

        <Animated.View
          style={[
            styles.sphere,
            {
              width: sphereRadius * 2,
              height: sphereRadius * 2,
            },
            sphereAnimatedStyle,
          ]}
        >
          <Svg width={sphereRadius * 2} height={sphereRadius * 2}>
            <Circle
              cx={sphereRadius}
              cy={sphereRadius}
              r={sphereRadius - 2}
              fill="none"
              stroke={colors.cyan}
              strokeWidth={1.5}
            />
            {/* Inner faint glow ring -- mirrors the Mac HUD's inset shadow.
                A second outline at half-alpha gives the sphere depth without
                requiring a real radial gradient (not yet available in plain
                react-native-svg without defs). */}
            <Circle
              cx={sphereRadius}
              cy={sphereRadius}
              r={sphereRadius * 0.7}
              fill="none"
              stroke={colors.cyanDark}
              strokeWidth={1}
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
  // (rgba(0,255,204,0.5)). The value uses the full-bright cyan with a
  // shadow-like accent via the cyan colour alone -- shadows in SVG are
  // expensive on Android, so we skip them on the phone.
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
  ring: {
    position: 'absolute',
  },
  sphere: {
    position: 'absolute',
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
