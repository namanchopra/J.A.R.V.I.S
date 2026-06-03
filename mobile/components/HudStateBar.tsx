// ---------------------------------------------------------------------------
// HudStateBar -- Friday mobile port of the Mac HUD's voice activity strip.
// ---------------------------------------------------------------------------
// Source-of-truth aesthetic: frontend/src/components/hud/HudVoiceBar.tsx
// (a 48pt full-width strip with a cyan dot + monospace label). Friday only
// needs the *state pill* portion -- the transcript echo lives in
// TranscriptChip.tsx so we can place it at the bottom of the screen instead
// of cramming everything into one bar.
//
// The four states drive one of four left-side indicator visuals, each
// implemented as a reanimated worklet (no setState animation loops):
//   idle       -- single dim cyan dot, static.
//   listening  -- single cyan dot pulsing on a 1s cycle.
//   thinking   -- three dots animating in a typing-indicator wave.
//   speaking   -- four short vertical bars pulsing at staggered phases.
//
// All animations are driven from `useSharedValue` + `withRepeat`, so the JS
// thread is free even while audio decode/STT bursts happen elsewhere.
// ---------------------------------------------------------------------------

import { useEffect } from 'react'
import type { ReactElement } from 'react'
import { StyleSheet, Text, View } from 'react-native'
import Animated, {
  Easing,
  cancelAnimation,
  useAnimatedStyle,
  useSharedValue,
  withRepeat,
  withTiming,
} from 'react-native-reanimated'

import { colors, fontFamilies, spacing } from '../lib/hud-tokens'

// ---- Public types ---------------------------------------------------------

/** Four voice-cycle states driven by the daemon. */
export type HudState = 'idle' | 'listening' | 'thinking' | 'speaking'

export interface HudStateBarProps {
  state: HudState
}

// ---- Constants ------------------------------------------------------------

const LABEL: Record<HudState, string> = {
  idle: 'IDLE',
  listening: 'LISTENING',
  thinking: 'THINKING',
  speaking: 'SPEAKING',
}

// ---- Indicator: ListeningDot -- single pulsing cyan dot ------------------

function ListeningDot(): ReactElement {
  const opacity = useSharedValue(0.4)

  useEffect(() => {
    // 1s cycle: fade 0.4 -> 1.0 -> 0.4 via withRepeat reverse.
    opacity.value = withRepeat(
      withTiming(1, { duration: 500, easing: Easing.inOut(Easing.ease) }),
      -1,
      true,
    )
    return () => {
      cancelAnimation(opacity)
    }
  }, [opacity])

  const animatedStyle = useAnimatedStyle(() => ({
    opacity: opacity.value,
  }))

  return (
    <Animated.View
      testID="hud-state-indicator-listening"
      style={[styles.dot, styles.dotActive, animatedStyle]}
    />
  )
}

// ---- Indicator: ThinkingDots -- 3-dot typing wave ------------------------

function ThinkingDots(): ReactElement {
  // Three shared values, each offset by 200ms so the wave reads as
  // left-to-right travel across the trio.
  const d0 = useSharedValue(0.3)
  const d1 = useSharedValue(0.3)
  const d2 = useSharedValue(0.3)

  useEffect(() => {
    const wave = (sv: typeof d0, delayMs: number): void => {
      // Burn the delay by chaining a withTiming(start) followed by the
      // repeating pulse. We hold at the start opacity during the delay.
      sv.value = withTiming(0.3, { duration: delayMs }, () => {
        sv.value = withRepeat(
          withTiming(1, { duration: 400, easing: Easing.inOut(Easing.ease) }),
          -1,
          true,
        )
      })
    }
    wave(d0, 0)
    wave(d1, 200)
    wave(d2, 400)
    return () => {
      cancelAnimation(d0)
      cancelAnimation(d1)
      cancelAnimation(d2)
    }
  }, [d0, d1, d2])

  const s0 = useAnimatedStyle(() => ({ opacity: d0.value }))
  const s1 = useAnimatedStyle(() => ({ opacity: d1.value }))
  const s2 = useAnimatedStyle(() => ({ opacity: d2.value }))

  return (
    <View
      testID="hud-state-indicator-thinking"
      style={styles.thinkingRow}
    >
      <Animated.View style={[styles.dot, styles.dotActive, s0]} />
      <Animated.View style={[styles.dot, styles.dotActive, s1]} />
      <Animated.View style={[styles.dot, styles.dotActive, s2]} />
    </View>
  )
}

// ---- Indicator: SpeakingBars -- 4 vertical bars pulsing in phases --------

function SpeakingBars(): ReactElement {
  const b0 = useSharedValue(0.4)
  const b1 = useSharedValue(0.6)
  const b2 = useSharedValue(0.5)
  const b3 = useSharedValue(0.7)

  useEffect(() => {
    const pulse = (
      sv: typeof b0,
      durationMs: number,
      delayMs: number,
    ): void => {
      sv.value = withTiming(sv.value, { duration: delayMs }, () => {
        sv.value = withRepeat(
          withTiming(1, {
            duration: durationMs,
            easing: Easing.inOut(Easing.ease),
          }),
          -1,
          true,
        )
      })
    }
    // Slightly different periods + start delays so the bars never sync up,
    // which would otherwise look like one bar repeated four times.
    pulse(b0, 320, 0)
    pulse(b1, 420, 90)
    pulse(b2, 380, 60)
    pulse(b3, 460, 180)
    return () => {
      cancelAnimation(b0)
      cancelAnimation(b1)
      cancelAnimation(b2)
      cancelAnimation(b3)
    }
  }, [b0, b1, b2, b3])

  // Each bar's vertical scale + opacity follow the same shared value so the
  // bar appears to "grow" rather than just dim.
  const sty0 = useAnimatedStyle(() => ({
    opacity: b0.value,
    transform: [{ scaleY: 0.4 + b0.value * 0.8 }],
  }))
  const sty1 = useAnimatedStyle(() => ({
    opacity: b1.value,
    transform: [{ scaleY: 0.4 + b1.value * 0.8 }],
  }))
  const sty2 = useAnimatedStyle(() => ({
    opacity: b2.value,
    transform: [{ scaleY: 0.4 + b2.value * 0.8 }],
  }))
  const sty3 = useAnimatedStyle(() => ({
    opacity: b3.value,
    transform: [{ scaleY: 0.4 + b3.value * 0.8 }],
  }))

  return (
    <View
      testID="hud-state-indicator-speaking"
      style={styles.speakingRow}
    >
      <Animated.View style={[styles.bar, sty0]} />
      <Animated.View style={[styles.bar, sty1]} />
      <Animated.View style={[styles.bar, sty2]} />
      <Animated.View style={[styles.bar, sty3]} />
    </View>
  )
}

// ---- Indicator: IdleDot -- static dim dot --------------------------------

function IdleDot(): ReactElement {
  return (
    <View
      testID="hud-state-indicator-idle"
      style={[styles.dot, styles.dotIdle]}
    />
  )
}

// ---- Component ------------------------------------------------------------

export function HudStateBar({ state }: HudStateBarProps): ReactElement {
  let indicator: ReactElement
  switch (state) {
    case 'listening':
      indicator = <ListeningDot />
      break
    case 'thinking':
      indicator = <ThinkingDots />
      break
    case 'speaking':
      indicator = <SpeakingBars />
      break
    case 'idle':
    default:
      indicator = <IdleDot />
      break
  }

  return (
    <View testID="hud-state-bar" style={styles.container}>
      <View style={styles.indicatorSlot}>{indicator}</View>
      <Text testID="hud-state-bar-label" style={styles.label}>
        {LABEL[state]}
      </Text>
      {/* Right slot mirrors the indicator slot width so the label stays
          visually centered in the bar. */}
      <View style={styles.indicatorSlot} />
    </View>
  )
}

// ---- Styles ---------------------------------------------------------------

const INDICATOR_SLOT_WIDTH = 48

const styles = StyleSheet.create({
  container: {
    height: 36,
    width: '100%',
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: spacing.md,
    backgroundColor: colors.bgPanel,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.border,
  },
  indicatorSlot: {
    width: INDICATOR_SLOT_WIDTH,
    height: 24,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'flex-start',
  },
  label: {
    fontFamily: fontFamilies.monoBold,
    fontSize: 11,
    letterSpacing: 2,
    color: colors.cyan,
    textAlign: 'center',
  },
  // Dots ---------------------------------------------------------------
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    marginRight: 4,
  },
  dotActive: {
    backgroundColor: colors.cyan,
  },
  dotIdle: {
    backgroundColor: colors.cyanDim,
  },
  thinkingRow: {
    flexDirection: 'row',
    alignItems: 'center',
  },
  // Speaking bars ------------------------------------------------------
  speakingRow: {
    flexDirection: 'row',
    alignItems: 'center',
    height: 16,
  },
  bar: {
    width: 3,
    height: 16,
    marginRight: 3,
    backgroundColor: colors.cyan,
    borderRadius: 1,
  },
})
