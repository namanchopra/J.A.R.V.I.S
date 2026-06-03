// ---------------------------------------------------------------------------
// TranscriptView -- TASK-024, v0.3.0 P2.
// ---------------------------------------------------------------------------
// Renders the last N transcript turns (default 6) as fade-in chat bubbles
// below the orb. User turns align right, assistant turns align left -- the
// same convention the Mac HUD uses so the mobile screen feels like the same
// product. Subscribes to `transcript` events from JarvisWS and self-evicts
// older bubbles past `maxTurns` so the screen stays bounded.
//
// Why an event subscription instead of pulling turns from a store?
//   1. JarvisWS is already the single source of truth on the wire; adding a
//      duplicate cache (Zustand, Context) would make us reason about which
//      copy is canonical when the socket reconnects mid-utterance.
//   2. Transcript bubbles are *ephemeral UI*. There's no persistence
//      requirement -- if the user kills the app the turns go away, matching
//      Siri/Alexa expectations. Local component state is the right scope.
//
// Why react-native-reanimated `FadeIn` / `FadeOut`?
//   - Already a project dependency (used by OrbView), so no new package.
//   - Runs on the UI thread via worklets -- bubble appearance stays smooth
//     even when the JS thread is busy decoding TTS chunks (TASK-026).
//   - 200ms feels snappy without being abrupt; we picked it to match the
//     Mac HUD's `.hud-fade-in` animation duration.
//
// Bubble id strategy: timestamp + 6-char random suffix. Two transcript
// events in the same millisecond would otherwise collide on Date.now()
// alone and React would warn about duplicate keys. The suffix is cheap
// (Math.random + base36 slice) and only needs intra-session uniqueness.
// ---------------------------------------------------------------------------

import { useEffect, useState } from 'react'
import type { ReactElement } from 'react'
import { ScrollView, StyleSheet, Text } from 'react-native'
import Animated, { FadeIn, FadeOut } from 'react-native-reanimated'

import { colors, fontFamilies, spacing } from '../lib/hud-tokens'
import type { JarvisWS } from '../lib/jarvis-ws'

// ---- Types ---------------------------------------------------------------

/**
 * A single rendered turn. `id` is generated locally because the WS protocol
 * doesn't carry a turn id; we only need uniqueness for React's key prop.
 */
interface TranscriptTurn {
  id: string
  role: 'user' | 'assistant'
  text: string
}

/**
 * Minimal slice of JarvisWS this component needs. We deliberately do NOT
 * type this as `JarvisWS` directly because it makes the component much
 * easier to test (the mock only needs to implement `.on('transcript', ...)`)
 * and removes a class-import coupling. Production code passes a real
 * `JarvisWS`, which satisfies this structural type.
 */
export interface TranscriptWSLike {
  on: JarvisWS['on']
}

export interface TranscriptViewProps {
  /** WebSocket-like source of `transcript` events. */
  ws: TranscriptWSLike
  /**
   * Maximum number of bubbles kept on screen. Older turns are evicted
   * FIFO. Defaults to 6 -- matches the HUD's 600px viewport budget on
   * iPhone 14 mini at 13pt mono.
   */
  maxTurns?: number
}

// ---- Component ------------------------------------------------------------

export function TranscriptView({
  ws,
  maxTurns = 6,
}: TranscriptViewProps): ReactElement {
  const [turns, setTurns] = useState<TranscriptTurn[]>([])

  useEffect(() => {
    // `ws.on` returns a disposer; returning it directly from useEffect
    // wires React's cleanup to the subscription teardown. If `ws` ever
    // changes identity (e.g. a hot reload swaps the singleton) the old
    // listener is removed before the new one is registered.
    return ws.on('transcript', (payload) => {
      setTurns((prev) => {
        const id = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
        const next = [
          ...prev,
          { id, role: payload.role, text: payload.text },
        ]
        // Cap to the most recent `maxTurns`. We slice the tail rather than
        // shifting one-at-a-time so a burst of N events (which can happen
        // during a fast LLM reply broken into sentences) settles in a
        // single render.
        if (next.length > maxTurns) {
          return next.slice(-maxTurns)
        }
        return next
      })
    })
  }, [ws, maxTurns])

  return (
    <ScrollView
      testID="transcript-view"
      style={styles.container}
      contentContainerStyle={styles.content}
      showsVerticalScrollIndicator={false}
    >
      {turns.map((turn) => (
        <Animated.View
          key={turn.id}
          testID={`transcript-bubble-${turn.role}`}
          entering={FadeIn.duration(200)}
          exiting={FadeOut.duration(200)}
          style={[
            styles.bubble,
            turn.role === 'user' ? styles.userBubble : styles.assistantBubble,
          ]}
        >
          <Text style={styles.role}>
            {turn.role === 'user' ? 'YOU::' : 'JARVIS::'}
          </Text>
          <Text style={styles.bubbleText}>{turn.text}</Text>
        </Animated.View>
      ))}
    </ScrollView>
  )
}

// ---- Styles --------------------------------------------------------------
// Colors / spacing pulled from hud-tokens so the bubble look stays in sync
// with the rest of the HUD when those tokens move. `maxWidth: '85%'` keeps
// long sentences from spanning the whole screen and helps the user/assistant
// alignment read as a conversation.

const styles = StyleSheet.create({
  container: {
    flex: 1,
    paddingHorizontal: spacing.lg,
  },
  content: {
    paddingVertical: spacing.lg,
    alignItems: 'stretch',
  },
  bubble: {
    marginVertical: spacing.xs,
    padding: spacing.md,
    borderWidth: 1,
    borderColor: colors.cyanDark,
    borderRadius: 4,
    maxWidth: '85%',
  },
  // Right-aligned user bubble, slightly darker fill so it reads as "outgoing".
  userBubble: {
    alignSelf: 'flex-end',
    backgroundColor: 'rgba(0,255,204,0.05)',
  },
  // Left-aligned assistant bubble, slightly brighter so Jarvis's voice
  // visually leads the conversation.
  assistantBubble: {
    alignSelf: 'flex-start',
    backgroundColor: 'rgba(0,255,204,0.10)',
  },
  role: {
    fontFamily: fontFamilies.mono,
    fontSize: 9,
    color: colors.cyan,
    opacity: 0.6,
    letterSpacing: 1.5,
    marginBottom: 4,
  },
  bubbleText: {
    fontFamily: fontFamilies.mono,
    fontSize: 13,
    color: colors.textPrimary,
    lineHeight: 18,
  },
})
