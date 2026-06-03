// ---------------------------------------------------------------------------
// SessionBadge -- compact top-left tap target showing Claude session count.
// ---------------------------------------------------------------------------
// Source-of-truth aesthetic: the corner-bracketed labels around the orb in
// frontend/src/components/JarvisHudView.tsx (~line 251 -- HudBracket) and the
// SESSIONS::N corner label in mobile/components/OrbView.tsx. On Mac the full
// session list lives in HudSessionPanel; on a phone screen we condense it to
// a single tappable badge and let Agent A wire the tap to open a session
// drawer in a follow-up task.
//
// The corner brackets are rendered with four absolutely-positioned `View`s
// rather than SVG -- the L-shape is just two borders on each View, which is
// cheaper than mounting a Svg root for what amounts to eight straight lines.
//
// The "pending approvals" alert is a small notification-style dot in the
// top-right corner. It uses `colors.red` (the existing HUD red token) rather
// than amber to match Apple's notification-badge convention.
// ---------------------------------------------------------------------------

import type { ReactElement } from 'react'
import { Pressable, StyleSheet, Text, View } from 'react-native'

import { colors, fontFamilies } from '../lib/hud-tokens'

// ---- Public types ---------------------------------------------------------

export interface SessionBadgeProps {
  /** Number of active Claude Code sessions. */
  count: number
  /** True when at least one session has a pending y/n approval prompt. */
  hasApprovals: boolean
  /** Optional tap handler -- fires on press. */
  onPress?: () => void
}

// ---- Component ------------------------------------------------------------

export function SessionBadge({
  count,
  hasApprovals,
  onPress,
}: SessionBadgeProps): ReactElement {
  // Guard against negative/NaN counts -- the daemon should never send them
  // but a stale snapshot could. We coerce to a non-negative integer for the
  // displayed value rather than blowing up the badge layout.
  const safeCount = Number.isFinite(count) && count >= 0 ? Math.floor(count) : 0

  return (
    <Pressable
      testID="session-badge"
      onPress={onPress}
      disabled={!onPress}
      hitSlop={12}
      accessibilityRole="button"
      accessibilityLabel={
        hasApprovals
          ? `Sessions ${safeCount}, approvals pending`
          : `Sessions ${safeCount}`
      }
      style={styles.container}
    >
      {/* Corner brackets -- 4 L-shaped views, same pattern as TranscriptChip
          and the Mac HudBracket helper. */}
      <View style={[styles.corner, styles.cornerTL]} />
      <View style={[styles.corner, styles.cornerTR]} />
      <View style={[styles.corner, styles.cornerBL]} />
      <View style={[styles.corner, styles.cornerBR]} />

      <Text testID="session-badge-label" style={styles.label}>
        {`SESSIONS::${safeCount}`}
      </Text>

      {hasApprovals && (
        // The dot is purely decorative -- the parent's accessibilityLabel
        // already announces approvals state to VoiceOver/TalkBack.
        <View testID="session-badge-alert-dot" style={styles.alertDot} />
      )}
    </Pressable>
  )
}

// ---- Styles ---------------------------------------------------------------

const CORNER_SIZE = 8
const CORNER_THICKNESS = 1
const ALERT_DOT_SIZE = 8

const styles = StyleSheet.create({
  container: {
    minWidth: 72,
    height: 32,
    paddingHorizontal: 10,
    backgroundColor: colors.bgPanel,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
    borderRadius: 2,
    alignItems: 'center',
    justifyContent: 'center',
    position: 'relative',
  },
  label: {
    fontFamily: fontFamilies.monoBold,
    fontSize: 10,
    letterSpacing: 1.5,
    color: colors.cyan,
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
  // Notification-style alert dot --------------------------------------
  alertDot: {
    position: 'absolute',
    // Slight outward offset so the dot reads as a notification badge
    // sitting on the corner of the card, not contained within it.
    top: -3,
    right: -3,
    width: ALERT_DOT_SIZE,
    height: ALERT_DOT_SIZE,
    borderRadius: ALERT_DOT_SIZE / 2,
    backgroundColor: colors.red,
  },
})
