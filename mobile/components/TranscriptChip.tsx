// ---------------------------------------------------------------------------
// TranscriptChip -- Friday mobile bottom-of-screen rolling transcript chip.
// ---------------------------------------------------------------------------
// Source-of-truth aesthetic: frontend/src/components/JarvisHudView.tsx's
// HudBracket panel (corner-bracketed cyan box with a label prefix) combined
// with the subtitle layer logic in HudVoiceBar.tsx. On Mac we have two stacked
// layers (live transcript + jarvis response). On a phone screen there's only
// room for one chip at a time, so we display whichever of (userText,
// assistantText) is the *most recently changed* and cross-fade between them
// with reanimated `withTiming`.
//
// Cross-fade is implemented with two absolutely-positioned text layers and a
// pair of shared opacity values. When a new value arrives we fade the old
// layer out (200ms) and the new one in (200ms). No setState animation loops.
//
// The component returns `null` when both props are null, so Agent A can
// always mount it -- it'll simply disappear when there's nothing to show.
// ---------------------------------------------------------------------------

import { useEffect, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import { Pressable, StyleSheet, Text, View } from 'react-native'
import Animated, {
  useAnimatedStyle,
  useSharedValue,
  withTiming,
  type SharedValue,
} from 'react-native-reanimated'

import { colors, fontFamilies, spacing } from '../lib/hud-tokens'

// ---- Public types ---------------------------------------------------------

export interface TranscriptChipProps {
  /** Most recent user transcript. */
  userText?: string | null
  /** Most recent assistant response. */
  assistantText?: string | null
  /**
   * Optional tap handler. Agent A may wire this to expand the chip into a
   * full transcript view; the chip itself doesn't render any expanded state.
   */
  onPress?: () => void
}

// ---- Internal display model -----------------------------------------------

/** Which role's text is currently visible. */
type Role = 'user' | 'assistant'

interface DisplayState {
  /** The text to render. */
  text: string
  /** Drives the YOU > vs JARVIS > prefix. */
  role: Role
}

// ---- Constants ------------------------------------------------------------

const FADE_DURATION_MS = 200

// ---- Helpers --------------------------------------------------------------

/**
 * Decide which of (userText, assistantText) should be displayed.
 *
 * Strategy: whichever value *changed* most recently wins. We track the last
 * seen string for each role via a ref and compare against incoming props.
 * If both changed in the same render (rare; only on cold mount) we prefer
 * the assistant text since it's the "reply" the user wants to see.
 *
 * Returns null when neither role has any text to show.
 */
function pickRole(
  userText: string | null | undefined,
  assistantText: string | null | undefined,
  prevUser: string | null,
  prevAssistant: string | null,
): DisplayState | null {
  const userChanged = (userText ?? null) !== prevUser
  const assistantChanged = (assistantText ?? null) !== prevAssistant

  if (assistantChanged && assistantText) {
    return { role: 'assistant', text: assistantText }
  }
  if (userChanged && userText) {
    return { role: 'user', text: userText }
  }
  // Nothing changed -- but the *current* display should remain whatever was
  // previously shown; the caller handles that. Returning null here means
  // "no new selection".
  return null
}

// ---- Component ------------------------------------------------------------

export function TranscriptChip({
  userText,
  assistantText,
  onPress,
}: TranscriptChipProps): ReactElement | null {
  // Track the last props we observed so we can detect "what changed" between
  // renders. Initial value is `null` (never seen) so the first non-null
  // prop is always treated as a change.
  const prevUserRef = useRef<string | null>(null)
  const prevAssistantRef = useRef<string | null>(null)

  // The currently visible chip. `null` means render nothing.
  const [current, setCurrent] = useState<DisplayState | null>(null)
  // The outgoing chip during a crossfade. Mounted only when fading out.
  const [outgoing, setOutgoing] = useState<DisplayState | null>(null)

  // Two opacity shared values -- one per layer. We swap which one is fading
  // up vs down on every change.
  const currentOpacity = useSharedValue(0)
  const outgoingOpacity = useSharedValue(0)

  useEffect(() => {
    const next = pickRole(
      userText,
      assistantText,
      prevUserRef.current,
      prevAssistantRef.current,
    )
    prevUserRef.current = userText ?? null
    prevAssistantRef.current = assistantText ?? null

    if (next === null) {
      // No new selection. If we already have a current chip, leave it alone.
      // If neither prop has ever been set, current stays null and we render
      // nothing.
      return
    }

    // Crossfade: previous current becomes outgoing, new value becomes current.
    if (current !== null) {
      setOutgoing(current)
      outgoingOpacity.value = 1
      outgoingOpacity.value = withTiming(0, { duration: FADE_DURATION_MS })
    }
    setCurrent(next)
    currentOpacity.value = 0
    currentOpacity.value = withTiming(1, { duration: FADE_DURATION_MS })
    // We intentionally exclude `current` from the deps -- it's updated inside
    // this effect and including it would cause a render loop. We only want to
    // react to prop changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userText, assistantText])

  // If neither prop has ever been non-null, render nothing.
  if (current === null) {
    return null
  }

  return (
    <Pressable
      testID="transcript-chip"
      onPress={onPress}
      disabled={!onPress}
      hitSlop={8}
      style={styles.container}
    >
      <View style={styles.bracket}>
        {/* Corner brackets -- 4 L-shaped corners, ported from Mac HudBracket. */}
        <View style={[styles.corner, styles.cornerTL]} />
        <View style={[styles.corner, styles.cornerTR]} />
        <View style={[styles.corner, styles.cornerBL]} />
        <View style={[styles.corner, styles.cornerBR]} />

        {/* Current (incoming) text layer */}
        <ChipLayer
          state={current}
          opacity={currentOpacity}
          testID="transcript-chip-current"
        />

        {/* Outgoing (fading) text layer -- absolutely positioned over the
            current layer so the two visually cross. Only mounted while a
            fade-out is in progress. */}
        {outgoing !== null && (
          <ChipLayer
            state={outgoing}
            opacity={outgoingOpacity}
            absolute
            testID="transcript-chip-outgoing"
          />
        )}
      </View>
    </Pressable>
  )
}

// ---- ChipLayer -- a single text row (prefix + body) ----------------------

interface ChipLayerProps {
  state: DisplayState
  // Reanimated v4 no longer namespaces SharedValue under Animated; import it
  // as a top-level type. This keeps the prop typed end-to-end without
  // resorting to `any`.
  opacity: SharedValue<number>
  /** When true, position absolutely so it stacks on top of the sibling. */
  absolute?: boolean
  testID?: string
}

function ChipLayer({
  state,
  opacity,
  absolute,
  testID,
}: ChipLayerProps): ReactElement {
  const animatedStyle = useAnimatedStyle(() => ({
    opacity: opacity.value,
  }))

  const prefix = state.role === 'user' ? 'YOU >' : 'JARVIS >'
  const prefixStyle =
    state.role === 'user' ? styles.prefixUser : styles.prefixAssistant

  return (
    <Animated.View
      testID={testID}
      style={[styles.layer, absolute ? styles.layerAbsolute : null, animatedStyle]}
      pointerEvents="none"
    >
      <Text style={[styles.prefix, prefixStyle]}>{prefix}</Text>
      <Text style={styles.body} numberOfLines={2} ellipsizeMode="tail">
        {state.text}
      </Text>
    </Animated.View>
  )
}

// ---- Styles ---------------------------------------------------------------

const CORNER_SIZE = 10
const CORNER_THICKNESS = 1

const styles = StyleSheet.create({
  container: {
    marginHorizontal: spacing.lg,
  },
  bracket: {
    position: 'relative',
    paddingVertical: spacing.sm,
    paddingHorizontal: spacing.md,
    backgroundColor: colors.bgPanel,
    borderRadius: 2,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
  },
  // Inner layer -- the row containing prefix + body text.
  layer: {
    flexDirection: 'row',
    alignItems: 'flex-start',
  },
  layerAbsolute: {
    position: 'absolute',
    top: spacing.sm,
    left: spacing.md,
    right: spacing.md,
    bottom: spacing.sm,
  },
  prefix: {
    fontFamily: fontFamilies.monoBold,
    fontSize: 10,
    letterSpacing: 1.5,
    marginRight: spacing.sm,
    paddingTop: 2,
  },
  prefixUser: {
    color: colors.textDim,
  },
  prefixAssistant: {
    color: colors.cyanBright,
  },
  body: {
    flex: 1,
    fontFamily: fontFamilies.mono,
    fontSize: 12,
    lineHeight: 16,
    color: colors.textPrimary,
  },
  // Corner brackets ----------------------------------------------------
  corner: {
    position: 'absolute',
    width: CORNER_SIZE,
    height: CORNER_SIZE,
    borderColor: colors.cyan,
  },
  cornerTL: {
    top: 0,
    left: 0,
    borderTopWidth: CORNER_THICKNESS,
    borderLeftWidth: CORNER_THICKNESS,
  },
  cornerTR: {
    top: 0,
    right: 0,
    borderTopWidth: CORNER_THICKNESS,
    borderRightWidth: CORNER_THICKNESS,
  },
  cornerBL: {
    bottom: 0,
    left: 0,
    borderBottomWidth: CORNER_THICKNESS,
    borderLeftWidth: CORNER_THICKNESS,
  },
  cornerBR: {
    bottom: 0,
    right: 0,
    borderBottomWidth: CORNER_THICKNESS,
    borderRightWidth: CORNER_THICKNESS,
  },
})
